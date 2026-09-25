import type { ThreadResponse } from "./thread";
export type CombinationItem = { ref: string; targetId: string; candidateId: string; variantId?: string; configurationVersion: number; quantity: number; cartItemId?: string; checkoutEligible: boolean; allocationFit: "WITHIN" | "OVER" | "UNKNOWN" };
export type Combination = { schemaVersion: "vitlane.combination.v1"; id: string; kind: string; items: CombinationItem[]; currency: string; minimumMinor: number | null; maximumMinor: number | null; budgetMinor: number | null; differenceMinor: number | null; budgetStatus: "WITHIN" | "OVER" | "UNKNOWN" | "NO_LIMIT"; estimated: boolean; priceBasis: "EXACT" | "RANGE" | "ESTIMATED" | "UNKNOWN"; missingTargetIds: string[]; sourceState: string; budgetVersion: number; cartVersion: number; criteriaVersions: Record<string, number>; compatibility: "SUPPORTED" | "UNVERIFIED" | "UNRELATED" | "CONFLICT"; reasons: string[]; tips: { label: string; body: string }[]; cautions: string[]; budgetAdvice: string };
export type CombinationStatus = { schemaVersion: "vitlane.combination-status.v1"; current: boolean; reasonCode?: string; cartVersion: number };
export type CombinationCartMode = "ADD" | "REPLACE";

/** Present structured advice as ordinary reply paragraphs, preserving the existing conversation UI. */
export function combinationReplyText(response: ThreadResponse): string {
 const plan = response.combination;
 if (!plan) return response.body;
 const paragraphs = [
  response.body,
  [...plan.reasons, ...plan.tips.map(tip => tip.body)].filter(Boolean).join(" "),
  plan.cautions.filter(Boolean).join(" "),
  plan.budgetAdvice,
 ];
 return [...new Set(paragraphs.map(text => text.trim()).filter(Boolean))].join("\n\n");
}
