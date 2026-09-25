import { describe, expect, it } from "vitest";
import type { ResearchGroup, ResearchRound } from "../../../shared/api/types";
import type { IntelligenceJob } from "./types";
import {
  buildRoundTargetIndex,
  latestRoundForTarget,
  resolveTargetWorkStatus,
} from "./targetWork";

function round(overrides: Partial<ResearchRound>): ResearchRound {
  return {
    id: "round-1",
    shoppingSessionId: "session-1",
    userId: "user-1",
    roundNumber: 1,
    contextSchema: "vitlane.research-context.v1",
    contextVersion: 1,
    contextHash: "context-hash",
    status: "RESULTS_READY",
    createdAt: "2026-08-13T00:00:00Z",
    ...overrides,
  };
}

function group(overrides: Partial<ResearchGroup>): ResearchGroup {
  return {
    session: {
      id: "session-1",
      planTargetId: "target-1",
      userId: "user-1",
      targetSnapshot: {} as ResearchGroup["session"]["targetSnapshot"],
      researchScopeSnapshot: {} as ResearchGroup["session"]["researchScopeSnapshot"],
      status: "REVIEWING",
      version: 1,
      createdAt: "2026-08-13T00:00:00Z",
      updatedAt: "2026-08-13T00:00:00Z",
    },
    rounds: [],
    ...overrides,
  };
}

function job(overrides: Partial<IntelligenceJob>): IntelligenceJob {
  return {
    jobId: "job-1",
    actionId: "action-1",
    targetKind: "RESEARCH_ROUND",
    targetId: "round-1",
    provider: "MANAGED",
    status: "RUNNING",
    retryable: false,
    attempt: 1,
    steps: [],
    ...overrides,
  };
}

describe("buildRoundTargetIndex", () => {
  it("maps round, rounds history, and currentResearchRoundId to the target", () => {
    const groups = [
      group({
        session: {
          ...group({}).session,
          currentResearchRoundId: "round-current",
        },
        round: round({ id: "round-live" }),
        rounds: [round({ id: "round-old", roundNumber: 1 })],
      }),
    ];
    const index = buildRoundTargetIndex(groups);
    expect(index.get("round-current")).toBe("target-1");
    expect(index.get("round-live")).toBe("target-1");
    expect(index.get("round-old")).toBe("target-1");
  });
});

describe("resolveTargetWorkStatus", () => {
  it("carries the queue position and wait reason of a PENDING job", () => {
    const groups = [group({ round: round({ id: "round-1", status: "REQUESTED" }) })];
    const index = buildRoundTargetIndex(groups);
    expect(
      resolveTargetWorkStatus({
        targetId: "target-1",
        jobs: [job({ status: "PENDING", queueAhead: 2 })],
        roundTargetIndex: index,
      }),
    ).toEqual({ kind: "QUEUED", ahead: 2, reason: undefined });
    expect(
      resolveTargetWorkStatus({
        targetId: "target-1",
        jobs: [job({ status: "PENDING", queueReason: "MODEL_SLOT_BUSY", queueAhead: 0 })],
        roundTargetIndex: index,
      }),
    ).toEqual({ kind: "QUEUED", ahead: 0, reason: "MODEL_SLOT_BUSY" });
  });

  const baseIndex = new Map([["round-1", "target-1"]]);

  it("prefers the synchronous Expand over everything else", () => {
    expect(
      resolveTargetWorkStatus({
        targetId: "target-1",
        syncWorkingTargetId: "target-1",
        jobs: [job({ status: "FAILED" })],
        roundTargetIndex: baseIndex,
      }),
    ).toEqual({ kind: "SYNC_RUNNING" });
  });

  it("maps PENDING and RUNNING research jobs onto the target", () => {
    expect(
      resolveTargetWorkStatus({
        targetId: "target-1",
        jobs: [job({ status: "PENDING" })],
        roundTargetIndex: baseIndex,
      }),
    ).toEqual({ kind: "QUEUED" });
    expect(
      resolveTargetWorkStatus({
        targetId: "target-1",
        jobs: [job({ status: "RUNNING" })],
        roundTargetIndex: baseIndex,
      }),
    ).toEqual({ kind: "RUNNING" });
  });

  it("reports the newest failed job with a safe label, never a raw code", () => {
    const status = resolveTargetWorkStatus({
      targetId: "target-1",
      jobs: [
        job({
          status: "FAILED",
          failureCode: "CANDIDATE_RANKING_EMPTY",
          retryable: true,
        }),
      ],
      roundTargetIndex: baseIndex,
    });
    expect(status).toEqual({
      kind: "FAILED",
      reasonLabel: "조건과 확인 가능한 정보를 모두 만족하는 상품을 찾지 못했습니다.",
      retryable: true,
      jobId: "job-1",
    });
  });

  it("lets a newer success mask an older failure for the same target", () => {
    const index = new Map([
      ["round-1", "target-1"],
      ["round-2", "target-1"],
    ]);
    const status = resolveTargetWorkStatus({
      targetId: "target-1",
      jobs: [
        job({ jobId: "job-old", targetId: "round-1", status: "FAILED" }),
        job({ jobId: "job-new", targetId: "round-2", status: "SUCCEEDED" }),
      ],
      roundTargetIndex: index,
      latestRound: round({ id: "round-2", status: "RESULTS_READY" }),
    });
    expect(status).toEqual({ kind: "IDLE" });
  });

  it("falls back to the round verdict when no job is projected", () => {
    expect(
      resolveTargetWorkStatus({
        targetId: "target-1",
        jobs: [],
        roundTargetIndex: baseIndex,
        latestRound: round({ status: "NO_RESULTS" }),
      }),
    ).toEqual({ kind: "NO_RESULTS" });
    expect(
      resolveTargetWorkStatus({
        targetId: "target-1",
        jobs: [],
        roundTargetIndex: baseIndex,
        latestRound: round({
          status: "FAILED",
          failureReasonCode: "PROVIDER_UNAVAILABLE",
          failureRetryable: true,
        }),
      }),
    ).toEqual({
      kind: "FAILED",
      reasonLabel: "조사 지능을 사용할 수 없습니다.",
      retryable: true,
    });
  });

  it("ignores jobs belonging to other targets", () => {
    const index = new Map([["round-9", "target-9"]]);
    expect(
      resolveTargetWorkStatus({
        targetId: "target-1",
        jobs: [job({ targetId: "round-9", status: "RUNNING" })],
        roundTargetIndex: index,
      }),
    ).toEqual({ kind: "IDLE" });
  });
});

describe("latestRoundForTarget", () => {
  it("prefers the projected round and otherwise the highest round number", () => {
    const groups = [
      group({
        rounds: [
          round({ id: "round-1", roundNumber: 1 }),
          round({ id: "round-3", roundNumber: 3, status: "NO_RESULTS" }),
          round({ id: "round-2", roundNumber: 2 }),
        ],
      }),
    ];
    expect(latestRoundForTarget(groups, "target-1")?.id).toBe("round-3");
    const withLive = [
      group({ round: round({ id: "round-live" }), rounds: [] }),
    ];
    expect(latestRoundForTarget(withLive, "target-1")?.id).toBe("round-live");
  });
});
