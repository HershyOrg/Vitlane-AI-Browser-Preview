import type { ResearchGroup, ResearchRound } from "../../../shared/api/types";
import type { IntelligenceJob } from "./types";
import { jobReasonLabel } from "./jobPresentation";

/**
 * What is happening to a single Target right now.
 *
 * Async research jobs reference a ResearchRound ID, not a Target ID, so the
 * projection alone cannot say "this Target is being researched". This module
 * closes that gap: rounds are mapped back to their Target through the
 * ShoppingSession, and the newest job for the Target decides the state.
 */
export type TargetWorkStatus =
  | { kind: "SYNC_RUNNING" }
  | { kind: "QUEUED"; ahead?: number; reason?: string }
  | { kind: "RUNNING" }
  | { kind: "FAILED"; reasonLabel: string; retryable: boolean; jobId?: string }
  | { kind: "NO_RESULTS" }
  | { kind: "IDLE" };

export function buildRoundTargetIndex(
  groups: readonly ResearchGroup[],
): Map<string, string> {
  const index = new Map<string, string>();
  for (const group of groups) {
    const targetId = group.session.planTargetId;
    if (group.session.currentResearchRoundId) {
      index.set(group.session.currentResearchRoundId, targetId);
    }
    if (group.round) index.set(group.round.id, targetId);
    for (const round of group.rounds ?? []) index.set(round.id, targetId);
  }
  return index;
}

export function latestRoundForTarget(
  groups: readonly ResearchGroup[],
  targetId: string,
): ResearchRound | undefined {
  const group = groups.find(({ session }) => session.planTargetId === targetId);
  if (!group) return undefined;
  return (
    group.round ??
    [...(group.rounds ?? [])].sort(
      (left, right) => right.roundNumber - left.roundNumber,
    )[0]
  );
}

// Jobs are projected oldest-first for the whole curation; only the newest job
// touching this Target may speak for it, so a fresh success permanently masks
// older failures.
function newestJobForTarget(
  jobs: readonly IntelligenceJob[],
  roundTargetIndex: ReadonlyMap<string, string>,
  targetId: string,
): IntelligenceJob | undefined {
  for (let index = jobs.length - 1; index >= 0; index -= 1) {
    const job = jobs[index];
    if (job.targetKind !== "RESEARCH_ROUND") continue;
    if (roundTargetIndex.get(job.targetId) === targetId) return job;
  }
  return undefined;
}

export function resolveTargetWorkStatus(input: {
  targetId: string;
  syncWorkingTargetId?: string;
  jobs: readonly IntelligenceJob[];
  roundTargetIndex: ReadonlyMap<string, string>;
  latestRound?: ResearchRound;
}): TargetWorkStatus {
  const { targetId, syncWorkingTargetId, jobs, roundTargetIndex, latestRound } =
    input;
  if (syncWorkingTargetId === targetId) return { kind: "SYNC_RUNNING" };

  const job = newestJobForTarget(jobs, roundTargetIndex, targetId);
  if (job?.status === "PENDING") {
    return { kind: "QUEUED", ahead: job.queueAhead, reason: job.queueReason || undefined };
  }
  if (job?.status === "RUNNING") return { kind: "RUNNING" };
  if (job?.status === "FAILED") {
    return {
      kind: "FAILED",
      reasonLabel: jobReasonLabel(job.failureCode),
      retryable: job.retryable,
      jobId: job.jobId,
    };
  }

  // A newest job that succeeded (or was cancelled) hands the verdict to the
  // round it produced; a FAILED round without any projected job (history
  // beyond the job window) still deserves an explanation, just without retry.
  if (latestRound?.status === "FAILED" && job?.status !== "SUCCEEDED") {
    return {
      kind: "FAILED",
      reasonLabel: jobReasonLabel(latestRound.failureReasonCode),
      retryable: latestRound.failureRetryable ?? false,
    };
  }
  if (latestRound?.status === "NO_RESULTS") return { kind: "NO_RESULTS" };
  return { kind: "IDLE" };
}
