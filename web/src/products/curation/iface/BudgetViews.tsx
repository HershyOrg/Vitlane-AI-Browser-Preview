import { useBudget, useBudgetTarget } from "../app/useBudget";
import { budgetMinor } from "../domain/budget";
import { useResearchCurrency } from "../research/app/useResearchCurrency";
import { convertResearchMinor } from "../research/domain/exchangeRate";
import { formatMinor, type LiveCartItem } from "../research/infra/liveCatalogReviewApi";
import type { CandidatePrice } from "../domain/candidatePresentation";
import { useLocale } from "../../../shared/i18n";

export function useBudgetMoney(compact = false) {
  const { currency, exchange } = useResearchCurrency();
  return (amount: bigint, from: string) => {
    const converted = currency === from ? Number(amount) : exchange?.rate ? convertResearchMinor(Number(amount), from, currency, exchange.rate) : undefined;
    const destination = converted === undefined ? from : currency;
    const value = converted ?? Number(amount);
    const formatted = compact ? new Intl.NumberFormat("en-US", { style: "currency", currency: destination, minimumFractionDigits: 0, maximumFractionDigits: destination === "KRW" ? 0 : 2 }).format(value / (destination === "KRW" ? 1 : 100)) : formatMinor(value, destination);
    return `${!compact && converted !== undefined && from !== currency ? "≈ " : ""}${formatted}`;
  };
}

/**
 * Whether a price fits the Target's unit budget: true or false when both can be
 * compared in the budget's currency, undefined otherwise (no budget, unknown
 * price, or no exchange rate). Vitlane Pick reads this (ADR-0086).
 */
export function useBudgetFit(targetId?: string) {
  const scopedTarget = useBudgetTarget(); const { ledger } = useBudget(); const { exchange } = useResearchCurrency();
  const allocation = ledger?.enabled ? ledger.allocations.find(a => a.targetId === (targetId ?? scopedTarget)) : undefined;
  const limit = allocation && ledger ? budgetMinor(allocation.amount, ledger.currency) : undefined;
  return (price: CandidatePrice): boolean | undefined => {
    if (!ledger || !allocation || limit === undefined || price.kind === "UNKNOWN") return undefined;
    const source = price.kind === "OBSERVED" ? price.amountMinor : price.minimumMinor;
    const minor = price.currency === ledger.currency ? source : exchange?.rate ? convertResearchMinor(source, price.currency, ledger.currency, exchange.rate) : undefined;
    if (minor === undefined || !Number.isSafeInteger(minor)) return undefined;
    return BigInt(minor) * BigInt(allocation.quantity) <= limit;
  };
}

export function TargetBudgetLabel({ targetId }: { targetId: string }) {
  const { ledger } = useBudget(); const { l } = useLocale(); const money = useBudgetMoney();
  const a = ledger?.allocations.find(a => a.targetId === targetId); if (!a || !ledger) return null;
  const amount = budgetMinor(a.amount, ledger.currency);
  return <span className="budget-target-label">{l("Goal ×{quantity}", "목표 ×{quantity}", { quantity: a.quantity })} · {ledger.enabled && amount !== undefined ? l("Budget {amount}", "예산 {amount}", { amount: money(amount, ledger.currency) }) : l("No limit", "제한 없음")}</span>;
}

export function CandidateBudgetDelta({ price, targetId }: { price: CandidatePrice; targetId?: string }) {
  const scopedTarget = useBudgetTarget(); const { ledger } = useBudget(); const { l } = useLocale(); const { exchange } = useResearchCurrency(); const money = useBudgetMoney();
  const a = ledger?.allocations.find(a => a.targetId === (targetId ?? scopedTarget));
  if (!ledger?.enabled || !a) return null;
  const limit = budgetMinor(a.amount, ledger.currency);
  if (price.kind === "UNKNOWN" || limit === undefined) return <small className="budget-delta">{l("Budget comparison unavailable", "예산 비교 미확인")}</small>;
  const source = price.kind === "OBSERVED" ? price.amountMinor : price.minimumMinor;
  const minor = price.currency === ledger.currency ? source : exchange?.rate ? convertResearchMinor(source, price.currency, ledger.currency, exchange.rate) : undefined;
  if (minor === undefined || !Number.isSafeInteger(minor)) return <small className="budget-delta">{l("Budget comparison unavailable", "예산 비교 미확인")}</small>;
  const delta = BigInt(minor) * BigInt(a.quantity) - limit;
  const abs = delta < 0n ? -delta : delta;
  const rounded = (abs + BigInt(Math.floor(a.quantity / 2))) / BigInt(a.quantity);
  const floor = budgetMinor(a.minimumUnitAmount, ledger.currency);
  const text = delta === 0n ? l("At budget", "예산과 같음") : `${delta < 0n ? "−" : "+"}${money(rounded, ledger.currency)}`;
  return <small className={`budget-delta ${delta < 0n ? "is-saving" : delta > 0n ? "is-over" : ""}`} title={l("Unit price compared with target budget ÷ goal quantity", "상품 단가와 Target 예산 ÷ 목표 수량 비교")}>{price.kind === "RANGE" ? `${l("From", "최저가 기준")} ` : ""}<span className="budget-delta__amount">{text}</span>{delta > 0n ? ` · ${l("Over budget", "예산 초과")}` : ""}{floor !== undefined && BigInt(minor) < floor ? ` · ${l("Below minimum", "하한 아래")}` : ""}</small>;
}

export function CartBudgetView({ items }: { items: LiveCartItem[] }) {
  const { ledger } = useBudget(); const { l } = useLocale(); const { exchange } = useResearchCurrency(); const money = useBudgetMoney();
  if (!ledger) return null;
  if (!ledger.enabled) return <div className="budget-cart-summary">{l("Budget", "예산")} · {l("No limit", "제한 없음")}</div>;
  const total = budgetMinor(ledger.totalAmount, ledger.currency); let sum = 0n; let known = total !== undefined;
  for (const item of items) {
    const original = item.previewPriceMinor * item.quantity;
    const converted = item.previewCurrency === ledger.currency ? original : exchange?.rate ? convertResearchMinor(original, item.previewCurrency, ledger.currency, exchange.rate) : undefined;
    if (converted === undefined || !Number.isSafeInteger(converted) || converted < 0) { known = false; break; } sum += BigInt(converted);
  }
  const savings = known && total !== undefined ? total - sum : undefined;
  return <div className="budget-cart-summary" aria-live="polite"><span>{savings !== undefined && savings < 0n ? l("Over budget", "예산 초과") : l("Savings vs budget", "예산 대비 절약")}</span><strong>{savings === undefined ? l("Comparison unavailable", "비교 미확인") : money(savings < 0n ? -savings : savings, ledger.currency)}</strong><small>{l("Merchandise total only. Shipping, tax and fees are excluded.", "상품 금액 합계 기준 · 배송비, 세금, 수수료 제외")}</small></div>;
}
