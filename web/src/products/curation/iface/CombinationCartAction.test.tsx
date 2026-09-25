// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { CombinationCartAction } from "./CombinationCartAction";
import { applyRepresentativeCart, loadCatalogCart, LiveCatalogAPIError } from "../research/infra/liveCatalogReviewApi";
import type { LiveCatalogProduct, CatalogCartResponse } from "../research/infra/liveCatalogReviewApi";
vi.mock("../research/infra/liveCatalogReviewApi",async original=>({...await original<object>(),applyRepresentativeCart:vi.fn(),loadCatalogCart:vi.fn()}));
let locale="ko-KR";
vi.mock("../../../shared/i18n",()=>({useLocale:()=>({l:(en:string,ko:string,values?:Record<string,string|number>)=>Object.entries(values??{}).reduce((s,[k,v])=>s.replaceAll("{"+k+"}",String(v)),locale==="ko-KR"?ko:en)}),invariantContent:(v:string)=>v}));
let root:Root;
afterEach(async()=>{await act(async()=>root?.unmount());document.body.innerHTML="";vi.clearAllMocks();locale="ko-KR";});
const variant={variantId:"fine",title:"Fine",priceMinor:2500,currency:"USD",available:true,selectedOptions:[]};
const product:LiveCatalogProduct={candidateId:"pen",title:"Pen",source:"SHOPIFY",description:"",currency:"USD",categories:[],features:[],specifications:[]};

const saved={schemaVersion:"vitlane.cart-view.v2",curationId:"cur",version:1,items:[]} as unknown as CatalogCartResponse;
async function mount(overrides:Partial<React.ComponentProps<typeof CombinationCartAction>>={}){
 const props={curationId:"cur",representatives:[{targetId:"writing",candidateId:"pen"}],products:new Map([["pen",product]]),configurations:{pen:{variant,observedAt:"2026-09-22T00:00:00Z",version:1}},cart:[],cartVersion:0,disabled:false,onSaved:vi.fn(),onBusy:vi.fn(),...overrides};
 vi.mocked(loadCatalogCart).mockResolvedValue(saved);
 vi.mocked(applyRepresentativeCart).mockResolvedValue(saved);
 const el=document.createElement("div");document.body.append(el);root=createRoot(el);
 await act(async()=>root.render(<CombinationCartAction {...props}/>));return props;
}
const click=async()=>{await act(async()=>document.querySelector<HTMLButtonElement>(".curation-results__combination")!.click());};
// Adding says nothing (owner 2026-09-23): the Cart count on the composer is the answer, external items included.
it.each(["ko-KR","en-US"])("adds current representatives, skips external items and shows no notice in %s",async mode=>{
 locale=mode;
 const props=await mount({representatives:[{targetId:"writing",candidateId:"pen"},{targetId:"other",candidateId:"external"}],products:new Map([["pen",product],["external",{...product,candidateId:"external",title:"External item",source:"AMAZON"}]])});
 expect(document.querySelector("button")!.textContent).toBe(mode==="ko-KR"?"조합을 Cart에 추가":"Add combination to Cart");
 await click();
 expect(applyRepresentativeCart).toHaveBeenCalledOnce();
 expect(vi.mocked(applyRepresentativeCart).mock.calls[0].slice(0,2)).toEqual(["cur",0]);
 expect(vi.mocked(applyRepresentativeCart).mock.calls[0][2]).toHaveLength(1);
 expect(props.onSaved).toHaveBeenCalledWith(saved);
 expect(document.querySelector(".curation-cart-toast")).toBeNull();
});
it("offers no action when nothing in the combination can go into the Vitlane Cart",async()=>{
 await mount({representatives:[{targetId:"writing",candidateId:"external"}],products:new Map([["external",{...product,candidateId:"external",title:"External item",source:"COUPANG"}]])});
 expect(document.querySelector(".curation-results__combination")).toBeNull();
 expect(document.querySelector(".curation-cart-toast")).toBeNull();
});
it("says nothing when every product it could add is already in the Cart",async()=>{
 await mount({cart:[{cartItemId:"cart:pen:fine",targetId:"writing",candidateId:"pen",variantId:"fine",productTitle:"Pen",selectedOptions:[],previewPriceMinor:2500,previewCurrency:"USD",quantity:1,observedAt:"2026-09-22T00:00:00Z"} as never]});
 await click();
 expect(applyRepresentativeCart).not.toHaveBeenCalled();
 expect(document.querySelector(".curation-cart-toast")).toBeNull();
});
it.each(["ko-KR","en-US"])("asks for Shopify options when nothing can be added in %s",async mode=>{
 locale=mode;
 await mount({configurations:{}});await click();
expect(applyRepresentativeCart).not.toHaveBeenCalled();
 expect(document.querySelector(".curation-cart-toast")!.textContent).toContain(mode==="ko-KR"?"상품 옵션을 선택해 주세요":"Please select product options");
});
it("opens the product whose option is not chosen yet instead of a notice",async()=>{
 const onChooseOption=vi.fn();
 await mount({configurations:{},onChooseOption});await click();
 expect(applyRepresentativeCart).not.toHaveBeenCalled();
 expect(onChooseOption).toHaveBeenCalledWith("pen","writing");
 expect(document.querySelector(".curation-cart-toast")).toBeNull();
});
it("adds what it can, then opens the product still waiting for its option",async()=>{
 const onChooseOption=vi.fn();
 const props=await mount({onChooseOption,representatives:[{targetId:"writing",candidateId:"pen"},{targetId:"other",candidateId:"ink"}],products:new Map([["pen",product],["ink",{...product,candidateId:"ink",title:"Ink"}]])});
 await click();
 expect(vi.mocked(applyRepresentativeCart).mock.calls[0][2].map(item=>item.candidateId)).toEqual(["pen"]);
 expect(props.onSaved).toHaveBeenCalledWith(saved);
 expect(onChooseOption).toHaveBeenCalledWith("ink","other");
 expect(document.querySelector(".curation-cart-toast")).toBeNull();
});
it("does not require an old recommendation to add representatives",async()=>{await mount();await click();expect(applyRepresentativeCart).toHaveBeenCalledOnce();});
it("refreshes a conflicting Cart without asking for another recommendation",async()=>{
 const props=await mount();vi.mocked(applyRepresentativeCart).mockRejectedValueOnce(new LiveCatalogAPIError({code:"CONFLICT",reasonCode:"PHASE8_CART_VERSION_CONFLICT",retryable:false}));
 await click();expect(props.onSaved).toHaveBeenCalledWith(saved);expect(document.body.textContent).not.toContain("다시 요청");
});
it("uses a changed representative and its current option",async()=>{
 const props=await mount();
 await act(async()=>root.render(<CombinationCartAction {...props} representatives={[{targetId:"writing",candidateId:"c"}]} products={new Map([["c",{...product,candidateId:"c",title:"C"}]])} configurations={{c:{variant:{...variant,variantId:"c-fine"},observedAt:"2026-09-23T00:00:00Z",version:5}}}/>));
 await click();
 expect(vi.mocked(applyRepresentativeCart).mock.calls[0][2][0]).toMatchObject({candidateId:"c",variantId:"c-fine"});
});
it("suppresses concurrent clicks",async()=>{
 await mount();let resolve!:(value:CatalogCartResponse)=>void;
 vi.mocked(applyRepresentativeCart).mockImplementation(()=>new Promise(r=>{resolve=r;}));
 await act(async()=>{const button=document.querySelector<HTMLButtonElement>(".curation-results__combination")!;button.click();button.click();});
 expect(applyRepresentativeCart).toHaveBeenCalledOnce();
 await act(async()=>resolve(saved));
});
it.each([new Error("connection lost"),new LiveCatalogAPIError({code:"PROVIDER_UNAVAILABLE",reasonCode:"TEMPORARY",retryable:true})])("reuses the exact command for an uncertain result",async error=>{
 await mount();vi.mocked(applyRepresentativeCart).mockRejectedValueOnce(error);
 await click();await click();
 const calls=vi.mocked(applyRepresentativeCart).mock.calls;
 expect(calls).toHaveLength(2);expect(calls[1]).toEqual(calls[0]);
});
it("reports a server-side variant rejection without pretending it succeeded",async()=>{
 const props=await mount();vi.mocked(applyRepresentativeCart).mockRejectedValueOnce(new LiveCatalogAPIError({code:"CONFLICT",reasonCode:"COMBINATION_VARIANT_NOT_CONFIRMED",retryable:false}));
 await click();expect(document.body.textContent).toContain("조합을 추가하지 못했습니다");expect(props.onSaved).toHaveBeenCalledWith(saved);
});
