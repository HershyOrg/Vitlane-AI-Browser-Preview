import {useEffect,useRef,useState} from "react";
import {randomUUID} from "../../../shared/browser/randomUUID";
import {backgroundChanged,syncProductNotices} from "../infra/backgroundApi";
// Separate from the sidebar list: only the boolean notice projection is polled.
export function useProductNotices(userId:string|undefined,curationId:string|undefined){
 const client=useRef(randomUUID());
 const [ids,setIds]=useState<string[]>([]);
 const queue=useRef(Promise.resolve());
 useEffect(()=>{
  setIds([]);
  if(!userId)return;
  let alive=true;
  // Serialize this tab's visibility changes: a slow old request cannot restore
  // a lease for a curation the reader already left.

  const sync=()=>{
   const visible=document.visibilityState==="visible";
   queue.current=queue.current.catch(()=>{}).then(async()=>{
    if(!alive)return;
    const result=await syncProductNotices(client.current,curationId,visible);
    if(alive&&Array.isArray(result?.curationIds)){setIds(result.curationIds);window.dispatchEvent(new CustomEvent(backgroundChanged,{detail:curationId}));}
   }).catch(()=>{});
  };
  sync();
  const timer=window.setInterval(()=>{if(document.visibilityState==="visible")sync();},60000);
  document.addEventListener("visibilitychange",sync);
  return ()=>{
   alive=false;window.clearInterval(timer);document.removeEventListener("visibilitychange",sync);
   // A lease expires after 90 seconds if a close/navigation cannot deliver.
   queue.current=queue.current.catch(()=>{}).then(async()=>{await syncProductNotices(client.current,undefined,false);}).catch(()=>{});
  };
 },[userId,curationId]);
 return new Set(ids.filter(id=>id!==curationId));
}
