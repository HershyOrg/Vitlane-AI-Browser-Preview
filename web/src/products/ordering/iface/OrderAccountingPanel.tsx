import { Link } from "react-router";
import { paymentStateLabel } from "./stateChipCopy";
import { Disclosure, Notice, Chip } from "../../../shared/ui";
import { readLocalePreference, useLocale, type Localize } from "../../../shared/i18n";
import {
  type MerchantOrderAccounting,
  type OrderAccountingEvent,
  type OrderAccountingProjection,
} from "../infra/agencyOrderOperatorApi";

export function formatAccountingMoney(minor: number, currency = "USD") {
  return new Intl.NumberFormat(readLocalePreference(), {
    style: "currency",
    currency,
    minimumFractionDigits: 2,
  }).format(minor / 100);
}

export function formatAccountingDelta(minor: number, direction: "CREDIT" | "DEBIT") {
  const amount = formatAccountingMoney(Math.abs(minor));
  if (minor === 0) return amount;
  return `${direction === "CREDIT" ? "+" : "−"}${amount}`;
}

export function OrderAccountingPanel({
  accounting,
  compact = false,
  focusMerchantOrderId,
  defaultExpanded = false,
}: {
  accounting: OrderAccountingProjection;
  compact?: boolean;
  focusMerchantOrderId?: string;
  defaultExpanded?: boolean;
}) {
  const { l } = useLocale();
  const focus = focusMerchantOrderId
    ? accounting.merchantOrders.find((value) => value.merchantOrderId === focusMerchantOrderId)
    : undefined;
  const content = <>
    <BalanceBridge
      actual={focus?.realizedBalanceMinor ?? accounting.realizedBalanceMinor}
      adjustment={focus?.forecastAdjustmentMinor ?? accounting.forecastAdjustmentMinor}
      forecast={focus?.forecastBalanceMinor ?? accounting.forecastBalanceMinor}
      compact={compact}
    />
    {focus ? <FocusedMerchantOrder value={focus} /> : null}
    {!compact ? <>
      <ActualFacts accounting={accounting} />
      <MerchantOrderTable values={accounting.merchantOrders} />
      <EventLedger events={accounting.events} />
    </> : null}
    {accounting.unreconciledCashGrossMinor > 0 ? <Notice tone="warning">
      <strong>{l("Cash economics need reconciliation.", "수납 경제성 대사가 필요합니다.")}</strong>{" "}
      {l(
        "Gross cash of {amount} is visible, but its processor fee or net receipt is not final. It is excluded from the realized balance until reconciled.",
        "총 수납 {amount}은 확인됐지만 결제 수수료 또는 순수취액이 확정되지 않았습니다. 대사 전에는 실현 잔고에서 제외합니다.",
        { amount: formatAccountingMoney(accounting.unreconciledCashGrossMinor, accounting.currency) },
      )}
    </Notice> : null}
    {accounting.requiresAttention ? <Notice tone="danger">
      <strong>{l("Accounting attention required", "회계 확인 필요")}</strong>{" "}
      {accounting.attentionReasons.map((reason) => attentionReasonLabel(reason, l)).join(" · ")}
      {" · "}<Link to="/admin/agencyOrder">{l("Open operator lookup", "운영자 주문 조회 열기")}</Link>
    </Notice> : null}
  </>;

  if (compact) {
    return <section className={`order-accounting-card is-compact ${accounting.requiresAttention ? "requires-attention" : ""}`} aria-label={l("Order accounting", "주문 회계")}>
      <AccountingIdentity accounting={accounting} />
      {content}
    </section>;
  }

  return <Disclosure
    className={`order-accounting-card ${accounting.requiresAttention ? "requires-attention" : ""}`}
    contentClassName="order-accounting-card__body"
    defaultOpen={defaultExpanded || accounting.requiresAttention}
    summary={<span className="order-accounting-card__summary">
      <AccountingIdentity accounting={accounting} />
      <span className="order-accounting-card__headline">
        <small>{l("Realized", "실현")}</small>
        <strong>{formatAccountingMoney(accounting.realizedBalanceMinor, accounting.currency)}</strong>
        <small>{l("Forecast {amount}", "예상 {amount}", { amount: formatAccountingMoney(accounting.forecastBalanceMinor, accounting.currency) })}</small>
      </span>
    </span>}
  >{content}</Disclosure>;
}

function AccountingIdentity({ accounting }: { accounting: OrderAccountingProjection }) {
  const { l } = useLocale();
  return <div className="order-accounting-identity">
    <span className="order-accounting-identity__badges">
      <Chip mode={accounting.providerEnvironment === "LIVE" ? "live" : "test"}>{accounting.providerEnvironment}</Chip>
      <Chip>{accounting.rail}</Chip>
      {accounting.requiresAttention ? <Chip attention>{l("Attention", "확인 필요")}</Chip> : null}
    </span>
    <strong>{l("Order {id}", "주문 {id}", { id: accounting.agencyOrderId })}</strong>
    <small>{l("Payment", "결제")} <code>{shortID(accounting.customerPaymentId)}</code> · {paymentStateLabel(accounting.paymentState, l)}</small>
  </div>;
}

function BalanceBridge({ actual, adjustment, forecast, compact }: {
  actual: number;
  adjustment: number;
  forecast: number;
  compact: boolean;
}) {
  const { l } = useLocale();
  return <div className={`order-accounting-bridge ${compact ? "is-compact" : ""}`} aria-label={l("Realized-to-forecast balance bridge", "실현·예상 잔고 브리지")}>
    <div><small>{l("Realized balance", "실현 잔고")}</small><strong>{formatAccountingMoney(actual)}</strong><span>{l("Actual cash events only", "실제 현금 이벤트만")}</span></div>
    <i aria-hidden="true">+</i>
    <div><small>{l("Forecast adjustment", "예상 조정")}</small><strong className={adjustment < 0 ? "is-negative" : ""}>{formatAccountingMoney(adjustment)}</strong><span>{l("Unfinished MOs", "미종결 MO")}</span></div>
    <i aria-hidden="true">=</i>
    <div className="is-result"><small>{l("Forecast balance", "예상 잔고")}</small><strong className={forecast < 0 ? "is-negative" : ""}>{formatAccountingMoney(forecast)}</strong><span>{l("Realized + forecast", "실현 + 예상")}</span></div>
  </div>;
}

function ActualFacts({ accounting }: { accounting: OrderAccountingProjection }) {
  const { l } = useLocale();
  return <dl className="order-accounting-facts" aria-label={l("Actual order cash facts", "주문 실제 현금 사실")}>
    <div><dt>{l("Customer gross in", "고객 총수납")}</dt><dd>{formatAccountingMoney(accounting.actualCustomerGrossInMinor)}</dd></div>
    <div><dt>{l("Processor fee", "결제 수수료")}</dt><dd>{formatAccountingDelta(accounting.actualProcessorFeeMinor, "DEBIT")}</dd></div>
    <div><dt>{l("Net cash in", "순수취")}</dt><dd>{formatAccountingMoney(accounting.actualNetCashInMinor)}</dd></div>
    <div><dt>{l("Merchant purchases", "상점 구매")}</dt><dd>{formatAccountingDelta(accounting.actualMerchantSpendMinor, "DEBIT")}</dd></div>
    <div><dt>{l("Customer compensation", "고객 환급")}</dt><dd>{formatAccountingDelta(accounting.actualCustomerCompensatedMinor, "DEBIT")}</dd></div>
    <div><dt>{l("Merchant recovery", "상점 회수")}</dt><dd>{formatAccountingDelta(accounting.actualMerchantRecoveredMinor, "CREDIT")}</dd></div>
  </dl>;
}

function FocusedMerchantOrder({ value }: { value: MerchantOrderAccounting }) {
  const { l } = useLocale();
  return <div className="order-accounting-focus">
    <div><span>{l("This MerchantOrder", "이 MerchantOrder")}</span><strong>{value.shopDomain} #{value.checkoutOrdinal}</strong><small><code>{shortID(value.merchantOrderId || value.allocationId)}</code></small></div>
    <dl>
      <div><dt>{l("Customer gross", "고객 총액")}</dt><dd>{formatAccountingMoney(value.customerGrossMinor)}</dd></div>
      <div><dt>{l("Merchant cost", "상점 비용")}</dt><dd>{formatAccountingMoney(value.passThroughMinor)}</dd></div>
      <div><dt>{l("Allocated fee", "배분 수수료")}</dt><dd>{formatAccountingMoney(value.feeTotalMinor)}</dd></div>
      <div><dt>{l("Funding", "자금")}</dt><dd>{fundingStateLabel(value.fundingState, l)}</dd></div>
    </dl>
  </div>;
}

function MerchantOrderTable({ values }: { values: MerchantOrderAccounting[] }) {
  const { l } = useLocale();
  return <section className="order-accounting-mos" aria-label={l("MerchantOrder accounting", "MerchantOrder 회계")}>
    <header><div><span>{l("MerchantOrders", "MerchantOrders")}</span><h3>{l("Immutable gross by shop checkout", "Shop checkout별 불변 총액")}</h3></div><small>{l("Whole-MO compensation uses this same gross.", "MO 전체 환급은 같은 총액을 사용합니다.")}</small></header>
    <div className="order-accounting__table-wrap"><table>
      <thead><tr><th>{l("Shop checkout", "Shop checkout")}</th><th>{l("Customer gross", "고객 총액")}</th><th>{l("Composition", "구성")}</th><th>{l("Funding / order", "자금 / 주문")}</th><th>{l("Actual → forecast", "실현 → 예상")}</th></tr></thead>
      <tbody>{values.map((value) => <tr key={value.allocationId} className={value.attentionReasons.length ? "requires-attention" : ""}>
        <td><strong>{value.shopDomain}</strong><small>#{value.checkoutOrdinal} · <code>{shortID(value.merchantOrderId || value.allocationId)}</code></small></td>
        <td><strong>{formatAccountingMoney(value.customerGrossMinor)}</strong><small>{value.feePolicyVersion}</small></td>
        <td><span>{formatAccountingMoney(value.passThroughMinor)} + {l("fee", "수수료")} {formatAccountingMoney(value.feeTotalMinor)}</span><small>{l("Variable {variable} · fixed {fixed}", "비율 {variable} · 고정 {fixed}", { variable: formatAccountingMoney(value.feeVariableMinor), fixed: formatAccountingMoney(value.feeFixedMinor) })}</small></td>
        <td><span>{fundingStateLabel(value.fundingState, l)}</span><small>{merchantOrderStateLabel(value.merchantOrderState, l)}</small></td>
        <td><strong>{formatAccountingMoney(value.realizedBalanceMinor)} → {formatAccountingMoney(value.forecastBalanceMinor)}</strong>{value.compensationState ? <small>{l("Compensation", "환급")} {value.compensationState}</small> : null}</td>
      </tr>)}</tbody>
    </table></div>
  </section>;
}

function EventLedger({ events }: { events: OrderAccountingEvent[] }) {
  const { l, locale } = useLocale();
  return <section className="order-accounting-events" aria-label={l("Order cash event timeline", "주문 현금 이벤트 타임라인")}>
    <header><div><span>{l("Event ledger", "이벤트 원장")}</span><h3>{l("What actually moved", "실제로 움직인 금액")}</h3></div><small>{l("Authorization releases are recorded as neutral: no cash moved.", "승인 해제는 현금 이동이 없어 중립으로 기록합니다.")}</small></header>
	    {events.length === 0 ? <p className="order-accounting-events__empty">{l("No cash event has occurred yet. PayPal authorizations remain in forecast; finalized tVit prepayment is recorded as actual cash.", "아직 현금 이벤트가 없습니다. PayPal 승인은 예상 잔고에 남고, 확정된 tVit 선납은 실제 현금으로 기록됩니다.")}</p> : <ol>
      {events.map((event) => <li className={`is-${event.direction.toLowerCase()} ${event.economicsReconciled ? "" : "is-unreconciled"}`} key={event.id}>
        <span className="order-accounting-events__marker" aria-hidden="true" />
        <div><strong>{eventKindLabel(event.kind, l)}</strong><small>{new Date(event.occurredAt).toLocaleString(locale)} · {event.source}{event.cause ? ` · ${event.cause}` : ""}</small>{event.merchantOrderId ? <small>{l("MO", "MO")} <code>{shortID(event.merchantOrderId)}</code></small> : null}</div>
        <span className="order-accounting-events__amount"><strong>{eventAmount(event)}</strong>{!event.economicsReconciled ? <small>{l("Unreconciled", "미대사")}</small> : <small>{directionLabel(event.direction, l)}</small>}</span>
      </li>)}
    </ol>}
  </section>;
}

function eventAmount(event: OrderAccountingEvent) {
  if (event.direction === "NEUTRAL") return "—";
  return `${event.direction === "CREDIT" ? "+" : "−"}${formatAccountingMoney(Math.abs(event.amountMinor))}`;
}

function shortID(value: string) {
  return value.length > 12 ? value.slice(0, 12) : value;
}

function eventKindLabel(kind: OrderAccountingEvent["kind"], l: Localize) {
  return {
    CUSTOMER_CASH_IN: l("Customer cash in", "고객 수납"),
    MERCHANT_PURCHASE: l("Merchant purchase", "상점 구매"),
    CUSTOMER_COMPENSATION: l("Customer compensation", "고객 환급"),
    AUTHORIZATION_RELEASE: l("Authorization release", "승인 해제"),
    MERCHANT_RECOVERY: l("Merchant recovery", "상점 회수"),
  }[kind];
}

function directionLabel(direction: OrderAccountingEvent["direction"], l: Localize) {
  return {
    CREDIT: l("Cash credit", "현금 유입"),
    DEBIT: l("Cash debit", "현금 유출"),
    NEUTRAL: l("No cash movement", "현금 이동 없음"),
  }[direction];
}

function fundingStateLabel(state: string, l: Localize) {
  return {
    AVAILABLE: l("Available", "활성화 전"),
    ACTIVATION_PENDING: l("Capture pending", "Capture 진행 중"),
    ACTIVATION_UNKNOWN: l("Capture outcome unknown", "Capture 결과 확인 중"),
    ACTIVE: l("Active", "활성"),
    RELEASE_PENDING: l("Release pending", "해제 진행 중"),
    RELEASE_UNKNOWN: l("Release outcome unknown", "해제 결과 확인 중"),
    RELEASED: l("Released", "해제됨"),
    FAILED: l("Failed", "실패"),
  }[state] ?? state;
}

function merchantOrderStateLabel(state: string | undefined, l: Localize) {
  if (!state) return l("Not planned yet", "아직 생성 전");
  return {
    PLANNED: l("Purchase planned", "구매 예정"),
    READY_TO_PLACE: l("Ready to purchase", "구매 준비"),
    PLACEMENT_PENDING: l("Purchase in progress", "구매 진행 중"),
    PLACED: l("Purchased", "구매 완료"),
    PLACEMENT_UNKNOWN: l("Purchase outcome unknown", "구매 결과 확인 중"),
    FAILED: l("Purchase failed", "구매 실패"),
    CANCELLED: l("Closed without purchase", "구매 없이 종료"),
  }[state] ?? state;
}

function attentionReasonLabel(reason: string, l: Localize) {
  return {
    PAYPAL_CASH_UNRECONCILED: l("PayPal cash is unreconciled", "PayPal 수납 미대사"),
    MERCHANT_PURCHASE_UNKNOWN: l("Merchant purchase outcome is unknown", "상점 구매 결과 확인 중"),
    CUSTOMER_COMPENSATION_EXPECTED: l("Customer compensation is expected", "고객 환급 예정"),
    AUTHORIZATION_RELEASE_EXPECTED: l("Authorization release is expected", "승인 해제 예정"),
	    FUNDING_EFFECT_UNKNOWN: l("MO funding outcome is unknown", "MO 자금 결과 확인 중"),
	    FUNDING_OUTCOME_UNKNOWN: l("MO funding outcome is unknown", "MO 자금 결과 확인 중"),
    COMPENSATION_OUTCOME_UNKNOWN: l("Compensation outcome is unknown", "환급 결과 확인 중"),
    NEGATIVE_FORECAST_BALANCE: l("Forecast balance is negative", "예상 잔고가 음수"),
  }[reason] ?? reason;
}
