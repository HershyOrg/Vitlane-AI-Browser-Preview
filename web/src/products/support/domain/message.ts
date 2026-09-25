// Support 대화(ADR-0059) 계약 타입 — shared/openapi/v1.yaml SupportMessage의
// Web 미러다. agencyOrderId는 서버→사용자 단방향 주문 링크의 유일한 근거인
// 구조화 참조이며, 본문 텍스트는 author와 무관하게 파싱·링크화하지 않는다.

export type SupportAuthor = "CUSTOMER" | "OPERATOR" | "SYSTEM";

export type SupportBusinessCard = {
  type: "PROCUREMENT_REQUEST" | "PROCUREMENT_RESPONSE" | "PROCUREMENT_DECISION" |
    "ORDER_CANCELLATION" | "ORDER_CANCELLATION_DECLINED" |
    "REFUND_REQUEST" | "REFUND_DECISION" | "REFUND_STATUS" |
    "PAYPAL_DISPUTE" | "PAYPAL_DISPUTE_STATUS" |
    "DELIVERY_DELAY" | "DELIVERY_RESOLUTION";
  reference: { type: "PROCUREMENT" | "REFUND" | "PAYPAL_DISPUTE" | "DELIVERY"; id: string };
  actionRequired: boolean;
  resolvesCardId?: string;
  publicPayload: Record<string, unknown>;
};

export type SupportImageAttachment = {
  id: string;
  mediaType: "image/jpeg" | "image/png";
  width: number;
  height: number;
  byteSize: number;
  downloadName: string;
};

export type SupportMessage = {
  id: string;
  author: SupportAuthor;
  body: string;
  agencyOrderId?: string;
  createdAt: string;
  readAt?: string;
  contentKind?: "TEXT" | "BUSINESS_CARD";
  businessCard?: SupportBusinessCard;
  attachments?: SupportImageAttachment[];
};

export type SupportCustomerContact = {
  // 탈퇴(tombstone) 계정은 빈 문자열로 온다.
  email: string;
  displayName: string;
};

export type SupportConversation = {
  userId: string;
  customer?: SupportCustomerContact;
  lastMessage: SupportMessage;
  awaitingReply: boolean;
  awaitingCustomerMessageId?: string;
  actionRequired?: boolean;
};

// 시도당 새 키 — 채팅은 같은 본문 재전송("네" 두 번)이 정상이라 본문 해시
// dedupe를 쓰지 않는다. 자동 재시도에만 같은 키를 재사용한다.
export function newSendAttemptKey() {
  return typeof crypto !== "undefined" && "randomUUID" in crypto
    ? `support:${crypto.randomUUID()}`
    : `support:${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
