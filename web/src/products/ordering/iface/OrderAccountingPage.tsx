import { useCallback, useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { Button, Disclosure, FeedbackState, NativeSelect, NativeSelectOption, Notice, PageHeader } from "../../../shared/ui";
import { useLocale } from "../../../shared/i18n";
import {
  getOrderAccounting,
  type AccountingEnvironment,
  type OrderAccountingSummary,
} from "../infra/agencyOrderOperatorApi";
import { formatAccountingDelta, formatAccountingMoney, OrderAccountingPanel } from "./OrderAccountingPanel";
import { RecoveryLedgerPanel } from "./RecoveryLedgerPanel";
import "./agency-order.css";

export function OrderAccountingPage() {
  const { l, locale } = useLocale();
  const [searchParams] = useSearchParams();
  const recoveryOrder = searchParams.get("recoveryOrder") ?? undefined;
  const [view, setView] = useState<"LEDGER" | "RECOVERY">(recoveryOrder ? "RECOVERY" : "LEDGER");
  const [environment, setEnvironment] = useState<AccountingEnvironment>("LIVE");
  const [summary, setSummary] = useState<OrderAccountingSummary>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  const load = useCallback(async () => {
    try {
      const response = await getOrderAccounting(environment);
      setSummary(response.summary);
      setError(undefined);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : l("We couldn't load order accounting.", "주문 회계를 불러오지 못했습니다."));
    } finally {
      setLoading(false);
    }
  }, [environment, l]);

  useEffect(() => {
    setLoading(true);
    void load();
    const timer = window.setInterval(() => void load(), 10_000);
    return () => window.clearInterval(timer);
  }, [load]);

  const orders = summary
    ? [...summary.orders].sort((left, right) => {
        if (left.requiresAttention !== right.requiresAttention) return left.requiresAttention ? -1 : 1;
        return new Date(right.createdAt).getTime() - new Date(left.createdAt).getTime();
      })
    : [];

  return <main className="order-ui-operator-console order-accounting">
    <PageHeader
      eyebrow={l("Order accounting", "주문 회계")}
      title={l("Cash truth by order", "주문별 현금 원장")}
      description={l(
        "Read actual cash events first, then the forecast for unfinished MerchantOrders. Authorization releases stay neutral; environments are never combined.",
        "실제 현금 이벤트를 먼저 보고, 미종결 MerchantOrder의 예상값을 이어서 봅니다. 승인 해제는 중립이며 환경 간 금액은 합산하지 않습니다.",
      )}
      secondaryActions={[{ label: l("Refresh now", "지금 갱신"), onClick: () => void load() }]}
      summary={<div className="order-accounting__environment"><label>{l("Environment", "환경")}<NativeSelect aria-label={l("Accounting environment", "회계 환경")} size="sm" value={environment} onChange={(event) => setEnvironment(event.target.value as AccountingEnvironment)}><NativeSelectOption value="LIVE">{l("LIVE · PayPal", "LIVE · PayPal")}</NativeSelectOption><NativeSelectOption value="SANDBOX">{l("SANDBOX · PayPal", "SANDBOX · PayPal")}</NativeSelectOption><NativeSelectOption value="TESTNET">{l("TESTNET · tVitUSDC", "TESTNET · tVitUSDC")}</NativeSelectOption></NativeSelect></label></div>}
    />

    <div aria-label={l("Order accounting view", "주문 회계 보기")} className="workspace-history-views" role="group">
      {([[
        "LEDGER", l("Event ledger", "이벤트 원장"),
      ], ["RECOVERY", l("Record recovery", "회수 기입")]] as const).map(([value, label]) => (
        <Button aria-pressed={view === value} emphasis="quiet" key={value} onClick={() => setView(value)} size="compact" type="button">{label}</Button>
      ))}
    </div>

    {view === "RECOVERY" ? <RecoveryLedgerPanel initialOrderId={recoveryOrder} /> : null}
    {view === "LEDGER" ? <>
      <EnvironmentNotice environment={environment} />
      {error ? <Notice announce tone="danger">{error}</Notice> : null}
      {loading && !summary ? <FeedbackState description={l("Folding cash, purchase, compensation, and recovery events.", "수납·구매·환급·회수 이벤트를 집계하고 있습니다.")} state="loading" title={l("Loading order accounting", "주문 회계를 불러오는 중입니다")} /> : null}
      {summary ? <>
        <section className="order-accounting__balance-sheet" aria-label={l("Realized and forecast balances", "실현 및 예상 잔고")}>
          <header><div><span>{l("As of", "기준 시각")}</span><strong>{new Date(summary.asOf).toLocaleString(locale)}</strong></div><small>{l("{count} orders · {attention} need attention", "주문 {count}건 · 확인 필요 {attention}건", { count: summary.orderCount, attention: summary.attentionOrderCount })}</small></header>
          <div className="order-accounting__balance-bridge" aria-label={l("Portfolio balance equation", "전체 잔고 계산") }>
            <div><small>{l("Realized balance", "실현 잔고")}</small><strong>{formatAccountingMoney(summary.realizedBalanceMinor)}</strong><span>{l("Cash events completed", "완료된 현금 이벤트")}</span></div>
            <i aria-hidden="true">+</i>
            <div><small>{l("Forecast adjustment", "예상 조정")}</small><strong className={summary.forecastAdjustmentMinor < 0 ? "is-negative" : ""}>{formatAccountingMoney(summary.forecastAdjustmentMinor)}</strong><span>{l("Unfinished MOs", "미종결 MO")}</span></div>
            <i aria-hidden="true">=</i>
            <div className="is-result"><small>{l("Forecast balance", "예상 잔고")}</small><strong className={summary.forecastBalanceMinor < 0 ? "is-negative" : ""}>{formatAccountingMoney(summary.forecastBalanceMinor)}</strong><span>{environment}</span></div>
          </div>
          <dl className="order-accounting__actuals">
            <div><dt>{l("Customer gross in", "고객 총수납")}</dt><dd>{formatAccountingMoney(summary.actualCustomerGrossInMinor)}</dd></div>
            <div><dt>{l("Processor fee", "결제 수수료")}</dt><dd>{formatAccountingDelta(summary.actualProcessorFeeMinor, "DEBIT")}</dd></div>
            <div><dt>{l("Net cash in", "순수취")}</dt><dd>{formatAccountingMoney(summary.actualNetCashInMinor)}</dd></div>
            <div><dt>{l("Merchant purchases", "상점 구매")}</dt><dd>{formatAccountingDelta(summary.actualMerchantSpendMinor, "DEBIT")}</dd></div>
            <div><dt>{l("Customer compensation", "고객 환급")}</dt><dd>{formatAccountingDelta(summary.actualCustomerCompensatedMinor, "DEBIT")}</dd></div>
            <div><dt>{l("Merchant recovery", "상점 회수")}</dt><dd>{formatAccountingDelta(summary.actualMerchantRecoveredMinor, "CREDIT")}</dd></div>
          </dl>
          <Disclosure className="order-accounting__forecast-basis" contentClassName="order-accounting__forecast-basis-body" summary={l("Forecast basis", "예상 잔고 근거")}>
            <dl>
              <div><dt>{l("Forecast net cash in", "예상 순수취")}</dt><dd>{formatAccountingDelta(summary.forecastNetCashInMinor, "CREDIT")}</dd></div>
              <div><dt>{l("Forecast processor fee", "예상 결제 수수료")}</dt><dd>{formatAccountingDelta(summary.forecastProcessorFeeMinor, "DEBIT")}</dd></div>
              <div><dt>{l("Forecast merchant spend", "예상 상점 구매")}</dt><dd>{formatAccountingDelta(summary.forecastMerchantSpendMinor, "DEBIT")}</dd></div>
              <div><dt>{l("Expected compensation", "예상 고객 환급")}</dt><dd>{formatAccountingDelta(summary.expectedCompensationMinor, "DEBIT")}</dd></div>
            </dl>
            <p>{l("Forecast is an operational estimate for unresolved MerchantOrders. It is not a bank balance, tax statement, or accrual P&L.", "예상 잔고는 미종결 MerchantOrder를 위한 운영 추정치입니다. 은행 잔고·세무 장부·발생주의 손익이 아닙니다.")}</p>
          </Disclosure>
          {summary.unreconciledCashGrossMinor > 0 ? <Notice tone="warning">{l("Unreconciled customer cash: {amount}. It is visible but excluded from realized net cash until processor economics are confirmed.", "미대사 고객 수납: {amount}. 금액은 표시하지만 결제 경제성이 확정될 때까지 실현 순수취에서 제외합니다.", { amount: formatAccountingMoney(summary.unreconciledCashGrossMinor) })}</Notice> : null}
        </section>

        <section className="order-accounting__orders" aria-label={l("Accounting by order", "주문별 회계")}>
          <div className="order-accounting__section-heading"><div><span>{l("Order ledgers", "주문 원장")}</span><h2>{l("Orders and event timelines", "주문과 이벤트 타임라인")}</h2></div><div><b>{l("{count} orders", "{count}건", { count: summary.orderCount })}</b>{summary.attentionOrderCount > 0 ? <b className="is-attention">{l("Attention {count}", "확인 필요 {count}", { count: summary.attentionOrderCount })}</b> : null}</div></div>
          {orders.length === 0 ? <FeedbackState description={l("No accounting facts exist in {environment}.", "{environment}에 회계 사실이 없습니다.", { environment })} state="empty" title={l("There are no orders to summarize", "집계할 주문이 없습니다")} /> : orders.map((accounting, index) => <OrderAccountingPanel accounting={accounting} defaultExpanded={index === 0} key={accounting.agencyOrderId} />)}
        </section>
      </> : null}
    </> : null}
  </main>;
}

function EnvironmentNotice({ environment }: { environment: AccountingEnvironment }) {
  const { l } = useLocale();
  if (environment === "LIVE") {
    return <Notice tone="danger"><strong>{l("LIVE · PayPal", "LIVE · PayPal")}</strong>{" "}{l("This ledger represents real-money effects. Verify provider references before acting on a discrepancy.", "이 원장은 실제 자금 효과를 나타냅니다. 불일치에 대응하기 전 provider reference를 확인하세요.")}</Notice>;
  }
  if (environment === "TESTNET") {
    return <Notice tone="neutral"><strong>{l("TESTNET · tVitUSDC", "TESTNET · tVitUSDC")}</strong>{" "}{l("Testnet token accounting only. It never mixes with PayPal Sandbox or Live.", "테스트넷 토큰 회계만 표시합니다. PayPal Sandbox·Live와 합산하지 않습니다.")}</Notice>;
  }
  return <Notice tone="neutral"><strong>{l("SANDBOX · PayPal", "SANDBOX · PayPal")}</strong>{" "}{l("Operational test data only. Switching the filter does not change an order's recorded environment.", "운영 테스트 데이터만 표시합니다. 필터를 바꿔도 주문에 기록된 환경은 바뀌지 않습니다.")}</Notice>;
}
