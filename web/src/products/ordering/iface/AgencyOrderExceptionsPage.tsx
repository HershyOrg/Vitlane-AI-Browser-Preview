import { ProcessReceiptNotice } from "./OrderProcessProgress";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { Button, FeedbackState, Field, Input, NativeSelect, NativeSelectOption, Notice, PageHeader, Textarea } from "../../../shared/ui";
import { textConstraint } from "../../../shared/forms/textConstraint";
import {
  createLogisticsReturn,
  decideRefundRequest,
  listOperatorWorkItems,
  resolveLogisticsException,
  retryProcessEffect,
  updateLogisticsReturn,
  type OperatorRefundRequest,
  type OperatorWorkItem,
  type OrderAccountingProjection,
} from "../infra/agencyOrderOperatorApi";
import { OrderAccountingPanel } from "./OrderAccountingPanel";
import { useOperatorSurface } from "../app/useOperatorSurface";
import "./agency-order.css";
import { invariantContent, useLocale, type Localize } from "../../../shared/i18n";
import { PayPalDisputePanel } from "./PayPalDisputePanel";
import { PayPalResourceAdoptionPanel } from "./PayPalResourceAdoptionPanel";

// returnNextFromActions는 서버가 허용한 다음 회수 행동을 상태 전이로 사상한다.
// 회수 진행 버튼은 이 파생만 사용한다(ADR-0057 — 로컬 상태 사다리 금지).
function returnNextFromActions(actions: string[], l: Localize) {
  if (actions.includes("MARK_RETURN_IN_TRANSIT")) return { state: "RETURN_IN_TRANSIT" as const, label: l("Return in transit", "회수 운송 중") };
  if (actions.includes("MARK_RECEIVED")) return { state: "RECEIVED" as const, label: l("Received", "수취 확인") };
  if (actions.includes("MARK_MERCHANT_RETURNED")) return { state: "MERCHANT_RETURNED" as const, label: l("Returned to merchant", "merchant 반환") };
  if (actions.includes("CLOSE_RETURN")) return { state: "CLOSED" as const, label: l("Closed", "종결") };
  return undefined;
}

type ExceptionTab = "REFUND" | "PAYMENT" | "DISPUTE" | "DELIVERY" | "RETURN" | "INTERVENTION" | "RESOLVED";

// 예외 처리 페이지(ADR-0057 2차 P2) — 환불 심사·배송 예외·회수·process 개입과
// 종결 열람을 소유한다. 맨 왼쪽 탭이 기본이다(P5). 주문 처리는 별도 페이지.
export function AgencyOrderExceptionsPage() {
  const { l, locale } = useLocale();
  const { surface, load, run, can, working, actionError, actionReceipt, receiptRefreshFailed } = useOperatorSurface();
  const [tab, setTab] = useState<ExceptionTab>("REFUND");
  const [deliveryRationales, setDeliveryRationales] = useState<Record<string, string>>({});
  const [resolvedItems, setResolvedItems] = useState<OperatorWorkItem[]>();

  // 처리 완료 열람은 탭 진입 시에만 조회한다(RESOLVED — 행동 없음).
  useEffect(() => {
    if (tab !== "RESOLVED") return;
    let active = true;
    void listOperatorWorkItems("RESOLVED")
      .then((result) => { if (active) setResolvedItems(result.items); })
      .catch(() => { if (active) setResolvedItems([]); });
    return () => { active = false; };
  }, [tab]);

  const openTaskMerchantOrders = new Set(surface.procurement
    .filter((item) => item.operational.workStage === "PROCUREMENT" || item.operational.workStage === "LOGISTICS")
    .map((item) => item.merchantOrder.id));
  const merchantOrderProcessingNotice = (merchantOrderId: string) =>
    openTaskMerchantOrders.has(merchantOrderId)
      ? <Notice tone="danger"><strong>{l("This MerchantOrder also has an open processing action", "이 MerchantOrder에 열린 처리 행동도 있습니다")}</strong> {l("— only this shop checkout is affected; sibling MerchantOrders continue independently.", "— 이 Shop 결제 단위에만 적용되며 형제 MerchantOrder는 독립적으로 계속됩니다.")} <Link to="/admin/agencyOrder">{l("Go to order operations", "주문 처리로 이동")}</Link></Notice>
      : null;

  const tabs: Array<[ExceptionTab, string, number | undefined]> = [
    ["REFUND", l("Refund review", "환불 심사"), surface.refunds.length],
    ["PAYMENT", l("Payment reconciliation", "결제 대사"), surface.paymentReconciliations.length + surface.paypalResourceAdoptions.length],
    ["DISPUTE", l("PayPal disputes", "PayPal 분쟁"), undefined],
    ["DELIVERY", l("Delivery exceptions", "배송 예외"), surface.exceptions.length],
    ["RETURN", l("Return progress", "회수 진행"), surface.openReturns.length],
    ["INTERVENTION", l("Process intervention", "process 개입"), surface.interventions.length],
    ["RESOLVED", l("Resolved", "처리 완료"), undefined],
  ];

  return <main className="order-ui-operator-console agency-order-operator">
    <PageHeader eyebrow="Exception Ops" title={l("Exception operations", "예외 처리")} description={l("Review payment reconciliation, refunds, and PayPal disputes; resolve delivery exceptions, manage returns, and intervene in stopped orders. Every exception and action is scoped to its exact MerchantOrder unless the card explicitly says it is an order summary.", "결제 대사·환불·PayPal 분쟁 심사, 배송 예외 판정, 회수 진행과 멈춘 주문 개입을 소유합니다. 주문 요약이라고 명시된 카드를 제외하면 모든 예외와 행동은 정확한 MerchantOrder에만 적용됩니다.")} secondaryActions={[{ label: l("Refresh now", "지금 갱신"), onClick: () => void load() }]} summary={<div className="order-ui-page-header-summary"><span>{l("Payment reconciliation", "결제 대사")} <strong>{surface.paymentReconciliations.length + surface.paypalResourceAdoptions.length}</strong></span><span>{l("Refund review", "환불 심사")} <strong>{surface.refunds.length}</strong></span><span>{l("Delivery exceptions", "배송 예외")} <strong>{surface.exceptions.length}</strong></span><span>{l("Returns", "회수")} <strong>{surface.openReturns.length}</strong></span><span>{l("Interventions", "개입")} <strong>{surface.interventions.length}</strong></span></div>} />
    {surface.error ? <Notice announce tone="danger">{surface.error}</Notice> : null}
    {actionError ? <Notice announce tone="danger">{actionError}</Notice> : null}
    {actionReceipt ? <ProcessReceiptNotice receipt={actionReceipt} operator /> : null}
    {receiptRefreshFailed ? <Notice tone="warning">{l("Progress could not be refreshed. Your saved request is still being processed.", "진행 상황을 갱신하지 못했습니다. 저장된 요청은 계속 처리됩니다.")}</Notice> : null}
    <div aria-label={l("Exception view", "예외 처리 보기")} className="workspace-history-views" role="group">{tabs.map(([value, label, count]) => <Button aria-pressed={tab === value} emphasis="quiet" key={value} onClick={() => setTab(value)} size="compact" type="button">{label}{count !== undefined ? <span> {count}</span> : null}</Button>)}</div>

    {tab === "REFUND" ? <section aria-label={l("MerchantOrder refund request review", "MerchantOrder 환불 요청 심사")} className="catalog-ui-refund-queue">
      <PageHeader eyebrow="Refund Review" title={l("Refund request review ({count})", "환불 요청 심사 ({count})", { count: surface.refunds.length })} description={l("Each request covers one whole MerchantOrder and its immutable gross amount, including the allocated Vitlane fee. Record one approval or rejection and one customer-visible rationale. Other MerchantOrders in the AgencyOrder continue independently.", "요청 하나는 MerchantOrder 하나의 불변 총액(배분된 Vitlane 수수료 포함) 전체를 처리합니다. 승인 또는 거절 하나와 고객 공개 근거 하나를 기록하세요. 같은 AgencyOrder의 다른 MerchantOrder는 독립적으로 계속 진행됩니다.")} />
      {surface.refunds.length === 0 ? <FeedbackState description={l("New refund requests appear here.", "새 환불 요청이 접수되면 여기에 표시됩니다.")} state="empty" title={l("No refund requests need review", "심사할 환불 요청이 없습니다")} /> : null}
      {surface.refunds.map((request) => <RefundReviewCard accounting={surface.accountingByOrder[request.agencyOrderId]} can={can} key={request.id} notice={merchantOrderProcessingNotice(request.merchantOrderId)} request={request} run={run} working={working} />)}
    </section> : null}

    {tab === "PAYMENT" ? <PayPalResourceAdoptionPanel
      adoptions={surface.paypalResourceAdoptions}
      can={can}
      reconciliations={surface.paymentReconciliations}
      run={run}
      working={working}
    /> : null}

    {tab === "DISPUTE" ? <PayPalDisputePanel /> : null}

    {tab === "DELIVERY" ? <section aria-label={l("Delivery exception resolution", "배송 예외 판정")} className="catalog-ui-refund-queue">
      <PageHeader eyebrow="Delivery Resolution" title={l("Delivery exception resolution ({count})", "배송 예외 판정 ({count})", { count: surface.exceptions.length })} description={l("These physical units identify missing, incorrect, or lost delivery evidence. A refund decision compensates the unit's entire MerchantOrder gross allocation; it never creates a unit-level amount. An incorrect item can also start a return lane.", "Physical unit은 누락·오배송·분실 배송 evidence를 식별합니다. 환불 판정은 unit 금액을 만들지 않고 해당 unit이 속한 MerchantOrder의 불변 총액 전체를 보상합니다. 오배송은 회수 lane도 시작할 수 있습니다.")} />
      {surface.exceptions.length === 0 ? <FeedbackState description={l("No item units were routed to exceptions during receipt confirmation.", "수령 확인에서 예외로 분기된 물품이 없습니다.")} state="empty" title={l("No exceptions are awaiting a decision", "판정 대기 예외가 없습니다")} /> : null}
      {surface.exceptions.map((unit) => {
        const rationale = deliveryRationales[unit.id] ?? "";
        const rationaleConstraint = textConstraint({ value: rationale, min: 1, max: 2_000, l });
        const rationaleValid = rationaleConstraint.ready;
        return <article key={unit.id}>
        <div><strong>{unit.lineId} #{unit.unitIndex}</strong><span>{exceptionLabel(unit.fulfillment, l)}{l(" · AgencyOrder ", " · AgencyOrder ")}{short(unit.agencyOrderId)}</span></div>
        {merchantOrderProcessingNotice(unit.merchantOrderId)}
        <Field error={rationaleConstraint.error} hint={`${l("Required for both refund and normal-receipt correction. The customer receives this rationale in Messages.", "환불 판정과 정상 수령 정정 모두 필수이며, 고객은 이 근거를 메시지에서 받습니다.")} ${rationaleConstraint.hint}`} id={`delivery-public-rationale-${unit.id}`} label={l("Customer-visible decision rationale", "고객 공개 판정 근거")} required>
          <Textarea maxLength={2_000} onChange={(event) => setDeliveryRationales((current) => ({ ...current, [unit.id]: event.target.value }))} placeholder={l("Record the delivery, tracking, receipt, or recovery facts supporting this decision.", "이 판정을 뒷받침하는 배송·추적·수령·회수 사실을 기록하세요.")} required value={rationale} />
        </Field>
        <div>
          {can("DELIVERY_RESOLUTION", unit.id, "RESOLVE_REFUND") ? <Button busy={working === `${unit.id}:refund`} disabled={!rationaleValid} emphasis="primary" onClick={() => void run(`${unit.id}:refund`, async () => {
            await resolveLogisticsException(unit.id, "REFUND", rationale.trim());
          })} type="button">{l("Refund whole MerchantOrder", "MerchantOrder 전체 환불")}</Button> : null}
          {can("DELIVERY_RESOLUTION", unit.id, "RESOLVE_DELIVERED_OK") ? <Button busy={working === `${unit.id}:ok`} disabled={!rationaleValid} emphasis="secondary" onClick={() => void run(`${unit.id}:ok`, async () => {
            await resolveLogisticsException(unit.id, "DELIVERED_OK", rationale.trim());
          })} type="button">{l("Correct to received normally", "정상 수령 정정")}</Button> : null}
          {can("DELIVERY_RESOLUTION", unit.id, "START_RETURN") ? <Button busy={working === `${unit.id}:return`} emphasis="quiet" onClick={() => void run(`${unit.id}:return`, async () => {
            await createLogisticsReturn(unit.id, invariantContent("오배송 실물 회수"));
          })} type="button">{l("Start return", "회수 시작")}</Button> : null}
        </div>
      </article>;
      })}
    </section> : null}

    {tab === "RETURN" ? <section aria-label={l("Return progress", "회수 진행")} className="catalog-ui-refund-queue">
      <PageHeader eyebrow="Return Progress" title={l("Return progress ({count})", "회수 진행 ({count})", { count: surface.openReturns.length })} description={l("This is the progress lane for returning incorrect items. The next stage is derived only from server-authorized actions. View closed returns in Resolved.", "오배송 실물 회수의 진행 lane입니다. 다음 단계는 서버가 허용한 행동에서만 파생됩니다 — 종결된 회수는 [처리 완료] 탭에서 열람합니다.")} />
      {surface.openReturns.length === 0 ? <FeedbackState description={l("No returns are in progress.", "진행 중인 회수가 없습니다.")} state="empty" title={l("The return lane is empty", "회수 lane이 비어 있습니다")} /> : null}
      {surface.openReturns.map((item) => <article key={item.id}>
        <div><strong>{l("Return {state}", "회수 {state}", { state: item.state })}</strong><span>{l("AgencyOrder {order} · unit {unit} · ", "AgencyOrder {order} · unit {unit} · ", { order: short(item.agencyOrderId), unit: short(item.expectedUnitId) })}<Link to={`/admin/order-accounting?recoveryOrder=${item.agencyOrderId}`}>{l("Record recovery", "회수금 기입")}</Link></span></div>
        <div>
          {(() => {
            // 다음 단계는 서버 actions에서 파생한다 — 화면이 상태 사다리를
            // 소유하지 않는다(ADR-0055 §5).
            const next = returnNextFromActions(surface.actionsByKey[`RETURN_PROGRESS:${item.id}`] ?? [], l);
            return next ? <Button busy={working === `${item.id}:return-next`} emphasis="quiet" onClick={() => void run(`${item.id}:return-next`, async () => {
              await updateLogisticsReturn(item.id, next.state,
                next.state === "MERCHANT_RETURNED" ? "MERCHANT_REFUNDED" : "");
            })} type="button">{l("Return progress", "회수 진행")}: {item.state} → {next.label}</Button> : null;
          })()}
        </div>
      </article>)}
    </section> : null}

    {tab === "INTERVENTION" ? <section aria-label={l("Process intervention", "process 개입")} className="catalog-ui-refund-queue">
      <PageHeader eyebrow="Process Intervention" title={l("Order actions awaiting review ({count})", "확인이 필요한 주문 작업 ({count})", { count: surface.interventions.length })} description={l("Check the waiting reason and retry the existing action. Uncertain payment results must be reconciled using the original payment identity.", "대기 사유를 확인한 뒤 기존 작업을 재시도하세요. 미확정 결제 결과는 원래 결제 식별자로 대사해야 합니다.")} />
      {surface.interventions.length === 0 ? <FeedbackState description={l("No unresolved actions need review.", "확인이 필요한 미해결 작업이 없습니다.")} state="empty" title={l("No actions need intervention", "개입이 필요한 작업이 없습니다")} /> : null}
      {surface.interventions.map((item) => <article key={item.effectId}>
        <div><strong>{item.type}</strong><span>{l("AgencyOrder", "AgencyOrder")} {short(item.agencyOrderId)} · {l("{count} attempts", "시도 {count}회", { count: item.attemptCount })} · {item.lastErrorCode || l("No reason code", "원인 코드 없음")}</span></div>
        <p>{item.guidance?.operatorAction === "RECONCILE_PAYMENT"
          ? l("Confirm the original payment result, then retry this action. Do not create another payment or purchase.", "원래 결제의 결과를 확인한 뒤 이 작업을 재시도하세요. 결제나 구매를 새로 만들지 마세요.")
          : l("Check the recorded waiting reason and the owner's result, then retry the existing action. Retrying does not mark it complete.", "기록된 대기 사유와 담당 영역의 결과를 확인한 뒤 기존 작업을 재시도하세요. 재시도 자체로 작업이 완료되지는 않습니다.")}</p>
        <div>
          {can("PROCESS_INTERVENTION", item.effectId, "RETRY") ? <Button busy={working === `${item.effectId}:retry`} emphasis="primary" onClick={() => void run(`${item.effectId}:retry`, async () => {
            await retryProcessEffect(item.effectId);
          })} type="button">{l("Retry", "재시도")}</Button> : null}

        </div>
      </article>)}
    </section> : null}

    {tab === "RESOLVED" ? <section aria-label={l("Resolved-item audit", "처리 완료 열람")} className="catalog-ui-refund-queue">
      <PageHeader eyebrow="Resolved" title={l("Resolved", "처리 완료")} description={l("Audit view for closed reviews, decisions, and returns. No actions are available.", "종결된 심사·판정·회수의 감사 열람입니다. 행동은 열리지 않습니다.")} />
      {resolvedItems === undefined ? <FeedbackState description={l("Loading closed items.", "종결 항목을 불러오고 있습니다.")} state="loading" title={l("Checking resolved history", "처리 완료 내역 확인 중")} /> : null}
      {resolvedItems?.length === 0 ? <FeedbackState description={l("No exception handling has been closed yet.", "아직 종결된 예외 처리가 없습니다.")} state="empty" title={l("There is no resolved history", "처리 완료 내역이 없습니다")} /> : null}
      {(resolvedItems ?? []).map((item) => <article key={`${item.kind}:${item.id}`}>
        <div><strong>{resolvedKindLabel(item.kind, l)}</strong><span>{l("AgencyOrder", "AgencyOrder")} {short(item.agencyOrderId)} · {resolvedStateLabel(item, l)} · {new Date(item.updatedAt).toLocaleString(locale)}</span></div>
        {item.kind === "DELIVERY_RESOLUTION" && item.detail.resolution ? <dl className="settlement-ui-quote">
          <div><dt>{l("MerchantOrder", "MerchantOrder")}</dt><dd>{item.detail.merchantOrderId}</dd></div>
          <div><dt>{l("Original delivery fact", "원래 배송 사실")}</dt><dd>{exceptionLabel(item.detail.resolution.cause, l)}</dd></div>
          <div><dt>{l("Decision", "판정")}</dt><dd>{item.detail.resolution.decision}</dd></div>
          <div><dt>{l("Customer-visible rationale", "고객 공개 근거")}</dt><dd>{item.detail.resolution.note || l("No rationale recorded", "기록된 근거 없음")}</dd></div>
          <div><dt>{l("Money outcome", "금액 처리")}</dt><dd>{item.detail.compensationAction || l("No compensation", "보상 없음")} · {item.detail.compensationState || l("Not applicable", "해당 없음")}</dd></div>
          {item.detail.returnState ? <div><dt>{l("Return", "회수")}</dt><dd>{item.detail.returnState}</dd></div> : null}
        </dl> : null}
      </article>)}
    </section> : null}
  </main>;
}

// 한 요청은 한 MerchantOrder와 불변 gross allocation 하나다. 운영자는 상품별
// 판정을 만들지 않고 요청 전체에 대해 결정·공개 근거를 한 번 기록한다.
function RefundReviewCard({ request, can, working, run, notice, accounting }: {
  request: OperatorRefundRequest;
  can: (kind: string, id: string, action: string) => boolean;
  working?: string;
  run: (key: string, action: () => Promise<void>) => Promise<void>;
  notice: React.ReactNode;
  accounting?: OrderAccountingProjection;
}) {
  const { l, locale } = useLocale();
  const mayApprove = can("REFUND_REVIEW", request.id, "APPROVE_ITEMS");
  const mayReject = can("REFUND_REVIEW", request.id, "REJECT_ITEMS");
  const reviewable = mayApprove || mayReject;
  const [approve, setApprove] = useState(mayApprove);
  const [publicRationale, setPublicRationale] = useState(request.decisionPublicRationale ?? "");
  const [internalNote, setInternalNote] = useState(request.reviewContext?.internalNote ?? "");
  const publicLength = [...publicRationale.trim()].length;
  const publicConstraint = textConstraint({ value: publicRationale, min: 1, max: 2_000, l });
  const internalLength = [...internalNote.trim()].length;
  const context = request.reviewContext;
  const decisionValid = Boolean(context && context.lines.length > 0) && publicLength >= 1 && publicLength <= 2_000 &&
    internalLength <= 4_000 && (approve ? mayApprove : mayReject);

  return <article className="refund-review-card">
    <header className="refund-review-card__header">
      <div><span>{l("Order / MerchantOrder", "주문 / MerchantOrder")}</span><strong>{context?.orderNumber || request.agencyOrderId}</strong><small>{context?.shopDomain || l("Shop unavailable", "Shop 정보 없음")} · <code>{request.merchantOrderId}</code></small></div>
      <div className="refund-review-card__total"><span>{l("Whole-MO refund", "MO 전체 환불액")}</span><strong>{formatReviewMoney(request.requestedGrossAmount, locale)}</strong><small>{l("Products, shipping, and allocated Vitlane fee included", "상품·배송비·배분된 Vitlane 수수료 포함")}</small></div>
    </header>
    <section aria-label={l("Customer refund rationale", "고객 환불 요청 근거")} className="refund-review-card__request">
      <span>{l("Customer request", "고객 요청")}</span>
      <strong>{refundReasonLabelForOperator(request.reasonCode, l)}</strong>
      <p>{request.publicRationale}</p>
    </section>
    <p className="refund-review-card__scope"><strong>{l("One decision covers the entire MerchantOrder.", "한 번의 판정이 MerchantOrder 전체에 적용됩니다.")}</strong> {l("The full gross allocation above is returned if approved; other MerchantOrders in the AgencyOrder continue independently.", "승인 시 위 불변 총액 전부가 반환되며 같은 AgencyOrder의 다른 MerchantOrder는 독립적으로 계속 진행됩니다.")}</p>
    {notice}
    {accounting ? <OrderAccountingPanel accounting={accounting} compact focusMerchantOrderId={request.merchantOrderId} /> : null}
    {context ? <>
      <dl className="refund-review-evidence__identity">
        <div><dt>{l("Shop", "Shop")}</dt><dd>{context.shopDomain}</dd></div>
        <div><dt>{l("Merchant", "Merchant")}</dt><dd>{context.merchantId || l("Not recorded", "기록 없음")}</dd></div>
        <div><dt>{l("External order", "외부 주문")}</dt><dd>{context.externalOrderRef || l("Not recorded", "기록 없음")}</dd></div>
        <div><dt>{l("MerchantOrder state", "MerchantOrder 상태")}</dt><dd>{context.merchantOrderState}</dd></div>
        <div><dt>{l("Allocation", "Allocation")}</dt><dd><code>{context.allocationId}</code></dd></div>
        <div><dt>{l("Received", "접수 시각")}</dt><dd>{new Date(request.createdAt).toLocaleString(locale)}</dd></div>
      </dl>
      <section className="refund-review-card__products" aria-label={l("Products in this MerchantOrder", "이 MerchantOrder의 상품") }>
        <header><strong>{l("Products covered by this decision", "이 판정에 포함되는 상품")}</strong><small>{l("No line or physical unit carries an independent refund amount.", "상품 line이나 physical unit에는 독립 환불액이 없습니다.")}</small></header>
        <ul>{context.lines.map((line) => {
          const href = safeExternalURL(line.productUrl);
          return <li key={line.lineId}><div><strong>{href ? <a href={href} rel="noreferrer" target="_blank">{line.productTitle}</a> : line.productTitle}</strong><span>{line.variantTitle || l("Default variant", "기본 variant")}{line.selectedOptions.length ? ` · ${line.selectedOptions.join(" · ")}` : ""}</span></div><b>{l("Qty {quantity}", "수량 {quantity}", { quantity: line.quantity })}</b></li>;
        })}</ul>
      </section>
      <section className="refund-review-card__unit-facts" aria-label={l("Logistics facts by physical unit", "Physical unit별 물류 사실") }>
        <header><strong>{l("Logistics facts", "물류 사실")}</strong><small>{l("Units identify delivery and return evidence only.", "Unit은 배송·회수 evidence만 식별합니다.")}</small></header>
        <ul>{context.units.map((unit) => <li key={unit.merchantOrderUnitId}>
          <div><strong>{unit.lineId} #{unit.unitIndex}</strong><span>{unit.disposition}</span></div>
          <dl>
            <Fact label={l("Shipment", "배송")} value={[unit.deliveryFacts.shipmentState, unit.deliveryFacts.carrier, unit.deliveryFacts.trackingRef].filter(Boolean).join(" · ")} empty={l("Not recorded", "기록 없음")} />
            <Fact label={l("Latest event", "최근 이벤트")} value={[unit.deliveryFacts.latestEventStatus, unit.deliveryFacts.latestEventNote].filter(Boolean).join(" · ")} empty={l("Not recorded", "기록 없음")} />
            <Fact label={l("Observed", "관찰 시각")} value={formatReviewDate(unit.deliveryFacts.latestEventOccurredAt, locale)} empty={l("Not recorded", "기록 없음")} />
            <Fact label={l("Return / recovery", "회수") } value={[unit.returnFacts.state, unit.returnFacts.merchantDisposition, unit.returnFacts.note].filter(Boolean).join(" · ")} empty={l("Not recorded", "기록 없음")} />
          </dl>
        </li>)}</ul>
      </section>
    </> : <Notice tone="danger">{l("MerchantOrder evidence is unavailable. Refresh the queue before deciding.", "MerchantOrder evidence를 불러오지 못했습니다. 판정 전에 큐를 새로고침하세요.")}</Notice>}

    {reviewable && request.state !== "RESOLVED" ? <div className="refund-review-decisions">
      <header><div><span>{l("Decision record", "판정 기록")}</span><strong>{l("One decision for this MerchantOrder", "이 MerchantOrder에 대한 단일 판정")}</strong></div><small>{l("The customer-visible rationale is required for approval and rejection. The internal note is retained for operations and accounting only.", "승인·거절 모두 고객 공개 근거가 필수입니다. 내부 메모는 운영·회계 기록에만 남습니다.")}</small></header>
      <div className="refund-review-decision refund-review-decision--whole-mo">
        <Field id={`refund-decision-${request.id}`} label={l("Decision", "판정")} required>
          <NativeSelect onChange={(event) => setApprove(event.target.value === "APPROVE")} required value={approve ? "APPROVE" : "REJECT"}>
            {mayApprove ? <NativeSelectOption value="APPROVE">{l("Approve · refund the whole MO", "승인 · MO 전체 환불")}</NativeSelectOption> : null}
            {mayReject ? <NativeSelectOption value="REJECT">{l("Reject", "거절")}</NativeSelectOption> : null}
          </NativeSelect>
        </Field>
        <Field error={publicConstraint.error} hint={`${l("Shown to the customer with the decision.", "판정과 함께 고객에게 표시됩니다.")} ${publicConstraint.hint}`} id={`refund-public-rationale-${request.id}`} label={l("Customer-visible rationale", "고객 공개 근거")} required>
          <Textarea maxLength={2_000} onChange={(event) => setPublicRationale(event.target.value)} placeholder={approve ? l("Explain why the whole-MO refund is approved.", "MO 전체 환불을 승인한 근거를 설명하세요.") : l("Explain why the request is declined.", "요청을 거절한 근거를 설명하세요.")} required value={publicRationale} />
        </Field>
        <Field hint={l("Optional. Never shown to the customer.", "선택 사항이며 고객에게 표시되지 않습니다.")} id={`refund-internal-note-${request.id}`} label={l("Internal note", "내부 메모")}>
          <Textarea maxLength={4_000} onChange={(event) => setInternalNote(event.target.value)} placeholder={l("Provider reference, recovery follow-up, or accounting note", "provider reference, 회수 후속 조치 또는 회계 메모")} value={internalNote} />
        </Field>
      </div>
      <div className="refund-review-decisions__confirm">
        <Button busy={working === `${request.id}:decide`} disabled={!decisionValid || working === `${request.id}:decide`} emphasis="primary" onClick={() => void run(`${request.id}:decide`, async () => {
          const note = internalNote.trim();
          await decideRefundRequest(request.id, { approve, publicRationale: publicRationale.trim(), ...(note ? { internalNote: note } : {}) });
        })} type="button">{approve ? l("Approve whole-MO refund · {amount}", "MO 전체 환불 승인 · {amount}", { amount: formatReviewMoney(request.requestedGrossAmount, locale) }) : l("Reject refund request", "환불 요청 거절")}</Button>
        <small>{approve ? l("Confirmation starts the exact gross refund to the original payment method.", "확정하면 불변 총액 그대로 원 결제수단 환불을 시작합니다.") : l("No refund executes; the customer receives the rationale above.", "환불은 실행되지 않으며 위 근거가 고객에게 전달됩니다.")}</small>
      </div>
    </div> : null}
  </article>;
}

function Fact({ label, value, empty }: { label: string; value?: string; empty: string }) {
  return <div><dt>{label}</dt><dd>{value || empty}</dd></div>;
}

function formatReviewMoney(money: { amountMinor: number; currency: string }, locale: string) {
  try {
    return new Intl.NumberFormat(locale, { style: "currency", currency: money.currency }).format(money.amountMinor / 100);
  } catch {
    return `${money.currency} ${(money.amountMinor / 100).toFixed(2)}`;
  }
}

function formatReviewDate(value: string | undefined, locale: string) {
  return value ? new Date(value).toLocaleString(locale) : undefined;
}

function safeExternalURL(value: string | undefined) {
  if (!value) return undefined;
  try {
    const url = new URL(value);
    return url.protocol === "https:" || url.protocol === "http:" ? url.toString() : undefined;
  } catch {
    return undefined;
  }
}

function refundReasonLabelForOperator(reasonCode: string, l: Localize) {
  return {
    ITEM_NOT_RECEIVED: l("Item not received", "상품 미수령"),
    ITEM_DAMAGED_DEFECTIVE: l("Damaged or defective item", "파손·불량 상품"),
    WRONG_ITEM_RECEIVED: l("Wrong item received", "다른 상품 수령"),
    ORDER_DELAYED: l("Order delayed", "주문 지연"),
    OTHER_SERVICE_FAULT: l("Other product or service issue", "기타 상품·서비스 문제"),
  }[reasonCode] ?? reasonCode;
}

function exceptionLabel(fulfillment: string, l: Localize) {
  return { MISSING: l("Missing", "누락"), WRONG_ACTUAL: l("Incorrect item", "오배송"), LOST: l("Lost", "분실") }[fulfillment] ?? fulfillment;
}

function resolvedKindLabel(kind: OperatorWorkItem["kind"], l: Localize) {
	  return {
	    PAYMENT_RECONCILIATION: l("Payment reconciliation", "결제 대사"),
	    REFUND_REVIEW: l("Refund review closed", "환불 심사 종결"), DELIVERY_RESOLUTION: l("Delivery exception closed", "배송 예외 판정 종결"),
    RETURN_PROGRESS: l("Return closed", "회수 종결"), PROCUREMENT_EXECUTION: l("Procurement", "조달"), PROCESS_INTERVENTION: l("Process intervention", "process 개입"),
  }[kind];
}

function resolvedStateLabel(item: OperatorWorkItem, l: Localize) {
  if (item.kind === "REFUND_REVIEW") {
    return item.detail.decision === "APPROVED"
      ? l("Whole-MO refund approved", "MO 전체 환불 승인")
      : item.detail.decision === "REJECTED"
        ? l("Refund request declined", "환불 요청 거절")
        : item.state;
  }
  if (item.kind === "DELIVERY_RESOLUTION") {
    return item.state === "NONCONFORMING_RESOLVED" ? l("Validation exception closed", "검증 예외 종결") : l("Decision closed", "판정 종결");
  }
  return item.state;
}

function short(value: string) {
  return value.length > 18 ? `${value.slice(0, 10)}…${value.slice(-6)}` : value;
}
