import { expect, it } from "vitest";
import { convertResearchMinor } from "./exchangeRate";
import { formatMinor } from "../infra/liveCatalogReviewApi";
const now = Date.parse("2026-09-11T03:00:00Z");
const rate = { base: "USD", quote: "KRW", rate: "1340.18", asOf: "2026-09-10", observedAt: "2026-09-11T00:00:00Z", source: "https://frankfurter.dev/" } as const;
it("KRW has zero decimal places, USD has two, and conversion preserves original money", () => {
 const original = { amount: 1234, currency: "USD" };
 expect(convertResearchMinor(original.amount, original.currency, "KRW", rate, now)).toBe(16538);
 expect(convertResearchMinor(23400, "KRW", "USD", rate, now)).toBe(1746);
 expect(original).toEqual({ amount: 1234, currency: "USD" });
 expect(formatMinor(23400, "KRW")).toContain("23,400"); expect(formatMinor(1234, "USD")).toContain("12.34");
});
it("missing, future, stale and unsafe values never become displayed estimates", () => {
 for (const patch of [{ asOf: "2026-09-01" }, { asOf: "2026-09-12" }, { rate: "0" }, { rate: "NaN" }]) expect(convertResearchMinor(100, "USD", "KRW", { ...rate, ...patch }, now)).toBeUndefined();
 expect(convertResearchMinor(Number.MAX_SAFE_INTEGER + 1, "USD", "KRW", rate, now)).toBeUndefined();
 expect(convertResearchMinor(1.2, "KRW", "USD", rate, now)).toBeUndefined();
});
