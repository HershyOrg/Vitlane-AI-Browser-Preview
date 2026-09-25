// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AnalyticsConsent } from "./AnalyticsConsent";
import * as analytics from "./analytics";

let host: HTMLDivElement;
let root: Root;
const l = (en: string, ko: string) => ko;
const notice = () => host.querySelector(".analytics-notice");
const click = async (text: string) => {
  const button = [...host.querySelectorAll("button")].find(node => node.textContent === text || node.getAttribute("aria-label") === text);
  expect(button).toBeDefined();
  await act(async () => button!.click());
};
const render = async (translate = l) => {
  await act(async () => root.render(<AnalyticsConsent l={translate} showSettings />));
};

beforeEach(async () => {
  vi.useFakeTimers();
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => ({
    schemaVersion: "vitlane.analytics-config.v1", mode: "debug", release: "test",
  }) })));
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
  history.replaceState(null, "", "/");
  await analytics.startAnalytics("marketing", "ko-KR");
  analytics.setConsent("allowed"); // Also resets in-memory reminder state.
  document.cookie = "vt_analytics=; Max-Age=0; Path=/";
  document.cookie = "vt_analytics_notice=; Max-Age=0; Path=/";
  localStorage.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it("keeps unanswered consent visible across config updates and remounts without taking focus", async () => {
  await render();
  expect(notice()).not.toBeNull();
  expect(document.activeElement).not.toBe(notice());
  await act(async () => window.dispatchEvent(new Event(analytics.analyticsChanged)));
  expect(notice()).not.toBeNull();
  await act(async () => root.unmount());
  document.cookie = "vt_analytics_notice=" + Date.now() + "; Path=/"; // Older builds saved a seen time before any choice.
  root = createRoot(host);
  await render();
  expect(notice()).not.toBeNull();
  expect(analytics.consent()).toBe("unknown");
  await act(async () => { expect(analytics.track({ name: "page_view", screen: "landing" })).toBe(false); });
});

it("shows the notice again only after 24 hours while keeping refusal and tracking disabled", async () => {
  await render();
  await click("동의하지 않기");
  expect(notice()).toBeNull();
  await act(async () => vi.advanceTimersByTime(analytics.consentNoticeInterval - 1));
  expect(notice()).toBeNull();
  await act(async () => vi.advanceTimersByTime(2));
  expect(notice()).not.toBeNull();
  expect(analytics.consent()).toBe("denied");
  await act(async () => { expect(analytics.track({ name: "page_view", screen: "landing" })).toBe(false); });
  expect(document.querySelector('script[src*="google"]')).toBeNull();
  expect(analytics.analyticsHeaders("/api/v1/curations", "POST")).toEqual({});
  await act(async () => analytics.openAnalyticsSettings());
  await click("닫기");
  expect(notice()).toBeNull();
  await click("정보 제공 설정");
  await click("동의하지 않기");
  expect(analytics.consentNoticeDelay()).toBe(analytics.consentNoticeInterval);
});

it("dismisses with X without granting or refusing, persists the delay, and reminds after 24 hours", async () => {
  await render();
  await click("닫기");
  expect(notice()).toBeNull();
  expect(analytics.consent()).toBe("unknown");
  await act(async () => root.unmount());
  root = createRoot(host);
  await render();
  expect(notice()).toBeNull();
  await act(async () => vi.advanceTimersByTime(analytics.consentNoticeInterval + 1));
  expect(notice()).not.toBeNull();
  expect(analytics.consent()).toBe("unknown");
  await act(async () => { expect(analytics.track({ name: "page_view", screen: "landing" })).toBe(false); });
});

it("keeps allowed users free of automatic reminders and lets them reopen and withdraw", async () => {
  await render();
  await click("허용하기");
  await act(async () => vi.advanceTimersByTime(analytics.consentNoticeInterval * 2));
  expect(notice()).toBeNull();
  expect(analytics.consentNoticeDelay()).toBe(Infinity);
  await act(async () => analytics.openAnalyticsSettings());
  expect(document.activeElement).toBe(notice());
  await click("동의하지 않기");
  expect(analytics.consent()).toBe("denied");
  expect(notice()).toBeNull();
});

it("defers an overdue notice in a hidden tab until it becomes visible", async () => {
  await render();
  await click("동의하지 않기");
  Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
  await act(async () => {
    document.dispatchEvent(new Event("visibilitychange"));
    vi.advanceTimersByTime(analytics.consentNoticeInterval + 1);
  });
  expect(notice()).toBeNull();
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
  await act(async () => document.dispatchEvent(new Event("visibilitychange")));
  expect(notice()).not.toBeNull();
  expect(analytics.consent()).toBe("denied");
});

it.each(["ko", "en"])("uses information consent wording and the primary allow button in %s", async locale => {
  await render((en, ko) => locale === "ko" ? ko : en);
  expect(notice()?.getAttribute("aria-label")).toBe(locale === "ko" ? "이용 정보 제공 동의" : "Usage information consent");
  expect(host.querySelector(".vt-button--primary")?.textContent).toBe(locale === "ko" ? "허용하기" : "Allow");
  expect(host.querySelector(".vt-button--secondary")?.textContent).toBe(locale === "ko" ? "동의하지 않기" : "Decline");
  expect(host.textContent).not.toMatch(/추가 분석|Additional analytics/);
  await click(locale === "ko" ? "동의하지 않기" : "Decline");
  await click(locale === "ko" ? "정보 제공 설정" : "Usage information preferences");
  expect(notice()).not.toBeNull();
});
