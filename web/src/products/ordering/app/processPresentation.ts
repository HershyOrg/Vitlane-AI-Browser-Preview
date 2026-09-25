import type { Localize } from "../../../shared/i18n";
import type { ProcessReceipt } from "../infra/orderProcessApi";

export function processProgressLabel(receipt: ProcessReceipt, l: Localize, operator = false) {
  const code = receipt.guidance.reasonCode;
  if (code === "CANCELLED_DELIVERY_REQUIRES_REVIEW") return operator ? l("Cancellation and payment return are complete, but delivery was reported. Review the physical item and arrange its return if needed.", "취소와 결제금 반환은 완료되었으나 배송이 보고되었습니다. 실물을 확인하고 필요한 반품을 진행해 주세요.") : l("Payment was returned, but delivery was reported. Contact support to confirm what arrived and arrange any necessary return.", "결제금이 반환되었으나 배송이 보고되었습니다. 지원 채널로 실제 수령 내용을 알리고 필요한 반품을 안내받아 주세요.");
  if (code === "MONEY_GATE_CLOSED") return operator ? l("New payment execution is paused. Check the payment controls; keep the existing request pending.", "신규 결제 실행이 일시 중지되었습니다. 결제 제어 설정을 확인하고 기존 요청을 유지해 주세요.") : l("Payment processing is paused. Your request is saved; do not submit another payment or purchase.", "결제 처리가 일시 중지되었습니다. 요청은 저장되어 있으며 결제나 구매를 새로 요청하지 마세요.");
  if (code === "COMPENSATED") return l("The purchase did not complete. Payment return is confirmed.", "구매가 완료되지 않았으며 결제금 반환이 확인되었습니다.");
  if (code === "PAYMENT_OUTCOME_UNKNOWN") return l("The payment result is being confirmed. Do not start another payment or purchase.", "결제 결과를 확인하고 있습니다. 결제나 구매를 새로 시작하지 마세요.");
  if (code === "MERCHANT_RESULT_REQUIRED") return operator ? l("Purchase permission is ready. Record the merchant's actual result.", "구매 권한이 준비되었습니다. 판매처의 실제 구매 결과를 기록해 주세요.") : l("The operator is confirming the merchant purchase result.", "담당자가 판매처 구매 결과를 확인하고 있습니다.");
  if (code === "CANCELLATION_CONFIRMED_REFUND_PENDING" || code === "COMPENSATION_PENDING") return l("Cancellation or purchase failure is confirmed. Payment return is still pending.", "취소 또는 구매 실패가 확인되었습니다. 결제금 반환은 아직 진행 중입니다.");
  if (["ASSIGNMENT_REQUIRED", "PROCUREMENT_ASSIGNMENT_REQUIRED", "PROCUREMENT_LEASE_EXPIRED"].includes(code ?? "")) return l("An operator must claim this task before processing can continue.", "담당자가 이 작업을 배정받아야 처리를 계속할 수 있습니다.");
  if (code === "PROCUREMENT_MANUAL_DECISION_REQUIRED") return l("Record the purchase conditions before requesting a purchase.", "구매를 요청하기 전에 구매 조건을 기록해 주세요.");
  if (code === "CUSTOMER_DECISION_REQUIRED" || code === "PROCUREMENT_CUSTOMER_REQUEST_OPEN") return operator ? l("Wait for the customer's response before changing purchase conditions.", "구매 조건을 변경하기 전에 고객의 응답을 기다려 주세요.") : l("Review the open question and send your response.", "열린 확인 요청을 검토하고 응답해 주세요.");
  if (code === "PROCUREMENT_PII_ACCESS_DENIED") return l("Check the current task assignment and request access again with a valid reason.", "현재 작업 배정을 확인하고 올바른 사유로 접근을 다시 요청해 주세요.");
  if (code === "MERCHANT_GATE_CLOSED") return l("Merchant purchases are paused. The existing request remains pending.", "판매처 구매가 일시 중지되었습니다. 기존 요청은 대기 상태로 유지됩니다.");
  if (code === "DELIVERED") return l("Delivery was confirmed first. Delay cancellation is unavailable; review the refund options if there is a problem.", "배송이 먼저 확인되었습니다. 지연 취소는 사용할 수 없으며, 문제가 있다면 환불 요청 항목을 확인해 주세요.");
  if (code === "EFFECT_IN_PROGRESS") return receipt.outcome === "DEFERRED"
    ? l("This request is waiting for the current purchase or payment result. It will be reconsidered automatically.", "현재 구매·결제 결과를 기다리고 있습니다. 결과가 확인되면 요청을 자동으로 다시 판단합니다.")
    : l("This change is blocked while another action is in progress. Wait for its result and review the updated state.", "다른 작업이 진행 중이어서 이 변경이 차단되었습니다. 결과를 기다린 뒤 갱신된 상태를 확인해 주세요.");
  if (receipt.outcome === "REJECTED") return l("This request was declined. Review the current shop status before taking another action.", "요청이 거절되었습니다. 다음 행동 전에 현재 Shop 상태를 확인해 주세요.");
  if (receipt.outcome === "COMPLETED") {
    if (code === "CANCELLED_AND_REFUNDED") return l("Cancellation and payment return are complete.", "취소와 결제금 반환이 완료되었습니다.");
    if (code === "MERCHANT_PLACED") return l("The merchant purchase result is confirmed.", "판매처 구매 결과가 확인되었습니다.");
    return l("The requested action is recorded.", "요청한 행동이 기록되었습니다.");
  }
  return l("The request is saved and processing continues. This is not yet a purchase or refund confirmation.", "요청이 저장되어 처리를 계속하고 있습니다. 아직 구매·환불 완료가 확인된 것은 아닙니다.");
}
