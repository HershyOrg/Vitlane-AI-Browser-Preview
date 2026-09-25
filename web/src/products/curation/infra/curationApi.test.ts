// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cancelCurationAction,
  createCurationSelection,
  curationThreadsChangedEvent,
  executeAutoResearchAction,
  executeConversationRequest,
  executeResearchAgainAction,
  executeTargetRemoveAction,
  getAvailableCurationActions,
  getCurationCart,
  isCurationConflictAPIError,
  prepareCandidateConfiguration,
  removeCurationSelection,
  respondToFollowUp,
  retryIntelligenceJob,
  toCurationWorkspaceModel,
  updateCurationSelection,
  type CurationThreadsChangedDetail,
} from "./curationApi";
import type {
  CurationWorkspaceResponse,
  PrepareCandidateConfigurationResponse,
} from "../domain/types";

afterEach(() => {
  sessionStorage.clear();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("curation API", () => {
  it("Auto 요청은 원문·Curation version·동일 client/idempotency ID를 서버에 보낸다", async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(jsonResponse({
      status: "EXECUTED",
      decision: "RESEARCH_AGAIN",
      source: "MANAGED",
      targetId: "target-1",
      reasonCode: "MANAGED_NORMALIZED_TARGET_MATCH",
      replay: false,
    }, 202));
    vi.stubGlobal("fetch", fetchMock);

    await expect(executeAutoResearchAction({
      curationId: "curation/1",
      request: "여행용 어뎁터 좀더 조사해봐",
      expectedCurationVersion: 7,
    })).resolves.toMatchObject({
      status: "EXECUTED",
      decision: "RESEARCH_AGAIN",
      targetId: "target-1",
    });

    const [, init] = fetchMock.mock.calls[0];
    const body = JSON.parse(String(init?.body));
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/curations/curation%2F1/conversation-requests");
    expect(body).toMatchObject({
      request: "여행용 어뎁터 좀더 조사해봐",
      expectedCurationVersion: 7,
    });
    expect(body.clientRequestId).toBe(
      (init?.headers as Record<string, string>)["Idempotency-Key"],
    );
  });

  it("응답 유실 후 버전이 진행해도 원 요청 ID와 snapshot으로 재전송한다", async () => {
    const fetchMock = vi.fn<typeof fetch>()
      .mockRejectedValueOnce(new TypeError("Network response lost"))
      .mockResolvedValueOnce(jsonResponse({status:"EXECUTED", replay:true},202))
      .mockResolvedValueOnce(jsonResponse({status:"EXECUTED", replay:false},202));
    vi.stubGlobal("fetch",fetchMock);
    const input={curationId:"lost-response",request:"더 작은 것으로",expectedCurationVersion:7,expectedConversationVersion:3};
    await expect(executeAutoResearchAction(input)).rejects.toThrow();
    // sessionStorage survives module reload, as it does a browser refresh.
    vi.resetModules();
    const {executeAutoResearchAction: reloaded}=await import("./curationApi");
    await reloaded({...input,expectedCurationVersion:9,expectedConversationVersion:4});
    const first=JSON.parse(String(fetchMock.mock.calls[0][1]?.body));
    expect(JSON.parse(String(fetchMock.mock.calls[1][1]?.body))).toEqual(first);
    await reloaded({...input,expectedCurationVersion:9,expectedConversationVersion:4});
    const next=JSON.parse(String(fetchMock.mock.calls[2][1]?.body));
    expect(next.clientRequestId).not.toBe(first.clientRequestId);
    expect(next.expectedConversationVersion).toBe(4);
  });

  it("스레드에 닿는 호출은 성공·거절·연결 끊김 모두 끝나면 스레드 변경 사건을 한 번 보낸다 (ADR-0081)", async () => {
    const seen: (string | undefined)[] = [];
    const listener = (event: Event) =>
      seen.push((event as CustomEvent<CurationThreadsChangedDetail>).detail.curationId);
    window.addEventListener(curationThreadsChangedEvent, listener);
    try {
      vi.stubGlobal("fetch", vi.fn<typeof fetch>()
        .mockResolvedValueOnce(jsonResponse({ jobId: "job-1" }))
        .mockResolvedValueOnce(jsonResponse({ cancelledJobs: 1 }))
        .mockResolvedValueOnce(jsonResponse({ error: { code: "CURATION_THREAD_ACTIVE", message: "busy" } }, 409))
        .mockRejectedValueOnce(new TypeError("Network response lost")));

      await retryIntelligenceJob("job-1");
      expect(seen).toEqual([undefined]);
      await cancelCurationAction("action-1");
      await expect(executeConversationRequest({
        curationId: "curation-1", expectedCurationVersion: 2, mode: "RESEARCH_AGAIN",
        request: "더 조용한 것", targetId: "target-1",
      })).rejects.toThrow();
      await expect(respondToFollowUp("curation-1", { id: "message-1", version: 1 }, "ACCEPT")).rejects.toThrow();

      expect(seen).toEqual([undefined, undefined, "curation-1", "curation-1"]);
    } finally {
      window.removeEventListener(curationThreadsChangedEvent, listener);
    }
  });

  it("진행 중 action 취소는 action ID를 URL 인코딩해 POST한다", async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse({ cancelledJobs: 2 }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(cancelCurationAction("action/1")).resolves.toEqual({
      cancelledJobs: 2,
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/curation-actions/action%2F1/cancel",
      expect.objectContaining({
        method: "POST",
        credentials: "same-origin",
      }),
    );
  });

  it("기준 충돌 뒤 명시적 재시도는 새 기준을 읽고 새 action으로 제출한다", async () => {
    const mock=vi.fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse({version:1}))
      .mockResolvedValueOnce(new Response(JSON.stringify({error:{code:"RESEARCH_CRITERIA_CHANGED",message:"changed"}}),{status:409,headers:{"Content-Type":"application/json"}}))
      .mockResolvedValueOnce(jsonResponse({version:2}))
      .mockResolvedValueOnce(jsonResponse({round:{id:"new-round",status:"REQUESTED"},replay:false}));
    vi.stubGlobal("fetch",mock);
    const input={curationId:"criteria-curation",targetId:"target",sessionId:"session",feedback:"",expectedCurationVersion:1,expectedSessionVersion:1};
    await expect(executeResearchAgainAction(input)).rejects.toThrow();
    await executeResearchAgainAction(input);
    const first=JSON.parse(String(mock.mock.calls[1][1]?.body)),next=JSON.parse(String(mock.mock.calls[3][1]?.body));
    expect(first.expectedCriteriaVersion).toBe(1);expect(next.expectedCriteriaVersion).toBe(2);expect(next.curationActionId).not.toBe(first.curationActionId);
  });

  it("종결 round의 재조사 replay 응답은 '새 작업 없음' 신호를 표면화한다", async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse({version:1}))
      .mockResolvedValueOnce(jsonResponse({
        round: { id: "round-1", status: "FAILED" },
        replay: true,
      }))
      .mockResolvedValueOnce(jsonResponse({version:1}))
      .mockResolvedValueOnce(jsonResponse({
        round: { id: "round-2", status: "REQUESTED" },
        replay: false,
      }));
    vi.stubGlobal("fetch", fetchMock);

    const replayed = await executeResearchAgainAction({
      curationId: "curation-1", targetId: "target-1", sessionId: "session-1",
      feedback: "더 가벼운 의자", expectedCurationVersion: 3, expectedSessionVersion: 5,
    });
    expect(replayed.replayedTerminalRound).toBe(true);
    expect(replayed.effect.tasks).toEqual([{ roundId: "round-1" }]);

    const fresh = await executeResearchAgainAction({
      curationId: "curation-1", targetId: "target-1", sessionId: "session-1",
      feedback: "더 가벼운 의자로 다시", expectedCurationVersion: 3, expectedSessionVersion: 5,
    });
    expect(fresh.replayedTerminalRound).toBe(false);
    expect(fresh.effect.tasks).toEqual([{ roundId: "round-2" }]);
  });

  it("availableActions와 CartView를 cache 없이 Curation 범위로 조회한다", async () => {
    const actions = {
      curationId: "curation/1",
      phase: "PLANNING",
      version: 3,
      availableActions: [],
    };
    const cart = {
      curationId: "curation/1",
      selections: [],
      total: { amount: "0", currency: "USD" },
      warnings: [],
      updatedAt: "2026-07-31T00:00:00Z",
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse(actions))
      .mockResolvedValueOnce(jsonResponse(cart));
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      getAvailableCurationActions("curation/1"),
    ).resolves.toEqual(actions);
    await expect(getCurationCart("curation/1")).resolves.toEqual(cart);

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/api/v1/curations/curation%2F1/available-actions",
      expect.objectContaining({
        cache: "no-store",
        credentials: "same-origin",
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/api/v1/curations/curation%2F1/cart",
      expect.objectContaining({
        cache: "no-store",
        credentials: "same-origin",
      }),
    );
  });

  it("Selection 생성·교체·soft remove 계약에 client command와 version을 보낸다", async () => {
    const selection = {
      selection: {
        id: "selection-1",
        curationId: "curation-1",
        targetId: "target-1",
        shoppingSessionId: "session-1",
        candidateId: "candidate-1",
        candidateConfigurationId: "configuration-2",
        configurationHash: "sha256:hash",
        quantity: 2,
        version: 2,
        selectedAt: "2026-07-31T00:00:00Z",
        updatedAt: "2026-07-31T00:01:00Z",
      },
      replay: false,
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockImplementation(async () => jsonResponse(selection));
    vi.stubGlobal("fetch", fetchMock);

    const createInput = {
      clientCommandId: "command-create",
      candidateConfigurationId: "configuration-1",
      expectedCurationVersion: 7,
      quantity: 1,
    };
    const updateInput = {
      clientCommandId: "command-update",
      expectedCurationVersion: 7,
      expectedVersion: 1,
      candidateConfigurationId: "configuration-2",
      quantity: 2,
    };
    const removeInput = {
      clientCommandId: "command-remove",
      expectedCurationVersion: 7,
      expectedVersion: 2,
    };
    await createCurationSelection("curation-1", createInput);
    await updateCurationSelection(
      "curation-1",
      "selection-1",
      updateInput,
    );
    await removeCurationSelection(
      "curation-1",
      "selection-1",
      removeInput,
    );

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/api/v1/curations/curation-1/selections",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify(createInput),
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/api/v1/curations/curation-1/selections/selection-1",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify(updateInput),
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      "/api/v1/curations/curation-1/selections/selection-1",
      expect.objectContaining({
        method: "DELETE",
        body: JSON.stringify(removeInput),
      }),
    );
  });

  it("TARGET_REMOVE는 action ID를 만든 뒤 typed owner route만 호출한다", async () => {
    const response = {
      action: { id: "action-1" },
      targetId: "target/1",
      curationVersion: 5,
      removedAt: "2026-07-31T00:02:00Z",
      replay: false,
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse(response, 201));
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      executeTargetRemoveAction({
        curationId: "curation/1",
        targetId: "target/1",
        expectedCurationVersion: 4,
      }),
    ).resolves.toEqual(response);

    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toMatch(
      /^\/api\/v1\/curations\/curation%2F1\/actions\/[0-9a-f-]+\/target-remove$/,
    );
    expect(init).toEqual(
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify({
          targetId: "target/1",
          expectedCurationVersion: 4,
        }),
      }),
    );
  });

  it("CandidateConfiguration 준비 계약을 session과 candidate 범위로 POST한다", async () => {
    const input = {
      fields: [],
      selections: {},
      confirmsNoOptions: true,
    };
    const response = {
      configuration: {
        id: "configuration-1",
        configurationSequence: 1,
        shoppingSessionId: "session/1",
        candidateId: "candidate/1",
        userId: "user-1",
        schemaVersion: "vitlane.candidate-configuration.v1",
        fields: [],
        selections: [],
        confirmsNoOptions: true,
        configurationHash: "sha256:configuration",
        createdAt: "2026-07-31T00:00:00Z",
      },
    } satisfies PrepareCandidateConfigurationResponse;
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse(response, 201));
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      prepareCandidateConfiguration("session/1", "candidate/1", input),
    ).resolves.toEqual(response);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/shopping-sessions/session%2F1/candidates/candidate%2F1/configurations",
      expect.objectContaining({
        method: "POST",
        credentials: "same-origin",
        body: JSON.stringify(input),
      }),
    );
  });


  it("selection version 충돌을 APIError code와 status로 보존한다", async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(
      jsonResponse(
        {
          error: {
            code: "CURATION_SELECTION_VERSION_CONFLICT",
            message: "선택이 변경되었습니다.",
          },
        },
        409,
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    let thrown: unknown;
    try {
      await updateCurationSelection("curation-1", "selection-1", {
        clientCommandId: "22222222-2222-4222-8222-222222222222",
        candidateConfigurationId: "configuration-1",
        expectedCurationVersion: 7,
        quantity: 2,
        expectedVersion: 1,
      });
    } catch (error) {
      thrown = error;
    }

    expect(isCurationConflictAPIError(thrown)).toBe(true);
    if (!isCurationConflictAPIError(thrown)) {
      throw new Error("expected Curation conflict APIError");
    }
    expect(thrown).toMatchObject({
      code: "CURATION_SELECTION_VERSION_CONFLICT",
      status: 409,
      retryable: false,
    });
  });

  it("APPEND 행동만 transcript에 투영하고 PATCH_ONLY hook은 숨긴다", () => {
    const response = {
      plan: {
        id: "plan-1",
        originalIntent: "업무 공간",
      },
      curation: {
        id: "curation-1",
        shoppingPlanId: "plan-1",
        userId: "user-1",
        phase: "CURATING",
        version: 5,
        createdAt: "2026-07-31T00:00:00Z",
        updatedAt: "2026-07-31T00:02:00Z",
      },
      targets: [],
      research: { planId: "plan-1", groups: [] },
      cart: {
        curationId: "curation-1",
        selections: [],
        total: { amount: "0", currency: "USD" },
        warnings: [],
        updatedAt: "2026-07-31T00:02:00Z",
      },
      availableActions: [],
      timeline: [
        {
          action: {
            id: "intent-action",
            curationId: "curation-1",
            type: "INTENT_NEXT_STEP",
            phaseAtRequest: "HAVING_INTENT",
            requestedTransitionTo: "PLANNING",
            subjectType: "INTENT",
            effectKind: "NONE",
            expectedCurationVersion: 1,
            createdAt: "2026-07-31T00:00:00Z",
          },
          displayBody: "업무 공간",
          result: {
            kind: "INTENT_ACCEPTED",
            summary: "Curation 계획을 시작했습니다.",
            occurredAt: "2026-07-31T00:00:01Z",
          },
        },
        {
          action: {
            id: "remove-action",
            curationId: "curation-1",
            type: "TARGET_REMOVE",
            phaseAtRequest: "CURATING",
            subjectType: "TARGET",
            subjectId: "target-1",
            effectKind: "NONE",
            expectedCurationVersion: 4,
            createdAt: "2026-07-31T00:01:00Z",
          },
        },
        {
          action: {
            id: "research-action",
            curationId: "curation-1",
            type: "TARGET_RESEARCH_AGAIN",
            phaseAtRequest: "CURATING",
            subjectType: "TARGET",
            subjectId: "target-1",
            effectKind: "INTELLIGENCE",
            expectedCurationVersion: 5,
            createdAt: "2026-07-31T00:02:00Z",
          },
          result: {
            kind: "TARGET_RESEARCHED",
            summary: "업무용 의자 Target 재조사를 시작했습니다.",
            occurredAt: "2026-07-31T00:02:01Z",
          },
        },
      ],
      latestArtifact: "CURATION_BOARD",
      coverage: "NONE",
      agencyOrderTrace: [],
    } as unknown as CurationWorkspaceResponse;

    const model = toCurationWorkspaceModel(response);
    const actionEntries = model.timeline.filter(
      (entry) => entry.kind === "ACTION",
    );
    expect(actionEntries).toHaveLength(2);
    expect(actionEntries).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          id: "intent-action",
          command: "@Intent-NextStep: 업무 공간",
        }),
        expect.objectContaining({
          id: "research-action",
          command: "@Target:target-1-ResearchAgain",
        }),
      ]),
    );
    expect(model.timeline.map(({ id }) => id)).toEqual([
      "intent-action",
      "intent-action:result",
      "research-action",
      "research-action:result",
      "curation-1:artifact:5",
    ]);
    expect(model.timeline[1]).toMatchObject({
      kind: "RESULT",
      summary: "Curation 계획을 시작했습니다.",
      artifact: {
        title: "Intent 반영",
      },
    });
  });
});

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
