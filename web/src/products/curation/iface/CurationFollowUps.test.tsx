// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, it, expect, vi } from "vitest";
import { CurationFollowUpBubble, followUpText } from "./CurationFollowUps";
import { formatMessage } from "../../../shared/i18n";
import type { FollowUpMessage } from "../domain/types";
const proposal:FollowUpMessage={id:"proposal-1",responseId:"response",kind:"PROPOSAL",status:"PENDING",version:1,createdAt:"2026-09-12T00:00:00Z",content:{code:"LOW_AXIS_FIT",body:"현재 기준으로 다시 찾아볼까요?",locale:"ko-KR"}};
describe("follow-up message controls",()=>{
 it("accept and dismiss refer directly to this message",async()=>{
  const node=document.createElement("div");document.body.append(node);const root=createRoot(node);const respond=vi.fn();
  await act(async()=>root.render(<CurationFollowUpBubble message={proposal} onRespond={respond}/>));
  const buttons=node.querySelectorAll("button");expect(buttons).toHaveLength(2);
  const bubble=node.querySelector('.curation-conversation-bubble.is-vitlane')!;
  expect(bubble.querySelector('header strong')?.textContent).toBe('Vitlane');
  expect(bubble.querySelector('time')?.dateTime).toBe(proposal.createdAt);
  expect(bubble.querySelector('p')?.textContent).toBe(proposal.content.body);
  expect(bubble.querySelectorAll('button')).toHaveLength(2);
  expect(bubble.querySelector('p')?.nextElementSibling?.className).toBe('curation-follow-up__actions');
  await act(async()=>buttons[0].click());expect(respond).toHaveBeenCalledWith(proposal,"ACCEPT");
  await act(async()=>buttons[1].click());expect(respond).toHaveBeenCalledWith(proposal,"DISMISS");
  await act(async()=>root.unmount());node.remove();
 });
 it("history is plain text and busy submission disables every pending control",()=>{
  for(const status of ["ACCEPTED","DISMISSED","ACKNOWLEDGED","SUPERSEDED"] as const){const html=renderToStaticMarkup(<CurationFollowUpBubble message={{...proposal,status}} onRespond={()=>{}}/>);expect(html).not.toContain("<button");expect(html).toContain(proposal.content.body);}
  const html=renderToStaticMarkup(<CurationFollowUpBubble message={proposal} busy onRespond={()=>{}}/>);expect(html.match(/disabled=""/g)).toHaveLength(2);
 });
 it("renders candidate counts as Vitlane messages while retaining empty-result guidance",()=>{
  const message: FollowUpMessage={...proposal,kind:"RESULT",status:"ACKNOWLEDGED",content:{code:"RESEARCH_COMPLETED",added:9}};
  const html=renderToStaticMarkup(<CurationFollowUpBubble message={message} onRespond={()=>{}}/>);
  expect(html).toContain('curation-conversation-bubble is-vitlane');
  expect(html).toContain('현재 기준으로 새 후보 9개를 찾았어요.');
  expect(html).not.toContain('<button');
  expect(followUpText(message,(english,_korean,values)=>formatMessage(english,values))).toBe('I found 9 new candidates using the current criteria.');
  expect(renderToStaticMarkup(<CurationFollowUpBubble message={{...message,content:{...message.content,added:0}}}/>)).not.toBe("");
 });
 it("waits for availableAt before accepting a call-limit proposal while dismissing stays open",async()=>{
  vi.useFakeTimers();vi.setSystemTime(new Date("2026-09-18T00:00:00Z"));
  try{
   const limited:FollowUpMessage={...proposal,content:{code:"SOURCES_RATE_LIMITED",body:"쿠팡은 호출 한도로 이번에 확인하지 못했어요. 잠시 뒤 만년필을 다시 찾아볼까요?",locale:"ko-KR",availableAt:"2026-09-18T00:01:00Z"}};
   const html=renderToStaticMarkup(<CurationFollowUpBubble message={limited} onRespond={()=>{}}/>);
   expect(html).toContain("잠시 뒤에 수락할 수 있어요.");expect(html.match(/disabled=""/g)).toHaveLength(1);
   const node=document.createElement("div");document.body.append(node);const root=createRoot(node);const respond=vi.fn();
   await act(async()=>root.render(<CurationFollowUpBubble message={limited} onRespond={respond}/>));
   const [accept,dismiss]=Array.from(node.querySelectorAll("button"));
   expect(accept.disabled).toBe(true);expect(accept.getAttribute("aria-describedby")).toBe(`curation-follow-up-wait-${limited.id}`);expect(dismiss.disabled).toBe(false);
   await act(async()=>{vi.advanceTimersByTime(60_000);});
   expect(node.querySelector(".curation-follow-up__wait")).toBeNull();expect(accept.disabled).toBe(false);expect(accept.hasAttribute("aria-describedby")).toBe(false);
   await act(async()=>accept.click());expect(respond).toHaveBeenCalledWith(limited,"ACCEPT");
   await act(async()=>root.unmount());node.remove();
  }finally{vi.useRealTimers();}
  const past=renderToStaticMarkup(<CurationFollowUpBubble message={{...proposal,content:{...proposal.content,availableAt:"2020-01-01T00:00:00Z"}}} onRespond={()=>{}}/>);
  expect(past).not.toContain("잠시 뒤에 수락할 수 있어요.");expect(past).not.toContain('disabled=""');
 });
 it("acknowledges an error without offering acceptance",()=>{
  const html=renderToStaticMarkup(<CurationFollowUpBubble message={{...proposal,kind:"ERROR",content:{code:"RESEARCH_FAILED",targetTitle:"만년필"}}} onRespond={()=>{}}/>);
  expect(html).toContain("확인");expect(html).not.toContain("수락");expect(html.match(/<button/g)).toHaveLength(1);
 });
});
