import { request } from "../../../../shared/api/client";

export type ResearchRoundSummary = {
 routes?: Array<{routeId:string;policyVersion:string;productVertical:string;decision:string;reason:string;count:number;meanPressure:number}>;
  schemaVersion: "vitlane.research-round-summary.v1";
  windowDays: number;
  since: string;
  generatedAt: string;
  rounds: Array<{ country: string; status: string; count: number }>;
  failures: Array<{ failureCode: string; stepKind: string; retryable: boolean; count: number }>;
  sources: Array<{ country: string; source: string; status: string; reasonCode: string; count: number }>;
  apiCalls: Array<{ apiId: string; outcome: string; billable: boolean; count: number }>;
  admitted: { rounds: number; median: number; buckets: Array<{ label: string; count: number }> };
  evaluation: { evaluated: number; unevaluated: number };
  steps?: Array<{ kind: string; count: number; p50Seconds: number; p95Seconds: number }>;
  attempts?: Array<{ status: string; failureCode: string; count: number }>;
  reservations?: Array<{ status: string; count: number; amountMicros: number }>;
};

/** Durable ledgers only; the read never spends a provider call. */
export const getResearchRoundSummary = (days = 7) =>
  request<ResearchRoundSummary>(`/api/v1/admin/catalog-apis/round-summary?days=${days}`);
