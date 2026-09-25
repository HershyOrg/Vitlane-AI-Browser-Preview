// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { LocaleProvider } from "../../../../shared/i18n";
import { ResearchRoundSummary } from "./ResearchRoundSummary";
afterEach(() => { vi.unstubAllGlobals(); localStorage.clear(); });
const summary = {
  schemaVersion: "vitlane.research-round-summary.v1", windowDays: 7, since: "2026-09-08T00:00:00Z", generatedAt: "2026-09-15T00:00:00Z",
  rounds: [{ country: "KR", status: "RESULTS_READY", count: 4 }, { country: "KR", status: "FAILED", count: 2 }],
  failures: [{ failureCode: "PROVIDER_RESPONSE_INVALID", stepKind: "RANKING", retryable: false, count: 2 }],
  sources: [{ country: "KR", source: "ELEVENST", status: "FAILED", reasonCode: "CATALOG_TIMEOUT", count: 1 }, { country: "KR", source: "COUPANG", status: "SUCCEEDED", reasonCode: "", count: 4 }],
  apiCalls: [{ apiId: "OWN_PRODUCT", outcome: "CATALOG_API_RATE_LIMITED", billable: false, count: 3 }],
  admitted: { rounds: 4, median: 3.5, buckets: [{ label: "0", count: 0 }, { label: "1-3", count: 2 }, { label: "4-7", count: 2 }, { label: "8-15", count: 0 }, { label: "16+", count: 0 }] },
  evaluation: { evaluated: 14, unevaluated: 0 },
  steps: [{ kind: "RANKING", count: 4, p50Seconds: 61.5, p95Seconds: 118 }],
  attempts: [{ status: "EFFECT_UNKNOWN", failureCode: "EXTERNAL_EFFECT_UNKNOWN", count: 1 }, { status: "FAILED", failureCode: "QUOTA_EXCEEDED", count: 2 }],
  reservations: [{ status: "UNKNOWN", count: 1, amountMicros: 70730 }],
};
for (const locale of ["en-US", "ko-KR"]) it(`${locale}: renders the ledger tables from the summary endpoint and recovers from a failed load`, async () => {
  document.cookie = `vt_locale_choice=${locale}; Path=/`;
  let fail = false;
  const fetchMock = vi.fn(async (url: string) => {
    expect(url).toBe("/api/v1/admin/catalog-apis/round-summary?days=7");
    if (fail) return Response.json({ error: { code: "INTERNAL_ERROR", message: "boom" } }, { status: 500 });
    return Response.json(summary);
  });
  vi.stubGlobal("fetch", fetchMock);
  const el = document.createElement("div"); document.body.append(el); const root = createRoot(el);
  try {
    await act(async () => root.render(<LocaleProvider><ResearchRoundSummary /></LocaleProvider>));
    const text = el.textContent ?? "";
    expect(text).toContain(locale === "ko-KR" ? "후보 확보" : "Candidates found");
    expect(text).toContain(locale === "ko-KR" ? "후보 비교 중" : "Comparing candidates");
    expect(text).toContain("PROVIDER_RESPONSE_INVALID");
    expect(text).toContain(locale === "ko-KR" ? "11번가" : "11st");
    expect(text).toContain("CATALOG_TIMEOUT");
    expect(text).toContain(locale === "ko-KR" ? "로컬 거절" : "Local denial");
    expect(text).toContain("3.5");
    expect(text).toContain("14 / 0");
    expect(text).toContain("118.0s");
    expect(text).toContain("QUOTA_EXCEEDED");
    expect(text).toContain(locale === "ko-KR" ? "결과 미확인" : "Result unconfirmed");
    expect(text).toContain("$0.0707");
    expect(el.querySelector('[role="alert"]')).toBeNull();
    fail = true;
    const refresh = [...el.querySelectorAll<HTMLButtonElement>("button")].find(b => b.textContent === (locale === "ko-KR" ? "요약 새로고침" : "Refresh summary"))!;
    await act(async () => refresh.click());
    expect(el.querySelector('[role="alert"]')).not.toBeNull();
    // The last confirmed summary stays on screen while the alert explains the failed refresh.
    expect(el.textContent).toContain("PROVIDER_RESPONSE_INVALID");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  } finally { await act(async () => root.unmount()); el.remove(); }
});
