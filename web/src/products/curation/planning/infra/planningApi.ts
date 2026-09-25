import { request } from "../../../../shared/api/client";
import { randomUUID } from "../../../../shared/browser/randomUUID";
import type {
  ExecutionMode,
  Money,
  PlanResult,
  PlanningMode,
  URLMode,
} from "../../../../shared/api/types";

export type CreatePlanRequest = {
  originalIntent: string;
  planningMode: PlanningMode;
  controlMode?: "AUTO" | "MANUAL";
  totalBudget: Money;
  budget?: { inputMode?: "AUTO" | "EXPLICIT"; schemaVersion: "vitlane.curation-budget.v1"; currency: string; totalAmount: string | null; allocationMode: "AUTO" | "EQUAL" };
  executionMode: ExecutionMode;
  location: { country: string; city: string };
  category: string;
  allowedItems: string[];
  blockedItems: string[];
  minPrice: Money | null;
  maxPrice: Money | null;
  referenceUrl: string;
  urlMode: URLMode;
  agentMode: AgentMode;
  /** Only sent for MANAGED; the server rejects a model key on an EXTERNAL plan. */
  modelKey?: string;
};

export type AgentMode = "MANAGED" | "EXTERNAL";

export type ManagedRunnerModel = {
  key: string;
  label: string;
};

export type ManagedRunnerCapability = {
  enabled: boolean;
  models: ManagedRunnerModel[];
  defaultModelKey: string;
  serverExhausted: boolean;
};

export type ManagedRunnerUsage = {
  enabled: boolean;
  usageDate: string;
  userSpentMicros: number;
  userLimitMicros: number;
  userExhausted: boolean;
  serverExhausted: boolean;
  serverLimitMicros: number;
};

export function fetchManagedRunnerCapability(): Promise<ManagedRunnerCapability> {
  return request<ManagedRunnerCapability>("/api/v1/managed-runner/capability");
}

export function fetchManagedRunnerUsage(): Promise<ManagedRunnerUsage> {
  return request<ManagedRunnerUsage>("/api/v1/managed-runner/usage");
}

export type ManagedRunnerStepKind =
  | "INTERPRETING"
  | "SEARCHING_CATALOG"
  | "RANKING"
  | "SUBMITTING";

export type ManagedRunnerStep = {
  kind: ManagedRunnerStepKind;
  status: "RUNNING" | "SUCCEEDED" | "FAILED";
  reasonCode?: string;
  startedAt: string;
};

export function fetchManagedRunnerProgress(
  workOrderId: string,
): Promise<{ steps: ManagedRunnerStep[] }> {
  return request<{ steps: ManagedRunnerStep[] }>(
    `/api/v1/managed-runner/work/${workOrderId}/progress`,
  );
}

const CREATE_RETRY_KEY = "vitlane.plan.create.retry";

export async function createPlan(input: CreatePlanRequest): Promise<PlanResult> {
  const body = JSON.stringify(input);
  const pending = readPendingCreation();
  const idempotencyKey =
    pending?.body === body ? pending.key : randomUUID();
  sessionStorage.setItem(
    CREATE_RETRY_KEY,
    JSON.stringify({ key: idempotencyKey, body }),
  );
  try {
    const result = await request<PlanResult>("/api/v1/shopping-plans", {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey },
      body,
    });
    sessionStorage.removeItem(CREATE_RETRY_KEY);
    return result;
  } catch (error) {
    if (error instanceof Error && "code" in error && error.code === "IDEMPOTENCY_KEY_REUSED") {
      sessionStorage.removeItem(CREATE_RETRY_KEY);
    }
    throw error;
  }
}

function readPendingCreation(): { key: string; body: string } | null {
  try {
    const raw = sessionStorage.getItem(CREATE_RETRY_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as { key?: string; body?: string };
    if (!parsed.key || !parsed.body) return null;
    return { key: parsed.key, body: parsed.body };
  } catch {
    sessionStorage.removeItem(CREATE_RETRY_KEY);
    return null;
  }
}
