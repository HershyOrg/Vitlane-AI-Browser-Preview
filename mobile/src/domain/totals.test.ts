import type { PlanItemView } from "./models";
import { calculatePlanTotals } from "./totals";

const text = (value: string) => ({ "ko-KR": value, "en-US": value });

const item = (
  id: string,
  merchantId: string | null,
  quantity: number,
  unitPrice: number,
  state: PlanItemView["state"] = "selected",
): PlanItemView => ({
  id,
  kind: "product",
  title: text(id),
  merchantId,
  merchantLabel: merchantId ? text(merchantId) : null,
  quantity,
  unitPrice: { currency: "KRW", amount: unitPrice },
  necessity: "required",
  state,
});

describe("calculatePlanTotals", () => {
  it("counts shipping once per active merchant and ignores removed or owned items", () => {
    const totals = calculatePlanTotals(
      [
        item("snacks", "demo-a", 2, 18000),
        item("drinks", "demo-a", 2, 12000),
        item("decor", "demo-b", 1, 14000, "removed"),
        item("cups", null, 6, 0, "already_owned"),
      ],
      { "demo-a": 5000, "demo-b": 3000 },
      80000,
    );

    expect(totals.subtotal.amount).toBe(60000);
    expect(totals.knownShipping.amount).toBe(5000);
    expect(totals.knownTotal.amount).toBe(65000);
    expect(totals.remainingBudget.amount).toBe(15000);
    expect(totals.overBudget.amount).toBe(0);
    expect(totals.budgetComparisonState).toBe("AVAILABLE");
  });

  it("marks a total as incomplete when an active merchant shipping fee is unknown", () => {
    const totals = calculatePlanTotals(
      [item("decor", "demo-b", 1, 14000)],
      { "demo-b": null },
      150000,
    );

    expect(totals.knownTotal.amount).toBe(14000);
    expect(totals.hasUnknownFees).toBe(true);
    expect(totals.totalState).toBe("PARTIAL");
    expect(totals.budgetComparisonState).toBe("INCOMPLETE");
    expect(totals.unknownMerchantIds).toEqual(["demo-b"]);
  });

  it("keeps USD totals in USD major units", () => {
    const usdItem: PlanItemView = {
      ...item("lamp", "merchant-us", 2, 24.99),
      unitPrice: { currency: "USD", amount: 24.99 },
    };
    const totals = calculatePlanTotals(
      [usdItem],
      { "merchant-us": 5 },
      { currency: "USD", amount: 100 },
    );

    expect(totals.knownTotal).toEqual({ currency: "USD", amount: 54.98 });
    expect(totals.remainingBudget).toEqual({ currency: "USD", amount: 45.02 });
  });

  it("keeps a committed overage distinct from a zero remainder", () => {
    const totals = calculatePlanTotals(
      [item("snacks", "demo-a", 1, 90000)],
      { "demo-a": 5000 },
      80000,
    );

    expect(totals.remainingBudget.amount).toBe(0);
    expect(totals.overBudget).toEqual({ currency: "KRW", amount: 15000 });
  });
});
