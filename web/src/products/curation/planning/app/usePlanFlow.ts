import { useEffect, useState } from "react";
import { APIError } from "../../../../shared/api/client";
import { localizeFixedCopy } from "../../../../shared/i18n";
import type { PlanResult } from "../../../../shared/api/types";
import {
  initialPlanForm,
  listFromText,
  type PlanForm,
} from "../domain/form";
import { createPlan } from "../infra/planningApi";
import {
  publishCurationCreated,
  publishCurationCreationUnconfirmed,
} from "../../app/useSidebarCurations";

export function usePlanFlow(initial: PlanResult | null = null) {
  const [plan, setPlan] = useState<PlanResult | null>(initial);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setPlan(initial);
  }, [initial]);

  async function submitPlan(form: PlanForm): Promise<PlanResult | null> {
    setWorking(true);
    setError(null);
    try {
      const result = await createPlan({
        originalIntent: form.originalIntent,
        planningMode: form.controlMode === "MANUAL" ? "SINGLE" : "AUTO",
        controlMode: form.controlMode ?? "AUTO",
        totalBudget: { amount: form.amount || "0", currency: form.currency },
        budget: { schemaVersion: "vitlane.curation-budget.v1", inputMode: form.controlMode === "MANUAL" ? "EXPLICIT" : "AUTO", currency: form.currency, totalAmount: form.amount.trim() || null, allocationMode: form.controlMode === "MANUAL" ? "EQUAL" : "AUTO" },
        executionMode: form.executionMode,
        location: { country: form.country, city: form.city },
        category: form.category,
        allowedItems: listFromText(form.allowedItems),
        blockedItems: listFromText(form.blockedItems),
        minPrice: null,
        maxPrice: null,
        referenceUrl: form.referenceUrl,
        urlMode: form.urlMode,
        agentMode: form.agentMode,
        // The server rejects a model key on an EXTERNAL plan, because it would
        // imply Vitlane chose a model it never runs.
        ...(form.agentMode === "MANAGED" && form.modelKey
          ? { modelKey: form.modelKey }
          : {}),
      });
      setPlan(result);
      // The response already holds the sidebar row, so the list shows the new
      // Curation without asking the server again (ADR-0079).
      publishCurationCreated({
        curationId: result.curation.id,
        intentSummary: result.plan.originalIntent,
        createdAt: result.curation.createdAt,
      });
      return result;
    } catch (caught) {
      // A refusal from the server saved nothing. Any other failure, such as a
      // lost connection or an unreadable or 5xx response, may have come after
      // the Curation was saved, so the sidebar checks for it (ADR-0079).
      if (!(caught instanceof APIError && caught.status < 500)) {
        publishCurationCreationUnconfirmed();
      }
      setError(messageOf(caught));
      return null;
    } finally {
      setWorking(false);
    }
  }

  return {
    plan,
    working,
    error,
    initialForm: initialPlanForm,
    submitPlan,
  };
}

export function messageOf(error: unknown): string {
  if (error instanceof APIError) {
    if (error.code === "SHIPPING_PROFILE_REQUIRED") {
      return localizeFixedCopy("Save a default shipping address in your account before purchase.", "구매 전에 계정에서 기본 배송지를 저장해 주세요.");
    }
    return error.message;
  }
  return localizeFixedCopy("We couldn't connect to the server. Check its status.", "서버에 연결하지 못했습니다. 실행 상태를 확인해 주세요.");
}
