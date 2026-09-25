// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { ResearchSettings } from "./ResearchSettings";
import { LocaleProvider } from "../../../../shared/i18n";
import { ResearchCurrencyProvider } from "../app/useResearchCurrency";
afterEach(() => { vi.unstubAllGlobals(); localStorage.clear(); document.cookie = "vt_locale_choice=; Max-Age=0; Path=/"; document.body.innerHTML = ""; });
it.each(["ko-KR", "en-US"])("%s: editing the next country sends only a settings CAS command", async locale => {
 document.cookie = `vt_locale_choice=${locale}; Path=/`; localStorage.setItem("vitlane.locale.v2", locale); const writes: { path: string; body: unknown }[] = [];
 vi.stubGlobal("fetch", vi.fn((path, init) => {
  if (init.method === "PATCH") writes.push({ path, body: JSON.parse(init.body) });
  const data = String(path).endsWith("exchange-rate") ? { schemaVersion: "vitlane.exchange-rate.v1", status: "UNAVAILABLE" } : { schemaVersion: "vitlane.research-settings.v1", version: init.method === "PATCH" ? 4 : 3, country: init.method === "PATCH" ? "US" : "KR" };
  return Promise.resolve({ ok: true, status: 200, json: async () => data } as Response);
 }));
 const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
 await act(async () => root.render(<LocaleProvider><ResearchCurrencyProvider curationId="c-1"><ResearchSettings curationId="c-1" /></ResearchCurrencyProvider></LocaleProvider>));
 await act(async () => { const select = container.querySelector("select")!; select.value = "US"; select.dispatchEvent(new Event("change", { bubbles: true })); });
 expect(writes).toEqual([{ path: "/api/v1/curations/c-1/research-settings", body: { schemaVersion: "vitlane.research-settings.v1", expectedVersion: 3, country: "US" } }]);
 expect(container.textContent).toContain(locale === "ko-KR" ? "현재 결과와 가격 조건" : "Current results and price limits");
 await act(async () => root.unmount());
});
