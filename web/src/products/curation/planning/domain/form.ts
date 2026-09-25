import type {
  ExecutionMode,
  Money,
  PlanTarget,
  PlanningMode,
  URLMode,
} from "../../../../shared/api/types";
import { invariantContent } from "../../../../shared/i18n";

export type PlanForm = {
  controlMode?: "AUTO" | "MANUAL";
  originalIntent: string;
  planningMode: PlanningMode;
  amount: string;
  budgetExplicit?: boolean;
  displayCurrency?: "KRW" | "USD";
  budgetAllocationMode?: "AUTO" | "EQUAL";
  currency: string;
  executionMode: ExecutionMode;
  country: string;
  city: string;
  category: string;
  allowedItems: string;
  blockedItems: string;
  minPrice: string;
  maxPrice: string;
  referenceUrl: string;
  urlMode: URLMode;
  /**
   * Which intelligence provider runs this plan's AI work. MANAGED is the only
   * accepted value today; EXTERNAL exists for plans created before the
   * external agent was retired. Both fields are fixed at submission.
   */
  agentMode: AgentMode;
  modelKey: string;
};

export type AgentMode = "MANAGED" | "EXTERNAL";

export const initialPlanForm: PlanForm = {
  originalIntent: "",
  controlMode: "AUTO",
  planningMode: "AUTO",
  amount: "",
  budgetExplicit: false,
  budgetAllocationMode: "AUTO",
  currency: "KRW",
  executionMode: "EXPERIMENT",
  country: "KR",
  city: invariantContent("서울"),
  category: "",
  allowedItems: "",
  blockedItems: "",
  minPrice: "",
  maxPrice: "",
  referenceUrl: "",
  urlMode: "NONE",
  agentMode: "MANAGED",
  modelKey: "",
};

export function listFromText(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

export function optionalMoney(amount: string, currency: string): Money | null {
  const clean = amount.trim();
  return clean ? { amount: clean, currency } : null;
}

export function formFromTarget(target: PlanTarget): PlanForm {
  return {
    originalIntent: target.normalizedIntent,
    planningMode: "SINGLE",
    amount: target.allocatedBudget.amount,
    currency: target.allocatedBudget.currency,
    executionMode: "EXPERIMENT",
    country: target.researchScope.country,
    city: target.researchScope.city ?? "",
    category: target.researchScope.category ?? "",
    allowedItems: target.researchScope.allowedItems.join(", "),
    blockedItems: target.researchScope.blockedItems.join(", "),
    minPrice: target.researchScope.minPrice?.amount ?? "",
    maxPrice: target.researchScope.maxPrice?.amount ?? "",
    referenceUrl: target.researchScope.referenceUrl ?? "",
    urlMode: target.researchScope.urlMode,
    agentMode: "MANAGED",
    modelKey: "",
  };
}
