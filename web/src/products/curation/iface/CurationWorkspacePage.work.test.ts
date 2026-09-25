import { afterEach, describe, expect, it, vi } from "vitest";
import type { IntelligenceJob } from "../domain/types";
import {
  initialPlanningViewState,
  isCurationWorkInFlight,
  startCurationWorkspacePolling,
} from "./CurationWorkspacePage";

function planningJob(
  status: IntelligenceJob["status"],
): Pick<IntelligenceJob, "targetKind" | "status"> {
  return { targetKind: "PLANNING_TASK", status };
}

describe("Curation workspace work state", () => {
  afterEach(() => vi.useRealTimers());

  it("Job 기반 activeWork가 없으면 legacy session 상태와 무관하게 polling을 멈춘다", () => {
    expect(isCurationWorkInFlight({})).toBe(false);
  });

  it("PENDING/RUNNING Job projection만 polling을 유지한다", () => {
    expect(
      isCurationWorkInFlight({
        activeWork: {
          workTargetId: "round-1",
          label: "후보 조사",
          status: "QUEUED",
        },
      }),
    ).toBe(true);
    expect(
      isCurationWorkInFlight({
        activeWork: {
          workTargetId: "round-1",
          label: "후보 조사",
          status: "RUNNING",
        },
      }),
    ).toBe(true);
  });

  it("EFFECT_UNKNOWN 결과 확인 상태는 polling을 중단한다", () => {
    expect(
      isCurationWorkInFlight({
        activeWork: {
          workTargetId: "round-unknown",
          label: "후보 조사",
          status: "RESULT_CONFIRMATION_REQUIRED",
        },
      }),
    ).toBe(false);
  });

  it("10초 동안 기존 RUNNING은 4회 poll하고 결과 확인 상태는 0회 poll한다", () => {
    vi.useFakeTimers();
    const oldProjectionPoll = vi.fn();
    const fixedProjectionPoll = vi.fn();
    const stopOld = startCurationWorkspacePolling(true, oldProjectionPoll);
    const stopFixed = startCurationWorkspacePolling(false, fixedProjectionPoll);

    vi.advanceTimersByTime(10_000);

    expect(oldProjectionPoll).toHaveBeenCalledTimes(4);
    expect(fixedProjectionPoll).not.toHaveBeenCalled();
    stopOld();
    stopFixed();
  });

  it("첫 Planning 취소를 Target 0개의 진행 중 화면으로 되돌리지 않는다", () => {
    expect(initialPlanningViewState([planningJob("CANCELLED")])).toBe(
      "CANCELLED",
    );
    expect(initialPlanningViewState([planningJob("FAILED")])).toBe(
      "FAILED",
    );
  });

  it("EFFECT_UNKNOWN Planning을 무한 진행 화면으로 표시하지 않는다", () => {
    expect(
      initialPlanningViewState(
        [planningJob("RUNNING")],
        false,
        "RESULT_CONFIRMATION_REQUIRED",
      ),
    ).toBe("RESULT_CONFIRMATION_REQUIRED");
  });

  it("생성 순서상 최신 Planning Job과 로컬 제출 상태를 우선한다", () => {
    expect(
      initialPlanningViewState([
        planningJob("CANCELLED"),
        planningJob("RUNNING"),
      ]),
    ).toBe("WORKING");
    expect(
      initialPlanningViewState([planningJob("CANCELLED")], true),
    ).toBe("WORKING");
    expect(initialPlanningViewState([])).toBe("IDLE");
  });
});
