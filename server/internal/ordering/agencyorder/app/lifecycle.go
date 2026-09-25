package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"strings"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

type LifecycleRepository interface {
	GetProjection(context.Context, string, string) (agencydomain.Projection, error)
	ListProjections(context.Context, string, ListQuery) (ListPage, error)
	GetProjectionForReceipt(context.Context, string) (agencydomain.Projection, error)
	ListFinalizedTransactions(context.Context, string) ([]agencydomain.ChainTransaction, error)
	CreateReceipt(context.Context, agencydomain.Receipt) error
	RecordDelayRuleNotice(ctx context.Context, agencyOrderID, idempotencyKey string, now time.Time) error
	// ResolveOrderOwner는 주문 소유 고객을 확인한다 — Support 대화의 주문
	// 첨부 검증 게이트웨이(ADR-0059 §5)가 이 서비스로 구조 충족된다.
	ResolveOrderOwner(ctx context.Context, agencyOrderID string) (string, error)
	GetReceipt(context.Context, string, string) (agencydomain.Receipt, error)
	CreateRefundRequest(context.Context, agencydomain.RefundRequest, RefundAuthority, time.Time) (agencydomain.RefundRequest, error)
	ListRefundRequests(ctx context.Context, userID, agencyOrderID string, openOnly bool, limit int) ([]agencydomain.RefundRequest, error)
	ListResolvedRefundRequests(ctx context.Context, limit int) ([]agencydomain.RefundRequest, error)
	CountOpenRefundRequests(ctx context.Context) (int, error)
	DecideRefundRequest(ctx context.Context, requestID, actorUserID string, decision RefundDecision, now time.Time) (agencydomain.RefundRequest, error)
}

// lifecycleLookupRepository는 SUPPORT executor의 owner 사실 조회다(ADR-0070 §4.5).
type lifecycleLookupRepository interface {
	GetRefundRequestByID(ctx context.Context, requestID string) (agencydomain.RefundRequest, error)
	GetDelayRuleNotice(ctx context.Context, agencyOrderID, idempotencyKey string) (agencydomain.CustomerNotice, error)
}

// RefundDecision is the single whole-MO outcome. There are no item decisions.
type RefundDecision struct {
	Approve         bool
	PublicRationale string
	InternalNote    string
}

type ListView string

const (
	// ListViewPaymentRequired — 고객 행동(PAY)이 필요한 유일한 상태를 진행
	// 중에 묻지 않는다(ADR-0057 D1).
	ListViewPaymentRequired ListView = "PAYMENT_REQUIRED"
	ListViewInProgress      ListView = "IN_PROGRESS"
	ListViewNeedsAttention  ListView = "NEEDS_ATTENTION"
	// ListViewFinished is the customer-facing history of every terminal order.
	// The terminal reason still distinguishes completed, refunded, cancelled,
	// and expired outcomes inside each order card.
	ListViewFinished ListView = "FINISHED"
	ListViewAll      ListView = "ALL"
)

type ListSort string

const (
	ListSortUpdatedDesc ListSort = "UPDATED_DESC"
	ListSortCreatedDesc ListSort = "CREATED_DESC"
	ListSortCreatedAsc  ListSort = "CREATED_ASC"
)

var ErrListQueryInvalid = errors.New("AGENCY_ORDER_LIST_QUERY_INVALID")

type ListCursor struct {
	View          ListView  `json:"view"`
	Sort          ListSort  `json:"sort"`
	UpdatedAt     time.Time `json:"updatedAt"`
	IssuedAt      time.Time `json:"issuedAt"`
	AgencyOrderID string    `json:"agencyOrderId"`
}

type ListQuery struct {
	View   ListView
	Sort   ListSort
	Limit  int
	Cursor *ListCursor
}

type ListCounts struct {
	PaymentRequired int64 `json:"PAYMENT_REQUIRED"`
	InProgress      int64 `json:"IN_PROGRESS"`
	NeedsAttention  int64 `json:"NEEDS_ATTENTION"`
	Finished        int64 `json:"FINISHED"`
	All             int64 `json:"ALL"`
}

type ListPage struct {
	Items      []agencydomain.Projection
	NextCursor *ListCursor
	Counts     ListCounts
}

type OperatorShippingAddress struct {
	RecipientName string `json:"recipientName"`
	AddressLine1  string `json:"addressLine1"`
	AddressLine2  string `json:"addressLine2,omitempty"`
	City          string `json:"city"`
	Region        string `json:"region"`
	PostalCode    string `json:"postalCode"`
	Country       string `json:"country"`
	Phone         string `json:"phone,omitempty"`
}

type OperatorShippingReader interface {
	RevealShippingSnapshot(context.Context, string) (OperatorShippingAddress, error)
}

type LifecycleService struct {
	inputs     procmsg.ActionInputs
	repository LifecycleRepository
	shipping   OperatorShippingReader
	clock      sharedapp.Clock
	ids        sharedapp.IDGenerator
}

// GetRefundRequestByID는 SUPPORT executor가 환불 카드를 만들 때 읽는 commit된
// 요청 사실이다.
func (s *LifecycleService) GetRefundRequestByID(
	ctx context.Context,
	requestID string,
) (agencydomain.RefundRequest, error) {
	lookup, ok := s.repository.(lifecycleLookupRepository)
	if !ok {
		return agencydomain.RefundRequest{}, agencydomain.ErrNotFound
	}
	return lookup.GetRefundRequestByID(ctx, strings.TrimSpace(requestID))
}

// GetDelayRuleNotice는 기록된 SYSTEM 지연 rule 고지다(카드 관찰 시각의 근거).
func (s *LifecycleService) GetDelayRuleNotice(
	ctx context.Context,
	agencyOrderID, idempotencyKey string,
) (agencydomain.CustomerNotice, error) {
	lookup, ok := s.repository.(lifecycleLookupRepository)
	if !ok {
		return agencydomain.CustomerNotice{}, agencydomain.ErrNotFound
	}
	return lookup.GetDelayRuleNotice(ctx, strings.TrimSpace(agencyOrderID), strings.TrimSpace(idempotencyKey))
}

func NewLifecycleService(
	repository LifecycleRepository,
	shipping OperatorShippingReader,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
) *LifecycleService {
	return &LifecycleService{repository: repository, shipping: shipping, clock: clock, ids: ids}
}

func (s *LifecycleService) Get(
	ctx context.Context,
	userID, agencyOrderID string,
) (agencydomain.Projection, error) {
	projection, err := s.repository.GetProjection(
		ctx, strings.TrimSpace(userID), strings.TrimSpace(agencyOrderID))
	if err != nil {
		return agencydomain.Projection{}, err
	}
	projection.AvailableActions = agencydomain.AvailableCustomerActions(projection, s.clock.Now())
	return projection, nil
}

// GetOperatorProjection은 운영자 조사 화면에 권위 주문 사영을 제공한다.
// 고객 행동 공간은 의도적으로 비워 두고, 호출자는 필요한 safe field만 별도
// 계약으로 투영한다.
func (s *LifecycleService) GetOperatorProjection(
	ctx context.Context,
	agencyOrderID string,
) (agencydomain.Projection, error) {
	projection, err := s.repository.GetProjectionForReceipt(ctx, strings.TrimSpace(agencyOrderID))
	if errors.Is(err, agencydomain.ErrNotFound) {
		return agencydomain.Projection{}, agencydomain.ErrNotFound
	}
	if err != nil {
		return agencydomain.Projection{}, err
	}
	projection.AvailableActions = nil
	return projection, nil
}

func (s *LifecycleService) List(
	ctx context.Context,
	userID string,
	query ListQuery,
) (ListPage, error) {
	if query.View == "" {
		query.View = ListViewInProgress
	}
	if query.Sort == "" {
		query.Sort = ListSortUpdatedDesc
	}
	if query.Limit == 0 {
		query.Limit = 30
	}
	if !validListQuery(query) || strings.TrimSpace(userID) == "" {
		return ListPage{}, ErrListQueryInvalid
	}
	page, err := s.repository.ListProjections(ctx, strings.TrimSpace(userID), query)
	if err != nil {
		return ListPage{}, err
	}
	now := s.clock.Now()
	for index := range page.Items {
		page.Items[index].AvailableActions =
			agencydomain.AvailableCustomerActions(page.Items[index], now)
	}
	return page, nil
}

func validListQuery(query ListQuery) bool {
	if query.Limit < 1 || query.Limit > 100 {
		return false
	}
	switch query.View {
	case ListViewPaymentRequired, ListViewInProgress, ListViewNeedsAttention,
		ListViewFinished, ListViewAll:
	default:
		return false
	}
	switch query.Sort {
	case ListSortUpdatedDesc, ListSortCreatedDesc, ListSortCreatedAsc:
	default:
		return false
	}
	if query.Cursor == nil {
		return true
	}
	return query.Cursor.AgencyOrderID != "" && !query.Cursor.IssuedAt.IsZero() &&
		query.Cursor.View == query.View && query.Cursor.Sort == query.Sort &&
		(query.Sort != ListSortUpdatedDesc || !query.Cursor.UpdatedAt.IsZero())
}

func (s *LifecycleService) RevealOwnerShipping(
	ctx context.Context,
	userID, agencyOrderID string,
) (OperatorShippingAddress, error) {
	if s.shipping == nil || strings.TrimSpace(userID) == "" ||
		strings.TrimSpace(agencyOrderID) == "" {
		return OperatorShippingAddress{}, agencydomain.ErrPIIAccessDenied
	}
	projection, err := s.repository.GetProjection(
		ctx, strings.TrimSpace(userID), strings.TrimSpace(agencyOrderID),
	)
	if err != nil {
		return OperatorShippingAddress{}, err
	}
	return s.shipping.RevealShippingSnapshot(
		ctx, projection.AgencyOrder.ShippingAddress.SnapshotRef,
	)
}

func (s *LifecycleService) GetReceipt(
	ctx context.Context,
	userID, agencyOrderID string,
) (agencydomain.Receipt, error) {
	return s.repository.GetReceipt(ctx, strings.TrimSpace(userID), strings.TrimSpace(agencyOrderID))
}

// ResolveOrderOwner는 Support 대화(ADR-0059)의 OrderingGateway 포트 구현이다.
// wiring이 이 서비스를 support app에 주입한다 — infra 교차 import 없음.
func (s *LifecycleService) ResolveOrderOwner(
	ctx context.Context,
	agencyOrderID string,
) (string, error) {
	return s.repository.ResolveOrderOwner(ctx, strings.TrimSpace(agencyOrderID))
}

// SendDelayRuleNotice는 process Effect(send_notice, kind=DELAY_RULE)의 실행
// 창구다(ADR-0056 — 종전 30일 전수 스캔 고지의 대체).
func (s *LifecycleService) SendDelayRuleNotice(
	ctx context.Context,
	agencyOrderID, idempotencyKey string,
) error {
	agencyOrderID = strings.TrimSpace(agencyOrderID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	// 고객 카드(DELIVERY_DELAY)는 리듀서가 notice.recorded 이벤트에서 발행한다
	// (ADR-0070 §4.5) — 이 실행은 고지 사실만 남긴다.
	return s.repository.RecordDelayRuleNotice(
		ctx, agencyOrderID, idempotencyKey, s.clock.Now(),
	)
}

// IssueReceipt는 process Effect(issue_receipt)의 실행 창구다(ADR-0056 —
// 종전 TERMINAL 후보 스캔 Tick의 대체). TERMINAL 근거가 아직 없으면(terminal
// 참조 미확보 등) 재시도 가능 오류로 올린다 — backoff가 재시도한다.
func (s *LifecycleService) IssueReceipt(ctx context.Context, agencyOrderID string) error {
	return s.issueReceipts(ctx, []string{strings.TrimSpace(agencyOrderID)})
}

type receiptClassification struct {
	Kind      string
	Watermark string
	LegalSale bool
}

// classifyReceipt derives the customer/accounting label from the immutable
// order profile, never from the deployment's current selector. A Live record
// says that real money was used; legalSale only becomes true when at least one
// LIVE merchant order actually reached PLACED. The record is still not a
// merchant tax receipt, which the watermark states explicitly.
func classifyReceipt(order agencydomain.AgencyOrder, merchants []agencydomain.MerchantOrderSummary) (receiptClassification, error) {
	profile := order.ExecutionProfile
	hash, err := profile.Hash()
	if err != nil || hash != order.ExecutionProfileHash ||
		!profile.MatchesSelection(order.PaymentSelection) {
		return receiptClassification{}, agencydomain.ErrInvalid
	}
	switch {
	case profile.PaymentRail == agencydomain.PaymentProviderPayPal &&
		profile.ProviderEnvironment == agencydomain.ProviderEnvironmentLive &&
		profile.Asset == agencydomain.AssetUSD &&
		profile.EconomicEffect == agencydomain.EconomicEffectRealMoney &&
		profile.MerchantExecutionMode == agencydomain.MerchantExecutionLiveEffect:
		legalSale := false
		for _, merchant := range merchants {
			if merchant.ExecutionMode != agencydomain.MerchantExecutionLiveEffect {
				return receiptClassification{}, agencydomain.ErrInvalid
			}
			legalSale = legalSale || merchant.State == "PLACED"
		}
		return receiptClassification{
			Kind:      agencydomain.ReceiptKindLiveOrderRecord,
			Watermark: "PAYPAL LIVE · REAL MONEY · VITLANE AGENCY-ORDER RECORD · NOT A MERCHANT TAX RECEIPT",
			LegalSale: legalSale,
		}, nil
	case profile.EconomicEffect == agencydomain.EconomicEffectNoRealValue &&
		profile.MerchantExecutionMode == agencydomain.MerchantExecutionSimulatedNoEffect:
		for _, merchant := range merchants {
			if merchant.ExecutionMode != agencydomain.MerchantExecutionSimulatedNoEffect {
				return receiptClassification{}, agencydomain.ErrInvalid
			}
		}
		return receiptClassification{
			Kind:      agencydomain.ReceiptKindTest,
			Watermark: "TEST · 실제 구매 증빙이 아님",
			LegalSale: false,
		}, nil
	default:
		return receiptClassification{}, agencydomain.ErrInvalid
	}
}

func (s *LifecycleService) issueReceipts(ctx context.Context, ids []string) error {
	for _, agencyOrderID := range ids {
		now := s.clock.Now()
		projection, err := s.repository.GetProjectionForReceipt(ctx, agencyOrderID)
		if err != nil {
			return err
		}
		refunded := projection.Process.TerminalReason == agencydomain.TerminalReasonRefundedAll
		if projection.Payment == nil ||
			projection.Process.State != agencydomain.ProcessTerminal ||
			(!refunded && !agencydomain.CompletedTerminalReason(projection.Process.TerminalReason)) {
			// Effect가 왔는데 종결 근거가 아직 안 보인다 — 재시도 가능(리듀서
			// reopen과 Effect 사이의 경합 창).
			return fmt.Errorf("receipt not ready for %s: process not terminal", agencyOrderID)
		}
		if projection.Payment.Rail != projection.AgencyOrder.ExecutionProfile.PaymentRail {
			return fmt.Errorf("receipt payment rail mismatch for %s: %w", agencyOrderID, agencydomain.ErrInvalid)
		}
		isPayPal := projection.Payment.Rail == "PAYPAL"
		transactions := []agencydomain.ChainTransaction{}
		if !isPayPal {
			transactions, err = s.repository.ListFinalizedTransactions(ctx, projection.Payment.ID)
			if err != nil {
				return err
			}
		}
		// terminal 참조: GIWA는 chain tx hash, PayPal은 Capture/Refund ID가
		// PayPal형 compact 증거다(ADR-0050).
		terminalHash := projection.Payment.CompleteTxHash
		if isPayPal {
			terminalHash = projection.Payment.CaptureID
		}
		if refunded {
			terminalHash = projection.Payment.RefundTxHash
			if isPayPal {
				terminalHash = projection.Payment.PayPalRefundID
			}
		}
		if terminalHash == "" {
			// 종결 참조가 아직 관찰되지 않았다(환불 id·complete tx 지연) —
			// backoff 재시도가 이어받는다.
			return fmt.Errorf("receipt not ready for %s: terminal reference missing", agencyOrderID)
		}
		classification, err := classifyReceipt(
			projection.AgencyOrder, projection.MerchantOrders,
		)
		if err != nil {
			return fmt.Errorf("receipt execution profile mismatch for %s: %w", agencyOrderID, err)
		}
		payload := struct {
			SchemaVersion        string                              `json:"schemaVersion"`
			Kind                 string                              `json:"kind"`
			Watermark            string                              `json:"watermark"`
			LegalSale            bool                                `json:"legalSale"`
			PaymentRail          string                              `json:"paymentRail"`
			ExecutionProfile     agencydomain.OrderExecutionProfile  `json:"executionProfile"`
			ExecutionProfileHash string                              `json:"executionProfileHash"`
			AgencyOrder          agencydomain.AgencyOrder            `json:"agencyOrder"`
			Payment              agencydomain.PaymentProjection      `json:"payment"`
			MerchantOrders       []agencydomain.MerchantOrderSummary `json:"merchantOrders"`
			Transactions         []agencydomain.ChainTransaction     `json:"transactions"`
			GeneratedAt          time.Time                           `json:"generatedAt"`
		}{
			SchemaVersion: "vitlane.agency-order-receipt.v4", Kind: classification.Kind,
			Watermark: classification.Watermark, LegalSale: classification.LegalSale,
			PaymentRail:          projection.Payment.Rail,
			ExecutionProfile:     projection.AgencyOrder.ExecutionProfile,
			ExecutionProfileHash: projection.AgencyOrder.ExecutionProfileHash,
			AgencyOrder:          projection.AgencyOrder, Payment: *projection.Payment,
			MerchantOrders: projection.MerchantOrders, Transactions: transactions,
			GeneratedAt: now,
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		receiptHash, err := shareddomain.CanonicalJSONHashBytes(payloadJSON)
		if err != nil {
			return err
		}
		receipt := agencydomain.Receipt{
			ID: s.ids.NewID(), AgencyOrderID: agencyOrderID,
			Kind:                  classification.Kind,
			PaymentRail:           projection.AgencyOrder.ExecutionProfile.PaymentRail,
			ProviderEnvironment:   projection.AgencyOrder.ExecutionProfile.ProviderEnvironment,
			Asset:                 projection.AgencyOrder.ExecutionProfile.Asset,
			EconomicEffect:        projection.AgencyOrder.ExecutionProfile.EconomicEffect,
			MerchantExecutionMode: projection.AgencyOrder.ExecutionProfile.MerchantExecutionMode,
			ExecutionProfileHash:  projection.AgencyOrder.ExecutionProfileHash,
			LegalSale:             classification.LegalSale,
			TerminalState:         string(projection.Process.TerminalReason), TerminalTxHash: terminalHash,
			ReceiptHash: receiptHash, Payload: payloadJSON, CreatedAt: now,
		}
		if isPayPal {
			receipt.CustomerPaymentID = projection.Payment.ID
		} else {
			receipt.SettlementPaymentID = projection.Payment.ID
			if !receipt.HasFinalizedTerminalTransaction() {
				return fmt.Errorf("receipt not ready for %s: terminal transaction not finalized", agencyOrderID)
			}
		}
		if err := s.repository.CreateReceipt(ctx, receipt); err != nil {
			return err
		}
	}
	return nil
}

// RequestRefund receives one post-effect whole-MO request. The repository
// resolves and locks its immutable allocation gross in the ownership check.
func (s *LifecycleService) RequestRefund(
	ctx context.Context,
	userID, agencyOrderID, merchantOrderID string,
	reasonCode, publicRationale string,
	authority RefundAuthority,
) (agencydomain.RefundRequest, error) {
	merchantOrderID = strings.TrimSpace(merchantOrderID)
	if merchantOrderID == "" {
		return agencydomain.RefundRequest{}, agencydomain.ErrRefundRequestInvalid
	}
	reasonCode = strings.ToUpper(strings.TrimSpace(reasonCode))
	if err := agencydomain.ValidateRefundReasonCode(reasonCode); err != nil {
		return agencydomain.RefundRequest{}, err
	}
	publicRationale = strings.TrimSpace(publicRationale)
	if err := agencydomain.ValidateRefundRequestReason(publicRationale); err != nil {
		return agencydomain.RefundRequest{}, err
	}
	request := agencydomain.RefundRequest{
		ID:              s.ids.NewID(),
		AgencyOrderID:   strings.TrimSpace(agencyOrderID),
		MerchantOrderID: merchantOrderID,
		UserID:          strings.TrimSpace(userID),
		ReasonCode:      reasonCode,
		PublicRationale: publicRationale,
	}
	// REFUND_REQUEST 카드는 리듀서가 customer.refund_requested 이벤트에서 발행한다.
	return s.repository.CreateRefundRequest(ctx, request, authority, s.clock.Now())
}

// ListRefundQueue는 운영자 심사 큐(미종결 요청)다.
func (s *LifecycleService) ListRefundQueue(
	ctx context.Context,
	limit int,
) ([]agencydomain.RefundRequest, error) {
	return s.repository.ListRefundRequests(ctx, "", "", true, limit)
}

// ListResolvedRefundQueue는 종결 심사의 열람이다(ADR-0057 — 운영자 "처리
// 완료" 탭, 행동 없음).
func (s *LifecycleService) ListResolvedRefundQueue(
	ctx context.Context,
	limit int,
) ([]agencydomain.RefundRequest, error) {
	return s.repository.ListResolvedRefundRequests(ctx, limit)
}

// CountOpenRefundQueue는 심사 대기 요청의 전역 카운트다(ADR-0057 2차 P2).
func (s *LifecycleService) CountOpenRefundQueue(ctx context.Context) (int, error) {
	return s.repository.CountOpenRefundRequests(ctx)
}

// DecideRefund records one approve/reject outcome for the whole MO request.
func (s *LifecycleService) DecideRefund(
	ctx context.Context,
	requestID, actorUserID string,
	decision RefundDecision,
) (agencydomain.RefundRequest, error) {
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(actorUserID) == "" {
		return agencydomain.RefundRequest{}, agencydomain.ErrRefundRequestInvalid
	}
	decision.PublicRationale = strings.TrimSpace(decision.PublicRationale)
	if err := agencydomain.ValidateRefundDecisionPublicRationale(decision.PublicRationale); err != nil {
		return agencydomain.RefundRequest{}, err
	}
	decision.InternalNote = strings.TrimSpace(decision.InternalNote)
	if err := agencydomain.ValidateRefundInternalNote(decision.InternalNote); err != nil {
		return agencydomain.RefundRequest{}, err
	}
	// REFUND_DECISION 카드는 리듀서가 refund_review.decided 이벤트에서 발행한다.
	return s.repository.DecideRefundRequest(
		ctx, strings.TrimSpace(requestID), strings.TrimSpace(actorUserID),
		decision, s.clock.Now(),
	)
}
