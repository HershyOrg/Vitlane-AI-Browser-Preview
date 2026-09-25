// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { LocaleProvider } from "../../../../shared/i18n";
import { AmazonAPIUsage } from "./AmazonAPIUsage";

afterEach(() => { vi.unstubAllGlobals(); window.localStorage.clear(); });
for (const locale of ["en-US", "ko-KR"] as const) it(`${locale}: marks stub quota as historical and retains it on refresh failure`, async () => {
  document.cookie = `vt_locale_choice=${locale}; Path=/`;
  window.localStorage.setItem("vitlane.locale.v1", locale);
  let fail = false;
  const fetch = vi.fn(async () => fail
    ? new Response("{}", { status: 503 })
    : new Response(JSON.stringify({ mode: "STUB", enabled: true, source: "AMAZON", quota: {
      remaining: 45, limit: 100, observedAt: "2026-09-10T00:00:00Z", resetAt: "2026-10-01T00:00:00Z",
    }, estimatedRemaining: 45, requests24h: 48, succeeded24h: 47 }), { status: 200 }));
  vi.stubGlobal("fetch", fetch);
  const el = document.createElement("div"); document.body.append(el);
  const root = createRoot(el);
  try {
    await act(async () => root.render(<LocaleProvider><AmazonAPIUsage /></LocaleProvider>));
    const notice = locale === "ko-KR" ? "마지막 실조회 기록" : "last live check";
    expect(el.querySelector('[role="status"]')?.textContent).toContain(notice);
    expect(el.textContent).toContain("45 / 100");
    await act(async () => Array.from(el.querySelectorAll("button")).find(button => button.textContent?.includes(locale === "ko-KR" ? "Amazon 사용량" : "Refresh Amazon"))!.click());
    expect(fetch.mock.calls).toHaveLength(2);
    expect(el.querySelector('[role="status"]')?.textContent).toContain(notice);
    fail = true;
    await act(async () => Array.from(el.querySelectorAll("button")).find(button => button.textContent?.includes(locale === "ko-KR" ? "Amazon 사용량" : "Refresh Amazon"))!.click());
    expect(el.querySelector('[role="alert"]')).not.toBeNull();
    expect(el.textContent).toContain("45 / 100");
    expect(el.querySelector('[role="status"]')?.textContent).toContain(notice);
  } finally { await act(async () => root.unmount()); el.remove(); }
});

for (const locale of ["en-US", "ko-KR"] as const) it(`${locale}: saves the operator switch, preserves quota and reloads a concurrent change`, async () => {
  document.cookie = `vt_locale_choice=${locale}; Path=/`; window.localStorage.setItem("vitlane.locale.v1", locale);
  let enabled=true,version=1,conflict=false;
  const writes: Array<unknown>=[];
  const usage=()=>({schemaVersion:"vitlane.catalog-api-usage.v3",source:"AMAZON",enabled,configured:true,
    control:{enabled,version,updatedAt:"2026-09-11T00:00:00Z"},mode:enabled?"STUB":"DISABLED",
    quota:{remaining:0,limit:100,observedAt:"2026-09-11T00:00:00Z",resetAt:"2026-10-01T00:00:00Z"},estimatedRemaining:0,requests24h:100,succeeded24h:100});
  const fetch=vi.fn(async (url:string,init?:RequestInit)=>{
    if(init?.method==="PUT"){
      expect(url).toBe("/api/v1/admin/catalog-sources/amazon/control");
      const body=JSON.parse(init.body as string);writes.push(body);expect(body.expectedVersion).toBe(version);
      if(conflict){enabled=false;version++;return new Response(JSON.stringify({error:{code:"CONFLICT",reasonCode:"AMAZON_CONTROL_VERSION_CONFLICT"}}),{status:409});}
      enabled=body.enabled;version++;
    }else expect(url).toBe("/api/v1/admin/catalog-sources/amazon/usage");
    return new Response(JSON.stringify(usage()),{status:200});
  });vi.stubGlobal("fetch",fetch);
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
  try {
    await act(async()=>root.render(<LocaleProvider><AmazonAPIUsage/></LocaleProvider>));
    const toggle=()=>el.querySelector<HTMLButtonElement>('[role="switch"]')!;
    expect(toggle().getAttribute("aria-checked")).toBe("true");
    await act(async()=>toggle().click());expect(toggle().getAttribute("aria-checked")).toBe("false");expect(writes[0]).toEqual({enabled:false,expectedVersion:1});
    expect(el.textContent).toContain("0 / 100");
    await act(async()=>toggle().click());expect(toggle().getAttribute("aria-checked")).toBe("true");expect(el.textContent).toContain("stub");
    conflict=true;await act(async()=>toggle().click());expect(toggle().getAttribute("aria-checked")).toBe("false");expect(el.querySelector('[role="alert"]')).not.toBeNull();
    expect(fetch.mock.calls).toHaveLength(5);
  }finally{await act(async()=>root.unmount());el.remove();}
});
