import { describe, expect, it } from "vitest";
import { allocateBudget, allocationTotal, budgetAmount, budgetMinor } from "./budget";

describe("budget minor-unit arithmetic", () => {
  it("preserves every cent and deterministic target order", () => {
    expect(allocateBudget(10001n, [1n, 1n, 1n])).toEqual([3334n, 3334n, 3333n]);
    for (const total of [0n, 1n, 19999n, 9007199254740991n]) {
      expect(allocateBudget(total, [2n, 3n, 7n]).reduce((a, b) => a + b, 0n)).toBe(total);
    }
    expect(allocateBudget(5n, [0n, 0n])).toEqual([3n, 2n]);
  });
  it("rejects unknown, invalid precision and unsafe amounts", () => {
    for (const value of [null, undefined, "", "-1", "1e6", "1.001", "9007199254740992"]) expect(budgetMinor(value, "USD")).toBeUndefined();
    expect(budgetMinor("1.00", "KRW")).toBeUndefined();
    expect(budgetMinor("12.05", "USD")).toBe(1205n);
    expect(budgetAmount(1205n, "USD")).toBe("12.05");
    expect(allocationTotal([{ targetId: "a", amount: "12.05", quantity: 2 }, { targetId: "b", amount: "1.02", quantity: 1 }], "USD")).toBe(1307n);
  });
});
