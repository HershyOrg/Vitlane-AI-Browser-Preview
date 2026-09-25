import { applyOwnerAction } from "../../ordering/infra/orderProcessApi";
import { request } from "../../../shared/api/client";
import { newSendAttemptKey, type SupportMessage } from "../domain/message";

// Support 고객 창구(ADR-0059/0063) — "메시지" 위젯이 쓴다. 주문 링크는 구조화
// 참조(agencyOrderId)로만 성립하며, 자기 소유 주문만 서버가 허용한다
// (운영자와 대칭 — 본문 파싱 링크화는 없다).

export async function listSupportMessages() {
  return request<{ schemaVersion: string; messages: SupportMessage[] }>(
    "/api/v1/support/messages",
  );
}

export async function sendSupportMessage(
  body: string,
  agencyOrderId = "",
  images: File[] = [],
) {
  if (images.length > 0) {
    const form = new FormData();
    form.append("body", body);
    if (agencyOrderId) form.append("agencyOrderId", agencyOrderId);
    images.forEach((image) => form.append("images", image));
    return request<{ message: SupportMessage; replay: boolean }>(
      "/api/v1/support/messages",
      {
        method: "POST",
        headers: { "Idempotency-Key": newSendAttemptKey() },
        body: form,
      },
    );
  }
  return request<{ message: SupportMessage; replay: boolean }>(
    "/api/v1/support/messages",
    {
      method: "POST",
      headers: { "Idempotency-Key": newSendAttemptKey() },
      body: JSON.stringify(
        agencyOrderId ? { body, agencyOrderId } : { body },
      ),
    },
  );
}

export function supportImageURL(attachmentId: string) {
  const base = import.meta.env.VITE_API_URL ?? "";
  return `${base}/api/v1/support/images/${encodeURIComponent(attachmentId)}`;
}

export async function markSupportRead() {
  return request<{ read: true }>("/api/v1/support/messages/read", {
    method: "POST",
    body: "{}",
  });
}

export async function getSupportSummary() {
  return request<{ schemaVersion: string; unread: number }>(
    "/api/v1/support/summary",
  );
}

export async function respondToProcurementRequest(
  agencyOrderId: string,
  requestId: string,
  expectedVersion: number,
  response: Record<string, unknown>,
  decline = false,
) {
  return applyOwnerAction(
    `/api/v1/agencyOrder/${encodeURIComponent(agencyOrderId)}/procurement-requests/${encodeURIComponent(requestId)}/response`,
    {
      method: "POST",
      headers: { "Idempotency-Key": `procurement-response:${requestId}:${crypto.randomUUID()}` },
      body: JSON.stringify({ expectedVersion, response, decline }),
    },
    false,
  );
}
