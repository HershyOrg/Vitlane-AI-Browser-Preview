import { request } from "../../../shared/api/client";

export type PayPalEnvironment = "SANDBOX" | "LIVE";
export type PayPalDisputeState = "OPEN" | "RESOLVED";
export type PayPalDisputeProviderStatus =
  | "UNKNOWN" | "OPEN" | "WAITING_FOR_SELLER_RESPONSE"
  | "WAITING_FOR_BUYER_RESPONSE" | "UNDER_REVIEW" | "RESOLVED";
export type PayPalDisputeOutcome =
  | "NONE" | "RESOLVED_BUYER_FAVOUR" | "RESOLVED_SELLER_FAVOUR"
  | "RESOLVED_WITH_PAYOUT" | "CANCELED_BY_BUYER" | "ACCEPTED" | "DENIED";

export type PayPalDisputeCase = {
  id: string;
  environment: PayPalEnvironment;
  disputeId: string;
  agencyOrderId: string;
  captureId: string;
  state: PayPalDisputeState;
  providerStatus: PayPalDisputeProviderStatus;
  outcome: PayPalDisputeOutcome;
  reason: string;
  lifecycleStage: "UNKNOWN" | "INQUIRY" | "CHARGEBACK" | "PRE_ARBITRATION" | "ARBITRATION";
  sellerResponseDueAt?: string;
  lastObservedAt: string;
  version: number;
};

export type PayPalDisputeActionInput = {
  expectedVersion: number;
  actionKind: "CASE_OBSERVED" | "MESSAGE_SENT" | "EVIDENCE_SUBMITTED" |
    "OFFER_MADE" | "CLAIM_ACCEPTED" | "APPEAL_SUBMITTED" | "OTHER";
  externalReference: string;
  publicRationale: string;
  internalNote?: string;
  observedProviderStatus: Exclude<PayPalDisputeProviderStatus, "UNKNOWN">;
  observedOutcome?: PayPalDisputeOutcome;
  evidenceSource: "PAYPAL_RESOLUTION_CENTER" | "PAYPAL_EMAIL" |
    "INTERNAL_ORDER_RECORD" | "CARRIER" | "MERCHANT_RECEIPT" | "OTHER";
  evidenceHash: string;
  observedAt: string;
};

export async function listPayPalDisputes(
  environment: PayPalEnvironment,
  state: PayPalDisputeState | "ALL" = "OPEN",
) {
  const query = new URLSearchParams({ environment, state, limit: "100" });
  return request<{ schemaVersion: string; disputes: PayPalDisputeCase[] }>(
    `/api/v1/admin/payment/paypal/disputes?${query.toString()}`,
  );
}

export async function recordPayPalDisputeAction(
  environment: PayPalEnvironment,
  caseId: string,
  input: PayPalDisputeActionInput,
) {
  const query = new URLSearchParams({ environment });
  return request<{
    schemaVersion: string;
    result: { case: PayPalDisputeCase; replay: boolean };
  }>(`/api/v1/admin/payment/paypal/disputes/${encodeURIComponent(caseId)}/actions?${query.toString()}`, {
    method: "POST",
    headers: { "Idempotency-Key": `paypal-dispute:${caseId}:${crypto.randomUUID()}` },
    body: JSON.stringify(input),
  });
}
