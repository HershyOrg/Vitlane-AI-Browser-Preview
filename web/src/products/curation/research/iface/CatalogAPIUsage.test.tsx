// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { LocaleProvider } from "../../../../shared/i18n";
import { CatalogAPIUsage } from "./CatalogAPIUsage";
afterEach(() => { vi.unstubAllGlobals(); localStorage.clear(); });
for (const locale of ["en-US", "ko-KR"]) it(`${locale}: retains unknown quota and disabled configuration; resolves a control conflict without optimistic success`, async () => {
  document.cookie=`vt_locale_choice=${locale}; Path=/`;
  const row = {id:"OWN_WEB",apiProvider:"OpenWebNinja",apiProduct:"Web Search",quotaScope:"web",configured:true,control:{enabled:true,version:2},localRequestsPerMinute:10,localDailyLimit:100,requests24h:3,notFound24h:2,failures24h:[]};
  const missing={...row,id:"NAVER_WEBKR",apiProvider:"NAVER",apiProduct:"Webkr",configured:false,control:{enabled:false,version:1}};
  const fetchMock=vi.fn(async (url: string, init?: RequestInit) => {
    if(url.endsWith("/catalog-apis")) return Response.json({apis:[row,missing]});
    if(init?.method==="PUT") {
      expect(JSON.parse(init.body as string)).toEqual({schemaVersion:"vitlane.catalog-api-control.v1",enabled:false,expectedVersion:2});
      row.control={enabled:false,version:3};
      return Response.json({error:{code:"CONFLICT",message:"Conflict"}},{status:409});
    }
    expect(url).toBe("/api/v1/admin/catalog-apis/OWN_WEB/usage");
    return Response.json(row);
  });vi.stubGlobal("fetch",fetchMock);
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
  try {
    await act(async()=>root.render(<LocaleProvider><CatalogAPIUsage /></LocaleProvider>));
    const toggles=el.querySelectorAll<HTMLButtonElement>('[role="switch"]');
    expect(toggles[1].disabled).toBe(true);
    expect(el.querySelector('.catalog-api-row__usage')?.textContent).toContain(locale==="ko-KR"?"잔여 미확인":"Remaining unknown");
    await act(async()=>toggles[0].click());
    expect(toggles[0].getAttribute('aria-checked')).toBe('false');
    expect(el.querySelector('[role="alert"]')).not.toBeNull();
    expect(fetchMock).toHaveBeenCalledTimes(3);
    const detail=el.querySelector<HTMLButtonElement>('[aria-expanded]')!;
    await act(async()=>detail.click());
    expect(el.querySelector<HTMLElement>('.catalog-api-row__details')!.hidden).toBe(false);
    const details=el.querySelector<HTMLElement>('.catalog-api-row__details')!.textContent??'';
    expect(details).toContain(locale==='ko-KR'?'미발견 (24시간)2':'Not found (24h)2');
    const refresh=[...el.querySelectorAll<HTMLButtonElement>('button')].find(b=>b.textContent===(locale==='ko-KR'?'사용량 새로고침':'Refresh usage'))!;
    expect(refresh.disabled).toBe(true);
  } finally {await act(async()=>root.unmount());el.remove();}
});

it("refreshes every Actor view after a shared account update", async () => {
 document.cookie="vt_locale_choice=en-US; Path=/";
 let used=10000;
 const rows=()=>["APIFY_MUSINSA","APIFY_29CM"].map(id=>({id,apiProvider:"Apify",apiProduct:id,quotaScope:"apify_account",configured:true,control:{enabled:true,version:1},localRequestsPerMinute:2,localDailyLimit:20,localMaxConcurrent:1,requests24h:1,failures24h:[],resources:{canStart:true,pressure:used/100000,costClass:"METERED",constraints:[{id:"account:apify:usd",kind:"BUDGET",used,limit:100000,pressure:used/100000}]}}));
 const fetchMock=vi.fn(async(url:string)=>{
  if(url.endsWith("/catalog-apis"))return Response.json({apis:rows()});
  expect(url).toContain("APIFY_MUSINSA/usage?refresh=true");used=70000;return Response.json(rows()[0]);
 });vi.stubGlobal("fetch",fetchMock);
 const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
 try{
  await act(async()=>root.render(<LocaleProvider><CatalogAPIUsage/></LocaleProvider>));
  const refresh=[...el.querySelectorAll<HTMLButtonElement>("button")].find(b=>b.textContent==="Refresh usage")!;
  await act(async()=>refresh.click());
  expect(fetchMock).toHaveBeenCalledTimes(3);
  for(const details of el.querySelectorAll(".catalog-api-row__details")){
   expect(details.textContent).toContain("70%");
   expect(details.textContent).toContain("USD 0.0700 / 0.10");
  }
 }finally{await act(async()=>root.unmount());el.remove();}
});

// Shopify is one API product of the same ledger (SHOPIFY_UCP): the page groups it under its provider's
// name from the payload and shows its 24-hour calls and its failures by reason, like every other row.
it("lists Shopify's calls and failures from the same ledger as every other research API", async () => {
  document.cookie = "vt_locale_choice=ko-KR; Path=/";
  const shopify = { id: "SHOPIFY_UCP", apiProvider: "Shopify", apiProduct: "Global Catalog search and lookup (UCP)", quotaScope: "shopify_ucp", configured: true, control: { enabled: true, version: 1 }, localRequestsPerMinute: 600, localDailyLimit: 200000, localMaxConcurrent: 64, requests24h: 41, notFound24h: 0, failures24h: [{ reasonCode: "RATE_LIMITED", count: 2 }] };
  const naver = { ...shopify, id: "NAVER_WEBKR", apiProvider: "NAVER API HUB", apiProduct: "Search Webkr", requests24h: 7, failures24h: [] };
  vi.stubGlobal("fetch", vi.fn(async (url: string) => { expect(url.endsWith("/catalog-apis")).toBe(true); return Response.json({ apis: [shopify, naver] }); }));
  const el = document.createElement("div"); document.body.append(el); const root = createRoot(el);
  try {
    await act(async () => root.render(<LocaleProvider><CatalogAPIUsage /></LocaleProvider>));
    const text = el.textContent ?? "";
    expect(text).toContain("Shopify");
    expect(text).toContain("Global Catalog search and lookup (UCP)");
    expect(text).toContain("24시간 41회");
    expect(text.indexOf("Shopify")).toBeLessThan(text.indexOf("NAVER API HUB"));
    await act(async () => el.querySelector<HTMLButtonElement>("[aria-expanded]")!.click());
    expect(el.querySelector<HTMLElement>(".catalog-api-row__details")!.textContent).toContain("RATE_LIMITED: 2");
    expect(el.querySelectorAll('[role="switch"]')).toHaveLength(2);
  } finally { await act(async () => root.unmount()); el.remove(); }
});
