export const budgetSchema = "vitlane.curation-budget.v1" as const;
export type BudgetCurrency = "KRW" | "USD";
export type TargetBudget = { targetId: string; quantity: number; amount: string | null; minimumUnitAmount?: string };
export type BudgetLedger = { schemaVersion: typeof budgetSchema; version: number; researchVersion: number; enabled: boolean; currency: BudgetCurrency; totalAmount: string | null; allocations: TargetBudget[] };
export type BudgetCommand = {
  schemaVersion: typeof budgetSchema; commandId: string; expectedVersion: number;
  kind: "ENABLE" | "DISABLE" | "SET_TOTAL" | "SET_TARGET" | "SET_QUANTITY" | "SET_MINIMUM";
  currency?: BudgetCurrency; totalAmount?: string; allocationMode?: "AUTO" | "EQUAL" | "MANUAL" | "PROPORTIONAL";
  allocations?: TargetBudget[]; targetId?: string; amount?: string; quantity?: number; minimumUnitAmount?: string; updateMinimum?: boolean;
};

export function budgetMinor(amount: string | null | undefined, currency: string): bigint | undefined {
  if (amount == null || amount.length > 20 || !/^(0|[1-9]\d*)(?:\.\d{1,2})?$/.test(amount) || !["USD", "KRW"].includes(currency)) return undefined;
  const [whole, fraction = ""] = amount.split(".");
  if (currency === "KRW" && fraction) return undefined;
  const result = BigInt(whole) * (currency === "USD" ? 100n : 1n) + BigInt(currency === "USD" ? fraction.padEnd(2, "0") : "0");
  return result <= BigInt(Number.MAX_SAFE_INTEGER) ? result : undefined;
}
export function budgetAmount(minor: bigint, currency: string): string {
  return currency === "USD" ? `${minor / 100n}.${(minor % 100n).toString().padStart(2, "0")}` : minor.toString();
}
export function allocateBudget(total: bigint, weights: bigint[]): bigint[] {
  if (!weights.length) return [];
  let sum = weights.reduce((a, b) => a + b, 0n);
  if (sum === 0n) { weights = weights.map(() => 1n); sum = BigInt(weights.length); }
  const amounts = weights.map(w => total * w / sum);
  const order = weights.map((w, index) => ({ index, remainder: total * w % sum })).sort((a, b) => a.remainder === b.remainder ? a.index - b.index : a.remainder > b.remainder ? -1 : 1);
  const left = total - amounts.reduce((a, b) => a + b, 0n);
  for (let i = 0; i < Number(left); i++) amounts[order[i].index]++;
  return amounts;
}
export function allocationTotal(allocations: TargetBudget[], currency: string): bigint | undefined {
  let total = 0n;
  for (const allocation of allocations) { const amount = budgetMinor(allocation.amount, currency); if (amount === undefined) return undefined; total += amount; }
  return total <= BigInt(Number.MAX_SAFE_INTEGER) ? total : undefined;
}
