import { request } from "../../../../shared/api/client";
import type { AmazonVariantRef, KoreanProductRef } from "../../domain/sourceProduct";
export type { AmazonVariantRef, SourceProductRef, VariantObservation } from "../../domain/sourceProduct";
export type PurchaseRecord = { candidateId: string; variantRef?: AmazonVariantRef; productRef?: KoreanProductRef; checked: boolean; version: number; recordedAt: string; evidence: "SELF_REPORTED" };
export type PurchaseFeedback = { schemaVersion: string; version: number; records: PurchaseRecord[] };
export type AmazonState = { variantRef: AmazonVariantRef; configurationVersion: number; purchaseFeedback: PurchaseFeedback };
export type AmazonSourceControl = { enabled: boolean; version: number; updatedAt: string };
import type { CatalogOperationUsage } from "../domain/catalogOperationUsage";
export type AmazonUsage = {
  operations24h?: CatalogOperationUsage[];
  configured: boolean; control: AmazonSourceControl;
  schemaVersion: string; source: "AMAZON"; enabled: boolean; mode?: "LIVE" | "STUB" | "DISABLED";
  quota: { limit: number; used: number; remaining: number; resetAt: string; observedAt: string; isFree: boolean } | null;
  estimatedRemaining: number | null; attemptsSinceObservation: number;
  requests24h: number; succeeded24h: number;
  failures24h: Array<{ reasonCode: string; count: number }>;
  lastFailureCode?: string; lastFailureAt?: string; quotaRefreshFailure?: string;
};
export const amazonCandidateURL = (curation: string, candidate: string, action: string) => `/api/v1/curations/${encodeURIComponent(curation)}/catalog-research/candidates/${encodeURIComponent(candidate)}/amazon/${action}`;
export const readAmazonState = (curation: string, candidate: string) => request<AmazonState>(amazonCandidateURL(curation, candidate, "state"));
export const amazonUsage = (refresh = false) => request<AmazonUsage>(`/api/v1/admin/catalog-sources/amazon/usage${refresh ? "?refresh=true" : ""}`);
// One foreground Amazon request at a time across cards; queued work cancels on unmount.
let tail: Promise<unknown> = Promise.resolve();
let lastStarted = 0;
export function queueAmazonRead<T>(operation: () => Promise<T>, signal?: AbortSignal): Promise<T> {
  const result = tail.catch(() => undefined).then(async () => {
    signal?.throwIfAborted();
    const wait = Math.max(0, 1000 - (Date.now() - lastStarted));
    if (wait) await new Promise((resolve) => setTimeout(resolve, wait));
    signal?.throwIfAborted(); lastStarted = Date.now();
    return operation();
  });
  tail = result.catch(() => undefined);
  return result;
}

export const setAmazonControl = (enabled: boolean, expectedVersion: number) => request<{
 schemaVersion: "vitlane.amazon-source-control.v1"; mode: "LIVE" | "STUB" | "DISABLED"; control: AmazonSourceControl; configured: boolean; enabled: boolean;
}>("/api/v1/admin/catalog-sources/amazon/control", {method:"PUT",body:JSON.stringify({enabled,expectedVersion})});
