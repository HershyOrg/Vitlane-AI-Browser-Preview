// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { LocaleProvider, useLocale } from "../../../shared/i18n";
import { PreferencesProvider, usePreferences } from "./usePreferences";
import { PlanCreator } from "../../curation/planning/iface/PlanCreator";
import { initialPlanForm } from "../../curation/planning/domain/form";
const identity = vi.hoisted(() => ({ user: { id: "alice" } }));
vi.mock("./useCurrentUser", () => ({ useCurrentUser: () => identity }));
vi.mock("../../curation/planning/app/useManagedRunner", () => ({ useManagedRunner: () => ({ capability: null }) }));
let root: Root, container: HTMLDivElement;
const result = (version: number, fields: Record<string, string> = {}) => ({ preferences: { schemaVersion: "vitlane.user-preferences.v1", version, ...fields }, effective: { uiLocale: "ko-KR", preferredCurrency: "KRW", researchCountry: "KR", ...fields } });
const response = (body: unknown, status = 200) => Promise.resolve({ ok: status < 400, status, json: async () => body } as Response);
function Probe() { const p = usePreferences(); const locale = useLocale(); return <><output>{JSON.stringify({ ...p.values, ready: p.ready, scope: p.storageScope, locale: locale.locale })}</output><button id="usd" onClick={() => void p.save({ preferredCurrency: "USD" })}>USD</button><button id="english" onClick={() => locale.setLocale("en-US")}>English</button></>; }
const app = (child = <Probe />) => <LocaleProvider><PreferencesProvider>{child}</PreferencesProvider></LocaleProvider>;
beforeEach(() => { identity.user = { id: "alice" }; localStorage.clear(); document.cookie = "vt_locale_choice=; Max-Age=0; Path=/"; container = document.createElement("div"); document.body.append(container); root = createRoot(container); });
afterEach(async () => { await act(async () => root.unmount()); container.remove(); vi.unstubAllGlobals(); });
it("first read uses KR defaults without persisting a selection; CAS retries merge only the selected field", async () => {
 const bodies: Record<string, unknown>[] = []; let gets = 0;
 vi.stubGlobal("fetch", vi.fn((_url, init) => { if (init.method === "PATCH") { bodies.push(JSON.parse(init.body)); return bodies.length === 1 ? response({ error: { code: "CONFLICT" } }, 409) : response(result(2, { uiLocale: "en-US", preferredCurrency: "USD", researchCountry: "US" })); } gets++; return response(gets === 1 ? result(0) : result(1, { uiLocale: "en-US", researchCountry: "US" })); }));
 await act(async () => root.render(app()));
 expect(JSON.parse(container.querySelector("output")!.textContent!)).toMatchObject({ uiLocale: "ko-KR", preferredCurrency: "KRW", researchCountry: "KR", ready: true });
 expect(bodies).toHaveLength(0); expect(localStorage.getItem("vitlane.locale.v2")).toBeNull();
 await act(async () => container.querySelector<HTMLButtonElement>("#usd")!.click());
 expect(bodies).toEqual([0, 1].map(expectedVersion => ({ schemaVersion: "vitlane.user-preferences.v1", preferredCurrency: "USD", expectedVersion })));
 expect(container.querySelector("output")!.textContent).toContain('"researchCountry":"US"');
});
it("a late response for account A cannot replace account B defaults", async () => {
 let release!: (response: Response) => void;
 vi.stubGlobal("fetch", vi.fn(() => identity.user.id === "alice" ? new Promise<Response>(resolve => { release = resolve; }) : response(result(0))));
 await act(async () => root.render(app()));
 identity.user = { id: "bob" }; await act(async () => root.render(app()));
 await act(async () => release(await response(result(9, { uiLocale: "en-US", preferredCurrency: "USD", researchCountry: "US" }))));
 expect(JSON.parse(container.querySelector("output")!.textContent!)).toMatchObject({ scope: "bob", preferredCurrency: "KRW", researchCountry: "KR", locale: "ko-KR" });
});
it("a failed preference read keeps the app mounted in a non-layout alert and retry clears it", async () => {
 let calls = 0;
 vi.stubGlobal("fetch", vi.fn(() => ++calls === 1
   ? response({ error: { code: "NOT_FOUND" } }, 404)
   : response(result(2))));
 await act(async () => { root.render(app()); await Promise.resolve(); });
 const alert = container.querySelector<HTMLElement>('[role="alert"]');
 expect(alert?.className).toBe("shell-preferences-alert");
 expect(container.querySelector("output")).not.toBeNull();
 await act(async () => { alert?.querySelector<HTMLButtonElement>("button")?.click(); await Promise.resolve(); });
 expect(container.querySelector('[role="alert"]')).toBeNull();
 expect(container.querySelector("output")?.textContent).toContain('"ready":true');
});
it("legacy budget caches do not override manual budget currency or persist across account switches", async () => {
 localStorage.setItem("vitlane.plan-preferences.v1:alice", JSON.stringify({ minPrice: "25", maxPrice: "80", currency: "USD", modelKey: "gpt-5.6-luna" }));
 const fetch = vi.fn((_url?: unknown, _init?: RequestInit) => response(result(3, { preferredCurrency: "KRW", researchCountry: "KR" }))); vi.stubGlobal("fetch", fetch);
 const view = () => app(<PlanCreator initialForm={initialPlanForm} working={false} onSubmit={vi.fn()} />);
 const open = async () => { await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="예산 설정"]')!.click()); };
 const manual = async () => { const toggle=document.querySelector<HTMLButtonElement>('[role="switch"][aria-label="자동 큐레이션"]')!; expect(toggle.getAttribute("aria-checked")).toBe("true"); await act(async()=>toggle.click()); };
 await act(async () => root.render(view())); await open(); await manual();
 expect(document.querySelector<HTMLInputElement>("#curation-min-price")).toBeNull();
 expect(document.querySelector<HTMLSelectElement>("#curation-currency")!.value).toBe("KRW");
 expect(document.querySelector<HTMLInputElement>('[aria-label="총 예산"]')!.value).toBe("");
 await act(async () => { const currency = document.querySelector<HTMLSelectElement>("#curation-currency")!; currency.value = "USD"; currency.dispatchEvent(new Event("change", { bubbles: true })); });
 expect(fetch).toHaveBeenCalledTimes(1);
 identity.user = { id: "bob" }; await act(async () => root.render(view()));
 expect(document.querySelector('[role="dialog"]')).toBeNull(); await open(); await manual();
 expect(document.querySelector<HTMLSelectElement>("#curation-currency")!.value).toBe("KRW");
 expect(fetch.mock.calls.every(([,init]) => !init || (init as RequestInit).method !== "PATCH")).toBe(true);
});
it("a guest follows the browser language and an unselected account does not become a choice", async () => {
 Object.defineProperty(window.navigator, "languages", { configurable: true, get: () => ["en-US", "en"] });
 identity.user = undefined as unknown as { id: string };
 vi.stubGlobal("fetch", vi.fn(() => response(result(0, {}))));
 await act(async () => root.render(app()));
 expect(JSON.parse(container.querySelector("output")!.textContent!)).toMatchObject({ uiLocale: "en-US", preferredCurrency: "USD", researchCountry: "US", locale: "en-US", ready: true });
 expect(document.cookie).toContain("vt_locale_seen=en-US");
 expect(document.cookie).not.toContain("vt_locale_choice=");
 identity.user = { id: "alice" };
 vi.stubGlobal("fetch", vi.fn(() => response({ preferences: { schemaVersion: "vitlane.user-preferences.v1", version: 0 }, effective: { schemaVersion: "vitlane.user-preferences.v1", version: 0, uiLocale: "en-US", preferredCurrency: "USD", researchCountry: "US" } })));
 await act(async () => root.render(app()));
 expect(JSON.parse(container.querySelector("output")!.textContent!)).toMatchObject({ scope: "alice", locale: "en-US", preferredCurrency: "USD" });
 expect(document.cookie).not.toContain("vt_locale_choice=");
});
it("an account language selection becomes this browser's explicit choice", async () => {
 vi.stubGlobal("fetch", vi.fn(() => response(result(4, { uiLocale: "en-US" }))));
 await act(async () => root.render(app()));
 expect(JSON.parse(container.querySelector("output")!.textContent!)).toMatchObject({ locale: "en-US" });
 expect(document.cookie).toContain("vt_locale_choice=en-US");
 expect(document.cookie).toContain("vt_locale_seen=en-US");
});
