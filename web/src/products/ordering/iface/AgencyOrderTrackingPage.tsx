import { isProcessReceipt } from "../infra/orderProcessApi";
import { processProgressLabel } from "../app/processPresentation";
import { OrderProcessProgress } from "./OrderProcessProgress";
import { useCallback, useEffect, useState } from "react";
import { ReceiptText } from "lucide-react";
import { Link, useParams } from "react-router";
import { Button, buttonClassName, Disclosure, Field, FeedbackState, NativeSelect, NativeSelectOption, Notice, PageHeader, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Textarea, Chip } from "../../../shared/ui";
import { textConstraint } from "../../../shared/forms/textConstraint";
import { useLocale, type Localize } from "../../../shared/i18n";
import {
  cancelAgencyOrder,
  cancelAgencyOrderDelayRule,
  customerActionOf,
  type RefundReasonCode,
  getAgencyOrder,
  getAgencyOrderReceipt,
  listAgencyOrders,
  requestAgencyOrderRefund,
  revealAgencyOrderShipping,
  type AgencyOrderListCounts,
  type AgencyOrderListSort,
  type AgencyOrderListView,
  type AgencyOrderProjection,
  type AgencyOrderReceipt,
  type MerchantOrderSummary,
} from "../infra/agencyOrderApi";
import { fulfillmentExceptionKinds, fulfillmentLabel, merchantOrderOperationalStageLabel, unitStageLabel } from "./unitStageCopy";
import { compensationStateLabel, disputeOutcomeLabel, disputeStateLabel, fundingStateLabel, merchantOrderStateLabel, paymentStateLabel, refundRequestStateLabel, stateTone } from "./stateChipCopy";
import { PaymentModeBadge } from "./PaymentModeBadge";
import "./agency-order.css";

type RevealedAddress = Awaited<ReturnType<typeof revealAgencyOrderShipping>>["address"];

const emptyCounts: AgencyOrderListCounts = {
  PAYMENT_REQUIRED: 0,
  IN_PROGRESS: 0,
  NEEDS_ATTENTION: 0,
  FINISHED: 0,
  ALL: 0,
};

export function AgencyOrderTrackingPage() {
  const { l } = useLocale();
  const { agencyOrderId } = useParams();
  const [orders, setOrders] = useState<AgencyOrderProjection[]>([]);
  const [counts, setCounts] = useState<AgencyOrderListCounts>(emptyCounts);
  const [view, setView] = useState<AgencyOrderListView>("IN_PROGRESS");
  const [sort, setSort] = useState<AgencyOrderListSort>("UPDATED_DESC");
  const [nextCursor, setNextCursor] = useState<string>();
  const [loadingMore, setLoadingMore] = useState(false);
  const [paginationExpanded, setPaginationExpanded] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  const load = useCallback(async (quiet = false) => {
    if (!quiet) setLoading(true);
    try {
      if (agencyOrderId) {
        const result = await getAgencyOrder(agencyOrderId);
        setOrders([result.agencyOrder]);
        setCounts((current) => ({ ...current, ALL: Math.max(1, current.ALL) }));
      } else {
        const result = await listAgencyOrders({ view, sort, limit: 30 });
        setOrders(result.agencyOrders);
        setCounts(result.countsByView);
        setNextCursor(result.nextCursor);
      }
      setError(undefined);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : l("We couldn't load order progress.", "주문 진행 상태를 불러오지 못했습니다."));
    } finally {
      if (!quiet) setLoading(false);
    }
  }, [agencyOrderId, l, sort, view]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (paginationExpanded) return;
    const timer = window.setInterval(() => void load(true), 5000);
    return () => window.clearInterval(timer);
  }, [load, paginationExpanded]);

  useEffect(() => {
    setPaginationExpanded(false);
  }, [sort, view]);

  async function loadMoreOrders() {
    if (!nextCursor || loadingMore) return;
    setLoadingMore(true);
    try {
      const result = await listAgencyOrders({ view, sort, limit: 30, cursor: nextCursor });
      setOrders((current) => [...current, ...result.agencyOrders]);
      setCounts(result.countsByView);
      setNextCursor(result.nextCursor);
      setPaginationExpanded(true);
    } catch {
      setError(l("We couldn't load more AgencyOrder history.", "다음 AgencyOrder 내역을 불러오지 못했습니다."));
    } finally {
      setLoadingMore(false);
    }
  }

  const orderViews: Array<{ value: AgencyOrderListView; label: string }> = [
    { value: "IN_PROGRESS", label: l("In progress", "진행 중") },
    { value: "PAYMENT_REQUIRED", label: l("Payment required", "결제 필요") },
    { value: "NEEDS_ATTENTION", label: l("Needs attention", "확인 필요") },
    { value: "FINISHED", label: l("Finished", "완료 내역") },
    { value: "ALL", label: l("All", "전체") },
  ];

  const header = <PageHeader
    eyebrow={l("After AgencyOrder", "AgencyOrder 이후")}
    title={agencyOrderId ? l("Agency-order processing status", "구매대행 주문 처리 현황") : l("Orders, payment, and agency processing", "주문·결제와 구매대행 처리")}
    description={l("After payment, Vitlane continues verification and processing for each shop. Closing this page does not stop progress.", "결제를 마치면 Vitlane이 확인과 Shop별 처리를 이어갑니다. 이 페이지를 닫아도 진행은 멈추지 않습니다.")}
    secondaryActions={[{ label: l("Refresh now", "지금 갱신"), disabled: loading, onClick: () => void load() }]}
    summary={<div className="order-ui-page-header-summary">
      <span>{l("In progress", "진행 중")} <strong>{counts.IN_PROGRESS}</strong></span>
      <span>{l("Payment required", "결제 필요")} <strong>{counts.PAYMENT_REQUIRED}</strong></span>
      <span>{l("Needs attention", "확인 필요")} <strong>{counts.NEEDS_ATTENTION}</strong></span>
      <span>{l("Finished", "완료 내역")} <strong>{counts.FINISHED}</strong></span>
    </div>}
  />;

  if (agencyOrderId) {
    return <div className="agency-order-tracking-shell order-ui-tracking-page agency-order-tracking">
      {header}
      {error && <Notice announce tone="danger">{error}</Notice>}
      {loading && <FeedbackState description={l("Loading the order snapshot and payment and processing records.", "주문 snapshot과 결제·처리 기록을 불러오고 있습니다.")} state="loading" title={l("Checking the AgencyOrder", "AgencyOrder를 확인하고 있습니다")} />}
      {!loading && orders.map((order) => <AgencyOrderActivityRow detail key={order.agencyOrder.id} projection={order} />)}
      <Link className="order-ui-primary-link" to="/agencyOrder">{l("View all AgencyOrders", "전체 AgencyOrder 보기")}</Link>
    </div>;
  }

  return <div className="agency-order-tracking-shell order-ui-tracking-page agency-order-tracking">
    {header}
    {error && <Notice announce tone="danger">{error}</Notice>}
    {loading && <FeedbackState description={l("Loading payment and per-shop processing information.", "결제와 Shop별 처리 정보를 불러오고 있습니다.")} state="loading" title={l("Checking progress", "진행 상태를 확인하고 있습니다")} />}
    {!loading && <section id="agency-order-tracking-panel">
      <ListToolbar
        label={l("AgencyOrder status", "AgencyOrder 상태")}
        options={orderViews}
        counts={counts}
        value={view}
        onView={(next) => setView(next as AgencyOrderListView)}
        sort={sort}
        onSort={(next) => setSort(next as AgencyOrderListSort)}
      />
      {orders.length === 0
        ? <TrackingEmpty title={l("No AgencyOrders match this view", "이 보기에 해당하는 AgencyOrder가 없습니다")} description={l("Choose another status view or create an order sheet from a curation cart.", "다른 상태 보기를 선택하거나 큐레이션 장바구니에서 주문서를 작성해 보세요.")} />
        : <>
          <header className="product-ui-agency-order-detail-heading"><p>{l("One AgencyOrder · one-line summary", "AgencyOrder 하나 · 한 줄 요약")}</p><h2>{l("{status} orders", "{status} 주문 내역", { status: orderViews.find((item) => item.value === view)?.label ?? "" })}</h2></header>
          <div className="tracking-list">{orders.map((order) => <AgencyOrderActivityRow key={order.agencyOrder.id} projection={order} />)}</div>
          {nextCursor && <Button busy={loadingMore} onClick={() => void loadMoreOrders()}>{l("Show more", "더 보기")}</Button>}
        </>}
    </section>}
  </div>;
}

function ListToolbar<T extends string>({ label, options, counts, value, onView, sort, onSort }: {
  label: string;
  options: Array<{ value: T; label: string }>;
  counts: Record<T, number>;
  value: T;
  onView: (value: T) => void;
  sort: string;
  onSort: (value: string) => void;
}) {
  const { l } = useLocale();
  return <div className="workspace-history-toolbar">
    <div aria-label={label} className="workspace-history-views" role="group">
      {options.map((option) => <Button aria-pressed={value === option.value} emphasis="quiet" key={option.value} onClick={() => onView(option.value)} size="compact" type="button">{option.label} <span>{counts[option.value]}</span></Button>)}
    </div>
    <div className="workspace-history-sort"><span>{l("Sort", "정렬")}</span><Select onValueChange={onSort} value={sort}><SelectTrigger aria-label={l("Sort history", "내역 정렬")} className="workspace-history-sort__trigger"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="UPDATED_DESC">{l("Recently updated", "최근 변경순")}</SelectItem><SelectItem value="CREATED_DESC">{l("Recently created", "최근 생성순")}</SelectItem><SelectItem value="CREATED_ASC">{l("Oldest created", "오래된 생성순")}</SelectItem></SelectContent></Select></div>
  </div>;
}

function TrackingEmpty({ description, title }: { description: string; title: string }) {
  const { l } = useLocale();
  return <div className="order-ui-empty-with-link"><FeedbackState description={description} state="empty" title={title} /><Link className="order-ui-primary-link" to="/">{l("Find products", "상품 찾기")}</Link></div>;
}

function AgencyOrderActivityRow({ projection, detail = false }: { projection: AgencyOrderProjection; detail?: boolean }) {
  const { l, locale } = useLocale();
  const order = projection.agencyOrder;
  const line = order.lines[0];
  const terminal = projection.process.state === "TERMINAL";
  const [shipping, setShipping] = useState<RevealedAddress>();
  const [shippingBusy, setShippingBusy] = useState(false);
  const [shippingError, setShippingError] = useState<string>();
  const receiptEligible = terminal &&
    ["COMPLETED_ALL", "COMPLETED_PARTIAL", "REFUNDED_ALL"].includes(projection.process.terminalReason ?? "") &&
    (projection.payment?.rail !== "GIWA" || projection.payment.state === "COMPLETED" ||
      projection.process.terminalReason === "REFUNDED_ALL");
  const [receipt, setReceipt] = useState<AgencyOrderReceipt>();
  const [receiptPending, setReceiptPending] = useState(false);
  const liveReceipt = receipt?.kind === "LIVE_ORDER_RECORD";

  useEffect(() => {
    if (!receiptEligible || receipt) return;
    let active = true;
    setReceiptPending(true);
    getAgencyOrderReceipt(order.id)
      .then((result) => { if (active) setReceipt(result.receipt); })
      // 영수증은 finality 뒤 Owner가 발급하므로 잠시 404일 수 있다.
      // 부모의 5초 조회로 새 projection을 받으면 다시 확인한다.
      .catch(() => undefined)
      .finally(() => { if (active) setReceiptPending(false); });
    return () => { active = false; };
  }, [receiptEligible, receipt, order.id, projection]);

  async function toggleShipping() {
    if (shipping) {
      setShipping(undefined);
      return;
    }
    setShippingBusy(true);
    setShippingError(undefined);
    try {
      setShipping((await revealAgencyOrderShipping(order.id)).address);
    } catch {
      setShippingError(l("We couldn't reveal the shipping information fixed to this AgencyOrder.", "이 AgencyOrder에 고정된 배송정보를 확인하지 못했습니다."));
    } finally {
      setShippingBusy(false);
    }
  }

  return <article className={`tracking-card ${terminal ? "is-terminal" : ""}`}>
    <Disclosure className="workspace-agency-order-disclosure" contentClassName="workspace-agency-order-detail" defaultOpen={detail} summary={<span className="workspace-agency-order-row"><span className="workspace-agency-order-row__media">{line?.imageUrl ? <img alt="" loading="lazy" src={line.imageUrl} /> : <span aria-hidden="true">{l("V", "V")}</span>}</span><span className="workspace-agency-order-row__identity"><small>{line?.shopDomain || l("Vitlane Agency", "Vitlane Agency")}</small><strong>{order.lines.length > 1 ? l("{title} and {count} more", "{title} 외 {count}건", { title: line?.productTitle || l("AgencyOrder", "AgencyOrder"), count: order.lines.length - 1 }) : line?.productTitle || l("AgencyOrder", "AgencyOrder")}</strong><span>{optionCopy(line?.selectedOptions, l)} · {formatMoney(order.customerPayableTotal)}</span></span><span className="workspace-agency-order-row__status"><strong>{statusCopy(projection, l)}</strong><time dateTime={projection.process.updatedAt}>{formatCompactTime(projection.process.updatedAt, locale)}</time></span></span>}>
      <div className="workspace-agency-order-detail__actions"><div>{line?.productUrl ? <a href={line.productUrl} target="_blank" rel="noreferrer">{l("Open product page", "상품 페이지 열기")}</a> : <span>{l("No product URL", "상품 URL 없음")}</span>}<span>{l("{count} products", "상품 {count}개", { count: order.lines.length })} · {order.shippingAddress.country} · {order.shippingAddress.maskedSummary}</span></div><Link className={buttonClassName({ emphasis: "secondary", size: "compact" })} to={`/agencyOrder/${order.id}/payment`}><ReceiptText aria-hidden="true" /> {l("Payment details", "결제 상세")}</Link></div>
      <ProcessRail projection={projection} />
      {detail ? <OrderProcessProgress orderId={order.id} shops={projection.merchantOrders ?? []} /> : null}
      <MerchantOrderResolutionSection projection={projection} />
      <div className="workspace-agency-order-detail__shipping"><div className="account-ui-tracking-shipping">{shipping && <address><span>{shipping.recipientName}</span><span>{addressLines(shipping)}</span>{shipping.phone && <span>{shipping.phone}</span>}</address>}<Button busy={shippingBusy} onClick={() => void toggleShipping()} size="compact">{shipping ? l("Hide shipping information", "배송정보 가리기") : l("Reveal order shipping information", "주문 시 배송정보 보기")}</Button>{shippingError && <small role="alert">{shippingError}</small>}</div></div>
      {receiptEligible ? (
        receipt ? (
          <section className={`agency-order-receipt ${liveReceipt ? "agency-order-receipt--live" : ""}`} aria-label={liveReceipt ? l("PayPal Live order record", "PayPal Live 주문 처리 기록") : l("TEST receipt", "TEST 영수증")}>
            <header><ReceiptText aria-hidden="true" /><strong>{liveReceipt
              ? receiptRefunded(receipt) ? l("Refunded PayPal Live order record", "환불된 PayPal Live 주문 처리 기록") : l("PayPal Live order record", "PayPal Live 주문 처리 기록")
              : receiptRefunded(receipt) ? l("Refund TEST receipt", "환불 TEST 영수증") : l("TEST receipt", "TEST 영수증")}</strong><span>{liveReceipt
              ? receipt.legalSale
                ? l("Real money was processed and a merchant purchase was recorded. This Vitlane record is not the merchant's tax or legal receipt.", "실제 금액이 처리되고 판매처 구매가 기록되었습니다. 이 Vitlane 기록은 판매처의 세금·법적 영수증을 대신하지 않습니다.")
                : l("Real money was processed, but no completed merchant purchase was recorded. This Vitlane record is not the merchant's tax or legal receipt.", "실제 금액이 처리되었지만 완료된 판매처 구매는 기록되지 않았습니다. 이 Vitlane 기록은 판매처의 세금·법적 영수증을 대신하지 않습니다.")
              : l("This is a TEST settlement record, not a real sale.", "실제 판매가 아닌 TEST 정산 기록입니다.")}</span></header>
            <dl>
              <div><dt>{liveReceipt ? l("Order result", "주문 처리 결과") : l("Settlement result", "정산 결과")}</dt><dd>{receiptRefunded(receipt) ? l("Fully refunded", "전액 환불") : receipt.terminalState === "COMPLETED_PARTIAL" ? l("Settled · includes partial refund", "정산 완료 · 부분 환불 포함") : l("Settled", "정산 완료")}</dd></div>
              <div><dt>{l("Environment", "환경")}</dt><dd>{receipt.paymentRail} · {receipt.providerEnvironment} · {receipt.economicEffect}</dd></div>
              <div><dt>{l("Receipt hash", "영수증 hash")}</dt><dd><code>{receipt.receiptHash}</code></dd></div>
              <div><dt>{receipt.paymentRail === "PAYPAL" ? l("Provider reference", "결제 제공자 참조") : l("Settlement tx", "정산 tx")}</dt><dd><code>{receipt.terminalTxHash}</code></dd></div>
              <div><dt>{l("Issued", "발급 시각")}</dt><dd><time dateTime={receipt.createdAt}>{new Date(receipt.createdAt).toLocaleString(locale)}</time></dd></div>
            </dl>
          </section>
        ) : (
          <p className="agency-order-receipt agency-order-receipt--pending" role="status">
            {receiptPending ? l("Checking the receipt…", "영수증을 확인하고 있습니다…") : l("The receipt is being issued. Refresh shortly.", "영수증을 발급하고 있습니다. 잠시 후 새로고침해 주세요.")}
          </p>
        )
      ) : null}
      {detail && projection.shipments?.length ? <ShipmentsSection projection={projection} /> : null}
      {projection.notices?.length ? (
        <section aria-label={l("Order updates", "주문 안내")} className="agency-order-notices">
          <ul className="agency-order-card__updates">
            {projection.notices.map((notice) => (
              <li key={notice.id}>
                <b>{notice.kind === "OPERATOR" ? l("Operator update", "담당자 안내") : l("Update", "안내")}</b>{" "}{notice.body}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {projection.process.state === "ATTENTION_REQUIRED" && projection.process.lastReasonCode ? <Notice announce tone="danger">{l("Needs attention: ", "확인이 필요합니다: ")}{reasonCodeMessage(projection.process.lastReasonCode, projection, l)}</Notice> : null}
      {!detail && <Link className="order-ui-primary-link" to={`/agencyOrder/${order.id}`}>{l("View processing details", "처리 현황 자세히 보기")}</Link>}
    </Disclosure>
  </article>;
}

// ProcessRail은 process.state의 단일 선형 사영이다(ADR-0057 §6 — 이원
// 시각화 폐기). RESOLUTION·ATTENTION은 rail을 멈추지 않고 경고 뱃지로
// 얹는다 — 정확한 국면은 headline(statusCopy)이 이미 말한다.
const railIndexByState: Record<AgencyOrderProjection["process"]["state"], number> = {
  WAITING_CUSTOMER_PAYMENT: 0,
  PAYMENT_RECONCILIATION: 1,
  PROCUREMENT_IN_PROGRESS: 2,
  LOGISTICS_IN_PROGRESS: 3,
  RESOLUTION_IN_PROGRESS: 3,
  ATTENTION_REQUIRED: 3,
  TERMINAL: 5,
};

function ProcessRail({ projection }: { projection: AgencyOrderProjection }) {
  const { l } = useLocale();
  const railNodes = [
    l("Payment", "결제"),
    l("Verification", "확인"),
    l("Shop order", "판매처 주문"),
    l("Shipping", "배송"),
    l("Complete", "완료"),
  ] as const;
  const state = projection.process.state;
  const current = railIndexByState[state];
  const warn = state === "RESOLUTION_IN_PROGRESS" || state === "ATTENTION_REQUIRED";
  return <section className="agency-order-rail" aria-label={l("Order progress", "주문 진행 단계")}>
    <ol className="order-ui-tracking-lane">
      {railNodes.map((label, index) => (
        <li className={index < current ? "is-done" : index === current ? "is-current" : ""} key={label}>
          <span>{index < current ? "✓" : index + 1}</span>
          <div><b>{label}</b></div>
        </li>
      ))}
    </ol>
    <footer className="agency-order-rail__meta">
      {warn ? <Chip tone={state === "ATTENTION_REQUIRED" ? "failed" : "progress"}>{statusCopy(projection, l)}</Chip> : null}
      <PaymentModeBadge selection={projection.paymentInstruction?.paymentSelection ?? projection.agencyOrder.paymentSelection} />
      <small>{paymentSummaryCopy(projection, l)}</small>
    </footer>
  </section>;
}

// 결제 세부(tx·capture)는 결제 상세 페이지 전담이다(ADR-0057 D4) — 카드에는
// 수단·국면 한 줄만 남긴다.
function paymentSummaryCopy(projection: AgencyOrderProjection, l: Localize) {
  const payment = projection.payment;
  if (!payment) return l("Payment method: not started", "결제 수단: 진행 전");
  if (payment.rail === "PAYPAL") {
    const selection = projection.paymentInstruction?.paymentSelection ??
      projection.agencyOrder.paymentSelection;
    const label = payment.state === "AUTHORIZED" ? l("Authorized · captured per shop at purchase start", "승인 완료 · Shop별 구매 시작 시 캡처")
      : payment.state === "PARTIALLY_CAPTURED" ? l("Purchasing · some shop amounts captured", "구매 진행 · 일부 Shop 금액 캡처")
      : payment.state === "CAPTURED" ? l("All shop amounts captured", "모든 Shop 금액 캡처")
		: payment.state === "ACTION_REQUIRED" ? l("Awaiting approval", "승인 대기")
		: ["PROCESSING", "OUTCOME_UNKNOWN"].includes(payment.state) ? l("Verifying authorization", "승인 확인 중")
		: payment.state === "CLOSED" ? l("Funding closed", "자금 처리 종료")
      : payment.state;
    return selection.providerEnvironment === "LIVE"
      ? l("PayPal Live · {status} · real-money payment", "PayPal Live · {status} · 실제 금액 결제", { status: label })
      : l("PayPal Sandbox · {status} · no real charge", "PayPal Sandbox · {status} · 실제 청구 없음", { status: label });
  }
  return `tVITUSD · ${paymentStateLabel(payment.state, l)}`;
}

// 고객 금액과 고객 행동의 경계는 불변 MerchantOrder allocation이다. Physical
// unit은 아래 물류 상세에만 남으며 취소·환불 금액을 소유하지 않는다.
function MerchantOrderResolutionSection({ projection: initial }: { projection: AgencyOrderProjection }) {
  const { l } = useLocale();
  const [projection, setProjection] = useState(initial);
  useEffect(() => setProjection(initial), [initial]);
  if (projection.merchantOrders.length === 0) return null;

  async function refresh() {
    const response = await getAgencyOrder(projection.agencyOrder.id);
    setProjection(response.agencyOrder);
  }

  return <section className="merchant-order-resolution" aria-labelledby={`merchant-orders-${projection.agencyOrder.id}`}>
    <header>
      <div><span>{l("Shop checkouts", "Shop별 결제 단위")}</span><strong id={`merchant-orders-${projection.agencyOrder.id}`}>{l("Shop orders and resolution", "Shop별 구매·해결 현황")}</strong></div>
      <small>{l("Each card is one MerchantOrder. Cancellation and refunds always cover every product in that card and include its allocated Vitlane fee.", "카드 하나가 MerchantOrder 하나입니다. 취소·환불은 카드 안의 모든 상품과 배분된 Vitlane 수수료를 항상 함께 처리합니다.")}</small>
    </header>
    <div className="merchant-order-resolution__list">
      {projection.merchantOrders.map((merchantOrder) => <MerchantOrderResolutionCard key={merchantOrder.id} merchantOrder={merchantOrder} projection={projection} refresh={refresh} />)}
    </div>
  </section>;
}

function MerchantOrderResolutionCard({ merchantOrder, projection, refresh }: {
  merchantOrder: MerchantOrderSummary;
  projection: AgencyOrderProjection;
  refresh: () => Promise<void>;
}) {
  const { l } = useLocale();
  const [reasonCode, setReasonCode] = useState<RefundReasonCode | "">("");
  const [publicRationale, setPublicRationale] = useState("");
  const [working, setWorking] = useState<"cancel" | "delay" | "refund">();
  const [feedback, setFeedback] = useState<{ tone: "ok" | "error"; message: string }>();
  const cancelEligible = customerActionOf(projection, "CANCEL_PRE_EFFECT")?.eligibleMerchantOrderIds?.includes(merchantOrder.id) ?? false;
  const delayEligible = customerActionOf(projection, "CANCEL_DELAY_RULE")?.eligibleMerchantOrderIds?.includes(merchantOrder.id) ?? false;
  const refundAction = customerActionOf(projection, "REQUEST_REFUND");
  const refundEligible = refundAction?.eligibleMerchantOrderIds?.includes(merchantOrder.id) ?? false;
  const requests = (projection.refundRequests ?? []).filter((request) => request.merchantOrderId === merchantOrder.id);
  const rationaleConstraint = textConstraint({ value: publicRationale, min: 1, max: 500, l });
  const rationaleValid = rationaleConstraint.ready;
  const products = merchantOrderProducts(projection, merchantOrder);
  const refundDestination = projection.payment?.rail === "PAYPAL" ? "PayPal" : l("tVITUSD wallet", "tVITUSD 지갑");

  async function run(kind: "cancel" | "delay" | "refund", action: () => Promise<unknown>, success: string) {
    if (working) return;
    setWorking(kind);
    setFeedback(undefined);
    try {
      const outcome = await action();
      await refresh();
      if (isProcessReceipt(outcome)) {
        setFeedback({ tone: outcome.outcome === "REJECTED" ? "error" : "ok", message: processProgressLabel(outcome, l) });
      } else { setFeedback({ tone: "ok", message: success }); }
      if (kind === "refund") {
        setReasonCode("");
        setPublicRationale("");
      }
    } catch {
      setFeedback({ tone: "error", message: l("This request could not be submitted. Refresh the order and check whether this shop checkout has already moved to another stage.", "요청을 접수하지 못했습니다. 주문을 새로고침하고 이 Shop 결제 단위가 이미 다른 단계로 이동했는지 확인해 주세요.") });
    } finally {
      setWorking(undefined);
    }
  }

  return <article className="merchant-order-card">
    <MerchantOrderStatusRail merchantOrder={merchantOrder} />
    <div className="merchant-order-card__body">
      <header>
        <div><span>{l("SHOP {ordinal}", "SHOP {ordinal}", { ordinal: merchantOrder.checkoutOrdinal })}</span><h3>{merchantOrder.shopDomain}</h3><small>{l("MerchantOrder", "MerchantOrder")} <code>{merchantOrder.id}</code></small></div>
        <div className="merchant-order-card__amount"><span>{l("Whole-MO refund amount", "MO 전체 환불액")}</span><strong>{formatMoney(merchantOrder.customerGrossAmount)}</strong><small>{l("Products, shipping, and allocated fee included", "상품·배송비·배분 수수료 포함")}</small></div>
      </header>
      <div className="merchant-order-card__badges" aria-label={l("MerchantOrder status", "MerchantOrder 상태")}>
        <Chip>{l("Current stage", "현재 단계")} · {merchantOrderOperationalStageLabel(merchantOrder.operational.stage, l)}</Chip>
        <Chip tone={merchantOrder.fundingState ? stateTone(merchantOrder.fundingState) : "waiting"}>{l("Funding", "자금")} · {merchantOrder.fundingState ? fundingStateLabel(merchantOrder.fundingState, l) : l("Not created", "생성 전")}</Chip>
        <Chip tone={stateTone(merchantOrder.state)}>{l("Shop order", "판매처 주문")} · {merchantOrderStateLabel(merchantOrder.state, l)}</Chip>
        {merchantOrder.cancelIntent ? <Chip>{cancelIntentLabel(merchantOrder.cancelIntent, l)}</Chip> : null}
        {merchantOrder.refundRequestState ? <Chip tone={stateTone(merchantOrder.refundRequestState)}>{l("Refund review", "환불 심사")} · {refundRequestStateLabel(merchantOrder.refundRequestState, l)}</Chip> : null}
        {merchantOrder.compensationState ? <Chip tone={stateTone(merchantOrder.compensationState)}>{l("Money return", "금액 반환")} · {compensationStateLabel(merchantOrder.compensationState, l)}</Chip> : null}
        {merchantOrder.disputeState ? <Chip tone={stateTone(merchantOrder.disputeState)}>{l("PayPal dispute fact", "PayPal 분쟁 사실")} · {disputeStateLabel(merchantOrder.disputeState, l)}{merchantOrder.disputeOutcome ? ` · ${disputeOutcomeLabel(merchantOrder.disputeOutcome, l)}` : ""}</Chip> : null}
      </div>
      <ul className="merchant-order-card__products">
        {products.map(({ line, quantity }) => <li key={line.lineId}><div><strong>{line.productTitle}</strong><span>{line.variantTitle || optionCopy(line.selectedOptions, l)}</span></div><b>{l("Qty {quantity}", "수량 {quantity}", { quantity })}</b></li>)}
      </ul>
      <ul className="merchant-order-card__products merchant-order-card__units" aria-label={l("Unit status", "상품 단위 상태")}>
        {projection.units.filter((unit) => unit.merchantOrderId === merchantOrder.id).map((unit) => <li key={unit.merchantOrderUnitId}>
          <span>{unit.lineId} #{unit.unitIndex}</span>
          <strong>{unitStageLabel(unit.stage, l)}</strong>
          {unit.resolutionCause ? <small>{l("Delivery fact", "배송 사실")} · {fulfillmentLabel(unit.resolutionCause, l)}</small> : null}
          {unit.resolutionDecision ? <small>{l("Decision", "판정")} · {unit.resolutionDecision}</small> : null}
        </li>)}
      </ul>
      {cancelEligible || delayEligible ? <div className="merchant-order-card__actions">
        {cancelEligible ? <Button busy={working === "cancel"} disabled={Boolean(working)} emphasis="secondary" onClick={() => void run("cancel", () => cancelAgencyOrder(projection.agencyOrder.id, merchantOrder.id), l("Cancellation received for this entire shop checkout. Its full gross amount will return to the original payment method.", "이 Shop 결제 단위 전체의 취소를 접수했습니다. 총액 전부가 원 결제수단으로 반환됩니다."))} type="button">{l("Cancel this shop checkout before purchase", "구매 전 이 Shop 결제 단위 취소")}</Button> : null}
        {delayEligible ? <Button busy={working === "delay"} disabled={Boolean(working)} emphasis="secondary" onClick={() => void run("delay", () => cancelAgencyOrderDelayRule(projection.agencyOrder.id, merchantOrder.id), l("The 30-day cancellation was received for this entire shop checkout.", "이 Shop 결제 단위 전체의 30일 지연 취소를 접수했습니다."))} type="button">{l("Cancel this delayed shop checkout", "이 Shop 결제 단위 지연 취소")}</Button> : null}
        <small>{l("The displayed gross amount includes the fee allocated to this MerchantOrder. No product inside it can be cancelled separately at this stage. Products in other shop checkouts continue independently.", "표시된 총액에는 이 MerchantOrder에 배분된 수수료가 포함됩니다. 현재 단계에서는 내부 상품 일부만 따로 취소할 수 없습니다. 다른 Shop 결제 단위의 상품은 그대로 진행됩니다.")}</small>
      </div> : null}

      {refundEligible ? <div className="merchant-order-card__refund-form">
        <NativeSelect aria-label={l("Refund reason for {shop}", "{shop} 환불 사유", { shop: merchantOrder.shopDomain })} onChange={(event) => setReasonCode(event.target.value as RefundReasonCode | "")} value={reasonCode}>
          <NativeSelectOption value="">{l("Choose a refund reason (required)", "환불 사유 선택 (필수)")}</NativeSelectOption>
          {refundReasonOptions(l).filter(([code]) => (refundAction?.reasonCodes ?? []).includes(code)).map(([code, label]) => <NativeSelectOption key={code} value={code}>{label}</NativeSelectOption>)}
        </NativeSelect>
        <Field error={rationaleConstraint.error} hint={`${l("The reviewer and your decision history will see this explanation.", "심사 담당자와 내 결정 내역에 이 설명이 표시됩니다.")} ${rationaleConstraint.hint}`} id={`refund-rationale-${merchantOrder.id}`} label={l("What happened to this shop checkout?", "이 Shop 결제 단위에 무슨 일이 있었나요?")} required>
          <Textarea maxLength={500} onChange={(event) => setPublicRationale(event.target.value)} placeholder={l("Describe the product or service issue and relevant delivery facts.", "상품·서비스 문제와 관련 배송 사실을 구체적으로 적어주세요.")} required value={publicRationale} />
        </Field>
        <Button busy={working === "refund"} disabled={Boolean(working) || reasonCode === "" || !rationaleValid} emphasis="secondary" onClick={() => void run("refund", () => requestAgencyOrderRefund(projection.agencyOrder.id, merchantOrder.id, reasonCode as RefundReasonCode, publicRationale.trim()), l("Refund review requested for this whole shop checkout. If approved, {amount} returns to {destination}.", "이 Shop 결제 단위 전체의 환불 심사를 요청했습니다. 승인되면 {amount}이(가) {destination}(으)로 반환됩니다.", { amount: formatMoney(merchantOrder.customerGrossAmount), destination: refundDestination }))} type="button">{l("Request whole-MO refund · {amount}", "MO 전체 환불 요청 · {amount}", { amount: formatMoney(merchantOrder.customerGrossAmount) })}</Button>
        <small>{l("Change-of-mind refunds are unavailable after purchase. The request covers every product above; partial product refunds are not supported before Live launch. Products in other shop checkouts continue independently.", "구매 시작 후 단순변심 환불은 제공되지 않습니다. 요청은 위 상품 전체를 포함하며 Live 출시 전에는 일부 상품 환불을 지원하지 않습니다. 다른 Shop 결제 단위의 상품은 그대로 진행됩니다.")}</small>
      </div> : null}

      {requests.map((request) => <section className="merchant-order-card__decision" key={request.id}>
        <header><strong>{request.state === "RESOLVED" ? l("Refund review complete", "환불 심사 완료") : l("Refund review pending", "환불 심사 대기")}</strong><span>{formatMoney(request.requestedGrossAmount)} · {refundReasonLabel(request.reasonCode, l)}</span></header>
        <p><strong>{l("Your explanation", "내가 제출한 설명")}</strong><span>{request.publicRationale}</span></p>
        {request.decision ? <p><strong>{request.decision === "APPROVED" ? l("Refund approved", "환불 승인") : l("Refund declined", "환불 거절")}</strong><span>{request.decisionPublicRationale || l("No decision rationale was recorded.", "기록된 판단 근거가 없습니다.")}</span></p> : null}
      </section>)}
      {feedback ? <Notice announce tone={feedback.tone === "ok" ? "neutral" : "danger"}>{feedback.message}</Notice> : null}
    </div>
  </article>;
}

// 취소 intent의 리듀서 판정(ADR-0070 §4.3) — 접수 직후의 "처리 중"과 경합 패배의
// "취소되지 않음"을 같은 카드에서 보여준다.
function cancelIntentLabel(intent: NonNullable<MerchantOrderSummary["cancelIntent"]>, l: (en: string, ko: string) => string) {
  const kind = intent.kind === "DELAY_RULE" ? l("30-day cancellation", "30일 지연 취소") : l("Cancellation", "취소");
  switch (intent.outcome) {
    case "EFFECT_ISSUED":
      return `${kind} · ${l("Processing", "처리 중")}`;
    case "DEFERRED":
      return `${kind} · ${l("Waiting for the purchase in progress to finish", "진행 중인 구매가 끝난 뒤 재평가")}`;
    case "REJECTED":
      return `${kind} · ${l("Not cancelled", "취소되지 않음")}${intent.code ? ` · ${intent.code}` : ""}`;
    case "SUCCEEDED":
      return `${kind} · ${l("Cancelled", "취소 완료")}`;
    case "SUPERSEDED":
      return `${kind} · ${l("Closed by another outcome", "다른 결과로 종결")}`;
    default:
      return `${kind} · ${intent.outcome}`;
  }
}

function MerchantOrderStatusRail({ merchantOrder }: { merchantOrder: MerchantOrderSummary }) {
  const { l } = useLocale();
  const steps = [
    { label: l("Funding", "자금"), state: merchantOrder.operational.progress.funding },
    { label: l("Shop order", "판매처 주문"), state: merchantOrder.operational.progress.procurement },
    { label: l("Delivery", "배송"), state: merchantOrder.operational.progress.delivery },
    { label: l("Resolution", "해결"), state: merchantOrder.operational.progress.resolution },
  ] as const;
  return <ol className="merchant-order-status-rail" aria-label={l("{shop} status", "{shop} 상태", { shop: merchantOrder.shopDomain })}>
    {steps.map((step) => <li className={`is-${step.state.toLowerCase()}`} key={step.label}><span aria-hidden="true" /><small>{step.label}</small></li>)}
  </ol>;
}

function merchantOrderProducts(projection: AgencyOrderProjection, merchantOrder: MerchantOrderSummary) {
  const quantities = new Map<string, number>();
  for (const unit of merchantOrder.units) quantities.set(unit.lineId, (quantities.get(unit.lineId) ?? 0) + 1);
  const fallback = quantities.size === 0;
  return projection.agencyOrder.lines
    .filter((line) => fallback ? line.shopDomain === merchantOrder.shopDomain : quantities.has(line.lineId))
    .map((line) => ({ line, quantity: quantities.get(line.lineId) ?? line.quantity }));
}

// ShipmentsSection은 실물 패키지 상세다(계약 v7 §9 — 분할 배송은 패키지
// 카드로). Physical unit은 이 물류 상세에서만 식별한다.
function ShipmentsSection({ projection }: { projection: AgencyOrderProjection }) {
  const { l, locale } = useLocale();
  const unitByID = new Map(projection.units.map((unit) => [unit.merchantOrderUnitId, unit]));
  return <section aria-label={l("Shipping status", "배송 현황")} className="agency-order-shipments">
    <header><strong>{l("Shipping packages", "배송 패키지")}</strong><span>{l("{count} packages", "패키지 {count}건", { count: projection.shipments.length })}</span></header>
    {projection.shipments.map((shipment) => (
      <div className="agency-order-shipments__item" key={shipment.id}>
        <div>
          <strong>{shipmentStateLabel(shipment.state, l)}{shipment.units.some((unit) => (fulfillmentExceptionKinds as readonly string[]).includes(unit.disposition))
            ? l(" · Exception under review", " · 예외 확인 중")
            : shipment.units.some((unit) => unitByID.get(unit.id)?.resolutionCause)
              ? l(" · Exception resolved", " · 예외 처리 완료")
              : ""}</strong>
          <span>{shipment.carrier} · <code>{shipment.trackingRef}</code></span>
        </div>
        <ul>
          {shipment.units.map((unit) => (
            <li key={`${unit.lineId}:${unit.unitIndex}`}>
              {unit.lineId} #{unit.unitIndex} — {fulfillmentLabel(unit.disposition, l)}
              {unitByID.get(unit.id)?.resolutionCause ? ` · ${fulfillmentLabel(unitByID.get(unit.id)?.resolutionCause ?? "", l)} → ${unitByID.get(unit.id)?.resolutionDecision}` : ""}
            </li>
          ))}
        </ul>
        <time dateTime={shipment.updatedAt}>{new Date(shipment.updatedAt).toLocaleString(locale)}</time>
      </div>
    ))}
  </section>;
}

// 단순변심 OFF 게이트(ADR-0052 §1): 과실 계열 사유만 제공된다.
function refundReasonOptions(l: Localize): Array<[RefundReasonCode, string]> {
  return [
    ["ITEM_NOT_RECEIVED", l("I did not receive the product", "상품을 받지 못했어요")],
    ["ITEM_DAMAGED_DEFECTIVE", l("The product is damaged or defective", "상품이 파손·불량이에요")],
    ["WRONG_ITEM_RECEIVED", l("I received the wrong product", "다른 상품이 도착했어요")],
    ["ORDER_DELAYED", l("Processing is taking too long", "처리가 너무 지연되고 있어요")],
    ["OTHER_SERVICE_FAULT", l("Another service issue", "기타 서비스 문제가 있어요")],
  ];
}

function refundReasonLabel(code: string, l: Localize) {
  return refundReasonOptions(l).find(([value]) => value === code)?.[1] ?? code;
}

function receiptRefunded(receipt: AgencyOrderReceipt) {
  return receipt.terminalState === "REFUNDED" || receipt.terminalState === "REFUNDED_ALL";
}

function reasonCodeMessage(
  code: string,
  projection: AgencyOrderProjection | undefined,
  l: Localize,
) {
  if (projection?.payment?.rail === "PAYPAL") {
    const partialMessages: Record<string, string> = {
      PRODUCT_UNAVAILABLE: l("Some products were unavailable, so processing stopped for them. Their amounts will be refunded.", "일부 판매처 재고가 없어 해당 상품 처리를 중단했습니다. 해당 상품 금액은 환불됩니다."),
      VARIANT_UNAVAILABLE: l("Some selected options were no longer available, so processing stopped for those products. Their amounts will be refunded.", "일부 상품 옵션이 판매자에게 더 이상 없어 처리를 중단했습니다. 해당 상품 금액은 환불됩니다."),
      VARIANT_COMBINATION_INVALID: l("Some option combinations were unavailable, so processing stopped for those products. Their amounts will be refunded.", "일부 상품의 옵션 조합을 판매자가 제공하지 않아 처리를 중단했습니다. 해당 상품 금액은 환불됩니다."),
      VARIANT_AMBIGUOUS: l("Some product options were unclear, so processing stopped for those products. Their amounts will be refunded.", "일부 상품 옵션이 명확하지 않아 처리를 중단했습니다. 해당 상품 금액은 환불됩니다."),
      VARIANT_REQUIRES_CLARIFICATION: l("Some product options require clarification, so processing stopped for those products. Their amounts will be refunded.", "일부 상품의 옵션 확인이 필요해 처리를 중단했습니다. 해당 상품 금액은 환불됩니다."),
      PRICE_CHANGED: l("Some merchant prices changed, so processing stopped for those products. Their amounts will be refunded.", "일부 판매자 가격이 변동되어 처리를 중단했습니다. 해당 상품 금액은 환불됩니다."),
      SHIPPING_UNAVAILABLE: l("Some products cannot ship to this address, so processing stopped for them. Their amounts will be refunded.", "일부 상품이 해당 배송지로 배송 불가하여 처리를 중단했습니다. 해당 상품 금액은 환불됩니다."),
    };
    if (partialMessages[code]) return partialMessages[code];
  }
  const messages: Record<string, string> = {
    PRODUCT_UNAVAILABLE: l("The merchant was out of stock, so processing stopped. The full payment will be refunded.", "판매자 재고가 없어 담당자가 처리를 중단했습니다. 결제 금액은 전액 환불됩니다."),
    VARIANT_UNAVAILABLE: l("The selected option is no longer available, so processing stopped. The full payment will be refunded.", "선택한 옵션이 판매자에게 더 이상 없어 처리를 중단했습니다. 결제 금액은 전액 환불됩니다."),
    VARIANT_COMBINATION_INVALID: l("The merchant does not offer the selected option combination, so processing stopped. The full payment will be refunded.", "선택한 옵션 조합을 판매자가 제공하지 않아 처리를 중단했습니다. 결제 금액은 전액 환불됩니다."),
    VARIANT_AMBIGUOUS: l("The options were unclear, so processing stopped. The full payment will be refunded.", "옵션이 명확하지 않아 처리를 중단했습니다. 결제 금액은 전액 환불됩니다."),
    VARIANT_REQUIRES_CLARIFICATION: l("The options require clarification, so processing stopped. The full payment will be refunded.", "옵션 확인이 필요해 처리를 중단했습니다. 결제 금액은 전액 환불됩니다."),
    PRICE_CHANGED: l("The merchant price changed, so processing stopped. The full payment will be refunded.", "판매자 가격이 변동되어 처리를 중단했습니다. 결제 금액은 전액 환불됩니다."),
    SHIPPING_UNAVAILABLE: l("The product cannot ship to this address, so processing stopped. The full payment will be refunded.", "해당 배송지로 배송이 불가하여 처리를 중단했습니다. 결제 금액은 전액 환불됩니다."),
  };
  return messages[code] ?? l("Operator review is required. ({code})", "처리 담당자의 확인이 필요합니다. ({code})", { code });
}

// 상태 headline은 process.state의 라벨 사영이다(ADR-0057 §1 — 어휘 단일
// 소스). TEST/SANDBOX 표기는 배포 전역 스위치가 아니라 **이 주문의**
// paymentSelection.economicEffect에서 파생한다(2차 P0 — 주문별 결제 모드).
function statusCopy(item: AgencyOrderProjection, l: Localize) {
  const testMoney =
    item.paymentInstruction?.paymentSelection?.economicEffect !== "REAL_MONEY";
  if (item.process.state === "TERMINAL") {
    if (item.payment?.rail === "GIWA" && item.payment.state !== "COMPLETED" &&
      ["COMPLETED_ALL", "COMPLETED_PARTIAL"].includes(item.process.terminalReason ?? "")) {
      return l("Delivery complete · awaiting settlement confirmation", "배송 완료 · 정산 확정 대기");
    }
    switch (item.process.terminalReason) {
      case "REFUNDED_ALL":
        if (!testMoney) return l("Refund complete", "환불 완료");
        return item.payment?.rail === "PAYPAL" ? l("SANDBOX refund complete", "SANDBOX 환불 완료") : l("TEST token refund complete", "TEST 토큰 환불 완료");
      case "COMPLETED_PARTIAL":
        return testMoney ? l("TEST complete · partial refund", "TEST 처리 완료 · 부분 환불") : l("Complete · partial refund", "처리 완료 · 부분 환불");
      case "CANCELLED":
        return l("Cancelled", "취소됨");
      case "EXPIRED":
        return l("Expired", "만료됨");
      default:
        return testMoney ? l("TEST complete", "TEST 처리 완료") : l("Complete", "처리 완료");
    }
  }
  const labels: Record<AgencyOrderProjection["process"]["state"], string> = {
    WAITING_CUSTOMER_PAYMENT: l("Awaiting payment", "결제 대기"),
    PAYMENT_RECONCILIATION: l("Verifying payment", "결제 확인 중"),
    PROCUREMENT_IN_PROGRESS: l("Purchasing", "구매 진행 중"),
    LOGISTICS_IN_PROGRESS: l("In transit", "배송 중"),
    RESOLUTION_IN_PROGRESS: l("Refund in progress", "환불 진행 중"),
    ATTENTION_REQUIRED: l("Needs attention", "확인 필요"),
    TERMINAL: l("Complete", "처리 완료"),
  };
  return labels[item.process.state];
}

function optionCopy(options: string[] | undefined, l: Localize) { return options?.join(" · ") || l("No options", "옵션 없음"); }
function formatMoney(money: { amountMinor: number; currency: string }) { return new Intl.NumberFormat("en-US", { style: "currency", currency: money.currency }).format(money.amountMinor / 100); }
function formatCompactTime(value: string, locale: string) { return new Intl.DateTimeFormat(locale, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(new Date(value)); }

function shipmentStateLabel(state: string, l: Localize) {
  return {
    CREATED: l("Preparing shipment", "배송 준비"),
    LABEL_CREATED: l("Tracking registered", "운송장 등록"),
    IN_TRANSIT: l("In transit", "배송 중"),
    OUT_FOR_DELIVERY: l("Out for delivery", "배송 출발"),
    DELIVERED: l("Delivery outcome recorded", "배송 결과 기록됨"),
    EXCEPTION: l("Shipping needs attention", "배송 확인 필요"),
    LOST: l("Checking lost package", "분실 확인"),
    RETURN_TO_SENDER: l("Returning to sender", "반송 중"),
    RETURNED: l("Returned", "반송 완료"),
    CANCELLED_NO_EFFECT: l("Shipping cancelled", "배송 취소"),
    EXCEPTION_RECONCILIATION: l("Post-delivery review", "수령 후 확인 중"),
  }[state];
}
function addressLines(address: RevealedAddress) { return [address.addressLine1, address.addressLine2, address.city, address.region, address.postalCode, address.country].filter(Boolean).join(", "); }
