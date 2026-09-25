import { request } from "../../../shared/api/client";

export type ProcessGuidance = {
  reasonCode?: string;
  waitingFor?: string;
  customerAction: string;
  operatorAction: string;
};
export type ProcessReceipt = {
  schemaVersion: "vitlane.order-process-receipt.v1";
  agencyOrderId: string;
  merchantOrderId?: string;
  requestId: string;
  flowId: string;
  kind: string;
  outcome: "RECEIVED" | "ACCEPTED" | "DEFERRED" | "WAITING" | "COMPLETED" | "REJECTED";
  guidance: ProcessGuidance;
};
export class ProcessRequestPending extends Error {
  constructor(readonly receipt: ProcessReceipt) { super("PROCESS_REQUEST_PENDING"); }
}
export class ProcessRequestRejected extends Error {
  constructor(readonly receipt: ProcessReceipt) { super(receipt.guidance.reasonCode ?? "PROCESS_REQUEST_REJECTED"); }
}
export const isProcessReceipt = (value: unknown): value is ProcessReceipt => !!value && typeof value === "object" && "schemaVersion" in value && value.schemaVersion === "vitlane.order-process-receipt.v1";
export function listProcessRequests(orderId: string) {
  return request<{ schemaVersion: string; requests: ProcessReceipt[] }>(`/api/v1/agencyOrder/${encodeURIComponent(orderId)}/process-requests`);
}
export function getProcessReceipt(receipt: ProcessReceipt, operator = false) {
  const base = operator ? "/api/v1/admin/ordering/orders" : "/api/v1/agencyOrder";
  return request<ProcessReceipt>(`${base}/${encodeURIComponent(receipt.agencyOrderId)}/process-requests/${encodeURIComponent(receipt.requestId)}`);
}
export function submitProcessAction(path: string, init: RequestInit) {
  return request<ProcessReceipt>(path, { ...init, headers: { "Idempotency-Key": crypto.randomUUID(), ...init.headers } });
}
// Wait only for short Owner inputs (claim, evidence, judgment). A timeout retains
// the receipt and stops dependent actions; it never resubmits with a new key.
export async function applyOwnerAction(path: string, init: RequestInit, operator = true) {
  let receipt = await submitProcessAction(path, init);
  const deadline = Date.now() + 8000;
  while (receipt.outcome !== "COMPLETED" && receipt.outcome !== "REJECTED" && Date.now() < deadline) {
    await new Promise((resolve) => globalThis.setTimeout(resolve, 250));
    try { receipt = await getProcessReceipt(receipt, operator); }
    catch { throw new ProcessRequestPending(receipt); }
  }
  if (receipt.outcome === "REJECTED") throw new ProcessRequestRejected(receipt);
  if (receipt.outcome !== "COMPLETED") throw new ProcessRequestPending(receipt);
  return receipt;
}
