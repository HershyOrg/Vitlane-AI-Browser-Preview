import { localizeFixedCopy, type Localize } from "../../../shared/i18n";
import type { MerchantOrderOperationalStage, UnitStage } from "../infra/agencyOrderApi";

// The Server owns stage and terminal-reason truth. These functions map each
// contract value to one deterministic English/Korean presentation pair.
export function terminalReasonLabel(
  reason: string | undefined,
  l: Localize = localizeFixedCopy,
) {
  return {
    COMPLETED_ALL: l("Delivered", "배송 완료"),
    COMPLETED_PARTIAL: l("Complete · includes partial refund", "완료 · 부분 환불 포함"),
    REFUNDED_ALL: l("Fully refunded", "전액 환불"),
    CANCELLED: l("Cancelled", "취소됨"),
    EXPIRED: l("Expired", "만료됨"),
  }[reason ?? ""];
}

export function unitStageLabel(
  stage: UnitStage,
  l: Localize = localizeFixedCopy,
) {
  return {
    ORDERED: l("Order received", "주문 접수"),
    PROCURING: l("Purchasing", "구매 진행 중"),
    PROCUREMENT_FAILED: l("Unavailable · refund planned", "구매 불가 · 환불 예정"),
    CANCELLED: l("Cancelled", "취소됨"),
    AWAITING_SHIPMENT: l("Preparing shipment", "배송 준비"),
    IN_TRANSIT: l("In transit", "배송 중"),
    DELIVERED: l("Delivery confirmed", "수령 확인"),
    EXCEPTION: l("Under review", "확인 중"),
    RETURN_IN_PROGRESS: l("Return in progress", "회수 진행 중"),
    REFUND_REQUESTED: l("Refund under review", "환불 심사 중"),
    REFUND_PENDING: l("Refund in progress", "환불 진행 중"),
    REFUNDED: l("Refund complete", "환불 완료"),
  }[stage];
}

export function merchantOrderOperationalStageLabel(
  stage: MerchantOrderOperationalStage,
  l: Localize = localizeFixedCopy,
) {
  return {
    PROCUREMENT_PENDING: l("Ready for procurement", "조달 대기"),
    PROCUREMENT_ACTIVE: l("Procurement in progress", "조달 진행 중"),
    AWAITING_SHIPMENT: l("Tracking required", "운송장 등록 필요"),
    IN_TRANSIT: l("In transit", "배송 중"),
    DELIVERY_EXCEPTION: l("Delivery exception", "배송 예외"),
    REFUND_REVIEW: l("Refund review", "환불 심사"),
    RETURN_IN_PROGRESS: l("Return in progress", "회수 진행 중"),
    COMPENSATION_PENDING: l("Refund in progress", "환불 진행 중"),
    PAYPAL_DISPUTE: l("PayPal dispute", "PayPal 분쟁"),
    ATTENTION_REQUIRED: l("Needs attention", "확인 필요"),
    DELIVERED: l("Delivered", "정상 배송 완료"),
    REFUNDED: l("Refunded", "환불 완료"),
    PROCUREMENT_FAILED: l("Procurement failed", "조달 실패"),
    CANCELLED: l("Cancelled", "취소 완료"),
  }[stage];
}

export function unitGuidance(
  stage: UnitStage,
  refundDestination: string,
  l: Localize = localizeFixedCopy,
) {
  switch (stage) {
    case "PROCUREMENT_FAILED":
      return l(
        "The merchant could not supply this product. Its amount will be refunded automatically.",
        "판매처 사정으로 이 상품을 구매하지 못했습니다. 해당 상품 금액이 자동 환불됩니다.",
      );
    case "EXCEPTION":
      return l(
        "We are checking whether the item is missing or incorrect. The decision will lead to a refund or return.",
        "누락·오배송 여부를 확인하고 있습니다. 판정 후 환불 또는 회수로 진행됩니다.",
      );
    case "RETURN_IN_PROGRESS":
      return l(
        "The incorrect item is being returned. Any refund proceeds according to the decision, independently of the return.",
        "오배송 상품을 회수하고 있습니다. 회수와 별개로 환불은 판정에 따라 진행됩니다.",
      );
    case "REFUND_REQUESTED":
      return l(
        "After operator review, the refund will return to the original payment method ({destination}).",
        "담당자 확인 후 원 결제수단({destination})으로 환불됩니다.",
        { destination: refundDestination },
      );
    case "REFUND_PENDING":
      return l(
        "The refund is being sent to the original payment method ({destination}).",
        "원 결제수단({destination})으로 환불을 실행하고 있습니다.",
        { destination: refundDestination },
      );
    default:
      return undefined;
  }
}

// unit fulfillment(배송 기대) 결과 어휘(운영정합 5차 C3) — 패키지 카드가
// "수령 절차 완료"와 "전 유닛 정상 수령"을 구분해 말하게 한다. 어휘 밖 값은
// 화면이 raw로 노출하지 않고 "확인 중"으로 강등한다.
export const fulfillmentExceptionKinds = ["MISSING", "WRONG_ACTUAL", "LOST"] as const;

export function fulfillmentLabel(
  value: string,
  l: Localize = localizeFixedCopy,
) {
  return {
    AWAITING_EFFECT: l("Preparing shipment", "배송 준비"),
    IN_TRANSIT_EXPECTED: l("In transit", "배송 중"),
    DELIVERED_EXPECTED: l("Received normally", "정상 수령"),
    MISSING: l("Missing", "누락"),
    WRONG_ACTUAL: l("Incorrect item", "오배송"),
    LOST: l("Lost", "분실"),
    RESOLVED: l("Decision complete", "판정 완료"),
    NONCONFORMING_RESOLVED: l("Decision complete", "판정 완료"),
    RETURNED: l("Return confirmed", "반송 확인"),
  }[value] ?? l("Under review", "확인 중");
}
