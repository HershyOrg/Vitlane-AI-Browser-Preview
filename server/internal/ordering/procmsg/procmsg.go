// Package procmsg는 주문 워크플로 메시지의 공유 규격이다(ADR-0056).
//
// 여기 있는 것은 이벤트·Effect 타입 상수, payload 구조체, 결정적 key 빌더와
// append 헬퍼뿐이다 — 제품 로직이 없고 어떤 제품 패키지도 import하지 않는다
// (ordering/policy와 같은 규격 계층). owner Context와 ordering/process가 양쪽에서
// import하며, owner ↛ process · process ↛ owner 무순환을 이 패키지가 지탱한다.
//
// payload 값은 owner 저장 어휘 문자열·수치만 담는다. 수량 관찰(미종결
// MerchantOrder 수 등)은 owner가 자기 transaction에서 COUNT해 싣는다 — Reducer는
// +1/-1 산술을 하지 않는다(ADR-0056 §4). PII는 payload에 넣지 않는다.
package procmsg

import (
	"fmt"
	"strings"
	"time"
)

// Source는 이벤트를 기록한 주체다(order_process_events.source CHECK와 일치).
type Source string

const (
	SourceAgencyOrder Source = "AGENCYORDER"
	SourceSupport     Source = "SUPPORT"
	SourcePayment     Source = "PAYMENT"
	SourceSettlement  Source = "SETTLEMENT"
	SourceProcurement Source = "PROCUREMENT"
	SourceLogistics   Source = "LOGISTICS"
	SourceCustomer    Source = "CUSTOMER"
	SourceOperator    Source = "OPERATOR"
	SourceTimer       Source = "TIMER"
	SourceWatchdog    Source = "WATCHDOG"
	SourceSystem      Source = "SYSTEM"
)

// Target은 Effect를 실행하는 owner다(order_process_effects.target CHECK와 일치).
type Target string

const (
	TargetAgencyOrder Target = "AGENCYORDER"
	TargetPayment     Target = "PAYMENT"
	TargetProcurement Target = "PROCUREMENT"
	TargetLogistics   Target = "LOGISTICS"
	// TargetSupport는 고객 통신(Support 카드) executor다(ADR-0070 §4.5) — owner
	// 어댑터·marker 워커 대신 리듀서가 이벤트에서 카드 Effect를 발행한다.
	TargetSupport Target = "SUPPORT"
)

// 이벤트 타입 카탈로그 v1 — 과거형·버전. 카탈로그 밖 타입은 워크플로에 영향을
// 주지 않는 감사 기록일 뿐이다. instruction 만료 Effect는 현행 동작에 만료
// 경로가 없어 v1에서 제외했다(행위 보존 — 어휘 TERMINAL('EXPIRED')는 예약 유지).
const (
	EventOrderIssued         = "agencyorder.order.issued.v1"
	EventRefundReviewDecided = "agencyorder.refund_review.decided.v1"
	EventReceiptIssued       = "agencyorder.receipt.issued.v1"
	EventNoticeRecorded      = "agencyorder.notice.recorded.v1"

	EventRefundRequested = "customer.refund_requested.v1"

	EventCustomerPaymentStateChanged = "payment.customer_payment.state_changed.v1"
	// EventCustomerFundingReady is the clean-cut planning handoff. PayPal emits
	// it after a full-order AUTHORIZE is verified; GIWA emits it after prepaid
	// finality. It deliberately does not imply that every MO has been captured.
	EventCustomerFundingReady       = "payment.customer_funding.ready.v2"
	EventFundsReceiptRecorded       = "payment.funds_receipt.recorded.v1"
	EventMOCompensationStateChanged = "payment.mo_compensation.state_changed.v1"

	EventSettlementStateChanged   = "settlement.state_changed.v1"
	EventSettlementRefundConflict = "settlement.refund_conflict.v1"

	EventMerchantOrderStateChanged = "procurement.merchant_order.state_changed.v1"

	EventUnitFulfillmentChanged = "logistics.unit.fulfillment_changed.v1"
	EventDeliveryFaultJudged    = "logistics.delivery_fault.judged.v1"

	EventTimerFired         = "timer.fired.v1"
	EventWatchdogDivergence = "watchdog.divergence_detected.v1"

	// 카탈로그 v2(ADR-0070 §4.2) — "상태 전이는 전부 보고, 기계는 보고하지
	// 않는다". MO·Unit 단위 fold의 입력이며 owner 집계 필드는 싣지 않는다.
	EventMOFundingStateChanged       = "payment.mo_funding.state_changed.v1"
	EventDisputeStateChanged         = "payment.dispute.state_changed.v1"
	EventEffectLockStateChanged      = "procurement.effect_lock.state_changed.v1"
	EventCustomerRequestStateChanged = "procurement.customer_request.state_changed.v1"
	EventProcurementDecisionRecorded = "procurement.decision.recorded.v1"
	EventShipmentStateChanged        = "logistics.shipment.state_changed.v1"
	EventReturnStateChanged          = "logistics.return.state_changed.v1"
)

// Effect 타입 카탈로그 v1 — 명령형·버전. 발행은 Reducer만 한다.
const (
	EffectReservePurchase       = "procurement.prepare_purchase.v1"
	EffectGrantMerchantPurchase = "procurement.authorize_merchant_effect.v1"
	EffectCompensateMO          = "payment.execute_mo_compensation.v1"
	EffectPlanMerchantOrders    = "procurement.plan_from_funding.v2"
	EffectCancelPrePurchase     = "procurement.cancel_pre_effect.v1"
	EffectCancelByDelayRule     = "procurement.cancel_delay_rule.v1"
	EffectRegisterExpectedUnits = "logistics.register_expected_units.v1"
	EffectIssueReceipt          = "agencyorder.issue_receipt.v1"
	EffectSendNotice            = "agencyorder.send_notice.v1"
	EffectPublishSupportCard    = "support.publish_card.v1"
)

// Support 카드 kind(support/domain 어휘의 사본 — procmsg는 어떤 제품도 import하지
// 않는다). 카드 payload·대상 고객은 SUPPORT executor가 owner 사실을 참조 id로
// 읽어 만든다; Effect는 "어떤 사실의 어떤 카드"만 말한다.
const (
	SupportCardOrderCancellation         = "ORDER_CANCELLATION"
	SupportCardOrderCancellationDeclined = "ORDER_CANCELLATION_DECLINED"
	SupportCardRefundRequest             = "REFUND_REQUEST"
	SupportCardRefundDecision            = "REFUND_DECISION"
	SupportCardRefundStatus              = "REFUND_STATUS"
	SupportCardProcurementDecision       = "PROCUREMENT_DECISION"
	SupportCardProcurementRequest        = "PROCUREMENT_REQUEST"
	SupportCardProcurementResponse       = "PROCUREMENT_RESPONSE"
	SupportCardDeliveryResolution        = "DELIVERY_RESOLUTION"
	SupportCardDeliveryDelay             = "DELIVERY_DELAY"
	SupportCardPayPalDispute             = "PAYPAL_DISPUTE"
)

// 타이머 kind(EventTimerFired payload).
const TimerKindDelayRuleNotice = "DELAY_RULE_NOTICE"

// 고지 kind(EffectSendNotice·EventNoticeRecorded payload).
const NoticeKindDelayRule = "DELAY_RULE"

// ProcessEvent는 append 입력 envelope다. Payload는 JSON marshal 가능한 값이어야 하며
// 타입별 구조체는 payloads.go가 소유한다.
type ProcessEvent struct {
	AgencyOrderID       string
	FlowID              string
	CausationEffectID   string
	SourceEntityVersion int64
	Source              Source
	Type                string
	Payload             any
	DedupKey            string
	OccurredAt          time.Time
}

// EventDedupKey는 owner 행 전이의 결정적 dedup key다:
// "{owner_table}:{row_id}:{transition}". 같은 전이의 재기록(재시도·중복 배달)이
// 새 이벤트를 만들지 않는다.
func EventDedupKey(ownerTable, rowID, transition string) string {
	return ownerTable + ":" + rowID + ":" + transition
}

// TimerDedupKey는 타이머 합성 이벤트의 dedup key다. wake 시각을 넣어, 결정이
// wake를 지우기 전에 재합성돼도 같은 key로 무효화된다.
func TimerDedupKey(agencyOrderID, kind string, wakeAt time.Time) string {
	return fmt.Sprintf("timer:%s:%s:%d", agencyOrderID, kind, wakeAt.Unix())
}

// EffectIdempotencyKey는 결정적 Effect 발행 key다:
// "{type}:{agency_order_id}[:{scope…}]". 같은 결정이 재실행돼도 중복 발행이 0이다.
func EffectIdempotencyKey(effectType, agencyOrderID string, scope ...string) string {
	parts := append([]string{effectType, agencyOrderID}, scope...)
	return strings.Join(parts, ":")
}

// EventOwner is the authority catalog, independent from the actor requesting work.
func EventOwner(eventType string) (Source, bool) {
	switch eventType {
	case EventOrderIssued, EventRefundReviewDecided, EventReceiptIssued, EventNoticeRecorded:
		return SourceAgencyOrder, true
	case EventRefundRequested:
		return SourceAgencyOrder, true
	case EventInstructionClaimed, EventCustomerPaymentStateChanged, EventCustomerFundingReady, EventFundsReceiptRecorded, EventMOCompensationStateChanged, EventMOFundingStateChanged, EventDisputeStateChanged:
		return SourcePayment, true
	case EventMerchantOrderStateChanged, EventEffectLockStateChanged, EventCustomerRequestStateChanged, EventProcurementDecisionRecorded:
		return SourceProcurement, true
	case EventUnitFulfillmentChanged, EventDeliveryFaultJudged, EventShipmentStateChanged, EventReturnStateChanged:
		return SourceLogistics, true
	case EventSettlementStateChanged, EventSettlementRefundConflict:
		return SourceSettlement, true
	}
	return "", false
}
