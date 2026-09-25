import type { Currency, Money, PlanItemView, PlanTotals } from "./models";

const amount = (currency: Currency, value: number): Money => {
  const valid = currency === "KRW"
    ? Number.isSafeInteger(value)
    : Number.isFinite(value) && Math.abs(Math.round(value * 100) - value * 100) < 1e-7;
  if (!valid || value < 0) {
    throw new RangeError(`${currency} amounts must be non-negative valid major-unit values`);
  }
  return { currency, amount: currency === "USD" ? Math.round(value * 100) / 100 : value };
};

export function calculatePlanTotals(
  items: readonly PlanItemView[],
  shippingByMerchant: Readonly<Record<string, number | null>>,
  budget: number | Money,
): PlanTotals {
  const selectedItems = items.filter((item) => item.state === "selected");
  const currency = typeof budget === "number"
    ? selectedItems[0]?.unitPrice.currency ?? "KRW"
    : budget.currency;
  if (selectedItems.some((item) => item.unitPrice.currency !== currency)) {
    throw new RangeError("Plan totals require one currency");
  }
  const budgetAmount = typeof budget === "number" ? budget : budget.amount;
  const subtotalAmount = normalize(currency, selectedItems.reduce(
    (total, item) => total + item.unitPrice.amount * item.quantity,
    0,
  ));
  const activeMerchantIds = [...new Set(
    selectedItems
      .map((item) => item.merchantId)
      .filter((merchantId): merchantId is string => merchantId !== null),
  )];
  const unknownMerchantIds = activeMerchantIds.filter(
    (merchantId) => shippingByMerchant[merchantId] == null,
  );
  const knownShippingAmount = normalize(currency, activeMerchantIds.reduce((total, merchantId) => {
    const shipping = shippingByMerchant[merchantId];
    return shipping == null ? total : total + shipping;
  }, 0));
  const knownTotalAmount = normalize(currency, subtotalAmount + knownShippingAmount);
  const totalComplete = unknownMerchantIds.length === 0;

  return {
    subtotal: amount(currency, subtotalAmount),
    knownShipping: amount(currency, knownShippingAmount),
    knownTotal: amount(currency, knownTotalAmount),
    totalState: totalComplete ? "COMPLETE" : "PARTIAL",
    hasUnknownFees: !totalComplete,
    unknownMerchantIds,
    budget: amount(currency, budgetAmount),
    remainingBudget: amount(currency, normalize(currency, Math.max(0, budgetAmount - knownTotalAmount))),
    overBudget: amount(currency, normalize(currency, Math.max(0, knownTotalAmount - budgetAmount))),
    budgetComparisonState: totalComplete ? "AVAILABLE" : "INCOMPLETE",
    budgetState: "LIMITED",
  };
}

function normalize(currency: Currency, value: number): number {
  return currency === "USD" ? Math.round(value * 100) / 100 : value;
}

export function withPlanRevision(
  plan: Omit<import("./models").PlanView, "revision" | "totals">,
  revision: number,
  budgetAmount: number,
): import("./models").PlanView {
  return {
    ...plan,
    revision,
    totals: calculatePlanTotals(plan.items, plan.shippingByMerchant, budgetAmount),
  };
}
