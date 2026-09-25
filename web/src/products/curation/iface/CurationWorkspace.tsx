import { discoveryResponses, useBackgroundResearch, type DiscoveryResponse } from "../app/useBackgroundResearch";
import type { ResearchFinding } from "../infra/backgroundApi";
import { BackgroundDiscoveryResponse, CurationBackground } from "./CurationBackground";
import { CurationFollowUpBubble } from "./CurationFollowUps";
import { CurationConversationBubble, formatTimelineTime } from "./CurationConversationBubble";
import type { FollowUpMessage } from "../domain/types";
import { useCurationThreads } from "../app/useThreads";
import type { CurationThread } from "../domain/thread";
import { CurationThreadReport, type TurnFollowUps } from "./CurationThread";
import {
  type ReactNode,
  useEffect,
 useState,
  useLayoutEffect,
  useRef,
} from "react";
import { Button } from "../../../shared/ui";
import type {
  CurationArtifact,
  CurationPendingTranscriptAction,
  CurationTimelineEntry,
  CurationWorkspaceModel,
} from "../domain/types";
import type { ConversationMessage } from "../domain/conversation";
import "./curation-workspace.css";
import {
  invariantContent,
  useLocale,
  type Localize,
  type UILocale,
} from "../../../shared/i18n";
import { CurationComposerHostContext } from "./CurationComposerDock";

type ArtifactRenderContext = {
  curationId: string;
  isLatest: boolean;
  conversationTail?: ReactNode;
};

type TimelineActionEntry = Extract<CurationTimelineEntry, { kind: "ACTION" }>;

type ConversationRenderItem =
 | {kind:"DISCOVERY"; response:DiscoveryResponse; createdAt:string}
 | {kind:"FOLLOW_UP"; message:FollowUpMessage; createdAt:string}
  | { kind: "TIMELINE"; entry: CurationTimelineEntry; createdAt: string }
  | { kind: "MESSAGE"; message: ConversationMessage; createdAt: string }
  // A request Thread renders as the user's request plus one Vitlane report,
  // at the moment the request was made.
  | { kind: "THREAD"; thread: CurationThread; createdAt: string };

type Props = {
  workspace?: CurationWorkspaceModel | null;
 onFollowUpResponse?: (message:FollowUpMessage,response:"ACCEPT"|"DISMISS"|"ACKNOWLEDGE")=>void;
  loading?: boolean;
  error?: string | null;
  working?: boolean;
  pendingTranscriptAction?: CurationPendingTranscriptAction;
  conversationMessages?: readonly ConversationMessage[];
  onRetry?: () => void;
  renderArtifact?: (
    artifact: CurationArtifact,
    context: ArtifactRenderContext,
  ) => ReactNode;
  // The external-effect surface for the work an action just created stays at
  // the live tail, beside the contextual controls that can act on that result.
  agentWork?: ReactNode;
  // Set only by the composition that renders progress for this exact action.
  displayedActivityActionId?: string;
};

export function CurationWorkspace({
 onFollowUpResponse,  workspace,
  loading = false,
  error,
  working = false,
  pendingTranscriptAction,
  conversationMessages = [],
  onRetry,
  renderArtifact,
  agentWork,
  displayedActivityActionId,
}: Props) {
  if (loading) return <WorkspaceLoading />;
  if (error) return <WorkspaceError message={error} onRetry={onRetry} />;
  if (!workspace) return <WorkspaceEmpty />;

  return (
    <WorkspaceReady
      key={workspace.curation.id}
      onFollowUpResponse={onFollowUpResponse}
 workspace={workspace}
      working={working}
      pendingTranscriptAction={pendingTranscriptAction}
      conversationMessages={conversationMessages}
      renderArtifact={renderArtifact}
      agentWork={agentWork}
      displayedActivityActionId={displayedActivityActionId}
    />
  );
}

function WorkspaceReady({
 onFollowUpResponse,  workspace,
  working,
  pendingTranscriptAction,
  conversationMessages,
  renderArtifact,
  agentWork,
  displayedActivityActionId,
}: {
  onFollowUpResponse?: Props["onFollowUpResponse"];
 workspace: CurationWorkspaceModel;
  working: boolean;
  pendingTranscriptAction?: CurationPendingTranscriptAction;
  conversationMessages: readonly ConversationMessage[];
  renderArtifact?: Props["renderArtifact"];
  agentWork?: ReactNode;
  displayedActivityActionId?: string;
}) {
  const [composerHost, setComposerHost] = useState<HTMLDivElement | null>(null);
  const { l, locale } = useLocale();
  const threads = useCurationThreads();
  const background = useBackgroundResearch(workspace.curation.id, workspace.conversation?.messages.map(m => m.id + ":" + m.status).join("|"));
  const discoveries = discoveryResponses(background.view);
  const [finding, setFinding] = useState<ResearchFinding | null>(null);
  const coveredActions = new Set(threads?.threads.flatMap(t => t.actions.flatMap(s => [s.id, ...s.jobs.map(j => j.actionId)])) ?? []);
  const coveredRequests = new Set(threads?.threads.map(t => t.id) ?? []);
  const activeThread = threads?.active;
  // A request is one turn from the moment it is sent (ADR-0089): running or finished, it keeps its place in time.
  const threadItems: ConversationRenderItem[] = (threads?.threads ?? []).map(t => ({ kind: "THREAD" as const, thread: t, createdAt: t.createdAt }));
  // Messages about a request (proposals, failure notes) belong to its turn; its result message only repeats the turn.
  const turnMessages = new Map<string, FollowUpMessage[]>();
  for (const message of workspace.conversation?.messages ?? []) {
    if (!coveredRequests.has(message.responseId) || message.kind === "RESULT") continue;
    turnMessages.set(message.responseId, [...(turnMessages.get(message.responseId) ?? []), message]);
  }
  const followUpBusy = working || threads?.busy || Boolean(workspace.activeWork);
  const turnFollowUps = (thread: CurationThread): TurnFollowUps | undefined => {
    const messages = turnMessages.get(thread.id);
    return messages ? { messages, busy: followUpBusy, onRespond: onFollowUpResponse } : undefined;
  };
  const timeline = workspace.timeline.filter(entry => (entry.kind === "RESULT" && Boolean(entry.artifact)) || !coveredActions.has(entry.kind === "ACTION" ? entry.action?.id ?? entry.id : entry.id.replace(/:result$/, "")));
  const activeWork =
    workspace.activeWork?.status === "QUEUED" ||
    workspace.activeWork?.status === "RUNNING";
  const {
    entries: visibleTimeline,
    pendingEntryID,
  } = orderTranscriptForPendingAction(
    timeline,
    activeWork,
    pendingTranscriptAction,
  );
  const artifactEntryIDs = workspace.timeline.flatMap((entry) =>
    entry.kind === "RESULT" && entry.artifact ? [entry.id] : [],
  );
  const latestArtifactEntryID = artifactEntryIDs.at(-1);
  const latestArtifactEntry = visibleTimeline.find(
    (entry) => entry.id === latestArtifactEntryID,
  );
  const curatingConversation =
    workspace.curation.phase === "CURATING" && Boolean(latestArtifactEntry);
  const activeActionEntry = curatingConversation && pendingEntryID
    ? visibleTimeline.find(
        (entry): entry is TimelineActionEntry =>
          entry.kind === "ACTION" && entry.id === pendingEntryID,
      )
    : undefined;
  const activeMessages = curatingConversation
    ? conversationMessages.filter((message) => message.state === "ACTIVE")
    : [];
  const visibleActiveMessages = activeActionEntry
    ? activeMessages.filter(
        (message) =>
          message.role !== "USER" || !message.reconcilesServerAction,
      )
    : activeMessages;
  const historicalMessages = curatingConversation
    ? conversationMessages.filter((message) => message.state !== "ACTIVE")
    : [];
  const historicalTimeline = curatingConversation
    ? visibleTimeline.filter(
        (entry) =>
          entry.id !== latestArtifactEntryID &&
          entry.id !== activeActionEntry?.id,
      )
    : [];
  const renderedConversation: ConversationRenderItem[] = curatingConversation
    ? [
        ...(workspace.conversation?.messages.filter(m=>m.status!=="PENDING" && !coveredRequests.has(m.responseId)) ?? []).map(message=>({kind:"FOLLOW_UP" as const,message,createdAt:message.createdAt})),
        ...(workspace.conversation?.requests.filter(r=>r.body && !coveredRequests.has(r.id) && !workspace.timeline.some(t=>t.kind==="ACTION" && t.action?.id===r.actionId)) ?? []).map(r=>({kind:"MESSAGE" as const,message:{id:r.id,role:"USER" as const,title:l("Me","나"),body:r.body,createdAt:r.createdAt},createdAt:r.createdAt})),
        ...historicalTimeline.map((entry) => ({
          kind: "TIMELINE" as const,
          entry,
          createdAt: entry.createdAt,
        })),
        ...historicalMessages.map((message) => ({
          kind: "MESSAGE" as const,
          message,
          createdAt: message.createdAt,
        })),
        ...threadItems,
        ...discoveries.map(response => ({kind: "DISCOVERY" as const, response, createdAt: response.createdAt})),
      ].sort((left, right) => Date.parse(left.createdAt) - Date.parse(right.createdAt))
    : orderPlanningTranscript(visibleTimeline, threadItems, pendingEntryID, latestArtifactEntryID);
  if (curatingConversation && latestArtifactEntry) {
    renderedConversation.push({
      kind: "TIMELINE",
      entry: latestArtifactEntry,
      createdAt: latestArtifactEntry.createdAt,
    });
  }
  // Only a waiting message that answers no request stays at the tail; the others wait in their turn.
  const pendingFollowUps = workspace.conversation?.messages.filter(m=>m.status==="PENDING" && !coveredRequests.has(m.responseId)) ?? [];
 const conversationTail = activeActionEntry || visibleActiveMessages.length > 0 || pendingFollowUps.length > 0 ? (
    <div className="curation-live-conversation-turn">
 {pendingFollowUps.map(m=><CurationFollowUpBubble key={m.id} message={m} busy={followUpBusy} onRespond={onFollowUpResponse} />)}
      {activeActionEntry ? (
        <CurationUserBubble
          entry={activeActionEntry}
          pending
          showPendingStatus={!displayedActivityActionId || activeActionEntry.action?.id !== displayedActivityActionId}
          locale={locale}
          l={l}
        />
      ) : null}
      {visibleActiveMessages.map((message) => (
        <CurationConversationBubble
          key={message.id}
          message={message}
          locale={locale}
        />
      ))}
    </div>
  ) : undefined;
  const [unseen,setUnseen]=useState(false);
 const initialTailRef = useRef<HTMLDivElement>(null);
  const workspaceMainRef = useRef<HTMLElement>(null);
  const followsTailRef = useRef(true);
  const ownScrollTopRef = useRef<number | undefined>(undefined);

  const liveConversationKey = [
    ...discoveries.map(r => `discovery:${r.id}:${r.findings.length}`),
    activeActionEntry ? `action:${activeActionEntry.id}` : "",
    activeThread ? `thread:${activeThread.id}:${activeThread.status}` : "",
    ...visibleActiveMessages.map((message) => `message:${message.id}`),
 ...pendingFollowUps.map(m=>`follow-up:${m.id}:${m.status}`),
    activeWork ? `work:${workspace.activeWork?.workTargetId ?? "unknown"}` : "",
  ].filter(Boolean).join("|") || "idle";
  const previousLiveConversationKeyRef = useRef(liveConversationKey);

  function followTail() {
    const tail = initialTailRef.current;
    const scroller = tail?.closest<HTMLElement>(".shell-product-body");
    if (!scroller) return;
    scroller.scrollTop = scroller.scrollHeight;
    ownScrollTopRef.current = scroller.scrollTop;
    followsTailRef.current = true;
 setUnseen(false);
  }

  useLayoutEffect(() => {
    followTail();
  }, [workspace.curation.id]);

  useEffect(() => {
    const tail = initialTailRef.current;
    const scroller = tail?.closest<HTMLElement>(".shell-product-body");
    if (!scroller) return;
    const observePosition = () => {
      // Our own scroll to the tail also reports here, sometimes after the page has grown again
      // (a response receives its results a moment after it mounts). Growth is not the reader
      // leaving the tail, so only a scroll that moved away from where we put it can stop following.
      if (ownScrollTopRef.current !== undefined && Math.abs(scroller.scrollTop - ownScrollTopRef.current) <= 1) return;
      ownScrollTopRef.current = undefined;
      followsTailRef.current =
        scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop <= 96;
 if(followsTailRef.current)setUnseen(false);
    };
    observePosition();
    scroller.addEventListener("scroll", observePosition, { passive: true });
    return () => scroller.removeEventListener("scroll", observePosition);
  }, [workspace.curation.id]);

  useEffect(() => {
    const content = workspaceMainRef.current;
    const scroller = content?.closest<HTMLElement>(".shell-product-body");
    if (!content || !scroller || typeof ResizeObserver === "undefined") return;
    let frame = 0;
    const observer = new ResizeObserver(() => {
      if (!followsTailRef.current) return;
      window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(() => {
        if (!followsTailRef.current) return;
        scroller.scrollTop = scroller.scrollHeight;
        ownScrollTopRef.current = scroller.scrollTop;
      });
    });
    observer.observe(content);
    return () => {
      observer.disconnect();
      window.cancelAnimationFrame(frame);
    };
  }, [workspace.curation.id]);

  // The composer's shade lifts it off the conversation scrolling under it. Scrolled to the end (or with nothing to
  // scroll), nothing is under it, so the shade goes (owner 2026-09-23). Marked on the host the dock is portaled into.
  useEffect(() => {
    const content = workspaceMainRef.current;
    const scroller = content?.closest<HTMLElement>(".shell-product-body");
    if (!composerHost || !content || !scroller) return;
    const update = () => {
      composerHost.dataset.scrollEnd = String(scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop <= 1);
    };
    update();
    scroller.addEventListener("scroll", update, { passive: true });
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(update);
    observer?.observe(scroller);
    observer?.observe(content);
    return () => {
      scroller.removeEventListener("scroll", update);
      observer?.disconnect();
    };
  }, [composerHost, workspace.curation.id]);

  useLayoutEffect(() => {
    if (previousLiveConversationKeyRef.current === liveConversationKey) return;
    previousLiveConversationKeyRef.current = liveConversationKey;
    if (followsTailRef.current) {
      followTail();
    } else {setUnseen(true);}
  }, [liveConversationKey]);

  return (
    <CurationComposerHostContext.Provider value={composerHost}>
      <section
      className="curation-workspace"
      aria-label={l("Curation conversation and results", "큐레이션 대화와 결과")}
      aria-busy={working || activeWork || undefined}
      data-phase={workspace.curation.phase}
    >
      <main className="curation-workspace__main" ref={workspaceMainRef}>
 {unseen && <Button type="button" className="curation-new-response" emphasis="quiet" onClick={followTail}>{l("New response","새 응답 보기")}</Button>}
        <ol
          className="curation-transcript"
          aria-label={l("Curation actions and results", "큐레이션 행동과 결과")}
        >
          {renderedConversation.length === 0 ? (
            <li className="curation-transcript__empty">
              <span aria-hidden="true">@</span>
              <div>
                <strong>{l("Choose the first action to begin.", "첫 행동을 선택해 흐름을 시작하세요.")}</strong>
                <p>
                  {l("Use the dedicated controls shown with the current result to start the next action.", "현재 결과에 표시되는 전용 컨트롤에서 다음 행동을 시작할 수 있습니다.")}
                </p>
              </div>
            </li>
          ) : (
            renderedConversation.map((item) => {
              if (item.kind === "DISCOVERY") return <li className="curation-transcript__row curation-transcript__row--message" key={`discovery:${item.response.id}`}><BackgroundDiscoveryResponse response={item.response} onOpen={setFinding} /></li>;
 if(item.kind==="FOLLOW_UP")return <li className="curation-transcript__row curation-transcript__row--message" key={item.message.id}><CurationFollowUpBubble message={item.message} /></li>;
              if (item.kind === "THREAD") {
                return (
                  <li className="curation-transcript__row curation-transcript__row--thread" key={`thread:${item.thread.id}`}>
                    <CurationThreadReport thread={item.thread} followUps={turnFollowUps(item.thread)} />
                  </li>
                );
              }
              if (item.kind === "MESSAGE") {
                return (
                  <li
                    className="curation-transcript__row curation-transcript__row--message"
                    key={item.message.id}
                  >
                    <CurationConversationBubble
                      message={item.message}
                      locale={locale}
                    />
                  </li>
                );
              }
              const { entry } = item;
              const isLatestArtifact = entry.id === latestArtifactEntryID;
              return (
                <TimelineEntry
                  key={isLatestArtifact ? `${workspace.curation.id}:live-artifact` : entry.id}
                  entry={entry}
                  curationId={workspace.curation.id}
                  isLatestArtifact={isLatestArtifact}
                  pending={entry.id === pendingEntryID}
                  displayedActivityActionId={displayedActivityActionId}
                  locale={locale}
                  l={l}
                  renderArtifact={renderArtifact}
                  conversationTail={
                    isLatestArtifact ? conversationTail : undefined
                  }
                />
              );
            })
          )}
        </ol>

        {agentWork ? (
          <div className="curation-agent-work">{agentWork}</div>
        ) : null}
        <div aria-hidden="true" className="curation-workspace__initial-tail" ref={initialTailRef} />
        </main>
        <div className="curation-workspace__dock-shell">
          <CurationBackground curationId={workspace.curation.id} view={background.view} refresh={background.refresh} finding={finding} onFindingChange={setFinding} />
          <div className="curation-composer-anchor" ref={setComposerHost} />
        </div>
      </section>
    </CurationComposerHostContext.Provider>
  );
}

// PLANNING has no artifact-anchored conversation: request turns merge into the timeline by time,
// the planning surface follows them (it portals each product group's rows into the turn that
// planned it and keeps only what belongs to no request), and a pending legacy request comes last.
function orderPlanningTranscript(
  timeline: CurationTimelineEntry[],
  threadItems: ConversationRenderItem[],
  pendingEntryID: string | undefined,
  artifactEntryID: string | undefined,
): ConversationRenderItem[] {
  const items: ConversationRenderItem[] = [
    ...timeline.map((entry) => ({ kind: "TIMELINE" as const, entry, createdAt: entry.createdAt })),
    ...threadItems,
  ].sort((left, right) => Date.parse(left.createdAt) - Date.parse(right.createdAt));
  for (const id of [artifactEntryID, pendingEntryID]) {
    const index = id ? items.findIndex((item) => item.kind === "TIMELINE" && item.entry.id === id) : -1;
    if (index >= 0) items.push(...items.splice(index, 1));
  }
  return items;
}

function orderTranscriptForPendingAction(
  timeline: CurationTimelineEntry[],
  activeWork: boolean,
  pendingAction?: CurationPendingTranscriptAction,
) {
  const entries = [...timeline];
  let movableIndex = -1;
  if (pendingAction) {
    movableIndex = findLastTimelineActionIndex(
      entries,
      (entry) => entry.command === pendingAction.command,
    );
  } else if (activeWork) {
    movableIndex = findLastTimelineActionIndex(
      entries,
      (entry) => entry.action?.effectKind === "INTELLIGENCE",
    );
  }

  if (movableIndex >= 0) {
    const [entry] = entries.splice(movableIndex, 1);
    entries.push(entry);
    return { entries, pendingEntryID: entry.id };
  }
  if (pendingAction) {
    entries.push({
      id: pendingAction.id,
      kind: "ACTION",
      createdAt: pendingAction.createdAt,
      command: pendingAction.command,
    });
    return { entries, pendingEntryID: pendingAction.id };
  }
  return { entries, pendingEntryID: undefined };
}

function findLastTimelineActionIndex(
  timeline: CurationTimelineEntry[],
  predicate: (
    entry: Extract<CurationTimelineEntry, { kind: "ACTION" }>,
  ) => boolean,
) {
  for (let index = timeline.length - 1; index >= 0; index -= 1) {
    const entry = timeline[index];
    if (entry.kind === "ACTION" && predicate(entry)) return index;
  }
  return -1;
}

function TimelineEntry({
  entry,
  curationId,
  isLatestArtifact,
  pending,
  locale,
  l,
  renderArtifact,
  conversationTail,
  displayedActivityActionId,
}: {
  entry: CurationTimelineEntry;
  curationId: string;
  isLatestArtifact: boolean;
  pending: boolean;
  locale: UILocale;
  l: Localize;
  renderArtifact?: Props["renderArtifact"];
  conversationTail?: ReactNode;
  displayedActivityActionId?: string;
}) {
  if (entry.kind === "ACTION") {
    return (
      <li
        className={[
          "curation-transcript__row",
          "curation-transcript__row--action",
          pending ? "curation-transcript__row--pending" : "",
        ].filter(Boolean).join(" ")}
      >
        <CurationUserBubble
          entry={entry}
          pending={pending}
          showPendingStatus={!displayedActivityActionId || entry.action?.id !== displayedActivityActionId}
          locale={locale}
          l={l}
        />
      </li>
    );
  }

  if (!entry.artifact) {
    return (
      <li className="curation-transcript__row">
        <p className="curation-result-note">{entry.summary}</p>
      </li>
    );
  }

  if (!isLatestArtifact) {
    return (
      <li className="curation-transcript__row">
        <HistoricalResultBubble
          artifact={entry.artifact}
          resultSummary={entry.summary}
          locale={locale}
          l={l}
        />
      </li>
    );
  }

  return (
    <li className="curation-transcript__row curation-transcript__row--live">
      <article
        className="curation-live-artifact"
        data-live-artifact="true"
        aria-labelledby={`artifact-${entry.artifact.id}`}
      >
        <header className="curation-live-artifact__header">
          <div>
            <p>{l("Vitlane", "Vitlane")}</p>
            <h2 id={`artifact-${entry.artifact.id}`}>
              {entry.artifact.title}
            </h2>
          </div>
          <time dateTime={entry.artifact.updatedAt}>
            {l("Updated {time}", "{time} 갱신", { time: formatTimelineTime(entry.artifact.updatedAt, locale) })}
          </time>
        </header>
        <div className="curation-live-artifact__body">
          {renderArtifact ? (
            renderArtifact(entry.artifact, {
              curationId,
              isLatest: true,
              conversationTail,
            })
          ) : (
            <>
              <DefaultArtifact artifact={entry.artifact} l={l} />
              {conversationTail}
            </>
          )}
        </div>
      </article>
    </li>
  );
}

function CurationUserBubble({
  entry,
  pending,
  showPendingStatus = true,
  locale,
  l,
}: {
  entry: TimelineActionEntry;
  pending: boolean;
  showPendingStatus?: boolean;
  locale: UILocale;
  l: Localize;
}) {
  return (
    <div
      className={`curation-user-bubble${pending ? " is-pending" : ""}`}
      data-pending-conversation={pending || undefined}
    >
      <header>
        <strong>{l("Me", "나")}</strong>
        <time dateTime={entry.createdAt}>
          {formatTimelineTime(entry.createdAt, locale)}
        </time>
      </header>
      <p>{userFacingAction(entry.command, l)}</p>
      {pending && showPendingStatus ? (
        <span>{l("Waiting for Vitlane", "Vitlane 응답 대기 중")}</span>
      ) : null}
    </div>
  );
}

function HistoricalResultBubble({
  artifact,
  resultSummary,
  locale,
  l,
}: {
  artifact: CurationArtifact;
  resultSummary: string;
  locale: UILocale;
  l: Localize;
}) {
  return (
    <div
      className="curation-conversation-bubble is-vitlane"
      data-conversation-presentation="result"
    >
      <header>
        <strong>{l("Vitlane", "Vitlane")}</strong>
        <time dateTime={artifact.updatedAt}>
          {formatTimelineTime(artifact.updatedAt, locale)}
        </time>
      </header>
      <p>
        {conversationalResultBody(artifact, resultSummary, l)}
      </p>
    </div>
  );
}

function DefaultArtifact({ artifact, l }: { artifact: CurationArtifact; l: Localize }) {
  if (!artifact.targets || artifact.targets.length === 0) {
    return (
      <div className="curation-artifact-empty">
        <strong>{l("There are no products to display yet.", "아직 표시할 상품이 없습니다.")}</strong>
        <p>{artifact.summary ?? l("Add a research target with the command below.", "아래 명령으로 조사 대상을 추가하세요.")}</p>
      </div>
    );
  }

  return (
    <div className="curation-target-list">
      <p>{artifact.summary}</p>
      <ol>
        {artifact.targets.map((target, index) => (
          <li key={target.id}>
            <span>{String(index + 1).padStart(2, "0")}</span>
            <strong>{target.title}</strong>
            <small>
              {l("{count} candidates", "후보 {count}개", { count: target.candidateCount ?? 0 })}
              {target.status ? ` · ${targetStatusLabel(target.status, l)}` : ""}
            </small>
          </li>
        ))}
      </ol>
    </div>
  );
}

function WorkspaceLoading() {
  const { l } = useLocale();
  return (
    <section
      className="curation-feedback curation-feedback--loading"
      aria-label={l("Loading curation", "큐레이션 불러오는 중")}
      aria-busy="true"
    >
      <p role="status">{l("Loading the curation flow…", "큐레이션 흐름을 불러오는 중…")}</p>
      <div aria-hidden="true">
        <span />
        <span />
        <span />
      </div>
    </section>
  );
}

function WorkspaceError({
  message,
  onRetry,
}: {
  message: string;
  onRetry?: () => void;
}) {
  const { l } = useLocale();
  return (
    <section className="curation-feedback" role="alert">
      <span className="curation-feedback__mark">!</span>
      <p>{l("Curation unavailable", "큐레이션을 표시할 수 없습니다")}</p>
      <h1>{l("We couldn't load the workflow.", "작업 흐름을 불러오지 못했습니다.")}</h1>
      <span>{message}</span>
      {onRetry ? (
        <Button type="button" emphasis="primary" onClick={onRetry}>
          {l("Reload", "다시 불러오기")}
        </Button>
      ) : null}
    </section>
  );
}

function WorkspaceEmpty() {
  const { l } = useLocale();
  return (
    <section className="curation-feedback">
      <span className="curation-feedback__mark">@</span>
      <p>{l("INTENT → CURATION", "INTENT → CURATION")}</p>
      <h1>{l("Start a new curation.", "새 큐레이션을 시작하세요.")}</h1>
      <span>
        {l("Enter your intent to move from purchase planning to candidate comparison in one flow.", "의도를 입력하면 구매 항목 계획부터 후보 비교까지 한 흐름으로 이어집니다.")}
      </span>
    </section>
  );
}

function conversationalResultBody(
  artifact: CurationArtifact,
  resultSummary: string,
  l: Localize,
) {
  const summary = artifact.summary || resultSummary;
  if (summary === invariantContent("Curation 계획을 시작했습니다.")) {
    return l(
      "Started a curation based on your request.",
      "요청을 바탕으로 큐레이션을 시작했습니다.",
    );
  }
  const researchStarted = summary.match(
    /^Target (\d+)개에 ResearchRound \d+개를 시작했습니다\.$/,
  );
  if (researchStarted) {
    return l(
      "Started recommendation research for {count} products.",
      "상품 {count}개의 추천 조사를 시작했습니다.",
      { count: researchStarted[1] },
    );
  }
  return summary || l("Curation updated.", "큐레이션을 갱신했습니다.");
}

function userFacingAction(command: string, l: Localize) {
  const bodyIndex = command.lastIndexOf(": ");
  if (bodyIndex >= 0) return command.slice(bodyIndex + 2).trim();
  if (command.includes("NextStep")) return l("Start curating with this configuration.", "이 구성으로 큐레이팅을 시작해 주세요.");
  if (command.includes("Remove")) return l("Remove this item.", "이 항목을 제거해 주세요.");
  if (command.includes("ResearchAgain")) return l("Research again with these conditions.", "이 조건으로 다시 조사해 주세요.");
  return l("I requested the next action for this result.", "이 결과에 다음 행동을 요청했어요.");
}

function targetStatusLabel(status: "READY" | "RESEARCHING" | "REVIEWING", l: Localize) {
  switch (status) {
    case "READY":
      return l("Ready to research", "조사 준비");
    case "RESEARCHING":
      return l("Researching", "조사 중");
    case "REVIEWING":
      return l("Reviewing candidates", "후보 검토");
  }
}
