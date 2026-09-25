import type { Localize } from "../../../shared/i18n";

// Still Water (ADR-0073, PR E): customer-facing status chips are sentence-case
// words, never raw enums. Every known state has a Korean/English pair; an
// unknown value falls back to a humanized English word so a new server state
// never surfaces as SNAKE_CASE on a customer screen. Operator screens keep the
// raw vocabulary on purpose (voice-and-terms: Operator).
export type StateTone = "progress" | "done" | "waiting" | "failed";

export function humanizeState(value: string): string {
  const words = value.toLowerCase().split("_").filter(Boolean);
  if (words.length === 0) return value;
  return words[0].charAt(0).toUpperCase() + words[0].slice(1) + (words.length > 1 ? ` ${words.slice(1).join(" ")}` : "");
}

function pick(table: Record<string, string>, value: string) {
  return table[value] ?? humanizeState(value);
}

export function paymentStateLabel(state: string, l: Localize) {
  return pick({
    AUTHORIZED: l("Authorized", "승인됨"),
    PARTIALLY_CAPTURED: l("Partially captured", "일부 Capture"),
    CAPTURED: l("Captured", "Capture 완료"),
    COMPLETED: l("Completed", "완료"),
    VOIDED: l("Voided", "승인 해제"),
    FINALIZED: l("Finalized", "확정됨"),
    SAFE: l("Safe", "안전 확정"),
    SUBMITTED: l("Submitted", "제출됨"),
    PAY_SUBMITTED: l("Submitted", "제출됨"),
    REORGED: l("Reorganized", "재구성됨"),
    PENDING: l("Pending", "대기"),
    PROCESSING: l("Processing", "처리 중"),
    OUTCOME_UNKNOWN: l("Outcome unknown", "결과 확인 중"),
    ACTION_REQUIRED: l("Awaiting approval", "승인 대기"),
    EXPIRED: l("Expired", "만료"),
    FAILED: l("Failed", "실패"),
    CLOSED: l("Closed", "종료"),
  }, state);
}

export function merchantOrderStateLabel(state: string, l: Localize) {
  return pick({
    PLANNED: l("Planned", "예정"),
    READY_TO_PLACE: l("Ready to place", "주문 준비"),
    PLACEMENT_PENDING: l("Placing", "주문 중"),
    PLACED: l("Placed", "주문 완료"),
    PLACEMENT_UNKNOWN: l("Placement unknown", "주문 결과 확인 중"),
    FAILED: l("Failed", "실패"),
    CANCELLED: l("Cancelled", "취소됨"),
  }, state);
}

export function fundingStateLabel(state: string, l: Localize) {
  return pick({
    AVAILABLE: l("Available", "확보됨"),
    ACTIVATION_PENDING: l("Activating", "활성화 중"),
    ACTIVATION_UNKNOWN: l("Activation unknown", "활성화 확인 중"),
    ACTIVE: l("Active", "활성"),
    RELEASE_PENDING: l("Releasing", "반환 중"),
    RELEASE_UNKNOWN: l("Release unknown", "반환 확인 중"),
    RELEASED: l("Released", "반환됨"),
    FAILED: l("Failed", "실패"),
  }, state);
}

export function refundRequestStateLabel(state: string, l: Localize) {
  return pick({
    REQUESTED: l("Requested", "요청됨"),
    REVIEWING: l("Reviewing", "심사 중"),
    RESOLVED: l("Resolved", "처리됨"),
  }, state);
}

export function compensationStateLabel(state: string, l: Localize) {
  return pick({
    APPROVED: l("Approved", "승인됨"),
    EXECUTION_PENDING: l("Returning", "반환 중"),
    OUTCOME_UNKNOWN: l("Outcome unknown", "결과 확인 중"),
    SUCCEEDED: l("Returned", "반환 완료"),
    FAILED: l("Failed", "실패"),
  }, state);
}

export function disputeStateLabel(state: string, l: Localize) {
  return pick({
    OPEN: l("Open", "진행 중"),
    RESOLVED: l("Resolved", "해결됨"),
  }, state);
}

export function disputeOutcomeLabel(outcome: string, l: Localize) {
  return pick({
    NONE: l("No outcome yet", "결과 없음"),
    RESOLVED_BUYER_FAVOUR: l("In the buyer's favour", "구매자 승"),
    RESOLVED_SELLER_FAVOUR: l("In the seller's favour", "판매자 승"),
    RESOLVED_WITH_PAYOUT: l("Resolved with payout", "지급으로 해결"),
    CANCELED_BY_BUYER: l("Cancelled by buyer", "구매자 취소"),
    ACCEPTED: l("Accepted", "수용"),
    DENIED: l("Denied", "거절"),
  }, outcome);
}

// The dot beside a chip is the only place a state carries color: progress is
// still, done is text, waiting is an empty circle, failure is danger.
export function stateTone(value: string | undefined): StateTone {
  if (!value) return "waiting";
  if (/FAILED|DENIED|EXCEPTION|CANCEL/.test(value)) return "failed";
  if (/PENDING|UNKNOWN|REVIEWING|REQUESTED|OPEN|PROCESSING|ACTIVE\b|PARTIALLY|AUTHORIZED|SUBMITTED/.test(value)) return "progress";
  if (/PLACED|RELEASED|RESOLVED|SUCCEEDED|COMPLETED|CAPTURED|FINALIZED|AVAILABLE|ACCEPTED|CLOSED|VOIDED|DELIVERED/.test(value)) return "done";
  return "waiting";
}
