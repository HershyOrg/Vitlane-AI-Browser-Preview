import { ProcessRequestPending, ProcessRequestRejected, getProcessReceipt, type ProcessReceipt } from "../infra/orderProcessApi";
import { ProcessReceiptNotice } from "./OrderProcessProgress";
import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { CircleAlert, ExternalLink, LockKeyhole, MessageSquareText, PackageCheck } from "lucide-react";
import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Disclosure, FeedbackState, Field, Input, NativeSelect, NativeSelectOption, Notice, PageHeader, Textarea, Chip } from "../../../shared/ui";
import { textConstraint } from "../../../shared/forms/textConstraint";
import {
  localizeFixedCopy,
  invariantContent,
  useLocale,
  type Localize,
} from "../../../shared/i18n";
import {
  beginProcurementMerchantEffect,
  claimProcurementTask,
  completeProcurementTask,
  confirmShipmentDelivered,
  createProcurementCustomerRequest,
  createShipment,
  failProcurementTask,
  getProcurementManualReview,
  listOrderShipments,
  recordProcurementManualDecision,
  recordShipmentEvent,
  revealProcurementContinueURL,
  revealProcurementShipping,
  resolveProcurementCustomerRequest,
  type OperatorShipmentView,
  type OperatorShippingAddress,
  type OrderAccountingProjection,
  type PaymentReconciliationItem,
  type ProcurementCustomerRequest,
  type ProcurementManualDecision,
  type ProcurementQueueItem,
} from "../infra/agencyOrderOperatorApi";
import { sendSupportOrderMessage } from "../../support/infra/supportOperatorApi";
import { OrderAccountingPanel } from "./OrderAccountingPanel";
import { PaymentModeBadge } from "./PaymentModeBadge";
import { useOperatorSurface, type OperatorSurface } from "../app/useOperatorSurface";
import { fulfillmentLabel, merchantOrderOperationalStageLabel, terminalReasonLabel } from "./unitStageCopy";
import { getCurrentUser } from "../../account/infra/accountApi";
import { OperatorOrderLookup } from "./OperatorOrderLookup";
import "./agency-order.css";

type OrderScope = "MINE" | "AVAILABLE" | "OTHERS" | "ALL";
type OrderStage = "PROCUREMENT" | "LOGISTICS" | "ISSUE" | "DONE" | "ALL";
type ExecutionScope = "ALL" | "SANDBOX_TEST" | "LIVE";

// 서버 canonical MO projection의 통과 함수다. Web은 owner 상태나 주문 전체
// processState를 조합해 MO 단계를 다시 판정하지 않는다.
export function stageOf(item: ProcurementQueueItem): OrderStage {
  return item.operational.workStage;
}

// 주문 보조줄(2축 고지) — MO 사실과 주문 사실이 다를 때만 말한다. 같은 주문의
// 다른 행 존재는 surface에서 파생한다(권위는 process — 표시 전용).
export function orderContextCopy(
  item: ProcurementQueueItem,
  siblings: ProcurementQueueItem[],
  l: Localize = localizeFixedCopy,
): string | undefined {
  const stage = item.operational.workStage;
  if (stage === "DONE" && item.processState !== "TERMINAL") {
    const othersOpen = siblings.some((other) =>
      other.task.id !== item.task.id && stageOf(other) !== "DONE");
    return othersOpen
      ? l("Order: another shop is in progress · not settled", "주문: 다른 Shop 진행 중 · 정산 전")
      : l("Order: awaiting settlement", "주문: 정산 대기");
  }
  if (stage !== "DONE" && item.processState === "TERMINAL") {
    return l(
      "Order: {reason}",
      "주문: {reason}",
      { reason: terminalReasonLabel(item.terminalReason, l) ?? l("Closed", "종결") },
    );
  }
  if (stage !== "ISSUE" && siblings.some((other) =>
    other.task.id !== item.task.id && other.operational.workStage === "ISSUE")) {
    return l(
      "Order summary: another shop checkout has an exception; this MerchantOrder continues independently.",
      "주문 요약: 다른 Shop 결제 단위에 예외가 있으며, 이 MerchantOrder는 독립적으로 계속됩니다.",
    );
  }
  return undefined;
}

// 예외 뱃지는 정확한 MO에만 붙인다. 주문 단위 intervention은 주문 요약에서만
// 보이며 형제 MO의 상태나 행동을 바꾸지 않는다.
export function exceptionCountsByMerchantOrder(surface: OperatorSurface) {
  const map: Record<string, { refund: number; delivery: number; returns: number; intervention: number }> = {};
  const bump = (merchantOrderID: string, key: "refund" | "delivery") => {
    map[merchantOrderID] = map[merchantOrderID] ?? { refund: 0, delivery: 0, returns: 0, intervention: 0 };
    map[merchantOrderID][key] += 1;
  };
  for (const request of surface.refunds) bump(request.merchantOrderId, "refund");
  for (const unit of surface.exceptions) bump(unit.merchantOrderId, "delivery");
  return map;
}

// 구매대행 주문 처리(ADR-0052·0057) — 담당 단위는 MerchantOrder(Shop-checkout)
// Task이고, 화면은 담당 축(내 담당/담당 가능/다른 담당/전체) × stage 축
// (조달/배송/예외/완료)이다. 앞의 세 범위는 활성 작업의 완전 분할이다.
// 맨 왼쪽 탭이 기본이다(2차 P5). 예외 처리는 별도 페이지가 소유한다.
export function AgencyOrderOperatorPage() {
  const { l } = useLocale();
  const { surface, load, run, can, working, actionError, actionReceipt, receiptRefreshFailed } = useOperatorSurface();
  const [scope, setScope] = useState<OrderScope>("MINE");
  const [stage, setStage] = useState<OrderStage>("PROCUREMENT");
  const [executionScope, setExecutionScope] = useState<ExecutionScope>("ALL");
  const [operatorUserID, setOperatorUserID] = useState<string>();
  const [addresses, setAddresses] = useState<Record<string, OperatorShippingAddress>>({});
  const [continueURLs, setContinueURLs] = useState<Record<string, string>>({});
  const [continueGone, setContinueGone] = useState<Record<string, boolean>>({});
  const [reasonDetails, setReasonDetails] = useState<Record<string, string>>({});
  const [noticeDrafts, setNoticeDrafts] = useState<Record<string, string>>({});
  const [noticeImages, setNoticeImages] = useState<Record<string, File[]>>({});

  useEffect(() => {
    let active = true;
    getCurrentUser()
      .then(({ user }) => { if (active) setOperatorUserID(user.id); })
      .catch(() => undefined);
    return () => { active = false; };
  }, []);

  useEffect(() => {
    const timer = window.setInterval(() => void load(), 5_000);
    return () => window.clearInterval(timer);
  }, [load]);

  const items = surface.procurement;
  const paymentItems = surface.paymentReconciliations.filter((item) => {
    if (executionScope === "ALL") return true;
    return executionScope === "LIVE"
      ? item.providerEnvironment === "LIVE"
      : item.providerEnvironment !== "LIVE";
  });
  const exceptionsByMerchantOrder = exceptionCountsByMerchantOrder(surface);

	const matchesScope = (item: ProcurementQueueItem, value: OrderScope) => {
		if (value === "ALL") return true;
		if (value === "MINE") {
			return item.assignmentState === "ACTIVE" && Boolean(operatorUserID) &&
				item.task.assignedOperatorUserId === operatorUserID;
		}
		if (value === "AVAILABLE") return item.assignmentState === "UNASSIGNED" || item.assignmentState === "EXPIRED";
		return item.assignmentState === "ACTIVE" && Boolean(item.task.assignedOperatorUserId) &&
			item.task.assignedOperatorUserId !== operatorUserID;
  };
  const matchesStage = (item: ProcurementQueueItem, value: OrderStage) => {
    if (value === "ALL") return true;
    return stageOf(item) === value;
  };
  const matchesExecution = (item: ProcurementQueueItem, value: ExecutionScope) => {
    if (value === "ALL") return true;
    const live = item.merchantOrder.executionMode === "LIVE_MERCHANT_EFFECT";
    return value === "LIVE" ? live : !live;
  };
  const scopedItems = items.filter((item) => matchesScope(item, scope));
  const executionItems = scopedItems.filter((item) => matchesExecution(item, executionScope));
  const visibleItems = executionItems.filter((item) => matchesStage(item, stage));
  const scopeCount = (value: OrderScope) => items.filter((item) => matchesScope(item, value)).length;
  const executionCount = (value: ExecutionScope) => scopedItems.filter((item) => matchesExecution(item, value)).length +
    surface.paymentReconciliations.filter((item) => value === "ALL" ||
      (value === "LIVE" ? item.providerEnvironment === "LIVE" : item.providerEnvironment !== "LIVE")).length;
  const stageCount = (value: OrderStage) => executionItems.filter((item) => matchesStage(item, value)).length +
    (value === "ISSUE" || value === "ALL" ? paymentItems.length : 0);
  const visiblePaymentItems = stage === "ISSUE" || stage === "ALL" ? paymentItems : [];

	const scopes = [
		["MINE", l("Assigned to me", "내 담당")],
		["AVAILABLE", l("Available", "담당 가능")],
		["OTHERS", l("Assigned to others", "다른 담당")],
		["ALL", l("All", "전체")],
  ] as const;
  // stage 축은 완전 분할이다(3차 #6): 조달+배송+예외+완료 = 전체. 예외 탭은
  // 존재를 세는 read-only 열람이고 행동은 예외 처리 페이지가 소유한다.
  const stages = [
    ["PROCUREMENT", l("Procurement", "조달")],
    ["LOGISTICS", l("Shipping", "배송")],
    ["ISSUE", l("Exceptions", "예외")],
    ["DONE", l("Complete", "완료")],
    ["ALL", l("All", "전체")],
  ] as const;
  const executionScopes = [
    ["ALL", l("All modes", "전체 모드")],
    ["SANDBOX_TEST", l("Sandbox / TEST", "Sandbox / TEST")],
    ["LIVE", l("Live", "Live")],
  ] as const;

  return <main className="order-ui-operator-console agency-order-operator">
    <PageHeader
      eyebrow={l("Procurement Ops", "Procurement Ops")}
      title={l("Order processing", "주문 처리")}
	      description={l(
	        "Funding-ready orders arrive as one task per shop checkout. Sandbox and Live use the same authorization → MO activation → tracking → delivery workflow; Sandbox keeps provider and merchant effects in test mode. Refunds, decisions, and returns are owned by Exceptions.",
	        "자금 준비가 끝난 주문이 Shop-checkout 단위 Task로 전달됩니다. Sandbox·Live는 승인 → MO 활성화 → 운송장 → 수령 워크플로를 함께 쓰며, Sandbox에서는 provider·merchant effect를 테스트 모드로 유지합니다. 환불·판정·회수는 예외 처리 페이지가 소유합니다.",
      )}
      secondaryActions={[{ label: l("Refresh now", "지금 갱신"), onClick: () => void load() }]}
		summary={<div className="order-ui-page-header-summary">
			<span>{l("Assigned to me", "내 담당")} <strong>{scopeCount("MINE")}</strong></span>
			<span>{l("Available", "담당 가능")} <strong>{scopeCount("AVAILABLE")}</strong></span>
			<span>{l("Assigned to others", "다른 담당")} <strong>{scopeCount("OTHERS")}</strong></span>
			<span>{l("Active total", "활성 전체")} <strong>{scopeCount("MINE") + scopeCount("AVAILABLE") + scopeCount("OTHERS")}</strong></span>
			<span>{l("Assignment complete", "담당 완료")} <strong>{items.filter((item) => item.assignmentState === "COMPLETED").length}</strong></span>
      </div>}
    />
    <OperatorOrderLookup />
    {surface.error ? <Notice announce tone="danger">{surface.error}</Notice> : null}
    {actionError ? <Notice announce tone="danger">{actionError}</Notice> : null}
    {actionReceipt ? <ProcessReceiptNotice receipt={actionReceipt} operator /> : null}
    {receiptRefreshFailed ? <Notice tone="warning">{l("Progress could not be refreshed. Your saved request is still being processed.", "진행 상황을 갱신하지 못했습니다. 저장된 요청은 계속 처리됩니다.")}</Notice> : null}
    {paymentItems.length > 0 && stage !== "ISSUE" && stage !== "ALL" ? (
      <Notice announce tone="warning" title={l(
        "PayPal payments need reconciliation",
        "PayPal 결제 대사가 필요합니다",
      )}>
        {l(
	          "{count} order(s) are paused before procurement. No merchant task is created until the PayPal authorization is verified.",
	          "주문 {count}건이 조달 전에 일시 중지되었습니다. PayPal 승인이 검증되기 전에는 merchant 작업이 생성되지 않습니다.",
          { count: paymentItems.length },
        )}{" "}
        <Button emphasis="quiet" onClick={() => setStage("ISSUE")} size="compact" type="button">
          {l("Review payments", "결제 검토")}
        </Button>
      </Notice>
    ) : null}
    <div aria-label={l("Assignment scope", "담당 범위")} className="workspace-history-views" role="group">{scopes.map(([value, label]) => <Button aria-pressed={scope === value} emphasis="quiet" key={value} onClick={() => setScope(value)} size="compact" type="button">{label} <span>{scopeCount(value)}</span></Button>)}</div>
    <div aria-label={l("Execution mode", "실행 모드")} className="workspace-history-views" role="group">{executionScopes.map(([value, label]) => <Button aria-pressed={executionScope === value} emphasis="quiet" key={value} onClick={() => setExecutionScope(value)} size="compact" type="button">{label} <span>{executionCount(value)}</span></Button>)}</div>
    <div aria-label={l("Processing stage", "처리 단계")} className="workspace-history-views" role="group">{stages.map(([value, label]) => <Button aria-pressed={stage === value} emphasis="quiet" key={value} onClick={() => setStage(value)} size="compact" type="button">{label} <span>{stageCount(value)}</span></Button>)}</div>
    <section className="settlement-ui-order-list" aria-label={l("Shop-checkout processing list", "Shop-checkout 처리 목록")}>
      {visiblePaymentItems.map((item) => <PaymentReconciliationCard item={item} key={item.paymentId} />)}
      {visibleItems.map((item) => <OrderCard
        addresses={addresses} can={can} continueGone={continueGone} continueURLs={continueURLs}
        exceptions={exceptionsByMerchantOrder[item.merchantOrder.id]}
        accounting={surface.accountingByOrder[item.task.agencyOrderId]}
        item={item} key={item.task.id} noticeDrafts={noticeDrafts} noticeImages={noticeImages}
        orderContext={orderContextCopy(item, items.filter((other) => other.task.agencyOrderId === item.task.agencyOrderId), l)}
        operatorUserID={operatorUserID} reasonDetails={reasonDetails} run={run}
        setAddresses={setAddresses} setContinueGone={setContinueGone} setContinueURLs={setContinueURLs}
        setNoticeDrafts={setNoticeDrafts} setNoticeImages={setNoticeImages}
        setReasonDetails={setReasonDetails} working={working}
      />)}
      {visibleItems.length + visiblePaymentItems.length === 0 ? <FeedbackState description={stage === "ISSUE"
        ? l("No orders are in an exception process state. Pending refund and decision requests are on the Exceptions page.", "process가 예외 국면인 주문이 없습니다. 심사 대기 중인 환불·판정 요청은 예외 처리 페이지에 있습니다.")
        : l("Choose another assignment scope or stage, or wait for the next paid order.", "다른 담당 범위·단계를 선택하거나 다음 수납 확정 주문을 기다려 주세요.")} state="empty" title={l("No processing items match this view", "이 보기에 해당하는 처리 항목이 없습니다")} /> : null}
    </section>
  </main>;
}

function PaymentReconciliationCard({ item }: { item: PaymentReconciliationItem }) {
  const { l } = useLocale();
  return <article className="agency-order-payment-reconciliation">
    <header>
      <span className={`agency-order-payment-reconciliation__mode is-${item.providerEnvironment.toLowerCase()}`}>
        {item.providerEnvironment}
      </span>
      <div>
		<strong>{l("PayPal authorization result is still unknown", "PayPal 승인 결과 확인 중")}</strong>
        <small>{item.reasonCode || item.attemptState}</small>
      </div>
      <strong>{formatMoney({ amountMinor: item.amountMinor, currency: item.currency })}</strong>
    </header>
    <Notice tone="warning">
      <CircleAlert aria-hidden="true" />{" "}
      {l(
		"Do not start merchant purchasing or ask the customer to authorize again. Vitlane is rechecking the stored PayPal Order; Procurement opens only after the authorization is verified.",
		"merchant 구매를 시작하거나 고객에게 재승인을 요청하지 마세요. Vitlane이 저장된 PayPal Order를 재조회하며, 승인이 검증된 뒤에만 Procurement가 열립니다.",
      )}
    </Notice>
    <dl>
      <div><dt>{l("AgencyOrder", "AgencyOrder")}</dt><dd><Link to={`/agencyOrder/${item.agencyOrderId}`}>{item.agencyOrderId}</Link></dd></div>
      <div><dt>{l("PayPal Order", "PayPal Order")}</dt><dd>{item.paypalOrderId || "-"}</dd></div>
      <div><dt>{l("Payment", "Payment")}</dt><dd>{item.paymentId}</dd></div>
      <div><dt>{l("Last checked", "최근 확인")}</dt><dd>{new Date(item.updatedAt).toLocaleString()}</dd></div>
    </dl>
  </article>;
}

type ExceptionSummary = { refund: number; delivery: number; returns: number; intervention: number };

function exceptionNoticeCopy(summary: ExceptionSummary, l: Localize) {
  const parts: string[] = [];
  if (summary.refund > 0) parts.push(l("{count} refund reviews", "환불 심사 {count}건", { count: summary.refund }));
  if (summary.delivery > 0) parts.push(l("{count} shipping exceptions", "배송 예외 {count}건", { count: summary.delivery }));
  if (summary.returns > 0) parts.push(l("{count} returns", "회수 {count}건", { count: summary.returns }));
  if (summary.intervention > 0) parts.push(l("{count} process interventions", "process 개입 {count}건", { count: summary.intervention }));
  return parts.join(" · ");
}

function placementEvidenceSourceLabel(source: string, l: Localize) {
  return {
    OPERATOR_OBSERVATION: l("Operator observation", "운영자 관찰"),
    MERCHANT_PAGE: l("Merchant page", "판매처 페이지"),
    MERCHANT_POLICY: l("Merchant policy", "판매처 정책"),
    RECEIPT: l("Receipt", "영수증"),
    OTHER: l("Other", "기타"),
  }[source] ?? source;
}

function parseUSDToMinor(value: string): number | undefined {
  const match = /^(\d+)(?:\.(\d{1,2}))?$/.exec(value.trim());
  if (!match) return undefined;
  const minor = BigInt(match[1]) * 100n + BigInt((match[2] ?? "").padEnd(2, "0"));
  if (minor > BigInt(Number.MAX_SAFE_INTEGER)) return undefined;
  return Number(minor);
}

function validOpaquePlacementReference(value: string, maxLength: number) {
  const normalized = value.trim();
  return normalized.length >= 1 && Array.from(normalized).length <= maxLength &&
    !/[\r\n\t]/.test(normalized) && !/https?:\/\//i.test(normalized);
}

export function procurementRequestValidation(input: {
  observedCondition: string;
  publicContext: string;
  prompt: string;
  responseType: ProcurementCustomerRequest["responseType"];
  responseOptions: string[];
}) {
  const observedLength = Array.from(input.observedCondition.trim()).length;
  const contextLength = Array.from(input.publicContext.trim()).length;
  const promptLength = Array.from(input.prompt.trim()).length;
  const optionLengthsValid = input.responseOptions.every((option) => {
    const length = Array.from(option).length;
    return length >= 1 && length <= 200;
  });
  const optionsReady = input.responseType !== "SINGLE_CHOICE" || (
    input.responseOptions.length >= 2 && input.responseOptions.length <= 10 &&
    optionLengthsValid && new Set(input.responseOptions).size === input.responseOptions.length
  );
  const observedReady = observedLength >= 1 && observedLength <= 2_000;
  const contextReady = contextLength >= 8 && contextLength <= 2_000;
  const promptReady = promptLength >= 1 && promptLength <= 2_000;
  return {
    observedReady,
    contextReady,
    promptReady,
    optionsReady,
    ready: observedReady && contextReady && promptReady && optionsReady,
  };
}

// OrderCard는 서버 canonical MO stage와 owner actions를 표시하는 처리 카드다.
// Web은 processState나 owner state로 stage/action을 다시 계산하지 않는다.
function OrderCard({ item, can, working, run, operatorUserID, exceptions, accounting, orderContext, addresses, setAddresses, continueURLs, setContinueURLs, continueGone, setContinueGone, reasonDetails, setReasonDetails, noticeDrafts, setNoticeDrafts, noticeImages, setNoticeImages }: {
  item: ProcurementQueueItem;
  can: (kind: string, id: string, action: string) => boolean;
  working?: string;
  run: (key: string, action: () => Promise<void>) => Promise<void>;
  operatorUserID?: string;
  exceptions?: ExceptionSummary;
  accounting?: OrderAccountingProjection;
  orderContext?: string;
  addresses: Record<string, OperatorShippingAddress>;
  setAddresses: React.Dispatch<React.SetStateAction<Record<string, OperatorShippingAddress>>>;
  continueURLs: Record<string, string>;
  setContinueURLs: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  continueGone: Record<string, boolean>;
  setContinueGone: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  reasonDetails: Record<string, string>;
  setReasonDetails: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  noticeDrafts: Record<string, string>;
  setNoticeDrafts: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  noticeImages: Record<string, File[]>;
  setNoticeImages: React.Dispatch<React.SetStateAction<Record<string, File[]>>>;
}) {
  const { l, locale } = useLocale();
  const [externalOrderRef, setExternalOrderRef] = useState("");
  const [receiptSafeRef, setReceiptSafeRef] = useState("");
  const [actualAmountUSD, setActualAmountUSD] = useState("");
  const [placementAmountMode, setPlacementAmountMode] = useState<"UNCHANGED" | "CHANGED">("UNCHANGED");
  const [placementEvidenceSource, setPlacementEvidenceSource] = useState<
    "OPERATOR_OBSERVATION" | "MERCHANT_PAGE" | "MERCHANT_POLICY" | "RECEIPT" | "OTHER"
  >("RECEIPT");
  const [noticeImageError, setNoticeImageError] = useState("");
  // 접힌 행에서도 보이는 예외 이동 뱃지(운영정합 4차 §4 — 이전 라운드의
  // "펼쳐야 보이는 링크" 실수 교정). Disclosure summary가 버튼이라 중첩 앵커
  // 대신 stopPropagation 이동을 쓴다 — 키보드 사용자는 행을 펼치면 같은
  // 목적지의 카드 링크를 만난다.
  const navigate = useNavigate();
  const exceptionTotal = exceptions
    ? exceptions.refund + exceptions.delivery + exceptions.returns + exceptions.intervention
    : 0;
  const task = item.task;
  const order = item.merchantOrder;

  // The row disclosure belongs to the Task and must stay open while the server
  // advances that Task. Only placement-stage inputs are reset on an
  // authoritative Task/MO transition; remounting the whole card would also
  // collapse the row and hide the next required action.
  useEffect(() => {
    setExternalOrderRef("");
    setReceiptSafeRef("");
    setActualAmountUSD("");
    setPlacementAmountMode("UNCHANGED");
    setPlacementEvidenceSource("RECEIPT");
  }, [task.state, order.state]);
  const assigned = item.assignmentState === "ACTIVE";
  const mine = assigned && task.assignedOperatorUserId === operatorUserID;
  const address = addresses[task.id];
  const continueURL = continueURLs[task.id];
  // 프리필 금지(2차 P6) — 사유는 운영자가 매번 직접 쓴다(서버 최소 8자).
  const detail = reasonDetails[task.id] ?? "";
  const detailConstraint = textConstraint({ value: detail, min: 8, max: 500, l });
  const detailReady = detailConstraint.ready;
  const lines = item.agencyOrder.lines.filter((line) => line.shopDomain === order.shopDomain);
  const snapshot = order.checkoutSnapshot;
  const simulated = order.executionMode === "SIMULATED_NO_EFFECT";
	const placementAmountMinor = parseUSDToMinor(actualAmountUSD);
	const placementApprovedMinor = snapshot.authoritativeTotal?.amountMinor;
	const placementAmountValid = placementAmountMinor !== undefined &&
		placementAmountMinor > 0 && placementApprovedMinor !== undefined &&
		placementAmountMinor <= placementApprovedMinor;
	const externalOrderRefValid = validOpaquePlacementReference(externalOrderRef, 255);
	const receiptSafeRefValid = validOpaquePlacementReference(receiptSafeRef, 512);
	const placementEvidenceReady = externalOrderRefValid && receiptSafeRefValid &&
		(placementAmountMode === "UNCHANGED" ? placementApprovedMinor !== undefined && placementApprovedMinor > 0 : placementAmountValid);
	const placementApprovedLabel = placementApprovedMinor === undefined
		? l("not available", "확인 불가")
		: formatMoney({ amountMinor: placementApprovedMinor, currency: "USD" });
  // 할당된 항목에 서버가 CLAIM을 다시 열었다 = lease 만료(운영정합 5차 B1).
  // 진행 행동은 서버 actions가 이미 닫았으므로 재담당만 안내한다.
  const leaseLost = item.assignmentState === "EXPIRED";
  const itemStage = stageOf(item);
  const hasExceptions = exceptions &&
    (exceptions.refund + exceptions.delivery + exceptions.returns + exceptions.intervention) > 0;
  const checkoutTotal = snapshot.authoritativeTotal
    ? formatMoney(snapshot.authoritativeTotal)
    : l("No authoritative total", "권위 총액 없음");
  const delivery = item.delivery;
  const summaryTitle = lines[0]?.productTitle || order.shopDomain;
  return (
    <Disclosure
      className={`product-ui-operator-order state-${task.state.toLowerCase()}`}
      contentClassName="product-ui-operator-order__detail"
      summary={(
        <span className="product-ui-operator-row">
          <Chip>{stageLabel(item, l)}</Chip>
          <span className="product-ui-operator-row__identity">
            <strong>
              {lines.length > 1
                ? l("{title} and {count} more", "{title} 외 {count}건", {
                    title: summaryTitle,
                    count: lines.length - 1,
                  })
                : summaryTitle}
            </strong>
            <small>
              {order.shopDomain} · {item.assignmentState === "COMPLETED"
                ? l("Assignment complete · final operator", "담당 완료 · 최종 담당자")
                : assigned
                ? l("Assigned", "담당자 할당됨")
                : leaseLost
                  ? l("Reassignment available", "재할당 가능")
                  : l("Shared / unassigned", "공용 / 미할당")} ·{" "}
              <PaymentModeBadge includeRail selection={item.agencyOrder.paymentSelection} />
              {" · "}{simulated
                ? l("Merchant simulated", "Merchant simulated")
                : l("Merchant live", "Merchant live")}
              {exceptionTotal > 0 ? (
                <span
                  className="product-ui-operator-row__issues"
                  role="link"
                  onClick={(event) => {
                    event.stopPropagation();
                    navigate("/admin/agencyOrder/exceptions");
                  }}
                >
                  {l("Exceptions {count}", "예외 {count}", { count: exceptionTotal })}
                </span>
              ) : null}
            </small>
            {orderContext ? <small className="product-ui-operator-row__order">{orderContext}</small> : null}
          </span>
          <strong className="product-ui-operator-row__amount">{checkoutTotal}</strong>
        </span>
      )}
    >
      <article className={`settlement-ui-order-card state-${task.state.toLowerCase()}`}>
        {accounting ? <OrderAccountingPanel accounting={accounting} compact focusMerchantOrderId={order.id} /> : null}
        {itemStage === "ISSUE" ? (
          <Notice tone="danger">
            <strong>{merchantOrderOperationalStageLabel(item.operational.stage, l)}</strong>
            {l(" — exception actions are scoped to this MerchantOrder. Other shop checkouts continue independently. ", " — 예외 행동은 이 MerchantOrder에만 적용됩니다. 다른 Shop 결제 단위는 독립적으로 계속됩니다. ")}
            <Link to="/admin/agencyOrder/exceptions">{l("Go to Exceptions", "예외 처리로 이동")}</Link>
          </Notice>
        ) : null}
        {itemStage !== "ISSUE" && hasExceptions ? (
          <Notice tone="danger">
            <strong>{l("Exception requests also exist", "예외 처리 요청도 존재합니다")}</strong>
            {" — "}{exceptionNoticeCopy(exceptions, l)} ·{" "}
            <Link to="/admin/agencyOrder/exceptions">{l("Go to Exceptions", "예외 처리로 이동")}</Link>
          </Notice>
        ) : null}
        <div className="settlement-ui-order-heading">
          <div>
            <Chip>{stageLabel(item, l)}</Chip>{" "}
            <PaymentModeBadge includeRail selection={item.agencyOrder.paymentSelection} />{" "}
            <Chip mode={simulated ? "test" : "live"}>{simulated
              ? l("Merchant SIMULATED · no real order", "Merchant SIMULATED · 실제 주문 없음")
              : l("Merchant LIVE · real shop order and spend", "Merchant LIVE · 실제 Shop 주문·지출")}</Chip>
            <h2>{order.shopDomain}</h2>
            <p>{l("AgencyOrder", "AgencyOrder")} {task.agencyOrderId}</p>
          </div>
          <strong>
            {checkoutTotal}
            <small>{l("Merchant cost for this checkout · maximum authorized spend", "이 checkout의 merchant 실비 총액 · 최대 승인 지출")}</small>
          </strong>
        </div>
        <div className="agency-order-tracking__lines">
          {lines.map((line) => (
            <div key={line.lineId}>
              <strong>{line.productUrl ? <a href={line.productUrl} rel="noreferrer" target="_blank">{line.productTitle}</a> : line.productTitle}</strong>
              <span>
                {line.variantTitle} · {line.selectedOptions.join(" · ") || l("No options", "옵션 없음")} ·{" "}
                {l("{count} units", "{count}개", { count: line.quantity })} ·{" "}
                {l("Unit price", "단가")} {formatMoney(line.unitPrice)} ·{" "}
                {l("Subtotal", "소계")} {formatMoney(line.lineSubtotal)}
              </span>
            </div>
          ))}
        </div>
        <dl className="settlement-ui-quote">
          <div><dt>{l("Tax included", "세금 포함")}</dt><dd>{snapshot.taxTotal ? formatMoney(snapshot.taxTotal) : "-"}</dd></div>
          <div><dt>{l("Selected shipping", "선택 배송")}</dt><dd>{delivery?.derived ? `${delivery.title} (${formatMoney({ amountMinor: delivery.amountMinor, currency: "USD" })})` : l("Derivation failed · inspect the original snapshot", "파생 실패 — 스냅샷 원문 확인 필요")}</dd></div>
          <div><dt>{l("Shipping snapshot", "배송 스냅샷")}</dt><dd>{item.agencyOrder.shippingAddress.maskedSummary}</dd></div>
        </dl>
        {((snapshot.providerNotices?.length ?? 0) > 0 || (snapshot.manualSiteSteps?.length ?? 0) > 0) ? (
          <section className="agency-order-operator__provider-observations">
            <h3>{l("Provider observations · read-only", "Provider 관찰값 · 읽기 전용")}</h3>
            <dl className="settlement-ui-quote">
              <div><dt>{l("Provider status", "Provider 상태")}</dt><dd>{snapshot.providerStatus || "-"}</dd></div>
              <div><dt>{l("OrderSheet handling", "OrderSheet 처리")}</dt><dd>{snapshot.procurementHandling || "-"}</dd></div>
            </dl>
            {(snapshot.providerNotices ?? []).map((notice, index) => (
              <div key={`${notice.code ?? "notice"}:${index}`} className="agency-order-operator__provider-observation">
                <strong>{notice.code || notice.type || l("Provider notice", "Provider 안내")}</strong>
                <span>{notice.source === "REQUIREMENT"
                  ? (notice.registered ? l("Registered provider requirement", "등록된 provider 요구") : l("Unregistered provider requirement", "미등록 provider 요구"))
                  : (notice.registered ? l("Registered code", "등록 코드") : l("Unregistered provider code", "미등록 provider 코드"))}</span>
                {notice.severity ? <small>{l("Severity", "심각도")}: {notice.severity}</small> : null}
                {notice.safePath ? <small>{l("Path", "경로")}: {notice.safePath}</small> : null}
                {notice.text ? <p>{notice.text}</p> : null}
              </div>
            ))}
            {(snapshot.manualSiteSteps ?? []).length > 0 ? <p><strong>{l("Expected merchant-site steps", "예상 판매처 사이트 단계")}:</strong>{" "}{(snapshot.manualSiteSteps ?? []).map((step) => step.kind).join(" · ")}</p> : null}
          </section>
        ) : null}
        {order.placementEvidence ? (
          <section className={`agency-order-operator__placement-evidence ${order.placementEvidence.kind === "SANDBOX_TEST_EVIDENCE" ? "is-test" : "is-live"}`}>
            <Notice
              tone={order.placementEvidence.kind === "SANDBOX_TEST_EVIDENCE" ? "neutral" : "warning"}
              title={order.placementEvidence.kind === "SANDBOX_TEST_EVIDENCE"
                ? l("Recorded TEST purchase evidence", "기록된 TEST 구매 증거")
                : l("Recorded live purchase evidence", "기록된 Live 구매 증거")}
            >
              {order.placementEvidence.kind === "SANDBOX_TEST_EVIDENCE"
                ? l("Workflow rehearsal only — this record does not claim a real merchant purchase.", "워크플로 연습 전용 — 실제 merchant 구매를 주장하지 않는 기록입니다.")
                : l("Real merchant-effect record — use these safe references for later review and reconciliation.", "실제 merchant effect 기록 — 추후 심사·대사에 안전 참조를 사용하세요.")}
            </Notice>
            <dl className="settlement-ui-quote">
              <div><dt>{l("Merchant order safe reference", "merchant 주문 안전 참조")}</dt><dd>{order.placementEvidence.externalOrderRef}</dd></div>
              <div><dt>{l("Receipt safe reference", "영수증 안전 참조")}</dt><dd>{order.placementEvidence.receiptSafeRef}</dd></div>
              <div><dt>{l("Recorded amount", "기입 금액")}</dt><dd>{formatMoney({ amountMinor: order.placementEvidence.actualAmountMinor, currency: order.placementEvidence.currency })}</dd></div>
              <div><dt>{l("Evidence source", "증거 출처")}</dt><dd>{placementEvidenceSourceLabel(order.placementEvidence.evidenceSource, l)}</dd></div>
              <div><dt>{l("Evidence hash", "증거 해시")}</dt><dd>{order.placementEvidence.evidenceHash}</dd></div>
              <div><dt>{l("Observed", "관찰 시각")}</dt><dd>{new Date(order.placementEvidence.observedAt).toLocaleString(locale)}</dd></div>
              {order.placementEvidence.recordedByUserId ? <div><dt>{l("Recorded by", "기록 운영자")}</dt><dd>{order.placementEvidence.recordedByUserId}</dd></div> : null}
              {order.placementEvidence.recordedAt ? <div><dt>{l("Recorded", "기록 시각")}</dt><dd>{new Date(order.placementEvidence.recordedAt).toLocaleString(locale)}</dd></div> : null}
            </dl>
          </section>
        ) : null}
        <Disclosure
          className="agency-order-operator__order-summary"
          summary={<span>{l("Full order summary (reference — not the spend for this row)", "주문 전체 요약(참고 — 이 행의 지출액이 아님)")}</span>}
        >
          <dl className="settlement-ui-quote">
            <div><dt>{l("Product and shipping cost", "상품·배송 실비 합")}</dt><dd>{formatMoney(item.agencyOrder.passThroughTotal)}</dd></div>
            <div><dt>{l("Agency fee", "대행 수수료")}</dt><dd>{formatMoney(item.agencyOrder.agencyFee.total)}</dd></div>
            <div><dt>{l("Customer payment total", "고객 결제 총액")}</dt><dd>{formatMoney(item.agencyOrder.customerPayableTotal)}</dd></div>
          </dl>
        </Disclosure>
        {leaseLost ? (
          <Notice tone="danger">
            <strong>{l("The assignment lease expired.", "담당 lease가 만료되었습니다.")}</strong>{" "}
            {l("Claim it again to reveal details or record completion. Existing work is preserved.", "다시 담당해야 열람·완료 기록이 열립니다 — 진행하던 내용은 그대로 유지됩니다.")}
          </Notice>
        ) : null}
        {can("PROCUREMENT_EXECUTION", task.id, "CLAIM") ? (
          <Button emphasis="primary" busy={working === `${task.id}:claim`} onClick={() => void run(`${task.id}:claim`, async () => { await claimProcurementTask(task.id); })}>
            {leaseLost || assigned ? l("Claim again", "다시 담당하기") : l("Assign to me", "내가 맡기")}
          </Button>
        ) : null}
        {mine && !leaseLost && itemStage === "PROCUREMENT" ? (
          <>
            {!address ? <>
              <Field
                className="agency-order-operator__reason"
                error={detailConstraint.error}
                hint={`${l("Audit record reason for the reveal.", "감사 기록용 열람 사유입니다.")} ${detailConstraint.hint}`}
                id={`reveal-reason-${task.id}`}
                label={l("Reason for revealing shipping information", "배송정보 열람 사유")}
                required
              >
                <Textarea
                  maxLength={500}
                  placeholder={l("Example: Review shipping address to process the merchant order", "예: 구매대행 merchant 주문 처리를 위한 배송지 확인")}
                  value={detail}
                  onChange={(event) => setReasonDetails((current) => ({ ...current, [task.id]: event.target.value }))}
                />
              </Field>
              {can("PROCUREMENT_EXECUTION", task.id, "REVEAL_SHIPPING") ? (
                <Button emphasis="primary" disabled={!detailReady} busy={working === `${task.id}:reveal`} onClick={() => void run(`${task.id}:reveal`, async () => { const result = await revealProcurementShipping(task.id, detail.trim()); setAddresses((current) => ({ ...current, [task.id]: result.shippingAddress })); })}>
                  <LockKeyhole aria-hidden="true" /> {l("Audit and reveal shipping information", "배송정보 감사 열람")}
                </Button>
              ) : null}
            </> : (
              <Notice tone="neutral" title={l("Shipping-information audit completed", "배송정보 감사 열람 완료")}>
                {l("The access reason and shipping-snapshot reveal were recorded in the audit log.", "열람 사유와 배송 snapshot 접근을 감사 기록에 저장했습니다.")}
                <address>{address.recipientName}<br />{address.addressLine1} {address.addressLine2}<br />{address.city}, {address.region} {address.postalCode}<br />{address.country}</address>
              </Notice>
            )}
            {snapshot.continueUrlSafeRef ? (
              continueURL ? (
                <p className="agency-order-operator__continue">
                  <ExternalLink aria-hidden="true" />{" "}
                  <a href={continueURL} rel="noreferrer" target="_blank">{l("Open saved checkout link", "보관된 checkout 링크 열기")}</a>
                  <small>{l("Operational reference only — the authoritative amount is the merchant cost above.", "운용 참조 정보 — 금액 권위는 위 실비 총액입니다.")}</small>
                </p>
              ) : continueGone[task.id] ? (
                <p className="agency-order-operator__continue"><small>{l("The optional saved checkout reference is unavailable. Use the shop, product, variant, option, and quantity details above for manual purchasing.", "선택적 checkout 참조를 사용할 수 없습니다. 위 Shop·상품·variant·옵션·수량 정보로 수동 구매를 진행하세요.")}</small></p>
              ) : can("PROCUREMENT_EXECUTION", task.id, "REVEAL_CONTINUE_URL") ? (
                <Button emphasis="secondary" disabled={!detailReady} busy={working === `${task.id}:continue`} onClick={() => void run(`${task.id}:continue`, async () => {
                  try {
                    const result = await revealProcurementContinueURL(task.id, detail.trim());
                    setContinueURLs((current) => ({ ...current, [task.id]: result.continueUrl }));
                  } catch {
                    setContinueGone((current) => ({ ...current, [task.id]: true }));
                  }
                })}>
                  <ExternalLink aria-hidden="true" /> {l("Audit and reveal checkout link", "checkout 링크 감사 열람")}
                </Button>
              ) : null
            ) : null}
			<ProcurementManualReviewPanel
				addressRevealed={Boolean(address)}
				canRecordFailure={can("PROCUREMENT_EXECUTION", task.id, "RECORD_FAILURE")}
				compensationAmountMinor={accounting?.merchantOrders.find((entry) => entry.merchantOrderId === order.id)?.customerGrossMinor}
				item={item}
				key={`${task.id}:${task.state}:${order.state}`}
				run={run}
				working={working}
			/>
			{can("PROCUREMENT_EXECUTION", task.id, "RECORD_PLACED") ? (
              <section className={`agency-order-operator__placement-evidence ${simulated ? "is-test" : "is-live"}`}>
                <Notice tone={simulated ? "neutral" : "warning"} title={simulated
                  ? l("TEST purchase evidence", "TEST 구매 증거")
                  : l("Live merchant-effect evidence", "Live merchant effect 증거")}>
                  {simulated ? l(
                    "These references exercise the Live evidence workflow. They are stored as TEST evidence and never claim that a real merchant purchase occurred.",
                    "Live 증거 절차를 시험하는 참조입니다. TEST 증거로 저장되며 실제 merchant 구매가 발생했다고 주장하지 않습니다.",
                  ) : l(
                    "This record states that real money was spent at the merchant. Enter only opaque references—never credentials, full receipt payloads, or payment details.",
                    "판매처에서 실제 돈이 지출됐다는 기록입니다. credential·영수증 원문·결제정보가 아닌 opaque 참조만 입력하세요.",
                  )}
                </Notice>
                <div className="agency-order-operator__placement-evidence-fields">
                  <Field
                    error={externalOrderRef !== "" && !externalOrderRefValid ? l(
                      "Use an opaque reference of 1–255 characters without a URL, line break, or tab.",
                      "URL·줄바꿈·탭이 없는 1–255자 opaque 참조를 입력하세요.",
                    ) : undefined}
                    hint={`${l("Opaque ID only, no URL.", "opaque ID만, URL 불가.")} ${textConstraint({ value: externalOrderRef, min: 1, max: 255, l }).hint}`}
                    id={`placement-external-ref-${task.id}`}
                    label={simulated
                      ? l("TEST merchant order reference", "TEST merchant 주문 참조")
                      : l("Live merchant order reference", "Live merchant 주문 참조")}
                    required
                  >
                    <Input
                      placeholder={simulated
                        ? l("Example: TEST-ORDER-8842", "예: TEST-ORDER-8842")
                        : l("Merchant order number", "실제 주문 번호")}
                      value={externalOrderRef}
                      onChange={(event) => setExternalOrderRef(event.target.value)}
                    />
                  </Field>
                  <Field
                    error={receiptSafeRef !== "" && !receiptSafeRefValid ? l(
                      "Use an opaque reference of 1–512 characters without a URL, line break, or tab.",
                      "URL·줄바꿈·탭이 없는 1–512자 opaque 참조를 입력하세요.",
                    ) : undefined}
                    hint={`${l("Opaque ID only, no URL.", "opaque ID만, URL 불가.")} ${textConstraint({ value: receiptSafeRef, min: 1, max: 512, l }).hint}`}
                    id={`placement-receipt-ref-${task.id}`}
                    label={l("Receipt safe reference", "영수증 안전 참조")}
                    required
                  >
                    <Input
                      placeholder={l("Opaque receipt reference", "opaque 영수증 참조")}
                      value={receiptSafeRef}
                      onChange={(event) => setReceiptSafeRef(event.target.value)}
                    />
                  </Field>
                  <label>
                    <span>{l("Paid amount", "결제 금액")}</span>
                    <NativeSelect value={placementAmountMode} onChange={(event) => setPlacementAmountMode(event.target.value as "UNCHANGED" | "CHANGED")}>
                      <NativeSelectOption value="UNCHANGED">{l("Unchanged · use approved amount {maximum}", "변동 없음 · 승인액 {maximum} 사용", { maximum: placementApprovedLabel })}</NativeSelectOption>
                      <NativeSelectOption value="CHANGED">{l("Amount changed · enter actual payment", "금액 변동 · 실제 결제액 입력")}</NativeSelectOption>
                    </NativeSelect>
                  </label>
                  {placementAmountMode === "CHANGED" ? <label>
                    <span>{simulated
						? l("TEST recorded amount (USD)", "TEST 기입액(USD)")
						: l("Actual paid amount (USD)", "실제 결제액(USD)")} <small>{l(
							"(required · $0.01–{maximum} · up to 2 decimal places)",
							"(필수 · $0.01–{maximum} · 소수점 최대 2자리)",
							{ maximum: placementApprovedLabel },
						)}</small></span>
                    <Input inputMode="decimal" placeholder={l("Example: 52.23", "예: 52.23")} value={actualAmountUSD} onChange={(event) => setActualAmountUSD(event.target.value)} />
					{actualAmountUSD !== "" && !placementAmountValid ? <small role="status">{l(
						"Enter USD with up to 2 decimal places, from $0.01 through {maximum}.",
						"USD 금액을 소수점 최대 2자리로 $0.01부터 {maximum}까지 입력하세요.",
						{ maximum: placementApprovedLabel },
					)}</small> : null}
                  </label> : null}
                  <label>
                    <span>{l("Placement evidence source", "구매 증거 출처")} <small>{l("(required)", "(필수)")}</small></span>
                    <NativeSelect
                      value={placementEvidenceSource}
                      onChange={(event) => setPlacementEvidenceSource(event.target.value as typeof placementEvidenceSource)}
                    >
                      <NativeSelectOption value="RECEIPT">{l("Receipt", "영수증")}</NativeSelectOption>
                      <NativeSelectOption value="MERCHANT_PAGE">{l("Merchant page", "판매처 페이지")}</NativeSelectOption>
                      <NativeSelectOption value="OPERATOR_OBSERVATION">{l("Operator observation", "운영자 관찰")}</NativeSelectOption>
                      <NativeSelectOption value="MERCHANT_POLICY">{l("Merchant policy", "판매처 정책")}</NativeSelectOption>
                      <NativeSelectOption value="OTHER">{l("Other", "기타")}</NativeSelectOption>
                    </NativeSelect>
                  </label>
				</div>
				<p>{l(
					"The server generates the SHA-256 evidence hash and observed time automatically when this record is accepted. No hash or time input is required.",
					"서버가 기록 승인 시 SHA-256 증거 해시와 관찰 시각을 자동 생성합니다. 해시·시각 입력은 필요하지 않습니다.",
				)}</p>
				{!placementEvidenceReady ? <small role="status">{l(
					"Complete every required field using the format and allowed range shown in its label.",
					"각 label에 표시된 형식과 허용 범위에 맞게 필수 필드를 모두 입력하세요.",
				)}</small> : null}
			</section>
            ) : null}
            <div className="agency-order-operator__actions">
			  {can("PROCUREMENT_EXECUTION", task.id, "RECORD_PLACED") ? (
				<Button emphasis="primary" disabled={!placementEvidenceReady} busy={working === `${task.id}:done`} onClick={() => void run(`${task.id}:done`, async () => {
                  await completeProcurementTask(task.id, {
                    evidenceKind: simulated ? "SANDBOX_TEST_EVIDENCE" : "LIVE_MERCHANT_EFFECT_EVIDENCE",
                    externalOrderRef: externalOrderRef.trim(),
                    receiptSafeRef: receiptSafeRef.trim(),
					amountMode: placementAmountMode,
					...(placementAmountMode === "CHANGED" ? { actualAmountMinor: placementAmountMinor } : {}),
                    evidenceSource: placementEvidenceSource,
                  });
                })}>
                  <PackageCheck aria-hidden="true" /> {simulated
                    ? l("Record TEST purchase evidence", "TEST 구매 증거 기록")
                    : l("Record live purchase evidence", "Live 구매 증거 기록")}
                </Button>
              ) : null}
            </div>
          </>
        ) : null}
        {order.placementEvidence && itemStage === "LOGISTICS" ? (
          <p role="status">
            {l("Order completion recorded", "주문 완료 기록됨")}
            {simulated ? l(" · TEST evidence only; no live merchant effect claimed", " · TEST 증거 전용; 실제 merchant effect 주장 없음") : ""}
            {l(" — continue with shipping below.", " — 아래에서 배송을 진행해 주세요.")}
          </p>
        ) : null}
        {itemStage === "LOGISTICS" ? <LogisticsPanel
          allowedActions={(action) => can("PROCUREMENT_EXECUTION", task.id, action)}
          item={item}
          simulated={simulated}
        /> : null}
        {itemStage === "DONE" ? (
          <p role="status">{l("MerchantOrder closed — {reason}.", "MerchantOrder 종결 — {reason}.", {
            reason: merchantOrderOperationalStageLabel(item.operational.stage, l),
          })}</p>
        ) : null}
        {item.operational.stage === "PROCUREMENT_FAILED" ? <p role="alert">{order.failureCode} · {l("refunding this shop's products; other shops continue", "해당 Shop 상품 환불 진행 (다른 Shop은 계속)")}</p> : null}
        <div className="agency-order-operator__notice">
          <label>
            {l("Send customer update", "고객 안내 보내기")}
            <Textarea
              placeholder={l("Example: Merchant verification is delayed and processing will take 1–2 more days.", "예: 판매처 확인이 지연되어 처리에 1~2일이 더 걸립니다.")}
              value={noticeDrafts[task.id] ?? ""}
              onChange={(event) => setNoticeDrafts((current) => ({ ...current, [task.id]: event.target.value }))}
            />
          </label>
          <Input
            accept="image/jpeg,image/png"
            aria-label={l("Attach customer evidence images", "고객 안내 증거 이미지 첨부")}
            key={`${task.id}:${(noticeImages[task.id] ?? []).length}`}
            multiple
            onChange={(event) => {
              const selected = Array.from(event.target.files ?? []);
              const valid = selected.length <= 4 && selected.every((file) =>
                (file.type === "image/jpeg" || file.type === "image/png") &&
                file.size <= 5 * 1024 * 1024,
              );
              setNoticeImages((current) => ({ ...current, [task.id]: valid ? selected : [] }));
              setNoticeImageError(valid ? "" : l(
                "Attach up to four JPEG or PNG images, no larger than 5 MB each.",
                "JPEG 또는 PNG 이미지를 최대 4장, 장당 5MB 이하로 첨부해 주세요.",
              ));
            }}
            type="file"
          />
          {noticeImageError ? <p className="vt-field__error" role="status">{noticeImageError}</p> : null}
          {(noticeImages[task.id] ?? []).length > 0 ? <small>{l(
            "{count} images attached (JPEG/PNG, up to 5 MB each)",
            "이미지 {count}장 첨부(JPEG/PNG, 장당 최대 5MB)",
            { count: noticeImages[task.id].length },
          )}</small> : null}
          <Button emphasis="quiet" size="compact" disabled={!(noticeDrafts[task.id] ?? "").trim()} busy={working === `${task.id}:notice`} onClick={() => void run(`${task.id}:notice`, async () => {
            await sendSupportOrderMessage(task.agencyOrderId, (noticeDrafts[task.id] ?? "").trim(), noticeImages[task.id] ?? []);
            setNoticeDrafts((current) => ({ ...current, [task.id]: "" }));
            setNoticeImages((current) => ({ ...current, [task.id]: [] }));
            setNoticeImageError("");
          })}>
            <MessageSquareText aria-hidden="true" /> {l("Send update", "안내 발송")}
          </Button>
        </div>
        <footer className="settlement-ui-order-foot"><div><code>{short(order.id)}</code><span>{l("mode=", "mode=")}{order.executionMode}</span></div></footer>
      </article>
    </Disclosure>
  );
}

function ProcurementManualReviewPanel({ item, addressRevealed, canRecordFailure, compensationAmountMinor, run, working }: {
  item: ProcurementQueueItem;
  addressRevealed: boolean;
  canRecordFailure: boolean;
  compensationAmountMinor?: number;
  run: (key: string, action: () => Promise<void>) => Promise<void>;
  working?: string;
}) {
  const { l, locale } = useLocale();
  const taskID = item.task.id;
  const [decisions, setDecisions] = useState<ProcurementManualDecision[]>([]);
  const [requests, setRequests] = useState<ProcurementCustomerRequest[]>([]);
  const [loadError, setLoadError] = useState<string>();
  const [decision, setDecision] = useState<ProcurementManualDecision["decision"]>("WITHIN_AUTHORIZATION");
  const [publicRationale, setPublicRationale] = useState("");
  const [internalNote, setInternalNote] = useState("");
  const [observedCondition, setObservedCondition] = useState("");
  const [evidenceSource, setEvidenceSource] = useState<ProcurementManualDecision["evidenceSource"]>("MERCHANT_PAGE");
  const [requestKind, setRequestKind] = useState<ProcurementCustomerRequest["kind"]>("INFORMATION");
  const [responseType, setResponseType] = useState<ProcurementCustomerRequest["responseType"]>("TEXT");
  const [prompt, setPrompt] = useState("");
  const [publicContext, setPublicContext] = useState("");
  const [responseOptions, setResponseOptions] = useState("");
  const [failureMode, setFailureMode] = useState(false);
  const [confirmNoResponseRequestID, setConfirmNoResponseRequestID] = useState<string>();
  const restoredAnsweredRequestID = useRef<string | undefined>(undefined);

  const refresh = useCallback(async () => {
    try {
      const review = await getProcurementManualReview(taskID);
      setDecisions(review.decisions);
      setRequests(review.customerRequests);
      setLoadError(undefined);
    } catch (caught) {
      setLoadError(caught instanceof Error
        ? caught.message
        : l("Review history could not be loaded.", "심사 이력을 불러오지 못했습니다."));
    }
  }, [l, taskID]);

  useEffect(() => { void refresh(); }, [refresh, item.merchantOrder.state]);

  const latestDecision = decisions[0];
  const latestRequest = requests[0];
  const pendingRequest = requests.find((request) => request.state === "PENDING");
  const answeredRequest = latestRequest?.state === "ANSWERED" &&
    latestDecision?.decision === "MATERIAL_NEW_CONDITION" &&
    latestRequest.sourceDecisionId === latestDecision.id
    ? latestRequest
    : undefined;
  const terminalStopRequest = requests.find((request) =>
    request.state === "DECLINED" || request.state === "FAILED_NO_RESPONSE" ||
    request.state === "CANCELLED");
	const fundingNeedsReconciliation = item.funding?.state === "ACTIVATION_PENDING" ||
		item.funding?.state === "ACTIVATION_UNKNOWN";
	const placementPending = item.merchantOrder.state === "PLACEMENT_PENDING";
	const forcedFailure = Boolean(terminalStopRequest);
	const failureActive = forcedFailure || failureMode;
	const decisionForSubmit = failureActive ? "UNABLE_TO_PURCHASE" : decision;
  const withinAuthorization = decisionForSubmit === "WITHIN_AUTHORIZATION";
  const immaterialVariance = decisionForSubmit === "IMMATERIAL_VARIANCE";
  const materialCondition = decisionForSubmit === "MATERIAL_NEW_CONDITION";
  const unableToPurchase = decisionForSubmit === "UNABLE_TO_PURCHASE";
  const reuseLatestUnable = unableToPurchase &&
    latestDecision?.decision === "UNABLE_TO_PURCHASE" &&
    publicRationale.trim() === "" && observedCondition.trim() === "";
  const withinPublicRationale = l(
    "We verified that the shop, product, variant, quantity, and price remain within the scope you approved.",
    "Shop·상품·variant·수량·금액이 고객 승인 범위와 일치함을 확인했습니다.",
  );
  const withinObservedCondition = l(
    "The merchant conditions match the immutable customer authorization.",
    "판매처 조건이 저장된 고객 승인 범위와 일치합니다.",
  );
  const decisionPublicRationale = reuseLatestUnable ? latestDecision.publicRationale
    : materialCondition ? publicContext.trim()
    : withinAuthorization ? withinPublicRationale
      : publicRationale.trim();
  const decisionObservedCondition = reuseLatestUnable ? latestDecision.observedCondition
    : withinAuthorization
    ? withinObservedCondition
    : observedCondition.trim();
  const decisionEvidenceSource = reuseLatestUnable ? latestDecision.evidenceSource
    : withinAuthorization ? "OPERATOR_OBSERVATION" : evidenceSource;
  const decisionInternalNote = reuseLatestUnable
    ? latestDecision.internalNote ?? ""
    : internalNote.trim();
  const [touched, setTouched] = useState<Record<string, boolean>>({});
  const touch = (key: string) => setTouched((current) => (current[key] ? current : { ...current, [key]: true }));
  const observedConstraint = textConstraint({ value: observedCondition, min: 1, max: 2_000, touched: touched.observed, l });
  const rationaleConstraint = textConstraint({ value: publicRationale, min: 8, max: 2_000, touched: touched.rationale, l });
  const internalNoteConstraint = textConstraint({ value: internalNote, max: 4_000, required: false, l });
  const contextConstraint = textConstraint({ value: publicContext, min: 8, max: 2_000, touched: touched.context, l });
  const promptConstraint = textConstraint({ value: prompt, min: 1, max: 2_000, touched: touched.prompt, l });
  const rationaleLength = Array.from(decisionPublicRationale.trim()).length;
  const observedLength = Array.from(decisionObservedCondition.trim()).length;
  const evidenceReady = rationaleLength >= 8 && rationaleLength <= 2_000 &&
    observedLength >= 1 && observedLength <= 2_000;
  const optionValues = responseOptions.split("\n").map((value) => value.trim()).filter(Boolean);
  const requestValidation = procurementRequestValidation({
    observedCondition,
    publicContext,
    prompt,
    responseType,
    responseOptions: optionValues,
  });
  const requestReady = materialCondition && !pendingRequest && requestValidation.ready;
  const actionReady = !pendingRequest && (
    ((withinAuthorization || immaterialVariance) && addressRevealed && evidenceReady) ||
    (materialCondition && requestReady) ||
    (unableToPurchase && canRecordFailure && evidenceReady)
  );
  const actionKey = `${taskID}:manual-action`;

  useEffect(() => {
    if (!answeredRequest || !latestDecision || pendingRequest || restoredAnsweredRequestID.current === answeredRequest.id) {
      return;
    }
    restoredAnsweredRequestID.current = answeredRequest.id;
    setDecision("MATERIAL_NEW_CONDITION");
    setObservedCondition(latestDecision.observedCondition);
    setEvidenceSource(latestDecision.evidenceSource);
    setInternalNote("");
    setRequestKind(answeredRequest.kind);
    setResponseType(answeredRequest.responseType);
    setResponseOptions((answeredRequest.responseOptions ?? []).join("\n"));
    setPublicContext(answeredRequest.publicContext);
    setPrompt(answeredRequest.prompt);
    setFailureMode(false);
  }, [answeredRequest, latestDecision, pendingRequest]);

  useEffect(() => {
    if (!pendingRequest) return;
    const timer = window.setInterval(() => void refresh(), 4_000);
    return () => window.clearInterval(timer);
  }, [pendingRequest, refresh]);

  async function recordSelectedDecision() {
    const alreadyRecorded = latestDecision?.decision === decisionForSubmit &&
      latestDecision.publicRationale === decisionPublicRationale &&
      latestDecision.internalNote === (decisionInternalNote || undefined) &&
      latestDecision.observedCondition === decisionObservedCondition &&
      latestDecision.evidenceSource === decisionEvidenceSource;
    if (alreadyRecorded) return;
    await recordProcurementManualDecision(taskID, {
      decision: decisionForSubmit,
      publicRationale: decisionPublicRationale,
      internalNote: decisionInternalNote,
      observedCondition: decisionObservedCondition,
      evidenceSource: decisionEvidenceSource,
    });
    await refresh();
  }

	async function submitSelectedAction() {
		if (materialCondition) {
			await createProcurementCustomerRequest(taskID, {
				observedCondition: decisionObservedCondition,
				internalNote: decisionInternalNote,
				evidenceSource: decisionEvidenceSource,
				kind: requestKind,
				prompt: prompt.trim(),
				responseType,
				responseOptions: responseType === "SINGLE_CHOICE" ? optionValues : [],
				publicContext: publicContext.trim(),
			});
			setDecision("WITHIN_AUTHORIZATION");
		} else {
			await recordSelectedDecision();
		}
		if (withinAuthorization || immaterialVariance) {
			const admission = await beginProcurementMerchantEffect(taskID);
            if (admission.outcome === "REJECTED") throw new ProcessRequestRejected(admission);
            if (admission.outcome !== "COMPLETED") throw new ProcessRequestPending(admission);
		} else if (unableToPurchase) {
			await failProcurementTask(taskID, "PRODUCT_UNAVAILABLE");
    }
    setFailureMode(false);
    setPublicRationale("");
    setInternalNote("");
    setObservedCondition("");
    setPrompt("");
    setPublicContext("");
    setResponseOptions("");
    await refresh();
  }

  const actionLabel = withinAuthorization
    ? l("Begin order processing", "주문 처리 시작")
    : immaterialVariance
      ? l("Record variance and begin processing", "경미한 차이 근거 기록·주문 처리")
      : materialCondition
        ? answeredRequest
          ? l("Send follow-up request in Messages", "Messages로 재요청 보내기")
          : l("Send request in Messages", "Messages로 요청 보내기")
        : l("Record unavailable · refund this shop", "구매 불가 기록·해당 Shop 환불");

  return <section className="agency-order-operator__manual-review" aria-label={l("Manual procurement decision", "수동 Procurement 판단")}>
    <header>
      <div>
        <h3>{l("Manual procurement decision", "수동 Procurement 판단")}</h3>
        <p>{l(
          "Judge the merchant site yourself from the shop, product, variant, quantity, price limit, and customer-approved conditions. The saved checkout link is optional reference material.",
          "Shop·상품·variant·수량·승인 상한·고객 승인 조건을 보고 운영자가 직접 판단합니다. 보관된 checkout 링크는 선택적 참고 자료입니다.",
        )}</p>
      </div>
      {latestDecision ? <Chip>{manualDecisionLabel(latestDecision.decision, l)}</Chip> : null}
    </header>
    {loadError ? <Notice tone="danger">{loadError}</Notice> : null}
	{fundingNeedsReconciliation ? <Notice tone="warning" title={l(
		"PayPal funding must be reconciled before purchasing",
		"판매처 구매 전 PayPal 자금 대사가 필요합니다",
	)}>
		{l(
			"Do not purchase from the merchant yet. Recheck the one stored MO capture; the server reuses its original PayPal request identity and opens processing only after funding is confirmed.",
			"아직 판매처에서 구매하지 마세요. 저장된 단 하나의 MO Capture를 다시 확인하며, 서버는 최초 PayPal 요청 identity를 그대로 사용하고 자금이 확정된 뒤에만 주문 처리를 엽니다.",
		)}
		<div className="agency-order-operator__actions">
			<Button
				busy={working === `${taskID}:funding-reconciliation`}
				emphasis="primary"
				onClick={() => void run(`${taskID}:funding-reconciliation`, async () => {
					const admission = await beginProcurementMerchantEffect(taskID);
            if (admission.outcome === "REJECTED") throw new ProcessRequestRejected(admission);
            if (admission.outcome !== "COMPLETED") throw new ProcessRequestPending(admission);
				})}
				type="button"
			>{l("Recheck funding and resume", "자금 재확인·처리 재개")}</Button>
		</div>
	</Notice> : null}
	{latestDecision ? <dl className="settlement-ui-quote">
		<div><dt>{latestDecision.decision === "UNABLE_TO_PURCHASE"
			? l("Latest customer-visible rationale", "최근 고객 공개 근거")
			: l("Latest recorded rationale", "최근 기록 근거")}</dt><dd>{latestDecision.publicRationale}</dd></div>
      <div><dt>{l("Observed condition", "관찰 조건")}</dt><dd>{latestDecision.observedCondition}</dd></div>
      <div><dt>{l("Recorded", "기록 시각")}</dt><dd>{new Date(latestDecision.createdAt).toLocaleString(locale)}</dd></div>
    </dl> : <p>{l("No decision has been recorded yet.", "아직 기록된 판단이 없습니다.")}</p>}
    {requests.map((request) => <div className="agency-order-operator__request" key={request.id}>
      <strong>{customerRequestStateLabel(request.state, l)}</strong>
      <p>{request.prompt}</p>
      <p><strong>{l("Customer context", "고객 공개 배경")}</strong> · {request.publicContext}</p>
      {procurementCustomerResponseLabel(request, l) ? <p>
        <strong>{l("Customer response", "고객 응답")}</strong> · {procurementCustomerResponseLabel(request, l)}
      </p> : null}
      <small>{l("Due {date}", "응답 기한 {date}", { date: new Date(request.dueAt).toLocaleString(locale) })}</small>
      {request.state === "PENDING" ? <div className="agency-order-operator__actions">
        <Button
          busy={working === `${taskID}:no-response:${request.id}`}
          disabled={Date.now() < new Date(request.dueAt).getTime()}
          emphasis="quiet"
          onClick={() => setConfirmNoResponseRequestID(request.id)}
          size="compact"
          type="button"
        >{l("Close as no response", "무응답으로 종료")}</Button>
        <Button
          busy={working === `${taskID}:cancel-request:${request.id}`}
          emphasis="quiet"
          onClick={() => void run(`${taskID}:cancel-request:${request.id}`, async () => {
            await resolveProcurementCustomerRequest(request.id, request.version, true, "OPERATOR_CANCELLED");
            await refresh();
          })}
          size="compact"
          type="button"
        >{l("Cancel question", "질문 취소")}</Button>
      </div> : null}
    </div>)}
    <Dialog open={Boolean(confirmNoResponseRequestID)} onOpenChange={(open) => { if (!open) setConfirmNoResponseRequestID(undefined); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{l("Confirm no-response closure", "무응답 종료 확인")}</DialogTitle>
          <DialogDescription>{l(
            "This closes only the customer question. It does not issue a refund automatically; the MerchantOrder must next be recorded as unavailable.",
            "고객 질문만 종료합니다. 환불은 자동 실행되지 않으며, 다음으로 해당 MerchantOrder를 구매 불가 처리해야 합니다.",
          )}</DialogDescription>
        </DialogHeader>
        <dl className="settlement-ui-quote">
          <div><dt>{l("Order", "주문")}</dt><dd>{item.task.agencyOrderId}</dd></div>
          <div><dt>{l("MerchantOrder", "MerchantOrder")}</dt><dd>{item.merchantOrder.id}</dd></div>
          <div><dt>{l("Shop", "Shop")}</dt><dd>{item.merchantOrder.shopDomain}</dd></div>
          <div><dt>{l("Response due", "응답 기한")}</dt><dd>{confirmNoResponseRequestID ? new Date(requests.find((request) => request.id === confirmNoResponseRequestID)?.dueAt ?? "").toLocaleString(locale) : "-"}</dd></div>
          <div><dt>{l("Whole-MO refund or release", "MO 전체 환불·승인 해제액")}</dt><dd>{compensationAmountMinor === undefined ? l("Check order accounting", "주문 회계에서 확인") : formatMoney({ amountMinor: compensationAmountMinor, currency: "USD" })}</dd></div>
        </dl>
        <Notice tone="warning">{l(
          "Purchasing remains blocked. Confirm only after the response deadline; the next required action is to record this shop as unavailable and complete its refund or authorization release.",
          "구매는 계속 차단됩니다. 응답 기한 뒤에만 확정하고, 다음 필수 행동으로 이 Shop을 구매 불가 기록한 뒤 환불 또는 승인을 해제하세요.",
        )}</Notice>
        <DialogFooter>
          <Button emphasis="quiet" onClick={() => setConfirmNoResponseRequestID(undefined)} type="button">{l("Keep waiting", "계속 기다리기")}</Button>
          <Button emphasis="danger" busy={working === `${taskID}:no-response:${confirmNoResponseRequestID}`} onClick={() => {
            const request = requests.find((candidate) => candidate.id === confirmNoResponseRequestID);
            if (!request) return;
            void run(`${taskID}:no-response:${request.id}`, async () => {
              await resolveProcurementCustomerRequest(request.id, request.version, false, "NO_RESPONSE_AFTER_DUE");
              setConfirmNoResponseRequestID(undefined);
              setFailureMode(true);
              await refresh();
            });
          }} type="button">{l("Confirm no-response closure", "무응답 종료 확정")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
	{placementPending && !forcedFailure && !failureMode && !pendingRequest &&
		!fundingNeedsReconciliation && canRecordFailure ? <div className="agency-order-operator__actions">
		<Button
			emphasis="secondary"
			onClick={() => setFailureMode(true)}
			type="button"
		>{l("Report a purchase failure", "구매 실패 처리 시작")}</Button>
	</div> : null}
	{(item.merchantOrder.state === "PLANNED" || (placementPending && failureActive)) && !pendingRequest &&
		!fundingNeedsReconciliation ?
		<div className="agency-order-operator__review-form">
			{!failureActive ? <label>
				<span>{l("Decision", "판단")}</span>
				<NativeSelect aria-label={l("Decision", "판단")} value={decisionForSubmit} onChange={(event) => setDecision(event.target.value as ProcurementManualDecision["decision"])}>
					<NativeSelectOption value="WITHIN_AUTHORIZATION">{l("Within customer authorization", "고객 승인 범위 내")}</NativeSelectOption>
					<NativeSelectOption value="IMMATERIAL_VARIANCE">{l("Immaterial variance", "경미한 차이")}</NativeSelectOption>
					<NativeSelectOption value="MATERIAL_NEW_CONDITION">{l("Material new condition", "새로운 중대한 조건")}</NativeSelectOption>
					<NativeSelectOption value="UNABLE_TO_PURCHASE">{l("Unable to purchase", "구매 불가")}</NativeSelectOption>
				</NativeSelect>
			</label> : <div>
				<h4>{l("Purchase failure and refund", "구매 실패·환불 처리")}</h4>
				{terminalStopRequest ? <p className="vt-field__hint">{l(
					"The customer request ended without approval. This purchase can only be closed as unavailable.",
					"고객 요청이 승인 없이 종료되어 구매 불가로만 종결할 수 있습니다.",
				)}</p> : null}
				{placementPending && !forcedFailure ? <Button
					emphasis="quiet"
					onClick={() => setFailureMode(false)}
					size="compact"
					type="button"
				>{l("Cancel failure handling", "실패 처리 취소")}</Button> : null}
			</div>}
        {placementPending ? <Notice tone="neutral">{l(
          "A merchant attempt has already started. Record why it cannot be completed, then close it and refund this shop.",
          "merchant 주문 시도가 이미 시작되었습니다. 완료할 수 없는 근거를 기록한 뒤 해당 Shop 실패·환불로 닫습니다.",
        )}</Notice> : null}
        {!withinAuthorization ? <>
          <Field error={observedConstraint.error} hint={observedConstraint.hint} id={`manual-observed-${taskID}`} label={materialCondition
              ? l("New condition observed at the merchant", "판매처에서 발견한 새로운 조건")
              : l("Observed merchant condition", "관찰한 merchant 조건")} required>
            <Textarea aria-label={l("Observed merchant condition", "관찰한 merchant 조건")} placeholder={materialCondition
              ? l("Record the new price, option, policy, or requirement.", "새 가격·옵션·정책·요구사항을 기록하세요.")
              : l("What did you observe on the merchant site?", "merchant 사이트에서 무엇을 확인했나요?")} maxLength={2_000} value={observedCondition} onBlur={() => touch("observed")} onChange={(event) => setObservedCondition(event.target.value)} />
          </Field>
          {!materialCondition ? <Field error={rationaleConstraint.error} hint={`${l("Shown to the customer with the decision.", "판단과 함께 고객에게 표시됩니다.")} ${rationaleConstraint.hint}`} id={`manual-rationale-${taskID}`} label={l("Customer-visible rationale", "고객 공개 근거")} required>
            <Textarea aria-label={l("Customer-visible rationale", "고객 공개 근거")} maxLength={2_000} placeholder={l("Explain the decision to the customer", "판단 근거를 고객에게 설명하세요")} value={publicRationale} onBlur={() => touch("rationale")} onChange={(event) => setPublicRationale(event.target.value)} />
          </Field> : null}
          <label>
            <span>{l("Evidence source", "증거 출처")}</span>
            <NativeSelect aria-label={l("Evidence source", "증거 출처")} value={evidenceSource} onChange={(event) => setEvidenceSource(event.target.value as ProcurementManualDecision["evidenceSource"])}>
              <NativeSelectOption value="MERCHANT_PAGE">{l("Merchant page", "Merchant 페이지")}</NativeSelectOption>
              <NativeSelectOption value="MERCHANT_POLICY">{l("Merchant policy", "Merchant 정책")}</NativeSelectOption>
              <NativeSelectOption value="OPERATOR_OBSERVATION">{l("Operator observation", "운영자 관찰")}</NativeSelectOption>
              <NativeSelectOption value="RECEIPT">{l("Receipt", "영수증")}</NativeSelectOption>
              <NativeSelectOption value="OTHER">{l("Other", "기타")}</NativeSelectOption>
            </NativeSelect>
          </label>
          <Field error={internalNoteConstraint.error} hint={`${l("Never shown to the customer.", "고객에게 표시되지 않습니다.")} ${internalNoteConstraint.hint}`} id={`manual-internal-note-${taskID}`} label={l("Private accounting note (optional)", "내부 회계 메모(선택)")}>
            <Textarea aria-label={l("Private accounting note", "내부 회계 메모")} maxLength={4_000} placeholder={l("Optional private note for later accounting", "추후 회계 처리를 위한 선택 내부 메모")} value={internalNote} onChange={(event) => setInternalNote(event.target.value)} />
          </Field>
        </> : null}
        {materialCondition ? <div className="agency-order-operator__request-form">
          <label>
            <span>{l("Request kind", "요청 종류")}</span>
            <NativeSelect aria-label={l("Request kind", "요청 종류")} value={requestKind} onChange={(event) => {
              const next = event.target.value as ProcurementCustomerRequest["kind"];
              setRequestKind(next);
              if (next === "CONSENT") setResponseType("BOOLEAN_CONSENT");
              else if (responseType === "BOOLEAN_CONSENT") setResponseType("TEXT");
            }}>
              <NativeSelectOption value="INFORMATION">{l("Request information", "추가정보 요청")}</NativeSelectOption>
              <NativeSelectOption value="CONSENT">{l("Request consent", "동의 요청")}</NativeSelectOption>
            </NativeSelect>
          </label>
          <label>
            <span>{l("Response format", "응답 형식")}</span>
            <NativeSelect aria-label={l("Response format", "응답 형식")} value={responseType} onChange={(event) => setResponseType(event.target.value as ProcurementCustomerRequest["responseType"])}>
              {requestKind === "INFORMATION" ? <NativeSelectOption value="TEXT">{l("Text", "직접 입력")}</NativeSelectOption> : null}
              {requestKind === "INFORMATION" ? <NativeSelectOption value="SINGLE_CHOICE">{l("Single choice", "단일 선택")}</NativeSelectOption> : null}
              {requestKind === "CONSENT" ? <NativeSelectOption value="BOOLEAN_CONSENT">{l("Accept or decline", "동의·거절")}</NativeSelectOption> : null}
            </NativeSelect>
          </label>
          <Field error={contextConstraint.error} hint={`${l("The customer reads this in Messages.", "고객이 메시지에서 읽는 설명입니다.")} ${contextConstraint.hint}`} id={`request-context-${taskID}`} label={l("Customer-visible context and rationale", "고객 공개 배경·판단 근거")} required>
            <Textarea aria-label={l("Public context", "고객 공개 배경")} maxLength={2_000} placeholder={l("Explain the new condition, why a response is needed, and what happens next.", "새 조건, 응답이 필요한 이유와 답변 후 처리를 설명하세요.")} value={publicContext} onBlur={() => touch("context")} onChange={(event) => setPublicContext(event.target.value)} />
          </Field>
          <Field error={promptConstraint.error} hint={promptConstraint.hint} id={`request-prompt-${taskID}`} label={l("Question", "고객 질문")} required>
            <Textarea aria-label={l("Question", "고객 질문")} maxLength={2_000} placeholder={l("What must the customer answer?", "고객이 무엇에 답해야 하나요?")} value={prompt} onBlur={() => touch("prompt")} onChange={(event) => setPrompt(event.target.value)} />
          </Field>
          {responseType === "SINGLE_CHOICE" ? <Field error={responseOptions.trim() !== "" && !requestValidation.optionsReady ? l("Enter 2–10 unique choices, one per line and at most 200 characters each.", "중복 없는 선택지를 한 줄에 하나씩 2–10개 입력하세요(각 200자 이하).") : undefined} hint={l("2–10 unique options, one per line, up to 200 characters each · {count} now", "중복 없는 선택지 2~10개 · 한 줄에 하나 · 각 200자 이하 · 현재 {count}개", { count: optionValues.length })} id={`request-options-${taskID}`} label={l("Choice options", "선택지")} required>
            <Textarea aria-label={l("Choice options", "선택지")} maxLength={2_010} placeholder={l("One option per line", "한 줄에 선택지 하나")} value={responseOptions} onChange={(event) => setResponseOptions(event.target.value)} />
          </Field> : null}
        </div> : null}
        <Button
          disabled={!actionReady}
          emphasis={unableToPurchase ? "danger" : "primary"}
          busy={working === actionKey}
          onClick={() => void run(actionKey, submitSelectedAction)}
          type="button"
        >{actionLabel}</Button>
        {(withinAuthorization || immaterialVariance) && !addressRevealed ? <small role="status">{l(
          "First complete the audited shipping-address reveal above. The processing button then becomes available.",
          "먼저 위에서 배송정보 감사 열람을 완료하세요. 완료되면 주문 처리 버튼이 활성화됩니다.",
        )}</small> : null}
        {!withinAuthorization && !materialCondition && !evidenceReady ? <small role="status">{l(
          "Enter the required observed condition and customer-visible rationale.",
          "필수 관찰 조건과 고객 공개 근거를 입력하면 버튼이 활성화됩니다.",
        )}</small> : null}
        {materialCondition && !requestValidation.observedReady ? <small role="status">{l("Enter the observed merchant condition.", "판매처에서 확인한 새 조건을 입력하세요.")}</small> : null}
        {materialCondition && !requestValidation.contextReady ? <small role="status">{l("Enter 8–2,000 characters of customer-visible context.", "고객 공개 배경·판단 근거를 8–2,000자로 입력하세요.")}</small> : null}
        {materialCondition && !requestValidation.promptReady ? <small role="status">{l("Enter the question the customer must answer.", "고객이 답할 질문을 입력하세요.")}</small> : null}
        {materialCondition && !requestValidation.optionsReady ? <small role="status">{l("Enter 2–10 unique choices, one per line and at most 200 characters each.", "중복 없는 선택지를 한 줄에 하나씩 2–10개 입력하세요(각 200자 이하).")}</small> : null}
        {unableToPurchase && !canRecordFailure ? <small role="status">{l(
          "The server does not allow failure/refund in the current task state.",
          "현재 Task 상태에서는 서버가 실패·환불 행동을 허용하지 않습니다.",
        )}</small> : null}
      </div> : null}
  </section>;
}

function manualDecisionLabel(value: ProcurementManualDecision["decision"], l: Localize) {
  return {
    WITHIN_AUTHORIZATION: l("Within authorization", "승인 범위 내"),
    IMMATERIAL_VARIANCE: l("Immaterial variance", "비중요 차이"),
    MATERIAL_NEW_CONDITION: l("Customer input needed", "고객 응답 필요"),
    UNABLE_TO_PURCHASE: l("Unable to purchase", "구매 불가"),
  }[value];
}

function customerRequestStateLabel(value: ProcurementCustomerRequest["state"], l: Localize) {
  return {
    PENDING: l("Waiting for customer", "고객 응답 대기"),
    ANSWERED: l("Customer answered — record a new decision", "고객 답변 완료 — 새 판단 필요"),
    DECLINED: l("Customer declined", "고객 거절"),
    FAILED_NO_RESPONSE: l("Closed after no response", "무응답 종료"),
    CANCELLED: l("Question cancelled", "질문 취소"),
  }[value];
}

export function procurementCustomerResponseLabel(
  request: ProcurementCustomerRequest,
  l: Localize = localizeFixedCopy,
) {
  if (request.state === "DECLINED") return l("Customer declined", "고객이 거절했습니다");
  if (request.state === "FAILED_NO_RESPONSE") return l("No response by the due date", "기한 내 응답 없음");
  if (request.state === "CANCELLED") return l("Question cancelled by operator", "운영자가 질문을 취소함");
  if (request.state !== "ANSWERED" || !request.response || typeof request.response !== "object") return "";
  const response = request.response as Record<string, unknown>;
  if (typeof response.text === "string" && response.text.trim()) return response.text;
  if (typeof response.choice === "string" && response.choice.trim()) return response.choice;
  if (typeof response.accepted === "boolean") {
    return response.accepted ? l("Agreed", "동의함") : l("Declined", "거절함");
  }
  return l("Response recorded", "응답 기록됨");
}

// LogisticsPanel은 서버가 내려준 MO·Shipment 행동만 렌더한다. Sandbox도 같은
// 단계별 command를 사용하며 프론트가 전이표를 재구성하거나 단계를 건너뛰지 않는다.
function LogisticsPanel({ item, simulated, allowedActions }: {
  item: ProcurementQueueItem;
  simulated: boolean;
  allowedActions: (action: string) => boolean;
}) {
  const { l } = useLocale();
  const [views, setViews] = useState<OperatorShipmentView[]>([]);
  // 프리필 금지(2차 P6) — 운송사도 매번 직접 입력한다.
  const [carrier, setCarrier] = useState("");
  const [trackingRef, setTrackingRef] = useState("");
  const [exceptionMarks, setExceptionMarks] = useState<Record<string, "MISSING" | "WRONG_ACTUAL">>({});
  const [busy, setBusy] = useState<string>();
  const [message, setMessage] = useState<string>();
  const [pendingReceipt,setPendingReceipt]=useState<ProcessReceipt>();
  const waiting=!!pendingReceipt && !["COMPLETED","REJECTED"].includes(pendingReceipt.outcome);

  const orderShipments = views.filter((view) => view.shipment.merchantOrderId === item.merchantOrder.id);
  const canCreateShipment = !waiting && allowedActions("CREATE_SHIPMENT");
  const canRecordShipmentEvent = !waiting && allowedActions("RECORD_SHIPMENT_EVENT");
  const canConfirmDeliveryOutcome = !waiting && allowedActions("CONFIRM_DELIVERY_OUTCOME");

  async function refresh() {
    try {
      setViews((await listOrderShipments(item.task.agencyOrderId)).shipments);
    } catch {
      // 조회 실패는 패널 표시만 비운다 — 큐 폴링이 곧 복구한다.
    }
  }
  useEffect(() => { void refresh(); }, [item.task.id]);

  useEffect(()=>{
    if(!pendingReceipt || !waiting)return;
    let disposed=false;
    const timer=window.setInterval(async()=>{
      try {const next=await getProcessReceipt(pendingReceipt,true);if(disposed)return;setPendingReceipt(next);if(next.outcome==="COMPLETED")await refresh();}
      catch {if(!disposed)setMessage(l("Progress could not be refreshed. The saved shipping request is still being processed.", "진행 상황을 갱신하지 못했습니다. 저장된 배송 요청은 계속 처리됩니다."));}
    },2000);
    return()=>{disposed=true;window.clearInterval(timer);};
  },[pendingReceipt,waiting,l]);
  async function act(key: string, action: () => Promise<void>, done?: string) {
    setBusy(key); setMessage(undefined);setPendingReceipt(undefined);
    try { await action(); await refresh(); if (done) setMessage(done); }
    catch (caught) {
      if(caught instanceof ProcessRequestPending || caught instanceof ProcessRequestRejected){setPendingReceipt(caught.receipt);return;}
      const raw = caught instanceof Error ? caught.message : "";
      // 서버 전이 거절의 백스톱(3차 #3) — 원문 코드 대신 다음 행동을 안내한다.
      setMessage(raw.includes("LOGISTICS_SHIPMENT")
        ? l(
            "This action is unavailable at the current MerchantOrder stage. Refresh its server state and continue with the highlighted action.",
            "현재 MerchantOrder 단계에서는 실행할 수 없습니다. 서버 상태를 갱신한 뒤 강조된 다음 행동을 진행해 주세요.",
          )
        : raw || l("We couldn't complete shipping processing.", "배송 처리를 완료하지 못했습니다."));
    }
    finally { setBusy(undefined); }
  }

  const syntheticTracking = () => `SBX-${item.merchantOrder.id.slice(0, 8)}-${Date.now().toString(36)}`;

  return <section aria-label={l("Shipping progress", "배송 진행")} className="agency-order-operator__logistics">
    <h3>
      {l("Shipping progress", "배송 진행")} <Chip mode={simulated ? "test" : "live"}>{simulated ? l("SANDBOX", "SANDBOX") : l("LIVE", "LIVE")}</Chip>
      {simulated ? l(" — simulated checks without physical shipment", " — 실물 없이 처리했다 치고 체크") : ""}
    </h3>
    {canCreateShipment ? (
      <div className="agency-order-operator__logistics-form">
        <Input aria-label={l("Carrier", "운송사")} placeholder={simulated ? l("Carrier (example: SANDBOX)", "운송사 (예: SANDBOX)") : l("Carrier", "운송사")} value={carrier} onChange={(event) => setCarrier(event.target.value)} />
        <Input aria-label={l("Tracking number", "운송장 번호")} placeholder={l("Tracking number", "운송장 번호")} value={trackingRef} onChange={(event) => setTrackingRef(event.target.value)} />
	        <Button busy={busy === "ship"} disabled={!carrier.trim()} emphasis={orderShipments.length === 0 ? "primary" : "secondary"} size="compact" onClick={() => void act("ship", async () => {
          await createShipment(item.merchantOrder.id, carrier.trim(), trackingRef.trim() || syntheticTracking());
          setTrackingRef("");
        }, l("Tracking registered.", "운송장을 등록했습니다."))} type="button">{l("Register tracking", "운송장 등록")}</Button>
      </div>
    ) : null}
    {orderShipments.map((view) => <div className="agency-order-operator__shipment" key={view.shipment.id}>
      <div><strong>{shipmentStateLabel(view.shipment.state, l)}</strong><span>{view.shipment.carrier} · <code>{view.shipment.trackingRef}</code></span></div>
      <ul>
        {view.units.map((unit) => <li key={unit.id}>
          <label>
            {unit.lineId} #{unit.unitIndex} — {fulfillmentLabel(unit.fulfillment, l)}
            {canConfirmDeliveryOutcome && view.actions.includes("CONFIRM_DELIVERY_OUTCOME") ? (
              <NativeSelect aria-label={l("Delivery exception for {line} #{index}", "{line} #{index} 수령 예외", { line: unit.lineId, index: unit.unitIndex })} size="sm" value={exceptionMarks[unit.id] ?? ""} onChange={(event) => setExceptionMarks((current) => {
                const next = { ...current };
                if (event.target.value === "") delete next[unit.id];
                else next[unit.id] = event.target.value as "MISSING" | "WRONG_ACTUAL";
                return next;
              })}>
                <NativeSelectOption value="">{l("Received correctly", "정상 수령")}</NativeSelectOption>
                <NativeSelectOption value="MISSING">{l("Missing", "누락")}</NativeSelectOption>
                <NativeSelectOption value="WRONG_ACTUAL">{l("Wrong item", "오배송")}</NativeSelectOption>
              </NativeSelect>
            ) : null}
          </label>
        </li>)}
      </ul>
      {view.actions.length > 0 ? <div>
	        {canRecordShipmentEvent && view.actions.includes("RECORD_IN_TRANSIT") ? <Button busy={busy === `${view.shipment.id}:transit`} emphasis="primary" size="compact" onClick={() => void act(`${view.shipment.id}:transit`, async () => {
          await recordShipmentEvent(view.shipment.id, "IN_TRANSIT", simulated ? invariantContent("sandbox 처리했다 치고") : "");
        })} type="button">{l("Mark in transit", "배송중 처리")}</Button> : null}
	        {canConfirmDeliveryOutcome && view.actions.includes("CONFIRM_DELIVERY_OUTCOME") ? <Button busy={busy === `${view.shipment.id}:delivered`} emphasis="primary" size="compact" onClick={() => void act(`${view.shipment.id}:delivered`, async () => {
          await confirmShipmentDelivered(view.shipment.id, exceptionMarks);
          setExceptionMarks({});
        }, Object.keys(exceptionMarks).length > 0
          ? l("Delivery outcome recorded with {count} exception(s).", "배송 결과를 예외 {count}건과 함께 기록했습니다.", { count: Object.keys(exceptionMarks).length })
          : l("Normal receipt recorded.", "정상 수령을 기록했습니다."))} type="button">
          {Object.keys(exceptionMarks).length > 0
            ? l("Record delivery outcome · {count} exceptions", "배송 결과 기록 · 예외 {count}건", { count: Object.keys(exceptionMarks).length })
            : l("Record normal receipt", "정상 수령 기록")}
        </Button> : null}
      </div> : null}
    </div>)}
    {orderShipments.length === 0 ? <small>{l("No tracking is registered yet. Register tracking to start shipping.", "아직 등록된 운송장이 없습니다 — 배송을 시작하려면 운송장을 등록하세요.")}</small> : null}
    {pendingReceipt ? <ProcessReceiptNotice receipt={pendingReceipt} operator /> : null}
    {message ? <Notice tone="neutral">{message}</Notice> : null}
  </section>;
}

function shipmentStateLabel(
  state: OperatorShipmentView["shipment"]["state"],
  l: Localize = localizeFixedCopy,
) {
  return {
    CREATED: l("Preparing shipment", "배송 준비"),
    LABEL_CREATED: l("Tracking registered", "운송장 등록"),
    IN_TRANSIT: l("In transit", "배송 중"),
    OUT_FOR_DELIVERY: l("Out for delivery", "배송 출발"),
    DELIVERED: l("Delivery outcome recorded", "배송 결과 기록됨"),
    EXCEPTION: l("Needs attention", "확인 필요"),
    LOST: l("Lost", "분실"),
    RETURN_TO_SENDER: l("Returning to sender", "반송 중"),
    RETURNED: l("Returned", "반송 완료"),
    CANCELLED_NO_EFFECT: l("Cancelled", "취소"),
    EXCEPTION_RECONCILIATION: l("Post-delivery review", "수령 후 확인"),
  }[state];
}

function formatMoney(money: { amountMinor: number; currency: string }) {
  return new Intl.NumberFormat("en-US", { style: "currency", currency: money.currency }).format(money.amountMinor / 100);
}

function stageLabel(
  item: ProcurementQueueItem,
  l: Localize = localizeFixedCopy,
) {
  return merchantOrderOperationalStageLabel(item.operational.stage, l);
}

function short(value: string) {
  return value.length > 18 ? `${value.slice(0, 10)}…${value.slice(-6)}` : value;
}
