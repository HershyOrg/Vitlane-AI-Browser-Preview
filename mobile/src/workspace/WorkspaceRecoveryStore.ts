import AsyncStorage from "@react-native-async-storage/async-storage";

import type { AgentMessageResponse, Currency, QuestionAnswerInput } from "../domain";

export type PendingWorkspaceCreation = {
  readonly kind: "create_workspace";
  readonly idempotencyKey: string;
  readonly intent: string;
  readonly createdAt: string;
};

type PendingWorkspaceCommandBase = {
  readonly idempotencyKey: string;
  readonly workspaceId: string;
  readonly baseRevision: number;
  readonly createdAt: string;
};

/**
 * Minimal replay data for a mutation whose response may have been lost.
 *
 * Product candidates and workspace projections deliberately stay out of
 * AsyncStorage. Text is retained only when it is the command payload the
 * server needs to deduplicate or replay.
 */
export type PendingWorkspaceCommand =
  | (PendingWorkspaceCommandBase & {
      readonly kind: "answer_question";
      readonly answer: QuestionAnswerInput;
    })
  | (PendingWorkspaceCommandBase & {
      readonly kind: "apply_budget";
      readonly previewId: string;
      readonly requestedBudget: {
        readonly currency: Currency;
        readonly amount: number;
      };
    })
  | (PendingWorkspaceCommandBase & {
      readonly kind: "submit_follow_up";
      readonly text: string;
    })
  | (PendingWorkspaceCommandBase & {
      readonly kind: "respond_agent_message";
      readonly messageId: string;
      readonly messageVersion: number;
      readonly response: AgentMessageResponse;
    });

export type WorkspaceRecoverySnapshot = {
  readonly schemaVersion: 1;
  readonly workspaceId?: string;
  readonly pendingCreation?: PendingWorkspaceCreation;
  readonly pendingCommand?: PendingWorkspaceCommand;
};

export interface WorkspaceRecoveryStore {
  load(): Promise<WorkspaceRecoverySnapshot | null>;
  save(snapshot: WorkspaceRecoverySnapshot): Promise<void>;
  clear(): Promise<void>;
}

const storagePrefix = "@vitlane/workspace-recovery/v1";
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

/** Stores minimal command replay metadata. Session tokens always use SecureStore. */
export class AsyncWorkspaceRecoveryStore implements WorkspaceRecoveryStore {
  private readonly key: string;

  public constructor(ownerId: string) {
    const normalized = ownerId.trim();
    if (!normalized) throw new TypeError("Workspace recovery requires an owner ID");
    this.key = `${storagePrefix}:${encodeURIComponent(normalized)}`;
  }

  public async load(): Promise<WorkspaceRecoverySnapshot | null> {
    const raw = await AsyncStorage.getItem(this.key);
    if (raw === null) return null;
    try {
      const parsed: unknown = JSON.parse(raw);
      if (!isWorkspaceRecoverySnapshot(parsed)) {
        await this.clear();
        return null;
      }
      return parsed;
    } catch {
      await this.clear();
      return null;
    }
  }

  public async save(snapshot: WorkspaceRecoverySnapshot): Promise<void> {
    if (!isWorkspaceRecoverySnapshot(snapshot)) {
      throw new TypeError("Invalid workspace recovery snapshot");
    }
    await AsyncStorage.setItem(this.key, JSON.stringify(snapshot));
  }

  public async clear(): Promise<void> {
    await AsyncStorage.removeItem(this.key);
  }
}

export class MemoryWorkspaceRecoveryStore implements WorkspaceRecoveryStore {
  private snapshot: WorkspaceRecoverySnapshot | null = null;

  public async load(): Promise<WorkspaceRecoverySnapshot | null> {
    return this.snapshot ? clone(this.snapshot) : null;
  }

  public async save(snapshot: WorkspaceRecoverySnapshot): Promise<void> {
    this.snapshot = clone(snapshot);
  }

  public async clear(): Promise<void> {
    this.snapshot = null;
  }
}

function isWorkspaceRecoverySnapshot(value: unknown): value is WorkspaceRecoverySnapshot {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Record<string, unknown>;
  if (candidate.schemaVersion !== 1) return false;
  if (candidate.workspaceId !== undefined && typeof candidate.workspaceId !== "string") return false;
  if (candidate.pendingCreation !== undefined) {
    if (!candidate.pendingCreation || typeof candidate.pendingCreation !== "object") return false;
    const pending = candidate.pendingCreation as Record<string, unknown>;
    if (
      pending.kind !== "create_workspace"
      || !isUuid(pending.idempotencyKey)
      || typeof pending.intent !== "string"
      || typeof pending.createdAt !== "string"
    ) return false;
  }
  if (candidate.pendingCommand === undefined) return true;
  if (!candidate.pendingCommand || typeof candidate.pendingCommand !== "object") return false;
  const command = candidate.pendingCommand as Record<string, unknown>;
  if (
    !isUuid(command.idempotencyKey)
    || !isNonEmptyString(command.workspaceId)
    || !Number.isInteger(command.baseRevision)
    || (command.baseRevision as number) < 1
    || typeof command.createdAt !== "string"
    || (candidate.workspaceId !== undefined && candidate.workspaceId !== command.workspaceId)
  ) return false;
  switch (command.kind) {
    case "answer_question":
      return isQuestionAnswerInput(command.answer);
    case "apply_budget": {
      if (!isNonEmptyString(command.previewId) || !command.requestedBudget
        || typeof command.requestedBudget !== "object") return false;
      const budget = command.requestedBudget as Record<string, unknown>;
      return (budget.currency === "KRW" || budget.currency === "USD")
        && typeof budget.amount === "number"
        && Number.isFinite(budget.amount)
        && budget.amount > 0;
    }
    case "submit_follow_up":
      return isNonEmptyString(command.text);
    case "respond_agent_message":
      return isNonEmptyString(command.messageId)
        && Number.isInteger(command.messageVersion)
        && (command.messageVersion as number) >= 1
        && (command.response === "ACCEPT"
          || command.response === "DISMISS"
          || command.response === "ACKNOWLEDGE");
    default:
      return false;
  }
}

function isQuestionAnswerInput(value: unknown): value is QuestionAnswerInput {
  if (!value || typeof value !== "object") return false;
  const answer = value as Record<string, unknown>;
  return isNonEmptyString(answer.questionId)
    && Array.isArray(answer.selectedOptionIds)
    && answer.selectedOptionIds.every(isNonEmptyString)
    && (answer.customText === undefined || typeof answer.customText === "string")
    && (answer.disposition === "apply" || answer.disposition === "no_preference");
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0;
}

function isUuid(value: unknown): value is string {
  return typeof value === "string" && uuidPattern.test(value);
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}
