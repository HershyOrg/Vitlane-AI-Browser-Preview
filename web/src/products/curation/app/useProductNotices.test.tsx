// @vitest-environment jsdom
import {act} from "react";
import {createRoot,type Root} from "react-dom/client";
import {beforeEach,afterEach,it,expect,vi} from "vitest";
import {syncProductNotices} from "../infra/backgroundApi";
import {useProductNotices} from "./useProductNotices";
vi.mock("../infra/backgroundApi",()=>({syncProductNotices:vi.fn(),backgroundChanged:"bg"}));
const api=vi.mocked(syncProductNotices);
let root:Root,host:HTMLDivElement,seen:Set<string>;
function Probe({id}:{id?:string}){seen=useProductNotices("user",id);return null}
beforeEach(()=>{vi.useFakeTimers();api.mockReset();api.mockResolvedValue({schemaVersion:"vitlane.curation-notices.v1",curationIds:["a","b"]});Object.defineProperty(document,"visibilityState",{configurable:true,value:"visible"});host=document.createElement("div");document.body.append(host);root=createRoot(host);});
afterEach(async()=>{await act(async()=>root.unmount());host.remove();vi.useRealTimers();});
it("polls only notices every minute and suppresses the current curation",async()=>{
 await act(async()=>root.render(<Probe id="a"/>));expect([...seen]).toEqual(["b"]);expect(api).toHaveBeenCalledTimes(1);
 await act(async()=>vi.advanceTimersByTimeAsync(59999));expect(api).toHaveBeenCalledTimes(1);
 await act(async()=>vi.advanceTimersByTimeAsync(1));expect(api).toHaveBeenCalledTimes(2);
 Object.defineProperty(document,"visibilityState",{configurable:true,value:"hidden"});
 await act(async()=>document.dispatchEvent(new Event("visibilitychange")));expect(api.mock.calls.at(-1)?.[2]).toBe(false);
 const calls=api.mock.calls.length;await act(async()=>vi.advanceTimersByTimeAsync(120000));expect(api).toHaveBeenCalledTimes(calls);
});
it("releases the previous lease before marking a new curation visible",async()=>{
 await act(async()=>root.render(<Probe id="a"/>));await act(async()=>root.render(<Probe id="b"/>));
 expect(api.mock.calls.slice(-2).map(c=>[c[1],c[2]])).toEqual([[undefined,false],["b",true]]);
 expect([...seen]).toEqual(["a"]);
});
