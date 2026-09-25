import { request } from "../../../shared/api/client";
import {
  newSendAttemptKey,
  type SupportConversation,
  type SupportCustomerContact,
  type SupportMessage,
} from "../domain/message";

// Support 운영자 창구(ADR-0059) — 목록·스레드는 RequireOperator, 발신은
// fresh 재인증(freshOperatorRoute)이 서버에서 강제된다.

export async function listSupportConversations(view: "AWAITING_REPLY" | "ACTION_REQUIRED" | "ALL") {
  return request<{ schemaVersion: string; conversations: SupportConversation[] }>(
    `/api/v1/admin/support/conversations?view=${view}`,
  );
}

export async function getSupportThread(userId: string) {
  return request<{
    schemaVersion: string;
    userId: string;
    customer?: SupportCustomerContact;
    messages: SupportMessage[];
    awaitingReply: boolean;
    awaitingCustomerMessageId?: string;
    actionRequired: boolean;
  }>(
    `/api/v1/admin/support/conversations/${encodeURIComponent(userId)}/messages`,
  );
}

export function supportOperatorImageURL(userId: string, attachmentId: string) {
  const base = import.meta.env.VITE_API_URL ?? "";
  return `${base}/api/v1/admin/support/conversations/${encodeURIComponent(userId)}/images/${encodeURIComponent(attachmentId)}`;
}

// 마지막 고객 메시지를 답변 발송 없이 운영 완료로 처리한다. exact message
// ID를 보내 새 고객 메시지까지 실수로 함께 닫지 않는다(ADR-0062).
export async function handleSupportConversationWithoutReply(
  userId: string,
  customerMessageId: string,
) {
  return request<{ schemaVersion: string; handled: boolean; replay: boolean }>(
    `/api/v1/admin/support/conversations/${encodeURIComponent(userId)}/messages/${encodeURIComponent(customerMessageId)}/no-reply-resolution`,
    { method: "PUT" },
  );
}

export async function replySupportConversation(userId: string, body: string, images: File[] = []) {
  if (images.length > 0) {
    const form = new FormData();
    form.append("body", body);
    images.forEach((image) => form.append("images", image));
    return request<{ message: SupportMessage; replay: boolean }>(
      `/api/v1/admin/support/conversations/${encodeURIComponent(userId)}/messages`,
      {
        method: "POST",
        headers: { "Idempotency-Key": newSendAttemptKey() },
        body: form,
      },
    );
  }
  return request<{ message: SupportMessage; replay: boolean }>(
    `/api/v1/admin/support/conversations/${encodeURIComponent(userId)}/messages`,
    {
      method: "POST",
      headers: { "Idempotency-Key": newSendAttemptKey() },
      body: JSON.stringify({ body }),
    },
  );
}

// 종전 "고객 안내 보내기"(agencyorder notice 발행)의 대체 — 주문 소유 고객의
// 대화로 주문 첨부 메시지를 보낸다. 주문 운영 화면이 호출한다.
export async function sendSupportOrderMessage(agencyOrderId: string, body: string, images: File[] = []) {
  if (images.length > 0) {
    const form = new FormData();
    form.append("body", body);
    images.forEach((image) => form.append("images", image));
    return request<{ message: SupportMessage; replay: boolean }>(
      `/api/v1/admin/support/orders/${encodeURIComponent(agencyOrderId)}/messages`,
      {
        method: "POST",
        headers: { "Idempotency-Key": newSendAttemptKey() },
        body: form,
      },
    );
  }
  return request<{ message: SupportMessage; replay: boolean }>(
    `/api/v1/admin/support/orders/${encodeURIComponent(agencyOrderId)}/messages`,
    {
      method: "POST",
      headers: { "Idempotency-Key": newSendAttemptKey() },
      body: JSON.stringify({ body }),
    },
  );
}

// 답변 대기 전역 카운트(SQL COUNT) — 운영자 nav 뱃지가 이 값만 신뢰한다.
export async function getSupportCounts() {
  return request<{ schemaVersion: string; counts: { awaiting: number; actionRequired: number } }>(
    "/api/v1/admin/support/counts",
  );
}
