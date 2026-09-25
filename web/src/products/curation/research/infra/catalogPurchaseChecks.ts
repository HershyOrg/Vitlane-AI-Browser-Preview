import { request } from "../../../../shared/api/client";
import { randomUUID } from "../../../../shared/browser/randomUUID";
import type { AmazonVariantRef, KoreanProductRef } from "../../domain/sourceProduct";
import type { PurchaseFeedback } from "./amazonApi";
import { amazonCandidateURL } from "./amazonApi";
import { externalPurchaseChanged, publishPurchaseFeedback } from "./externalStateEvents";

// What the user saw when they marked the purchase (ADR-0075). It is their own
// record, never a catalog price or an order confirmation.
export type PurchaseCheckSnapshot = {
  productTitle: string;
  variantTitle?: string;
  merchant?: string;
  priceMinor: number;
  priceUnknown: boolean;
  currency?: string;
};

export type PurchaseCheckRecord = {
  curationId: string;
  targetId?: string;
  targetTitle?: string;
  candidateId: string;
  variantRef?: AmazonVariantRef;
  productRef?: KoreanProductRef;
  productUrl?: string;
  checked: boolean;
  version: number;
  recordedAt: string;
  evidence: "SELF_REPORTED";
  snapshot: PurchaseCheckSnapshot | null;
  snapshotAt?: string;
};

// The server owns the platform registry, so a new Korean mall reaches this list
// as its code. Narrowing the union here would drop the mall the day it is added.
export type PurchaseCheckSource = string;

export type PurchaseCheckItem = PurchaseCheckRecord & {
  key: string;
  curationPath: string;
  source: PurchaseCheckSource;
  subjectId: string;
};

// The curation cards already dispatch this event after every check or undo;
// the account list listens to the same one instead of owning a second signal.
export const purchaseChecksChanged = externalPurchaseChanged;

export function purchaseCheckItem(record: PurchaseCheckRecord): PurchaseCheckItem {
  const source: PurchaseCheckSource = record.productRef?.source ?? record.variantRef?.source ?? "AMAZON";
  const subjectId = record.productRef?.productId ?? record.variantRef?.asin ?? record.candidateId;
  return {
    ...record,
    source,
    subjectId,
    key: `${record.curationId}::${record.candidateId}::${source}:${subjectId}`,
    curationPath: `/curations/${record.curationId}`,
  };
}

export async function listPurchaseChecks(): Promise<PurchaseCheckItem[]> {
  const result = await request<{ schemaVersion: string; records: PurchaseCheckRecord[] }>(
    "/api/v1/account/purchase-checks",
  );
  return result.records.map(purchaseCheckItem);
}

// Undo reuses the per-Candidate purchase-check command the curation card sends;
// the account list never gets a writer of its own (ADR-0075).
export async function undoPurchaseCheck(record: PurchaseCheckRecord): Promise<void> {
  const headers = { "Idempotency-Key": randomUUID() };
  const candidate = `/api/v1/curations/${encodeURIComponent(record.curationId)}/catalog-research/candidates/${encodeURIComponent(record.candidateId)}`;
  const feedback = record.productRef
    ? await request<PurchaseFeedback>(`${candidate}/external-product/purchase-check`, {
      method: "PUT",
      headers,
      body: JSON.stringify({ schemaVersion: "vitlane.external-product-purchase.v1", productRef: record.productRef, checked: false, expectedVersion: record.version }),
    })
    : await request<PurchaseFeedback>(amazonCandidateURL(record.curationId, record.candidateId, "purchase-check"), {
      method: "PUT",
      headers,
      body: JSON.stringify({ variantRef: record.variantRef, checked: false, expectedVersion: record.version }),
    });
  // The cards of that curation update from this value instead of refetching.
  publishPurchaseFeedback(record.curationId, feedback);
}
