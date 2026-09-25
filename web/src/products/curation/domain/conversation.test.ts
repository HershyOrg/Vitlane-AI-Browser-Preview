import { describe, expect, it } from "vitest";
import type { PlanTarget, ResearchGroup } from "../../../shared/api/types";
import type { IntelligenceJob } from "./types";
import {
  describeJobCompletion,
  reconcileConversationTurn,
  selectActivitySlot,
  type ConversationMessage,
} from "./conversation";

function job(overrides: Partial<IntelligenceJob>): IntelligenceJob {
  return {
    jobId: "job-1",
    actionId: "action-1",
    targetKind: "RESEARCH_ROUND",
    targetId: "round-1",
    provider: "MANAGED",
    status: "SUCCEEDED",
    retryable: false,
    attempt: 1,
    steps: [],
    ...overrides,
  };
}

describe("selectActivitySlot", () => {
  it("prefers local processing, then the newest non-terminal job", () => {
    expect(
      selectActivitySlot({
        localActivity: { label: "후보 더 찾기", detail: "진행 중" },
        jobs: [job({ status: "RUNNING" })],
      }).kind,
    ).toBe("LOCAL_PROCESSING");

    const serverSlot = selectActivitySlot({
      jobs: [job({ jobId: "job-old", status: "FAILED" }), job({ status: "RUNNING" })],
    });
    expect(serverSlot).toMatchObject({ kind: "SERVER_PROCESSING" });
    expect(serverSlot.kind === "SERVER_PROCESSING" && serverSlot.job?.jobId).toBe(
      "job-1",
    );

    expect(selectActivitySlot({ jobs: [job({ status: "FAILED" })] }))
      .toEqual({ kind: "EMPTY" });
  });

  it("treats activeWork without a projected job as server processing", () => {
    expect(
      selectActivitySlot({
        jobs: [],
        activeWork: { workTargetId: "round-1", label: "조사", status: "QUEUED" },
      }).kind,
    ).toBe("SERVER_PROCESSING");
  });

  it("projects effect-unknown confirmation separately from server processing", () => {
    expect(
      selectActivitySlot({
        jobs: [job({ status: "RUNNING" })],
        activeWork: {
          workTargetId: "round-1",
          label: "조사",
          status: "RESULT_CONFIRMATION_REQUIRED",
        },
      }),
    ).toMatchObject({
      kind: "RESULT_CONFIRMATION_REQUIRED",
      job: { jobId: "job-1" },
    });
  });
});

describe("reconcileConversationTurn", () => {
  const activeTurn: ConversationMessage[] = [
    {
      id: "user",
      role: "USER",
      title: "나",
      body: "다시 조사해줘",
      createdAt: "2026-09-02T00:00:00Z",
      state: "ACTIVE",
      turnId: "turn-1",
      reconcilesServerAction: true,
    },
    {
      id: "progress",
      role: "VITLANE",
      title: "조사 중",
      body: "요청을 처리하고 있습니다.",
      createdAt: "2026-09-02T00:00:01Z",
      state: "ACTIVE",
      turnId: "turn-1",
    },
  ];

  it("replaces progress and the optimistic user echo when a durable Action settles", () => {
    expect(reconcileConversationTurn(activeTurn, {
      role: "VITLANE",
      title: "조사 완료",
      body: "추천을 갱신했습니다.",
      state: "SETTLED",
      turnId: "turn-1",
      reconcilesServerAction: true,
      diff: { changed: ["업무용 의자"] },
    })).toEqual([]);
  });

  it("keeps the user request when no Server Action was created", () => {
    expect(reconcileConversationTurn(activeTurn, {
      role: "VITLANE",
      title: "선택 필요",
      body: "대상을 선택해 주세요.",
      state: "SETTLED",
      turnId: "turn-1",
    })).toEqual([
      expect.objectContaining({ id: "user", state: "SETTLED" }),
    ]);
  });
});

describe("describeJobCompletion", () => {
  const groups = [
    {
      session: { planTargetId: "target-1" },
      round: { id: "round-1", status: "NO_RESULTS" },
      rounds: [],
    },
  ] as unknown as ResearchGroup[];
  const targets = [{ id: "target-1", title: "Commuter backpack" }] as PlanTarget[];
  const roundTargetIndex = new Map([["round-1", "target-1"]]);

  it("keeps a NO_RESULTS success neutral and preserves the pool wording", () => {
    const draft = describeJobCompletion(job({ status: "SUCCEEDED" }), {
      roundTargetIndex,
      groups,
      targets,
    });
    expect(draft.title).toBe("조사 완료");
    expect(draft.body).toContain(
      "조사는 완료됐지만 조건에 맞는 상품을 찾지 못했습니다.",
    );
    expect(draft.body).toContain("저장된 추천 상품은 유지");
    expect(draft.body).toContain("Commuter backpack");
  });

  it("points a failure to the owning target instead of a global banner", () => {
    const draft = describeJobCompletion(
      job({ status: "FAILED", failureCode: "PROVIDER_UNAVAILABLE" }),
      { roundTargetIndex, groups, targets },
    );
    expect(draft.title).toBe("조사를 완료하지 못했습니다");
    expect(draft.body).toContain("Commuter backpack");
    expect(draft.body).toContain("해당 상품의 안내를 확인하고");
    expect(draft.body).not.toContain("PROVIDER_UNAVAILABLE");
  });

  it("falls back to generic copy when the round is no longer projected", () => {
    const draft = describeJobCompletion(
      job({ status: "SUCCEEDED", targetId: "round-gone" }),
      { roundTargetIndex, groups, targets },
    );
    expect(draft.body).toBe("추천 상품을 갱신했습니다.");
  });

  it("describes cancelled and planning-task jobs", () => {
    expect(
      describeJobCompletion(job({ status: "CANCELLED" }), {
        roundTargetIndex,
        groups,
        targets,
      }).title,
    ).toBe("조사를 취소했습니다");
    expect(
      describeJobCompletion(
        job({ targetKind: "PLANNING_TASK", targetId: "task-1", status: "SUCCEEDED" }),
        { roundTargetIndex, groups, targets },
      ).title,
    ).toBe("상품 추가 완료");
  });
});
