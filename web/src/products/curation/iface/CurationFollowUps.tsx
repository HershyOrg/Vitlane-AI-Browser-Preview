import { createContext, useContext, useEffect, useState } from "react";
import { Button } from "../../../shared/ui";
import { useLocale, type Localize } from "../../../shared/i18n";
import type { FollowUpMessage } from "../domain/types";
import type { ConversationMessage } from "../domain/conversation";
import { CurationConversationBubble } from "./CurationConversationBubble";

export const ConversationActivity = createContext({busy:false, setBusy: (_busy: boolean) => {}});
export const useConversationActivity = () => useContext(ConversationActivity);
export function followUpText(message: FollowUpMessage, l: Localize): string {
 const c=message.content;
 if(c.body) return c.body;
 switch(c.code) {
 case "RESEARCH_COMPLETED": return (c.added ?? 0) > 0 ? l("I found {count} new candidates using the current criteria.","현재 기준으로 새 후보 {count}개를 찾았어요.",{count:c.added ?? 0}) : l("No new candidates matched this time. Your previous candidates are preserved.","이번에는 조건에 맞는 새 후보를 찾지 못했어요. 기존 후보는 그대로 있어요.");
 case "RESEARCH_CANCELLED": return l("Research stopped. Completed results are preserved.","조사를 중단했어요. 완료된 결과는 그대로 있어요.");
 case "RESEARCH_PARTIAL_FAILURE": return l("Research ended with an error. {count} new candidates were saved.","조사 중 오류가 발생했어요. 새 후보 {count}개는 저장했어요.",{count:c.added ?? 0});
 case "RESEARCH_SOURCE_INCOMPLETE": return l("Some sources for {target} could not finish. The available candidates are preserved.","{target}의 일부 조사 경로를 완료하지 못했어요. 확보한 후보는 그대로 있어요.",{target:c.targetTitle || l("this product","이 상품")});
 case "RESEARCH_FAILED": return l("Research for {target} could not finish. You can select Retry in the composer when it is available.","{target} 조사를 완료하지 못했어요. 재시도 가능한 작업은 컴포저에서 선택할 수 있어요.",{target:c.targetTitle || l("this request","이 요청")});
 case "NO_ACTION": return l("No research started. Tell me what you would like to find next.","조사를 시작하지 않았어요. 다음에 찾을 내용을 알려주세요.");
 case "BUDGET_SETTINGS_ONLY": return l("Edit budgets using the Budget control above the composer.","예산은 컴포저 위 예산 설정에서 변경해 주세요.");
 default: return l("Choose an action and product in the composer, then send your request.","컴포저에서 행동과 상품을 선택한 뒤 요청을 보내주세요.");
 }
}
// A proposal whose acceptance helps only later (a call limit clearing) carries the
// Server's availableAt. Accepting waits for it; dismissing never does.
function useWaiting(availableAt?: string) {
 const until=availableAt ? Date.parse(availableAt) : Number.NaN;
 const [now,setNow]=useState(()=>Date.now());
 useEffect(()=>{
  const remaining=until-Date.now();
  if(!(remaining>0))return;
  const timer=setTimeout(()=>setNow(Date.now()),remaining);
  return ()=>clearTimeout(timer);
 },[until]);
 return until>Math.max(now,Date.now());
}
// What became of a message once it no longer waits. Inside a request's turn it takes the place of the
// buttons it had, so the turn keeps its shape (ADR-0089).
function settledLabel(message: FollowUpMessage, l: Localize) {
 switch(message.status) {
 case "ACCEPTED": return l("Accepted","수락했어요");
 case "DISMISSED": return l("Dismissed","취소했어요");
 case "ACKNOWLEDGED": return l("Acknowledged","확인했어요");
 case "SUPERSEDED": return l("Replaced by your next request","다음 요청으로 넘어갔어요");
 default: return "";
 }
}
export function CurationFollowUpBubble({message,busy,onRespond,inTurn}: {message:FollowUpMessage;busy?:boolean;onRespond?:(message:FollowUpMessage,response:"ACCEPT"|"DISMISS"|"ACKNOWLEDGE")=>void;inTurn?:boolean}) {
 const {l,locale}=useLocale(); const pending=message.status==="PENDING" && message.kind!=="RESULT";
 const settled=inTurn && !pending ? settledLabel(message,l) : "";
 const waiting=useWaiting(pending && message.kind==="PROPOSAL" ? message.content.availableAt : undefined); const waitId=`curation-follow-up-wait-${message.id}`;
 const conversationMessage:ConversationMessage={id:message.id,role:"VITLANE",title:l("Vitlane","Vitlane"),body:followUpText(message,l),createdAt:message.createdAt};
 return <article className="curation-follow-up" data-message-id={message.id} data-message-status={message.status} data-in-turn={inTurn || undefined} aria-label={message.kind==="PROPOSAL" ? l("Follow-up proposal","후속 제의") : l("Vitlane message","Vitlane 메시지")}>
  <CurationConversationBubble
   message={conversationMessage}
   locale={locale}
  >
  {pending && onRespond && <div className="curation-follow-up__actions">
   {message.kind==="PROPOSAL" ? <>{waiting && <span id={waitId} className="curation-follow-up__wait">{l("You can accept this in a moment.","잠시 뒤에 수락할 수 있어요.")}</span>}<Button type="button" size="compact" emphasis="primary" disabled={busy || waiting} aria-describedby={waiting ? waitId : undefined} onClick={()=>onRespond(message,"ACCEPT")}>{l("Accept","수락")}</Button><Button type="button" size="compact" emphasis="secondary" disabled={busy} onClick={()=>onRespond(message,"DISMISS")}>{l("Dismiss","취소")}</Button></> : <Button type="button" size="compact" emphasis="secondary" disabled={busy} onClick={()=>onRespond(message,"ACKNOWLEDGE")}>{l("OK","확인")}</Button>}
  </div>}
  {settled && <div className="curation-follow-up__actions"><span className="curation-follow-up__status">{settled}</span></div>}
  </CurationConversationBubble>
 </article>;
}

export function conversationError(caught: unknown,l:Localize): string | undefined {
 const e=caught as {reasonCode?:string;code?:string};const code=e?.reasonCode ?? e?.code;
 if(!code)return undefined;
 if(code.startsWith("FOLLOW_UP_"))return l("This message can no longer be acted on. The latest conversation has been refreshed.","이 메시지는 더 이상 처리할 수 없어요. 최신 대화를 확인해 주세요.");
 if(code.includes("IDEMPOTENCY_CONFLICT") || code==="CONVERSATION_VERSION_CONFLICT")return l("Another request changed this conversation. Review the latest state and try again.","다른 요청으로 대화가 바뀌었어요. 최신 상태를 확인하고 다시 요청해 주세요.");
 if(code==="CONVERSATION_REQUEST_UNAVAILABLE" || code==="CONVERSATION_REQUEST_INVALID")return l("This request cannot run with the selected action and product. Check your selection and try again.","선택한 행동과 상품으로 요청을 실행할 수 없어요. 선택을 확인한 뒤 다시 요청해 주세요.");
 if(code==="RESEARCH_IN_PROGRESS")return l("This product is being researched. Changes to it wait until the research finishes or is cancelled.","이 상품은 조사 중이에요. 조사가 끝나거나 취소된 뒤에 바꿀 수 있어요.");
 return undefined;
}
