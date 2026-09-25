export type ExchangeRate = { base: "USD"; quote: "KRW"; rate: string; asOf: string; observedAt: string; source: string };
export type ExchangeRateView = { schemaVersion: string; status: "CURRENT" | "STALE" | "UNAVAILABLE"; rate?: ExchangeRate };

export function convertResearchMinor(amount: number, from: string, to: string, rate: ExchangeRate, now = Date.now()): number | undefined {
  if (!Number.isSafeInteger(amount) || amount < 0 || !["USD", "KRW"].includes(from) || !["USD", "KRW"].includes(to)) return undefined;
  if (from === to) return amount;
  const age = now - Date.parse(`${rate.asOf}T00:00:00Z`);
  if (!Number.isFinite(age) || age < 0 || age >= 8 * 86400000 || rate.base !== "USD" || rate.quote !== "KRW" || !/^\d{1,6}(?:\.\d{1,12})?$/.test(rate.rate)) return undefined;
  const [whole, decimal = ""] = rate.rate.split(".");
  const numerator = BigInt(whole + decimal);
  if (numerator <= 0n) return undefined;
  const denominator = 10n ** BigInt(decimal.length) * 100n;
  const n = BigInt(amount) * (from === "USD" ? numerator : denominator);
  const d = from === "USD" ? denominator : numerator;
  const minor = (n * 2n + d) / (d * 2n);
  return minor <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(minor) : undefined;
}
