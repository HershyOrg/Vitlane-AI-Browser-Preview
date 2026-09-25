// Package app은 Payment Bounded Context의 orchestration을 소유한다.
// 결제수단은 결제 단계의 어댑터이며(ADR-0050) 수납 성공 이후의 주문 처리는
// 기존 AgencyOrder lifecycle이 이어받는다. redirect/webhook은 wake-up 신호일 뿐
// 성공 권위가 아니고, 모든 판정은 provider GET 재조회로 한다.
package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/infra/paypal"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// ProviderClient는 PayPal REST adapter port다. write는 호출자가 저장한
// PayPal-Request-Id를 사용하고 결과 불명은 paypal.ErrOutcomeUnknown이다.
type ProviderClient interface {
	CreateOrder(context.Context, paypal.CreateOrderInput) (paypal.Order, error)
	GetOrder(context.Context, string) (paypal.Order, error)
	GetCapture(context.Context, string) (paypal.Capture, error)
	RefundCapture(context.Context, string, string, int64) (paypal.Refund, error)
	GetRefund(context.Context, string) (paypal.Refund, error)
	VerifyWebhookSignature(context.Context, paypal.WebhookVerification) (bool, error)
}

// authorizationProviderClient is the AUTHORIZE + per-MO capture capability.
// It is separate from the historical port so non-PayPal test adapters cannot
// accidentally become eligible for money writes merely by compiling.
type authorizationProviderClient interface {
	GetOrder(context.Context, string) (paypal.Order, error)
	AuthorizeOrder(context.Context, string, string) (paypal.Order, error)
	GetAuthorizedOrder(context.Context, string) (paypal.Order, error)
	GetAuthorization(context.Context, string) (paypal.Authorization, error)
	CaptureAuthorization(context.Context, paypal.CaptureAuthorizationInput) (paypal.Capture, error)
	VoidAuthorization(context.Context, string, string) error
	ReauthorizeAuthorization(
		context.Context, paypal.ReauthorizeAuthorizationInput,
	) (paypal.Authorization, error)
}

// InstructionGate reads Payment's own durable confirmation and requests one when
// absent. It runs outside Owner transactions and never calls AgencyOrder apps.
type InstructionGate interface {
	ConfirmInstruction(context.Context, procmsg.InstructionClaim) error
}

type ReconcileItem struct {
	Payment domain.CustomerPayment
	Attempt domain.PayPalAttempt
}

// PaymentReconciliationItem is the operator-safe projection for a payment
// whose PayPal outcome must be reconciled before Procurement can start. It
// intentionally excludes payer data and provider payloads.
type PaymentReconciliationItem struct {
	PaymentID           string              `json:"paymentId"`
	AgencyOrderID       string              `json:"agencyOrderId"`
	PayPalAttemptID     string              `json:"paypalAttemptId"`
	PayPalOrderID       string              `json:"paypalOrderId,omitempty"`
	ProviderEnvironment string              `json:"providerEnvironment"`
	AmountMinor         int64               `json:"amountMinor"`
	Currency            string              `json:"currency"`
	PaymentState        domain.PaymentState `json:"paymentState"`
	AttemptState        domain.AttemptState `json:"attemptState"`
	ReasonCode          string              `json:"reasonCode"`
	UpdatedAt           time.Time           `json:"updatedAt"`
}

type Repository interface {
	GetPayableInstruction(ctx context.Context, userID, agencyOrderID string) (domain.PayableInstruction, error)
	GetOpenPayment(ctx context.Context, userID, agencyOrderID string) (domain.CustomerPayment, domain.PayPalAttempt, bool, error)
	CreatePaymentWithAttempt(ctx context.Context, payment domain.CustomerPayment, attempt domain.PayPalAttempt, operation domain.ExternalOperation) error
	GetAttemptByNonce(ctx context.Context, nonce string) (domain.CustomerPayment, domain.PayPalAttempt, error)
	FindByPayPalOrder(ctx context.Context, paypalOrderID string) (domain.CustomerPayment, domain.PayPalAttempt, bool, error)
	// MarkOperationSent는 immutable provider request identity
	// (purpose/owner/idempotency key/request hash)를 기준으로 SENT로 전이한다.
	// 호출자가 serial UNKNOWN retry에서 새 local operation ID를 생성해도 같은
	// provider request를 가리키며, first_sent_at은 최초값을 보존한다.
	MarkOperationSent(ctx context.Context, operation domain.ExternalOperation, attemptID string, attemptState domain.AttemptState, paymentID string, paymentState domain.PaymentState, firstSentAt, idempotencyDeadline time.Time) error
	// RecordOrderCreated는 attempt의 create operation을 owner 기준으로 resolve하고
	// PayPal Order identity와 승인 URL을 고정한다.
	RecordOrderCreated(ctx context.Context, attemptID, paymentID, paypalOrderID, approvalURL string, now time.Time) error
	// RecordAttemptOutcome은 (선택적으로 purpose의 owner operation), attempt,
	// payment를 한 transaction에서 전이한다. 이미 terminal인 row는 덮지 않는다.
	RecordAttemptOutcome(ctx context.Context, attemptID string, attemptState domain.AttemptState, paymentID string, paymentState domain.PaymentState, purpose domain.OperationPurpose, operationState domain.OperationState, reason string, now time.Time) error
	MarkPayerApproved(ctx context.Context, attemptID, paymentID string, now time.Time) error
	ListReconcileDue(ctx context.Context, limit int, now time.Time) ([]ReconcileItem, error)
	ListPaymentReconciliations(ctx context.Context, limit int) ([]PaymentReconciliationItem, error)
	CountPaymentReconciliations(ctx context.Context) (int, error)
	GetBinding(ctx context.Context, environment string) (domain.AccountBinding, bool, error)
	UpsertBinding(ctx context.Context, binding domain.AccountBinding, now time.Time) error
	InsertWebhookEvent(ctx context.Context, event domain.WebhookEvent) (bool, error)
	MarkWebhookProcessed(ctx context.Context, environment, webhookID, eventID string, now time.Time) error
	ProcessDisputeWebhook(ctx context.Context, event domain.WebhookEvent, observation domain.DisputeObservation, caseID string) (domain.PayPalDisputeCase, bool, error)
	ListPayPalDisputes(ctx context.Context, filter DisputeQueueFilter) ([]domain.PayPalDisputeCase, error)
	GetPayPalDispute(ctx context.Context, environment, caseID string) (domain.PayPalDisputeCase, error)
	ListPayPalDisputeActions(ctx context.Context, environment, caseID string) ([]domain.PayPalDisputeManualAction, error)
	RecordPayPalDisputeAction(ctx context.Context, environment string, action domain.PayPalDisputeManualAction, expectedVersion int64) (domain.PayPalDisputeCase, domain.PayPalDisputeManualAction, bool, error)
}

type authorizationRepository interface {
	PrepareAuthorizationOperation(
		context.Context, string, domain.ExternalOperation, time.Time,
	) (domain.ExternalOperation, bool, error)
	RecordAuthorizationCompleted(
		context.Context, string, string, domain.PayPalAuthorization, time.Time,
	) (bool, error)
}

type Config struct {
	// Environment is retained only by the single-provider test constructor.
	// Registry-backed production initiation is selected by the immutable
	// PaymentInstruction environment, never a global new-order selector.
	Environment   string
	WebhookID     string
	PublicBaseURL string
	// 제안값 — 확정치는 payment design.md가 소유한다.
	CreateTimeout        time.Duration
	CaptureTimeout       time.Duration
	QueryTimeout         time.Duration
	CreateRetryWindow    time.Duration
	AuthorizeRetryWindow time.Duration
	CaptureRetryWindow   time.Duration
	RefundRetryWindow    time.Duration
	ReconcileBatch       int
}

func (c Config) normalized() Config {
	c.Environment = strings.ToUpper(strings.TrimSpace(c.Environment))
	if c.CreateTimeout <= 0 {
		c.CreateTimeout = 15 * time.Second
	}
	if c.CaptureTimeout <= 0 {
		c.CaptureTimeout = 30 * time.Second
	}
	if c.QueryTimeout <= 0 {
		c.QueryTimeout = 10 * time.Second
	}
	if c.CreateRetryWindow <= 0 {
		c.CreateRetryWindow = 6 * time.Hour
	}
	if c.AuthorizeRetryWindow <= 0 {
		c.AuthorizeRetryWindow = 6 * time.Hour
	}
	if c.CaptureRetryWindow <= 0 {
		c.CaptureRetryWindow = 72 * time.Hour
	}
	if c.RefundRetryWindow <= 0 {
		c.RefundRetryWindow = 42 * 24 * time.Hour
	}
	if c.ReconcileBatch <= 0 {
		c.ReconcileBatch = 25
	}
	return c
}

type Service struct {
	repository    Repository
	providers     ProviderRegistry
	instructions  InstructionGate
	transactor    sharedapp.Transactor
	config        Config
	clock         sharedapp.Clock
	ids           sharedapp.IDGenerator
	liveMoneyGate LiveMoneyGate
}

type LiveMoneyGate interface {
	AllowLiveMoneyEffect(context.Context) (bool, error)
}

func (s *Service) EnableLiveMoneyGate(gate LiveMoneyGate) { s.liveMoneyGate = gate }

func (s *Service) requireLiveMoney(ctx context.Context, environment string) error {
	if !strings.EqualFold(environment, "LIVE") {
		return nil
	}
	if s.liveMoneyGate == nil {
		return domain.ErrRailUnavailable
	}
	allowed, err := s.liveMoneyGate.AllowLiveMoneyEffect(ctx)
	if err != nil || !allowed {
		return domain.ErrRailUnavailable
	}
	return nil
}

func NewService(
	repository Repository,
	provider ProviderClient,
	instructions InstructionGate,
	transactor sharedapp.Transactor,
	config Config,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
) *Service {
	registry, _ := NewProviderRegistry(ProviderRegistration{
		Environment: config.Environment, Client: provider, WebhookID: config.WebhookID,
		IssueEnabled: true, CaptureEnabled: true,
	})
	return NewServiceWithProviderRegistry(
		repository, registry, instructions, transactor, config, clock, ids,
	)
}

func NewServiceWithProviderRegistry(
	repository Repository,
	providers ProviderRegistry,
	instructions InstructionGate,
	transactor sharedapp.Transactor,
	config Config,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
) *Service {
	return &Service{
		repository: repository, providers: providers, instructions: instructions,
		transactor: transactor,
		config:     config.normalized(), clock: clock, ids: ids,
	}
}

func (s *Service) providerFor(environment string) (ProviderRegistration, error) {
	registration, ok := s.providers.Get(environment)
	if !ok {
		return ProviderRegistration{}, domain.ErrRailUnavailable
	}
	return registration, nil
}

// paymentInitiationProvider returns the one provider registration currently
// authorized to create a new PayPal payer resource. Historical provider
// resources are reconciled from their stored environment without this check;
// only the transition from no provider effect to CreateOrder is selector- and
// capture-gated.
func (s *Service) paymentInitiationProvider(ctx context.Context, environment string) (ProviderRegistration, error) {
	registration, err := s.providerFor(environment)
	if err != nil || !registration.IssueEnabled || !registration.CaptureEnabled {
		return ProviderRegistration{}, domain.ErrRailUnavailable
	}
	if err := s.requireLiveMoney(ctx, environment); err != nil {
		return ProviderRegistration{}, err
	}
	return registration, nil
}

type CheckoutView struct {
	Payment     domain.CustomerPayment `json:"payment"`
	Attempt     domain.PayPalAttempt   `json:"attempt"`
	ApprovalURL string                 `json:"approvalUrl,omitempty"`
	ReturnNonce string                 `json:"returnNonce,omitempty"`
}

// StartCheckout은 저장된 PAYPAL instruction이 현재 신규-order selector와 정확히
// 같고 그 environment의 issue/capture gate가 모두 열렸을 때만 새 PayPal Order와
// payer 승인 URL을 만든다. 이미 provider write가 시작된 결제는 저장된
// environment에서 대사한다(멱등).
func (s *Service) StartCheckout(
	ctx context.Context,
	userID, agencyOrderID string,
) (CheckoutView, error) {
	userID = strings.TrimSpace(userID)
	agencyOrderID = strings.TrimSpace(agencyOrderID)
	if userID == "" || agencyOrderID == "" {
		return CheckoutView{}, domain.ErrInvalid
	}
	now := s.clock.Now()
	instruction, err := s.repository.GetPayableInstruction(ctx, userID, agencyOrderID)
	if err != nil {
		return CheckoutView{}, err
	}
	registration, err := s.providerFor(instruction.ProviderEnvironment)
	if err != nil {
		return CheckoutView{}, domain.ErrRailUnavailable
	}
	if err := instruction.ValidateForPayPal(registration.Environment, now); err != nil {
		return CheckoutView{}, err
	}
	binding, found, err := s.repository.GetBinding(ctx, instruction.ProviderEnvironment)
	if err != nil {
		return CheckoutView{}, err
	}
	if !found {
		return CheckoutView{}, domain.ErrRailUnavailable
	}

	payment, attempt, found, err := s.repository.GetOpenPayment(ctx, userID, agencyOrderID)
	if err != nil {
		return CheckoutView{}, err
	}
	if found {
		return s.progressOpenPayment(ctx, binding, instruction, payment, attempt)
	}
	if _, err := s.paymentInitiationProvider(ctx, instruction.ProviderEnvironment); err != nil {
		return CheckoutView{}, err
	}

	nonce, err := newNonce()
	if err != nil {
		return CheckoutView{}, err
	}
	payment = domain.CustomerPayment{
		ID: s.ids.NewID(), AgencyOrderID: agencyOrderID, UserID: userID,
		Rail: instruction.Rail, ProviderEnvironment: instruction.ProviderEnvironment,
		Asset: instruction.Asset, EconomicEffect: instruction.EconomicEffect,
		MerchantExecutionMode: instruction.MerchantExecutionMode,
		ExecutionProfileHash:  instruction.ExecutionProfileHash,
		AmountMinor:           instruction.CustomerPayableMinor, Currency: "USD",
		State: domain.PaymentCreated, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	attempt = domain.PayPalAttempt{
		ID: s.ids.NewID(), CustomerPaymentID: payment.ID, Sequence: 1,
		State: domain.AttemptOrderPrepared, ReturnNonce: nonce,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	operation := s.newOperation(domain.OperationPayPalOrderCreate, "PAYPAL_ATTEMPT",
		attempt.ID, fmt.Sprintf("paypal:create:%s", attempt.ID), payment.AmountMinor)
	if err := s.repository.CreatePaymentWithAttempt(ctx, payment, attempt, operation); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			// 경쟁 생성 — 열린 결제를 다시 읽어 진행한다.
			payment, attempt, found, err = s.repository.GetOpenPayment(ctx, userID, agencyOrderID)
			if err != nil || !found {
				return CheckoutView{}, domain.ErrConflict
			}
			return s.progressOpenPayment(ctx, binding, instruction, payment, attempt)
		}
		return CheckoutView{}, err
	}
	return s.submitOrderCreate(ctx, payment, attempt, operation)
}

// progressOpenPayment는 열린 결제의 현재 단계에 맞는 다음 행동을 수행한다.
func (s *Service) progressOpenPayment(
	ctx context.Context,
	binding domain.AccountBinding,
	instruction domain.PayableInstruction,
	payment domain.CustomerPayment,
	attempt domain.PayPalAttempt,
) (CheckoutView, error) {
	if !payment.MatchesInstruction(instruction) ||
		!strings.EqualFold(binding.Environment, payment.ProviderEnvironment) {
		return CheckoutView{}, domain.ErrInstructionMismatch
	}
	switch attempt.State {
	case domain.AttemptOrderPrepared:
		// PREPARED는 아직 provider effect가 아니다. 배포 사이 gate/selector가
		// 닫혔다면 새 CreateOrder를 시작하지 않는다.
		if _, err := s.paymentInitiationProvider(ctx, payment.ProviderEnvironment); err != nil {
			return CheckoutView{}, err
		}
		operation := s.newOperation(domain.OperationPayPalOrderCreate, "PAYPAL_ATTEMPT",
			attempt.ID, fmt.Sprintf("paypal:create:%s", attempt.ID), payment.AmountMinor)
		return s.submitOrderCreate(ctx, payment, attempt, operation)
	case domain.AttemptOrderCreateSubmitted, domain.AttemptOrderCreateUnknown:
		return s.reconcileCreate(ctx, payment, attempt)
	case domain.AttemptPayerActionRequired, domain.AttemptPayerApproved,
		domain.AttemptCancelledByUser:
		// CANCELLED_BY_USER 뒤에도 PayPal Order가 유효하면 같은 승인 URL로 재개한다.
		return s.ensureAuthorized(ctx, binding, instruction, payment, attempt)
	case domain.AttemptAuthorizeSubmitted:
		// GET the approved Order first. If the sender crashed after its durable
		// claim but before POST, PrepareAuthorizationOperation reclaims the stale
		// SENT operation and reuses the exact same PayPal-Request-Id.
		return s.ensureAuthorized(ctx, binding, instruction, payment, attempt)
	case domain.AttemptAuthorizePending:
		return s.reconcileAuthorization(ctx, binding, payment, attempt)
	case domain.AttemptAuthorizeOutcomeUnknown:
		// Re-read the approved Order first; if no authorization exists, the
		// authorization path reclaims UNKNOWN and retries the same provider key.
		return s.ensureAuthorized(ctx, binding, instruction, payment, attempt)
	case domain.AttemptAuthorizeCompleted:
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	default:
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	}
}

func (s *Service) submitOrderCreate(
	ctx context.Context,
	payment domain.CustomerPayment,
	attempt domain.PayPalAttempt,
	operation domain.ExternalOperation,
) (CheckoutView, error) {
	if err := payment.ValidatePayPalExecutionProfile(); err != nil {
		return CheckoutView{}, err
	}
	registration, err := s.providerFor(payment.ProviderEnvironment)
	if err != nil {
		return CheckoutView{}, domain.ErrRailUnavailable
	}
	if err := s.requireLiveMoney(ctx, payment.ProviderEnvironment); err != nil {
		return CheckoutView{}, err
	}
	now := s.clock.Now()
	if err := s.repository.MarkOperationSent(ctx, operation, attempt.ID,
		domain.AttemptOrderCreateSubmitted, payment.ID, domain.PaymentProcessing,
		now, now.Add(s.config.CreateRetryWindow)); err != nil {
		if errors.Is(err, domain.ErrIdempotencyExpired) {
			if recordErr := s.repository.RecordAttemptOutcome(
				ctx, attempt.ID, domain.AttemptAbandonedBeforeAuthorize,
				payment.ID, domain.PaymentAbandoned,
				domain.OperationPayPalOrderCreate, domain.OperationCancelled,
				"ORDER_CREATE_IDEMPOTENCY_EXPIRED_NO_RESOURCE", now,
			); recordErr != nil {
				return CheckoutView{}, recordErr
			}
			payment.State = domain.PaymentAbandoned
			attempt.State = domain.AttemptAbandonedBeforeAuthorize
			return CheckoutView{Payment: payment, Attempt: attempt}, nil
		}
		if errors.Is(err, domain.ErrConflict) {
			// Another request owns the only provider send. The PayPal Order ID is
			// not known until that sender commits, so this caller must not retry
			// the write concurrently with the same PayPal-Request-Id.
			payment.State = domain.PaymentProcessing
			attempt.State = domain.AttemptOrderCreateSubmitted
			return CheckoutView{Payment: payment, Attempt: attempt,
				ReturnNonce: attempt.ReturnNonce}, nil
		}
		return CheckoutView{}, err
	}
	// SPA 라우트는 camelCase(/agencyOrder/{id}/payment)다 — 실 Sandbox 복귀
	// conformance가 kebab-case 오기를 404로 잡아냈다.
	returnBase := strings.TrimRight(s.config.PublicBaseURL, "/") +
		"/agencyOrder/" + payment.AgencyOrderID + "/payment"
	callCtx, cancel := context.WithTimeout(ctx, s.config.CreateTimeout)
	defer cancel()
	order, err := registration.Client.CreateOrder(callCtx, paypal.CreateOrderInput{
		RequestID:   operation.IdempotencyKey,
		ReferenceID: payment.AgencyOrderID,
		CustomID:    payment.ID,
		AmountMinor: payment.AmountMinor,
		ReturnURL:   returnBase + "?paypal=return&nonce=" + attempt.ReturnNonce,
		CancelURL:   returnBase + "?paypal=cancel&nonce=" + attempt.ReturnNonce,
	})
	now = s.clock.Now()
	if err != nil {
		if errors.Is(err, paypal.ErrOutcomeUnknown) ||
			errors.Is(err, paypal.ErrNonconformingResponse) {
			reason := "ORDER_CREATE_UNKNOWN"
			if errors.Is(err, paypal.ErrNonconformingResponse) {
				reason = "ORDER_CREATE_RESPONSE_NONCONFORMING"
			}
			if recordErr := s.repository.RecordAttemptOutcome(ctx, attempt.ID,
				domain.AttemptOrderCreateUnknown, payment.ID, domain.PaymentProcessing,
				domain.OperationPayPalOrderCreate, domain.OperationUnknown,
				reason, now); recordErr != nil {
				return CheckoutView{}, recordErr
			}
			payment.State = domain.PaymentProcessing
			attempt.State = domain.AttemptOrderCreateUnknown
			return CheckoutView{Payment: payment, Attempt: attempt}, nil
		}
		if recordErr := s.repository.RecordAttemptOutcome(ctx, attempt.ID,
			domain.AttemptOrderCreateFailed, payment.ID, domain.PaymentFailed,
			domain.OperationPayPalOrderCreate, domain.OperationFailed,
			providerReason(err), now); recordErr != nil {
			return CheckoutView{}, recordErr
		}
		return CheckoutView{}, err
	}
	if err := s.repository.RecordOrderCreated(ctx, attempt.ID, payment.ID,
		order.ID, order.PayerActionURL, now); err != nil {
		return CheckoutView{}, err
	}
	payment.State = domain.PaymentActionRequired
	attempt.State = domain.AttemptPayerActionRequired
	attempt.PayPalOrderID = order.ID
	attempt.ApprovalURL = order.PayerActionURL
	return CheckoutView{Payment: payment, Attempt: attempt,
		ApprovalURL: order.PayerActionURL, ReturnNonce: attempt.ReturnNonce}, nil
}

// reconcileCreate는 create 결과 불명을 GET으로 닫는다. Order ID를 모르는 불명은
// 같은 key 재시도(sweeper의 progressOpenPayment 경유)로만 닫는다.
func (s *Service) reconcileCreate(
	ctx context.Context,
	payment domain.CustomerPayment,
	attempt domain.PayPalAttempt,
) (CheckoutView, error) {
	if err := payment.ValidatePayPalExecutionProfile(); err != nil {
		return CheckoutView{}, err
	}
	if attempt.PayPalOrderID == "" {
		operation := s.newOperation(domain.OperationPayPalOrderCreate, "PAYPAL_ATTEMPT",
			attempt.ID, fmt.Sprintf("paypal:create:%s", attempt.ID), payment.AmountMinor)
		return s.submitOrderCreate(ctx, payment, attempt, operation)
	}
	registration, err := s.providerFor(payment.ProviderEnvironment)
	if err != nil {
		return CheckoutView{}, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.config.QueryTimeout)
	defer cancel()
	order, err := registration.Client.GetOrder(queryCtx, attempt.PayPalOrderID)
	if err != nil {
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	}
	now := s.clock.Now()
	if err := s.repository.RecordOrderCreated(ctx, attempt.ID, payment.ID,
		order.ID, order.PayerActionURL, now); err != nil {
		return CheckoutView{}, err
	}
	payment.State = domain.PaymentActionRequired
	attempt.State = domain.AttemptPayerActionRequired
	return CheckoutView{Payment: payment, Attempt: attempt,
		ApprovalURL: order.PayerActionURL, ReturnNonce: attempt.ReturnNonce}, nil
}

type ResumeInput struct {
	UserID        string
	AgencyOrderID string
	ReturnNonce   string
	Cancelled     bool
}

// Resume은 PayPal return/cancel redirect 뒤 SPA가 호출하는 wake-up이다.
// redirect만으로 성공을 표시하지 않고 provider GET으로 확정한다.
func (s *Service) Resume(ctx context.Context, input ResumeInput) (CheckoutView, error) {
	payment, attempt, err := s.repository.GetAttemptByNonce(ctx, strings.TrimSpace(input.ReturnNonce))
	if err != nil {
		return CheckoutView{}, err
	}
	if payment.UserID != strings.TrimSpace(input.UserID) ||
		payment.AgencyOrderID != strings.TrimSpace(input.AgencyOrderID) {
		return CheckoutView{}, domain.ErrNotFound
	}
	now := s.clock.Now()
	if input.Cancelled {
		if attempt.State == domain.AttemptPayerActionRequired {
			if err := s.repository.RecordAttemptOutcome(ctx, attempt.ID,
				domain.AttemptCancelledByUser, payment.ID, domain.PaymentActionRequired,
				"", "", "PAYER_CANCELLED", now); err != nil {
				return CheckoutView{}, err
			}
			payment.State = domain.PaymentActionRequired
			attempt.State = domain.AttemptCancelledByUser
		}
		return CheckoutView{Payment: payment, Attempt: attempt,
			ApprovalURL: attempt.ApprovalURL, ReturnNonce: attempt.ReturnNonce}, nil
	}
	binding, found, err := s.repository.GetBinding(ctx, payment.ProviderEnvironment)
	if err != nil {
		return CheckoutView{}, err
	}
	if !found {
		return CheckoutView{}, domain.ErrRailUnavailable
	}
	instruction, err := s.repository.GetPayableInstruction(ctx, payment.UserID, payment.AgencyOrderID)
	if err != nil {
		return CheckoutView{}, err
	}
	if !payment.MatchesInstruction(instruction) {
		return CheckoutView{}, domain.ErrInstructionMismatch
	}
	return s.ensureAuthorized(ctx, binding, instruction, payment, attempt)
}

// GetView는 결제 화면 polling용 현재 상태다(외부 호출 없음).
func (s *Service) GetView(
	ctx context.Context,
	userID, agencyOrderID string,
) (CheckoutView, bool, error) {
	payment, attempt, found, err := s.repository.GetOpenPayment(ctx, userID, agencyOrderID)
	if err != nil || !found {
		return CheckoutView{}, false, err
	}
	return CheckoutView{Payment: payment, Attempt: attempt,
		ApprovalURL: attempt.ApprovalURL, ReturnNonce: attempt.ReturnNonce}, true, nil
}

// WebhookInput은 서명 검증에 필요한 원문 header/body와 파싱된 safe field다.
type WebhookInput struct {
	ProviderEnvironment   string
	AuthAlgo              string
	CertURL               string
	TransmissionID        string
	TransmissionSig       string
	TransmissionTime      string
	EventID               string
	EventType             string
	ResourceKind          string
	ResourceID            string
	RelatedOrderID        string
	DisputeID             string
	DisputedCaptureID     string
	DisputeStatus         string
	DisputeOutcome        string
	DisputeReason         string
	DisputeLifecycleStage string
	SellerResponseDueAt   *time.Time
	ResourceOccurredAt    *time.Time
	RawBody               []byte
}

// HandleWebhook은 서명 검증 -> inbox dedupe -> 관련 리소스 GET 대사를 수행한다.
// event 순서를 가정하지 않고 payload를 상태 권위로 쓰지 않는다.
func (s *Service) HandleWebhook(ctx context.Context, input WebhookInput) error {
	registration, err := s.providerFor(input.ProviderEnvironment)
	if err != nil {
		return err
	}
	verified, err := registration.Client.VerifyWebhookSignature(ctx, paypal.WebhookVerification{
		AuthAlgo: input.AuthAlgo, CertURL: input.CertURL,
		TransmissionID: input.TransmissionID, TransmissionSig: input.TransmissionSig,
		TransmissionTime: input.TransmissionTime, WebhookID: registration.WebhookID,
		RawEvent: input.RawBody,
	})
	if err != nil {
		// 검증자 일시 장애는 2xx로 삼키지 않아 provider retry를 허용한다.
		return fmt.Errorf("webhook signature verification unavailable: %w", err)
	}
	if !verified {
		return domain.ErrInvalid
	}
	now := s.clock.Now()
	if domain.IsPayPalDisputeEvent(input.EventType) {
		observedAt := now
		if input.ResourceOccurredAt != nil && !input.ResourceOccurredAt.IsZero() {
			observedAt = input.ResourceOccurredAt.UTC()
		}
		disputeID := strings.TrimSpace(input.DisputeID)
		if disputeID == "" {
			disputeID = strings.TrimSpace(input.ResourceID)
		}
		observation, observationErr := domain.NewDisputeObservation(
			registration.Environment, input.EventID, input.EventType,
			disputeID, input.DisputedCaptureID, input.DisputeStatus,
			input.DisputeOutcome, input.DisputeReason, input.DisputeLifecycleStage,
			input.SellerResponseDueAt, observedAt,
		)
		if observationErr != nil {
			return observationErr
		}
		_, _, processErr := s.repository.ProcessDisputeWebhook(
			ctx,
			domain.WebhookEvent{
				Environment: registration.Environment, WebhookID: registration.WebhookID,
				EventID: observation.EventID, EventType: observation.EventType,
				TransmissionID: input.TransmissionID, ResourceKind: "PAYPAL_DISPUTE",
				ResourceID: disputeID, ReceivedAt: now,
			},
			observation, s.ids.NewID(),
		)
		// PAYPAL_DISPUTE 카드는 리듀서가 dispute.state_changed 이벤트에서 발행한다
		// (ADR-0070 §4.5).
		return processErr
	}
	var relatedPayment domain.CustomerPayment
	var relatedAttempt domain.PayPalAttempt
	var relatedFound bool
	if orderID := strings.TrimSpace(input.RelatedOrderID); orderID != "" {
		payment, attempt, found, findErr := s.repository.FindByPayPalOrder(ctx, orderID)
		if findErr == nil && found {
			if !strings.EqualFold(payment.ProviderEnvironment, registration.Environment) {
				return domain.ErrInstructionMismatch
			}
			relatedPayment, relatedAttempt, relatedFound = payment, attempt, true
		}
	}
	inserted, err := s.repository.InsertWebhookEvent(ctx, domain.WebhookEvent{
		Environment: registration.Environment, WebhookID: registration.WebhookID,
		EventID: input.EventID, EventType: input.EventType,
		TransmissionID: input.TransmissionID, ResourceKind: input.ResourceKind,
		ResourceID: input.ResourceID, ReceivedAt: now,
	})
	if err != nil {
		return err
	}
	if !inserted {
		return nil // duplicate delivery — safe no-op
	}
	// wake-up: 관련 주문을 GET 재조회로 대사한다. 실패해도 sweeper가 재시도한다.
	if relatedFound {
		if binding, ok, bindErr := s.repository.GetBinding(
			ctx, relatedPayment.ProviderEnvironment,
		); bindErr == nil && ok {
			if instruction, instErr := s.repository.GetPayableInstruction(
				ctx, relatedPayment.UserID, relatedPayment.AgencyOrderID); instErr == nil {
				_, _ = s.progressOpenPayment(
					ctx, binding, instruction, relatedPayment, relatedAttempt,
				)
			} else {
				_, _ = s.reconcileAuthorization(ctx, binding, relatedPayment, relatedAttempt)
			}
		}
	}
	return s.repository.MarkWebhookProcessed(
		ctx, registration.Environment, registration.WebhookID, input.EventID, now)
}

// Tick reconciles payer checkout authorization attempts. Per-MO capture and
// compensation use their own durable operation owners and entry points.
func (s *Service) Tick(ctx context.Context) error {
	now := s.clock.Now()
	items, err := s.repository.ListReconcileDue(ctx, s.config.ReconcileBatch, now)
	if err != nil {
		return err
	}
	for _, item := range items {
		binding, bindingFound, bindingErr := s.repository.GetBinding(
			ctx, item.Payment.ProviderEnvironment,
		)
		if bindingErr != nil || !bindingFound {
			continue
		}
		instruction, err := s.repository.GetPayableInstruction(
			ctx, item.Payment.UserID, item.Payment.AgencyOrderID)
		if err != nil {
			continue
		}
		if _, err := s.progressOpenPayment(ctx, binding, instruction,
			item.Payment, item.Attempt); err != nil &&
			!errors.Is(err, domain.ErrInstructionExpired) {
			continue
		}
	}
	return nil
}

// ListPaymentReconciliations exposes the read-only operator queue owned by
// Payment. An accepted receipt remains the only way to open Procurement.
func (s *Service) ListPaymentReconciliations(
	ctx context.Context,
	limit int,
) ([]PaymentReconciliationItem, error) {
	return s.repository.ListPaymentReconciliations(ctx, limit)
}

func (s *Service) CountPaymentReconciliations(ctx context.Context) (int, error) {
	return s.repository.CountPaymentReconciliations(ctx)
}

// RegisterBinding은 운영자가 확인한 non-secret merchant identity를 저장한다.
func (s *Service) RegisterBinding(ctx context.Context, binding domain.AccountBinding) error {
	if strings.TrimSpace(binding.Environment) == "" ||
		strings.TrimSpace(binding.MerchantID) == "" ||
		strings.TrimSpace(binding.WebhookID) == "" ||
		strings.TrimSpace(binding.VerifiedBy) == "" {
		return domain.ErrInvalid
	}
	return s.repository.UpsertBinding(ctx, binding, s.clock.Now())
}

func (s *Service) newOperation(
	purpose domain.OperationPurpose,
	ownerKind, ownerID, key string,
	amountMinor int64,
) domain.ExternalOperation {
	hash := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%d", purpose, key, amountMinor))
	return domain.ExternalOperation{
		ID: s.ids.NewID(), Purpose: purpose, OwnerKind: ownerKind, OwnerID: ownerID,
		IdempotencyKey: key, RequestHash: "0x" + hex.EncodeToString(hash[:]),
		State: domain.OperationPrepared,
	}
}

func providerReason(err error) string {
	var apiErr *paypal.APIError
	if errors.As(err, &apiErr) {
		if apiErr.IssueCode != "" {
			return apiErr.IssueCode
		}
		if apiErr.Name != "" {
			return apiErr.Name
		}
		return fmt.Sprintf("HTTP_%d", apiErr.StatusCode)
	}
	return "PROVIDER_ERROR"
}

func newNonce() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
