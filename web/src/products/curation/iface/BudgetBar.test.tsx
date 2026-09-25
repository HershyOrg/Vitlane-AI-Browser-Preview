// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { LocaleProvider } from "../../../shared/i18n";
import { BudgetBar } from "./BudgetBar";
import { budgetSchema } from "../domain/budget";

const { save } = vi.hoisted(() => ({ save: vi.fn() }));
vi.mock("../app/useBudget", () => ({ useBudget: () => ({
  curationId: "test", targets: [{ id: "pen", title: "Pen" }], save,
  ledger: { schemaVersion: budgetSchema, version: 7, researchVersion: 4, enabled: true, currency: "KRW", totalAmount: "20000", allocations: [{ targetId: "pen", quantity: 2, amount: "20000" }] },
}) }));
vi.mock("../research/app/useResearchCurrency", () => ({ useResearchCurrency: () => ({ currency: "KRW" }) }));
let root: Root;
afterEach(async () => { await act(async () => root?.unmount()); document.body.innerHTML = ""; document.cookie = "vt_locale_choice=; Max-Age=0; Path=/"; localStorage.clear(); vi.resetAllMocks(); });
async function render(locale: string) {
  document.cookie = `vt_locale_choice=${locale}; Path=/`; localStorage.setItem("vitlane.locale.v2", locale);
  const container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  await act(async () => root.render(<LocaleProvider><BudgetBar /></LocaleProvider>));
}
async function click(label: string) {
  const button = [...document.querySelectorAll<HTMLButtonElement>("button")].find(b => b.getAttribute("aria-label") === label || b.textContent?.trim() === label);
  expect(button, label).toBeDefined(); await act(async () => button!.click());
  // Let Radix restore focus before the next interaction opens another scope.
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)); });
}
it.each(["ko-KR", "en-US"])("%s: enabled total only confirms disabling; cancellation does not save", async locale => {
  const ko = locale === "ko-KR"; await render(locale);
  await click(ko ? "예산 설정" : "Budget settings");
  expect(document.querySelector(".budget-popover")?.textContent).toContain(ko ? "예산 설정을 해제하시겠습니까?" : "Turn off budget settings?");
  expect(document.querySelector(".budget-popover input")).toBeNull();
  await click(ko ? "취소" : "Cancel"); expect(save).not.toHaveBeenCalled();
  await click(ko ? "예산 설정" : "Budget settings");
  await click(ko ? "확인" : "Confirm");
  expect(save).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ kind: "DISABLE", expectedVersion: 7, schemaVersion: budgetSchema }));
  expect(document.querySelector(".budget-popover")).toBeNull();
});
it("retries an unconfirmed disable with the same command and locks dismissal", async () => {
  save.mockRejectedValueOnce(new TypeError("Network unavailable")).mockResolvedValueOnce(undefined);
  await render("ko-KR"); await click("예산 설정"); await click("확인");
  const command = save.mock.calls[0][0];
  expect(document.querySelector('[role="alert"]')?.textContent).toContain("저장 결과를 확인하지 못했습니다");
  expect([...document.querySelectorAll<HTMLButtonElement>("button")].find(b => b.textContent === "취소")?.disabled).toBe(true);
  await act(async () => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  expect(document.querySelector(".budget-popover")).not.toBeNull();
  await click("저장 다시 확인"); expect(save).toHaveBeenCalledTimes(2); expect(save.mock.calls[1][0]).toEqual(command);
  expect(document.querySelector(".budget-popover")).toBeNull();
});
