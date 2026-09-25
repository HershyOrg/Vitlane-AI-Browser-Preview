// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from "vitest";

beforeEach(() => {
  vi.resetModules();
  document.cookie = "vt_analytics=; Max-Age=0; Path=/";
  document.cookie = "vt_analytics_notice=; Max-Age=0; Path=/";
  document.head.innerHTML = ""; history.replaceState(null, "", "/");
  localStorage.clear(); sessionStorage.clear();
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
  delete window.gtag; delete window.dataLayer;
  vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => ({ schemaVersion: "vitlane.analytics-config.v1", mode: "ga4", measurementId: "G-TEST1234", release: "test" }) })));
});
it("does not load Google or queue behavior before consent or after refusal", async () => {
  const a = await import("./analytics");
  await a.startAnalytics("app", "ko-KR"); a.setAnalyticsIdentity({ analyticsUserId: "a".repeat(64) });
  expect(a.track({ name: "candidate_viewed", source: "AMAZON" })).toBe(false);
  a.setConsent("denied");
  expect(a.track({ name: "page_view", screen: "home" })).toBe(false);
  expect(document.querySelector('script[src*="google"]')).toBeNull();
  expect(a.analyticsHeaders("/api/v1/curations", "POST")).toEqual({});
});
it("sends only sanitized fields, deduplicates visible events, and stops after withdrawal", async () => {
  const a = await import("./analytics");
  await a.startAnalytics("app", "en-US"); a.setConsent("allowed"); a.setAnalyticsIdentity({ analyticsUserId: "b".repeat(64) });
  history.replaceState(null, "", "/curations/private-id?email=secret@example.com&utm_campaign=secret@example.com");
  document.title = "Private shopping intent"; document.cookie = "_ga=GA1.1.123.456; Path=/";
  expect(a.track({ name: "candidate_viewed", source: "AMAZON" }, "one")).toBe(true);
  expect(a.track({ name: "candidate_viewed", source: "AMAZON" }, "one")).toBe(false);
  const raw = JSON.stringify(window.dataLayer);
  expect(raw).not.toMatch(/private-id|secret@example|Private shopping/);
  const calls = (window.dataLayer ?? []).map(value => Array.from(value as ArrayLike<unknown>));
  const packet = calls.find(call => call[0] === "event" && call[1] === "candidate_viewed")?.[2] as Record<string, unknown>;
  expect(packet.event_key).toMatch(/^[a-f0-9-]{36}$/);
  expect(packet.event_key).toBe(packet.event_id);
  for (const call of calls) if (call[0] === "get") (call[3] as (id: string) => void)(call[2] === "client_id" ? "123.456" : "123456");
  expect(a.analyticsHeaders("/api/v1/curations", "POST")["X-Vitlane-Analytics-Client"]).toBe("123.456");
  expect(a.analyticsHeaders("https://elsewhere.test/api", "POST")).toEqual({});
  a.setConsent("denied");
  const count = window.dataLayer?.length;
  expect(document.cookie).not.toContain("_ga=");
  expect(a.track({ name: "page_view", screen: "home" })).toBe(false);
  expect(window.dataLayer?.length).toBe(count);
  expect(a.analyticsHeaders("/api/v1/curations", "POST")).toEqual({});
});
it("excludes operators and unknown/admin routes", async () => {
  const a = await import("./analytics");
  await a.startAnalytics("app", "ko-KR"); a.setConsent("allowed");
  a.setAnalyticsIdentity({ analyticsUserId: "a".repeat(64), phase5Operator: true });
  expect(a.track({ name: "page_view", screen: "home" })).toBe(false);
  a.setAnalyticsIdentity({ analyticsUserId: "a".repeat(64) }); history.replaceState(null, "", "/admin/orders/private");
  expect(a.track({ name: "page_view", screen: "admin" })).toBe(false);
  expect(document.querySelector("script")).toBeNull();
});
it("debug mode stays local and refused users still call their business API", async () => {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => ({ ok: true, status: 200, json: async () => url.includes("analytics/config") ? { schemaVersion:"vitlane.analytics-config.v1",mode:"debug",release:"local" } : { saved:true } })));
  const a = await import("./analytics"); await a.startAnalytics("app","ko-KR"); a.setAnalyticsIdentity(null); a.setConsent("allowed");
  expect(a.track({ name:"research_results_viewed" })).toBe(true);
  expect(a.debugEvents()).toHaveLength(1); expect(document.querySelector("script")).toBeNull();
  a.setConsent("denied");
  const { request } = await import("../api/client");
  expect(await request("/api/v1/business", { method:"POST",body:"{}" })).toEqual({saved:true});
  expect(vi.mocked(fetch).mock.calls.at(-1)?.[1]?.headers).not.toHaveProperty("X-Vitlane-Analytics-Client");
});

it("clears the configured identity on logout and account switch", async () => {
  const a = await import("./analytics");
  await a.startAnalytics("app", "ko-KR"); a.setConsent("allowed");
  a.setAnalyticsIdentity({ analyticsUserId: "a".repeat(64) });
  a.track({ name: "page_view", screen: "home" });
  a.setAnalyticsIdentity({ analyticsUserId: "b".repeat(64) });
  const configuredUser = () => (window.dataLayer ?? [])
    .map(value => Array.from(value as ArrayLike<unknown>))
    .filter(call => call[0] === "config").at(-1)?.[2] as { user_id: string | null; send_page_view: boolean };
  expect(configuredUser().user_id).toBe("b".repeat(64));
  a.clearAnalyticsIdentity();
  expect(configuredUser()).toMatchObject({ user_id: null, send_page_view: false });
});
