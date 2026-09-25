// @vitest-environment jsdom
import { readFileSync } from "node:fs";
import path from "node:path";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { LocaleProvider } from "../../../shared/i18n";
import { CurationComposer } from "./CurationComposer";

const composerCSS = readFileSync(
 path.resolve(process.cwd(), "src/products/curation/iface/catalog-curation-research.css"),
 "utf8",
);
const state=vi.hoisted(()=>({mode:{mode:"MANUAL" as "MANUAL"|"AUTO",version:1},threads:[{steps:[{jobs:[{jobId:"failed-job"}]}]}],changeMode:vi.fn(async()=>{})}));
afterEach(()=>{state.mode.mode="MANUAL";vi.clearAllMocks();localStorage.clear();document.cookie="vt_locale_choice=; Max-Age=0; Path=/";document.body.innerHTML="";});
vi.mock("../app/useThreads",()=>({useCurationThreads:()=>state}));
it("keeps manual retry available for a failed Job already recorded in a thread",async()=>{
 const host=document.createElement("div");document.body.append(host);const root=createRoot(host);const choose=vi.fn();
 try {
  await act(async()=>root.render(<CurationComposer country="KR" focus={{kind:"ADD_TARGET"}} selectFocus={choose} busy={false} modeSelectorOpen setModeSelectorOpen={()=>{}} composerRef={{current:null}} instruction="" setInstruction={()=>{}} submitComposer={()=>{}} retryJobs={[{jobId:"failed-job",title:"Keyboard"}]} />));
  const click=async(text:string)=>{const button=[...document.querySelectorAll("button")].find(b=>b.textContent?.includes(text));expect(button).toBeDefined();await act(async()=>button!.click());};
  await click("실패한 조사 재시도");await click("Keyboard");await click("적용");
  expect(choose).toHaveBeenCalledWith({kind:"RETRY",jobId:"failed-job",title:"Keyboard"});
  expect(state.changeMode).toHaveBeenCalledWith("MANUAL");
 } finally {await act(async()=>root.unmount());host.remove();}
});

it.each([
 ["ko-KR", "요청에 따라 행동, 대상과 예산을 자동으로 결정합니다."],
 ["en-US", "Actions, targets and budgets are determined from your request."],
])("%s Auto selector separates mode copy and its padded action footer",async(locale,description)=>{
 state.mode.mode="AUTO";
 document.cookie=`vt_locale_choice=${locale}; Path=/`;localStorage.setItem("vitlane.locale.v2",locale);
 const host=document.createElement("div");document.body.append(host);const root=createRoot(host);
 try {
  await act(async()=>root.render(<LocaleProvider><CurationComposer country="KR" focus={{kind:"AUTO"}} selectFocus={()=>{}} busy={false} modeSelectorOpen setModeSelectorOpen={()=>{}} composerRef={{current:null}} instruction="" setInstruction={()=>{}} submitComposer={()=>{}} /></LocaleProvider>));
  const selector=document.body.querySelector(".catalog-ui-mode-selector");
  const mode=selector?.querySelector(".catalog-ui-mode-selector__mode");
  const footer=selector?.querySelector(".catalog-ui-mode-selector__footer");
  expect(mode?.querySelector(".curation-mode-switch")).not.toBeNull();
  expect(mode?.querySelector(".catalog-ui-mode-selector__description")?.textContent).toBe(description);
  expect(selector?.querySelector(".catalog-ui-mode-selector__choices")).toBeNull();
  expect(footer?.querySelector(".catalog-ui-mode-selector__apply")).not.toBeNull();
  expect(composerCSS.match(/\.catalog-ui-mode-selector__mode \{[^}]+\}/)?.[0]).toContain("padding: var(--vt-foundation-space-3) var(--vt-foundation-space-4)");
  expect(composerCSS.match(/\.catalog-ui-mode-selector__footer \{[^}]+\}/)?.[0]).toContain("padding: var(--vt-foundation-space-3) var(--vt-foundation-space-4) var(--vt-foundation-space-4)");
 } finally {await act(async()=>root.unmount());host.remove();}
});
