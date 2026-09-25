import type { PlanTarget, ResearchGroup } from "../../../shared/api/types";
import type {
  CurationActiveWork,
  CurationArtifactDiff,
  IntelligenceJob,
} from "./types";
import { jobReasonLabel } from "./jobPresentation";
import { localizeFixedCopy } from "../../../shared/i18n";

/**
 * Conversation ordering belongs to the route-level timeline. This selector
 * only chooses the contextual activity surface that accompanies the one live
 * conversation event between the current artifact and composer.
 */
export type ConversationMessage = {
  id: string;
  role: "USER" | "VITLANE";
  title: string;
  body: string;
  createdAt: string;
  /**
   * ACTIVE messages belong below the current artifact while Vitlane is
   * answering. SETTLED messages belong to transcript history above it.
   * Messages without an explicit state are settled for backwards-compatible
   * local notices.
   */
  state?: "ACTIVE" | "SETTLED";
  turnId?: string;
  diff?: CurationArtifactDiff;
  /**
   * The local user echo is temporary when the same request becomes a durable
   * Server CurationAction. Once that Action is visible, render only the Server
   * user bubble and keep the local Vitlane progress/Diff for the turn.
   */
  reconcilesServerAction?: boolean;
};

export type ConversationDraft = Omit<ConversationMessage, "id" | "createdAt">;

export function reconcileConversationTurn(
  current: readonly ConversationMessage[],
  draft: ConversationDraft,
): ConversationMessage[] {
  if (draft.state !== "SETTLED" || !draft.turnId) return [...current];
  return current.flatMap((message) => {
    if (message.turnId !== draft.turnId) return [message];
    // Vitlane progress is replaced by the terminal response. When the request
    // produced a durable Server Action, that Action also replaces its local
    // optimistic user echo.
    if (message.role === "VITLANE" && message.state === "ACTIVE") return [];
    if (
      message.role === "USER" &&
      message.state === "ACTIVE" &&
      draft.reconcilesServerAction
    ) return [];
    return [{ ...message, state: "SETTLED" as const }];
  });
}

export type LocalActivity = {
  label: string;
  detail: string;
};

export type ActivitySlot =
  | { kind: "LOCAL_PROCESSING"; activity: LocalActivity }
  | { kind: "SERVER_PROCESSING"; job?: IntelligenceJob }
  | { kind: "RESULT_CONFIRMATION_REQUIRED"; job?: IntelligenceJob }
  | { kind: "EMPTY" };

export function selectActivitySlot(input: {
  localActivity?: LocalActivity;
  jobs: readonly IntelligenceJob[];
  activeWork?: CurationActiveWork;
}): ActivitySlot {
  // Synchronous local work is short and exact — it wins over everything.
  if (input.localActivity) {
    return { kind: "LOCAL_PROCESSING", activity: input.localActivity };
  }
  if (input.activeWork?.status === "RESULT_CONFIRMATION_REQUIRED") {
    const job = [...input.jobs]
      .reverse()
      .find(({ targetId, status }) =>
        targetId === input.activeWork?.workTargetId && status === "RUNNING"
      );
    return { kind: "RESULT_CONFIRMATION_REQUIRED", job };
  }
  // Only the newest non-terminal job may occupy the slot; older jobs are
  // history and never resurface here.
  for (let index = input.jobs.length - 1; index >= 0; index -= 1) {
    const job = input.jobs[index];
    if (job.status === "RUNNING" || job.status === "PENDING") {
      return { kind: "SERVER_PROCESSING", job };
    }
  }
  if (input.activeWork) {
    return { kind: "SERVER_PROCESSING" };
  }
  return { kind: "EMPTY" };
}

function findRoundStatus(
  groups: readonly ResearchGroup[],
  roundId: string,
): string | undefined {
  for (const group of groups) {
    if (group.round?.id === roundId) return group.round.status;
    const round = (group.rounds ?? []).find(({ id }) => id === roundId);
    if (round) return round.status;
  }
  return undefined;
}

/**
 * Turns a job that just reached a terminal status into the Vitlane message the
 * conversation records. A SUCCEEDED research job whose round closed as
 * NO_RESULTS is a completed investigation, not a failure — the copy must stay
 * neutral. Failure detail and retry live in the owning Target section, so the
 * failure message here is only a short pointer.
 */
export function describeJobCompletion(
  job: IntelligenceJob,
  context: {
    roundTargetIndex: ReadonlyMap<string, string>;
    groups: readonly ResearchGroup[];
    targets: readonly PlanTarget[];
  },
): ConversationDraft {
  const targetId =
    job.targetKind === "RESEARCH_ROUND"
      ? context.roundTargetIndex.get(job.targetId)
      : undefined;
  const targetTitle = context.targets.find(({ id }) => id === targetId)?.title;

  if (job.status === "CANCELLED") {
    return {
      role: "VITLANE",
      title: localizeFixedCopy("Research cancelled", "조사를 취소했습니다"),
      body: localizeFixedCopy("Your saved recommendations are unchanged.", "저장된 추천 상품은 그대로 유지됩니다."),
    };
  }

  if (job.targetKind === "PLANNING_TASK") {
    if (job.status === "FAILED") {
      return {
        role: "VITLANE",
        title: localizeFixedCopy("We couldn't add the product", "상품을 추가하지 못했습니다"),
        body: jobReasonLabel(job.failureCode),
      };
    }
    return {
      role: "VITLANE",
      title: localizeFixedCopy("Product added", "상품 추가 완료"),
      body: localizeFixedCopy("The new product is now visible in your research.", "새 상품을 조사 화면에 표시했습니다."),
    };
  }

  if (job.status === "FAILED") {
    return {
      role: "VITLANE",
      title: localizeFixedCopy("Research failed", "조사를 완료하지 못했습니다"),
      body: targetTitle
        ? localizeFixedCopy(
            "Research for {target} failed. Review the message for that product and try again.",
            "{target} 조사를 완료하지 못했습니다. 해당 상품의 안내를 확인하고 다시 시도해 주세요.",
            { target: targetTitle },
          )
        : localizeFixedCopy("Review the message for that product and try again.", "해당 상품의 안내를 확인하고 다시 시도해 주세요."),
    };
  }

  const roundStatus = findRoundStatus(context.groups, job.targetId);
  if (roundStatus === "NO_RESULTS") {
    return {
      role: "VITLANE",
      title: localizeFixedCopy("Research complete", "조사 완료"),
      body: targetTitle
        ? localizeFixedCopy(
            "Research completed but found no matching products. Your saved recommendations remain; adjust {target} and research again.",
            "조사는 완료됐지만 조건에 맞는 상품을 찾지 못했습니다. 저장된 추천 상품은 유지되며, {target}의 조건을 조정해 다시 조사할 수 있습니다.",
            { target: targetTitle },
          )
        : localizeFixedCopy("Research completed but found no matching products. Your saved recommendations remain.", "조사는 완료됐지만 조건에 맞는 상품을 찾지 못했습니다. 저장된 추천 상품은 유지됩니다."),
    };
  }
  return {
    role: "VITLANE",
    title: localizeFixedCopy("Research complete", "조사 완료"),
    body: targetTitle
      ? localizeFixedCopy("Updated recommendations for {target}.", "{target}의 추천 상품을 갱신했습니다.", { target: targetTitle })
      : localizeFixedCopy("Recommendations updated.", "추천 상품을 갱신했습니다."),
  };
}
