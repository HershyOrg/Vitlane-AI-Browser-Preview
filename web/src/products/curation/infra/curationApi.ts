import { APIError, request } from "../../../shared/api/client";
import { randomUUID } from "../../../shared/browser/randomUUID";
import { localizeFixedCopy } from "../../../shared/i18n";
import type {
  ExpansionResult,
  PlanResult,
} from "../../../shared/api/types";
import type {
  AddTargetsActionResult,
  AutoResearchActionResult,
  AvailableCurationActions,
  CartView,
  CreateSelectionInput,
  CurationActionDescriptor,
  CurationActionType,
  CurationConflictErrorCode,
  CurationTimelineAction,
  CurationTimelineEntry,
  CurationTimelineResult,
  CurationWorkspaceModel,
  CurationWorkspaceResponse,
  PrepareCandidateConfigurationInput,
  PrepareCandidateConfigurationResponse,
  RemoveSelectionInput,
  ResearchAgentActionResult,
  SelectionCommandResult,
  TargetRemoveActionResult,
  UpdateSelectionInput,
} from "../domain/types";

const EXPANSION_RETRY_KEY = "vitlane.curation.expansion.retry";

export function getPlan(planId: string): Promise<PlanResult> {
  return request(`/api/v1/shopping-plans/${planId}`);
}

// One sidebar row: the title and the Curation it links to. The server orders
// rows newest first by creation time (ADR-0079).
export type SidebarCuration = {
  curationId: string;
  intentSummary: string;
  createdAt: string;
};

export type CurationListPage = {
  schemaVersion: "vitlane.curation-list.v2";
  curations: SidebarCuration[];
  // Older rows remain. On an `after` page it means more new rows exist than a
  // page holds, so the caller starts over from this page.
  nextCursor?: string;
  // The newest row the caller now knows, for its next `after` check.
  latestCursor?: string;
};

// Cursors are opaque strings the server hands out; the client only returns them.
export function listCurations(query: { before?: string; after?: string } = {}): Promise<CurationListPage> {
  const parameters = new URLSearchParams();
  if (query.before) parameters.set("before", query.before);
  if (query.after) parameters.set("after", query.after);
  const suffix = parameters.size > 0 ? `?${parameters.toString()}` : "";
  return request(`/api/v1/curations${suffix}`);
}

export async function createExpansion(
  planId: string,
  instruction: string,
): Promise<ExpansionResult> {
  const body = JSON.stringify({ instruction });
  const pending = readPendingExpansion(planId);
  const idempotencyKey =
    pending?.body === body ? pending.key : randomUUID();
  sessionStorage.setItem(
    `${EXPANSION_RETRY_KEY}.${planId}`,
    JSON.stringify({ key: idempotencyKey, body }),
  );
  try {
    const result = await request<ExpansionResult>(
      `/api/v1/shopping-plans/${planId}/expansions`,
      {
        method: "POST",
        headers: { "Idempotency-Key": idempotencyKey },
        body,
      },
    );
    sessionStorage.removeItem(`${EXPANSION_RETRY_KEY}.${planId}`);
    return result;
  } catch (error) {
    if (
      error instanceof Error &&
      "code" in error &&
      (error.code === "IDEMPOTENCY_KEY_REUSED" || error.code === "RESEARCH_CRITERIA_CHANGED")
    ) {
      sessionStorage.removeItem(`${EXPANSION_RETRY_KEY}.${planId}`);
    }
    throw error;
  }
}

export function getCurrentExpansion(planId: string) {
  return request<ExpansionResult>(
    `/api/v1/shopping-plans/${planId}/expansions/current`,
  );
}

function readPendingExpansion(
  planId: string,
): { key: string; body: string } | null {
  try {
    const raw = sessionStorage.getItem(`${EXPANSION_RETRY_KEY}.${planId}`);
    if (!raw) return null;
    const value = JSON.parse(raw) as { key?: unknown; body?: unknown };
    if (typeof value.key !== "string" || typeof value.body !== "string") {
      return null;
    }
    return { key: value.key, body: value.body };
  } catch {
    return null;
  }
}

export function getAvailableCurationActions(curationId: string) {
  return request<AvailableCurationActions>(
    `/api/v1/curations/${encodeURIComponent(curationId)}/available-actions`,
    { cache: "no-store" },
  );
}

/**
 * Sent when a call that can create or change a request Thread has finished,
 * whatever its outcome, so the Thread list is read then instead of on a timer
 * (ADR-0081). A missing curationId means any open Curation may be affected.
 */
export const curationThreadsChangedEvent = "vitlane:curation-threads-changed";
export type CurationThreadsChangedDetail = { curationId?: string };

async function touchingThreads<T>(
  curationId: string | undefined,
  run: () => Promise<T>,
): Promise<T> {
  try {
    return await run();
  } finally {
    // A failure may still have been stored before the response was lost, and
    // a rejection may be another window's active Thread, so both are read too.
    if (typeof window !== "undefined") {
      window.dispatchEvent(
        new CustomEvent<CurationThreadsChangedDetail>(
          curationThreadsChangedEvent,
          { detail: { curationId } },
        ),
      );
    }
  }
}

/**
 * Reopens a failed job the Server marked retryable. The Server enforces the
 * attempt ceiling, so this button can never become an open-ended spend.
 * A retry opens a new request Thread.
 */
export async function retryIntelligenceJob(jobId: string) {
  return touchingThreads(undefined, () =>
    request<{ jobId: string }>(
      `/api/v1/intelligence/jobs/${encodeURIComponent(jobId)}/retry`,
      { method: "POST" },
    ),
  );
}

/**
 * Stops every still-pending Job produced by one CurationAction. The Server
 * closes the product targets in the same transaction and preserves completed
 * results, so this is a stop command rather than a rollback.
 */
export async function cancelCurationAction(actionId: string) {
  return touchingThreads(undefined, () =>
    request<{ cancelledJobs: number }>(
      `/api/v1/curation-actions/${encodeURIComponent(actionId)}/cancel`,
      { method: "POST" },
    ),
  );
}

export async function getCurationWorkspace(curationId: string) {
  const response = await request<CurationWorkspaceResponse>(
    `/api/v1/curations/${encodeURIComponent(curationId)}/workspace`,
    { cache: "no-store" },
  );
  return {
    response,
    model: toCurationWorkspaceModel(response),
  };
}

export function getCurationCart(curationId: string) {
  return request<CartView>(
    `/api/v1/curations/${encodeURIComponent(curationId)}/cart`,
    { cache: "no-store" },
  );
}

export function prepareCandidateConfiguration(
  sessionId: string,
  candidateId: string,
  input: PrepareCandidateConfigurationInput,
) {
  return request<PrepareCandidateConfigurationResponse>(
    `/api/v1/shopping-sessions/${encodeURIComponent(sessionId)}/candidates/${encodeURIComponent(candidateId)}/configurations`,
    {
      method: "POST",
      body: JSON.stringify(input),
    },
  );
}

export function createCurationSelection(
  curationId: string,
  input: CreateSelectionInput,
) {
  return request<SelectionCommandResult>(
    `/api/v1/curations/${encodeURIComponent(curationId)}/selections`,
    {
      method: "POST",
      body: JSON.stringify(input),
    },
  );
}

export function updateCurationSelection(
  curationId: string,
  selectionId: string,
  input: UpdateSelectionInput,
) {
  return request<SelectionCommandResult>(
    `/api/v1/curations/${encodeURIComponent(curationId)}/selections/${encodeURIComponent(selectionId)}`,
    {
      method: "PUT",
      body: JSON.stringify(input),
    },
  );
}

export function removeCurationSelection(
  curationId: string,
  selectionId: string,
  input: RemoveSelectionInput,
) {
  return request<SelectionCommandResult>(
    `/api/v1/curations/${encodeURIComponent(curationId)}/selections/${encodeURIComponent(selectionId)}`,
    {
      method: "DELETE",
      body: JSON.stringify(input),
    },
  );
}

export async function executeTargetRemoveAction(input: {
  curationId: string;
  targetId: string;
  expectedCurationVersion: number;
}): Promise<TargetRemoveActionResult> {
  const payload = JSON.stringify(input);
  return withActionRetry(
    `${input.curationId}.TARGET_REMOVE.${input.targetId}`,
    payload,
    (actionId) =>
      request<TargetRemoveActionResult>(
        `/api/v1/curations/${encodeURIComponent(input.curationId)}/actions/${encodeURIComponent(actionId)}/target-remove`,
        {
          method: "PUT",
          body: JSON.stringify({
            targetId: input.targetId,
            expectedCurationVersion: input.expectedCurationVersion,
          }),
        },
      ),
  );
}

const CURATION_CONFLICT_ERROR_CODES: ReadonlySet<string> = new Set<
  CurationConflictErrorCode
>([
  "CURATION_SELECTION_VERSION_CONFLICT",
  "CURATION_SELECTION_COMMAND_CONFLICT",
  "VERSION_CONFLICT",
  "IDEMPOTENCY_KEY_REUSED",
  "CURATION_ACTION_UNAVAILABLE",
  "CURATION_ACTION_IN_PROGRESS",
  "CURATION_ARCHIVED",
]);

export function isCurationConflictAPIError(
  error: unknown,
): error is APIError & { readonly code: CurationConflictErrorCode } {
  return (
    error instanceof APIError &&
    CURATION_CONFLICT_ERROR_CODES.has(error.code)
  );
}

export async function executeAddTargetsAction(input: {
  curationId: string;
  planId: string;
  type: Extract<
    CurationActionType,
    "PLANNING_ADD_TARGETS" | "CURATION_ADD_TARGETS"
  >;
  instruction: string;
  expectedCurationVersion: number;
}): Promise<AddTargetsActionResult> {
  const payload = JSON.stringify(input);
  return withActionRetry(
    `${input.curationId}.${input.type}`,
    payload,
    async (actionId) => ({
      actionId,
      effect: await request<AddTargetsActionResult["effect"]>(
        `/api/v1/shopping-plans/${encodeURIComponent(input.planId)}/expansions`,
        {
          method: "POST",
          headers: { "Idempotency-Key": actionId },
          body: JSON.stringify({
            curationId: input.curationId,
            curationActionId: actionId,
            type: input.type,
            instruction: input.instruction,
            expectedCurationVersion: input.expectedCurationVersion,
          }),
        },
      ),
    }),
  );
}

export async function executeAutoResearchAction(input: {
 expectedConversationVersion?:number;
  curationId: string;
  request: string;
  expectedCurationVersion: number;
}): Promise<AutoResearchActionResult> {
  return sendConversation(input.curationId,{...input,curationId:undefined,mode:"AUTO"});
}

export async function executeConversationRequest(input: {expectedConversationVersion?:number;curationId: string; expectedCurationVersion: number; mode: "ADD_TARGET" | "RESEARCH_AGAIN" | "RETRY"; request: string; targetId?: string; jobId?: string}) {
 return sendConversation(input.curationId,{...input,curationId:undefined});
}

type ConversationInput={mode:string;request:string;targetId?:string;jobId?:string;expectedCurationVersion:number;expectedConversationVersion?:number;curationId?:undefined};
// Conversation requests admit request Threads on the Server.
async function sendConversation(curationId:string,input:ConversationInput):Promise<AutoResearchActionResult>{
 return touchingThreads(curationId,()=>sendConversationOnce(curationId,input));
}
// Persist the admitted snapshot with its key. A reload after a lost response
// must replay the original request, even if workspace versions have advanced.
async function sendConversationOnce(curationId:string,input:ConversationInput):Promise<AutoResearchActionResult>{
 const storageKey=`vitlane.curation.conversation.retry.${curationId}`;
 const semantic=JSON.stringify([input.mode,input.request,input.targetId??"",input.jobId??""]);
 let saved:{semantic:string;body:typeof input & {schemaVersion:string;clientRequestId:string}}|undefined;
 try{saved=JSON.parse(sessionStorage.getItem(storageKey)??"null") ?? undefined;}catch{/* Invalid local draft is discarded. */}
 if(saved?.semantic!==semantic || !saved?.body?.clientRequestId){saved={semantic,body:{...input,schemaVersion:"vitlane.curation-conversation-request.v1",clientRequestId:randomUUID()}};}
 sessionStorage.setItem(storageKey,JSON.stringify(saved));
 try{
  const result=await request<AutoResearchActionResult>(`/api/v1/curations/${encodeURIComponent(curationId)}/conversation-requests`,{method:"POST",headers:{"Idempotency-Key":saved.body.clientRequestId},body:JSON.stringify(saved.body)});
  sessionStorage.removeItem(storageKey);return result;
 }catch(caught){if(caught instanceof APIError && caught.status>=400&&caught.status<500&&caught.code!=="AUTO_RESEARCH_IN_PROGRESS")sessionStorage.removeItem(storageKey);throw caught;}
}

export async function respondToFollowUp(curationId: string, message: {id: string; version: number}, response: "ACCEPT" | "DISMISS" | "ACKNOWLEDGE") {
 const payload = {response,expectedVersion:message.version};
 // Accepting a proposal opens a request Thread inside the conversation API.
 return touchingThreads(curationId, () => withActionRetry(`${curationId}.FOLLOW_UP.${message.id}`,JSON.stringify(payload),clientRequestId => request(`/api/v1/curations/${encodeURIComponent(curationId)}/follow-ups/${encodeURIComponent(message.id)}/responses`,{method:"POST",headers:{"Idempotency-Key":clientRequestId},body:JSON.stringify({...payload,clientRequestId})})));
}

export async function executeStartCuratingAction(input: {
  curationId: string;
  planId: string;
  expectedCurationVersion: number;
  sessionIds: string[];
}): Promise<ResearchAgentActionResult> {
  const payload = JSON.stringify(input);
  return withActionRetry(
    `${input.curationId}.PLANNING_START_CURATING`,
    payload,
    async (actionId) => ({
      actionId,
      effect: await request<ResearchAgentActionResult["effect"]>(
        `/api/v1/shopping-plans/${encodeURIComponent(input.planId)}/research-rounds`,
        {
          method: "POST",
          headers: { "Idempotency-Key": actionId },
          body: JSON.stringify({
            curationId: input.curationId,
            curationActionId: actionId,
            expectedCurationVersion: input.expectedCurationVersion,
            sessionIds: input.sessionIds,
          }),
        },
      ),
    }),
  );
}

export async function executeResearchAgainAction(input: {
  curationId: string;
  targetId: string;
  sessionId: string;
  feedback: string;
  expectedCurationVersion: number;
  expectedSessionVersion: number;
}): Promise<ResearchAgentActionResult> {
  const payload = JSON.stringify(input);
  // The research-again route admits a request Thread on the Server.
  return touchingThreads(input.curationId, () => withActionRetry(
    `${input.curationId}.TARGET_RESEARCH_AGAIN.${input.targetId}`,
    payload,
    async (actionId) => {
      const criteriaKey = `${ACTION_RETRY_PREFIX}.criteria.${actionId}`;
      let version = sessionStorage.getItem(criteriaKey);
      if (version === null) { const current = await request<{ version: number } | null>(`/api/v1/curations/${encodeURIComponent(input.curationId)}/targets/${encodeURIComponent(input.targetId)}/criteria`); version = String(current?.version ?? 0); sessionStorage.setItem(criteriaKey, version); }
      const expectedCriteriaVersion = Number(version);

      const result = await request<{
        round?: { id: string; status?: string };
        schemaVersion?: "vitlane.curation-thread.v2";
        replay?: boolean;
      }>(
        `/api/v1/shopping-sessions/${encodeURIComponent(input.sessionId)}/research-again`,
        {
          method: "POST",
          headers: { "Idempotency-Key": actionId },
          body: JSON.stringify({
 schemaVersion: "vitlane.research-again.v2", expectedCriteriaVersion,
            curationId: input.curationId,
            targetId: input.targetId,
            curationActionId: actionId,
            feedback: input.feedback,
            expectedCurationVersion: input.expectedCurationVersion,
            expectedSessionVersion: input.expectedSessionVersion,
          }),
        },
      );
      sessionStorage.removeItem(criteriaKey);
      return {
        actionId,
        effect: { tasks: result.round ? [{ roundId: result.round.id }] : [] },
        // 종결 round의 replay에는 서버가 새 job을 붙이지 않으므로(터미널
        // round 멱등 처리) 이 응답은 "새 작업 없음"이다. 화면이 무동작으로
        // 보이지 않게 호출부가 안내한다.
        replayedTerminalRound:
          result.replay === true && result.round?.status !== "REQUESTED",
      };
    },
  ));
}

export function toCurationWorkspaceModel(
  response: CurationWorkspaceResponse,
): CurationWorkspaceModel {
  const descriptorByType = new Map(
    response.availableActions.map((descriptor) => [descriptor.id, descriptor]),
  );
  const groupsByTarget = new Map(
    response.research.groups.map((group) => [
      group.session.planTargetId,
      group,
    ]),
  );
  const timeline: CurationTimelineEntry[] = response.timeline.flatMap(
    (item) => {
      const { action } = item;
      if (NON_TRANSCRIPT_ACTIONS.has(action.type)) {
        return [];
      }
      const entries: CurationTimelineEntry[] = [
        {
          id: action.id,
          kind: "ACTION",
          createdAt: action.createdAt,
          command: response.conversation?.requests.some(r=>r.id===action.id&&r.mode==="FOLLOW_UP_ACCEPT") ? localizeFixedCopy("Accepted the follow-up proposal", "후속 제의를 수락했어요") : displayCommand(
            action,
            descriptorByType.get(action.type)?.alias,
            item.displayBody,
          ),
          action,
        },
      ];
      if (item.result && !response.conversation?.requests.some(r => r.actionId === action.id)) {
        entries.push({
          id: `${action.id}:result`,
          kind: "RESULT",
          createdAt: item.result.occurredAt,
          summary: item.result.summary,
          artifact: historicalResultArtifact(
            action.id,
            item.result,
          ),
        });
      }
      return entries;
    },
  );
  timeline.push({
    id: `${response.curation.id}:artifact:${response.curation.version}`,
    kind: "RESULT",
    createdAt: response.curation.updatedAt,
    summary:
      response.curation.phase === "PLANNING"
        ? localizeFixedCopy("Current purchase targets", "현재 구매 항목")
        : localizeFixedCopy("Current recommendations and cart", "현재 추천 상품과 장바구니"),
    artifact: {
      id: `${response.curation.id}:v${response.curation.version}`,
      kind:
        response.curation.phase === "PLANNING" ? "TARGET_LIST" : "CURATION",
      title:
        response.curation.phase === "PLANNING"
          ? localizeFixedCopy("Purchase targets to research", "조사할 구매 항목")
          : localizeFixedCopy("Curation results", "큐레이션 결과"),
      updatedAt: response.curation.updatedAt,
      targets: response.targets.map((target) => {
        const group = groupsByTarget.get(target.id);
        return {
          id: target.id,
          title: target.title,
          // CandidatePool is loaded through the Phase 8 workspace endpoint.
          // The curation lifecycle projection intentionally carries no
          // legacy Candidate rows and therefore cannot claim a count here.
          candidateCount: 0,
          status: group?.session.status,
        };
      }),
    },
  });
  const rail = {
    id: response.curation.id,
    title: response.plan.originalIntent,
    phase: response.curation.phase,
    coverage: response.coverage ?? ("NONE" as const),
    updatedAt: response.curation.updatedAt,
    activeWork: Boolean(response.activeWork),
  };
  return {
    curation: {
      ...rail,
      version: response.curation.version,
    },
    curations: [rail],
    availableActions: response.availableActions,
    timeline,
    cart: response.cart,
    activeWork: response.activeWork,
 conversation: response.conversation,
  };
}

const NON_TRANSCRIPT_ACTIONS = new Set<CurationActionType>([
  "TARGET_REMOVE",
  "SELECTION_MUTATION",
]);

const HISTORICAL_ACTION_ALIASES: Record<
  CurationActionType,
  CurationActionDescriptor["alias"]
> = {
  INTENT_NEXT_STEP: "@Intent-NextStep",
  PLANNING_ADD_TARGETS: "@TargetList-AddTarget",
  PLANNING_START_CURATING: "@Planning-NextStep",
  CURATION_ADD_TARGETS: "@Curation-AddTarget",
  TARGET_RESEARCH_AGAIN: "@Target-ResearchAgain",
  TARGET_REMOVE: "@Target-Remove",
  SELECTION_MUTATION: "@Selection-Mutation",
};

function displayCommand(
  action: CurationTimelineAction,
  currentAlias: CurationActionDescriptor["alias"] | undefined,
  displayBody: string | undefined,
) {
  const baseAlias = currentAlias ?? HISTORICAL_ACTION_ALIASES[action.type];
  const separator = baseAlias.indexOf("-");
  const alias =
    action.subjectId && separator > 0
      ? `${baseAlias.slice(0, separator)}:${action.subjectId}${baseAlias.slice(separator)}`
      : baseAlias;
  return displayBody ? `${alias}: ${displayBody}` : alias;
}

function historicalResultArtifact(
  actionId: string,
  result: CurationTimelineResult,
) {
  const presentation = {
    INTENT_ACCEPTED: {
      kind: "INTENT" as const,
      title: localizeFixedCopy("Intent applied", "Intent 반영"),
    },
    TARGET_EXPANSION: {
      kind: "TARGET_LIST" as const,
      title: localizeFixedCopy("Product list changed", "상품 목록 변경"),
    },
    RESEARCH_STARTED: {
      kind: "TARGET_LIST" as const,
      title: localizeFixedCopy("Recommendation research started", "추천 상품 조사 시작"),
    },
    TARGET_RESEARCHED: {
      kind: "CURATION" as const,
      title: localizeFixedCopy("Product research result", "상품 재조사 결과"),
    },
  }[result.kind];
  return {
    id: `${actionId}:historical-result`,
    kind: presentation.kind,
    title: presentation.title,
    summary: result.summary,
    updatedAt: result.occurredAt,
    diff: result.diff,
  };
}

const ACTION_RETRY_PREFIX = "vitlane.curation.action.retry";

async function withActionRetry<T>(
  scope: string,
  payload: string,
  execute: (actionId: string) => Promise<T>,
): Promise<T> {
  const storageKey = `${ACTION_RETRY_PREFIX}.${scope}`;
  const pending = readPendingAction(storageKey);
  const actionId = pending?.payload === payload ? pending.actionId : randomUUID();
  sessionStorage.setItem(storageKey, JSON.stringify({ actionId, payload }));
  try {
    const result = await execute(actionId);
    sessionStorage.removeItem(storageKey);
    return result;
  } catch (error) {
    if (
      error instanceof Error &&
      "code" in error &&
      (error.code === "IDEMPOTENCY_KEY_REUSED" || error.code === "RESEARCH_CRITERIA_CHANGED")
    ) {
      sessionStorage.removeItem(storageKey);
      sessionStorage.removeItem(`${ACTION_RETRY_PREFIX}.criteria.${actionId}`);
    }
    throw error;
  }
}

function readPendingAction(
  storageKey: string,
): { actionId: string; payload: string } | null {
  try {
    const raw = sessionStorage.getItem(storageKey);
    if (!raw) return null;
    const value = JSON.parse(raw) as {
      actionId?: unknown;
      payload?: unknown;
    };
    return typeof value.actionId === "string" &&
      typeof value.payload === "string"
      ? { actionId: value.actionId, payload: value.payload }
      : null;
  } catch {
    return null;
  }
}
