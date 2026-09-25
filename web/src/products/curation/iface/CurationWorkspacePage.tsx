import { findingAdded } from "../infra/backgroundApi";
import { ConversationActivity, conversationError } from "./CurationFollowUps";
import type { FollowUpMessage } from "../domain/types";
import { CurationThreadProvider, useCurationThreads } from "../app/useThreads";
import { ConversationResultsProvider, useConversationResults } from "../app/useConversationResults";
import { turnResultGroups } from "../domain/conversationResults";
import { createPortal } from "react-dom";
import { PlanningComposer } from "./PlanningComposer";
import { ResponseSpeaker } from "./CurationThread";
import {
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useNavigate, useParams } from "react-router";
import { CurationAgentWork } from "./CurationAgentWork";
import {
  Button,
  Notice,
  useProductShellCart,
} from "../../../shared/ui";
import { SectionErrorBoundary } from "./SectionErrorBoundary";
import { randomUUID } from "../../../shared/browser/randomUUID";
import {
  parseCurationCommand,
  serializeCurationCommand,
} from "../domain/command";
import {
  reconcileConversationTurn,
  type ConversationDraft,
  type ConversationMessage,
} from "../domain/conversation";
import type {
  CurationActiveWork,
  CurationActionType,
  CurationArtifact,
  CurationCommandSubmission,
  CurationPendingTranscriptAction,
  CurationWorkspaceResponse,
  IntelligenceJob,
} from "../domain/types";
import {
  executeAddTargetsAction,
 executeConversationRequest, respondToFollowUp,
  executeResearchAgainAction,
  executeStartCuratingAction,
  executeTargetRemoveAction,
  getCurationWorkspace,
  isCurationConflictAPIError,
} from "../infra/curationApi";
import { CurationWorkspace } from "./CurationWorkspace";
import { CatalogCurationResearch } from "./CatalogCurationResearch";
import { useLocale, type Localize } from "../../../shared/i18n";
import "./curation-workspace-page.css";

type LoadedWorkspace = Awaited<ReturnType<typeof getCurationWorkspace>>;

export function isCurationWorkInFlight(
  response: Pick<CurationWorkspaceResponse, "activeWork"> | undefined,
) {
  return (
    response?.activeWork?.status === "QUEUED" ||
    response?.activeWork?.status === "RUNNING"
  );
}

export const CURATION_WORKSPACE_POLL_INTERVAL_MS = 2_500;

export function startCurationWorkspacePolling(
  workInFlight: boolean,
  poll: () => void,
): () => void {
  if (!workInFlight) return () => undefined;
  const timer = globalThis.setInterval(
    poll,
    CURATION_WORKSPACE_POLL_INTERVAL_MS,
  );
  return () => globalThis.clearInterval(timer);
}

export type InitialPlanningViewState =
  | "WORKING"
  | "RESULT_CONFIRMATION_REQUIRED"
  | "CANCELLED"
  | "FAILED"
  | "IDLE";

/**
 * The first Planning artifact exists before any Target does. Target count alone
 * therefore cannot tell the UI whether the Server is still working: after a
 * cancel or failure it remains zero by design. Jobs are projected oldest first,
 * so the last Planning job is the current presentation source.
 */
export function initialPlanningViewState(
  jobs: readonly Pick<IntelligenceJob, "targetKind" | "status">[] = [],
  working = false,
  activeWorkStatus?: CurationActiveWork["status"],
): InitialPlanningViewState {
  let latestPlanningJob:
    | Pick<IntelligenceJob, "targetKind" | "status">
    | undefined;
  for (const job of jobs) {
    if (job.targetKind === "PLANNING_TASK") latestPlanningJob = job;
  }

  if (activeWorkStatus === "RESULT_CONFIRMATION_REQUIRED") {
    return "RESULT_CONFIRMATION_REQUIRED";
  }
  if (
    working ||
    latestPlanningJob?.status === "PENDING" ||
    latestPlanningJob?.status === "RUNNING"
  ) {
    return "WORKING";
  }
  if (latestPlanningJob?.status === "CANCELLED") return "CANCELLED";
  if (latestPlanningJob?.status === "FAILED") return "FAILED";
  return "IDLE";
}

/**
 * One route serves every curation, so React reuses this component when only
 * `:curationId` changes and each `useState` below survives the move. That is
 * how a held `activeAgentWork` reference started appearing over unrelated
 * curations, taking its Cancel control with it — and cancelling there closed
 * the work order of whichever curation the reference came from, leaving the
 * one on screen running. Keying by curation makes a different curation a
 * different mount, which is what every piece of state here already assumes.
 */
export function CurationWorkspacePage() {
  const { curationId = "" } = useParams();
  return <CurationThreadProvider key={curationId} curationId={curationId}><ConversationResultsProvider><CurationWorkspaceRoute curationId={curationId} /></ConversationResultsProvider></CurationThreadProvider>;
}

function CurationWorkspaceRoute({ curationId }: { curationId: string }) {
  const threads = useCurationThreads();
  const { l, locale } = useLocale();
  const navigate = useNavigate();
  const registerCart = useProductShellCart();
  const [loaded, setLoaded] = useState<LoadedWorkspace | null>(null);
  const [loading, setLoading] = useState(true);
  const [working, setWorking] = useState(false);
 const [conversationBusy,setConversationBusy]=useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [pendingTranscriptAction, setPendingTranscriptAction] =
    useState<CurationPendingTranscriptAction>();
  // The route owns transient presentation events so the transcript can move a
  // complete user/Vitlane turn above the current artifact as one state change.
  // The Candidate artifact emits messages but never decides their DOM order.
  const [catalogConversationMessages, setCatalogConversationMessages] =
    useState<ConversationMessage[]>([]);
  const [catalogCartCount, setCatalogCartCount] = useState(0);
  const [catalogCartOpenRequest, setCatalogCartOpenRequest] = useState(0);
  const openCart = useCallback(() => {
    setCatalogCartOpenRequest((value) => value + 1);
  }, []);
  const appendCatalogConversationMessage = useCallback(
    (draft: ConversationDraft) => {
      setCatalogConversationMessages((current) => {
        const preceding = reconcileConversationTurn(current, draft);
        return [
          ...preceding,
          {
            ...draft,
            id: `phase8-message:${randomUUID()}`,
            createdAt: new Date().toISOString(),
          },
        ].slice(-50);
      });
    },
    [],
  );

  useEffect(() => {
    setCatalogConversationMessages([]);
  }, [locale]);

  const refresh = useCallback(async () => {
    if (!curationId) {
      setError(l("The curation ID is missing.", "큐레이션 식별자가 없습니다."));
      setLoading(false);
      return;
    }
    try {
      const result = await getCurationWorkspace(curationId);
      setLoaded(result);
      setError(null);
    } catch (caught) {
      setError(messageOf(caught, l));
    } finally {
      setLoading(false);
    }
  }, [curationId, l]);

  useEffect(() => {
    setLoading(true);
    void refresh();
  }, [refresh]);

  // MANAGED work continues on the Server after the action that started it has
  // already returned, and the transition to CURATING creates entirely new work
  // orders. Without polling the page keeps rendering the response it fetched
  // before any of that happened, so the user sees a finished planning step and
  // nothing else until they reload.
  const workInFlight = useMemo(() => {
    return isCurationWorkInFlight(loaded?.response) || Boolean(loaded?.response.conversation?.unfinished);
  }, [loaded]);

  useEffect(() => {
    return startCurationWorkspacePolling(workInFlight, () => {
      // A poll failure is not the user's problem: the last good response stays
      // on screen and the next tick retries. Surfacing it as a page error
      // would replace working content with a transient network message.
      void getCurationWorkspace(curationId)
        .then((result) => setLoaded(result))
        .catch(() => undefined);
    });
  }, [workInFlight, curationId]);

  useEffect(()=>{
   const requests=loaded?.response.conversation?.requests;
   if(!requests)return;
   setCatalogConversationMessages(current=>{
    const turns=new Set(current.filter(m=>m.role==="USER"&&m.turnId&&requests.some(r=>r.body===m.body)).map(m=>m.turnId));
    return turns.size ? current.filter(m=>!m.turnId||!turns.has(m.turnId)) : current;
   });
  },[loaded]);

  const sidebarCurationID = loaded?.response.curation.id;
	  const sidebarCartCount = catalogCartCount;
  useEffect(() => {
    if (!sidebarCurationID) return;
    return registerCart({
      curationId: sidebarCurationID,
      count: sidebarCartCount,
      onOpen: openCart,
    });
  }, [
    openCart,
    registerCart,
    sidebarCartCount,
    sidebarCurationID,
  ]);

  const workspace = useMemo(() => {
    if (!loaded) return null;
    return loaded.model;
  }, [loaded]);
  const foregroundActive = Boolean(workspace?.activeWork);

  async function submitAction(
    submission: CurationCommandSubmission,
    allowCurrentLocalAction = false,
  ) {
    if (
      !loaded ||
      foregroundActive ||
      (working && !allowCurrentLocalAction)
    ) {
      return;
    }
    const { response } = loaded;
    if (submission.descriptor.transcriptPolicy === "APPEND") {
      setPendingTranscriptAction({
        id: `pending:${randomUUID()}`,
        command: transcriptCommandForSubmission(submission),
        createdAt: new Date().toISOString(),
      });
    }
    setWorking(true);
    setNotice(null);
    try {
      switch (submission.descriptor.id) {
        case "PLANNING_ADD_TARGETS":
        case "CURATION_ADD_TARGETS": {
 if(response.conversation){await executeConversationRequest({expectedConversationVersion:response.conversation?.version,curationId:response.curation.id,expectedCurationVersion:response.curation.version,mode:"ADD_TARGET",request:submission.body});break;}
          const result = await executeAddTargetsAction({
            curationId: response.curation.id,
            planId: response.plan.id,
            type: submission.descriptor.id,
            instruction: submission.body,
            expectedCurationVersion: response.curation.version,
          });
          if (!result.effect.intelligenceJob) {
            setNotice(
              l("The action was saved, but the research job did not start. Check again shortly.", "행동은 저장됐지만 조사 작업이 시작되지 않았습니다. 잠시 뒤 다시 확인해 주세요."),
            );
          }
          break;
        }
        case "PLANNING_START_CURATING": {
          const sessionIds = response.research.groups.map(
            ({ session }) => session.id,
          );
          if (sessionIds.length === 0) {
            throw new Error(l("There are no products to begin researching.", "조사를 시작할 상품이 없습니다."));
          }
          const result = await executeStartCuratingAction({
            curationId: response.curation.id,
            planId: response.plan.id,
            expectedCurationVersion: response.curation.version,
            sessionIds,
          });
          void result;
          break;
        }
        case "TARGET_RESEARCH_AGAIN": {
          const targetId = submission.subjectId;
          const group = targetId
            ? response.research.groups.find(
                ({ session }) => session.planTargetId === targetId,
              )
            : undefined;
          if (!targetId || !group) {
            throw new Error(l("Select a product to research again from the latest results.", "최신 결과에서 다시 조사할 상품을 선택해 주세요."));
          }
          if(response.conversation){await executeConversationRequest({expectedConversationVersion:response.conversation?.version,curationId:response.curation.id,expectedCurationVersion:response.curation.version,mode:"RESEARCH_AGAIN",request:submission.body,targetId});break;}
 const result = await executeResearchAgainAction({
            curationId: response.curation.id,
            targetId,
            sessionId: group.session.id,
            feedback: submission.body,
            expectedCurationVersion: response.curation.version,
            expectedSessionVersion: group.session.version,
          });
          if (result.replayedTerminalRound) {
            setNotice(
              l("The previous research-again request had already finished, so no new job started. Submit the same request once more to start new research.", "이전 재조사 요청이 이미 종결되어 새 작업을 시작하지 않았습니다. 같은 내용으로 한 번 더 요청하면 새 조사가 시작됩니다."),
            );
          }
          break;
        }
        case "TARGET_REMOVE": {
          if (!submission.subjectId) {
            throw new Error(l("The command needs the ID of the purchase item to remove.", "제거할 구매 항목 ID가 명령에 필요합니다."));
          }
          await executeTargetRemoveAction({
            curationId: response.curation.id,
            targetId: submission.subjectId,
            expectedCurationVersion: response.curation.version,
          });
          break;
        }
        default:
          throw new Error(
            l("Use the dedicated control in the latest result for this action.", "이 행동은 최신 결과의 전용 컨트롤에서 실행해 주세요."),
          );
      }
      await refresh();
    } catch (caught) {
      if (!isLegacyExternalAgentFixtureError(caught)) {
        setNotice(messageOf(caught, l));
      }
      throw caught;
    } finally {
      setPendingTranscriptAction(undefined);
      setWorking(false);
    }
  }

  async function respond(message:FollowUpMessage,response:"ACCEPT"|"DISMISS"|"ACKNOWLEDGE") {
   if(working || conversationBusy || foregroundActive)return;
   setConversationBusy(true);setNotice(null);
   try{await respondToFollowUp(curationId,message,response);await refresh();}
   catch(caught){setNotice(messageOf(caught,l));await refresh();}
   finally{setConversationBusy(false);window.requestAnimationFrame(()=>document.querySelector<HTMLTextAreaElement>(".catalog-ui-focus-composer textarea")?.focus());}
  }

  async function dispatchHook(
    type: CurationActionType,
    subjectId?: string,
    body = "",
    allowCurrentLocalAction = false,
  ) {
    if (
      !loaded ||
      foregroundActive ||
      (working && !allowCurrentLocalAction)
    ) {
      return;
    }
    const descriptor = loaded.model.availableActions.find(
      (candidate) => candidate.id === type,
    );
    if (!descriptor) {
      throw new Error(l("This action is not available at the current stage.", "현재 단계에서는 이 행동을 실행할 수 없습니다."));
    }
    const command = serializeCurationCommand(
      descriptor,
      body,
      descriptor.subjectSchema.idRequired ? subjectId : undefined,
    );
    const parsed = parseCurationCommand(
      command,
      loaded.model.availableActions,
    );
    await submitAction(
      { ...parsed, command },
      allowCurrentLocalAction,
    );
  }

  const threadRevision = threads?.revision;
  useEffect(() => { if (threadRevision) void refresh(); }, [threadRevision, refresh]);
 useEffect(()=>{const update=(event:Event)=>{if((event as CustomEvent<string>).detail===curationId)void refresh();};window.addEventListener(findingAdded,update);return ()=>window.removeEventListener(findingAdded,update);},[curationId,refresh]);
  const activityEmbedded = Boolean(loaded?.response);

  return (
    <div className="curation-route">
      {notice ? (
        <Notice tone="danger" announce title={l("We couldn't complete the action", "행동을 완료하지 못했습니다")}>
          {notice}
        </Notice>
      ) : null}
      <ConversationActivity.Provider value={{busy:conversationBusy,setBusy:setConversationBusy}}><><CurationWorkspace
 onFollowUpResponse={(m,r)=>void respond(m,r)}        workspace={workspace}
        loading={loading}
        error={error}
        working={working || conversationBusy}
        pendingTranscriptAction={pendingTranscriptAction}
        conversationMessages={catalogConversationMessages}
        displayedActivityActionId={
          (loaded?.response.intelligence?.find((job) => job.status === "RUNNING") ??
            loaded?.response.intelligence?.find((job) => job.status === "PENDING"))?.actionId
        }
        onRetry={() => {
          setLoading(true);
          void refresh();
        }}
        agentWork={
          activityEmbedded ? undefined : (
            <CurationAgentWork
              jobs={loaded?.response.intelligence ?? []}
              activeWork={loaded?.response.activeWork}
              onWorkChanged={refresh}
            />
          )
        }
        renderArtifact={(artifact, context) => (
          <WorkspaceArtifact
            artifact={artifact}
            response={loaded?.response}
            working={working}
            catalogCartOpenRequest={catalogCartOpenRequest}
            onCatalogCartCountChange={setCatalogCartCount}
            onOpenOrderSheet={navigate}
            onResearchAgain={(targetId, feedback) =>
              dispatchHook("TARGET_RESEARCH_AGAIN", targetId, feedback)
            }
            onAddTargets={(instruction) =>
              dispatchHook(
                loaded?.response.curation.phase === "PLANNING"
                  ? "PLANNING_ADD_TARGETS"
                  : "CURATION_ADD_TARGETS",
                loaded?.response.curation.id,
                instruction,
              )
            }
            onStartCurating={() =>
              dispatchHook(
                "PLANNING_START_CURATING",
                loaded?.response.curation.id,
              )
            }
            onRemoveTarget={(targetId) =>
              dispatchHook("TARGET_REMOVE", targetId)
            }
            conversationTail={context.conversationTail}
            onConversationMessage={appendCatalogConversationMessage}
            onWorkChanged={refresh}
          />
        )}
      /></></ConversationActivity.Provider>
    </div>
  );
}

function isLegacyExternalAgentFixtureError(caught: unknown) {
  return Boolean(
    caught &&
      typeof caught === "object" &&
      "code" in caught &&
      caught.code === "EXTERNAL_AGENT_RETIRED",
  );
}

export function WorkspaceArtifact({
  artifact,
  response,
  working,
  catalogCartOpenRequest,
  onCatalogCartCountChange,
  onOpenOrderSheet,
  onResearchAgain,
  onAddTargets,
  onStartCurating,
  onRemoveTarget,
  conversationTail,
  onConversationMessage,
  onWorkChanged,
}: {
  artifact: CurationArtifact;
  response?: CurationWorkspaceResponse;
  working: boolean;
  catalogCartOpenRequest: number;
  onCatalogCartCountChange: (count: number) => void;
  onOpenOrderSheet?: (path: string) => void;
  onResearchAgain: (targetId: string, feedback: string) => Promise<void>;
  onAddTargets: (instruction: string) => Promise<void>;
  onStartCurating: () => Promise<void>;
  onRemoveTarget: (targetId: string) => Promise<void>;
  conversationTail?: ReactNode;
  onConversationMessage?: (message: ConversationDraft) => void;
  onWorkChanged?: () => void | Promise<void>;
}) {
  const { l } = useLocale();
  const [planningSending, setPlanningSending] = useState(false);
  const threadState = useCurationThreads();
  const conversationResults = useConversationResults();
  const planningBusy = working || planningSending || Boolean(threadState?.busy) || Boolean(response?.activeWork) || Boolean(response?.intelligence?.some(j => j.status === "PENDING" || j.status === "RUNNING"));
  const agentName = "Vitlane";
  const planningViewState = initialPlanningViewState(
    response?.intelligence,
    working,
    response?.activeWork?.status,
  );

  if (!response) {
    return (
      <div className="curation-artifact-empty">
        <strong>{l("Loading curation results.", "큐레이션 결과를 불러오고 있습니다.")}</strong>
      </div>
    );
  }

  if (response.curation.phase === "PLANNING") {
    // A planned product group belongs to the request that planned it (ADR-0089): its row goes into that
    // request's turn, right under the plan sentence, where the research row will stand once research
    // starts. What no request planned stays here, after the turns, with its own sentence.
    const planGroups = turnResultGroups(
      threadState?.threads ?? [],
      response.targets.map((target) => target.id),
      Object.fromEntries(response.targets.map((target) => [target.id, target.createdAt])),
    );
    const unplanned = planGroups.find((group) => !group.holderId)?.targetIds ?? [];
    const recovery = response.targets.length > 0 && !threadState?.active;
    const planRows = (ids: string[]) => (
      <ul className="curation-results__list curation-results__plan" data-layout="row">
        {ids.map((id) => {
          const target = response.targets.find((candidate) => candidate.id === id);
          if (!target) return null;
          // A product group with no budget of its own is allocated 0; that is "no limit", not a price of 0.
          const allocated = Number(target.allocatedBudget.amount);
          const note = [
            allocated > 0
              ? l("Budget {amount}", "예산 {amount}", { amount: formatMoney(target.allocatedBudget.amount, target.allocatedBudget.currency) })
              : l("Budget: no limit", "예산 제한 없음"),
            target.normalizedIntent && target.normalizedIntent !== target.title ? target.normalizedIntent : "",
          ].filter(Boolean).join(" · ");
          return (
            <li key={target.id} className="curation-result curation-result--row curation-result--plan" data-planning-target={target.id}>
              <div className="curation-result__row">
                <span className="curation-result__thumb" aria-hidden="true" />
                <span className="curation-result__text">
                  <span className="curation-result__caption">{target.title}</span>
                  <span className="curation-result__name curation-result__plan-note">{note}</span>
                </span>
              </div>
              <Button
                className="curation-response__plan-remove"
                type="button"
                size="compact"
                emphasis="quiet"
                disabled={planningBusy}
                aria-label={l("Remove the {title} product group", "{title} 해당 상품군 제거", { title: target.title })}
                onClick={() => void onRemoveTarget(target.id)}
              >
                {l("Remove", "빼기")}
              </Button>
            </li>
          );
        })}
      </ul>
    );
    const idle = response.targets.length === 0
      ? ["WORKING", "CANCELLED", "IDLE"].includes(planningViewState)
      : unplanned.length === 0 && !recovery;
    return (
      <div className={`shell-research-groups curation-hook-groups curation-planning-workspace${idle ? " is-idle-empty" : ""}`}>
        {response.targets.length === 0 ? (
          <InitialPlanningArtifact
            state={planningViewState}
            agentName={agentName}
          />
        ) : null}
        {planGroups.map((group) => {
          const host = group.holderId ? conversationResults?.hosts[group.holderId] : undefined;
          return host && group.holderId
            ? createPortal(<div className="curation-results" data-planning-response="true">{planRows(group.targetIds)}</div>, host, `plan:${group.holderId}`)
            : null;
        })}
        {unplanned.length > 0 || recovery ? (
          // Planning answers in the same voice as a finished request (ADR-0086).
          <article className="curation-response curation-planning-response" data-planning-response="true">
            <ResponseSpeaker />
            {unplanned.length > 0 ? (
              <>
                <p className="curation-response__text">
                  {unplanned.length > 1
                    ? l("I’ll look for these as {count} products.", "{count}가지로 나눠 찾아볼게요.", { count: unplanned.length })
                    : l("I’ll look for this product.", "이 상품으로 찾아볼게요.")}
                </p>
                <div className="curation-results">{planRows(unplanned)}</div>
              </>
            ) : null}
            {/* The manual start is a recovery path for a failed auto-start. While a
                request Thread owns the research, the bar above the composer is the
                only place that reports it. */}
            {recovery ? (
              <div className="curation-response__next">
                <p className="curation-response__text">
                  {l("The research has not started yet. Start it when the list looks right.", "아직 조사를 시작하지 않았어요. 목록이 맞으면 시작해 주세요.")}
                </p>
                <Button
                  type="button"
                  emphasis="primary"
                  disabled={planningBusy}
                  onClick={() => void onStartCurating()}
                >
                  {l("Start curating", "큐레이팅 시작")}
                </Button>
              </div>
            ) : null}
          </article>
        ) : null}
        <PlanningComposer
          response={response}
          working={planningBusy}
          onBusyChange={setPlanningSending}
          onAddTargets={onAddTargets}
          onWorkChanged={onWorkChanged}
        />
      </div>
    );
  }

  return (
    <SectionErrorBoundary
      title={l("We couldn't display the research screen.", "조사 화면을 그리지 못했습니다.")}
      description={l("Your research data is preserved. If retrying does not help, refresh the page.", "조사 데이터는 보존되어 있습니다. 다시 시도해도 반복되면 새로고침해 주세요.")}
      retryLabel={l("Try again", "다시 시도")}
    >
      <CatalogCurationResearch
        response={response}
        working={working}
        cartOpenRequest={catalogCartOpenRequest}
        onAddTargets={onAddTargets}
        onResearchAgain={onResearchAgain}
        onCartCountChange={onCatalogCartCountChange}
        onOpenOrderSheet={onOpenOrderSheet}
        onRemoveTarget={onRemoveTarget}
        conversationTail={conversationTail}
        onConversationMessage={onConversationMessage ?? (() => undefined)}
        onWorkChanged={onWorkChanged}
      />
    </SectionErrorBoundary>
  );
}

export function InitialPlanningArtifact({
  state,
}: {
  state: InitialPlanningViewState;
  agentName?: string;
}) {
  const { l } = useLocale();
  if (state === "WORKING") return null;

  if (state === "CANCELLED") {
    return <p className="curation-result-note" data-planning-state="cancelled" role="status">
      {l("Research cancelled. You can send another request below.", "조사를 취소했습니다. 아래에서 새 요청을 보낼 수 있습니다.")}
    </p>;
  }

  if (state === "RESULT_CONFIRMATION_REQUIRED") {
    return (
      <div
        className="curation-artifact-empty curation-artifact-empty--recovery"
        data-planning-state="result-confirmation-required"
      >
        <span className="curation-artifact-state" role="status">
          {l("Confirmation required", "결과 확인 필요")}
        </span>
        <strong>{l("Vitlane must confirm the research result.", "Vitlane에서 조사 결과를 확인해야 합니다.")}</strong>
        <p>
          {l("We paused this research safely. Your saved results are unchanged.", "조사를 안전하게 중단했습니다. 저장된 결과는 그대로 유지됩니다.")}
        </p>
      </div>
    );
  }

  if (state === "FAILED") {
    return (
      <div
        className="curation-artifact-empty curation-artifact-empty--recovery"
        data-planning-state="failed"
      >
        <span className="curation-artifact-state is-failed" role="status">
          {l("Stopped", "중단됨")}
        </span>
        <strong>{l("We couldn't finish organizing the research items.", "조사 항목 구성을 완료하지 못했습니다.")}</strong>
        <p>
          {l("Retry from the failure notice above, or add a different research item directly with the + button below.", "위 실패 안내에서 다시 시도하거나 아래 + 버튼에서 다른 조사 항목을 직접 추가할 수 있습니다.")}
        </p>
      </div>
    );
  }

  return (
    <div
      className="curation-artifact-empty curation-artifact-empty--recovery"
      data-planning-state="idle"
    >
      <span className="curation-artifact-state" role="status">
        {l("Ready for input", "입력 가능")}
      </span>
      <strong>{l("There are no research items yet.", "아직 조사 항목이 없습니다.")}</strong>
      <p>
        {l("Add an item to research with the + button below.", "아래 + 버튼에서 조사할 항목을 직접 추가해 주세요.")}
      </p>
    </div>
  );
}


function transcriptCommandForSubmission(
  submission: CurationCommandSubmission,
) {
  return submission.command;
}

function messageOf(caught: unknown, l: Localize) {
 const conversationMessage=conversationError(caught,l);if(conversationMessage)return conversationMessage;
  return caught instanceof Error
    ? caught.message
    : l("We couldn't process the curation action.", "큐레이션 행동을 처리하지 못했습니다.");
}


function formatMoney(amount: string, currency: string) {
  const value = Number(amount);
  if (!Number.isFinite(value)) return `${amount} ${currency}`;
  try {
    return new Intl.NumberFormat(undefined, {
      style: "currency",
      currency,
      maximumFractionDigits: 2,
    }).format(value);
  } catch {
    return `${amount} ${currency}`;
  }
}
