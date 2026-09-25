import { describe, expect, it } from "vitest";
import { selectCombinationCart, type CombinationConfiguration } from "./combinationCart";
import type { Combination } from "./combination";
import type { LiveCartItem, LiveCatalogProduct } from "../research/infra/liveCatalogReviewApi";

export const product: LiveCatalogProduct = {candidateId:"pen", title:"Pen", source:"SHOPIFY", description:"", currency:"USD", categories:[], features:[], specifications:[], hydration:{status:"READY",retryable:false}};
export const configuration: CombinationConfiguration = {version:2, observedAt:"2026-09-22T00:00:00Z", variant:{variantId:"fine",title:"Fine",priceMinor:2500,currency:"USD",available:true,selectedOptions:[{name:"Nib",value:"Fine"}]}};
export const plan: Combination = {schemaVersion:"vitlane.combination.v1",id:"pick",kind:"PICKS",items:[{ref:"c1",targetId:"writing",candidateId:"pen",variantId:"fine",configurationVersion:2,quantity:2,checkoutEligible:true,allocationFit:"WITHIN"}],currency:"USD",minimumMinor:5000,maximumMinor:5000,budgetMinor:10000,differenceMinor:5000,budgetStatus:"WITHIN",estimated:false,priceBasis:"EXACT",missingTargetIds:[],sourceState:"state",budgetVersion:1,cartVersion:0,criteriaVersions:{writing:1},compatibility:"UNVERIFIED",reasons:[],tips:[],cautions:[],budgetAdvice:""};
function select(p=product,c:CombinationConfiguration|undefined=configuration,cart:LiveCartItem[]=[]){
 return selectCombinationCart(plan,new Map([[p.candidateId,p]]),c?{pen:c}:{},cart);
}
describe("combination Cart eligibility",()=>{
 it("uses the saved variant price, selected options and recommendation quantity",()=>{
  const result=select();
  expect(result.skipped).toEqual([]);
  expect(result.items[0]).toMatchObject({candidateId:"pen",variantId:"fine",previewPriceMinor:2500,previewCurrency:"USD",quantity:2,selectedOptions:["Nib: Fine"]});
 });
 it("skips external products even if they have an amount and variant",()=>expect(select({...product,source:"AMAZON"}).skipped[0].reason).toBe("EXTERNAL"));
 it("does not use a preview variant as a confirmed configuration",()=>{
  const result=selectCombinationCart(plan,new Map([["pen",{...product,previewVariant:{id:"fine",title:"Fine",priceMinor:2500,currency:"USD"}}]]),{},[]);
  expect(result.items).toEqual([]);expect(result.skipped[0].reason).toBe("OPTION");
 });
 it.each([undefined,NaN,-1,1.5])("rejects unknown/invalid variant price %s",amount=>{
  expect(select(product,{...configuration,variant:{...configuration.variant,priceMinor:amount as number}}).skipped[0].reason).toBe("PRICE");
 });
 it("rejects an explicitly unknown zero price",()=>expect(select(product,{...configuration,variant:{...configuration.variant,priceMinor:0,priceUnknown:true}}).skipped[0].reason).toBe("PRICE"));
 it("allows a known zero price",()=>expect(select(product,{...configuration,variant:{...configuration.variant,priceMinor:0}}).items).toHaveLength(1));
 it("skips an unavailable variant",()=>expect(select(product,{...configuration,variant:{...configuration.variant,available:false}}).skipped[0].reason).toBe("UNAVAILABLE"));
 it("requires the saved option version and ID used by the recommendation",()=>{
  expect(select(product,{...configuration,version:3}).skipped[0].reason).toBe("CHANGED");
  expect(select(product,{...configuration,variant:{...configuration.variant,variantId:"medium"}}).skipped[0].reason).toBe("CHANGED");
 });
 it("does not increase an existing matching variant quantity",()=>{
  const cart=select().items;cart[0].quantity=4;
  const result=select(product,configuration,cart);
  expect(result.items).toEqual([]);expect(result.skipped[0].reason).toBe("ALREADY");expect(cart[0].quantity).toBe(4);
 });
 it("selects only eligible members of a mixed combination",()=>{
  const mixed={...plan,items:[...plan.items,{...plan.items[0],candidateId:"external",checkoutEligible:false},{...plan.items[0],candidateId:"unselected",variantId:undefined,configurationVersion:0}]};
  const result=selectCombinationCart(mixed,new Map([["pen",product],["external",{...product,candidateId:"external",source:"COUPANG"}],["unselected",{...product,candidateId:"unselected"}]]),{pen:configuration},[]);
  expect(result.items).toHaveLength(1);expect(result.skipped.map(s=>s.reason)).toEqual(["EXTERNAL","OPTION"]);
 });
});
