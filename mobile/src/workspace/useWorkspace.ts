import { useCallback, useEffect, useRef, useState } from "react";
import { AppState } from "react-native";

import type { CommerceGateway } from "../commerce";
import type {
  AgentMessageResponse,
  BrowserRunStartContext,
  BrowserRunView,
  BudgetChangePreview,
  QuestionAnswerInput,
  ShoppingPreferencesPatch,
  WorkspaceView,
} from "../domain";
import { secureUuidV4 } from "../infra";
import type {
  PendingWorkspaceCommand,
  WorkspaceRecoveryStore,
} from "./WorkspaceRecoveryStore";

const pendingCreationLifetimeMilliseconds = 24 * 60 * 60 * 1_000;
const activePollingMilliseconds = 3_000;
const backgroundResearchPollingMilliseconds = 60_000;
const commandPollingWindowMilliseconds = 45_000;

export type WorkspaceController = {
  workspace: WorkspaceView | null;
  busy: boolean;
  restoring: boolean;
  error: string | null;
  createWorkspace: (intent: string) => Promise<WorkspaceView | null>;
  answerQuestion: (answer: QuestionAnswerInput) => Promise<WorkspaceView | null>;
  previewBudget: (amount: number) => Promise<BudgetChangePreview | null>;
  applyBudgetPreview: (preview: BudgetChangePreview) => Promise<WorkspaceView | null>;
  submitFollowUp: (text: string) => Promise<WorkspaceView | null>;
  respondToAgentMessage: (
    messageId: string,
    messageVersion: number,
    response: AgentMessageResponse,
  ) => Promise<WorkspaceView | null>;
  cancelResearchSubscription: (subscriptionId: string) => Promise<WorkspaceView | null>;
  hideResearchFinding: (findingId: string) => Promise<WorkspaceView | null>;
  importResearchFinding: (findingId: string) => Promise<WorkspaceView | null>;
  updateShoppingPreferences: (patch: ShoppingPreferencesPatch) => Promise<WorkspaceView | null>;
  startBrowserRun: (candidateId: string) => Promise<BrowserRunView | null>;
  refresh: () => Promise<WorkspaceView | null>;
  clearError: () => void;
};

type UseWorkspaceOptions = {
  gateway: CommerceGateway;
  recoveryStore: WorkspaceRecoveryStore;
  commandIdFactory?: () => string;
  now?: () => number;
  pollIntervalMilliseconds?: number;
  backgroundPollIntervalMilliseconds?: number;
  onError?: (cause: unknown) => void;
};

/**
 * Coordinates the UI port, recovery and background refreshes. Screens remain
 * transport agnostic. Server mutations are persisted before transport so a
 * response loss can be reconciled with the same durable command identity.
 */
export function useWorkspace({
  gateway,
  recoveryStore,
  commandIdFactory = secureUuidV4,
  now = Date.now,
  pollIntervalMilliseconds = activePollingMilliseconds,
  backgroundPollIntervalMilliseconds = backgroundResearchPollingMilliseconds,
  onError,
}: UseWorkspaceOptions): WorkspaceController {
  const pendingRef = useRef(false);
  const refreshPendingRef = useRef(false);
  const browserRunCommandsRef = useRef(new Map<string, BrowserRunStartContext>());
  const generationRef = useRef(0);
  const pollUntilRef = useRef(0);
  const [workspace, setWorkspace] = useState<WorkspaceView | null>(null);
  const workspaceRef = useRef<WorkspaceView | null>(null);
  const [busy, setBusy] = useState(false);
  const [restoring, setRestoring] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const commit = useCallback((next: WorkspaceView, generation: number) => {
    if (generation !== generationRef.current) return false;
    workspaceRef.current = next;
    setWorkspace(next);
    return true;
  }, []);

  const remember = useCallback(async (
    next: WorkspaceView,
    clearPendingCommand = false,
  ) => {
    const snapshot = await recoveryStore.load();
    const pending = snapshot?.pendingCommand;
    const preservePending = !clearPendingCommand
      && pending?.workspaceId === next.id
      && !pendingCommandReflected(next, pending);
    await recoveryStore.save({
      schemaVersion: 1,
      workspaceId: next.id,
      ...(preservePending ? { pendingCommand: pending } : {}),
    });
  }, [recoveryStore]);

  useEffect(() => {
    let cancelled = false;
    const generation = ++generationRef.current;
    const restore = async () => {
      try {
        const snapshot = await recoveryStore.load();
        if (cancelled || !snapshot) return;
        let next: WorkspaceView | null = null;
        let pendingCommandReconciled = false;
        const pending = snapshot.pendingCreation;
        const pendingCreatedAt = pending ? Date.parse(pending.createdAt) : Number.NaN;
        if (pending && Number.isFinite(pendingCreatedAt)
          && now() - pendingCreatedAt <= pendingCreationLifetimeMilliseconds) {
          next = await gateway.createWorkspace(pending.intent, {
            idempotencyKey: pending.idempotencyKey,
            baseRevision: 0,
          });
        } else if (snapshot.workspaceId || snapshot.pendingCommand?.workspaceId) {
          const workspaceId = snapshot.workspaceId ?? snapshot.pendingCommand?.workspaceId;
          if (!workspaceId) return;
          next = await gateway.getWorkspace(workspaceId);
          const pendingCommand = snapshot.pendingCommand;
          if (
            pendingCommand?.workspaceId === workspaceId
          ) {
            if (pendingCommandReflected(next, pendingCommand)) {
              pendingCommandReconciled = true;
            } else {
              try {
                next = await replayPendingCommand(gateway, pendingCommand);
                pendingCommandReconciled = true;
              } catch (cause) {
                if (!cancelled && commit(next, generation)) await remember(next);
                if (isDefinitiveCommandFailure(cause)) await remember(next, true);
                throw cause;
              }
            }
          }
        } else if (pending) {
          await recoveryStore.clear();
        }
        if (cancelled || !next) return;
        if (commit(next, generation)) {
          await remember(next, pendingCommandReconciled);
          if (gateway.mode === "server") {
            pollUntilRef.current = now() + commandPollingWindowMilliseconds;
          }
        }
      } catch (cause) {
        if (!cancelled) {
          setError(errorMessage(cause));
          onError?.(cause);
        }
      } finally {
        if (!cancelled) setRestoring(false);
      }
    };
    void restore();
    return () => { cancelled = true; };
  }, [commit, gateway, now, onError, recoveryStore, remember]);

  const runCommand = useCallback(async (
    command: () => Promise<WorkspaceView | null>,
    pollAfter = gateway.mode === "server",
    prepare?: () => Promise<void>,
    clearPendingCommandOnSuccess = false,
  ): Promise<WorkspaceView | null> => {
    if (pendingRef.current) return null;
    pendingRef.current = true;
    const generation = ++generationRef.current;
    setBusy(true);
    setError(null);
    try {
      await prepare?.();
      const next = await command();
      if (next && commit(next, generation)) {
        await remember(next, clearPendingCommandOnSuccess);
        if (pollAfter) pollUntilRef.current = now() + commandPollingWindowMilliseconds;
      }
      return next;
    } catch (cause) {
      if (clearPendingCommandOnSuccess && isDefinitiveCommandFailure(cause)) {
        const current = workspaceRef.current;
        if (current) {
          try {
            await remember(current, true);
          } catch {
            // Keep the original command failure as the user-visible error.
          }
        }
      }
      setError(errorMessage(cause));
      onError?.(cause);
      return null;
    } finally {
      pendingRef.current = false;
      setBusy(false);
    }
  }, [commit, gateway.mode, now, onError, remember]);

  const acquirePendingCommand = useCallback(async <T extends PendingWorkspaceCommand>(
    current: WorkspaceView,
    matches: (pending: PendingWorkspaceCommand) => pending is T,
    create: () => T,
  ): Promise<T> => {
    const snapshot = await recoveryStore.load();
    const pending = snapshot?.pendingCommand;
    if (
      pending
      && pending.workspaceId === current.id
      && !pendingCommandReflected(current, pending)
    ) {
      if (matches(pending)) return pending;
      throw new Error("An earlier workspace change is still being reconciled");
    }
    const created = create();
    await recoveryStore.save({
      schemaVersion: 1,
      workspaceId: current.id,
      pendingCommand: created,
    });
    return created;
  }, [recoveryStore]);

  const refresh = useCallback(async (): Promise<WorkspaceView | null> => {
    const current = workspaceRef.current;
    if (!current || pendingRef.current || refreshPendingRef.current) return null;
    refreshPendingRef.current = true;
    const generation = ++generationRef.current;
    try {
      const next = await gateway.getWorkspace(current.id);
      if (commit(next, generation)) await remember(next);
      return next;
    } catch (cause) {
      setError(errorMessage(cause));
      onError?.(cause);
      return null;
    } finally {
      refreshPendingRef.current = false;
    }
  }, [commit, gateway, onError, remember]);

  useEffect(() => {
    if (!workspace || gateway.mode !== "server") return undefined;
    const shouldPollActiveWork = () => {
      const processing = workspaceRef.current?.processing;
      return processing ? processing.shouldPoll : now() < pollUntilRef.current;
    };
    const shouldPollBackgroundResearch = () => (
      workspaceRef.current?.backgroundResearch?.subscriptions.some(
        (subscription) => subscription.status === "ACTIVE",
      ) ?? false
    );
    if (!shouldPollActiveWork() && !shouldPollBackgroundResearch()) return undefined;
    const intervalMilliseconds = shouldPollActiveWork()
      ? pollIntervalMilliseconds
      : backgroundPollIntervalMilliseconds;
    const timer = setInterval(() => {
      if (
        AppState.currentState === "active"
        && (shouldPollActiveWork() || shouldPollBackgroundResearch())
      ) {
        void refresh();
      }
    }, intervalMilliseconds);
    return () => clearInterval(timer);
  }, [
    backgroundPollIntervalMilliseconds,
    gateway.mode,
    now,
    pollIntervalMilliseconds,
    refresh,
    workspace,
  ]);

  useEffect(() => {
    const subscription = AppState.addEventListener("change", (state) => {
      if (state === "active" && gateway.mode === "server" && workspaceRef.current) {
        void refresh();
      }
    });
    return () => subscription.remove();
  }, [gateway.mode, refresh]);

  const createWorkspace = useCallback(async (intent: string) => {
    const normalized = intent.trim();
    if (!normalized) return null;
    let idempotencyKey = "";
    let commandIntent = normalized;
    let recoveringEarlierIntent = false;
    return runCommand(
      async () => {
        const next = await gateway.createWorkspace(commandIntent, {
          idempotencyKey,
          baseRevision: 0,
        });
        if (!next || !recoveringEarlierIntent) return next;
        return {
          ...next,
          lastChange: [{
            "ko-KR": "완료 여부가 불분명했던 이전 요청을 먼저 복구했어요.",
            "en-US": "Recovered the earlier request whose completion was uncertain.",
          }],
        };
      },
      gateway.mode === "server",
      async () => {
        const snapshot = await recoveryStore.load();
        const pending = snapshot?.pendingCreation;
        const createdAt = pending ? Date.parse(pending.createdAt) : Number.NaN;
        if (
          pending
          && Number.isFinite(createdAt)
          && now() - createdAt <= pendingCreationLifetimeMilliseconds
        ) {
          idempotencyKey = pending.idempotencyKey;
          commandIntent = pending.intent;
          recoveringEarlierIntent = pending.intent !== normalized;
          return;
        }
        idempotencyKey = durableCommandId(commandIdFactory);
        await recoveryStore.save({
          schemaVersion: 1,
          pendingCreation: {
            kind: "create_workspace",
            idempotencyKey,
            intent: commandIntent,
            createdAt: new Date(now()).toISOString(),
          },
        });
      },
    );
  }, [commandIdFactory, gateway, now, recoveryStore, runCommand]);

  const answerQuestion = useCallback(async (answer: QuestionAnswerInput) => {
    const current = workspaceRef.current;
    if (!current) return null;
    if (gateway.mode !== "server") {
      return runCommand(() => gateway.answerQuestion(current.id, answer, {
        idempotencyKey: commandIdFactory(),
        baseRevision: current.revision,
      }));
    }
    const normalized = normalizeAnswer(answer);
    let pending: Extract<PendingWorkspaceCommand, { kind: "answer_question" }> | undefined;
    return runCommand(
      () => gateway.answerQuestion(current.id, pending?.answer ?? normalized, {
        idempotencyKey: pending?.idempotencyKey ?? "",
        baseRevision: pending?.baseRevision ?? current.revision,
      }),
      true,
      async () => {
        pending = await acquirePendingCommand(
          current,
          (candidate): candidate is Extract<PendingWorkspaceCommand, { kind: "answer_question" }> => (
            candidate.kind === "answer_question" && answersMatch(candidate.answer, normalized)
          ),
          () => ({
            kind: "answer_question",
            idempotencyKey: durableCommandId(commandIdFactory),
            workspaceId: current.id,
            baseRevision: current.revision,
            answer: normalized,
            createdAt: new Date(now()).toISOString(),
          }),
        );
      },
      true,
    );
  }, [acquirePendingCommand, commandIdFactory, gateway, now, runCommand]);

  const previewBudget = useCallback(async (amount: number) => {
    const current = workspaceRef.current;
    if (!current) return null;
    try {
      setError(null);
      return await gateway.previewBudget(current.id, amount, {
        idempotencyKey: commandIdFactory(),
        baseRevision: current.revision,
      });
    } catch (cause) {
      setError(errorMessage(cause));
      onError?.(cause);
      return null;
    }
  }, [commandIdFactory, gateway, onError]);

  const applyBudgetPreview = useCallback(async (preview: BudgetChangePreview) => {
    const current = workspaceRef.current;
    if (!current) return null;
    if (gateway.mode !== "server") {
      return runCommand(() => gateway.applyBudgetPreview(current.id, preview.id, {
        idempotencyKey: commandIdFactory(),
        baseRevision: current.revision,
      }));
    }
    let pending: Extract<PendingWorkspaceCommand, { kind: "apply_budget" }> | undefined;
    return runCommand(
      () => gateway.applyBudgetPreview(current.id, pending?.previewId ?? preview.id, {
        idempotencyKey: pending?.idempotencyKey ?? "",
        baseRevision: pending?.baseRevision ?? current.revision,
      }),
      true,
      async () => {
        pending = await acquirePendingCommand(
          current,
          (candidate): candidate is Extract<PendingWorkspaceCommand, { kind: "apply_budget" }> => (
            candidate.kind === "apply_budget"
            && moneyMatches(candidate.requestedBudget, preview.requestedBudget)
          ),
          () => ({
            kind: "apply_budget",
            idempotencyKey: durableCommandId(commandIdFactory),
            workspaceId: current.id,
            baseRevision: current.revision,
            previewId: preview.id,
            requestedBudget: preview.requestedBudget,
            createdAt: new Date(now()).toISOString(),
          }),
        );
      },
      true,
    );
  }, [acquirePendingCommand, commandIdFactory, gateway, now, runCommand]);

  const submitFollowUp = useCallback(async (text: string) => {
    const current = workspaceRef.current;
    const normalized = text.trim();
    if (!current || !normalized) return null;
    if (gateway.mode !== "server") {
      return runCommand(() => gateway.submitFollowUp(current.id, normalized, {
        idempotencyKey: commandIdFactory(),
        baseRevision: current.revision,
      }));
    }
    let pending: Extract<PendingWorkspaceCommand, { kind: "submit_follow_up" }> | undefined;
    return runCommand(
      () => gateway.submitFollowUp(current.id, pending?.text ?? normalized, {
        idempotencyKey: pending?.idempotencyKey ?? "",
        baseRevision: pending?.baseRevision ?? current.revision,
      }),
      true,
      async () => {
        pending = await acquirePendingCommand(
          current,
          (candidate): candidate is Extract<PendingWorkspaceCommand, { kind: "submit_follow_up" }> => (
            candidate.kind === "submit_follow_up" && candidate.text === normalized
          ),
          () => ({
            kind: "submit_follow_up",
            idempotencyKey: durableCommandId(commandIdFactory),
            workspaceId: current.id,
            baseRevision: current.revision,
            text: normalized,
            createdAt: new Date(now()).toISOString(),
          }),
        );
      },
      true,
    );
  }, [acquirePendingCommand, commandIdFactory, gateway, now, runCommand]);

  const respondToAgentMessage = useCallback(async (
    messageId: string,
    messageVersion: number,
    response: AgentMessageResponse,
  ) => {
    const current = workspaceRef.current;
    if (!current) return null;
    if (gateway.mode !== "server") {
      return runCommand(() => gateway.respondToAgentMessage(
        current.id,
        messageId,
        messageVersion,
        response,
        { idempotencyKey: commandIdFactory(), baseRevision: current.revision },
      ));
    }
    let pending: Extract<PendingWorkspaceCommand, { kind: "respond_agent_message" }> | undefined;
    return runCommand(
      () => gateway.respondToAgentMessage(
        current.id,
        pending?.messageId ?? messageId,
        pending?.messageVersion ?? messageVersion,
        pending?.response ?? response,
        {
          idempotencyKey: pending?.idempotencyKey ?? "",
          baseRevision: pending?.baseRevision ?? current.revision,
        },
      ),
      true,
      async () => {
        pending = await acquirePendingCommand(
          current,
          (candidate): candidate is Extract<PendingWorkspaceCommand, { kind: "respond_agent_message" }> => (
            candidate.kind === "respond_agent_message"
            && candidate.messageId === messageId
            && candidate.messageVersion === messageVersion
            && candidate.response === response
          ),
          () => ({
            kind: "respond_agent_message",
            idempotencyKey: durableCommandId(commandIdFactory),
            workspaceId: current.id,
            baseRevision: current.revision,
            messageId,
            messageVersion,
            response,
            createdAt: new Date(now()).toISOString(),
          }),
        );
      },
      true,
    );
  }, [acquirePendingCommand, commandIdFactory, gateway, now, runCommand]);

  const cancelResearchSubscription = useCallback(async (subscriptionId: string) => {
    const current = workspaceRef.current;
    if (!current) return null;
    return runCommand(
      () => gateway.cancelResearchSubscription(current.id, subscriptionId),
      false,
    );
  }, [gateway, runCommand]);

  const hideResearchFinding = useCallback(async (findingId: string) => {
    const current = workspaceRef.current;
    if (!current) return null;
    return runCommand(() => gateway.hideResearchFinding(current.id, findingId), false);
  }, [gateway, runCommand]);

  const importResearchFinding = useCallback(async (findingId: string) => {
    const current = workspaceRef.current;
    if (!current) return null;
    return runCommand(() => gateway.importResearchFinding(current.id, findingId), true);
  }, [gateway, runCommand]);

  const updateShoppingPreferences = useCallback(async (patch: ShoppingPreferencesPatch) => {
    const current = workspaceRef.current;
    if (!current) return null;
    return runCommand(() => gateway.updateShoppingPreferences(current.id, patch), false);
  }, [gateway, runCommand]);

  const startBrowserRun = useCallback(async (candidateId: string): Promise<BrowserRunView | null> => {
    const current = workspaceRef.current;
    const normalizedCandidateId = candidateId.trim();
    if (!current || !normalizedCandidateId || pendingRef.current) return null;
    pendingRef.current = true;
    setBusy(true);
    setError(null);
    const commandKey = `${current.id}\u0000${normalizedCandidateId}`;
    let context = browserRunCommandsRef.current.get(commandKey);
    if (!context) {
      const commandId = durableCommandId(commandIdFactory);
      context = {
        createIdempotencyKey: `browser-run:create:${commandId}`,
        navigationApprovalIdempotencyKey: `browser-run:navigate:${commandId}`,
      };
      browserRunCommandsRef.current.set(commandKey, context);
    }
    try {
      const run = await gateway.startBrowserRun(current.id, normalizedCandidateId, context);
      browserRunCommandsRef.current.delete(commandKey);
      return run;
    } catch (cause) {
      setError(errorMessage(cause));
      onError?.(cause);
      return null;
    } finally {
      pendingRef.current = false;
      setBusy(false);
    }
  }, [commandIdFactory, gateway, onError]);

  const clearError = useCallback(() => setError(null), []);

  return {
    workspace,
    busy,
    restoring,
    error,
    createWorkspace,
    answerQuestion,
    previewBudget,
    applyBudgetPreview,
    submitFollowUp,
    respondToAgentMessage,
    cancelResearchSubscription,
    hideResearchFinding,
    importResearchFinding,
    updateShoppingPreferences,
    startBrowserRun,
    refresh,
    clearError,
  };
}

function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : "The workspace request failed";
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function durableCommandId(factory: () => string): string {
  const commandId = factory();
  if (!uuidPattern.test(commandId)) throw new TypeError("Workspace command IDs must be UUIDs");
  return commandId.toLowerCase();
}

function pendingCommandReflected(
  workspace: WorkspaceView,
  command: PendingWorkspaceCommand,
): boolean {
  if (command.workspaceId !== workspace.id) return false;
  if (command.kind === "answer_question") {
    const committed = workspace.answers.find(
      (answer) => answer.questionId === command.answer.questionId,
    );
    return Boolean(committed && answersMatch(committed, command.answer));
  }
  if (command.kind === "apply_budget") {
    return workspace.plan.totals.budgetState === "LIMITED"
      && moneyMatches(workspace.plan.totals.budget, command.requestedBudget);
  }
  if (command.kind === "respond_agent_message") {
    const message = workspace.agentMessages?.find((item) => item.id === command.messageId);
    const expectedStatus = command.response === "ACCEPT"
      ? "ACCEPTED"
      : command.response === "DISMISS"
        ? "DISMISSED"
        : "ACKNOWLEDGED";
    return message?.status === expectedStatus && message.version >= command.messageVersion;
  }
  return false;
}

async function replayPendingCommand(
  gateway: CommerceGateway,
  command: PendingWorkspaceCommand,
): Promise<WorkspaceView> {
  const context = {
    idempotencyKey: command.idempotencyKey,
    baseRevision: command.baseRevision,
  };
  switch (command.kind) {
    case "answer_question":
      return gateway.answerQuestion(command.workspaceId, command.answer, context);
    case "apply_budget": {
      // Preview IDs are process-local. Rebuild the non-authoritative preview,
      // then apply the canonical mutation with the original command UUID.
      const preview = await gateway.previewBudget(
        command.workspaceId,
        command.requestedBudget.amount,
        context,
      );
      return gateway.applyBudgetPreview(command.workspaceId, preview.id, context);
    }
    case "submit_follow_up":
      return gateway.submitFollowUp(command.workspaceId, command.text, context);
    case "respond_agent_message":
      return gateway.respondToAgentMessage(
        command.workspaceId,
        command.messageId,
        command.messageVersion,
        command.response,
        context,
      );
  }
}

function normalizeAnswer(answer: QuestionAnswerInput): QuestionAnswerInput {
  const customText = answer.customText?.trim();
  return {
    questionId: answer.questionId,
    selectedOptionIds: [...answer.selectedOptionIds],
    ...(customText ? { customText } : {}),
    disposition: answer.disposition,
  };
}

function answersMatch(
  left: Pick<QuestionAnswerInput, "questionId" | "selectedOptionIds" | "customText">,
  right: Pick<QuestionAnswerInput, "questionId" | "selectedOptionIds" | "customText">,
): boolean {
  return left.questionId === right.questionId
    && (left.customText?.trim() ?? "") === (right.customText?.trim() ?? "")
    && left.selectedOptionIds.length === right.selectedOptionIds.length
    && left.selectedOptionIds.every((id, index) => id === right.selectedOptionIds[index]);
}

function moneyMatches(
  left: { readonly currency: string; readonly amount: number },
  right: { readonly currency: string; readonly amount: number },
): boolean {
  return left.currency === right.currency && left.amount === right.amount;
}

function isDefinitiveCommandFailure(cause: unknown): boolean {
  if (!cause || typeof cause !== "object") return false;
  const failure = cause as {
    readonly name?: unknown;
    readonly status?: unknown;
    readonly metadata?: { readonly retryable?: unknown };
  };
  if (failure.name === "ServerCommerceError" || failure.name === "FixtureDomainError") return true;
  if (failure.name !== "VitlaneApiError" || typeof failure.status !== "number") return false;
  return failure.status >= 400
    && failure.status < 500
    && failure.status !== 408
    && failure.status !== 429
    && failure.metadata?.retryable !== true;
}
