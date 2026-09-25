import { processProgressLabel } from "../app/processPresentation";
import { useCallback, useEffect, useMemo, useState } from "react";
import { ArrowLeft, Check, Copy, History, Link2, PackageSearch, ReceiptText } from "lucide-react";
import { useLocation, useNavigate, useParams } from "react-router";
import { APIError } from "../../../shared/api/client";
import { invariantContent, useLocale, type Localize } from "../../../shared/i18n";
import { Button, FeedbackState, PageHeader, Chip } from "../../../shared/ui";
import {
  getOperatorOrderInvestigation,
  getOperatorOrderTimeline,
  type OrderIdentifier,
  type OrderInvestigation,
  type OrderLookupIdentifierType,
  type OrderTimeline,
} from "../infra/agencyOrderOperatorApi";
import { fulfillmentLabel, merchantOrderOperationalStageLabel, terminalReasonLabel } from "./unitStageCopy";
import "./agency-order.css";

type IdentifierGroup = "ORDER" | "PAYMENT" | "COMMERCE" | "LOGISTICS";

export function AgencyOrderInvestigationPage() {
  const { agencyOrderId = "" } = useParams();
  const navigate = useNavigate();
  const location = useLocation();
  const { l, locale } = useLocale();
  const [investigation, setInvestigation] = useState<OrderInvestigation>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    if (!agencyOrderId) return;
    setLoading(true);
    setError("");
    try {
      const response = await getOperatorOrderInvestigation(agencyOrderId);
      setInvestigation(response.investigation);
    } catch (cause) {
      setError(investigationError(cause, l));
    } finally {
      setLoading(false);
    }
  }, [agencyOrderId, l]);

  useEffect(() => { void load(); }, [load]);

  if (loading) {
    return <main className="order-ui-operator-console agency-order-operator operator-order-investigation">
      <FeedbackState state="loading" title={l("Loading order evidence", "주문 evidence 불러오는 중")} description={l("Reading the canonical order and its linked payment, commerce, and logistics identifiers.", "권위 주문과 연결된 결제·판매처·물류 식별자를 읽고 있습니다.")} />
    </main>;
  }
  if (!investigation || error) {
    return <main className="order-ui-operator-console agency-order-operator operator-order-investigation">
      <FeedbackState
        state="error"
        title={l("Order investigation unavailable", "주문 조사를 열 수 없습니다")}
        description={error}
        action={{ label: l("Back to lookup", "조회로 돌아가기"), onAction: () => navigate("/admin/agencyOrder") }}
      />
    </main>;
  }

  const matchedBy = (location.state as { matchedBy?: OrderLookupIdentifierType } | null)?.matchedBy;
  const { order, process } = investigation;
  const terminal = process.terminalReason ? terminalReasonLabel(process.terminalReason, l) : undefined;
  return (
    <main className="order-ui-operator-console agency-order-operator operator-order-investigation">
      <PageHeader
        eyebrow={l("Order investigation", "주문 조사")}
        title={l("Order evidence", "주문 evidence")}
        description={<><code>{order.agencyOrderId}</code>{matchedBy ? <span className="operator-order-investigation__matched">{l("Matched by {kind}", "{kind}로 일치", { kind: identifierLabel(matchedBy, l) })}</span> : null}</>}
        secondaryActions={[{ label: l("Back to order processing", "주문 처리로 돌아가기"), onClick: () => navigate("/admin/agencyOrder") }]}
        summary={<div className="operator-order-investigation__summary">
          <span className={`operator-order-investigation__environment is-${order.executionProfile.economicEffect === "REAL_MONEY" ? "live" : "test"}`}>
            {order.executionProfile.providerEnvironment} · {order.executionProfile.paymentRail}
          </span>
          <span>{l("Process", "Process")} <strong>{process.state}</strong></span>
          {terminal ? <span>{l("Outcome", "결과")} <strong>{terminal}</strong></span> : null}
          <span>{l("Updated", "갱신")} <strong>{formatDate(process.updatedAt, locale)}</strong></span>
        </div>}
      />

      <section className="operator-order-investigation__overview" aria-label={l("Order status summary", "주문 상태 요약")}>
        <div className="operator-order-investigation__state">
          <span>{l("Current state", "현재 상태")}</span>
          <strong>{process.state}</strong>
          <p>{process.lastReasonCode || terminal || l("No blocking reason is recorded.", "기록된 차단 사유가 없습니다.")}</p>
        </div>
        <dl>
          <div><dt>{l("Customer payment", "고객 결제액")}</dt><dd>{formatMoney(order.customerPayableTotal, locale)}</dd></div>
          <div><dt>{l("Pass-through", "상품·배송 실비")}</dt><dd>{formatMoney(order.passThroughTotal, locale)}</dd></div>
          <div><dt>{l("Agency fee", "대행 수수료")}</dt><dd>{formatMoney(order.agencyFeeTotal, locale)}</dd></div>
          <div><dt>{l("Issued", "발행")}</dt><dd>{formatDate(order.issuedAt, locale)}</dd></div>
          <div><dt>{l("Shipping", "배송지")}</dt><dd>{order.shippingMasked || order.shippingCountry}</dd></div>
          <div><dt>{l("Execution", "실행")}</dt><dd>{order.executionProfile.merchantExecutionMode}</dd></div>
        </dl>
      </section>

      <IdentityRail identifiers={investigation.identifiers} />

      <div className="operator-order-investigation__evidence-grid">
        <section className="operator-order-investigation__panel" aria-labelledby="investigation-checkpoints-title">
          <header><ReceiptText aria-hidden="true" /><div><span>{l("Evidence checkpoints", "증거 체크포인트")}</span><h2 id="investigation-checkpoints-title">{l("What the system currently knows", "현재 시스템이 알고 있는 사실")}</h2></div></header>
          <ol className="operator-order-investigation__checkpoints">
            {investigation.checkpoints.map((checkpoint, index) => <li key={`${checkpoint.kind}-${checkpoint.reference}-${index}`}>
              <span aria-hidden="true" />
              <div><small>{checkpoint.kind} · {formatDate(checkpoint.observedAt, locale)}</small><strong>{checkpoint.state}</strong>{checkpoint.reasonCode ? <p>{checkpoint.reasonCode}</p> : null}{checkpoint.reference ? <code>{checkpoint.reference}</code> : null}</div>
            </li>)}
          </ol>
        </section>

        <section className="operator-order-investigation__panel" aria-labelledby="investigation-integrity-title">
          <header><Link2 aria-hidden="true" /><div><span>{l("Integrity", "무결성")}</span><h2 id="investigation-integrity-title">{l("Immutable evidence", "불변 evidence")}</h2></div></header>
          <dl className="operator-order-investigation__hashes">
            <HashFact label={l("Order snapshot", "주문 snapshot")} value={order.snapshotHash} />
            <HashFact label={l("Displayed snapshot", "표시 snapshot")} value={order.issuanceEvidence.displayedSnapshotHash} />
            <HashFact label={l("Execution profile", "실행 profile")} value={order.executionProfileHash} />
            <HashFact label={l("Issue idempotency", "발행 멱등성")} value={order.issuanceEvidence.idempotencyKeyHash} />
            <HashFact label={l("Payment instruction", "결제 지시")} value={investigation.paymentInstruction.id} />
            {investigation.receipt ? <HashFact label={l("Terminal receipt", "종결 receipt")} value={investigation.receipt.receiptHash} /> : null}
          </dl>
        </section>
      </div>

      <section className="operator-order-investigation__panel" aria-labelledby="investigation-merchant-orders-title">
        <header><PackageSearch aria-hidden="true" /><div><span>{l("MerchantOrder resolution", "MerchantOrder 해결 단위")}</span><h2 id="investigation-merchant-orders-title">{l("Immutable shop-checkout boundaries", "불변 Shop 결제 경계")}</h2></div></header>
        <p className="operator-order-investigation__panel-copy">{l("Each card is one cancellation and refund boundary. The gross amount includes its allocated Vitlane fee; lines and physical units inside the card do not carry money.", "카드 하나가 취소·환불 경계 하나입니다. 총액에는 배분된 Vitlane 수수료가 포함되며 카드 안의 line과 physical unit은 금액을 소유하지 않습니다.")}</p>
        <div className="operator-order-investigation__merchant-orders">
          {investigation.merchantOrders.map((merchantOrder) => <article className="operator-order-investigation__merchant-order" key={merchantOrder.id}>
            <ol aria-label={l("{shop} state rail", "{shop} 상태 레일", { shop: merchantOrder.shopDomain })}>
              <li className={`is-${merchantOrder.operational.progress.funding.toLowerCase()}`}><span /><small>{l("Funding", "자금")}</small></li>
              <li className={`is-${merchantOrder.operational.progress.procurement.toLowerCase()}`}><span /><small>{l("Purchase", "구매")}</small></li>
              <li className={`is-${merchantOrder.operational.progress.delivery.toLowerCase()}`}><span /><small>{l("Delivery", "배송")}</small></li>
              <li className={`is-${merchantOrder.operational.progress.resolution.toLowerCase()}`}><span /><small>{l("Resolution", "해결")}</small></li>
            </ol>
            <div><header><div><span>{l("SHOP {ordinal}", "SHOP {ordinal}", { ordinal: merchantOrder.checkoutOrdinal })}</span><h3>{merchantOrder.shopDomain}</h3><small><code>{merchantOrder.id}</code></small></div><strong>{formatMoney(merchantOrder.customerGrossAmount, locale)}</strong></header>
              <div className="operator-order-investigation__merchant-order-badges"><Chip>{l("Current stage", "현재 단계")} · {merchantOrderOperationalStageLabel(merchantOrder.operational.stage, l)}</Chip><Chip>{l("Funding fact", "자금 사실")} · {merchantOrder.fundingState || l("Not created", "생성 전")}</Chip><Chip>{l("Purchase fact", "구매 사실")} · {merchantOrder.state}</Chip>{merchantOrder.compensationState ? <span>{l("Compensation fact", "보상 사실")} · {merchantOrder.compensationState}</span> : null}{merchantOrder.disputeState ? <span>{l("PayPal dispute fact", "PayPal 분쟁 사실")} · {merchantOrder.disputeState}{merchantOrder.disputeOutcome ? ` · ${merchantOrder.disputeOutcome}` : ""}</span> : null}</div>
              <ul>{investigationMerchantOrderLines(investigation, merchantOrder.id).map(({ line, quantity }) => <li key={line.lineId}><div><strong>{line.productTitle}</strong><span>{line.variantTitle || l("Default variant", "기본 variant")}</span></div><b>{l("Qty {quantity}", "수량 {quantity}", { quantity })}</b><code>{line.evidenceHash}</code></li>)}</ul>
            </div>
          </article>)}
        </div>
      </section>

      <section className="operator-order-investigation__panel" aria-labelledby="investigation-fulfillment-title">
        <header><PackageSearch aria-hidden="true" /><div><span>{l("Logistics detail", "물류 상세")}</span><h2 id="investigation-fulfillment-title">{l("Physical-unit status", "Physical unit 상태")}</h2></div></header>
        <div className="operator-order-investigation__fulfillment operator-order-investigation__fulfillment--units">
          <div>
            <h3>{l("Unit evidence", "Unit evidence")}</h3>
            <ul>{investigation.units.length > 0 ? investigation.units.map((unit) => <li key={unit.merchantOrderUnitId}><div><strong>{unit.shopDomain} · #{unit.unitIndex}</strong><span>{unit.shipment ? `${unit.shipment.carrier} · ${unit.shipment.trackingRef}` : l("No shipment linked", "연결된 배송 없음")}</span></div><b>{fulfillmentLabel(unit.stage, l)}</b><code>{unit.merchantOrderUnitId}</code></li>) : <li className="is-empty">{l("No physical units have been created yet.", "아직 생성된 physical unit이 없습니다.")}</li>}</ul>
          </div>
        </div>
      </section>

      <ProcessTimelinePanel agencyOrderId={order.agencyOrderId} />

      <footer className="operator-order-investigation__privacy"><Check aria-hidden="true" /> {l("Safe operator projection: full shipping address, buyer identity, checkout URL, and provider payload are excluded. Use the existing fresh-auth reveal action only when execution requires PII.", "안전한 운영자 사영입니다. 배송주소 원문·구매자 신원·checkout URL·provider payload는 제외됩니다. 실행에 PII가 필요할 때만 기존 fresh-auth 열람 행동을 사용하세요.")}</footer>
    </main>
  );
}

// ADR-0070 §4.7 — 결정 원장 ⋈ 이벤트 ⋈ 커맨드. 운영자가 요청할 때만 읽는다
// (자동 폴링 없음): 결정 version 하나가 "무엇을 보고(이벤트 seq 범위) 무엇을
// 결정해(stage·MO phase diff) 무엇을 시켰는가(커맨드)"의 한 단위다.
function ProcessTimelinePanel({ agencyOrderId }: { agencyOrderId: string }) {
  const { l, locale } = useLocale();
  const [timeline, setTimeline] = useState<OrderTimeline>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [expanded, setExpanded] = useState<number>();

  async function load() {
    setLoading(true);
    setError("");
    try {
      const response = await getOperatorOrderTimeline(agencyOrderId);
      setTimeline(response.timeline);
    } catch (cause) {
      setError(cause instanceof APIError && cause.code === "ORDER_PROCESS_TIMELINE_NOT_FOUND"
        ? l("No process history exists for this order yet.", "이 주문의 process 이력이 아직 없습니다.")
        : l("The process timeline could not be loaded.", "process timeline을 불러오지 못했습니다."));
    } finally {
      setLoading(false);
    }
  }

  const effectsByEvent = useMemo(() => {
    const map = new Map<number, OrderTimeline["effects"]>();
    for (const command of timeline?.effects ?? []) {
      const list = map.get(command.causedByEventId) ?? [];
      list.push(command);
      map.set(command.causedByEventId, list);
    }
    return map;
  }, [timeline]);

  return <section className="operator-order-investigation__panel operator-order-timeline" aria-labelledby="investigation-timeline-title">
    <header><History aria-hidden="true" /><div><span>{l("Process timeline", "처리 타임라인")}</span><h2 id="investigation-timeline-title">{l("What the reducer observed and requested", "리듀서가 관찰하고 요청한 내용")}</h2></div>
      <Button busy={loading} emphasis={timeline ? "quiet" : "secondary"} onClick={() => void load()} size="compact">{timeline ? l("Reload timeline", "timeline 다시 읽기") : l("Load timeline", "timeline 열기")}</Button>
    </header>
    <p className="operator-order-investigation__panel-copy">{l("Each decision version consumed one range of events, folded them into the Order·MO·Unit state, and issued effects. Nothing here is a live poll; load it when you need the history.", "결정 version 하나가 이벤트 seq 범위 하나를 소비해 Order·MO·Unit 상태로 접고 Effect를 발행합니다. 자동 폴링이 아니므로 이력이 필요할 때 여세요.")}</p>
    {error ? <p className="operator-order-timeline__error" role="alert">{error}</p> : null}
    {timeline ? <>
      <dl className="operator-order-timeline__summary">
        <div><dt>{l("Process", "Process")}</dt><dd>{timeline.process.state}{timeline.process.lastReasonCode ? ` · ${timeline.process.lastReasonCode}` : ""}</dd></div>
        <div><dt>{l("Version", "Version")}</dt><dd>{timeline.process.version}</dd></div>
        <div><dt>{l("Applied seq", "소비 seq")}</dt><dd>{timeline.process.lastAppliedSeq}</dd></div>
        <div><dt>{l("Decisions", "결정")}</dt><dd>{timeline.decisions.length}</dd></div>
        <div><dt>{l("Events", "이벤트")}</dt><dd>{timeline.events.length}</dd></div>
        <div><dt>{l("Effects", "Effect")}</dt><dd>{timeline.effects.length}</dd></div>
      </dl>
      {timeline.decisions.length === 0 ? <p className="operator-order-timeline__empty">{l("No decision has been recorded since the ledger cutover; events before it are listed below.", "원장 컷오버 이후 기록된 결정이 없습니다. 그 이전 이벤트는 아래에 나열됩니다.")}</p> : null}
      <div className="operator-order-timeline__detail">
        <h3>{l("Requests and results", "요청과 결과")}</h3>
        {timeline.requests.map((receipt) => <section key={receipt.requestId}>
          <h4>{receipt.kind} · {receipt.outcome}</h4>
          <p><code>{receipt.requestId}</code> · <code>{receipt.merchantOrderId}</code></p>
          <p>{processProgressLabel(receipt, l, true)}</p>
          <ul>{timeline.effects.filter((effect) => effect.requestId === receipt.requestId).map((effect) => <li key={effect.effectId}>
            <strong>{effect.type}</strong><small>{effect.deliveryState}</small><code>{effect.effectId}</code>
          </li>)}</ul>
        </section>)}
      </div>
      <ol className="operator-order-timeline__decisions">
        {timeline.decisions.map((decision) => {
          const events = timeline.events.filter((event) => event.seq >= decision.seqFrom && event.seq <= decision.seqTo);
          const commands = events.flatMap((event) => effectsByEvent.get(event.id) ?? []);
          const open = expanded === decision.version;
          return <li key={decision.version} className={decision.stageChanged ? "is-stage-changed" : undefined}>
            <Button aria-expanded={open} className="operator-order-timeline__toggle" emphasis="quiet" onClick={() => setExpanded(open ? undefined : decision.version)}>
              <span className="operator-order-timeline__version">{l("v{version}", "v{version}", { version: decision.version })}</span>
              <strong>{decision.stageBefore && decision.stageBefore !== decision.stageAfter ? `${decision.stageBefore} → ${decision.stageAfter}` : decision.stageAfter}</strong>
              <small>{l("seq {from}–{to}", "seq {from}–{to}", { from: decision.seqFrom, to: decision.seqTo })} · {formatDate(decision.decidedAt, locale)}{decision.lastReasonCode ? ` · ${decision.lastReasonCode}` : ""}</small>
              <span className="operator-order-timeline__counts">{l("{events} events · {commands} commands · {merchantOrders} MO changes", "이벤트 {events} · 커맨드 {commands} · MO 변경 {merchantOrders}", { events: events.length, commands: commands.length, merchantOrders: decision.merchantOrders.length })}</span>
            </Button>
            {open ? <div className="operator-order-timeline__detail">
              <h3>{l("Events consumed", "소비한 이벤트")}</h3>
              <ul>{events.map((event) => <li key={event.id}><code>#{event.seq}</code><span>{event.source}</span><strong>{event.type}</strong><small>{formatDate(event.occurredAt, locale)}</small></li>)}</ul>
              {decision.merchantOrders.length > 0 ? <>
                <h3>{l("MerchantOrder changes", "MerchantOrder 변경")}</h3>
                <ul>{decision.merchantOrders.map((change) => <li key={change.merchantOrderId}><code>{change.merchantOrderId}</code><strong>{change.phaseBefore && change.phaseBefore !== change.phaseAfter ? `${change.phaseBefore} → ${change.phaseAfter}` : change.phaseAfter}</strong><small>{[change.intentAfter, change.attentionAfter, change.reason].filter(Boolean).join(" · ")}</small></li>)}</ul>
              </> : null}
              <h3>{l("Effects issued", "발행한 Effect")}</h3>
              <ul>{commands.length > 0 ? commands.map((command) => <li key={command.effectId} className={`is-${command.deliveryState.toLowerCase()}`}><code>{command.target}</code><strong>{command.type}</strong><small>{command.deliveryState} · {l("attempts {count}", "시도 {count}", { count: command.attemptCount })}</small></li>) : <li className="is-empty">{l("No effect was issued by this decision.", "이 결정은 Effect를 발행하지 않았습니다.")}</li>}</ul>
              {decision.wakeAt ? <p><small>{l("Wake scheduled", "wake 예약")} · {formatDate(decision.wakeAt, locale)}</small></p> : null}
            </div> : null}
          </li>;
        })}
      </ol>
      {timeline.events.some((event) => event.appliedVersion === undefined) ? <div className="operator-order-timeline__detail">
        <h3>{l("Events not yet consumed", "아직 소비되지 않은 이벤트")}</h3>
        <ul>{timeline.events.filter((event) => event.appliedVersion === undefined).map((event) => <li key={event.id}><code>#{event.seq}</code><span>{event.source}</span><strong>{event.type}</strong><small>{formatDate(event.occurredAt, locale)}</small></li>)}</ul>
      </div> : null}
      {timeline.events.some((event) => event.appliedVersion !== undefined && !timeline.decisions.some((decision) => decision.version === event.appliedVersion)) ? <div className="operator-order-timeline__detail">
        <h3>{l("Events consumed before the decision ledger", "결정 원장 이전에 소비된 이벤트")}</h3>
        <ul>{timeline.events.filter((event) => event.appliedVersion !== undefined && !timeline.decisions.some((decision) => decision.version === event.appliedVersion)).map((event) => <li key={event.id}><code>#{event.seq}</code><span>{event.source}</span><strong>{event.type}</strong><small>{l("v{version}", "v{version}", { version: event.appliedVersion ?? 0 })} · {formatDate(event.occurredAt, locale)}</small></li>)}</ul>
      </div> : null}
    </> : null}
  </section>;
}

function IdentityRail({ identifiers }: { identifiers: OrderIdentifier[] }) {
  const { l } = useLocale();
  const [copied, setCopied] = useState("");
  const groups = useMemo(() => {
    const result: Record<IdentifierGroup, OrderIdentifier[]> = { ORDER: [], PAYMENT: [], COMMERCE: [], LOGISTICS: [] };
    for (const identifier of identifiers) result[identifierGroup(identifier.kind)].push(identifier);
    return result;
  }, [identifiers]);
  async function copy(value: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(value);
      window.setTimeout(() => setCopied((current) => current === value ? "" : current), 1500);
    } catch {
      setCopied("");
    }
  }
  return <section className="operator-order-identity" aria-labelledby="operator-order-identity-title">
    <header><div><span>{l("Identity rail", "식별자 연결")}</span><h2 id="operator-order-identity-title">{l("One order across every system", "여러 시스템에 걸친 하나의 주문")}</h2></div><p>{l("Each value below resolves exactly to this AgencyOrder.", "아래의 각 값은 이 AgencyOrder에 정확히 연결됩니다.")}</p></header>
    <div>{(Object.keys(groups) as IdentifierGroup[]).map((group) => <section key={group}>
      <h3>{identifierGroupLabel(group, l)}</h3>
      <ul>{groups[group].length > 0 ? groups[group].map((identifier) => <li key={`${identifier.kind}:${identifier.value}:${identifier.qualifier ?? ""}`}>
        <div><small>{identifierLabel(identifier.kind, l)}{identifier.qualifier ? ` · ${identifier.qualifier}` : ""}</small><code>{identifier.value}</code></div>
        <Button aria-label={l("Copy {kind}", "{kind} 복사", { kind: identifierLabel(identifier.kind, l) })} emphasis="quiet" onClick={() => void copy(identifier.value)} size="compact"><span aria-hidden="true">{copied === identifier.value ? <Check /> : <Copy />}</span></Button>
      </li>) : <li className="is-empty">{l("Not created", "생성되지 않음")}</li>}</ul>
    </section>)}</div>
  </section>;
}

function HashFact({ label, value }: { label: string; value: string }) {
  return <div><dt>{label}</dt><dd><code>{value || invariantContent("—")}</code></dd></div>;
}

function identifierGroup(kind: OrderLookupIdentifierType): IdentifierGroup {
  if (kind === "AGENCY_ORDER_ID") return "ORDER";
  if (kind.startsWith("PAYPAL_") || kind === "PAYMENT_ID" || kind === "GIWA_TX_HASH") return "PAYMENT";
  if (kind.startsWith("MERCHANT_")) return "COMMERCE";
  return "LOGISTICS";
}

function identifierGroupLabel(group: IdentifierGroup, l: Localize) {
  switch (group) {
    case "ORDER": return l("Canonical order", "권위 주문");
    case "PAYMENT": return l("Payment", "결제");
    case "COMMERCE": return l("Merchant", "판매처");
    case "LOGISTICS": return l("Logistics", "물류");
  }
}

function identifierLabel(kind: OrderLookupIdentifierType, l: Localize) {
  switch (kind) {
    case "AUTO": return l("Exact ID", "정확 ID");
    case "AGENCY_ORDER_ID": return l("AgencyOrder ID", "AgencyOrder ID");
    case "PAYMENT_ID": return l("Payment ID", "Payment ID");
    case "PAYPAL_ORDER_ID": return l("PayPal Order ID", "PayPal Order ID");
    case "PAYPAL_CAPTURE_ID": return l("PayPal Capture ID", "PayPal Capture ID");
    case "PAYPAL_REFUND_ID": return l("PayPal Refund ID", "PayPal Refund ID");
    case "MERCHANT_ORDER_ID": return l("MerchantOrder ID", "MerchantOrder ID");
    case "MERCHANT_ORDER_REF": return l("Merchant order reference", "판매처 주문번호");
    case "SHIPMENT_ID": return l("Shipment ID", "Shipment ID");
    case "TRACKING_REF": return l("Tracking number", "운송장 번호");
    case "GIWA_TX_HASH": return l("GIWA transaction", "GIWA transaction");
  }
}

function investigationError(cause: unknown, l: Localize) {
  if (cause instanceof APIError && cause.code === "ORDERING_OPERATOR_LOOKUP_NOT_FOUND") {
    return l("This AgencyOrder does not exist or is no longer available.", "이 AgencyOrder가 없거나 더 이상 조회할 수 없습니다.");
  }
  return l("The safe evidence projection could not be loaded. Try again from Exact order lookup.", "안전한 evidence 사영을 불러오지 못했습니다. 정확 주문 조회에서 다시 시도하세요.");
}

function investigationMerchantOrderLines(investigation: OrderInvestigation, merchantOrderId: string) {
  const merchantOrder = investigation.merchantOrders.find((item) => item.id === merchantOrderId);
  if (!merchantOrder) return [];
  const quantities = new Map<string, number>();
  for (const unit of merchantOrder.units) quantities.set(unit.lineId, (quantities.get(unit.lineId) ?? 0) + 1);
  const fallback = quantities.size === 0;
  return investigation.order.lines
    .filter((line) => fallback ? line.shopDomain === merchantOrder.shopDomain : quantities.has(line.lineId))
    .map((line) => ({ line, quantity: quantities.get(line.lineId) ?? line.quantity }));
}

function formatMoney(money: { amountMinor: number; currency: string }, locale: string) {
  return new Intl.NumberFormat(locale, { style: "currency", currency: money.currency }).format(money.amountMinor / 100);
}

function formatDate(value: string, locale: string) {
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}
