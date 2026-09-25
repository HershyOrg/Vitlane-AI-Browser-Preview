// Package app은 Logistics의 조율자다: expected unit 등록(barrier의 근거),
// 운영자 shipment/tracking evidence 입력과 unit fulfillment 파생을 소유한다
// (계약 v7 §9, ADR-0052 — 자동 carrier effect 없음).
package app

import (
	"context"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/logistics/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type ShipmentView struct {
	Shipment domain.Shipment        `json:"shipment"`
	Units    []domain.ExpectedUnit  `json:"units"`
	Events   []domain.ShipmentEvent `json:"events"`
	Actions  []string               `json:"actions"`
}

type Repository interface {
	ListOrderShipments(ctx context.Context, agencyOrderID string) ([]ShipmentView, error)
	// CreateShipment는 merchant order의 AWAITING_EFFECT unit들(부분집합 가능)을
	// 새 shipment에 배정한다. unit당 active allocation은 최대 1이다.
	CreateShipment(ctx context.Context, merchantOrderID, carrier, trackingRef, operatorUserID string, expectedUnitIDs []string, now time.Time) (domain.Shipment, error)
	// RecordEvent는 append-only tracking evidence를 저장하고 허용 전이만
	// projection에 반영한다.
	RecordEvent(ctx context.Context, shipmentID, status, note, operatorUserID string, occurredAt, now time.Time) (domain.Shipment, error)
	// ConfirmDelivered는 패키지 delivered + unit 일괄 확인이다. exceptions의
	// unit은 MISSING/WRONG_ACTUAL로 분기해 resolution 대기로 남긴다.
	ConfirmDelivered(ctx context.Context, shipmentID, operatorUserID string, exceptions map[string]string, now time.Time) (domain.Shipment, error)
	// ListExceptionUnits는 판정 대기 예외 unit 큐다(MISSING/WRONG_ACTUAL/LOST,
	// 판정 없음).
	ListExceptionUnits(ctx context.Context, limit int) ([]domain.ExpectedUnit, error)
	// ListResolvedExceptionUnits는 판정 종결(RESOLVED 계열) 예외의 열람이다
	// (ADR-0057 — 운영자 "처리 완료" 탭, 행동 없음).
	ListResolvedExceptionUnits(ctx context.Context, limit int) ([]domain.ResolvedExceptionUnit, error)
	// ResolveDeliveryException은 write-once 판정이다. REFUND는 Payment의
	// MERCHANT_FAULT 환불 arm이 소비하고, DELIVERED_OK는 오탐을 정정한다.
	ResolveDeliveryException(ctx context.Context, expectedUnitID string, decision domain.ResolutionDecision, note, operatorUserID string, now time.Time) (domain.DeliveryResolution, bool, error)
	// CreateReturn은 §9.3 수동 회수 lane 시작이다(오배송·하자 실물).
	CreateReturn(ctx context.Context, expectedUnitID, note, operatorUserID string, now time.Time) (domain.Return, error)
	// UpdateReturn은 상태 전이·merchant disposition 기록이다. MERCHANT_REFUNDED
	// 처분은 간이 회수 원장 entry를 함께 만든다.
	UpdateReturn(ctx context.Context, returnID, state, merchantDisposition, note, operatorUserID string, now time.Time) (domain.Return, error)
	// ListReturns는 회수 lane 목록이다(openOnly면 CLOSED/CANCELLED 제외) —
	// 운영자 화면이 세션 상태가 아닌 서버 진실로 진행을 렌더한다.
	ListReturns(ctx context.Context, openOnly bool, limit int) ([]domain.Return, error)
	// ListClosedReturns는 종결(CLOSED·CANCELLED) 회수의 열람이다(ADR-0057).
	ListClosedReturns(ctx context.Context, limit int) ([]domain.Return, error)
	CountExceptionUnits(ctx context.Context) (int, error)
	CountOpenReturns(ctx context.Context) (int, error)
}

type DeliverySupportProjection struct {
	Resolution     domain.DeliveryResolution
	OperatorUserID string
}

type Service struct {
	inputs     procmsg.ActionInputs
	repository Repository
	clock      sharedapp.Clock
}

// deliveryLookupRepository는 SUPPORT executor의 판정 사실 조회다(ADR-0070 §4.5).
type deliveryLookupRepository interface {
	GetDeliveryResolution(ctx context.Context, resolutionID string) (DeliverySupportProjection, error)
}

// GetDeliveryResolution은 commit된 배송 판정과 판정 운영자다.
func (s *Service) GetDeliveryResolution(
	ctx context.Context,
	resolutionID string,
) (DeliverySupportProjection, error) {
	lookup, ok := s.repository.(deliveryLookupRepository)
	if !ok {
		return DeliverySupportProjection{}, domain.ErrResolutionNotFound
	}
	return lookup.GetDeliveryResolution(ctx, strings.TrimSpace(resolutionID))
}

func NewService(repository Repository, clock sharedapp.Clock) *Service {
	return &Service{repository: repository, clock: clock}
}

func (s *Service) ListOrderShipments(ctx context.Context, agencyOrderID string) ([]ShipmentView, error) {
	views, err := s.repository.ListOrderShipments(ctx, strings.TrimSpace(agencyOrderID))
	if err != nil {
		return nil, err
	}
	for index := range views {
		views[index].Actions = shipmentActions(views[index].Shipment.State)
	}
	return views, nil
}

func shipmentActions(state domain.ShipmentState) []string {
	actions := make([]string, 0, 2)
	if domain.AllowedTransition(state, domain.ShipmentInTransit) && state != domain.ShipmentInTransit {
		actions = append(actions, "RECORD_IN_TRANSIT")
	}
	if domain.AllowedTransition(state, domain.ShipmentDelivered) && state != domain.ShipmentDelivered {
		actions = append(actions, "CONFIRM_DELIVERY_OUTCOME")
	}
	return actions
}

func (s *Service) CreateShipment(
	ctx context.Context,
	merchantOrderID, carrier, trackingRef, operatorUserID string,
	expectedUnitIDs []string,
) (domain.Shipment, error) {
	if err := domain.ValidateShipmentInput(carrier, trackingRef); err != nil {
		return domain.Shipment{}, err
	}
	return s.repository.CreateShipment(
		ctx, strings.TrimSpace(merchantOrderID), strings.TrimSpace(carrier),
		strings.TrimSpace(trackingRef), strings.TrimSpace(operatorUserID),
		expectedUnitIDs, s.clock.Now(),
	)
}

func (s *Service) RecordEvent(
	ctx context.Context,
	shipmentID, status, note, operatorUserID string,
	occurredAt time.Time,
) (domain.Shipment, error) {
	status = strings.ToUpper(strings.TrimSpace(status))
	if status == "" || occurredAt.IsZero() {
		return domain.Shipment{}, domain.ErrShipmentInvalid
	}
	return s.repository.RecordEvent(
		ctx, strings.TrimSpace(shipmentID), status, strings.TrimSpace(note),
		strings.TrimSpace(operatorUserID), occurredAt, s.clock.Now(),
	)
}

func (s *Service) ConfirmDelivered(
	ctx context.Context,
	shipmentID, operatorUserID string,
	exceptions map[string]string,
) (domain.Shipment, error) {
	for _, kind := range exceptions {
		if kind != string(domain.FulfillmentMissing) && kind != string(domain.FulfillmentWrongActual) {
			return domain.Shipment{}, domain.ErrShipmentInvalid
		}
	}
	return s.repository.ConfirmDelivered(
		ctx, strings.TrimSpace(shipmentID), strings.TrimSpace(operatorUserID),
		exceptions, s.clock.Now(),
	)
}

func (s *Service) ListExceptionUnits(ctx context.Context, limit int) ([]domain.ExpectedUnit, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repository.ListExceptionUnits(ctx, limit)
}

// ListResolvedExceptionUnits는 판정 종결 예외의 열람이다(ADR-0057 — 운영자
// "처리 완료" 탭, 행동 없음).
func (s *Service) ListResolvedExceptionUnits(ctx context.Context, limit int) ([]domain.ResolvedExceptionUnit, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repository.ListResolvedExceptionUnits(ctx, limit)
}

func (s *Service) ResolveDeliveryException(
	ctx context.Context,
	expectedUnitID, decision, note, operatorUserID string,
) (domain.DeliveryResolution, bool, error) {
	typed, err := domain.ValidateResolutionDecision(strings.ToUpper(strings.TrimSpace(decision)))
	if err != nil {
		return domain.DeliveryResolution{}, false, err
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) < 1 || len([]rune(note)) > 2000 {
		return domain.DeliveryResolution{}, false, domain.ErrResolutionInvalid
	}
	operatorUserID = strings.TrimSpace(operatorUserID)
	resolution, replayed, err := s.repository.ResolveDeliveryException(
		ctx, strings.TrimSpace(expectedUnitID), typed, note,
		strings.TrimSpace(operatorUserID), s.clock.Now(),
	)
	// DELIVERY_RESOLUTION 카드는 리듀서가 delivery_fault.judged 이벤트에서
	// 발행한다(ADR-0070 §4.5).
	return resolution, replayed, err
}

func (s *Service) CreateReturn(
	ctx context.Context,
	expectedUnitID, note, operatorUserID string,
) (domain.Return, error) {
	if len(note) > 2000 {
		return domain.Return{}, domain.ErrReturnInvalid
	}
	return s.repository.CreateReturn(
		ctx, strings.TrimSpace(expectedUnitID), strings.TrimSpace(note),
		strings.TrimSpace(operatorUserID), s.clock.Now(),
	)
}

func (s *Service) UpdateReturn(
	ctx context.Context,
	returnID, state, merchantDisposition, note, operatorUserID string,
) (domain.Return, error) {
	state = strings.ToUpper(strings.TrimSpace(state))
	merchantDisposition = strings.ToUpper(strings.TrimSpace(merchantDisposition))
	if err := domain.ValidateMerchantDisposition(merchantDisposition); err != nil {
		return domain.Return{}, err
	}
	if len(note) > 2000 {
		return domain.Return{}, domain.ErrReturnInvalid
	}
	return s.repository.UpdateReturn(
		ctx, strings.TrimSpace(returnID), state, merchantDisposition,
		strings.TrimSpace(note), strings.TrimSpace(operatorUserID), s.clock.Now(),
	)
}

func (s *Service) ListReturns(ctx context.Context, openOnly bool, limit int) ([]domain.Return, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.repository.ListReturns(ctx, openOnly, limit)
}

// ListClosedReturns는 종결 회수의 열람이다(ADR-0057 — 운영자 "처리 완료" 탭).
func (s *Service) ListClosedReturns(ctx context.Context, limit int) ([]domain.Return, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.repository.ListClosedReturns(ctx, limit)
}

// CountExceptionUnits·CountOpenReturns는 nav 뱃지용 전역 카운트다(2차 P2).
func (s *Service) CountExceptionUnits(ctx context.Context) (int, error) {
	return s.repository.CountExceptionUnits(ctx)
}

func (s *Service) CountOpenReturns(ctx context.Context) (int, error) {
	return s.repository.CountOpenReturns(ctx)
}
