import { useRef, useState } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import { Button } from "../../../shared/ui";
import { Toast, ToastClose, ToastDescription, ToastProvider, ToastTitle, ToastViewport } from "../../../shared/ui";
import { useLocale } from "../../../shared/i18n";
import { randomUUID } from "../../../shared/browser/randomUUID";
import { useBudget } from "../app/useBudget";
import { selectCombinationCart, type CombinationConfiguration, type CombinationSkip } from "../domain/combinationCart";
import { applyRepresentativeCart, loadCatalogCart, LiveCatalogAPIError, type LiveCartItem, type LiveCatalogProduct, type CatalogCartResponse } from "../research/infra/liveCatalogReviewApi";
import "./combination-cart-action.css";

type Props = {
 curationId:string; representatives:readonly {targetId:string;candidateId:string}[];
 products:ReadonlyMap<string,LiveCatalogProduct>; configurations:Readonly<Record<string,CombinationConfiguration>>;
 cart:readonly LiveCartItem[]; cartVersion:number; disabled:boolean;
 onSaved:(cart:CatalogCartResponse)=>void; onBusy:(busy:boolean)=>void;
 /** Opens a product where its option is chosen. Given, a missing option opens that product instead of a notice. */
 onChooseOption?:(candidateId:string,targetId:string)=>void;
};
type Pending = {expectedVersion:number;items:LiveCartItem[];commandId:string};
export function CombinationCartAction(props:Props) {
 const {l}=useLocale(),{ledger}=useBudget();
 const [busy,setBusy]=useState(false),[notice,setNotice]=useState<{id:number;title:string;body:string;warning:boolean;open:boolean}>();
 const lock=useRef(false),pending=useRef<Pending|undefined>(undefined),sequence=useRef(0);
 // Only what Vitlane can check out goes into its Cart (Shopify). A combination of external-store products alone has
 // nothing to add, so it offers no action instead of a notice that nothing was added (owner 2026-09-23).
 const addable=props.representatives.some(({candidateId})=>{const source=props.products.get(candidateId)?.source;return !source||source==="SHOPIFY";});
 const notify=(title:string,body:string,warning=true)=>setNotice({id:++sequence.current,title,body,warning,open:true});
 const skipText=(skipped:CombinationSkip[])=>skipped.map(item=>{
  const reason=item.reason==="EXTERNAL"?l("external-store item","외부몰 상품"):item.reason==="OPTION"?l("variant not selected","옵션 미확정")
   :item.reason==="PRICE"?l("variant price unknown","옵션 가격 미확인"):item.reason==="CHANGED"?l("selected option changed","선택 옵션 변경")
   :item.reason==="ALREADY"?l("already in Cart","이미 Cart에 있음"):l("unavailable or not loaded","구매 불가 또는 정보 미확인");
  return l("{title} ({reason})","{title} ({reason})",{title:item.title||l("Product","상품"),reason});
 }).join(", ");
 async function add() {
  if(lock.current||props.disabled)return;
  lock.current=true;setBusy(true);props.onBusy(true);
  let missingOption:CombinationSkip|undefined;
  try {
   {
    const items=props.representatives.map((representative,index)=>({
     ...representative,ref:"current-"+index,configurationVersion:0,
     quantity:ledger?.allocations.find(a=>a.targetId===representative.targetId)?.quantity??1,
     checkoutEligible:!props.products.get(representative.candidateId)?.source||props.products.get(representative.candidateId)?.source==="SHOPIFY",
     allocationFit:"UNKNOWN" as const,
    }));
    const selection=selectCombinationCart({items},props.products,props.configurations,props.cart);
    missingOption=selection.skipped.find(item=>item.reason==="OPTION");
    if(pending.current && JSON.stringify(pending.current.items.map(i=>[i.targetId,i.candidateId,i.variantId,i.quantity])) !== JSON.stringify(selection.items.map(i=>[i.targetId,i.candidateId,i.variantId,i.quantity]))) pending.current=undefined;
    if(selection.items.length===0){
     // Nothing new to add — already in the Cart, or bought at the external store — needs no word: the Cart says so.
     if(selection.skipped.every(item=>item.reason==="ALREADY"||item.reason==="EXTERNAL"))return;
     // A Shopify product without its option chosen: open it where the option is chosen instead of saying so (owner 2026-09-23).
     if(missingOption&&props.onChooseOption){props.onChooseOption(missingOption.candidateId,missingOption.targetId);return;}
     const needsOption=Boolean(missingOption);
     notify(needsOption?l("Please select product options","상품 옵션을 선택해 주세요"):l("No items were added","추가된 상품이 없습니다"),
      needsOption?l("No items were added. Open the Shopify product details and select an option. {items}","추가된 상품이 없습니다. Shopify 상품 상세에서 옵션을 선택해 주세요. {items}",{items:skipText(selection.skipped)})
       :selection.skipped.length?skipText(selection.skipped):l("There are no representative products yet.","아직 대표 상품이 없습니다."));
     return;
    }
    pending.current ??= {expectedVersion:props.cartVersion,items:selection.items,commandId:randomUUID()};
   }
   const command=pending.current;
   const result=await applyRepresentativeCart(props.curationId,command.expectedVersion,command.items,command.commandId);
   // Added: the Cart count on the composer changes, and that is the whole answer (owner 2026-09-23 — no notice).
   // A product left out only for its option opens next, so the combination can be finished there.
   pending.current=undefined;props.onSaved(result);
   if(missingOption)props.onChooseOption?.(missingOption.candidateId,missingOption.targetId);
  } catch(error) {
   const known=error instanceof LiveCatalogAPIError,reason=known?error.fault.reasonCode:"";
   const changed=["PHASE8_CART_VERSION_CONFLICT","COMBINATION_VARIANT_NOT_CONFIRMED","COMBINATION_VARIANT_CHANGED","COMBINATION_ITEM_NOT_OFFERED"].includes(reason);
   if(known&&(!error.fault.retryable||changed))pending.current=undefined;
   if(changed){try{props.onSaved(await loadCatalogCart(props.curationId));}catch{/* The next successful reload will update the Cart. */}}
   notify(l("Could not add the combination","조합을 추가하지 못했습니다"),
    reason==="COMBINATION_CART_LIMIT"?l("Cart can hold up to 10 items. Review the Cart and try again.","Cart에는 최대 10개 항목을 담을 수 있습니다. Cart를 확인한 뒤 다시 시도해 주세요.")
    :changed?l("The Cart or selected option changed. Check the current products and try again.","Cart 또는 선택 옵션이 변경되었습니다. 현재 상품과 옵션을 확인하고 다시 눌러 주세요.")
    :l("Check the connection and try again. Retrying will not add the same request twice.","연결 상태를 확인하고 다시 눌러 주세요. 같은 요청의 재시도로 중복 추가하지 않습니다."));
  } finally {lock.current=false;setBusy(false);props.onBusy(false);}
 }
 if(!addable)return null;
 return <>
  <Button type="button" emphasis="quiet" size="compact" className="curation-results__combination" disabled={props.disabled||busy}
   title={l("Add the representative products currently shown","현재 표시된 대표 상품을 Cart에 추가합니다")} onClick={()=>void add()}>
   {busy?l("Adding to Cart…","Cart에 추가 중…"):l("Add combination to Cart","조합을 Cart에 추가")}
  </Button>
  {createPortal(<ToastProvider key={notice?.id??0} duration={3000} swipeDirection="right" label={l("Notification","알림")}>
   {notice&&<Toast key={notice.id} className="curation-cart-toast" data-tone={notice.warning?"warning":"neutral"} open={notice.open} onOpenChange={open=>setNotice(current=>current?{...current,open}:current)}>
    <div><ToastTitle className="curation-cart-toast__title">{notice.title}</ToastTitle><ToastDescription>{notice.body}</ToastDescription></div>
    <ToastClose asChild><Button type="button" emphasis="quiet" aria-label={l("Dismiss notification","알림 닫기")}><X size={16}/></Button></ToastClose>
   </Toast>}
   <ToastViewport className="curation-cart-toast-viewport" hotkey={["F8"]}/>
  </ToastProvider>,document.body)}
 </>;
}
