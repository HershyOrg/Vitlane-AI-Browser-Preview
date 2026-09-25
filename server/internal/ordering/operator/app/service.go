// Package app은 운영자 work surface의 read-only 합성이다(ADR-0055 §5).
// 결제 대사·조달 실행·환불 요청 심사·배송 예외 판정·회수 진행 큐를 하나의
// OperatorWorkItem 계약으로 통일해 목록으로 내린다. 큐·행동·감사의 소유는
// 각 컨텍스트에 그대로 남고(계약 v7 §14), 이 제품은 다른 제품의 app 포트를
// 읽기만 한다 — 명령은 여전히 owner의 endpoint로 간다.
package app

import (
	"context"
	"sort"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	logisticsdomain "github.com/vitlane/vitlane/server/internal/ordering/logistics/domain"
	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	paymentdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	processapp "github.com/vitlane/vitlane/server/internal/ordering/process/app"
	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type WorkItemKind string

const (
	KindPaymentReconciliation WorkItemKind = "PAYMENT_RECONCILIATION"
	KindProcurementExecution  WorkItemKind = "PROCUREMENT_EXECUTION"
	KindRefundReview          WorkItemKind = "REFUND_REVIEW"
	KindDeliveryResolution    WorkItemKind = "DELIVERY_RESOLUTION"
	KindReturnProgress        WorkItemKind = "RETURN_PROGRESS"
	// KindProcessIntervention은 재시도 소진(EXHAUSTED) Effect다(ADR-0056 §3) —
	// RETRY는 항상 열고, 자금 보상 Effect에는 ABANDON을 노출하지 않는다.
	KindProcessIntervention WorkItemKind = "PROCESS_INTERVENTION"
)

// WorkItem은 운영자 작업 하나의 통일 표현이다. Actions는 owner 계약이 이
// 상태에서 허용하는 다음 행동의 이름이며(자문 공간 — 권위 검증은 owner
// endpoint), Detail은 kind별 owner 사영 원문이다.
type WorkItem struct {
	Kind                   WorkItemKind                                     `json:"kind"`
	ID                     string                                           `json:"id"`
	AgencyOrderID          string                                           `json:"agencyOrderId"`
	State                  string                                           `json:"state"`
	AssignedOperatorUserID string                                           `json:"assignedOperatorUserId,omitempty"`
	UpdatedAt              time.Time                                        `json:"updatedAt"`
	Actions                []string                                         `json:"actions"`
	Operational            *agencydomain.MerchantOrderOperationalProjection `json:"operational,omitempty"`
	AssignmentState        string                                           `json:"assignmentState,omitempty"`
	Detail                 any                                              `json:"detail"`
	Accounting             *paymentdomain.OrderAccountingProjection         `json:"accounting,omitempty"`
}

type WorkItemCounts struct {
	PaymentReconciliation int `json:"PAYMENT_RECONCILIATION"`
	ProcurementExecution  int `json:"PROCUREMENT_EXECUTION"`
	RefundReview          int `json:"REFUND_REVIEW"`
	DeliveryResolution    int `json:"DELIVERY_RESOLUTION"`
	ReturnProgress        int `json:"RETURN_PROGRESS"`
	ProcessIntervention   int `json:"PROCESS_INTERVENTION"`
}

type WorkSurface struct {
	Items  []WorkItem     `json:"items"`
	Counts WorkItemCounts `json:"counts"`
}

// WorkView는 work surface의 열람 축이다(ADR-0057): OPEN은 행동이 열린 현재
// 큐(기본), RESOLVED는 종결 항목의 감사 열람(행동 없음)이다.
type WorkView string

const (
	WorkViewOpen     WorkView = "OPEN"
	WorkViewResolved WorkView = "RESOLVED"
)

// 각 owner의 목록 포트 — 구현은 wiring이 owner app service를 주입한다.
type ProcurementQueuePort interface {
	ListQueue(ctx context.Context, limit int) ([]procurementapp.QueueItem, error)
	CountOpenTasks(ctx context.Context) (int, error)
}

type RefundReviewPort interface {
	ListRefundQueue(ctx context.Context, limit int) ([]agencydomain.RefundRequest, error)
	ListResolvedRefundQueue(ctx context.Context, limit int) ([]agencydomain.RefundRequest, error)
	CountOpenRefundQueue(ctx context.Context) (int, error)
}

type LogisticsResolutionPort interface {
	ListExceptionUnits(ctx context.Context, limit int) ([]logisticsdomain.ExpectedUnit, error)
	ListReturns(ctx context.Context, openOnly bool, limit int) ([]logisticsdomain.Return, error)
	ListResolvedExceptionUnits(ctx context.Context, limit int) ([]logisticsdomain.ResolvedExceptionUnit, error)
	ListClosedReturns(ctx context.Context, limit int) ([]logisticsdomain.Return, error)
	CountExceptionUnits(ctx context.Context) (int, error)
	CountOpenReturns(ctx context.Context) (int, error)
}

// ProcessInterventionPort는 소진 Effect 큐의 read-only 관찰이다(owner:
// ordering/process — 허용된 RETRY/ABANDON 명령도 그 owner endpoint로 간다).
type ProcessInterventionPort interface {
	ListInterventions(ctx context.Context, limit int) ([]processapp.InterventionItem, error)
	CountInterventions(ctx context.Context) (int, error)
}

// PaymentReconciliationPort is Payment's read-only exception queue. It never
// creates Procurement work; only an accepted FundsReceipt can do that.
type PaymentReconciliationPort interface {
	ListPaymentReconciliations(ctx context.Context, limit int) ([]paymentapp.PaymentReconciliationItem, error)
	CountPaymentReconciliations(ctx context.Context) (int, error)
}

// PayPalResourceAdoptionPort projects only provider operations for which the
// original write is no longer safely retryable and Payment can therefore use
// its explicit GET-only adoption command. The owner command still rechecks
// every predicate under lock; this port only controls operator visibility.
type PayPalResourceAdoptionPort interface {
	ListPayPalResourceAdoptions(
		ctx context.Context, now time.Time, limit int,
	) ([]PayPalResourceAdoptionItem, error)
	CountPayPalResourceAdoptions(ctx context.Context, now time.Time) (int, error)
}

type AccountingReader interface {
	GetOrderAccounting(context.Context, string) (
		paymentdomain.OrderAccountingProjection, bool, error,
	)
}

// LivePayPalOrderCounter is a read-only navigation signal. It counts LIVE
// AgencyOrders that crossed the customer PayPal authorization boundary and
// never grants work-item actions.
type LivePayPalOrderCounter interface {
	CountLivePayPalOrders(context.Context) (int, error)
}

type Service struct {
	procurement    ProcurementQueuePort
	refunds        RefundReviewPort
	logistics      LogisticsResolutionPort
	intervention   ProcessInterventionPort
	payment        PaymentReconciliationPort
	paypalAdoption PayPalResourceAdoptionPort
	accounting     AccountingReader
	livePayPal     LivePayPalOrderCounter
	lookup         OrderLookupRepository
	orderEvidence  OrderEvidenceReader
	clock          sharedapp.Clock
}

func (s *Service) EnableAccounting(reader AccountingReader) {
	s.accounting = reader
}

func (s *Service) EnableLivePayPalOrderCount(counter LivePayPalOrderCounter) {
	s.livePayPal = counter
}

func NewService(
	procurement ProcurementQueuePort,
	refunds RefundReviewPort,
	logistics LogisticsResolutionPort,
	clock sharedapp.Clock,
) *Service {
	return &Service{
		procurement: procurement, refunds: refunds, logistics: logistics, clock: clock,
	}
}

// EnableProcessIntervention은 소진 Effect 큐를 합성에 편입한다(ADR-0056).
func (s *Service) EnableProcessIntervention(port ProcessInterventionPort) {
	s.intervention = port
}

func (s *Service) EnablePaymentReconciliation(port PaymentReconciliationPort) {
	s.payment = port
}

func (s *Service) EnablePayPalResourceAdoption(port PayPalResourceAdoptionPort) {
	s.paypalAdoption = port
}

// procurementActions는 Task 상태별 다음 행동이다(owner: procurement §14).
// exact-MO ISSUE·DONE은 예외 처리 또는 종결이 소유하므로 이 MO의 조달 행동만
// 닫는다. AgencyOrderProcess는 주문 요약 cursor이고 형제 MO 행동의 입력이 아니다.
// 할당됐어도 lease가 만료면 진행 행동을 닫고 CLAIM만 연다 — owner 재검사
// (requireActiveAssignment)가 어차피 거절할 행동을 화면에 남기지 않고,
// 재담당이 선행 조건임을 행동 공간으로 말한다(운영정합 5차 B1).
func procurementActions(
	item procurementapp.QueueItem,
	operational agencydomain.MerchantOrderOperationalProjection,
	now time.Time,
) []string {
	if operational.WorkStage == agencydomain.MOWorkIssue ||
		operational.WorkStage == agencydomain.MOWorkDone {
		return []string{}
	}
	switch item.Task.State {
	case "QUEUED":
		return []string{"CLAIM"}
	case "CLAIMED", "IN_PROGRESS":
		if item.Task.LeaseUntil == nil || !item.Task.LeaseUntil.After(now) {
			return []string{"CLAIM"}
		}
		actions := []string{"REVEAL_SHIPPING", "REVEAL_CONTINUE_URL"}
		// RECORD_PLACED is not a generic claimed-task action. The owner accepts
		// it only after BeginMerchantEffect has created the exact-MO STARTED
		// lock and moved the MerchantOrder to PLACEMENT_PENDING. Keeping this
		// distinction on the server lets Web render the evidence form from the
		// action contract instead of duplicating MerchantOrder state checks.
		switch item.MerchantOrder.State {
		case "PLANNED":
			actions = append(actions, "RECORD_FAILURE")
		case "PLACEMENT_PENDING":
			actions = append(actions, "RECORD_PLACED", "RECORD_FAILURE")
		}
		return actions
	case "SUCCEEDED":
		if operational.WorkStage == agencydomain.MOWorkLogistics {
			actions := make([]string, 0, 3)
			if operational.Units.AwaitingShipment > 0 {
				actions = append(actions, "CREATE_SHIPMENT")
			}
			if operational.Units.InTransit > 0 {
				actions = append(actions, "RECORD_SHIPMENT_EVENT", "CONFIRM_DELIVERY_OUTCOME")
			}
			return actions
		}
	}
	return []string{}
}

func procurementOperational(
	item procurementapp.QueueItem,
) agencydomain.MerchantOrderOperationalProjection {
	total := item.LogisticsSummary.ExpectedUnits
	if total == 0 {
		total = len(item.Units)
	}
	counts := agencydomain.MerchantOrderUnitCounts{
		Total:            total,
		AwaitingShipment: item.LogisticsSummary.AwaitingUnits,
		InTransit:        item.LogisticsSummary.InTransitUnits,
		Delivered:        item.LogisticsSummary.DeliveredUnits,
		Exception:        item.LogisticsSummary.ExceptionUnits,
		ReturnInProgress: item.LogisticsSummary.ReturnInProgressUnits,
	}
	if item.CompensationState == "SUCCEEDED" &&
		(item.CompensationAction == "REFUND" || item.CompensationAction == "TVIT_REFUND") {
		counts.Refunded = total
	} else if item.CompensationState == "APPROVED" ||
		item.CompensationState == "EXECUTION_PENDING" || item.ResolutionDecision == "REFUND" {
		counts.RefundPending = total
	} else if item.RefundRequestState == "REQUESTED" || item.RefundRequestState == "REVIEWING" {
		counts.RefundRequested = total
	}
	if item.MerchantOrder.State == "FAILED" {
		counts.ProcurementFailed = total
	}
	if item.MerchantOrder.State == "CANCELLED" {
		counts.Cancelled = total
	}
	return agencydomain.DeriveMerchantOrderOperationalProjection(
		agencydomain.MerchantOrderOperationalFacts{
			MerchantOrderState: string(item.MerchantOrder.State),
			TaskState:          string(item.Task.State),
			FundingState:       item.Funding.State,
			RefundRequestState: item.RefundRequestState,
			CancellationState:  item.CancellationState,
			ResolutionCause:    item.ResolutionCause,
			ResolutionDecision: item.ResolutionDecision,
			ReturnState:        item.ReturnState,
			CompensationAction: item.CompensationAction,
			CompensationState:  item.CompensationState,
			DisputeState:       item.DisputeState,
			DisputeOutcome:     item.DisputeOutcome,
			Units:              counts,
		},
	)
}

func procurementAssignmentState(
	item procurementapp.QueueItem,
	operational agencydomain.MerchantOrderOperationalProjection,
	now time.Time,
) string {
	// Procurement Task terminality does not end ownership while the exact MO
	// still has Logistics or Issue work. Only the canonical MO work stage can
	// retire the row from the active assignment partition.
	if operational.WorkStage == agencydomain.MOWorkDone {
		return "COMPLETED"
	}
	if item.Task.AssignedOperatorUserID == "" {
		return "UNASSIGNED"
	}
	if item.Task.LeaseUntil == nil || !item.Task.LeaseUntil.After(now) {
		return "EXPIRED"
	}
	return "ACTIVE"
}

func (s *Service) accountingFor(
	ctx context.Context,
	agencyOrderID string,
	cache map[string]*paymentdomain.OrderAccountingProjection,
) (*paymentdomain.OrderAccountingProjection, error) {
	if s.accounting == nil {
		return nil, nil
	}
	if cached, ok := cache[agencyOrderID]; ok {
		return cached, nil
	}
	position, found, err := s.accounting.GetOrderAccounting(ctx, agencyOrderID)
	if err != nil {
		return nil, err
	}
	if !found {
		cache[agencyOrderID] = nil
		return nil, nil
	}
	copy := position
	cache[agencyOrderID] = &copy
	return &copy, nil
}

func returnActions(value logisticsdomain.Return) []string {
	switch value.State {
	case logisticsdomain.ReturnRequested:
		return []string{"MARK_RETURN_IN_TRANSIT", "CANCEL_RETURN"}
	case logisticsdomain.ReturnInTransit:
		return []string{"MARK_RECEIVED", "CANCEL_RETURN"}
	case logisticsdomain.ReturnReceived:
		return []string{"MARK_MERCHANT_RETURNED", "CLOSE_RETURN"}
	case logisticsdomain.ReturnMerchantReturned:
		return []string{"CLOSE_RETURN"}
	}
	return []string{}
}

// List는 4개 큐를 한 번에 관찰해 kind 우선순위(실행 → 환불 심사 → 예외 판정
// → 회수), 같은 kind 안에서는 최근 갱신순으로 내린다. 부분 실패는 없다 —
// 한 owner 조회가 실패하면 전체가 실패한다(운영 화면이 절반 진실을 보이지
// 않게).
func (s *Service) List(ctx context.Context, view WorkView, limit int) (WorkSurface, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if view == WorkViewResolved {
		return s.listResolved(ctx, limit)
	}
	surface := WorkSurface{Items: make([]WorkItem, 0)}
	accountingCache := make(map[string]*paymentdomain.OrderAccountingProjection)
	now := s.clock.Now()

	if s.payment != nil {
		payments, err := s.payment.ListPaymentReconciliations(ctx, limit)
		if err != nil {
			return WorkSurface{}, err
		}
		for _, item := range payments {
			surface.Items = append(surface.Items, WorkItem{
				Kind: KindPaymentReconciliation, ID: item.PaymentID,
				AgencyOrderID: item.AgencyOrderID, State: string(item.PaymentState),
				UpdatedAt: item.UpdatedAt, Actions: []string{}, Detail: item,
			})
			surface.Counts.PaymentReconciliation++
		}
	}
	if s.paypalAdoption != nil {
		adoptions, err := s.paypalAdoption.ListPayPalResourceAdoptions(ctx, now, limit)
		if err != nil {
			return WorkSurface{}, err
		}
		for _, item := range adoptions {
			surface.Items = append(surface.Items, WorkItem{
				Kind: KindPaymentReconciliation, ID: item.OperationID,
				AgencyOrderID: item.AgencyOrderID, State: item.OperationState,
				UpdatedAt: item.UpdatedAt, Actions: item.Actions(), Detail: item,
			})
			surface.Counts.PaymentReconciliation++
		}
	}

	if s.intervention != nil {
		interventions, err := s.intervention.ListInterventions(ctx, limit)
		if err != nil {
			return WorkSurface{}, err
		}
		for _, item := range interventions {
			surface.Items = append(surface.Items, WorkItem{
				Kind: KindProcessIntervention, ID: item.EffectID,
				AgencyOrderID: item.AgencyOrderID,
				State:         "ATTENTION_REQUIRED",
				UpdatedAt:     item.UpdatedAt,
				Actions:       processapp.InterventionActions(item.Type),
				Detail:        item,
			})
			surface.Counts.ProcessIntervention++
		}
	}
	queue, err := s.procurement.ListQueue(ctx, limit)
	if err != nil {
		return WorkSurface{}, err
	}
	// 주문 회계는 read projection이라 행동 공간에 개입하지 않는다. 음수 forecast나
	// 미대사 수납도 owner endpoint의 상태 전이 권한을 대신하지 않는다.
	for _, item := range queue {
		operational := procurementOperational(item)
		accounting, err := s.accountingFor(ctx, item.Task.AgencyOrderID, accountingCache)
		if err != nil {
			return WorkSurface{}, err
		}
		surface.Items = append(surface.Items, WorkItem{
			Kind: KindProcurementExecution, ID: item.Task.ID,
			AgencyOrderID:          item.Task.AgencyOrderID,
			State:                  string(item.Task.State),
			AssignedOperatorUserID: item.Task.AssignedOperatorUserID,
			UpdatedAt:              item.Task.UpdatedAt,
			Actions:                procurementActions(item, operational, now),
			Operational:            &operational,
			AssignmentState:        procurementAssignmentState(item, operational, now),
			Detail:                 item,
			Accounting:             accounting,
		})
		surface.Counts.ProcurementExecution++
	}

	requests, err := s.refunds.ListRefundQueue(ctx, limit)
	if err != nil {
		return WorkSurface{}, err
	}
	for _, request := range requests {
		accounting, err := s.accountingFor(ctx, request.AgencyOrderID, accountingCache)
		if err != nil {
			return WorkSurface{}, err
		}
		surface.Items = append(surface.Items, WorkItem{
			Kind: KindRefundReview, ID: request.ID,
			AgencyOrderID: request.AgencyOrderID,
			State:         string(request.State),
			UpdatedAt:     request.UpdatedAt,
			Actions:       []string{"APPROVE_ITEMS", "REJECT_ITEMS"},
			Detail:        request,
			Accounting:    accounting,
		})
		surface.Counts.RefundReview++
	}

	exceptions, err := s.logistics.ListExceptionUnits(ctx, limit)
	if err != nil {
		return WorkSurface{}, err
	}
	for _, unit := range exceptions {
		actions := []string{"RESOLVE_REFUND", "RESOLVE_DELIVERED_OK"}
		if unit.Fulfillment == logisticsdomain.FulfillmentWrongActual {
			actions = append(actions, "START_RETURN")
		}
		surface.Items = append(surface.Items, WorkItem{
			Kind: KindDeliveryResolution, ID: unit.ID,
			AgencyOrderID: unit.AgencyOrderID,
			State:         string(unit.Fulfillment),
			UpdatedAt:     unit.UpdatedAt,
			Actions:       actions,
			Detail:        unit,
		})
		surface.Counts.DeliveryResolution++
	}

	returns, err := s.logistics.ListReturns(ctx, true, limit)
	if err != nil {
		return WorkSurface{}, err
	}
	for _, value := range returns {
		surface.Items = append(surface.Items, WorkItem{
			Kind: KindReturnProgress, ID: value.ID,
			AgencyOrderID: value.AgencyOrderID,
			State:         string(value.State),
			UpdatedAt:     value.UpdatedAt,
			Actions:       returnActions(value),
			Detail:        value,
		})
		surface.Counts.ReturnProgress++
	}

	sortWorkItems(surface.Items)
	return surface, nil
}

// listResolved는 종결 항목의 감사 열람이다(ADR-0057 — 운영자 "처리 완료" 탭).
// 행동 공간은 비어 있고, 명령 endpoint는 이 view의 항목을 받지 않는다.
func (s *Service) listResolved(ctx context.Context, limit int) (WorkSurface, error) {
	surface := WorkSurface{Items: make([]WorkItem, 0)}

	requests, err := s.refunds.ListResolvedRefundQueue(ctx, limit)
	if err != nil {
		return WorkSurface{}, err
	}
	for _, request := range requests {
		surface.Items = append(surface.Items, WorkItem{
			Kind: KindRefundReview, ID: request.ID,
			AgencyOrderID: request.AgencyOrderID,
			State:         string(request.State),
			UpdatedAt:     request.UpdatedAt,
			Actions:       []string{},
			Detail:        request,
		})
		surface.Counts.RefundReview++
	}

	units, err := s.logistics.ListResolvedExceptionUnits(ctx, limit)
	if err != nil {
		return WorkSurface{}, err
	}
	for _, unit := range units {
		surface.Items = append(surface.Items, WorkItem{
			Kind: KindDeliveryResolution, ID: unit.ID,
			AgencyOrderID: unit.AgencyOrderID,
			State:         string(unit.Fulfillment),
			UpdatedAt:     unit.UpdatedAt,
			Actions:       []string{},
			Detail:        unit,
		})
		surface.Counts.DeliveryResolution++
	}

	returns, err := s.logistics.ListClosedReturns(ctx, limit)
	if err != nil {
		return WorkSurface{}, err
	}
	for _, value := range returns {
		surface.Items = append(surface.Items, WorkItem{
			Kind: KindReturnProgress, ID: value.ID,
			AgencyOrderID: value.AgencyOrderID,
			State:         string(value.State),
			UpdatedAt:     value.UpdatedAt,
			Actions:       []string{},
			Detail:        value,
		})
		surface.Counts.ReturnProgress++
	}

	sortWorkItems(surface.Items)
	return surface, nil
}

// Counts는 kind별 전역 카운트다(ADR-0057 2차 P2) — 목록 limit 캡과 무관한
// SQL COUNT라 nav 뱃지의 근거로 정확하다. 부분 실패는 전체 실패다.
func (s *Service) Counts(ctx context.Context) (WorkItemCounts, error) {
	counts := WorkItemCounts{}
	var err error
	if s.intervention != nil {
		if counts.ProcessIntervention, err = s.intervention.CountInterventions(ctx); err != nil {
			return WorkItemCounts{}, err
		}
	}
	if s.payment != nil {
		if counts.PaymentReconciliation, err = s.payment.CountPaymentReconciliations(ctx); err != nil {
			return WorkItemCounts{}, err
		}
	}
	if s.paypalAdoption != nil {
		adoptions, countErr := s.paypalAdoption.CountPayPalResourceAdoptions(
			ctx, s.clock.Now(),
		)
		if countErr != nil {
			return WorkItemCounts{}, countErr
		}
		counts.PaymentReconciliation += adoptions
	}
	if counts.ProcurementExecution, err = s.procurement.CountOpenTasks(ctx); err != nil {
		return WorkItemCounts{}, err
	}
	if counts.RefundReview, err = s.refunds.CountOpenRefundQueue(ctx); err != nil {
		return WorkItemCounts{}, err
	}
	if counts.DeliveryResolution, err = s.logistics.CountExceptionUnits(ctx); err != nil {
		return WorkItemCounts{}, err
	}
	if counts.ReturnProgress, err = s.logistics.CountOpenReturns(ctx); err != nil {
		return WorkItemCounts{}, err
	}
	return counts, nil
}

// CountLivePayPalOrders returns the exact global post-authorization order count
// used by operator navigation. It includes every later authorization state and
// remains separate from open work-item counts.
func (s *Service) CountLivePayPalOrders(ctx context.Context) (int, error) {
	if s.livePayPal == nil {
		return 0, nil
	}
	return s.livePayPal.CountLivePayPalOrders(ctx)
}

func sortWorkItems(items []WorkItem) {
	kindOrder := map[WorkItemKind]int{
		// 확정 수납 전 오류와 소진 Effect가 가장 앞이다.
		KindPaymentReconciliation: 0, KindProcessIntervention: 1,
		KindProcurementExecution: 2, KindRefundReview: 3,
		KindDeliveryResolution: 4, KindReturnProgress: 5,
	}
	sort.SliceStable(items, func(a, b int) bool {
		left, right := items[a], items[b]
		if kindOrder[left.Kind] != kindOrder[right.Kind] {
			return kindOrder[left.Kind] < kindOrder[right.Kind]
		}
		return left.UpdatedAt.After(right.UpdatedAt)
	})
}
