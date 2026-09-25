// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initialPlanForm } from "../domain/form";
import { PlanCreator } from "./PlanCreator";
vi.mock("../app/useManagedRunner", () => ({ useManagedRunner: () => ({ capability: null }) }));
const { savePreferences } = vi.hoisted(() => ({ savePreferences: vi.fn() }));
vi.mock("../../../account/app/usePreferences", () => ({ usePreferences: () => ({ values: { researchCountry: "KR", preferredCurrency: "KRW" }, ready: true, save: savePreferences, refresh: async () => {} }) }));
beforeEach(() => { savePreferences.mockReset().mockResolvedValue(undefined); });
let root: Root;
afterEach(async () => { await act(async () => root?.unmount()); await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)); }); document.body.innerHTML = ""; localStorage.clear(); });
async function render() {
 const container = document.createElement("div"); document.body.append(container); root = createRoot(container);
 const onSubmit = vi.fn().mockResolvedValue(undefined);
 await act(async () => root.render(<PlanCreator initialForm={initialPlanForm} working={false} onSubmit={onSubmit} />));
 return { container, onSubmit };
}
async function click(label: string) { await act(async () => { const button = [...document.querySelectorAll<HTMLButtonElement>('button')].find(b => b.getAttribute('aria-label') === label || b.textContent?.trim() === label); expect(button, label).toBeDefined(); button!.click(); }); }
async function set(selector: string, value: string) { await act(async () => { const el = document.querySelector<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>(selector)!; expect(el, selector).not.toBeNull(); const proto = el.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : el.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype; Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(el,value); el.dispatchEvent(new Event(el.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true })); }); }
async function submit() { await set('#curation-intent', '10만원 안으로 만년필'); await click('상품 찾기 시작'); }
async function close() { await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))); }
describe('Init automatic settings', () => {
 it('starts Auto with no inherited budget; opening and closing does not select an explicit budget', async () => {
  localStorage.setItem('vitlane.plan-preferences.v1', JSON.stringify({ planningMode:'SINGLE', amount:'100', currency:'USD' }));
  const { container, onSubmit } = await render();
  expect(container.querySelector('h1')?.textContent).toBe('무엇을 찾고 있나요?');
  await click('예산 설정');
  expect(document.querySelector('[role="switch"]')?.getAttribute('aria-checked')).toBe('true');
  expect(document.body.textContent).toContain('예산이 자동으로 결정됩니다.');
  expect(document.querySelector('#curation-currency')).toBeNull();
  expect([...document.querySelectorAll('button')].some(b => ['저장','취소'].includes(b.textContent ?? ''))).toBe(false);
  await close(); await submit();
  expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ planningMode:'AUTO', budgetExplicit:false, amount:'', currency:'KRW' }));
  expect(savePreferences).not.toHaveBeenCalled();
 });
 it('applies country and budget immediately and preserves them across closing and reopening', async () => {
  const { onSubmit } = await render();
  await click('조사·보기 설정'); await set('#shipping-country','US'); await close();
  await click('예산 설정'); if (!document.querySelector('[aria-label="총 예산"]')) await click('자동 큐레이션'); await set('[aria-label="총 예산"]','200000'); await close();
  await click('예산 설정');
  expect(document.querySelector<HTMLInputElement>('[aria-label="총 예산"]')?.value).toBe('200000');
  await close(); await submit();
  expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ country:'US', amount:'200000', currency:'KRW', budgetExplicit:true, budgetAllocationMode:'EQUAL', controlMode:'MANUAL' }));
  expect(savePreferences).toHaveBeenCalledExactlyOnceWith({ researchCountry:"US" });
 });
 it('applies No limit immediately and keeps an explicit budget currency when display currency changes', async () => {
  const { onSubmit } = await render();
  await click('예산 설정'); if (!document.querySelector('[aria-label="총 예산"]')) await click('자동 큐레이션'); await set('[aria-label="총 예산"]','200000'); await click('제한 없음'); await close();
  await click('조사·보기 설정'); await set('[aria-label="표시 통화"]','USD'); await close(); await submit();
  expect(savePreferences).toHaveBeenCalledExactlyOnceWith({ preferredCurrency:'USD' });
  expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ amount:'', currency:'KRW', budgetExplicit:true, displayCurrency:'USD' }));
 });
 it.each(['-1','0','1.123','abc','100.'])('blocks invalid budget %s even after closing, then accepts a valid correction', async amount => {
  const { container, onSubmit } = await render();
  await set('#curation-intent','만년필'); await click('예산 설정'); if (!document.querySelector('[aria-label="총 예산"]')) await click('자동 큐레이션'); await set('#curation-currency','USD'); await set('[aria-label="총 예산"]',amount); await close();
  expect(container.querySelector('[role="alert"]')).not.toBeNull();
  await act(async () => container.querySelector('form')!.requestSubmit());
  expect(onSubmit).not.toHaveBeenCalled();
  await click('예산 설정'); if (!document.querySelector('[aria-label="총 예산"]')) await click('자동 큐레이션'); await set('[aria-label="총 예산"]','100.01'); await close(); await click('상품 찾기 시작');
  expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ amount:'100.01', currency:'USD', budgetExplicit:true }));
 });
 it('clears the amount immediately when changing budget denomination', async () => {
  const { onSubmit } = await render(); await click('예산 설정'); if (!document.querySelector('[aria-label="총 예산"]')) await click('자동 큐레이션'); await set('[aria-label="총 예산"]','200000'); await set('#curation-currency','USD');
  expect(document.querySelector<HTMLInputElement>('[aria-label="총 예산"]')?.value).toBe('');
  await close(); await submit();
  expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ amount:'', currency:'USD', budgetExplicit:true }));
 });
 it.each([[true,'country'],[false,'country'],[true,'currency'],[false,'currency']] as const)('automatically saves account settings through failure and retry (success=%s, field=%s)', async (success, field) => {
  let finish!: () => void;
  savePreferences.mockImplementationOnce(() => new Promise<void>((resolve,reject) => { finish = () => success ? resolve() : reject(new Error('failed')); }));
  const { container,onSubmit } = await render(); await set('#curation-intent','머그컵'); await click('조사·보기 설정'); await set(field === 'country' ? '#shipping-country' : '[aria-label="표시 통화"]', field === 'country' ? 'US' : 'USD');
  expect(savePreferences).toHaveBeenCalledExactlyOnceWith(field === 'country' ? { researchCountry:'US' } : { preferredCurrency:'USD' });
  await close();
  const input = container.querySelector('textarea')!;
  expect(input.disabled).toBe(true);
  await act(async () => container.querySelector('form')!.requestSubmit()); expect(onSubmit).not.toHaveBeenCalled();
  await act(async () => finish());
  expect(input.disabled).toBe(false); expect(input.value).toBe('머그컵');
  if (!success) {
   expect(container.querySelector('[role="alert"]')).not.toBeNull();
   await act(async () => container.querySelector('form')!.requestSubmit()); expect(onSubmit).not.toHaveBeenCalled();
   await click('다시 시도'); expect(savePreferences).toHaveBeenCalledTimes(2);
  }
  await click('상품 찾기 시작');
  expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ originalIntent:'머그컵', ...(field === 'country' ? {country:'US',currency:'KRW'} : {currency:'USD',displayCurrency:'USD'}) }));
 });
 it('retains a failed country choice when the user changes display currency before retrying', async () => {
  savePreferences.mockRejectedValueOnce(new Error('failed'));
  const {onSubmit}=await render();await click('조사·보기 설정');await set('#shipping-country','US');
  await set('[aria-label="표시 통화"]','USD');
  expect(savePreferences).toHaveBeenNthCalledWith(2,{researchCountry:'US',preferredCurrency:'USD'});
  await close();await submit();
  expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({country:'US',displayCurrency:'USD'}));
 });
 it('submits with Enter, preserves Shift+Enter/IME and does not steal composer focus on close', async () => {
  const { container,onSubmit } = await render(); await click('예산 설정');
  const input=container.querySelector('textarea')!;
  await act(async () => { document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true})); input.focus(); });
  await act(async () => { await new Promise(resolve=>setTimeout(resolve,20)); });
  expect(document.activeElement).toBe(input);
  await set('#curation-intent','만년필');
  for (const options of [{shiftKey:true},{isComposing:true},{keyCode:229}]) {
   await act(async () => input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true,cancelable:true,...options})));
   expect(onSubmit).not.toHaveBeenCalled();
  }
  await act(async () => input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true,cancelable:true})));
  expect(onSubmit).toHaveBeenCalledOnce();
 });
});
