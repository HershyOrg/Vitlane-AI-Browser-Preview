// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { CurationThreadProvider, THREAD_POLL_INTERVAL_MS, useCurationThreads } from "./useThreads";
import { request } from "../../../shared/api/client";
import { curationThreadsChangedEvent } from "../infra/curationApi";
vi.mock("../../../shared/api/client", () => ({request: vi.fn()}));
const api = vi.mocked(request);
function Probe(){ const s=useCurationThreads()!; return <><span>{s.ready ? "ready" : "loading"}</span><span>{s.threads[0]?.actions.length ?? "none"}</span><span>{s.threads[0] ? Object.keys(s.threads[0].targetLabels!).length : "none"}</span><button onClick={()=>void s.submit("same request",7).catch(()=>{})}>first</button><button onClick={()=>void s.submit("same request",9).catch(()=>{})}>retry</button></>; }
function StatusProbe(){ const s=useCurationThreads()!; return <><output id="mode">{s.mode.mode}:{s.mode.version}</output><output id="status">{s.threads.map(t=>t.status).join(",")||"none"}</output><output id="error">{String(s.error)}</output><output id="ready">{String(s.ready)}</output><button onClick={()=>void s.submit("again",3).catch(()=>{})}>submit</button></>; }
let root: Root;
let visibility: DocumentVisibilityState = "visible";
beforeEach(()=>{sessionStorage.clear();api.mockReset();visibility="visible";Object.defineProperty(document,"visibilityState",{configurable:true,get:()=>visibility})});afterEach(async()=>{await act(async()=>root?.unmount());document.body.innerHTML="";vi.useRealTimers()});
async function render(probe = <Probe/>){const el=document.createElement("div");document.body.append(el);root=createRoot(el);await act(async()=>root.render(<CurationThreadProvider curationId="curation">{probe}</CurationThreadProvider>));}
async function click(name:string){await act(async()=>{[...document.querySelectorAll("button")].find(b=>b.textContent===name)!.click()});}
const text = (id: string) => document.getElementById(id)!.textContent;
const listReads = () => api.mock.calls.filter(([path, options]) => String(path).endsWith("/threads") && !options?.method).length;
const modeReads = () => api.mock.calls.filter(([path, options]) => String(path).endsWith("/control-mode") && !options?.method).length;
const list = (statuses: string[], controlMode = { mode: "AUTO", version: 1 }) => ({ schemaVersion: "vitlane.curation-thread.v2", controlMode, threads: statuses.map((status, n) => ({ id: `thread-${n}`, status, actions: [], targetLabels: {}, updatedAt: `${status}-${n}` })) });
const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });
const settle = () => advance(0);

it("keeps the original request UUID and version after a lost response",async()=>{
 const sent: {headers?: Record<string,string>;body?: string}[]=[];
 api.mockImplementation(async(_path,options)=>{if(options?.method==="POST"){sent.push(options as typeof sent[number]);throw Error("connection lost")}return {controlMode:{mode:"AUTO",version:1},threads:[]}});
 await render();await click("first");expect(sent).toHaveLength(1);expect(sessionStorage.length).toBe(1);
 await click("retry");expect(sent).toHaveLength(2);expect(sent[1].headers).toEqual(sent[0].headers);expect(JSON.parse(sent[1].body!).expectedCurationVersion).toBe(7);expect(sent[1].body).toBe(sent[0].body);
});
it("reads a stored selection checkpoint with null collections",async()=>{
 api.mockImplementation(async()=>({controlMode:{mode:"AUTO",version:1},threads:[{id:"t",status:"WAITING_SELECTION",targetLabels:null,actions:null,decisions:null,answers:null}]}));
 await render();expect(document.body.textContent).toContain("ready0");
});

it("restores the mode from the list without asking for it separately", async () => {
 vi.useFakeTimers();
 api.mockImplementation(async () => list(["SUCCEEDED"], { mode: "MANUAL", version: 3 }));
 await render(<StatusProbe/>);
 expect(text("mode")).toBe("MANUAL:3");
 expect(listReads()).toBe(1);
 expect(modeReads()).toBe(0);
});

it("does not poll while no request is interpreting, running or waiting for a choice", async () => {
 vi.useFakeTimers();
 api.mockImplementation(async () => list(["SUCCEEDED", "WAITING_SELECTION", "CANCELLED", "FAILED"]));
 await render(<StatusProbe/>);
 await advance(30_000);
 expect(listReads()).toBe(1);
});

it("polls every two seconds while a request runs and stops once it ends", async () => {
 vi.useFakeTimers();
 let statuses = ["INTERPRETING"];
 api.mockImplementation(async () => list(statuses));
 await render(<StatusProbe/>);
 await advance(THREAD_POLL_INTERVAL_MS);
 statuses = ["RUNNING"];
 await advance(THREAD_POLL_INTERVAL_MS);
 expect(listReads()).toBe(3);
 expect(text("status")).toBe("RUNNING");
 statuses = ["SUCCEEDED"];
 await advance(THREAD_POLL_INTERVAL_MS);
 expect(text("status")).toBe("SUCCEEDED");
 await advance(20_000);
 expect(listReads()).toBe(4);
 expect(modeReads()).toBe(0);
});

it("stops polling in a hidden tab and reads once when the tab comes back", async () => {
 vi.useFakeTimers();
 api.mockImplementation(async () => list(["RUNNING"]));
 await render(<StatusProbe/>);
 await act(async () => { visibility = "hidden"; document.dispatchEvent(new Event("visibilitychange")); });
 await advance(20_000);
 expect(listReads()).toBe(1);
 // Returning fires both events; they share one read.
 await act(async () => {
  visibility = "visible";
  document.dispatchEvent(new Event("visibilitychange"));
  window.dispatchEvent(new Event("focus"));
 });
 await settle();
 expect(listReads()).toBe(2);
 await advance(THREAD_POLL_INTERVAL_MS);
 expect(listReads()).toBe(3);
});

it("reads after a Thread-touching call, once more if a read was already on its way", async () => {
 vi.useFakeTimers();
 let release: (() => void) | undefined;
 api.mockImplementation(async () => {
  if (listReads() === 2) await new Promise<void>(resolve => { release = resolve; });
  return list(["SUCCEEDED"]);
 });
 await render(<StatusProbe/>);
 await act(async () => { window.dispatchEvent(new CustomEvent(curationThreadsChangedEvent, { detail: { curationId: "other" } })); });
 expect(listReads()).toBe(1);
 await act(async () => { window.dispatchEvent(new CustomEvent(curationThreadsChangedEvent, { detail: {} })); });
 expect(listReads()).toBe(2);
 await act(async () => { window.dispatchEvent(new CustomEvent(curationThreadsChangedEvent, { detail: { curationId: "curation" } })); });
 expect(listReads()).toBe(2);
 await act(async () => { release!(); });
 await settle();
 expect(listReads()).toBe(3);
});

it("reads the list after a rejected submit so another window's request shows", async () => {
 vi.useFakeTimers();
 let statuses = ["SUCCEEDED"];
 api.mockImplementation(async (_path, options) => {
  if (options?.method === "POST") { statuses = ["RUNNING", "SUCCEEDED"]; throw Error("CURATION_THREAD_ACTIVE"); }
  return list(statuses);
 });
 await render(<StatusProbe/>);
 await click("submit");
 await settle();
 expect(listReads()).toBe(2);
 expect(text("status")).toBe("RUNNING,SUCCEEDED");
});

it("retries a failed read a few times once the list has loaded, then waits", async () => {
 vi.useFakeTimers();
 let failing = false;
 api.mockImplementation(async () => { if (failing) throw Error("offline"); return list(["SUCCEEDED"]); });
 await render(<StatusProbe/>);
 failing = true;
 await act(async () => { window.dispatchEvent(new Event("focus")); });
 await settle();
 expect(listReads()).toBe(2);
 expect(text("error")).toBe("true");
 await advance(2_000);
 await advance(5_000);
 await advance(10_000);
 expect(listReads()).toBe(5);
 await advance(60_000);
 expect(listReads()).toBe(5);
 failing = false;
 await act(async () => { window.dispatchEvent(new Event("focus")); });
 await settle();
 expect(listReads()).toBe(6);
 expect(text("error")).toBe("false");
});

it("keeps retrying the first read, which unlocks the composer", async () => {
 vi.useFakeTimers();
 let failures = 5;
 api.mockImplementation(async () => { if (failures-- > 0) throw Error("offline"); return list([]); });
 await render(<StatusProbe/>);
 expect(text("ready")).toBe("false");
 await advance(2_000);
 await advance(5_000);
 await advance(10_000);
 await advance(10_000);
 expect(listReads()).toBe(5);
 expect(text("ready")).toBe("false");
 await advance(10_000);
 expect(listReads()).toBe(6);
 expect(text("ready")).toBe("true");
});

it("remembers an open sheet for a Thread across bars that mount again",async()=>{
 api.mockImplementation(async path=>path.endsWith("control-mode")?{mode:"AUTO",version:1}:{threads:[]});
 let controller:ReturnType<typeof useCurationThreads>=null;
 function Screen(){controller=useCurationThreads();return null}
 const el=document.createElement("div");document.body.append(el);root=createRoot(el);
 await act(async()=>root.render(<CurationThreadProvider curationId="curation"><Screen key="planning"/></CurationThreadProvider>));
 act(()=>controller!.rememberSheet("thread",true));
 await act(async()=>root.render(<CurationThreadProvider curationId="curation"><Screen key="curating"/></CurationThreadProvider>));
 expect(controller!.sheetOpen("thread")).toBe(true);expect(controller!.sheetOpen("other")).toBeUndefined();
});
