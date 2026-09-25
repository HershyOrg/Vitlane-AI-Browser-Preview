package domain

import (
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/policy"
)

// 이 파일은 고객 상호작용 계약의 명령 공간이다(ADR-0055 §5): "이 상태에서
// 고객이 할 수 있는 행동"의 유일한 표현. Web은 자격 판정을 소유하지 않고
// Projection.availableActions를 렌더한다. 여기의 자격은 **자문 공간**이며,
// 권위 검증은 각 명령 endpoint의 트랜잭션 검사(TOCTOU 안전)가 유지한다.

type CustomerActionKind string

const (
	// ActionPay — PaymentInstruction이 유효하고 수납이 아직 확정되지 않았다.
	ActionPay CustomerActionKind = "PAY"
	// ActionCancelPreEffect — one MO before merchant effect, always full gross.
	ActionCancelPreEffect CustomerActionKind = "CANCEL_PRE_EFFECT"
	// ActionCancelDelayRule — FTC 30일 지연 rule 무료 취소(§2.5, 항상 GROSS).
	ActionCancelDelayRule CustomerActionKind = "CANCEL_DELAY_RULE"
	// ActionRequestRefund — one post-effect MO with a legitimate typed reason.
	ActionRequestRefund CustomerActionKind = "REQUEST_REFUND"
)

type CustomerAction struct {
	Kind CustomerActionKind `json:"kind"`
	// Rail은 PAY 전용 — 결제 진행 화면 선택 축이다("GIWA"|"PAYPAL").
	Rail string `json:"rail,omitempty"`
	// Every cancellation/refund command targets exactly one entry from this
	// server-computed MO set. Lines and units are never independent money targets.
	EligibleMerchantOrderIDs []string `json:"eligibleMerchantOrderIds,omitempty"`
	ReasonCodes              []string `json:"reasonCodes,omitempty"`
}

// customerPaid는 수납 확정(첫 단계 닫힘) 여부다. GIWA rail의 표시 상태는
// settlement 축(FINALIZED 이후)이고 PayPal은 중립 SUCCEEDED다.
func customerPaid(payment *PaymentProjection) bool {
	if payment == nil {
		return false
	}
	if payment.Rail == "PAYPAL" {
		switch payment.State {
		case "AUTHORIZED", "PARTIALLY_CAPTURED", "CAPTURED", "CLOSED":
			return true
		}
		return false
	}
	switch payment.State {
	case "FINALIZED", "COMPLETION_SUBMITTED", "COMPLETED":
		return true
	}
	return false
}

// customerFunded는 환불을 열 수 있는 수납 국면이다 — 부분 환불은 종결
// (COMPLETED) 뒤에도 가능하다(Settlement v2).
func customerFunded(payment *PaymentProjection) bool {
	if payment == nil {
		return false
	}
	if payment.Rail == "PAYPAL" {
		switch payment.State {
		case "AUTHORIZED", "PARTIALLY_CAPTURED", "CAPTURED", "CLOSED":
			return true
		}
		return false
	}
	switch payment.State {
	case "FINALIZED", "COMPLETION_SUBMITTED", "COMPLETED", "REFUND_PENDING", "REFUNDED":
		return true
	}
	return false
}

// AvailableCustomerActions는 Projection에서 현재 명령 공간을 계산하는 순수
// 함수다. 반환 순서는 표시 순서다(결제 → 취소 → 환불).
func AvailableCustomerActions(projection Projection, now time.Time) []CustomerAction {
	actions := make([]CustomerAction, 0, 3)
	paid := customerPaid(projection.Payment)

	// A consumed GIWA instruction may be waiting for its durable confirmation or
	// for the same signed approval to be used. Reloading must preserve that path.
	// Submission/UNKNOWN/finality states do not offer another payment action.
	resumeGIWA := projection.PaymentInstruction.PaymentSelection.Rail == "GIWA" &&
		projection.PaymentInstruction.State == "CONSUMED" &&
		(projection.Payment == nil || projection.Payment.State == "AUTHORIZED" ||
			projection.Payment.State == "AWAITING_ALLOWANCE" || projection.Payment.State == "FAILED")
	if !paid && (projection.PaymentInstruction.State == "PENDING" || resumeGIWA) &&
		now.Before(projection.PaymentInstruction.ExpiresAt) {
		actions = append(actions, CustomerAction{
			Kind: ActionPay, Rail: projection.PaymentInstruction.PaymentSelection.Rail,
		})
	}

	// CANCEL_PRE_EFFECT is per MO. A purchased sibling never closes cancellation
	// for another still-PLANNED MO.
	// A cancel intent that is still EFFECT_ISSUED/DEFERRED keeps every other
	// cancellation/refund command closed for that MO (ADR-0070 §4.3).
	preEffectIDs := make([]string, 0, len(projection.MerchantOrders))
	if paid {
		for _, order := range projection.MerchantOrders {
			if order.State == "PLANNED" && order.CancellationState == "" &&
				order.RefundRequestState == "" && order.CompensationState == "" &&
				!order.CancelIntentPending() {
				preEffectIDs = append(preEffectIDs, order.ID)
			}
		}
	}
	if len(preEffectIDs) > 0 {
		actions = append(actions, CustomerAction{
			Kind: ActionCancelPreEffect, EligibleMerchantOrderIDs: preEffectIDs,
		})
	}

	// CANCEL_DELAY_RULE — 발행 후 30일 초과·미배송이 남았을 때 열린다.
	// PLACEMENT_UNKNOWN 차단·미배송 정밀 판정은 취소 endpoint가 재검사한다.
	if paid && projection.Process.State != ProcessTerminal &&
		!now.Before(projection.AgencyOrder.IssuedAt.Add(
			policy.DelayRuleWindowDays*24*time.Hour)) {
		delayedIDs := make([]string, 0, len(projection.MerchantOrders))
		for _, order := range projection.MerchantOrders {
			if order.State != "PLANNED" && order.State != "FAILED" && order.State != "CANCELLED" &&
				order.CancellationState == "" && order.RefundRequestState == "" &&
				order.CompensationState == "" && !order.CancelIntentPending() {
				delayedIDs = append(delayedIDs, order.ID)
			}
		}
		if len(delayedIDs) > 0 {
			actions = append(actions, CustomerAction{
				Kind: ActionCancelDelayRule, EligibleMerchantOrderIDs: delayedIDs,
			})
		}
	}

	// REQUEST_REFUND — effect가 시작된 미보상 MO가 입력 공간이다. 금액은
	// immutable allocation gross 전체이고 사유 어휘만 고객이 선택한다.
	if customerFunded(projection.Payment) {
		eligible := make([]string, 0, len(projection.MerchantOrders))
		for _, order := range projection.MerchantOrders {
			if order.State != "PLANNED" && order.State != "FAILED" &&
				order.State != "CANCELLED" &&
				order.CancellationState == "" && order.RefundRequestState == "" &&
				order.CompensationState == "" && !order.CancelIntentPending() {
				eligible = append(eligible, order.ID)
			}
		}
		if len(eligible) > 0 {
			actions = append(actions, CustomerAction{
				Kind: ActionRequestRefund, EligibleMerchantOrderIDs: eligible,
				ReasonCodes: append([]string(nil), policy.RefundReasonCodes...),
			})
		}
	}
	return actions
}
