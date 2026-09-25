// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { LocaleProvider } from "../../../../shared/i18n";
import { PreferencesProvider } from "../../../account/app/usePreferences";
import { usePlanFlow } from "../app/usePlanFlow";
import { PlanCreator } from "./PlanCreator";

const identity = vi.hoisted(() => ({ user: { id: "alice" } }));
vi.mock("../../../account/app/useCurrentUser", () => ({ useCurrentUser: () => identity }));
vi.mock("../app/useManagedRunner", () => ({ useManagedRunner: () => ({ capability: null }) }));

let root: Root;
let container: HTMLDivElement;
const preferences = (version: number, uiLocale = "ko-KR", researchCountry = "KR") => {
  const values = { schemaVersion: "vitlane.user-preferences.v1", version, uiLocale, preferredCurrency: "KRW", researchCountry };
  return { preferences: values, effective: values };
};
const response = (value: unknown) => new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } });

function App() {
  const flow = usePlanFlow();
  return <PlanCreator initialForm={flow.initialForm} working={flow.working} onSubmit={async form => { await flow.submitPlan(form); }} />;
}
const app = () => <LocaleProvider><PreferencesProvider><App /></PreferencesProvider></LocaleProvider>;

beforeEach(() => {
  identity.user = { id: "alice" };
  localStorage.clear();
  sessionStorage.clear();
  document.cookie = "vt_locale_choice=; Max-Age=0; Path=/";
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)); });
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

async function click(label: string) {
  const button = [...document.querySelectorAll<HTMLButtonElement>("button")].find(el => el.getAttribute("aria-label") === label || el.textContent?.trim() === label);
  expect(button, label).toBeDefined();
  await act(async () => button!.click());
}
async function selectUS() {
  await act(async () => {
    const select = document.querySelector<HTMLSelectElement>("#shipping-country")!;
    select.value = "US";
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
}
async function enterIntent() {
  await act(async () => {
    const textarea = container.querySelector("textarea")!;
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(textarea, "fountain pen");
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  });
}



async function select(selector:string,value:string) {
  await act(async()=>{
    const element=document.querySelector<HTMLSelectElement>(selector)!;
    element.value=value;element.dispatchEvent(new Event("change",{bubbles:true}));
  });
}
async function close() {
  await act(async()=>document.dispatchEvent(new KeyboardEvent("keydown",{key:"Escape",bubbles:true,cancelable:true})));
}

it.each(["ko-KR","en-US"])("waits for initial preferences then immediately persists country and submits it (%s)",async locale=>{
  let release!:(response:Response)=>void;
  let reads=0,stored=preferences(0,locale);
  const writes:{url:string;body:Record<string,unknown>}[]=[];
  vi.stubGlobal("fetch",vi.fn((url:string,init:RequestInit)=>{
    if(init.method==="POST") {writes.push({url,body:JSON.parse(init.body as string)});return Promise.resolve(response({plan:{id:"plan"},curation:{id:"curation"}}));}
    if(init.method==="PATCH") {
      const body=JSON.parse(init.body as string);writes.push({url,body});
      stored=preferences(1,locale,body.researchCountry);return Promise.resolve(response(stored));
    }
    return ++reads===1?new Promise<Response>(resolve=>{release=resolve;}):Promise.resolve(response(stored));
  }));
  await act(async()=>root.render(app()));await click("조사·보기 설정");
  expect(document.querySelector<HTMLSelectElement>("#shipping-country")!.disabled).toBe(true);
  await enterIntent();expect(writes).toHaveLength(0);
  await act(async()=>release(response(stored)));
  await selectUS();await close();
  await click(locale==="ko-KR"?"상품 찾기 시작":"Start product search");
  expect(writes).toEqual([
    {url:"/api/v1/me/preferences",body:{schemaVersion:"vitlane.user-preferences.v1",expectedVersion:0,researchCountry:"US"}},
    {url:"/api/v1/shopping-plans",body:expect.objectContaining({originalIntent:"fountain pen",location:{country:"US",city:""}})},
  ]);
});

it.each(["ko-KR","en-US"])("restores saved country/display currency after reload while resetting only the budget (%s)",async locale=>{
  let stored=preferences(0,locale,"US");
  const patches:Record<string,unknown>[]=[];
  vi.stubGlobal("fetch",vi.fn((_url:string,init:RequestInit)=>{
    if(init.method==="PATCH") {
      const body=JSON.parse(init.body as string);patches.push(body);
      const {schemaVersion,expectedVersion,...patch}=body;
      expect(expectedVersion).toBe(stored.preferences.version);
      stored={preferences:{...stored.preferences,...patch,version:expectedVersion+1},effective:{...stored.effective,...patch,version:expectedVersion+1}};
    }
    return Promise.resolve(response(stored));
  }));
  await act(async()=>root.render(app()));
  await click(locale==="ko-KR"?"조사·보기 설정":"Research and display settings");
  await select("#shipping-country","KR");
  await select(`[aria-label="${locale==="ko-KR"?"표시 통화":"Display currency"}"]`,"USD");
  await close();await click(locale==="ko-KR"?"예산 설정":"Budget settings");
  await click(locale==="ko-KR"?"자동 큐레이션":"Automatic curation");
  await act(async()=>{
    const amount=document.querySelector<HTMLInputElement>(`.init-settings input`)!;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,"value")!.set!.call(amount,"100");amount.dispatchEvent(new Event("input",{bubbles:true}));
  });
  await act(async()=>root.unmount());root=createRoot(container);await act(async()=>root.render(app()));
  expect(container.querySelector('.init-composer__settings')!.textContent).toContain("KR · USD");
  await click(locale==="ko-KR"?"예산 설정":"Budget settings");
  expect(document.querySelector('[role="switch"]')?.getAttribute("aria-checked")).toBe("true");
  expect(document.querySelector('.init-settings input')).toBeNull();
  expect(patches).toEqual([
    {schemaVersion:"vitlane.user-preferences.v1",expectedVersion:0,researchCountry:"KR"},
    {schemaVersion:"vitlane.user-preferences.v1",expectedVersion:1,preferredCurrency:"USD"},
  ]);
});

it.each(["researchCountry","preferredCurrency"])("ignores %s save completing after switching accounts",async field=>{
  let release!:(response:Response)=>void;
  vi.stubGlobal("fetch",vi.fn((_url:string,init:RequestInit)=>{
    if(init.method==="PATCH") return new Promise<Response>(resolve=>{release=resolve;});
    return Promise.resolve(response(preferences(0)));
  }));
  await act(async()=>root.render(app()));await click("조사·보기 설정");
  await select(field==="researchCountry"?"#shipping-country":'[aria-label="표시 통화"]',field==="researchCountry"?"US":"USD");
  identity.user={id:"bob"};await act(async()=>root.render(app()));
  await act(async()=>release(response({...preferences(1),effective:{...preferences(1).effective,[field]:field==="researchCountry"?"US":"USD"}})));
  expect(container.querySelector('.init-composer__settings')!.textContent).toContain("KR · KRW");
  expect(container.querySelector('textarea')!.disabled).toBe(false);
  expect(document.querySelector('.init-settings')).toBeNull();
});

it("ignores a stale preference read arriving after saving the country",async()=>{
  let release!:(response:Response)=>void, reads=0;
  let stored=preferences(0);
  vi.stubGlobal("fetch",vi.fn((_url:string,init:RequestInit)=>{
    if(init.method==="PATCH") {stored=preferences(1,"ko-KR","US");return Promise.resolve(response(stored));}
    if(++reads===2) return new Promise<Response>(resolve=>{release=resolve;});
    return Promise.resolve(response(stored));
  }));
  const {usePreferences}=await import("../../../account/app/usePreferences");
  function Refresh(){const prefs=usePreferences();return <button onClick={()=>void prefs.refresh()}>Refresh</button>;}
  await act(async()=>root.render(<LocaleProvider><PreferencesProvider><Refresh/><App/></PreferencesProvider></LocaleProvider>));
  await click("Refresh");await click("조사·보기 설정");await selectUS();
  await act(async()=>release(response(preferences(0))));
  expect(document.querySelector<HTMLSelectElement>("#shipping-country")!.value).toBe("US");
});
