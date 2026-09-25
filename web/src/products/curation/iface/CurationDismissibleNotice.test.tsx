// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it } from "vitest";
import { LocaleProvider } from "../../../shared/i18n";
import { CurationSourceNotices } from "./CurationDismissibleNotice";
import type { SourceCoverage } from "../research/infra/liveCatalogReviewApi";

afterEach(() => { window.localStorage.clear(); document.cookie = "vt_locale_choice=; Max-Age=0; Path=/"; });

it.each(["en-US", "ko-KR"])("dismisses one source event across polling and restores a new failure in %s", async (locale) => {
  document.cookie = "vt_locale_choice=; Max-Age=0; Path=/";
  window.localStorage.setItem("vitlane.locale.v2", locale);
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  const coverage: SourceCoverage[] = [
    { source: "AMAZON", status: "FAILED", reasonCode: "PROVIDER_TIMEOUT", candidateCount: 0 },
    { source: "SHOPIFY", status: "FAILED", reasonCode: "PROVIDER_TIMEOUT", candidateCount: 0 },
  ];
  const snapshot = JSON.stringify(coverage);
  const render = (scopeId: string, rows = coverage) => act(async () => root.render(
    <LocaleProvider><CurationSourceNotices coverage={rows} scopeId={scopeId} /></LocaleProvider>,
  ));
  const closeAmazon = () => container.querySelector<HTMLButtonElement>(
    `button[aria-label="${locale === "ko-KR" ? "Amazon 안내 닫기" : "Dismiss Amazon notification"}"]`,
  );
  try {
    await render("round-1");
    expect(container.querySelectorAll('.vt-notice')).toHaveLength(2);
    expect(container.querySelector('[role="alert"]')?.textContent).toContain("Amazon");
    await act(async () => closeAmazon()!.click());
    expect(container.querySelectorAll('.vt-notice')).toHaveLength(1);
    expect(container.textContent).toContain("Shopify");
    await render("round-1", structuredClone(coverage));
    expect(closeAmazon()).toBeNull();
    await render("round-2");
    expect(closeAmazon()).not.toBeNull();
    await act(async () => closeAmazon()!.click());
    await render("round-2", [coverage[1]]);
    await render("round-2");
    expect(closeAmazon()).not.toBeNull();
    expect(JSON.stringify(coverage)).toBe(snapshot);
  } finally {
    await act(async () => root.unmount()); container.remove();
  }
});

it.each(["en-US", "ko-KR"])("omits Amazon admission notices but retains actual failures in %s", async(locale)=>{
 window.localStorage.setItem("vitlane.locale.v2",locale);
 const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
 try{
  for(const reasonCode of ["AMAZON_SOURCE_DISABLED","AMAZON_QUOTA_EXHAUSTED","AMAZON_QUOTA_UNCONFIRMED","AMAZON_RATE_LIMITED"]){
   for(const status of ["SKIPPED","FAILED"] as const){
    await act(async()=>root.render(<LocaleProvider><CurationSourceNotices scopeId="round" coverage={[{source:"AMAZON",status,reasonCode,candidateCount:0},{source:"SHOPIFY",status:"FAILED",reasonCode:"PROVIDER_TIMEOUT",candidateCount:0}]}/></LocaleProvider>));
    expect(el.querySelectorAll('.vt-notice')).toHaveLength(1);expect(el.textContent).toContain("Shopify");expect(el.textContent).not.toContain("Amazon");
   }
  }
  await act(async()=>root.render(<LocaleProvider><CurationSourceNotices scopeId="round" coverage={[{source:"AMAZON",status:"FAILED",reasonCode:"AMAZON_SCHEMA_MISMATCH",candidateCount:0}]}/></LocaleProvider>));
  expect(el.querySelector('[role="alert"]')?.textContent).toContain("Amazon");
 }finally{await act(async()=>root.unmount());el.remove();}
});


it.each(["en-US", "ko-KR"])("keeps normal source coverage silent and unchanged in %s", async locale => {
 window.localStorage.setItem("vitlane.locale.v2",locale);
 const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
 const coverage: SourceCoverage[] = [
  {source:"SHOPIFY",status:"UNSUPPORTED",reasonCode:"SHOPIFY_KR_MARKET_UNSUPPORTED",candidateCount:0},
  {source:"AMAZON",status:"UNSUPPORTED",reasonCode:"AMAZON_MARKET_UNSUPPORTED",candidateCount:0},
  {source:"ELEVENST",status:"PARTIAL",reasonCode:"CATALOG_UPSTREAM_FAILED",candidateCount:2},
  {source:"COUPANG",status:"FAILED",reasonCode:"CATALOG_API_DISABLED",candidateCount:0},
 ];
 const before=JSON.stringify(coverage);
 try {
  await act(async()=>root.render(<LocaleProvider><CurationSourceNotices scopeId="normal" coverage={coverage}/></LocaleProvider>));
  expect(el.querySelectorAll('.vt-notice')).toHaveLength(0);
  expect(JSON.stringify(coverage)).toBe(before);
 } finally {await act(async()=>root.unmount());el.remove();}
});
