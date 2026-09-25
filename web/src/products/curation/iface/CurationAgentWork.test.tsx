// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { IntelligenceJob } from "../domain/types";
import { CurationAgentWork } from "./CurationAgentWork";

const actions = vi.hoisted(() => ({
  retry: vi.fn(),
  cancel: vi.fn(),
}));

vi.mock("../infra/curationApi", () => ({
  retryIntelligenceJob: (jobId: string) => actions.retry(jobId),
  cancelCurationAction: (actionId: string) => actions.cancel(actionId),
}));

function runningJob(): IntelligenceJob {
  return {
    jobId: "job-1",
    actionId: "action-1",
    targetKind: "PLANNING_TASK",
    targetId: "task-1",
    provider: "MANAGED",
    status: "RUNNING",
    retryable: false,
    attempt: 1,
    steps: [
      {
        id: "s1",
        kind: "INTERPRETING",
        status: "SUCCEEDED",
        startedAt: "2026-08-07T00:00:00Z",
      },
      {
        id: "s2",
        kind: "SUBMITTING",
        status: "RUNNING",
        startedAt: "2026-08-07T00:00:01Z",
      },
    ],
  };
}

async function render(element: React.ReactElement) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => root.render(element));
  return container;
}

describe("CurationAgentWork", () => {
  it("keeps queued work separate from old completed steps and shows cancellation failures", async () => {
    const job = { ...runningJob(), status: "PENDING" as const };
    actions.cancel.mockRejectedValue(new Error("failed"));
    const view = await render(<CurationAgentWork jobs={[job]} />);
    expect(view.querySelector('[role="status"]')?.textContent).toContain("조사 대기 중");
    expect(view.querySelector('[role="status"]')?.textContent).not.toContain("결과 정리 중");
    await act(async () => view.querySelector<HTMLButtonElement>(".curation-cancel-action")!.click());
    expect(view.textContent).toContain("조사를 취소하지 못했습니다");
    expect(actions.cancel).toHaveBeenCalledWith("action-1");
  });
  afterEach(() => {
    document.body.innerHTML = "";
    actions.retry.mockReset();
    actions.cancel.mockReset();
  });

  it("shows observed steps immediately with one running spinner and no duplicate summary", async () => {
    const container = await render(<CurationAgentWork jobs={[runningJob()]} />);
    expect(container.querySelectorAll(".curation-step")).toHaveLength(2);
    expect(container.querySelectorAll(".curation-step-spinner")).toHaveLength(1);
    expect(container.querySelector("[aria-expanded]")).toBeNull();
    expect(container.querySelectorAll("[role=status]")).toHaveLength(1);
    expect(container.textContent).toContain("요청 해석");
    expect(container.textContent?.match(/결과 정리 중/g)).toHaveLength(1);
    expect(container.textContent).not.toContain("시도 1");
    expect(actions.retry).not.toHaveBeenCalled();
  });

  it("진행 중인 조사는 action 전체를 언제든 취소할 수 있다", async () => {
    actions.cancel.mockResolvedValue({ cancelledJobs: 2 });
    const changed = vi.fn();
    const container = await render(
      <CurationAgentWork jobs={[runningJob()]} onWorkChanged={changed} />,
    );
    const button = Array.from(container.querySelectorAll("button")).find(
      (candidate) => candidate.textContent?.includes("취소"),
    );
    await act(async () => button?.click());
    expect(actions.cancel).toHaveBeenCalledWith("action-1");
    expect(changed).toHaveBeenCalled();
  });

  it("EFFECT_UNKNOWN은 진행 spinner나 재시도 대신 결과 확인 안내를 보여준다", async () => {
    actions.cancel.mockResolvedValue({ cancelledJobs: 1 });
    const changed = vi.fn();
    const container = await render(
      <CurationAgentWork
        jobs={[runningJob()]}
        activeWork={{
          workTargetId: "task-1",
          label: "Target 계획",
          status: "RESULT_CONFIRMATION_REQUIRED",
        }}
        onWorkChanged={changed}
      />,
    );
    expect(container.textContent).toContain("조사 결과 확인 필요");
    expect(container.textContent).toContain("안전하게 중단했습니다");
    expect(container.textContent).not.toContain("행동 중");
    expect(container.textContent).toContain("취소");
    expect(container.textContent).not.toContain("다시 시도");
    const button = container.querySelector("button");
    await act(async () => button?.click());
    expect(actions.cancel).toHaveBeenCalledWith("action-1");
    expect(changed).toHaveBeenCalled();
  });

  it("재시도 가능한 실패는 이유와 함께 다시 시도를 제공한다", async () => {
    actions.retry.mockResolvedValue({ jobId: "job-1" });
    const changed = vi.fn();
    const failed: IntelligenceJob = {
      ...runningJob(),
      status: "FAILED",
      failureCode: "PROVIDER_RESPONSE_INVALID",
      retryable: true,
    };
    const container = await render(
      <CurationAgentWork jobs={[failed]} onWorkChanged={changed} />,
    );
    expect(container.textContent).toContain(
      "조사 결과를 해석하지 못했습니다.",
    );
    const button = container.querySelector("button");
    await act(async () => button?.click());
    expect(actions.retry).toHaveBeenCalledWith("job-1");
    expect(changed).toHaveBeenCalled();
    expect(container.textContent).toContain(
      "아래에서 다음 행동을 선택해 주세요.",
    );
  });

  it("재시도할 수 없는 실패에는 버튼을 만들지 않는다", async () => {
    const failed: IntelligenceJob = {
      ...runningJob(),
      status: "FAILED",
      failureCode: "QUOTA_EXCEEDED",
      retryable: false,
    };
    const container = await render(<CurationAgentWork jobs={[failed]} />);
    expect(container.textContent).toContain(
      "오늘 사용 가능한 조사량을 모두 사용했습니다.",
    );
    expect(container.querySelector("button")).toBeNull();
  });

  it("종결된 Job만 있으면 아무것도 그리지 않는다", async () => {
    const done: IntelligenceJob = { ...runningJob(), status: "SUCCEEDED" };
    const container = await render(<CurationAgentWork jobs={[done]} />);
    expect(container.textContent).toBe("");
  });

  it("과거 실패는 더 새로운 성공 뒤에 다시 나타나지 않는다", async () => {
    const oldFailed: IntelligenceJob = {
      ...runningJob(),
      jobId: "job-old",
      status: "FAILED",
      failureCode: "PROVIDER_RESPONSE_INVALID",
      retryable: true,
    };
    const newerSucceeded: IntelligenceJob = {
      ...runningJob(),
      jobId: "job-new",
      status: "SUCCEEDED",
    };
    const container = await render(
      <CurationAgentWork jobs={[oldFailed, newerSucceeded]} />,
    );
    expect(container.textContent).toBe("");
  });

  it("가장 최신 Job이 실패면 그 실패를 보여준다", async () => {
    const olderSucceeded: IntelligenceJob = {
      ...runningJob(),
      jobId: "job-old",
      status: "SUCCEEDED",
    };
    const newestFailed: IntelligenceJob = {
      ...runningJob(),
      jobId: "job-new",
      status: "FAILED",
      failureCode: "QUOTA_EXCEEDED",
      retryable: false,
    };
    const container = await render(
      <CurationAgentWork jobs={[olderSucceeded, newestFailed]} />,
    );
    expect(container.textContent).toContain("행동을 완료하지 못했습니다");
    expect(container.textContent).toContain(
      "오늘 사용 가능한 조사량을 모두 사용했습니다.",
    );
  });
});
