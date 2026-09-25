// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { CurationThreadProgress, CurationThreadReport } from "./CurationThread";
import { useCurationThreads } from "../app/useThreads";
import { retryIntelligenceJob } from "../infra/curationApi";
import type { CurationThread, CurationActionExecution } from "../domain/thread";
vi.mock("../app/useThreads",()=>({useCurationThreads:vi.fn()}));
vi.mock("../infra/curationApi",()=>({retryIntelligenceJob:vi.fn()}));
let locale="ko-KR";
const l=(en:string,ko:string,values?:Record<string,string|number>)=>Object.entries(values??{}).reduce((s,[k,v])=>s.replaceAll(`{${k}}`,String(v)),locale==="ko-KR"?ko:en);
vi.mock("../../../shared/i18n",()=>({useLocale:()=>({locale,l}),localizeFixedCopy:(en:string,ko:string)=>ko,invariantContent:(v:string)=>v}));
let root:Root;
// The Provider's sheet memory, shared by every mounted bar like the real one.
const sheets=new Map<string,boolean>();const sheet={sheetOpen:(id:string)=>sheets.get(id),rememberSheet:(id:string,open:boolean)=>{sheets.set(id,open)}};
afterEach(async()=>{await act(async()=>root?.unmount());document.body.innerHTML="";sheets.clear();vi.clearAllMocks()});
const action:CurationActionExecution={id:"auto",threadId:"request",sequence:0,type:"AUTO_START",status:"WAITING_SELECTION",jobs:[],effects:[],decisions:[{id:"d1",kind:"TARGET",result:"NEEDS_SELECTION",source:"MANAGED",reasonCode:"USER_SELECTION_REQUIRED",actionIds:[]}],decisionIds:[],answers:[],question:{id:"q1",prompt:"Which product?",options:[{id:"keyboard",label:"Keyboard"},{id:"headphones",label:"Headphones"}]}};
function thread():CurationThread{return {schemaVersion:"vitlane.curation-thread.v2",id:"request",curationId:"curation",mode:"AUTO",origin:"REQUEST",request:"Try again",revision:2,status:"WAITING_SELECTION",actions:[{...action}],targetLabels:{},createdAt:"2026-09-14T00:00:00Z",updatedAt:"2026-09-14T00:00:00Z"}}
async function mount(node:React.ReactNode){const el=document.createElement("div");document.body.append(el);root=createRoot(el);await act(async()=>root.render(node))}
function button(text:string){return [...document.querySelectorAll("button")].find(b=>b.textContent?.trim()===text)}
async function click(text:string){await act(async()=>{const b=button(text);expect(b,text).toBeDefined();b!.click()})}
it.each(["ko-KR","en-US"])("keeps the selection in the bar sheet, answers the same Action and stops the request with one control (%s)",async language=>{
 locale=language;const t=thread();const answer=vi.fn().mockResolvedValue(undefined),cancel=vi.fn().mockResolvedValue(undefined),cancelAction=vi.fn();
 vi.mocked(useCurationThreads).mockReturnValue({...sheet,threads:[t],active:t,answer,cancel,cancelAction,sending:false,busy:true} as unknown as ReturnType<typeof useCurationThreads>);
 await mount(<CurationThreadProgress/>);
 const bar=document.querySelector(".curation-thread__toggle")!;expect(bar.getAttribute("aria-expanded")).toBe("true");expect(bar.textContent).toContain(locale==="ko-KR"?"선택이 필요해요":"Your choice is needed");
 expect(document.querySelector('[role="group"][aria-label="Which product?"]')).not.toBeNull();
 await click("Keyboard");expect(answer).toHaveBeenCalledWith(t,"keyboard");
 await click(locale==="ko-KR"?"중단":"Stop");expect(cancel).toHaveBeenCalledWith(t);expect(cancelAction).not.toHaveBeenCalled();
 expect(button(locale==="ko-KR"?"이 행동 중단":"Stop this action")).toBeUndefined();
 expect(document.querySelectorAll("[data-action-id]")).toHaveLength(1);
 await click(locale==="ko-KR"?"직접 설명하기":"Describe it yourself");expect(document.querySelector("input")).not.toBeNull();
 t.actions[0]={...action,question:{...action.question!,id:"q2"}};
 await act(async()=>root.render(<CurationThreadProgress/>));expect(document.querySelector("input")).toBeNull();
 await act(async()=>{(bar as HTMLButtonElement).click()});expect(bar.getAttribute("aria-expanded")).toBe("false");expect(document.querySelector(".curation-thread__sheet")).toBeNull();
});
it("shows running research as one line with per-target progress",async()=>{
 locale="ko-KR";const t=thread();t.status="RUNNING";t.targetLabels={pen:"만년필",ink:"잉크"};
 t.actions=[{...action,status:"SUCCEEDED",question:undefined},{...action,id:"plan",type:"INTENT_NEXT_STEP",sequence:1,status:"SUCCEEDED",question:undefined,decisions:[]},{...action,id:"research",type:"START_RESEARCH",sequence:2,status:"RUNNING",question:undefined,decisions:[],jobs:[{jobId:"j1",actionId:"research",kind:"RESEARCH_ROUND",targetId:"pen",status:"SUCCEEDED",effects:[{kind:"CANDIDATES_ADDED",targetId:"pen",count:6}]},{jobId:"j2",actionId:"research",kind:"RESEARCH_ROUND",targetId:"ink",status:"RUNNING",effects:[]}]}];
 vi.mocked(useCurationThreads).mockReturnValue({...sheet,threads:[t],active:t,cancel:vi.fn(),sending:false,busy:true} as unknown as ReturnType<typeof useCurationThreads>);
 await mount(<CurationThreadProgress/>);
 const bar=document.querySelector(".curation-thread__toggle")!;expect(bar.getAttribute("aria-expanded")).toBe("false");
 expect(bar.querySelector('[role="status"]')?.textContent).toBe("상품 조사 중 · 2/3");expect(bar.querySelector(".curation-thread__detail")?.textContent).toBe("만년필 완료 · 잉크 진행 중");
 expect(document.querySelectorAll(".curation-thread .is-vitlane")).toHaveLength(0);
});
it("pulses one dot while loading: the folded bar, or only the active step once the sheet is open",async()=>{
 locale="ko-KR";const t=thread();t.status="INTERPRETING";t.actions=[{...action,status:"PENDING",question:undefined,decisions:[]}];
 vi.mocked(useCurationThreads).mockReturnValue({...sheet,threads:[t],active:t,cancel:vi.fn(),sending:false,busy:true} as unknown as ReturnType<typeof useCurationThreads>);
 await mount(<CurationThreadProgress/>);
 const header=()=>document.querySelector('.curation-thread__toggle [role="status"]')!;
 const pulsing=()=>[...document.querySelectorAll(".curation-thread .is-loading")].map(e=>e.closest("[data-action-id]")?.getAttribute("data-action-id")??"bar");
 const toggle=()=>act(async()=>{(document.querySelector(".curation-thread__toggle") as HTMLButtonElement).click()});
 expect(header().textContent).toBe("요청 해석 중");expect(pulsing()).toEqual(["bar"]);
 await toggle();expect(pulsing()).toEqual(["auto"]);
 await toggle();expect(pulsing()).toEqual(["bar"]);
 t.status="RUNNING";t.actions=[{...action,status:"SUCCEEDED",question:undefined},{...action,id:"plan",type:"INTENT_NEXT_STEP",sequence:1,status:"SUCCEEDED",question:undefined,decisions:[]},{...action,id:"research",type:"START_RESEARCH",sequence:2,status:"RUNNING",question:undefined,decisions:[]}];
 await act(async()=>root.render(<CurationThreadProgress/>));
 expect(header().textContent).toBe("상품 조사 중 · 2/3");expect(pulsing()).toEqual(["bar"]);
 await toggle();expect(pulsing()).toEqual(["research"]);
 t.status="WAITING_SELECTION";t.actions=[{...action}];await act(async()=>root.render(<CurationThreadProgress/>));
 expect(header().textContent).toBe("선택이 필요해요");expect(pulsing()).toEqual([]);
});
it("keeps an open sheet open when research starts and the Curating screen mounts the bar again",async()=>{
 locale="ko-KR";const t=thread();t.status="RUNNING";t.actions=[{...action,status:"SUCCEEDED",question:undefined},{...action,id:"plan",type:"INTENT_NEXT_STEP",sequence:1,status:"RUNNING",question:undefined,decisions:[]}];
 const controller=(active:CurationThread)=>({...sheet,threads:[active],active,cancel:vi.fn(),sending:false,busy:true}) as unknown as ReturnType<typeof useCurationThreads>;
 vi.mocked(useCurationThreads).mockReturnValue(controller(t));
 await mount(<CurationThreadProgress/>);
 const expanded=()=>document.querySelector(".curation-thread__toggle")!.getAttribute("aria-expanded");
 expect(expanded()).toBe("false");
 await act(async()=>{(document.querySelector(".curation-thread__toggle") as HTMLButtonElement).click()});expect(expanded()).toBe("true");
 // Planning's bar unmounts and Curating mounts its own.
 await act(async()=>root.unmount());document.body.innerHTML="";
 t.actions=[t.actions[0],{...t.actions[1],status:"SUCCEEDED"},{...action,id:"research",type:"START_RESEARCH",sequence:2,status:"RUNNING",question:undefined,decisions:[]}];
 await mount(<CurationThreadProgress/>);
 expect(expanded()).toBe("true");expect([...document.querySelectorAll(".curation-thread .is-loading")].map(e=>e.closest("[data-action-id]")?.getAttribute("data-action-id")??"bar")).toEqual(["research"]);
 // A new request still starts folded.
 await act(async()=>root.unmount());document.body.innerHTML="";
 vi.mocked(useCurationThreads).mockReturnValue(controller({...t,id:"next"}));
 await mount(<CurationThreadProgress/>);expect(expanded()).toBe("false");
});
it("reports a completed request as one Vitlane reply in the body with candidates and a record behind a link",async()=>{
 locale="ko-KR";const t=thread();t.status="SUCCEEDED";t.targetLabels={pen:"만년필",ink:"잉크"};
 t.actions=[{...action,status:"SUCCEEDED",question:undefined},{...action,id:"research",type:"START_RESEARCH",sequence:1,status:"SUCCEEDED",question:undefined,decisions:[],jobs:[{jobId:"j1",actionId:"research",kind:"RESEARCH_ROUND",targetId:"pen",status:"SUCCEEDED",effects:[{kind:"CANDIDATES_ADDED",targetId:"pen",count:6}]},{jobId:"j2",actionId:"research",kind:"RESEARCH_ROUND",targetId:"ink",status:"SUCCEEDED",effects:[{kind:"NO_RESULTS",targetId:"ink"}]}]}];
 vi.mocked(useCurationThreads).mockReturnValue({...sheet,threads:[t],active:undefined,sending:false,busy:false,reload:vi.fn()} as unknown as ReturnType<typeof useCurationThreads>);
 await mount(<CurationThreadReport thread={t}/>);
 // The request stays a bubble; Vitlane's reply is body text (ADR-0086), not a bubble.
 expect(document.querySelectorAll(".is-user")).toHaveLength(1);expect(document.querySelectorAll(".is-vitlane")).toHaveLength(0);expect(document.querySelectorAll("article.curation-response")).toHaveLength(1);
 const report=document.querySelector(".curation-thread-report")!;expect(report.getAttribute("data-thread-outcome")).toBe("SUCCEEDED");
 expect(report.textContent).toContain("만년필 후보 6개를 찾았어요.");expect(report.textContent).toContain("조건에 맞는 상품이 없었어요: 잉크");
 expect(button("다시 시도")).toBeUndefined();expect(document.querySelector("[data-action-id]")).toBeNull();
 await click("처리 내역");expect(document.querySelectorAll("[data-action-id]")).toHaveLength(2);expect(document.body.textContent).toContain("AI 판단");
});
it("reports a partial failure as 일부 완료 with the succeeded receipt, the failed reason and a retry",async()=>{
 locale="ko-KR";const t=thread();t.status="FAILED";t.reasonCode="PARTIAL_FAILURE";t.targetLabels={pen:"만년필",ink:"잉크"};
 t.actions=[{...action,status:"SUCCEEDED",question:undefined},{...action,id:"research",type:"START_RESEARCH",sequence:1,status:"FAILED",reasonCode:"PARTIAL_FAILURE",question:undefined,decisions:[],jobs:[{jobId:"j1",actionId:"research",kind:"RESEARCH_ROUND",targetId:"pen",status:"SUCCEEDED",effects:[{kind:"CANDIDATES_ADDED",targetId:"pen",count:6}]},{jobId:"j2",actionId:"research",kind:"RESEARCH_ROUND",targetId:"ink",status:"FAILED",reasonCode:"CATALOG_UNAVAILABLE",effects:[]}]},{...action,id:"later",type:"BUDGET_CHANGE",sequence:2,status:"SKIPPED",question:undefined,decisions:[]}];
 const reload=vi.fn().mockResolvedValue(undefined);vi.mocked(retryIntelligenceJob).mockResolvedValue({jobId:"retry"});
 vi.mocked(useCurationThreads).mockReturnValue({...sheet,threads:[t],active:undefined,sending:false,busy:false,reload} as unknown as ReturnType<typeof useCurationThreads>);
 await mount(<CurationThreadReport thread={t}/>);
 const report=document.querySelector(".curation-thread-report")!;expect(report.getAttribute("data-thread-outcome")).toBe("PARTIAL");
 expect(report.textContent).toContain("일부 완료");expect(report.textContent).toContain("만년필 후보 6개를 찾았어요.");expect(report.textContent).toContain("잉크: 상품 카탈로그를 조회하지 못했습니다.");expect(report.textContent).toContain("이후 작업은 진행하지 않았어요.");
 expect(report.textContent).not.toContain("오류로 요청이 중단되었습니다");
 await click("다시 시도");expect(retryIntelligenceJob).toHaveBeenCalledWith("j2");await act(async()=>{});expect(reload).toHaveBeenCalled();
});
it("reports a plain failure with its reason and a cancelled request without controls",async()=>{
 locale="en-US";const failed=thread();failed.status="FAILED";failed.reasonCode="AUTO_PROVIDER_UNAVAILABLE";failed.actions=[{...action,status:"FAILED",reasonCode:"AUTO_PROVIDER_UNAVAILABLE",question:undefined}];
 vi.mocked(useCurationThreads).mockReturnValue({...sheet,threads:[failed],active:undefined,sending:false,busy:false} as unknown as ReturnType<typeof useCurationThreads>);
 await mount(<CurationThreadReport thread={failed}/>);
 expect(document.querySelector(".curation-thread-report")!.getAttribute("data-thread-outcome")).toBe("FAILED");
 expect(document.body.textContent).toContain("The request could not be completed.");expect(document.body.textContent).toContain("Research intelligence is unavailable.");expect(button("Try again")).toBeUndefined();
 await act(async()=>root.unmount());
 const cancelled=thread();cancelled.status="CANCELLED";cancelled.actions=[{...action,status:"CANCELLED",question:undefined}];
 await mount(<CurationThreadReport thread={cancelled}/>);
 expect(document.querySelector(".curation-thread-report")!.getAttribute("data-thread-outcome")).toBe("CANCELLED");expect(document.body.textContent).toContain("Request stopped.");expect(document.querySelectorAll("button")).toHaveLength(1);
});
