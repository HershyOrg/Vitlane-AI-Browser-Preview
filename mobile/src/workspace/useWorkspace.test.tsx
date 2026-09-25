import { act, renderHook, waitFor } from "@testing-library/react-native";
import { AppState } from "react-native";

import type { CommerceGateway } from "../commerce";
import type {
  BudgetChangePreview,
  BrowserRunView,
  CandidateView,
  CommandContext,
  PlanView,
  QuestionAnswerInput,
  WorkspaceView,
} from "../domain";
import {
  MemoryWorkspaceRecoveryStore,
  type WorkspaceRecoveryStore,
  type WorkspaceRecoverySnapshot,
} from "./WorkspaceRecoveryStore";
import { useWorkspace } from "./useWorkspace";

const workspace: WorkspaceView = {
  id: "curation-1",
  mode: "fixture",
  title: { "ko-KR": "준비", "en-US": "Setup" },
  intent: "의자 찾아줘",
  revision: 1,
  notice: { "ko-KR": "", "en-US": "" },
  sourceLabel: { "ko-KR": "", "en-US": "" },
  conditions: [],
  questions: [],
  answers: [],
  activeQuestionId: null,
  plan: {
    id: "plan-1",
    revision: 1,
    items: [],
    shippingByMerchant: {},
    totals: {
      subtotal: { currency: "KRW", amount: 0 },
      knownShipping: { currency: "KRW", amount: 0 },
      knownTotal: { currency: "KRW", amount: 0 },
      totalState: "COMPLETE",
      hasUnknownFees: false,
      unknownMerchantIds: [],
      budget: { currency: "KRW", amount: 100_000 },
      remainingBudget: { currency: "KRW", amount: 100_000 },
      overBudget: { currency: "KRW", amount: 0 },
      budgetComparisonState: "AVAILABLE",
      budgetState: "LIMITED",
    },
  },
  budgetOptions: [],
  followUpSuggestions: [],
  candidates: [],
  activeCandidateIds: [],
  selectedCandidateIds: [],
  lastChange: [],
};

function gateway(createWorkspace: jest.Mock): CommerceGateway {
  return {
    mode: "fixture",
    createWorkspace,
    getWorkspace: async () => workspace,
    getPlan: async (): Promise<PlanView> => workspace.plan,
    getCandidates: async (): Promise<readonly CandidateView[]> => [],
    startBrowserRun: async () => { throw new Error("unused"); },
    answerQuestion: async (
      _workspaceId: string,
      _answer: QuestionAnswerInput,
      _context: CommandContext,
    ) => workspace,
    previewBudget: async (): Promise<BudgetChangePreview> => {
      throw new Error("unused");
    },
    applyBudgetPreview: async () => workspace,
    submitFollowUp: async () => workspace,
    respondToAgentMessage: async () => workspace,
    cancelResearchSubscription: async () => workspace,
    hideResearchFinding: async () => workspace,
    importResearchFinding: async () => workspace,
    updateShoppingPreferences: async () => workspace,
  };
}

describe("useWorkspace recovery", () => {
  it("replays a lost create response with the original idempotency key", async () => {
    const store = new MemoryWorkspaceRecoveryStore();
    await store.save({
      schemaVersion: 1,
      pendingCreation: {
        kind: "create_workspace",
        idempotencyKey: "11111111-1111-4111-8111-111111111111",
        intent: "의자 찾아줘",
        createdAt: "2026-09-24T00:00:00.000Z",
      },
    });
    const create = jest.fn(async () => workspace);
    const stableGateway = gateway(create);
    const { result } = await renderHook(() => useWorkspace({
      gateway: stableGateway,
      recoveryStore: store,
      now: () => Date.parse("2026-09-24T01:00:00.000Z"),
    }));

    await waitFor(() => expect(result.current.restoring).toBe(false));
    expect(result.current.workspace?.id).toBe(workspace.id);
    expect(create).toHaveBeenCalledWith("의자 찾아줘", {
      idempotencyKey: "11111111-1111-4111-8111-111111111111",
      baseRevision: 0,
    });
    await expect(store.load()).resolves.toEqual({
      schemaVersion: 1,
      workspaceId: workspace.id,
    });
  });

  it("persists the command before creating a workspace", async () => {
    const store = new MemoryWorkspaceRecoveryStore();
    let snapshotAtRequest: unknown;
    const create = jest.fn(async () => {
      snapshotAtRequest = await store.load();
      return workspace;
    });
    const stableGateway = gateway(create);
    const { result } = await renderHook(() => useWorkspace({
      gateway: stableGateway,
      recoveryStore: store,
      commandIdFactory: () => "22222222-2222-4222-8222-222222222222",
      now: () => Date.parse("2026-09-24T02:00:00.000Z"),
    }));
    await waitFor(() => expect(result.current.restoring).toBe(false));

    await act(async () => {
      await result.current.createWorkspace(" 의자 찾아줘 ");
    });

    expect(snapshotAtRequest).toEqual({
      schemaVersion: 1,
      pendingCreation: expect.objectContaining({
        idempotencyKey: "22222222-2222-4222-8222-222222222222",
        intent: "의자 찾아줘",
      }),
    });
    await expect(store.load()).resolves.toEqual({
      schemaVersion: 1,
      workspaceId: workspace.id,
    });
  });

  it("does not send a create request when durable replay metadata cannot be saved", async () => {
    const store: WorkspaceRecoveryStore = {
      load: async () => null,
      save: async () => { throw new Error("storage unavailable"); },
      clear: async () => undefined,
    };
    const create = jest.fn(async () => workspace);
    const stableGateway = gateway(create);
    const { result } = await renderHook(() => useWorkspace({
      gateway: stableGateway,
      recoveryStore: store,
      commandIdFactory: () => "33333333-3333-4333-8333-333333333333",
    }));
    await waitFor(() => expect(result.current.restoring).toBe(false));

    await act(async () => {
      await expect(result.current.createWorkspace("의자 찾아줘")).resolves.toBeNull();
    });

    expect(create).not.toHaveBeenCalled();
    expect(result.current.error).toBe("storage unavailable");
  });

  it("locks creation before saving so rapid taps cannot replace replay metadata", async () => {
    let releaseSave: () => void = () => {};
    const saveGate = new Promise<void>((resolve) => { releaseSave = resolve; });
    let saveCount = 0;
    let saved: WorkspaceRecoverySnapshot | null = null;
    const store: WorkspaceRecoveryStore = {
      load: async () => null,
      save: async (snapshot) => {
        saveCount += 1;
        saved = snapshot;
        if (snapshot.pendingCreation) await saveGate;
      },
      clear: async () => undefined,
    };
    const create = jest.fn(async () => workspace);
    const stableGateway = gateway(create);
    const { result } = await renderHook(() => useWorkspace({
      gateway: stableGateway,
      recoveryStore: store,
      commandIdFactory: () => "44444444-4444-4444-8444-444444444444",
    }));
    await waitFor(() => expect(result.current.restoring).toBe(false));

    await act(async () => {
      const first = result.current.createWorkspace("첫 요청");
      const second = result.current.createWorkspace("두 번째 요청");
      releaseSave();
      await expect(Promise.all([first, second])).resolves.toEqual([workspace, null]);
    });

    expect(create).toHaveBeenCalledTimes(1);
    expect(create).toHaveBeenCalledWith("첫 요청", expect.any(Object));
    expect(saveCount).toBe(2);
    expect(saved).toEqual({ schemaVersion: 1, workspaceId: workspace.id });
  });

  it("reuses the durable create key when the same screen retries an ambiguous failure", async () => {
    const store = new MemoryWorkspaceRecoveryStore();
    const create = jest.fn()
      .mockRejectedValueOnce(new Error("projection timeout"))
      .mockResolvedValueOnce(workspace);
    const ids = jest.fn()
      .mockReturnValueOnce("55555555-5555-4555-8555-555555555555")
      .mockReturnValueOnce("66666666-6666-4666-8666-666666666666");
    const stableGateway = gateway(create);
    const { result } = await renderHook(() => useWorkspace({
      gateway: stableGateway,
      recoveryStore: store,
      commandIdFactory: ids,
      now: () => Date.parse("2026-09-24T03:00:00.000Z"),
    }));
    await waitFor(() => expect(result.current.restoring).toBe(false));

    await act(async () => {
      await expect(result.current.createWorkspace("의자 찾아줘")).resolves.toBeNull();
      await expect(result.current.createWorkspace("의자 찾아줘")).resolves.toEqual(workspace);
    });

    expect(create).toHaveBeenNthCalledWith(1, "의자 찾아줘", {
      idempotencyKey: "55555555-5555-4555-8555-555555555555",
      baseRevision: 0,
    });
    expect(create).toHaveBeenNthCalledWith(2, "의자 찾아줘", {
      idempotencyKey: "55555555-5555-4555-8555-555555555555",
      baseRevision: 0,
    });
    expect(ids).toHaveBeenCalledTimes(1);
  });

  it("labels recovery when a new draft is submitted while an earlier create is unresolved", async () => {
    const store = new MemoryWorkspaceRecoveryStore();
    await store.save({
      schemaVersion: 1,
      pendingCreation: {
        kind: "create_workspace",
        idempotencyKey: "77777777-7777-4777-8777-777777777777",
        intent: "이전 요청",
        createdAt: "2026-09-24T03:00:00.000Z",
      },
    });
    const create = jest.fn(async () => workspace);
    const stableGateway = gateway(create);
    const { result } = await renderHook(() => useWorkspace({
      gateway: stableGateway,
      recoveryStore: store,
      now: () => Date.parse("2026-09-24T04:00:00.000Z"),
    }));
    await waitFor(() => expect(result.current.restoring).toBe(false));

    // The mount restore consumes a pending create, so put the ambiguous record
    // back to exercise a same-screen draft change explicitly.
    await store.save({
      schemaVersion: 1,
      pendingCreation: {
        kind: "create_workspace",
        idempotencyKey: "88888888-8888-4888-8888-888888888888",
        intent: "복구할 이전 요청",
        createdAt: "2026-09-24T03:30:00.000Z",
      },
    });

    let recovered!: WorkspaceView | null;
    await act(async () => {
      recovered = await result.current.createWorkspace("새 요청");
    });

    expect(create).toHaveBeenLastCalledWith("복구할 이전 요청", {
      idempotencyKey: "88888888-8888-4888-8888-888888888888",
      baseRevision: 0,
    });
    expect(recovered?.lastChange[0]?.["ko-KR"]).toContain("이전 요청");
  });
});

describe("useWorkspace BrowserRun launch", () => {
  it("reuses both idempotency keys when a launch response is uncertain", async () => {
    const store = new MemoryWorkspaceRecoveryStore();
    await store.save({ schemaVersion: 1, workspaceId: workspace.id });
    const approved: BrowserRunView = {
      id: "f1111111-1111-4111-8111-111111111111",
      curationId: workspace.id,
      candidateId: "candidate-1",
      productUrl: "https://merchant.example.test/products/canonical",
      merchantOrigin: "https://merchant.example.test",
      merchantHost: "merchant.example.test",
      state: "NAVIGATION_APPROVED",
      controlOwner: "NONE",
      version: 2,
    };
    const startBrowserRun = jest.fn()
      .mockRejectedValueOnce(new Error("response lost"))
      .mockResolvedValueOnce(approved);
    const stableGateway: CommerceGateway = {
      ...gateway(jest.fn()),
      mode: "server",
      startBrowserRun,
    };
    const ids = jest.fn(() => "11111111-1111-4111-8111-111111111111");
    const { result, unmount } = await renderHook(() => useWorkspace({
      gateway: stableGateway,
      recoveryStore: store,
      commandIdFactory: ids,
    }));
    await waitFor(() => expect(result.current.restoring).toBe(false));

    await act(async () => {
      await expect(result.current.startBrowserRun("candidate-1")).resolves.toBeNull();
    });
    await act(async () => {
      await expect(result.current.startBrowserRun("candidate-1")).resolves.toEqual(approved);
    });

    expect(startBrowserRun).toHaveBeenCalledTimes(2);
    expect(startBrowserRun.mock.calls[0]).toEqual(startBrowserRun.mock.calls[1]);
    expect(startBrowserRun).toHaveBeenLastCalledWith(workspace.id, "candidate-1", {
      createIdempotencyKey: "browser-run:create:11111111-1111-4111-8111-111111111111",
      navigationApprovalIdempotencyKey: "browser-run:navigate:11111111-1111-4111-8111-111111111111",
    });
    expect(ids).toHaveBeenCalledTimes(1);
    unmount();
  });
});

describe("useWorkspace background research polling", () => {
  const originalCurrentState = Object.getOwnPropertyDescriptor(AppState, "currentState");
  const activeResearchWorkspace: WorkspaceView = {
    ...workspace,
    mode: "server",
    processing: {
      status: "SUCCEEDED",
      label: "Ready",
      shouldPoll: false,
    },
    backgroundResearch: {
      availability: "available",
      subscriptions: [{
        id: "subscription-1",
        targetId: "target-1",
        targetTitle: "의자",
        status: "ACTIVE",
        country: "KR",
        currency: "KRW",
        maximum: { currency: "KRW", amount: 100_000 },
        keywords: ["의자"],
        expiresAt: "2026-10-01T00:00:00.000Z",
        createdAt: "2026-09-24T00:00:00.000Z",
      }],
      findings: [],
    },
  };

  afterEach(() => {
    if (originalCurrentState) {
      Object.defineProperty(AppState, "currentState", originalCurrentState);
    }
    jest.useRealTimers();
  });

  it("refreshes an active server-side monitor on the background interval while the app is active", async () => {
    jest.useFakeTimers();
    Object.defineProperty(AppState, "currentState", {
      configurable: true,
      value: "active",
    });
    expect(AppState.currentState).toBe("active");
    const store = new MemoryWorkspaceRecoveryStore();
    await store.save({ schemaVersion: 1, workspaceId: workspace.id });
    const getWorkspace = jest.fn(async () => activeResearchWorkspace);
    const serverGateway: CommerceGateway = {
      ...gateway(jest.fn()),
      mode: "server",
      getWorkspace,
    };
    const { result, unmount } = await renderHook(() => useWorkspace({
      gateway: serverGateway,
      recoveryStore: store,
      backgroundPollIntervalMilliseconds: 5_000,
    }));

    await act(async () => {
      await flushMicrotasks();
    });
    expect(result.current.restoring).toBe(false);
    expect(getWorkspace).toHaveBeenCalledTimes(1);

    await act(async () => {
      jest.advanceTimersByTime(4_999);
      await flushMicrotasks();
    });
    expect(getWorkspace).toHaveBeenCalledTimes(1);

    await act(async () => {
      jest.advanceTimersByTime(1);
      await flushMicrotasks();
    });
    expect(getWorkspace).toHaveBeenCalledTimes(2);
    unmount();
  });

  it("does not schedule monitor polling for fixture workspaces", async () => {
    jest.useFakeTimers();
    const store = new MemoryWorkspaceRecoveryStore();
    await store.save({ schemaVersion: 1, workspaceId: workspace.id });
    const fixtureWorkspace: WorkspaceView = { ...activeResearchWorkspace, mode: "fixture" };
    const getWorkspace = jest.fn(async () => fixtureWorkspace);
    const fixtureGateway: CommerceGateway = {
      ...gateway(jest.fn()),
      getWorkspace,
    };
    const { result, unmount } = await renderHook(() => useWorkspace({
      gateway: fixtureGateway,
      recoveryStore: store,
      backgroundPollIntervalMilliseconds: 5_000,
    }));

    await act(async () => {
      await flushMicrotasks();
    });
    expect(result.current.restoring).toBe(false);

    await act(async () => {
      jest.advanceTimersByTime(15_000);
      await flushMicrotasks();
    });
    expect(getWorkspace).toHaveBeenCalledTimes(1);
    unmount();
  });
});

describe("useWorkspace ambiguous server mutation recovery", () => {
  const commandId = "99999999-9999-4999-8999-999999999999";
  const now = () => Date.parse("2026-09-24T05:00:00.000Z");
  const serverWorkspace: WorkspaceView = {
    ...workspace,
    mode: "server",
    agentMessages: [{
      id: "proposal-1",
      kind: "PROPOSAL",
      status: "PENDING",
      version: 3,
      code: "SUBSCRIBE_DEALS",
      title: { "ko-KR": "딜을 지켜볼까요?", "en-US": "Watch for this deal?" },
      body: "이 조건에 맞는 딜을 7일 동안 지켜볼까요?",
      contentLocale: "ko-KR",
      createdAt: "2026-09-24T04:59:00.000Z",
    }],
    processing: {
      status: "SUCCEEDED",
      label: "Ready",
      shouldPoll: false,
    },
  };
  const answer: QuestionAnswerInput = {
    questionId: "question-1",
    selectedOptionIds: ["option-1"],
    disposition: "apply",
  };
  const preview: BudgetChangePreview = {
    id: "budget-preview:local-1",
    workspaceId: workspace.id,
    baseRevision: workspace.revision,
    proposedRevision: workspace.revision,
    requestedBudget: { currency: "KRW", amount: 120_000 },
    before: workspace.plan.totals,
    projectedPlan: {
      ...workspace.plan,
      totals: {
        ...workspace.plan.totals,
        budget: { currency: "KRW", amount: 120_000 },
        remainingBudget: { currency: "KRW", amount: 120_000 },
      },
    },
    changes: [],
  };

  async function mountedController(overrides: Partial<CommerceGateway>) {
    const store = new MemoryWorkspaceRecoveryStore();
    await store.save({ schemaVersion: 1, workspaceId: workspace.id });
    const base = gateway(jest.fn(async () => serverWorkspace));
    const serverGateway: CommerceGateway = {
      ...base,
      mode: "server",
      getWorkspace: async () => serverWorkspace,
      ...overrides,
    };
    const ids = jest.fn(() => commandId);
    const hook = await renderHook(() => useWorkspace({
      gateway: serverGateway,
      recoveryStore: store,
      commandIdFactory: ids,
      now,
    }));
    await waitFor(() => expect(hook.result.current.restoring).toBe(false));
    return { ...hook, store, ids };
  }

  it("keeps the answer command UUID until a lost response is canonically reconciled", async () => {
    const answered: WorkspaceView = {
      ...serverWorkspace,
      answers: [{
        questionId: answer.questionId,
        selectedOptionIds: answer.selectedOptionIds,
        committedAtRevision: 1,
        origin: "user",
      }],
    };
    const submit = jest.fn()
      .mockRejectedValueOnce(new Error("answer response timed out after commit"))
      .mockResolvedValueOnce(answered);
    const { result, store, ids, unmount } = await mountedController({ answerQuestion: submit });

    await act(async () => {
      await expect(result.current.answerQuestion(answer)).resolves.toBeNull();
    });
    await expect(store.load()).resolves.toEqual(expect.objectContaining({
      pendingCommand: expect.objectContaining({
        kind: "answer_question",
        idempotencyKey: commandId,
        answer,
      }),
    }));

    await act(async () => {
      await expect(result.current.answerQuestion(answer)).resolves.toEqual(answered);
    });

    expect(submit).toHaveBeenCalledTimes(2);
    expect(submit.mock.calls.map((call) => call[2])).toEqual([
      { idempotencyKey: commandId, baseRevision: 1 },
      { idempotencyKey: commandId, baseRevision: 1 },
    ]);
    expect(ids).toHaveBeenCalledTimes(1);
    await expect(store.load()).resolves.toEqual({ schemaVersion: 1, workspaceId: workspace.id });
    unmount();
  });

  it("reuses the exact budget preview command after a committed PATCH response is lost", async () => {
    const budgetChanged: WorkspaceView = {
      ...serverWorkspace,
      plan: preview.projectedPlan,
    };
    const apply = jest.fn()
      .mockRejectedValueOnce(new Error("budget response timed out after commit"))
      .mockResolvedValueOnce(budgetChanged);
    const { result, store, ids, unmount } = await mountedController({ applyBudgetPreview: apply });

    await act(async () => {
      await expect(result.current.applyBudgetPreview(preview)).resolves.toBeNull();
      await expect(result.current.applyBudgetPreview(preview)).resolves.toEqual(budgetChanged);
    });

    expect(apply.mock.calls).toEqual([
      [workspace.id, preview.id, { idempotencyKey: commandId, baseRevision: 1 }],
      [workspace.id, preview.id, { idempotencyKey: commandId, baseRevision: 1 }],
    ]);
    expect(ids).toHaveBeenCalledTimes(1);
    await expect(store.load()).resolves.toEqual({ schemaVersion: 1, workspaceId: workspace.id });
    unmount();
  });

  it("reuses follow-up clientRequestId and base revision after response loss", async () => {
    const submitted = { ...serverWorkspace, lastChange: [{ "ko-KR": "반영", "en-US": "Applied" }] };
    const followUp = jest.fn()
      .mockRejectedValueOnce(new Error("follow-up response timed out after commit"))
      .mockResolvedValueOnce(submitted);
    const { result, store, ids, unmount } = await mountedController({ submitFollowUp: followUp });

    await act(async () => {
      await expect(result.current.submitFollowUp(" 더 저렴하게 ")).resolves.toBeNull();
      await expect(result.current.submitFollowUp("더 저렴하게")).resolves.toEqual(submitted);
    });

    expect(followUp.mock.calls).toEqual([
      [workspace.id, "더 저렴하게", { idempotencyKey: commandId, baseRevision: 1 }],
      [workspace.id, "더 저렴하게", { idempotencyKey: commandId, baseRevision: 1 }],
    ]);
    expect(ids).toHaveBeenCalledTimes(1);
    await expect(store.load()).resolves.toEqual({ schemaVersion: 1, workspaceId: workspace.id });
    unmount();
  });

  it("reuses the durable proposal-response command after an ambiguous response loss", async () => {
    const accepted: WorkspaceView = {
      ...serverWorkspace,
      agentMessages: serverWorkspace.agentMessages?.map((message) => ({
        ...message,
        status: "ACCEPTED" as const,
        version: 4,
      })),
    };
    const respond = jest.fn()
      .mockRejectedValueOnce(new Error("proposal response timed out after commit"))
      .mockResolvedValueOnce(accepted);
    const { result, store, ids, unmount } = await mountedController({
      respondToAgentMessage: respond,
    });

    await act(async () => {
      await expect(result.current.respondToAgentMessage(
        "proposal-1",
        3,
        "ACCEPT",
      )).resolves.toBeNull();
    });
    await expect(store.load()).resolves.toEqual(expect.objectContaining({
      pendingCommand: expect.objectContaining({
        kind: "respond_agent_message",
        idempotencyKey: commandId,
        workspaceId: workspace.id,
        baseRevision: 1,
        messageId: "proposal-1",
        messageVersion: 3,
        response: "ACCEPT",
      }),
    }));

    await act(async () => {
      await expect(result.current.respondToAgentMessage(
        "proposal-1",
        3,
        "ACCEPT",
      )).resolves.toEqual(accepted);
    });

    expect(respond.mock.calls).toEqual([
      [workspace.id, "proposal-1", 3, "ACCEPT", {
        idempotencyKey: commandId,
        baseRevision: 1,
      }],
      [workspace.id, "proposal-1", 3, "ACCEPT", {
        idempotencyKey: commandId,
        baseRevision: 1,
      }],
    ]);
    expect(ids).toHaveBeenCalledTimes(1);
    await expect(store.load()).resolves.toEqual({ schemaVersion: 1, workspaceId: workspace.id });
    unmount();
  });

  it("rebuilds a process-local budget preview while preserving the persisted command UUID", async () => {
    const store = new MemoryWorkspaceRecoveryStore();
    await store.save({
      schemaVersion: 1,
      workspaceId: workspace.id,
      pendingCommand: {
        kind: "apply_budget",
        idempotencyKey: commandId,
        workspaceId: workspace.id,
        baseRevision: 1,
        previewId: "budget-preview:from-dead-process",
        requestedBudget: { currency: "KRW", amount: 120_000 },
        createdAt: new Date(now()).toISOString(),
      },
    });
    const rebuilt = { ...preview, id: "budget-preview:rebuilt" };
    const previewBudget = jest.fn(async () => rebuilt);
    const budgetChanged: WorkspaceView = { ...serverWorkspace, plan: preview.projectedPlan };
    const apply = jest.fn(async () => budgetChanged);
    const stableGateway: CommerceGateway = {
      ...gateway(jest.fn()),
      mode: "server",
      getWorkspace: async () => serverWorkspace,
      previewBudget,
      applyBudgetPreview: apply,
    };

    const { result, unmount } = await renderHook(() => useWorkspace({
      gateway: stableGateway,
      recoveryStore: store,
      now,
    }));
    await waitFor(() => expect(result.current.restoring).toBe(false));

    expect(previewBudget).toHaveBeenCalledWith(workspace.id, 120_000, {
      idempotencyKey: commandId,
      baseRevision: 1,
    });
    expect(apply).toHaveBeenCalledWith(workspace.id, rebuilt.id, {
      idempotencyKey: commandId,
      baseRevision: 1,
    });
    expect(result.current.workspace?.plan.totals.budget.amount).toBe(120_000);
    await expect(store.load()).resolves.toEqual({ schemaVersion: 1, workspaceId: workspace.id });
    unmount();
  });

  it("drops pending replay data after a definitive domain rejection", async () => {
    const rejected = Object.assign(new Error("revision changed"), { name: "ServerCommerceError" });
    const submit = jest.fn(async () => { throw rejected; });
    const { result, store, unmount } = await mountedController({ answerQuestion: submit });

    await act(async () => {
      await expect(result.current.answerQuestion(answer)).resolves.toBeNull();
    });

    await expect(store.load()).resolves.toEqual({ schemaVersion: 1, workspaceId: workspace.id });
    unmount();
  });
});

async function flushMicrotasks(): Promise<void> {
  for (let index = 0; index < 8; index += 1) await Promise.resolve();
}
