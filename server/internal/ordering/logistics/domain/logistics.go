// Package domain은 Logistics Bounded Context의 자기 축이다(ADR-0052, 계약 v7
// §4.5·§9). 기대(expected unit)와 실제 패키지(Shipment)를 분리해 분할 배송·
// 누락·오배송·수령을 표현한다. Phase 8의 유일한 물리 진실 입력은 운영자
// evidence이며(자동 polling 비범위), money obligation은 소유하지 않는다.
package domain

import (
	"errors"
	"slices"
	"strings"
	"time"
)

var (
	ErrShipmentNotFound   = errors.New("LOGISTICS_SHIPMENT_NOT_FOUND")
	ErrShipmentInvalid    = errors.New("LOGISTICS_SHIPMENT_INVALID")
	ErrTransitionInvalid  = errors.New("LOGISTICS_TRANSITION_INVALID")
	ErrUnitsNotAllocable  = errors.New("LOGISTICS_UNITS_NOT_ALLOCABLE")
	ErrEvidenceDuplicate  = errors.New("LOGISTICS_EVIDENCE_DUPLICATE")
	ErrOrderNotRegistered = errors.New("LOGISTICS_ORDER_NOT_REGISTERED")
	ErrUnitNotFound       = errors.New("LOGISTICS_UNIT_NOT_FOUND")
	ErrResolutionInvalid  = errors.New("LOGISTICS_RESOLUTION_INVALID")
	ErrReturnNotFound     = errors.New("LOGISTICS_RETURN_NOT_FOUND")
	ErrReturnInvalid      = errors.New("LOGISTICS_RETURN_INVALID")
)

type Fulfillment string

const (
	FulfillmentAwaitingEffect     Fulfillment = "AWAITING_EFFECT"
	FulfillmentInTransitExpected  Fulfillment = "IN_TRANSIT_EXPECTED"
	FulfillmentDeliveredExpected  Fulfillment = "DELIVERED_EXPECTED"
	FulfillmentMissing            Fulfillment = "MISSING"
	FulfillmentWrongActual        Fulfillment = "WRONG_ACTUAL"
	FulfillmentLost               Fulfillment = "LOST"
	FulfillmentReturned           Fulfillment = "RETURNED"
	FulfillmentNoPlacement        Fulfillment = "NO_PLACEMENT"
	FulfillmentSuperseded         Fulfillment = "SUPERSEDED_BY_CANCELLATION"
	FulfillmentResolutionPending  Fulfillment = "DELIVERY_RESOLUTION_PENDING"
	FulfillmentNonconformPending  Fulfillment = "NONCONFORMING_RESOLUTION_PENDING"
	FulfillmentResolved           Fulfillment = "RESOLVED"
	FulfillmentNonconformResolved Fulfillment = "NONCONFORMING_RESOLVED"
)

type ShipmentState string

const (
	ShipmentCreated        ShipmentState = "CREATED"
	ShipmentLabelCreated   ShipmentState = "LABEL_CREATED"
	ShipmentInTransit      ShipmentState = "IN_TRANSIT"
	ShipmentOutForDelivery ShipmentState = "OUT_FOR_DELIVERY"
	ShipmentDelivered      ShipmentState = "DELIVERED"
	ShipmentException      ShipmentState = "EXCEPTION"
	ShipmentLost           ShipmentState = "LOST"
	ShipmentReturnToSender ShipmentState = "RETURN_TO_SENDER"
	ShipmentReturned       ShipmentState = "RETURNED"
	ShipmentCancelledNoOp  ShipmentState = "CANCELLED_NO_EFFECT"
	ShipmentExceptionRecon ShipmentState = "EXCEPTION_RECONCILIATION"
)

// AllowedTransition은 계약 v7 §9.2의 package projection 전이다. carrier 상태는
// 단조롭지 않을 수 있으므로 event는 append-only로 먼저 저장되고, projection만
// 이 규칙을 따른다.
func AllowedTransition(from, to ShipmentState) bool {
	transitions := map[ShipmentState][]ShipmentState{
		ShipmentCreated:        {ShipmentLabelCreated, ShipmentInTransit, ShipmentCancelledNoOp},
		ShipmentLabelCreated:   {ShipmentInTransit, ShipmentCancelledNoOp},
		ShipmentInTransit:      {ShipmentOutForDelivery, ShipmentDelivered, ShipmentException, ShipmentLost, ShipmentReturnToSender},
		ShipmentOutForDelivery: {ShipmentDelivered, ShipmentException},
		ShipmentException:      {ShipmentInTransit, ShipmentDelivered, ShipmentLost, ShipmentReturnToSender},
		ShipmentReturnToSender: {ShipmentReturned},
		ShipmentDelivered:      {ShipmentExceptionRecon},
		ShipmentExceptionRecon: {ShipmentDelivered, ShipmentLost, ShipmentReturnToSender},
	}
	return from == to || slices.Contains(transitions[from], to)
}

type ExpectedUnit struct {
	ID                  string      `json:"id"`
	MerchantOrderUnitID string      `json:"merchantOrderUnitId"`
	MerchantOrderID     string      `json:"merchantOrderId"`
	AgencyOrderID       string      `json:"agencyOrderId"`
	LineID              string      `json:"lineId"`
	UnitIndex           int         `json:"unitIndex"`
	Fulfillment         Fulfillment `json:"fulfillment"`
	RegisteredAt        time.Time   `json:"registeredAt"`
	Version             int64       `json:"version"`
	UpdatedAt           time.Time   `json:"updatedAt"`
}

type Shipment struct {
	ID              string        `json:"id"`
	AgencyOrderID   string        `json:"agencyOrderId"`
	MerchantOrderID string        `json:"merchantOrderId"`
	Carrier         string        `json:"carrier"`
	TrackingRef     string        `json:"trackingRef"`
	State           ShipmentState `json:"state"`
	Version         int64         `json:"version"`
	CreatedAt       time.Time     `json:"createdAt"`
	UpdatedAt       time.Time     `json:"updatedAt"`
}

type ShipmentEvent struct {
	ID         string    `json:"id"`
	ShipmentID string    `json:"shipmentId"`
	Status     string    `json:"status"`
	Note       string    `json:"note,omitempty"`
	OccurredAt time.Time `json:"occurredAt"`
	CreatedAt  time.Time `json:"createdAt"`
}

func ValidateShipmentInput(carrier, trackingRef string) error {
	if strings.TrimSpace(carrier) == "" || len(carrier) > 100 ||
		strings.TrimSpace(trackingRef) == "" || len(trackingRef) > 200 {
		return ErrShipmentInvalid
	}
	return nil
}

// ResolutionDecision은 배송 예외(MISSING/WRONG_ACTUAL/LOST)에 대한 운영자
// write-once 판정이다(§9.1). REFUND는 해당 ExpectedUnit의 MerchantOrder 전체
// 보상으로 소비되고, DELIVERED_OK는 오탐 정정이다.
type ResolutionDecision string

const (
	ResolutionRefund      ResolutionDecision = "REFUND"
	ResolutionDeliveredOK ResolutionDecision = "DELIVERED_OK"
)

func ValidateResolutionDecision(decision string) (ResolutionDecision, error) {
	switch ResolutionDecision(decision) {
	case ResolutionRefund, ResolutionDeliveredOK:
		return ResolutionDecision(decision), nil
	}
	return "", ErrResolutionInvalid
}

// ResolvableFulfillment는 판정 대상 예외 상태다.
func ResolvableFulfillment(fulfillment Fulfillment) bool {
	return fulfillment == FulfillmentMissing ||
		fulfillment == FulfillmentWrongActual ||
		fulfillment == FulfillmentLost
}

type DeliveryResolution struct {
	ID              string             `json:"id"`
	ExpectedUnitID  string             `json:"expectedUnitId"`
	MerchantOrderID string             `json:"merchantOrderId"`
	AllocationID    string             `json:"allocationId"`
	AgencyOrderID   string             `json:"agencyOrderId"`
	Cause           Fulfillment        `json:"cause"`
	Decision        ResolutionDecision `json:"decision"`
	Note            string             `json:"note,omitempty"`
	CreatedAt       time.Time          `json:"createdAt"`
}

// ResolvedExceptionUnit is the immutable audit projection for one closed
// physical exception. ExpectedUnit remains the physical identity; the nested
// resolution and money/return outcomes explain what was actually closed.
type ResolvedExceptionUnit struct {
	ExpectedUnit
	Resolution         DeliveryResolution `json:"resolution"`
	ReturnState        string             `json:"returnState,omitempty"`
	CompensationAction string             `json:"compensationAction,omitempty"`
	CompensationState  string             `json:"compensationState,omitempty"`
}

// ReturnState는 §9.3 수동 회수 lane의 축소 상태기계다(전 operation 수동).
type ReturnState string

const (
	ReturnRequested        ReturnState = "REQUESTED"
	ReturnInTransit        ReturnState = "RETURN_IN_TRANSIT"
	ReturnReceived         ReturnState = "RECEIVED"
	ReturnMerchantReturned ReturnState = "MERCHANT_RETURNED"
	ReturnClosed           ReturnState = "CLOSED"
	ReturnCancelled        ReturnState = "CANCELLED"
)

func AllowedReturnTransition(from, to ReturnState) bool {
	transitions := map[ReturnState][]ReturnState{
		ReturnRequested:        {ReturnInTransit, ReturnCancelled},
		ReturnInTransit:        {ReturnReceived, ReturnCancelled},
		ReturnReceived:         {ReturnMerchantReturned, ReturnClosed},
		ReturnMerchantReturned: {ReturnClosed},
	}
	return from == to || slices.Contains(transitions[from], to)
}

// MerchantDisposition은 회수 실물의 처분이다. MERCHANT_REFUNDED는 간이 회수
// 원장(procurement_recovery_entries)과 연결된다.
func ValidateMerchantDisposition(disposition string) error {
	switch disposition {
	case "", "RESTOCKED", "MERCHANT_REFUNDED", "DISCARDED", "UNRESOLVED":
		return nil
	}
	return ErrReturnInvalid
}

type Return struct {
	ID                  string      `json:"id"`
	ExpectedUnitID      string      `json:"expectedUnitId"`
	AgencyOrderID       string      `json:"agencyOrderId"`
	State               ReturnState `json:"state"`
	MerchantDisposition string      `json:"merchantDisposition,omitempty"`
	Note                string      `json:"note,omitempty"`
	Version             int64       `json:"version"`
	CreatedAt           time.Time   `json:"createdAt"`
	UpdatedAt           time.Time   `json:"updatedAt"`
}
