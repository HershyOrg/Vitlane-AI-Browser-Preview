// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import { LocaleProvider } from "../../../../shared/i18n";
import { CatalogOperationUsage } from "./CatalogOperationUsage";
for (const locale of ["ko-KR", "en-US"]) it(`${locale}: distinguishes feed page and link budgets`, async () => {
 document.cookie=`vt_locale_choice=${locale}; Path=/`;
 const el=document.createElement("div");const root=createRoot(el);
 try {
 await act(async()=>root.render(<LocaleProvider><dl><CatalogOperationUsage rows={[
 {operation:"FEED_PAGE",requests24h:96,failures24h:0,localDailyLimit:480},
 {operation:"LINK_RESOLVE",requests24h:384,failures24h:3,localDailyLimit:384},
 {operation:"USAGE",requests24h:1,failures24h:0},
 ]}/></dl></LocaleProvider>));
 expect(el.textContent).toContain(locale==="ko-KR"?"피드 페이지 (24시간)96회 / 일 한도 480회":"Feed pages (24h)96 calls / 480 daily limit");
 expect(el.textContent).toContain(locale==="ko-KR"?"상품 링크 확인 (24시간)384회 / 일 한도 384회 · 실패 3회":"Product link resolution (24h)384 calls / 384 daily limit · 3 failed");
 }finally{await act(async()=>root.unmount());document.cookie="vt_locale_choice=; Max-Age=0; Path=/";}
});
