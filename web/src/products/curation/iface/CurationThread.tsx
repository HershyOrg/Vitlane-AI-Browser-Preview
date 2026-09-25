import { combinationReplyText } from "../domain/combination";
import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import { ChevronDown } from "lucide-react";
import { Button, Chip, Input, Popover, PopoverContent, PopoverTrigger, useSwipeDismiss, type ChipTone } from "../../../shared/ui";
import { useLocale, type Localize } from "../../../shared/i18n";
import { useCurationThreads } from "../app/useThreads";
import { useConversationResults } from "../app/useConversationResults";
import { retryIntelligenceJob } from "../infra/curationApi";
import { jobReasonLabel } from "../domain/jobPresentation";
import { comparisonFacts, shortProductName } from "../domain/conversationResults";
import { ComparisonFactsLine } from "./CurationResults";
import { formatTimelineTime } from "./CurationConversationBubble";
import { type CurationThread, type ActionEffect, type ActionJob, type CurationActionExecution, type ActionDecision, type ThreadOutcome, currentAction, threadActive, threadOutcome, threadProgress, candidateSummary, failedJobs, targetLabelOf, threadResponse, researchJobs, RESPONSE_ACTION, type ThreadResponse } from "../domain/thread";
import type { FollowUpMessage } from "../domain/types";
import { CurationFollowUpBubble } from "./CurationFollowUps";
import type { ResearchCriteria } from "../domain/researchCriteria";
import type { BudgetLedger } from "../domain/budget";
import "./curation-thread.css";

/**
 * One request, one place per state. While a Thread runs, the only progress
 * surface is the bar directly above the composer (expand for the plan,
 * selection and evidence). When it ends, the conversation gets one Vitlane
 * report bubble; nothing about the request is repeated per Action.
 */
export function CurationThreadProgress() {
 const state = useCurationThreads(); const { l } = useLocale();
 if (!state) return null;
 return <>
  {state.error && <p className="curation-thread__notice" role="alert">{l("Could not refresh request status.", "요청 상태를 새로 확인하지 못했습니다.")} <Button type="button" emphasis="quiet" size="compact" onClick={() => void state.reload()}>{l("Retry", "다시 확인")}</Button></p>}
  {state.active && <ThreadBar key={state.active.id} thread={state.active} />}
 </>;
}

const kindOrder = ["ACTION", "TARGET", "CONDITIONS", "BUDGET"];
function actionLabel(a: CurationActionExecution, l: Localize) {
 const labels: Record<string, string> = { AUTO_START: l("Interpret request", "요청 해석"), BUDGET_CHANGE: l("Change budget", "예산 변경"), CRITERIA_CHANGE: l("Update criteria", "축·조건 변경"), CURATION_ADD_TARGETS: l("Add products", "상품 추가"), PLANNING_ADD_TARGETS: l("Add products", "상품 추가"), INTENT_NEXT_STEP: l("Plan products and budget", "상품과 예산 구성"), START_RESEARCH: l("Research products", "상품 조사"), PLANNING_START_CURATING: l("Research products", "상품 조사"), TARGET_RESEARCH_AGAIN: l("Research again", "재조사"), RESPONSE: l("Write response", "응답 작성") };
 return labels[a.type] ?? l("Process request", "요청 처리");
}
function progressiveLabel(a: CurationActionExecution, l: Localize) {
 const labels: Record<string, string> = { AUTO_START: l("Interpreting request", "요청 해석 중"), BUDGET_CHANGE: l("Changing budget", "예산 변경 중"), CRITERIA_CHANGE: l("Updating criteria", "축·조건 변경 중"), CURATION_ADD_TARGETS: l("Adding products", "상품 추가 중"), PLANNING_ADD_TARGETS: l("Adding products", "상품 추가 중"), INTENT_NEXT_STEP: l("Planning products and budget", "상품과 예산 구성 중"), START_RESEARCH: l("Researching products", "상품 조사 중"), PLANNING_START_CURATING: l("Researching products", "상품 조사 중"), TARGET_RESEARCH_AGAIN: l("Researching again", "재조사 중"), RESPONSE: l("Writing response", "응답 작성 중") };
 return labels[a.type] ?? l("Processing request", "요청 처리 중");
}
function actionTone(a: CurationActionExecution): ChipTone {
 if (a.status === "SUCCEEDED") return "done";
 if (a.status === "RUNNING" || a.status === "WAITING_SELECTION") return "progress";
 if (a.status === "FAILED" || a.status === "CANCELLED") return "failed";
 return "waiting";
}
function statusWord(status: string, l: Localize) {
 return status === "SUCCEEDED" ? l("Completed", "완료") : status === "FAILED" ? l("Failed", "실패") : status === "CANCELLED" ? l("Cancelled", "취소") : status === "SKIPPED" ? l("Not run", "실행 안 함") : status === "WAITING_SELECTION" ? l("Waiting for your choice", "선택 대기") : status === "PENDING" ? l("Waiting", "대기") : l("In progress", "진행 중");
}
function jobKindLabel(j: ActionJob, l: Localize) {
 return j.kind === "ACTION_INTERPRETATION" ? l("Request interpretation", "요청 해석") : j.kind === "PLANNING_TASK" ? l("Product planning", "상품 구성") : l("Product research", "상품 조사");
}
function jobLabel(t: CurationThread, j: ActionJob, l: Localize) {
 const target = j.targetLabel || targetLabelOf(t, j.targetId);
 return target || jobKindLabel(j, l);
}
// Reasons the Thread itself reports (interpretation and primitive failures).
// Job failures keep the research vocabulary of jobReasonLabel.
export function threadReasonLabel(code: string | undefined, l: Localize) {
 if (!code) return l("We couldn't process the request.", "요청을 처리하지 못했습니다.");
 if (code === "AUTO_PROVIDER_UNAVAILABLE" || code === "AUTO_INTERPRETER_UNAVAILABLE") return l("Research intelligence is unavailable.", "조사 지능을 사용할 수 없습니다.");
 if (code === "AUTO_INTERPRETATION_INTERRUPTED") return l("The request interpretation was interrupted.", "요청 해석이 중단되었습니다.");
 if (code.startsWith("AUTO_BUDGET") || code === "AUTO_EXPLICIT_BUDGET_MISSED") return l("We couldn't confirm the budget in your request. Edit budgets using the Budget button above the input.", "요청의 예산을 확인하지 못했습니다. 예산은 입력창 위 예산 버튼에서 변경해 주세요.");
 if (code.startsWith("AUTO_")) return l("We couldn't turn the request into an action.", "요청을 실행할 행동으로 해석하지 못했습니다.");
 if (code === "CURATION_PRIMITIVE_FAILED") return l("We couldn't complete the action.", "작업을 완료하지 못했습니다.");
 return jobReasonLabel(code, l);
}
function requestText(t: CurationThread, l: Localize) {
 return t.retryOfJobId ? l("Retry failed job", "실패한 작업 재시도") : t.request || (t.actions.some(a => a.type === "RESPONSE" && a.instruction === "COMBINATION") ? l("Recommend a combination", "조합 추천") : t.actions.some(a => a.type === "TARGET_RESEARCH_AGAIN") ? l("Research again", "재조사") : l("Manual setting", "수동 설정"));
}
function jobLines(t: CurationThread, a: CurationActionExecution, l: Localize) {
 return a.jobs.map(j => `${jobLabel(t, j, l)} ${statusWord(j.status, l)}${j.status === "FAILED" && j.reasonCode ? ` · ${jobReasonLabel(j.reasonCode, l)}` : ""}`);
}

function ThreadBar({ thread: t }: { thread: CurationThread }) {
 const state = useCurationThreads(); const { l, locale } = useLocale();
 const active = currentAction(t); const question = active?.question;
 const [expanded, setExpanded] = useState(() => state?.sheetOpen(t.id) ?? Boolean(question));
 const [free, setFree] = useState<string | undefined>(); const [text, setText] = useState({ question: "", value: "" }); const [error, setError] = useState(false);
 const questionId = question?.id;
 useEffect(() => { if (questionId) setExpanded(true); }, [questionId]);
 useEffect(() => { state?.rememberSheet(t.id, expanded); }, [state, t.id, expanded]);
 // Dragging the open card down folds the sheet back into the bar.
 const cardRef = useRef<HTMLElement>(null);
 useSwipeDismiss(cardRef, { direction: "down", enabled: expanded && Boolean(state), exit: "settle", onDismiss: () => setExpanded(false) });
 if (!state) return null;
 const act = async (run: () => Promise<void>) => { setError(false); try { await run(); } catch { setError(true); } };
 const { done, total } = threadProgress(t);
 const headline = t.status === "INTERPRETING" ? l("Interpreting request", "요청 해석 중") : t.status === "WAITING_SELECTION" ? l("Your choice is needed", "선택이 필요해요") : active ? `${progressiveLabel(active, l)}${total > 1 ? ` · ${done}/${total}` : ""}` : l("Processing request", "요청 처리 중");
 // Loading pulses in one place: the bar's dot while folded, the step the bar names (pending or
 // running) once the sheet is open. A choice waiting for the user is not loading and stays still.
 const loading = t.status !== "WAITING_SELECTION";
 const pulsingStepId = expanded && loading ? active?.id : undefined;
 const detail = question ? question.prompt : active && active.jobs.length > 1 ? active.jobs.map(j => `${jobLabel(t, j, l)} ${statusWord(j.status, l)}`).join(" · ") : "";
 const sheetId = `curation-thread-sheet-${t.id}`;
 return <section ref={cardRef} className="curation-thread" data-thread-id={t.id} data-thread-status={t.status} aria-label={l("Request progress", "요청 진행")}>
  <div className="curation-thread__bar">
   <Button type="button" emphasis="quiet" className="curation-thread__toggle" aria-expanded={expanded} aria-controls={sheetId} onClick={() => setExpanded(v => !v)}>
    <Chip tone={loading ? "progress" : "waiting"} className={`curation-thread__chip${loading && !pulsingStepId ? " is-loading" : ""}`} role="status">{headline}</Chip>
    {detail && <span className="curation-thread__detail">{detail}</span>}
    <ChevronDown aria-hidden="true" />
   </Button>
   <Button type="button" emphasis="quiet" size="compact" disabled={state.sending} onClick={() => void act(() => state.cancel(t))}>{l("Stop", "중단")}</Button>
  </div>
  {expanded && <div id={sheetId} className="curation-thread__sheet">
   <ol className="curation-thread__steps">{t.actions.map(a => <li key={a.id} data-action-id={a.id}>
    <Chip tone={actionTone(a)} className={`curation-thread__chip${a.id === pulsingStepId ? " is-loading" : ""}`}>{actionLabel(a, l)}{a.targetLabel ? ` · ${a.targetLabel}` : ""}</Chip>
    {a.status === "SKIPPED" ? <span className="curation-thread__sub">{statusWord(a.status, l)}</span> : a.status === "FAILED" ? <span className="curation-thread__sub">{failedJobs({ ...t, actions: [a] })[0]?.reasonCode ? jobReasonLabel(failedJobs({ ...t, actions: [a] })[0].reasonCode, l) : threadReasonLabel(a.reasonCode, l)}</span> : a.jobs.length > 1 ? <span className="curation-thread__sub">{jobLines(t, a, l).join(" · ")}</span> : null}
    {[...a.effects, ...a.jobs.flatMap(j => j.effects)].flatMap(e => effectLines(e, l, locale, t.targetLabels)).map((line, i) => <span key={i} className="curation-thread__sub">{line}</span>)}
   </li>)}</ol>
   {question && <div key={question.id} className="curation-thread__question" role="group" aria-label={question.prompt}><strong>{question.prompt}</strong>{question.options.map(o => <Button key={o.id} type="button" emphasis="secondary" disabled={state.sending} onClick={() => void act(() => state.answer(t, o.id))}>{o.label}</Button>)}<Button type="button" emphasis="quiet" disabled={state.sending} onClick={() => setFree(question.id)}>{l("Describe it yourself", "직접 설명하기")}</Button>
    {free === question.id && <form onSubmit={e => { e.preventDefault(); if (text.question === question.id && text.value.trim()) void act(() => state.answer(t, undefined, text.value.trim())); }}><label>{l("Your answer", "답변")}<Input value={text.question === question.id ? text.value : ""} maxLength={2000} disabled={state.sending} onChange={e => setText({ question: question.id, value: e.target.value })} /></label><Button type="submit" disabled={state.sending || text.question !== question.id || !text.value.trim()}>{l("Send answer", "답변 보내기")}</Button></form>}
   </div>}
   <ThreadEvidence thread={t} />
   {error && <p role="alert">{l("Could not apply your choice. Refresh the request status and try again.", "선택을 적용하지 못했습니다. 요청 상태를 확인한 뒤 다시 시도해 주세요.")}</p>}
  </div>}
 </section>;
}

// The bubble and its side already say who spoke, so it carries the words only (ADR-0086). The speaker
// stays in the accessibility tree and the time in the tooltip.
function RequestBubble({ thread: t, l, locale }: { thread: CurationThread; l: Localize; locale: string }) {
 return <div className="curation-conversation-bubble is-user curation-thread__request" title={formatTimelineTime(t.createdAt, locale as never)}><span className="vt-visually-hidden">{l("Me", "나")}</span><p>{requestText(t, l)}</p></div>;
}

/** What Vitlane said it would look for when a request split into products. It is said once and stays as said. */
function planLead(t: CurationThread, l: Localize) {
 const added = new Set(t.actions.flatMap(a => [...a.effects, ...a.jobs.flatMap(j => j.effects)]).filter(e => e.kind === "TARGET_ADDED").map(e => e.targetId ?? ""));
 if (added.size === 0) return "";
 return added.size > 1 ? l("I’ll look for these as {count} products.", "{count}가지로 나눠 찾아볼게요.", { count: added.size }) : l("I’ll look for this product.", "이 상품으로 찾아볼게요.");
}

export type TurnFollowUps = { messages: readonly FollowUpMessage[]; busy?: boolean; onRespond?: (message: FollowUpMessage, response: "ACCEPT" | "DISMISS" | "ACKNOWLEDGE") => void };

/**
 * One request, one turn (ADR-0089): the reader's words and, under them, everything Vitlane answers to
 * them, from the moment the request is sent until long after it ends. The turn never moves and its parts
 * arrive in reading order: the plan sentence, the written reply or result sentence, the product
 * groups' rows, the facts and record, then follow-up messages. Rows can appear while research
 * runs; the completed reply is placed above them. The bar above the composer reports progress.
 */
export function CurationThreadReport({ thread: t, followUps }: { thread: CurationThread; followUps?: TurnFollowUps }) {
 const state = useCurationThreads(); const results = useConversationResults(); const { l, locale } = useLocale();
 const [retrying, setRetrying] = useState(false); const [error, setError] = useState<string | undefined>();
 const active = threadActive(t);
 const outcome = threadOutcome(t);
 const summary = reportSummary(t, outcome, l);
 const response = threadResponse(t);
 // A researched product has its own row below; only other changes need a line.
 const researched = new Set(t.actions.flatMap(a => a.jobs).filter(j => j.kind === "RESEARCH_ROUND" && j.targetId).map(j => j.targetId));
 const changes = t.actions.filter(a => a.status === "SUCCEEDED").flatMap(a => [...a.effects, ...a.jobs.flatMap(j => j.effects)]).filter(e => e.kind !== "CANDIDATES_ADDED" && e.kind !== "NO_RESULTS" && !(e.kind === "TARGET_ADDED" && researched.has(e.targetId))).flatMap(e => effectLines(e, l, locale, t.targetLabels));
 const retry = (outcome === "FAILED" || outcome === "PARTIAL") ? failedJobs(t).find(j => j.kind !== "ACTION_INTERPRETATION") : undefined;
 const registerHost = results?.registerHost;
 const host = useCallback((node: HTMLDivElement | null) => registerHost?.(t.id, node), [registerHost, t.id]);
 const plain = outcome === "SUCCEEDED" || outcome === "ANSWERED" || outcome === "NO_RESULTS" || outcome === "SETTINGS_ONLY";
 const lead = planLead(t, l);
 // The written reply replaces the fixed result sentence; the rows below show what was found.
 const headline = response ? "" : summary.headline;
 // What this request's research looked at, whichever turn shows the products too.
 const facts = comparisonFacts(t, researchJobs(t).flatMap(j => j.targetId ? [j.targetId] : []));
 const messages = followUps?.messages ?? [];
 return <div className="curation-thread-report" data-thread-id={t.id} data-thread-outcome={active ? "ACTIVE" : outcome}>
  <RequestBubble thread={t} l={l} locale={locale} />
  <article className="curation-response curation-thread-report__reply" data-turn-state={active ? "active" : "done"} role={!active && (outcome === "FAILED" || outcome === "PARTIAL") ? "alert" : undefined}>
   <ResponseSpeaker />
   {lead && <p className="curation-response__text curation-response__lead">{lead}</p>}
   {!active && <>
    {headline && (plain ? <p className="curation-response__text curation-thread-report__headline">{headline}</p> : <p className="curation-thread-report__headline"><Chip tone={summary.tone} className="curation-thread__chip">{headline}</Chip></p>)}
    {response && <p className="curation-response__text" data-response-kind={response.kind}><ResponseText response={{...response, body: combinationReplyText(response)}} /></p>}
    {!response && summary.lines.length > 0 && <ul className="curation-thread-report__receipt">{summary.lines.map((line, i) => <li key={i}>{line}</li>)}</ul>}
    {changes.length > 0 && <p className="curation-response__changes">{changes.join(" · ")}</p>}
   </>}
   <div className="curation-response__results" ref={host} />
   {!active && <>
    {response && <p className="curation-response__caption">{l("Prices reflect the listings checked. Confirm availability by color and size, and discounts, when purchasing.", "가격은 조회된 판매 페이지 기준이며, 색상·사이즈별 재고와 할인은 구매 시 확인이 필요해요.")}</p>}
    {error && <p role="alert">{error}</p>}
    {/* Under the reply, on the left: what the research looked at, and under it the way into the record. */}
    <footer className="curation-response__foot">
     {facts && <ComparisonFactsLine facts={facts} />}
     <span className="curation-response__foot-actions">
      <Popover><PopoverTrigger asChild><Button type="button" emphasis="quiet" size="compact" className="curation-response__link">{l("What Vitlane did", "처리 내역")}</Button></PopoverTrigger>
       <PopoverContent align="start" side="top" collisionPadding={12} className="curation-response__record" aria-label={l("What Vitlane did", "처리 내역")}><strong>{l("What Vitlane did", "처리 내역")}</strong><time className="curation-response__record-time" dateTime={t.updatedAt}>{formatTimelineTime(t.updatedAt, locale as never)}</time><ThreadDetails thread={t} /></PopoverContent></Popover>
      {retry && state && <Button type="button" emphasis="secondary" size="compact" busy={retrying} disabled={state.busy || retrying} onClick={() => { setRetrying(true); setError(undefined); retryIntelligenceJob(retry.jobId).then(() => state.reload()).catch(() => setError(l("We couldn't retry. Check again shortly.", "다시 시도하지 못했습니다. 잠시 후 다시 확인해 주세요."))).finally(() => setRetrying(false)); }}>{l("Try again", "다시 시도")}</Button>}
     </span>
    </footer>
   </>}
   {/* A message about this request (a proposal, a failure note) arrives in its turn, not as a second reply at the end. */}
   {messages.length > 0 && <div className="curation-response__follow-ups">{messages.map(m => <CurationFollowUpBubble key={m.id} message={m} busy={followUps?.busy} onRespond={followUps?.onRespond} inTurn />)}</div>}
  </article>
 </div>;
}

/** Body text needs no sender line: only the reader's words sit in a bubble. Assistive technology still hears who speaks. */
export function ResponseSpeaker() {
 const { l } = useLocale();
 return <span className="vt-visually-hidden">{l("Vitlane", "Vitlane")}</span>;
}

/**
 * Reply text. A `[[ref]]` token is a candidate the Server resolved, shown by its saved title and opening that product.
 * A Shopify product has no saved title (the Server keeps no display facts of that catalog), so its name is the one
 * the research surface just read; until that arrives the reply says "this product" rather than nothing.
 */
export function ResponseText({ response }: { response: ThreadResponse }) {
 const results = useConversationResults(); const { l } = useLocale();
 return <>{response.body.split(/\n\s*\n/).map((paragraph, paragraphIndex) => <span className="curation-response__paragraph" key={paragraphIndex}>{paragraph.split(/(\[\[[a-z][a-z0-9]{0,7}\]\])/g).map((part, index) => {
  const reference = response.references.find(r => `[[${r.ref}]]` === part);
  if (!reference) return <Fragment key={index}>{part}</Fragment>;
  const title = reference.title || results?.titles[reference.candidateId] || l("this product", "이 상품");
  return <Button key={index} type="button" emphasis="quiet" className="curation-response__ref" aria-haspopup="dialog" title={title} aria-label={title} onClick={() => results?.openCandidate(reference.candidateId, reference.targetId)}>{shortProductName(title)}</Button>;
 })}</span>)}</>;
}

function reportSummary(t: CurationThread, outcome: ThreadOutcome, l: Localize): { tone: ChipTone; headline: string; lines: string[] } {
 const found = candidateSummary(t, l("Product", "상품")).filter(c => c.count > 0);
 const empty = candidateSummary(t, l("Product", "상품")).filter(c => c.count === 0);
 const foundSentence = found.length > 0 ? l("Found {list}.", "{list}를 찾았어요.", { list: found.map(c => l("{count} {target} candidates", "{target} 후보 {count}개", { count: c.count, target: c.label })).join(", ") }) : "";
 const emptySentence = empty.length > 0 ? l("No candidates matched: {targets}.", "조건에 맞는 상품이 없었어요: {targets}", { targets: empty.map(c => c.label).join(", ") }) : "";
 const skipped = t.actions.some(a => a.status === "SKIPPED");
 if (outcome === "CANCELLED") return { tone: "waiting", headline: l("Request stopped. Completed changes are preserved.", "요청을 중단했어요. 완료된 변경은 그대로 있어요."), lines: foundSentence ? [foundSentence] : [] };
 if (outcome === "PARTIAL") {
  const failures = failedJobs(t).map(j => `${jobLabel(t, j, l)}: ${jobReasonLabel(j.reasonCode, l)}`);
  return { tone: "failed", headline: l("Partially completed", "일부 완료"), lines: [foundSentence, emptySentence, ...failures, skipped ? l("Later steps did not run. Completed changes are preserved.", "이후 작업은 진행하지 않았어요. 완료된 변경은 유지돼요.") : l("Completed changes are preserved.", "완료된 변경은 유지돼요.")].filter(Boolean) };
 }
 if (outcome === "FAILED") {
  const failed = failedJobs(t)[0];
  const failedAction = t.actions.find(a => a.status === "FAILED");
  const reason = failed?.reasonCode ? jobReasonLabel(failed.reasonCode, l) : threadReasonLabel(failedAction?.reasonCode ?? t.reasonCode, l);
  return { tone: "failed", headline: l("The request could not be completed.", "요청을 끝내지 못했어요."), lines: [reason, l("Completed changes are preserved.", "완료된 변경은 유지돼요.")] };
 }
 if (outcome === "NO_RESULTS") return { tone: "done", headline: l("No new candidates matched this time. Your previous candidates are preserved.", "이번에는 조건에 맞는 새 후보를 찾지 못했어요. 기존 후보는 그대로 있어요."), lines: [] };
 if (outcome === "SETTINGS_ONLY") return { tone: "done", headline: l("Settings updated. No research started.", "설정을 바꿨어요. 조사는 시작하지 않았어요."), lines: [] };
 // A question whose reply could not be written still gets an honest sentence instead of silence.
 if (outcome === "ANSWERED") return { tone: "done", headline: l("I couldn't write an answer just now. Ask again in a moment, or tell me what to research.", "지금은 답을 만들지 못했어요. 잠시 뒤 다시 물어보거나, 찾고 싶은 내용을 알려 주세요."), lines: [] };
 return { tone: "done", headline: foundSentence || l("Request completed.", "요청을 완료했어요."), lines: emptySentence ? [emptySentence] : [] };
}

function ThreadEvidence({ thread: t }: { thread: CurationThread }) {
 const { l } = useLocale(); const [open, setOpen] = useState(false);
 const decided = t.actions.filter(a => a.decisions.length > 0);
 if (decided.length === 0) return null;
 const id = `curation-thread-evidence-${t.id}`;
 return <>
  <Button type="button" emphasis="quiet" size="compact" className="curation-thread__evidence-toggle" aria-expanded={open} aria-controls={id} onClick={() => setOpen(v => !v)}>{l("Evidence", "근거")}<ChevronDown aria-hidden="true" /></Button>
  {open && <div id={id} className="curation-thread__evidence">{decided.map(a => <div key={a.id}>{decided.length > 1 && <span className="curation-thread__sub">{actionLabel(a, l)}</span>}<DecisionList decisions={a.decisions} /></div>)}</div>}
 </>;
}
// Details read Action by Action: what it was, how it ended, which Jobs ran
// and the decisions (with their source and quoted evidence) it rested on.
function ThreadDetails({ thread: t }: { thread: CurationThread }) {
 const { l } = useLocale();
 return <ol className="curation-thread__jobs">{t.actions.map(a => <li key={a.id} data-action-id={a.id}>
  <div className="curation-thread__record">
   <Chip tone={actionTone(a)} className="curation-thread__chip">{actionLabel(a, l)}{a.targetLabel ? ` · ${a.targetLabel}` : ""}</Chip>
   <span className="curation-thread__sub">{a.type === RESPONSE_ACTION && a.reasonCode === "RESPONSE_UNAVAILABLE" ? l("Finished without a written reply", "응답 글 없이 마침") : statusWord(a.status, l)}{a.status === "FAILED" && a.jobs.every(j => j.status !== "FAILED") ? ` · ${threadReasonLabel(a.reasonCode, l)}` : ""}</span>
   {a.jobs.map(j => <span key={j.jobId} className="curation-thread__job">{a.type === RESPONSE_ACTION ? l("Response writing", "응답 작성") : jobKindLabel(j, l)}{j.targetLabel || targetLabelOf(t, j.targetId) ? ` · ${j.targetLabel || targetLabelOf(t, j.targetId)}` : ""} · {statusWord(j.status, l)}{j.status === "FAILED" && j.reasonCode ? ` · ${jobReasonLabel(j.reasonCode, l)}` : ""}</span>)}
  </div>
  {a.decisions.length > 0 && <DecisionList decisions={a.decisions} />}
 </li>)}</ol>;
}
function DecisionList({ decisions }: { decisions: ActionDecision[] }) {
 const { l } = useLocale();
 return <ul className="curation-thread__decisions">{[...decisions].sort((a, b) => kindOrder.indexOf(a.kind) - kindOrder.indexOf(b.kind)).map((d, i) => <li key={d.id || i}><span>{({ ACTION: l("Action", "행동"), TARGET: l("Target", "대상"), CONDITIONS: l("Conditions", "조건"), BUDGET: l("Budget", "예산") })[d.kind]}: {d.targetLabel || decisionLabel(d.result, l)}</span><small>{d.source === "MANAGED" ? l("AI decision", "AI 판단") : d.source === "MANUAL" || d.source === "USER_SELECTION" ? l("Your choice", "사용자 선택") : l("Server rule", "서버 규칙")}</small>{d.evidence && <q>{d.evidence}</q>}</li>)}</ul>;
}
function decisionLabel(value: string, l: Localize) { const labels: Record<string, string> = { INITIALIZE: l("Initial configuration", "초기 구성"), CRITERIA: l("Edit criteria", "기준 편집"), BUDGET: l("Edit budget", "예산 편집"), KEEP: l("Keep current settings", "현재 설정 유지"), DEFERRED: l("Resolved in the next action", "후속 행동에서 결정"), NEEDS_SELECTION: l("Ask for a choice", "선택 요청"), SET_TOTAL: l("Set total budget", "총예산 설정"), ENABLE: l("Set budget", "예산 설정"), DISABLE: l("No limit", "제한 없음"), SET_TARGET: l("Set target allocation", "대상 배분 변경"), SET_QUANTITY: l("Change quantity", "수량 변경"), SET_MINIMUM: l("Change minimum", "하한 변경"), SET_CRITERIA: l("Update criteria", "조건 반영"), ADD_TARGET: l("Add product", "상품 추가"), RESEARCH_AGAIN: l("Research again", "재조사"), RETRY: l("Retry failed job", "실패한 작업 재시도"), SETTINGS_ONLY: l("Change settings", "설정 변경"), COMMAND_SCOPE: l("Selected setting", "선택한 설정"), NEW_TARGETS: l("New products", "새 상품") }; return labels[value] ?? l("Selected option", "선택한 항목"); }

// One receipt line per committed change. Callers decide how to join them.
export function effectLines(e: ActionEffect, l: Localize, locale: string, labels: Record<string, string> = {}): string[] {
 if (e.kind === "BUDGET_CHANGED") { const before = e.before as BudgetLedger | undefined; const after = e.after as BudgetLedger | undefined; const money = (b?: BudgetLedger) => !b?.enabled || b.totalAmount === null ? l("No limit", "제한 없음") : new Intl.NumberFormat(locale, { style: "currency", currency: b.currency }).format(Number(b.totalAmount));
  // An unchanged total (a new target redistributing the same budget) is not a change worth a line.
  const changes = money(before) === money(after) && before?.currency === after?.currency ? [] : [l("Budget: {before} → {after}", "예산: {before} → {after}", { before: money(before), after: money(after) })];
  for (const a of after?.allocations ?? []) { const b = before?.allocations.find(v => v.targetId === a.targetId); const target = labels[a.targetId] ?? e.targetLabel ?? l("Product", "상품"); const amount = (value: string | null | undefined, currency: string) => value == null ? l("No limit", "제한 없음") : new Intl.NumberFormat(locale, {style: "currency", currency}).format(Number(value));
   if (a.amount !== b?.amount || before?.currency !== after?.currency) changes.push(l("{target}: {before} → {after}", "{target}: {before} → {after}", {target, before: amount(b?.amount, before?.currency ?? after!.currency), after: amount(a.amount, after!.currency)}));
   if (b && a.quantity !== b.quantity) changes.push(l("{target} quantity: {before} → {after}", "{target} 수량: {before} → {after}", {target, before: b.quantity, after: a.quantity}));
   if (a.minimumUnitAmount !== b?.minimumUnitAmount) changes.push(l("{target} minimum: {amount}", "{target} 하한: {amount}", {target, amount: amount(a.minimumUnitAmount, after!.currency)}));
  } return changes.length > 0 ? changes : [l("Budget kept: {amount}", "예산 유지: {amount}", { amount: money(after) })]; }
 if (e.kind === "CRITERIA_CHANGED") {
  const before=e.before as ResearchCriteria | undefined, after=e.after as ResearchCriteria | undefined;
  const changes=[l("Updated criteria for {target}.", "{target}의 조건을 변경했습니다.", {target:e.targetLabel ?? labels[e.targetId ?? ""] ?? l("Selected product","선택한 상품")})];
  for(const axis of after?.axes ?? []) { const previous=before?.axes.find(a=>a.axisId===axis.axisId);
   if(!previous) changes.push(l("Added {axis}","{axis} 추가",{axis:axis.label}));
   else if(previous.importance!==axis.importance) changes.push(l("{axis} importance: {before} → {after}","{axis} 중요도: {before} → {after}",{axis:axis.label,before:previous.importance,after:axis.importance}));
  }
  for(const axis of before?.axes ?? []) if(!after?.axes.some(a=>a.axisId===axis.axisId)) changes.push(l("Removed {axis}","{axis} 삭제",{axis:axis.label}));
  for(const item of after?.exclusions ?? []) if(!before?.exclusions.includes(item)) changes.push(l("Excluded {item}","{item} 제외",{item}));
  for(const item of before?.exclusions ?? []) if(!after?.exclusions.includes(item)) changes.push(l("No longer exclude {item}","{item} 제외 해제",{item}));
  return changes;
 }
 if (e.kind === "TARGET_ADDED") return [l("Added {target}", "{target} 추가", { target: (e.targetLabel ?? labels[e.targetId ?? ""]) ?? l("Product", "상품") })];
 if (e.kind === "CANDIDATES_ADDED") return [l("{target}: added {count} candidates", "{target}: 후보 {count}개 추가", { target: (e.targetLabel ?? labels[e.targetId ?? ""]) ?? l("Selected product", "선택한 상품"), count: e.count ?? 0 })];
 if (e.kind === "NO_RESULTS") return [l("{target}: no new candidates", "{target}: 새 후보 없음", { target: (e.targetLabel ?? labels[e.targetId ?? ""]) ?? l("Selected product", "선택한 상품") })];
 return [l("Change applied", "변경 반영됨")];
}
export function effectText(e: ActionEffect, l: Localize, locale: string, labels: Record<string, string> = {}) {
 return effectLines(e, l, locale, labels).join(" · ");
}
