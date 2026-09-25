import { randomUUID } from "../browser/randomUUID";

// Optional behavior analytics only. Business records never depend on this module.
export type Consent = "unknown" | "allowed" | "denied";
export type AnalyticsConfig = { schemaVersion: "vitlane.analytics-config.v1"; mode: "disabled" | "debug" | "ga4"; measurementId: string; release: string };
export type Surface = "app" | "marketing";
export type Source = "SHOPIFY" | "AMAZON" | "COUPANG" | "ELEVENST";
export function analyticsSource(value: string): Source | undefined { return ["SHOPIFY", "AMAZON", "COUPANG", "ELEVENST"].includes(value) ? value as Source : undefined; }
export type Behavior =
  | { name: "page_view"; screen: string }
  | { name: "login" }
  | { name: "research_results_viewed" }
  | { name: "candidate_viewed" | "external_merchant_opened"; source: Source | undefined }
  | { name: "order_sheet_viewed" };
type Gtag = (...args: unknown[]) => void;
declare global { interface Window { dataLayer?: unknown[]; gtag?: Gtag; } }
const cookieName = "vt_analytics";
const noticeCookieName = "vt_analytics_notice";
export const consentNoticeInterval = 24 * 60 * 60 * 1000;
let lastNoticeAt = 0;
export const analyticsChanged = "vitlane:analytics-changed";
export const analyticsSettings = "vitlane:analytics-settings";
let config: AnalyticsConfig | undefined;
let loading: Promise<void> | undefined;
let locale = "ko-KR";
let surface: Surface = "app";
let identity: string | undefined;
let blocked = true; // App authentication must resolve before tracking.
let initialized = false;
let clientID = "";
let sessionID = "";
let consentEpoch = 0;
let lastConsent: Consent = "unknown";
const seen = new Set<string>();
const debug: { name: string; params: Record<string, unknown> }[] = [];
export function consent(): Consent {
  if (typeof document === "undefined") return "unknown";
  const value = document.cookie.split("; ").find(c => c.startsWith(cookieName + "="))?.split("=")[1];
  return value === "v1.allowed" ? "allowed" : value === "v1.denied" ? "denied" : "unknown";
}
function domainAttribute() { return /(^|\.)vitlane\.com$/.test(location.hostname) ? "; Domain=vitlane.com" : ""; }
function notify() { window.dispatchEvent(new Event(analyticsChanged)); }
export function analyticsConfig() { return config; }
export function debugEvents() { return debug.slice(); }
export function openAnalyticsSettings() { window.dispatchEvent(new Event(analyticsSettings)); }
// Preference UI scheduling only; this timestamp never enters analytics payloads.
export function consentNoticeDelay(now = Date.now()) {
  if (consent() === "allowed") return Infinity;
  const value = document.cookie.split("; ").find(c => c.startsWith(noticeCookieName + "="))?.split("=")[1];
  const stored = value?.startsWith("v1.") ? Number(value.slice(3)) : 0;
  const last = Math.max(lastNoticeAt, Number.isSafeInteger(stored) && stored > 0 ? stored : 0);
  return last > 0 && last <= now ? Math.max(0, last + consentNoticeInterval - now) : 0;
}
export function deferConsentNotice() {
  lastNoticeAt = Date.now();
  try {
    document.cookie = noticeCookieName + "=v1." + lastNoticeAt + "; Path=/; Max-Age=15552000; SameSite=Lax" + domainAttribute() + (location.protocol === "https:" ? "; Secure" : "");
    localStorage.setItem("vitlane.analytics.notice", "changed");
    localStorage.removeItem("vitlane.analytics.notice");
  } catch { /* In-memory scheduling remains available if storage is blocked. */ }
}
export function setConsent(value: Exclude<Consent, "unknown">) {
  if (value === "denied") deferConsentNotice();
  else lastNoticeAt = 0;
  try {
    if (value === "allowed") document.cookie = noticeCookieName + "=; Path=/; Max-Age=0; SameSite=Lax" + domainAttribute();
    document.cookie = cookieName + "=v1." + value + "; Path=/; Max-Age=15552000; SameSite=Lax" + domainAttribute() + (location.protocol === "https:" ? "; Secure" : "");
    localStorage.setItem("vitlane.analytics.preference", String(Date.now()));
  } catch { /* Cookies unavailable: fail closed. */ }
  synchronizeConsent();
  notify();
}
function stop() {
  consentEpoch++;
  clientID = ""; sessionID = ""; identity = undefined; seen.clear(); debug.length = 0;
  if (config?.measurementId) (window as unknown as Record<string, unknown>)["ga-disable-" + config.measurementId] = true;
  // Do not send denied-consent pings. Disable collection first; clear identifiers.
  if (initialized) window.gtag?.("set", { user_id: null });
  for (const cookie of document.cookie.split("; ")) {
    const name = cookie.split("=")[0];
    if (!/^_ga(?:_|$)/.test(name)) continue;
    for (const domain of ["", domainAttribute(), "; Domain=" + location.hostname]) {
      document.cookie = name + "=; Path=/; Max-Age=0; SameSite=Lax" + domain;
    }
  }
  try { sessionStorage.removeItem("vitlane.analytics.login"); } catch { /* unavailable */ }
}
function synchronizeConsent() {
  const next = consent();
  if (next !== lastConsent) {
    lastConsent = next;
    if (next !== "allowed") stop();
    notify();
  }
}
export async function startAnalytics(nextSurface: Surface, nextLocale: string) {
  surface = nextSurface; locale = nextLocale === "en-US" ? "en-US" : "ko-KR";
  if (surface === "marketing") blocked = false;
  if (!loading) {
    lastConsent = consent();
    for (const event of ["focus", "storage", "pageshow"]) window.addEventListener(event, synchronizeConsent);
    document.addEventListener("visibilitychange", synchronizeConsent);
    loading = fetch("/api/v1/analytics/config", { credentials: "same-origin", cache: "no-store" })
      .then(async response => {
        if (!response.ok) return;
        const value = await response.json();
        if (value.schemaVersion !== "vitlane.analytics-config.v1" || !["disabled", "debug", "ga4"].includes(value.mode)) return;
        if (value.mode === "ga4" && !/^G-[A-Z0-9]{4,20}$/.test(value.measurementId)) return;
        config = value; notify();
      }).catch(() => { /* Analytics must never make the product unavailable. */ });
  }
  await loading;
}
export function setAnalyticsIdentity(user: { analyticsUserId?: string; marketingAdmin?: boolean; phase5Operator?: boolean } | null) {
  blocked = !!(user?.marketingAdmin || user?.phase5Operator);
  const next = user?.analyticsUserId && /^[a-f0-9]{64}$/.test(user.analyticsUserId) ? user.analyticsUserId : undefined;
  if (identity !== next || blocked) {
    identity = next; seen.clear();
    if (blocked && config?.measurementId) (window as unknown as Record<string, unknown>)["ga-disable-" + config.measurementId] = true;
    // Config parameters outrank global set values: clear/replace the configured
    // identity too, including automatic SDK events after logout/account changes.
    if (initialized && config?.measurementId) window.gtag?.("config", config.measurementId, { ...sanitizedContext(), user_id: blocked ? null : identity ?? null, send_page_view: false });
  }
  if (blocked && config?.measurementId) (window as unknown as Record<string, unknown>)["ga-disable-" + config.measurementId] = true;
  notify();
}
export function hasAnalyticsIdentity() { return !!identity && !blocked; }
export function clearAnalyticsIdentity() { setAnalyticsIdentity(null); }
export function markAnalyticsLogin() {
  if (consent() !== "allowed") return;
  try { sessionStorage.setItem("vitlane.analytics.login", String(Date.now())); } catch { /* unavailable */ }
}
export function consumeAnalyticsLogin() {
  try {
    const value = sessionStorage.getItem("vitlane.analytics.login");
    if (!value || !identity) return;
    sessionStorage.removeItem("vitlane.analytics.login");
    if (Date.now() - Number(value) < 600000) track({ name: "login" });
  } catch { /* unavailable */ }
}
export function screenFor(path: string): string | undefined {
  if (/^\/(admin|dev-auth|design-lab)(\/|$)/.test(path)) return;
  if (surface === "marketing") {
    if (/^\/(ko\/)?(privacy\/|terms\/)?$/.test(path)) return path.includes("privacy") ? "privacy" : path.includes("terms") ? "terms" : "landing";
    return;
  }
  if (path === "/") return "home";
  if (path === "/login") return "login";
  if (path === "/account") return "account";
  if (path === "/plans/new") return "new_curation";
  if (/^\/curations\/[^/]+\/order-sheet$/.test(path)) return "order_sheet";
  if (/^\/curations\/[^/]+$/.test(path)) return "curation";
  if (/^\/agencyOrder(\/[^/]+)?(\/payment)?$/.test(path)) return path.endsWith("/payment") ? "payment" : "orders";
  return;
}
function sanitizedContext() {
  const screen = screenFor(location.pathname);
  let referrer = "";
  try {
    const ref = new URL(document.referrer);
    if (/^(www\.)?(google\.[a-z.]+|bing\.com|naver\.com|search\.naver\.com|duckduckgo\.com|vitlane\.com|app\.vitlane\.com)$/.test(ref.hostname))
      referrer = ref.origin + "/";
  } catch { /* direct entry */ }
  const params = new URLSearchParams(location.search);
  const source = params.get("utm_source") ?? "";
  const medium = params.get("utm_medium") ?? "";
  const campaign = params.get("utm_campaign") ?? "";
  return {
    page_location: "https://" + (surface === "app" ? "app." : "") + "vitlane.com/" + (screen ?? "other"),
    page_title: screen ?? "other", page_referrer: referrer,
    campaign_source: /^(google|naver|bing|newsletter|product_hunt|community)$/.test(source) ? source : undefined,
    campaign_medium: /^(organic|cpc|email|referral|social)$/.test(medium) ? medium : undefined,
    campaign_name: /^(launch|community|newsletter|product_hunt)$/.test(campaign) ? campaign : undefined,
  };
}
function permitted() {
  synchronizeConsent();
  return consent() === "allowed" && config && config.mode !== "disabled" && !blocked && !!screenFor(location.pathname);
}
function ensureTag() {
  if (!permitted() || config?.mode !== "ga4") return;
  (window as unknown as Record<string, unknown>)["ga-disable-" + config.measurementId] = false;
  if (!initialized) {
    initialized = true;
    window.dataLayer = window.dataLayer ?? [];
    window.gtag = function () { window.dataLayer!.push(arguments); };
    window.gtag("consent", "default", { analytics_storage: "granted", ad_storage: "denied", ad_user_data: "denied", ad_personalization: "denied" });
    window.gtag("js", new Date());
    window.gtag("config", config.measurementId, {
      ...sanitizedContext(), send_page_view: false, user_id: identity ?? null,
      allow_google_signals: false, allow_ad_personalization_signals: false,
      cookie_expires: 7776000, cookie_update: false,
    });
    const script = document.createElement("script");
    script.async = true; script.referrerPolicy = "no-referrer";
    script.src = "https://www.googletagmanager.com/gtag/js?id=" + config.measurementId;
    document.head.appendChild(script);
  }
  const epoch = consentEpoch;
  const accept = (kind: "client" | "session", value: unknown) => {
    if (epoch !== consentEpoch || !permitted()) return;
    const text = String(value);
    if (kind === "client" && /^[0-9]{1,20}\.[0-9]{1,20}$/.test(text)) clientID = text;
    if (kind === "session" && /^[0-9]{1,20}$/.test(text)) sessionID = text;
  };
  window.gtag?.("get", config.measurementId, "client_id", (value: unknown) => accept("client", value));
  window.gtag?.("get", config.measurementId, "session_id", (value: unknown) => accept("session", value));
}
export function track(event: Behavior, onceKey?: string): boolean {
  try {
    if (!permitted() || document.visibilityState === "hidden") return false;
    if (event.name !== "page_view" && !identity && config?.mode !== "debug") return false;
    const key = onceKey ? event.name + ":" + onceKey : "";
    if (key && seen.has(key)) return false;
    const eventId = randomUUID();
    const params: Record<string, unknown> = {
      ...sanitizedContext(), schema_version: 1, event_id: eventId, event_key: eventId,
      surface, emitter: "web", ui_locale: locale,
      release: /^[a-zA-Z0-9._-]{1,64}$/.test(config!.release) ? config!.release : "unknown",
    };
    if (event.name === "page_view") params.screen = screenFor(location.pathname);
    if ("source" in event && event.source && ["SHOPIFY", "AMAZON", "COUPANG", "ELEVENST"].includes(event.source)) params.source = event.source;
    if (config?.mode === "debug") {
      debug.push({ name: event.name, params }); if (debug.length > 100) debug.shift();
    } else {
      ensureTag();
      if (event.name === "page_view") window.gtag?.("config", config!.measurementId, { ...sanitizedContext(), user_id: identity ?? null, send_page_view: false });
      window.gtag?.("event", event.name, { ...params, user_id: identity ?? null, send_to: config!.measurementId });
    }
    if (key) { seen.add(key); if (seen.size > 500) seen.delete(seen.values().next().value!); }
    return true;
  } catch { return false; }
}
export function analyticsHeaders(url: string, method?: string): Record<string, string> {
  try {
    if (!permitted() || config?.mode !== "ga4" || !identity || !method || !/^(POST|PUT|PATCH|DELETE)$/i.test(method)) return {};
    if (new URL(url, location.href).origin !== location.origin) return {};
    ensureTag();
    if (!clientID || !sessionID) return {};
    return { "X-Vitlane-Analytics-Client": clientID, "X-Vitlane-Analytics-Session": sessionID, "X-Vitlane-Analytics-Locale": locale };
  } catch { return {}; }
}
