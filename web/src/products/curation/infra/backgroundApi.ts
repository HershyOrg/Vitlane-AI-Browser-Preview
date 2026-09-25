import { request } from "../../../shared/api/client";
export type DealProduct = {
 schemaVersion: "vitlane.deal-product.v1"; provider:string; externalId:string; identity:string;
 productRef?:{source:string;marketplace:string;productId:string};
 title:string; description?:string;url:string;imageUrl?:string;country:string;currency:string;
 priceMinor?:number;shippingMinor?:number;observedAt:string;expiresAt:string;
};
export type ResearchSubscription = {
 id:string;curationId:string;targetId:string;status:"ACTIVE"|"EXPIRED"|"CANCELLED"|"TARGET_REMOVED"|"ARCHIVED";createdAt:string;
 terms:{keywords:string[];country:string;currency:string;maximumMinor?:number;expiresAt:string;criteria:{subject:{label:string;productType:string};axes:{axisId:string;label:string;definition:string}[];exclusions:string[]}};
};
export type ResearchFinding = {id:string;subscriptionId:string;targetId:string;status:"NEW"|"HIDDEN"|"ADDED";product:DealProduct;reason:string;createdAt:string;candidateId?:string};
export type BackgroundView = {schemaVersion:"vitlane.background-research.v1";subscriptions:ResearchSubscription[];findings:ResearchFinding[]};
export const backgroundChanged="vitlane:background-research-changed";
export const findingAdded="vitlane:research-finding-added";
export const readBackground=(id:string)=>request<BackgroundView>(`/api/v1/curations/${id}/background-research`);
export async function backgroundAction(cid:string,kind:"subscriptions"|"findings",id:string,action:"cancel"|"hide"|"candidates") {
 const result=await request(`/api/v1/curations/${cid}/${kind}/${id}/${action}`,{method:"POST",body:"{}"});
 window.dispatchEvent(new CustomEvent(backgroundChanged,{detail:cid}));
 if(action==="candidates")window.dispatchEvent(new CustomEvent(findingAdded,{detail:cid}));
 return result;
}
export const syncProductNotices=(clientId:string,curationId:string|undefined,visible:boolean)=>request<{schemaVersion:string;curationIds:string[]}>("/api/v1/curations/product-notices/sync",{method:"POST",body:JSON.stringify({schemaVersion:"vitlane.curation-notice-sync.v1",clientId,curationId:curationId??"",visible})});
