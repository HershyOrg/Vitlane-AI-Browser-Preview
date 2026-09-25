// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CurationThread } from "../domain/thread";
import type { CurationWorkspaceModel, FollowUpMessage } from "../domain/types";

// Request turns (ADR-0089): the transcript reads the request Threads from the provider.
const threadState = vi.hoisted(() => ({ threads: [] as CurationThread[], active: undefined as CurationThread | undefined }));
vi.mock("../app/useThreads", () => ({
  useCurationThreads: () => ({ threads: threadState.threads, active: threadState.active, busy: Boolean(threadState.active), reload: async () => undefined }),
}));
vi.mock("../app/useBackgroundResearch", () => ({
  useBackgroundResearch: () => ({ view: undefined, refresh: () => undefined }),
  discoveryResponses: () => [],
}));

import { CurationWorkspace } from "./CurationWorkspace";

const workspace: CurationWorkspaceModel = {
  curation: { id: "curation-1", title: "필기구", phase: "CURATING", version: 4, coverage: "PARTIAL", updatedAt: "2026-09-23T03:00:00Z" },
  curations: [],
  availableActions: [],
  timeline: [{
    id: "result-1", kind: "RESULT", createdAt: "2026-09-23T01:00:00Z", summary: "조사를 시작했습니다.",
    artifact: { id: "artifact-1", kind: "CURATION_BOARD", title: "큐레이션 결과", updatedAt: "2026-09-23T01:00:00Z" },
  }],
  cart: { curationId: "curation-1", selections: [], total: { amount: "0", currency: "KRW" }, warnings: [] },
} as unknown as CurationWorkspaceModel;

const researchAction = (threadId: string, status: CurationThread["actions"][number]["status"]) => ({
  id: `${threadId}:research`, threadId, sequence: 0, type: "START_RESEARCH", status, decisionIds: [], decisions: [], answers: [], effects: [],
  jobs: [{ jobId: `${threadId}:job`, actionId: `${threadId}:research`, kind: "RESEARCH_ROUND", targetId: "pen", status: status === "SUCCEEDED" ? "SUCCEEDED" : "RUNNING", effects: [] }],
});
const thread = (id: string, createdAt: string, status: string, request: string): CurationThread => ({
  schemaVersion: "vitlane.curation-thread.v2", id, curationId: "curation-1", mode: "AUTO", origin: "REQUEST", request, revision: 1, status,
  createdAt, updatedAt: createdAt, targetLabels: { pen: "만년필" },
  actions: [researchAction(id, status === "SUCCEEDED" ? "SUCCEEDED" : "RUNNING")] as CurationThread["actions"],
});
const message = (id: string, responseId: string, status: FollowUpMessage["status"]): FollowUpMessage => ({
  id, responseId, kind: "ERROR", status, version: 1, createdAt: "2026-09-23T02:05:00Z",
  content: { code: "RESEARCH_FAILED", targetTitle: "만년필" },
});

describe("request turns", () => {
  let container: HTMLDivElement | undefined; let root: Root | undefined;
  afterEach(async () => {
    if (root) await act(async () => root!.unmount());
    container?.remove(); container = undefined; root = undefined;
    threadState.threads = []; threadState.active = undefined;
  });
  const mount = async (model: CurationWorkspaceModel) => {
    container ??= document.body.appendChild(document.createElement("div"));
    root ??= createRoot(container);
    await act(async () => root!.render(<CurationWorkspace workspace={model} onFollowUpResponse={() => undefined}
      renderArtifact={(_artifact, context) => <><div data-testid="artifact" />{context.conversationTail}</>} />));
    return container;
  };

  it("keeps every request as one turn at its time, running or finished, and leaves the live area to what answers no request", async () => {
    const done = thread("first", "2026-09-23T02:00:00Z", "SUCCEEDED", "만년필 찾아줘");
    const running = thread("second", "2026-09-23T02:30:00Z", "RUNNING", "잉크도 찾아줘");
    threadState.threads = [running, done]; threadState.active = running;
    const view = await mount(workspace);

    const rows = [...view.querySelectorAll(".curation-transcript > li")];
    const turns = rows.filter(row => row.classList.contains("curation-transcript__row--thread"));
    expect(turns.map(row => row.querySelector("[data-thread-id]")!.getAttribute("data-thread-id"))).toEqual(["first", "second"]);
    // The running request already stands in its own place, not in the live tail under the results.
    expect(turns[1].querySelector("[data-thread-outcome=\"ACTIVE\"] .curation-thread__request")!.textContent).toBe("나잉크도 찾아줘");
    expect(rows.at(-1)!.classList.contains("curation-transcript__row--live")).toBe(true);
    expect(rows.at(-1)!.querySelector(".curation-thread__request")).toBeNull();

    // When it ends it stays where it was.
    const ended = { ...running, status: "SUCCEEDED", actions: [researchAction("second", "SUCCEEDED")] as CurationThread["actions"] };
    threadState.threads = [ended, done]; threadState.active = undefined;
    await mount(workspace);
    const after = [...view.querySelectorAll(".curation-transcript > li")];
    expect(after.indexOf(view.querySelector("[data-thread-id=\"second\"]")!.closest("li")!)).toBe(rows.indexOf(turns[1]));
  });

  it("shows a message about a request inside that request's turn and keeps its shape when it no longer waits", async () => {
    const failed = thread("first", "2026-09-23T02:00:00Z", "FAILED", "만년필 찾아줘");
    threadState.threads = [failed];
    const conversation = (status: FollowUpMessage["status"]) => ({
      schemaVersion: "vitlane.curation-conversation.v1" as const, version: 1, unfinished: false, requests: [],
      messages: [message("about-first", "first", status), message("about-nothing", "no-request", "PENDING")],
    });
    const view = await mount({ ...workspace, conversation: conversation("PENDING") });

    const turn = view.querySelector("[data-thread-id=\"first\"]")!;
    const inTurn = turn.querySelector("[data-message-id=\"about-first\"]")!;
    expect(inTurn).not.toBeNull();
    expect(inTurn.querySelector("button")!.textContent).toBe("확인");
    // A message that answers no request still waits at the tail, after the results.
    const tail = view.querySelector("[data-message-id=\"about-nothing\"]")!;
    expect(view.querySelector("[data-testid=\"artifact\"]")!.compareDocumentPosition(tail) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(turn.contains(tail)).toBe(false);

    // Superseded by the next request: same place, the buttons become a word.
    await mount({ ...workspace, conversation: conversation("SUPERSEDED") });
    const settled = view.querySelector("[data-thread-id=\"first\"] [data-message-id=\"about-first\"]")!;
    expect(settled.querySelector("button")).toBeNull();
    expect(settled.querySelector(".curation-follow-up__status")!.textContent).toBe("다음 요청으로 넘어갔어요");
  });
});
