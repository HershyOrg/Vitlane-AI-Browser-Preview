// @vitest-environment jsdom
import { readFileSync } from "node:fs";
import path from "node:path";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { LocaleProvider } from "../../../shared/i18n";
import { BudgetProvider, BudgetTargetContext } from "../app/useBudget";
import { budgetSchema } from "../domain/budget";
import type { LiveCartItem } from "../research/infra/liveCatalogReviewApi";
import { CandidateBudgetDelta, CartBudgetView } from "./BudgetViews";
import { CurationCandidateCard } from "./CurationCandidateCard";

const budgetCSS = readFileSync(
  path.resolve(process.cwd(), "src/products/curation/iface/budget.css"),
  "utf8",
);

vi.mock("../research/app/useResearchCurrency", async importOriginal => ({
  ...await importOriginal<typeof import("../research/app/useResearchCurrency")>(),
  useResearchCurrency: () => ({ currency: "USD" }), useConvertedPrice: () => undefined,
}));
afterEach(() => { vi.unstubAllGlobals(); localStorage.clear(); document.cookie = "vt_locale_choice=; Max-Age=0; Path=/"; document.body.innerHTML = ""; });

it.each(["ko-KR", "en-US"])("%s: unit comparisons and simple cart savings remain views", async locale => {
  document.cookie = `vt_locale_choice=${locale}; Path=/`; localStorage.setItem("vitlane.locale.v2", locale);
  const requests = vi.fn(async (_path: RequestInfo | URL, _init?: RequestInit) => ({ ok: true, status: 200, json: async () => ({ schemaVersion: budgetSchema, version: 2, researchVersion: 1, enabled: true, currency: "USD", totalAmount: "100.00", allocations: [{ targetId: "pen", amount: "100.00", quantity: 2, minimumUnitAmount: "15.00" }] }) })); vi.stubGlobal("fetch", requests);
  const container = document.createElement("div"); document.body.append(container); const root = createRoot(container); const buy = vi.fn();
  await act(async () => root.render(<LocaleProvider><BudgetProvider curationId="c-1" targets={[{ id: "pen", title: "Pen" }]}><BudgetTargetContext.Provider value="pen">
    <CandidateBudgetDelta price={{ kind: "OBSERVED", amountMinor: 1000, currency: "USD" }} />
    <CurationCandidateCard candidate={{ id: "p", source: "SHOPIFY", title: "Pen", price: { kind: "OBSERVED", amountMinor: 6000, currency: "USD" }, features: [], specifications: [], disclosures: [], purchaseRoute: "VITLANE_CHECKOUT" }} primaryAction={{ label: "Buy", onAction: buy }} />
    <CartBudgetView items={[{ previewPriceMinor: 1000, previewCurrency: "USD", quantity: 3 } as LiveCartItem]} />
  </BudgetTargetContext.Provider></BudgetProvider></LocaleProvider>));
  expect(container.querySelector(".budget-delta.is-saving")?.textContent).toContain("−$40.00");
  expect(container.querySelector(".budget-delta.is-saving")?.textContent).toContain(locale === "ko-KR" ? "하한 아래" : "Below minimum");
  expect(container.querySelector(".budget-delta.is-over")?.textContent).toContain("+$10.00");
  expect(container.querySelector(".budget-cart-summary")?.textContent).toContain("$70.00");
  expect(container.querySelector(".budget-cart-summary")?.textContent).toContain(locale === "ko-KR" ? "예산 대비 절약" : "Savings vs budget");
  const button = [...container.querySelectorAll("button")].find(b => b.textContent === "Buy")!; expect(button.disabled).toBe(false); await act(async () => button.click()); expect(buy).toHaveBeenCalledOnce();
  expect(requests).toHaveBeenCalledTimes(1); expect(requests.mock.calls[0]?.[0]).toBe("/api/v1/curations/c-1/budget"); expect(requests.mock.calls[0]?.[1]?.method ?? "GET").toBe("GET"); // no view-triggered mutation
  await act(async () => root.unmount());
});

it("uses the comparison token at regular weight for Candidate savings", () => {
  const savingRule = budgetCSS.match(/\.budget-delta\.is-saving \{[^}]+\}/)?.[0] ?? "";
  // The comparison token keeps still in the neutral accent and flips for dark in theme.css,
  // so the stylesheet needs neither a foundation reference nor a dark override (ADR-0073).
  expect(savingRule).toContain("color: var(--vt-semantic-color-text-comparison)");
  expect(savingRule).toContain("font-weight: var(--vt-foundation-font-weight-regular)");
  expect(savingRule).not.toContain("--vt-semantic-color-text-accent");
  expect(budgetCSS).not.toContain("--vt-foundation-color-");
  expect(budgetCSS).not.toContain("oklch(from");
});

it("lets the composer surface show through the enabled bar with accent amounts and outline while No limit stays quiet", () => {
  const activeRule = budgetCSS.match(/\.curation-budget__bar \{[^}]+\}/)?.[0] ?? "";
  const unlimitedRule = budgetCSS.match(/\.curation-budget__bar\.is-unlimited \{[^}]+\}/)?.[0] ?? "";
  const dividerRule = budgetCSS.match(/\.curation-budget__segment \+ \.curation-budget__segment\.vt-button \{[^}]+\}/)?.[0] ?? "";
  const dockRule = budgetCSS.match(/\.curation-composer-anchor > \.catalog-ui-composer-dock \{[^}]+\}/)?.[0] ?? "";

  expect(activeRule).toContain("background: transparent");
  expect(activeRule).not.toContain("backdrop-filter");
  // The composer is the canvas colour of the theme behind one line, never glass over the conversation (ADR-0086).
  expect(dockRule).toContain("background: var(--vt-semantic-color-surface-canvas)");
  expect(dockRule).toContain("border-top: var(--vt-foundation-border-thin) solid var(--vt-semantic-color-border-default)");
  expect(dockRule).not.toContain("backdrop-filter");
  expect(dockRule).not.toContain("transparent");
  expect(activeRule).toContain("color: var(--vt-semantic-color-text-accent)");
  expect(activeRule).toContain("border: var(--vt-foundation-border-thin) solid var(--vt-semantic-color-text-accent)");
  expect(unlimitedRule).toContain("border-color: var(--vt-semantic-color-border-default)");
  expect(unlimitedRule).toContain("color: var(--vt-semantic-color-text-muted)");
  expect(dividerRule).toContain("var(--vt-semantic-color-text-accent)");
});

it.each([true, false])("allocation bar shows amounts or No limit, with target identity accessible (enabled=%s)", async enabled => {
  document.cookie = "vt_locale_choice=ko-KR; Path=/"; localStorage.setItem("vitlane.locale.v2", "ko-KR");
  const requests = vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ schemaVersion: budgetSchema, version: 1, researchVersion: 1, enabled, currency: "USD", totalAmount: enabled ? "130.60" : null, allocations: [{ targetId: "pen", amount: enabled ? "100.60" : null, quantity: 2 }, { targetId: "ink", amount: enabled ? "30.00" : null, quantity: 1 }] }) }));
  vi.stubGlobal("fetch", requests);
  const { BudgetBar } = await import("./BudgetBar");
  const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
  await act(async () => root.render(<LocaleProvider><BudgetProvider curationId="bar" targets={[{ id: "pen", title: "Pen" }, { id: "ink", title: "Ink" }]}><BudgetBar /></BudgetProvider></LocaleProvider>));
  const segments = [...container.querySelectorAll<HTMLButtonElement>(".curation-budget__segment")];
  const bar = container.querySelector(".curation-budget__bar")!;
  expect(bar.classList.contains("is-unlimited")).toBe(!enabled);
  expect(segments.map(b => b.textContent?.trim())).toEqual(enabled ? ["$100.6", "$30"] : ["제한없음", "제한없음"]);
  expect(container.querySelector(".curation-budget__total-trigger")?.getAttribute("aria-pressed")).toBe(String(enabled));
  expect(container.querySelector(".curation-budget__total-trigger")?.textContent).toContain(enabled ? "$130.6" : "설정 안됨");
  expect(segments[0].getAttribute("aria-label")).toContain("Pen ×2");
  expect(segments.every(b => b.disabled)).toBe(!enabled);
  expect(container.querySelector<HTMLButtonElement>(".curation-budget__total-trigger")?.disabled).toBe(false);
  expect(requests).toHaveBeenCalledTimes(1);
  await act(async () => root.unmount());
});
