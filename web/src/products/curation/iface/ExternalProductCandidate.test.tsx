// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { LocaleProvider } from "../../../shared/i18n";
import { ExternalProductCandidate } from "./ExternalProductCandidate";
import { listCatalogLikedCandidates } from "../research/infra/catalogLikedCandidates";
import type { LiveCatalogProduct } from "../research/infra/liveCatalogReviewApi";

const product: LiveCatalogProduct = { candidateId:"elevenst-card",source:"ELEVENST",title:"Original fountain pen",description:"",currency:"KRW",categories:[],features:[],specifications:[],externalObservation:{schemaVersion:"vitlane.external-product-observation.v1",productRef:{source:"ELEVENST",marketplace:"KR",productId:"12345"},productUrl:"https://www.11st.co.kr/products/12345",title:"Original fountain pen",price:{kind:"UNKNOWN",reasonCode:"PRICE_NOT_REPORTED"},priceScope:"PRODUCT",seller:{kind:"UNKNOWN"},observedAt:"2026-09-13T00:00:00Z",provenance:{apiProvider:"fixture",apiProduct:"fixture",discoveryChannel:"NAVER_WEB",country:"KR",queryLanguage:"ko"}} };
afterEach(()=>{vi.unstubAllGlobals();window.localStorage.clear();});
for(const locale of ["en-US","ko-KR"] as const) it(`${locale}: product fallback persists reactions and account likes without a fake Variant`,async()=>{
  document.cookie=`vt_locale_choice=${locale}; Path=/`;window.localStorage.setItem("vitlane.locale.v1",locale);
  let saved={pinned:false,sentiment:"NONE",version:0};let fail=false;const writes:unknown[]=[];
  vi.stubGlobal("fetch",vi.fn(async(url:string,options?:RequestInit)=>{
    if(url.endsWith("/reaction")){
      const body=JSON.parse(options?.body as string);expect(body.productRef).toEqual(product.externalObservation!.productRef);expect(body).not.toHaveProperty("variantId");expect(body.expectedVersion).toBe(saved.version);
      if(fail)return new Response("{}",{status:503});
      writes.push(body);saved={pinned:body.pinned,sentiment:body.sentiment,version:saved.version+1};return new Response(JSON.stringify({reaction:saved}),{status:200});
    }
    if(url==="/api/v1/account/liked-variants")return new Response(JSON.stringify({candidates:[],products:saved.sentiment==="LIKE"?[{curationId:"curation",candidateId:product.candidateId,observation:product.externalObservation,updatedAt:"2026-09-13T00:00:00Z"}]:[]}),{status:200});
    if(!url.endsWith("/external-product"))throw new Error(`Unexpected Variant/provider/Cart call: ${url}`);
    return new Response(JSON.stringify({reactionAllowed:true,reaction:saved,purchaseFeedback:{version:0,records:[]}}),{status:200});
  }));
  const el=document.createElement("div");document.body.append(el);let root=createRoot(el);
  const mount=()=>act(async()=>root.render(<LocaleProvider><ExternalProductCandidate product={product} curationId="curation"/></LocaleProvider>));
  const open=()=>act(async()=>el.querySelector<HTMLButtonElement>(".vt-candidate-card__hit-area")!.click());
  const button=(action:string)=>document.querySelector<HTMLButtonElement>(`[data-reaction="${action}"]`)!;
  await mount();await open();
  expect(document.querySelector('[role="dialog"]')?.textContent).not.toMatch(/Save option|옵션 저장|Add to cart|장바구니 담기/);
  expect(document.querySelector(".catalog-ui-variant-interactions")?.getAttribute("aria-label")).toBe(locale==="ko-KR"?"이 상품 반응":"Reaction to this product");
  await act(async()=>button("pin").click());await act(async()=>button("like").click());
  expect(saved).toEqual({pinned:true,sentiment:"LIKE",version:2});
  const liked=await listCatalogLikedCandidates();expect(liked).toHaveLength(1);expect(liked[0].variantId).toBeUndefined();expect(liked[0].variantTitle).toBeUndefined();expect(liked[0].priceUnknown).toBe(true);
  fail=true;await act(async()=>button("dislike").click());expect(button("like").getAttribute("aria-pressed")).toBe("true");expect(writes).toHaveLength(2);fail=false;
  await act(async()=>root.unmount());root=createRoot(el);await mount();await open();expect(button("pin").getAttribute("aria-pressed")).toBe("true");expect(button("like").getAttribute("aria-pressed")).toBe("true");
  await act(async()=>button("dislike").click());expect(saved.sentiment).toBe("DISLIKE");expect(await listCatalogLikedCandidates()).toHaveLength(0);
  await act(async()=>button("pin").click());expect(saved.pinned).toBe(false);
  await act(async()=>root.unmount());el.remove();
});

for(const blocked of ["server-denied","known-variant"] as const) it(`keeps product reactions blocked: ${blocked}`,async()=>{
  vi.stubGlobal("fetch",vi.fn(async()=>new Response(JSON.stringify({reactionAllowed:blocked!=="server-denied",reaction:{pinned:false,sentiment:"NONE",version:0},purchaseFeedback:{version:0,records:[]}}),{status:200})));
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
  const value=blocked==="known-variant"?{...product,previewVariant:{id:"known-variant",title:"Exact option",priceMinor:100,currency:"KRW"}}:product;
  await act(async()=>root.render(<LocaleProvider><ExternalProductCandidate product={value} curationId="curation"/></LocaleProvider>));
  await act(async()=>el.querySelector<HTMLButtonElement>(".vt-candidate-card__hit-area")!.click());
  expect(document.querySelector("[data-reaction]")).toBeNull();
  await act(async()=>root.unmount());el.remove();
});

for(const locale of ["en-US","ko-KR"] as const) it(`${locale}: a checked product shows the purchased signal and the self-report notice`,async()=>{
  document.cookie=`vt_locale_choice=${locale}; Path=/`;window.localStorage.setItem("vitlane.locale.v1",locale);
  vi.stubGlobal("fetch",vi.fn(async(url:string)=>{
    if(!url.endsWith("/external-product"))throw new Error(`Unexpected call: ${url}`);
    return new Response(JSON.stringify({reactionAllowed:true,reaction:{pinned:false,sentiment:"NONE",version:0},purchaseFeedback:{version:1,records:[{candidateId:product.candidateId,productRef:product.externalObservation!.productRef,checked:true,version:1,recordedAt:"2026-09-14T00:00:00Z",evidence:"SELF_REPORTED"}]}}),{status:200});
  }));
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
  await act(async()=>root.render(<LocaleProvider><ExternalProductCandidate product={product} curationId="curation"/></LocaleProvider>));
  await act(async()=>Promise.resolve());
  expect(el.querySelector(".vt-candidate-card__signal.is-purchased")).not.toBeNull();
  expect(el.textContent).toContain(locale==="ko-KR"?"구매함":"Marked purchased");
  expect(el.textContent).toContain(locale==="ko-KR"?"직접 구매했다고 표시했습니다":"Marked by you.");
  expect(Array.from(el.querySelectorAll("button")).some((b)=>b.textContent?.trim()===(locale==="ko-KR"?"체크 취소":"Undo"))).toBe(true);
  await act(async()=>root.unmount());el.remove();
});

it("다른 카드의 체크 결과는 이벤트로 받아 쓰고 서버에 다시 묻지 않는다",async()=>{
  document.cookie="vt_locale_choice=ko-KR; Path=/";window.localStorage.setItem("vitlane.locale.v1","ko-KR");
  let reads=0;
  vi.stubGlobal("fetch",vi.fn(async(url:string)=>{
    if(!url.endsWith("/external-product"))throw new Error(`Unexpected call: ${url}`);
    reads++;
    return new Response(JSON.stringify({reactionAllowed:true,reaction:{pinned:false,sentiment:"NONE",version:0},purchaseFeedback:{schemaVersion:"vitlane.external-purchase-feedback.v4",version:1,records:[]}}),{status:200});
  }));
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
  await act(async()=>root.render(<LocaleProvider><ExternalProductCandidate product={product} curationId="curation"/></LocaleProvider>));
  await act(async()=>Promise.resolve());
  expect(reads).toBe(1);
  const checked={schemaVersion:"vitlane.external-purchase-feedback.v4",version:2,records:[{candidateId:product.candidateId,productRef:product.externalObservation!.productRef,checked:true,version:1,recordedAt:"2026-09-16T00:00:00Z",evidence:"SELF_REPORTED"}]};
  await act(async()=>{window.dispatchEvent(new CustomEvent("vitlane:external-purchase-changed",{detail:{curationId:"curation",feedback:checked}}));});
  expect(reads).toBe(1);
  expect(el.querySelector(".vt-candidate-card__signal.is-purchased")).not.toBeNull();
  // 다른 큐레이션의 이벤트는 무시하고, 결과 없는 알림에만 다시 읽는다.
  await act(async()=>{window.dispatchEvent(new CustomEvent("vitlane:external-purchase-changed",{detail:{curationId:"other",feedback:{schemaVersion:"vitlane.external-purchase-feedback.v4",version:9,records:[]}}}));});
  expect(reads).toBe(1);
  await act(async()=>{window.dispatchEvent(new Event("vitlane:external-purchase-changed"));});
  await act(async()=>Promise.resolve());
  expect(reads).toBe(2);
  // 다른 카드의 반응 이벤트는 이 카드를 다시 읽게 하지 않는다.
  await act(async()=>{window.dispatchEvent(new CustomEvent("vitlane:product-reactions-changed",{detail:{curationId:"curation",candidateId:"another-card"}}));});
  expect(reads).toBe(2);
  await act(async()=>{window.dispatchEvent(new CustomEvent("vitlane:product-reactions-changed",{detail:{curationId:"curation",candidateId:product.candidateId}}));});
  await act(async()=>Promise.resolve());
  expect(reads).toBe(3);
  await act(async()=>root.unmount());el.remove();
});

it("opens straight into its details for the conversation: no card is drawn, and closing lets the owner go", async () => {
  document.cookie = "vt_locale_choice=ko-KR; Path=/"; window.localStorage.setItem("vitlane.locale.v1", "ko-KR");
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ reactionAllowed: true, reaction: { pinned: false, sentiment: "NONE", version: 0 }, purchaseFeedback: { version: 0, records: [] } }), { status: 200 })));
  const el = document.createElement("div"); document.body.append(el); const root = createRoot(el);
  const opened = vi.fn(), closed = vi.fn();
  await act(async () => root.render(<LocaleProvider><ExternalProductCandidate detailsOnly product={product} curationId="curation" openRequest={1} onOpened={opened} onClosed={closed} /></LocaleProvider>));
  await act(async () => Promise.resolve());
  // The reply's product name, or the row in the conversation, leads here without the product group's list.
  expect(el.querySelector(".vt-candidate-card")).toBeNull();
  const details = document.querySelector<HTMLElement>(".catalog-ui-candidate-modal")!;
  expect(details.querySelector("h2")?.textContent).toBe("Original fountain pen");
  expect(opened).toHaveBeenCalledTimes(1);
  // The details are a sheet that simply appears: no scrim behind it.
  expect(details.parentElement?.classList.contains("vt-scrim")).toBe(false);
  await act(async () => details.querySelector<HTMLButtonElement>('[aria-label="상품 상세 닫기"]')!.click());
  expect(document.querySelector(".catalog-ui-candidate-modal")).toBeNull();
  expect(closed).toHaveBeenCalledTimes(1);
  await act(async () => root.unmount()); el.remove();
});

it("워크스페이스가 준 상태를 쓰면 카드가 서버를 따로 읽지 않는다",async()=>{
  document.cookie="vt_locale_choice=ko-KR; Path=/";window.localStorage.setItem("vitlane.locale.v1","ko-KR");
  let reads=0;let stale=0;
  vi.stubGlobal("fetch",vi.fn(async(url:string)=>{
    if(!url.endsWith("/external-product"))throw new Error(`Unexpected call: ${url}`);
    reads++;return new Response(JSON.stringify({reactionAllowed:true,reaction:{pinned:false,sentiment:"NONE",version:0},purchaseFeedback:{schemaVersion:"vitlane.external-purchase-feedback.v4",version:1,records:[]}}),{status:200});
  }));
  const cardState={purchaseFeedback:{schemaVersion:"vitlane.external-purchase-feedback.v4",version:3,records:[{candidateId:product.candidateId,productRef:product.externalObservation!.productRef,checked:true,version:2,recordedAt:"2026-09-16T00:00:00Z",evidence:"SELF_REPORTED" as const}]},reactionAllowed:true,reaction:{pinned:true,sentiment:"LIKE" as const,version:4}};
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
  await act(async()=>root.render(<LocaleProvider><ExternalProductCandidate product={product} curationId="curation" cardState={cardState} onStateStale={()=>{stale++;}}/></LocaleProvider>));
  await act(async()=>Promise.resolve());
  // 화면을 열 때 카드 수만큼 조회하지 않는다.
  expect(reads).toBe(0);
  expect(el.querySelector(".vt-candidate-card__signal.is-purchased")).not.toBeNull();
  await act(async()=>el.querySelector<HTMLButtonElement>(".vt-candidate-card__hit-area")!.click());
  expect(document.querySelector<HTMLButtonElement>('[data-reaction="pin"]')!.getAttribute("aria-pressed")).toBe("true");
  expect(document.querySelector<HTMLButtonElement>('[data-reaction="like"]')!.getAttribute("aria-pressed")).toBe("true");
  // 결과 없는 알림에도 카드가 아니라 한 번의 워크스페이스 읽기를 요청한다.
  await act(async()=>{window.dispatchEvent(new Event("vitlane:external-purchase-changed"));});
  await act(async()=>Promise.resolve());
  expect(reads).toBe(0);expect(stale).toBe(1);
  await act(async()=>root.unmount());el.remove();
});

it("자기 읽기가 실패한 카드는 워크스페이스 읽기로 복구하고 오류 표시를 지운다",async()=>{
  document.cookie="vt_locale_choice=ko-KR; Path=/";window.localStorage.setItem("vitlane.locale.v1","ko-KR");
  let reads=0;let stale=0;
  vi.stubGlobal("fetch",vi.fn(async(url:string)=>{
    if(!url.endsWith("/external-product"))throw new Error(`Unexpected call: ${url}`);
    reads++;return new Response("{}",{status:503});
  }));
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
  const recovered={purchaseFeedback:{schemaVersion:"vitlane.external-purchase-feedback.v4",version:2,records:[{candidateId:product.candidateId,productRef:product.externalObservation!.productRef,checked:true,version:1,recordedAt:"2026-09-16T00:00:00Z",evidence:"SELF_REPORTED" as const}]},reactionAllowed:true,reaction:{pinned:false,sentiment:"NONE" as const,version:0}};
  const render=(state?:typeof recovered)=>act(async()=>root.render(<LocaleProvider><ExternalProductCandidate product={product} curationId="curation" cardState={state} onStateStale={async()=>{stale++;}}/></LocaleProvider>));
  // 서버가 상태를 싣지 않으면 카드가 스스로 읽고, 실패하면 오류를 보인다.
  await render();await act(async()=>Promise.resolve());
  expect(reads).toBe(1);
  const reloadButton=()=>Array.from(el.querySelectorAll("button")).find(b=>b.textContent==="구매 기록 다시 불러오기");
  expect(reloadButton()).toBeDefined();
  // 재조회는 카드가 아니라 워크스페이스 읽기를 요청한다.
  await act(async()=>reloadButton()!.click());
  await act(async()=>Promise.resolve());
  expect(reads).toBe(1);expect(stale).toBe(1);
  // 그 읽기가 상태를 실어 오면 오류가 사라지고 체크 상태가 보인다.
  await render(recovered);await act(async()=>Promise.resolve());
  expect(reloadButton()).toBeUndefined();
  expect(el.querySelector(".vt-candidate-card__signal.is-purchased")).not.toBeNull();
  await act(async()=>root.unmount());el.remove();
});
