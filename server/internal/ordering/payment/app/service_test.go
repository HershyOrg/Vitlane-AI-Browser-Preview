package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"strings"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/infra/paypal"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fakeIDs struct{ next int }

func (g *fakeIDs) NewID() string {
	g.next++
	return fmt.Sprintf("id-%d", g.next)
}

type directTransactor struct{}

func (directTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type trackingTransactor struct{ active bool }

func (t *trackingTransactor) WithinTransaction(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	t.active = true
	defer func() { t.active = false }()
	return fn(ctx)
}

type repositoryTransactor struct{ repository *fakeRepository }

func (t repositoryTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	payment := *t.repository.payment
	attempt := *t.repository.attempt
	authorization := t.repository.authorization
	receipts := make(map[string]domain.FundsReceipt, len(t.repository.receipts))
	for key, receipt := range t.repository.receipts {
		receipts[key] = receipt
	}
	operations := make(map[string]*domain.ExternalOperation, len(t.repository.operations))
	for key, operation := range t.repository.operations {
		copied := *operation
		operations[key] = &copied
	}
	if err := fn(ctx); err != nil {
		t.repository.payment = &payment
		t.repository.attempt = &attempt
		t.repository.authorization = authorization
		t.repository.receipts = receipts
		t.repository.operations = operations
		return err
	}
	return nil
}

type fakeInstructionGate struct {
	calls   int
	validAt time.Time
	err     error
}

func (c *fakeInstructionGate) ConfirmInstruction(_ context.Context, claim procmsg.InstructionClaim) error {
	c.calls++
	c.validAt = claim.ValidAt
	return c.err
}

// fakeRepository는 Repository의 in-memory 구현이다. 상태 전이는 단순 저장이며
// terminal 가드 같은 SQL 제약은 테스트 대상 flow에 필요한 만큼만 재현한다.
type fakeRepository struct {
	instruction                      domain.PayableInstruction
	payment                          *domain.CustomerPayment
	attempt                          *domain.PayPalAttempt
	authorization                    *domain.PayPalAuthorization
	operations                       map[string]*domain.ExternalOperation // idempotency key 기준
	receipts                         map[string]domain.FundsReceipt
	binding                          *domain.AccountBinding
	webhooks                         map[string]bool
	disputes                         map[string]domain.PayPalDisputeCase
	disputeActions                   map[string][]domain.PayPalDisputeManualAction
	pendingCompensationNotifications []MOCompensationNotification
	markedCompensationNotifications  []string
	markOperationSentErr             error
	units                            struct {
		allAccepted  bool
		anyFailed    bool
		someAccepted bool
	}
}

func (r *fakeRepository) ListPendingMOCompensationNotifications(
	_ context.Context, limit int,
) ([]MOCompensationNotification, error) {
	items := r.pendingCompensationNotifications
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return append([]MOCompensationNotification(nil), items...), nil
}

func (r *fakeRepository) MarkMOCompensationSupportNotified(
	_ context.Context, compensationID string, _ time.Time,
) error {
	r.markedCompensationNotifications = append(
		r.markedCompensationNotifications, compensationID,
	)
	return nil
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		operations:     map[string]*domain.ExternalOperation{},
		receipts:       map[string]domain.FundsReceipt{},
		webhooks:       map[string]bool{},
		disputes:       map[string]domain.PayPalDisputeCase{},
		disputeActions: map[string][]domain.PayPalDisputeManualAction{},
	}
}

func (r *fakeRepository) GetPayableInstruction(_ context.Context, userID, orderID string) (domain.PayableInstruction, error) {
	if r.instruction.AgencyOrderID != orderID || r.instruction.UserID != userID {
		return domain.PayableInstruction{}, domain.ErrNotFound
	}
	return r.instruction, nil
}

func (r *fakeRepository) GetOpenPayment(_ context.Context, userID, orderID string) (domain.CustomerPayment, domain.PayPalAttempt, bool, error) {
	if r.payment == nil || r.payment.UserID != userID || r.payment.AgencyOrderID != orderID {
		return domain.CustomerPayment{}, domain.PayPalAttempt{}, false, nil
	}
	switch r.payment.State {
	case domain.PaymentFailed, domain.PaymentAbandoned, domain.PaymentExpired, domain.PaymentSuperseded:
		return domain.CustomerPayment{}, domain.PayPalAttempt{}, false, nil
	}
	return *r.payment, *r.attempt, true, nil
}

func (r *fakeRepository) CreatePaymentWithAttempt(_ context.Context, payment domain.CustomerPayment, attempt domain.PayPalAttempt, operation domain.ExternalOperation) error {
	if r.payment != nil {
		switch r.payment.State {
		case domain.PaymentFailed, domain.PaymentAbandoned, domain.PaymentExpired, domain.PaymentSuperseded:
		default:
			return domain.ErrConflict
		}
	}
	r.payment = &payment
	r.attempt = &attempt
	op := operation
	r.operations[operation.IdempotencyKey] = &op
	return nil
}

func (r *fakeRepository) GetAttemptByNonce(_ context.Context, nonce string) (domain.CustomerPayment, domain.PayPalAttempt, error) {
	if r.attempt == nil || r.attempt.ReturnNonce != nonce {
		return domain.CustomerPayment{}, domain.PayPalAttempt{}, domain.ErrNotFound
	}
	return *r.payment, *r.attempt, nil
}

func (r *fakeRepository) FindByPayPalOrder(_ context.Context, orderID string) (domain.CustomerPayment, domain.PayPalAttempt, bool, error) {
	if r.attempt == nil || r.attempt.PayPalOrderID != orderID {
		return domain.CustomerPayment{}, domain.PayPalAttempt{}, false, nil
	}
	return *r.payment, *r.attempt, true, nil
}

func (r *fakeRepository) MarkOperationSent(_ context.Context, operation domain.ExternalOperation, attemptID string, attemptState domain.AttemptState, paymentID string, paymentState domain.PaymentState, firstSentAt, deadline time.Time) error {
	if r.markOperationSentErr != nil {
		return r.markOperationSentErr
	}
	op, found := r.operations[operation.IdempotencyKey]
	if !found || (op.State != domain.OperationPrepared && op.State != domain.OperationUnknown) ||
		op.Purpose != operation.Purpose || op.OwnerKind != operation.OwnerKind ||
		op.OwnerID != operation.OwnerID || op.RequestHash != operation.RequestHash {
		return domain.ErrConflict
	}
	op.State = domain.OperationSent
	if op.FirstSentAt == nil {
		op.FirstSentAt = &firstSentAt
	}
	r.attempt.State = attemptState
	r.payment.State = paymentState
	return nil
}

func (r *fakeRepository) RecordOrderCreated(_ context.Context, attemptID, paymentID, paypalOrderID, approvalURL string, _ time.Time) error {
	for _, op := range r.operations {
		if op.OwnerID == attemptID && op.Purpose == domain.OperationPayPalOrderCreate {
			op.State = domain.OperationSucceeded
			op.ProviderResourceID = paypalOrderID
		}
	}
	r.attempt.State = domain.AttemptPayerActionRequired
	r.attempt.PayPalOrderID = paypalOrderID
	if approvalURL != "" {
		r.attempt.ApprovalURL = approvalURL
	}
	r.payment.State = domain.PaymentActionRequired
	return nil
}

func (r *fakeRepository) RecordAttemptOutcome(_ context.Context, attemptID string, attemptState domain.AttemptState, paymentID string, paymentState domain.PaymentState, purpose domain.OperationPurpose, operationState domain.OperationState, reason string, _ time.Time) error {
	if purpose != "" && operationState != "" {
		for _, op := range r.operations {
			if op.OwnerID == attemptID && op.Purpose == purpose &&
				(op.State == domain.OperationPrepared || op.State == domain.OperationSent ||
					op.State == domain.OperationUnknown) {
				op.State = operationState
			}
		}
	}
	if attemptState != "" {
		r.attempt.State = attemptState
		r.attempt.LastReasonCode = reason
	}
	if paymentState != "" {
		r.payment.State = paymentState
		r.payment.LastReasonCode = reason
	}
	return nil
}

func (r *fakeRepository) MarkPayerApproved(_ context.Context, attemptID, paymentID string, _ time.Time) error {
	r.attempt.State = domain.AttemptPayerApproved
	r.payment.State = domain.PaymentProcessing
	return nil
}

func (r *fakeRepository) PrepareAuthorizationOperation(
	_ context.Context,
	attemptID string,
	operation domain.ExternalOperation,
	_ time.Time,
) (domain.ExternalOperation, bool, error) {
	for _, candidate := range r.operations {
		if candidate.OwnerID == attemptID &&
			candidate.Purpose == domain.OperationPayPalAuthorize {
			return *candidate, false, nil
		}
	}
	copy := operation
	r.operations[operation.IdempotencyKey] = &copy
	return copy, true, nil
}

func (r *fakeRepository) RecordAuthorizationCompleted(
	_ context.Context,
	attemptID, paymentID string,
	authorization domain.PayPalAuthorization,
	_ time.Time,
) (bool, error) {
	if r.authorization != nil {
		if r.authorization.PayPalAuthorizationID != authorization.PayPalAuthorizationID {
			return false, domain.ErrConflict
		}
		return false, nil
	}
	copy := authorization
	r.authorization = &copy
	for _, operation := range r.operations {
		if operation.OwnerID == attemptID &&
			operation.Purpose == domain.OperationPayPalAuthorize {
			operation.State = domain.OperationSucceeded
			operation.ProviderResourceID = authorization.PayPalAuthorizationID
		}
	}
	r.attempt.State = domain.AttemptAuthorizeCompleted
	r.payment.State = domain.PaymentAuthorized
	return true, nil
}

func (r *fakeRepository) ListReconcileDue(_ context.Context, _ int, _ time.Time) ([]ReconcileItem, error) {
	if r.payment == nil {
		return nil, nil
	}
	switch r.payment.State {
	case domain.PaymentCreated, domain.PaymentActionRequired,
		domain.PaymentProcessing, domain.PaymentOutcomeUnknown:
		return []ReconcileItem{{Payment: *r.payment, Attempt: *r.attempt}}, nil
	}
	return nil, nil
}

func (r *fakeRepository) ListPaymentReconciliations(
	_ context.Context,
	_ int,
) ([]PaymentReconciliationItem, error) {
	if r.payment == nil || r.payment.State != domain.PaymentOutcomeUnknown {
		return nil, nil
	}
	return []PaymentReconciliationItem{{
		PaymentID: r.payment.ID, AgencyOrderID: r.payment.AgencyOrderID,
		PayPalAttemptID: r.attempt.ID, PayPalOrderID: r.attempt.PayPalOrderID,
		ProviderEnvironment: r.payment.ProviderEnvironment,
		AmountMinor:         r.payment.AmountMinor, Currency: r.payment.Currency,
		PaymentState: r.payment.State, AttemptState: r.attempt.State,
		ReasonCode: r.payment.LastReasonCode, UpdatedAt: r.payment.UpdatedAt,
	}}, nil
}

func (r *fakeRepository) CountPaymentReconciliations(_ context.Context) (int, error) {
	items, err := r.ListPaymentReconciliations(context.Background(), 100)
	return len(items), err
}

func (r *fakeRepository) GetBinding(_ context.Context, environment string) (domain.AccountBinding, bool, error) {
	if r.binding == nil || r.binding.Environment != environment {
		return domain.AccountBinding{}, false, nil
	}
	return *r.binding, true, nil
}

func (r *fakeRepository) UpsertBinding(_ context.Context, binding domain.AccountBinding, _ time.Time) error {
	r.binding = &binding
	return nil
}

func (r *fakeRepository) InsertWebhookEvent(_ context.Context, event domain.WebhookEvent) (bool, error) {
	key := event.Environment + "|" + event.WebhookID + "|" + event.EventID
	if r.webhooks[key] {
		return false, nil
	}
	r.webhooks[key] = true
	return true, nil
}

func (r *fakeRepository) MarkWebhookProcessed(_ context.Context, _, _, _ string, _ time.Time) error {
	return nil
}

func (r *fakeRepository) ProcessDisputeWebhook(
	_ context.Context,
	event domain.WebhookEvent,
	observation domain.DisputeObservation,
	caseID string,
) (domain.PayPalDisputeCase, bool, error) {
	key := observation.Environment + "|" + observation.DisputeID
	webhookKey := event.Environment + "|" + event.WebhookID + "|" + event.EventID
	if r.webhooks[webhookKey] {
		return r.disputes[key], false, nil
	}
	if observation.CaptureID == "CROSS-ENV" {
		return domain.PayPalDisputeCase{}, false, domain.ErrInstructionMismatch
	}
	current, found := r.disputes[key]
	if !found && observation.CaptureID == "" {
		return domain.PayPalDisputeCase{}, false, domain.ErrDisputeNotFound
	}
	if !found {
		current = domain.PayPalDisputeCase{
			ID: caseID, Environment: observation.Environment,
			DisputeID: observation.DisputeID, AgencyOrderID: "order-1",
			CustomerPaymentID: "payment-1", MOCashReceiptID: "receipt-1",
			CaptureID: observation.CaptureID, OpenedAt: observation.ObservedAt,
			CreatedAt: event.ReceivedAt, Version: 0,
		}
	}
	if current.CaptureID != observation.CaptureID && observation.CaptureID != "" {
		return domain.PayPalDisputeCase{}, false, domain.ErrInstructionMismatch
	}
	if !observation.ObservedAt.Before(current.LastObservedAt) {
		current.State = observation.State()
		current.ProviderStatus = observation.ProviderStatus
		current.Outcome = observation.Outcome
		current.Reason = observation.Reason
		current.LifecycleStage = observation.LifecycleStage
		current.SellerResponseDueAt = observation.SellerResponseDueAt
		current.LatestEventID = observation.EventID
		current.LatestEventType = observation.EventType
		current.LastObservedAt = observation.ObservedAt
		current.Version++
		current.UpdatedAt = event.ReceivedAt
		if current.State == domain.DisputeResolved {
			resolved := observation.ObservedAt
			current.ResolvedAt = &resolved
		}
	}
	r.webhooks[webhookKey] = true
	r.disputes[key] = current
	return current, true, nil
}

func (r *fakeRepository) ListPayPalDisputes(
	_ context.Context,
	filter DisputeQueueFilter,
) ([]domain.PayPalDisputeCase, error) {
	items := make([]domain.PayPalDisputeCase, 0)
	for _, item := range r.disputes {
		if item.Environment == filter.Environment &&
			(filter.State == "ALL" || string(item.State) == filter.State) {
			items = append(items, item)
		}
	}
	return items, nil
}

func (r *fakeRepository) GetPayPalDispute(
	_ context.Context,
	environment, caseID string,
) (domain.PayPalDisputeCase, error) {
	for _, item := range r.disputes {
		if item.Environment == environment && item.ID == caseID {
			return item, nil
		}
	}
	return domain.PayPalDisputeCase{}, domain.ErrDisputeNotFound
}

func (r *fakeRepository) ListPayPalDisputeActions(
	_ context.Context,
	environment, caseID string,
) ([]domain.PayPalDisputeManualAction, error) {
	if _, err := r.GetPayPalDispute(context.Background(), environment, caseID); err != nil {
		return nil, err
	}
	return append([]domain.PayPalDisputeManualAction(nil), r.disputeActions[caseID]...), nil
}

func (r *fakeRepository) RecordPayPalDisputeAction(
	_ context.Context,
	environment string,
	action domain.PayPalDisputeManualAction,
	expectedVersion int64,
) (domain.PayPalDisputeCase, domain.PayPalDisputeManualAction, bool, error) {
	item, err := r.GetPayPalDispute(
		context.Background(), environment, action.DisputeCaseID,
	)
	if err != nil {
		return domain.PayPalDisputeCase{}, domain.PayPalDisputeManualAction{}, false, err
	}
	for _, existing := range r.disputeActions[action.DisputeCaseID] {
		if existing.IdempotencyKeyHash == action.IdempotencyKeyHash {
			if existing.RequestHash != action.RequestHash {
				return domain.PayPalDisputeCase{}, domain.PayPalDisputeManualAction{}, false, domain.ErrConflict
			}
			return item, existing, true, nil
		}
	}
	if item.Version != expectedVersion {
		return domain.PayPalDisputeCase{}, domain.PayPalDisputeManualAction{}, false, domain.ErrConflict
	}
	r.disputeActions[action.DisputeCaseID] = append(r.disputeActions[action.DisputeCaseID], action)
	item.Version++
	if action.ObservedProviderStatus == domain.DisputeStatusResolved {
		item.State = domain.DisputeResolved
		item.ProviderStatus = domain.DisputeStatusResolved
		item.Outcome = action.ObservedOutcome
		if item.Outcome == "" {
			item.Outcome = domain.DisputeOutcomeNone
		}
		resolved := action.ObservedAt
		item.ResolvedAt = &resolved
	}
	for key, value := range r.disputes {
		if value.ID == item.ID {
			r.disputes[key] = item
		}
	}
	return item, action, false, nil
}

// fakeProvider는 시나리오를 스크립트하는 PayPal fake다.
type fakeProvider struct {
	orderStatus                 string
	authorizationStatus         string
	authorizationID             string
	authorizationCreateTime     string
	authorizationParentMismatch bool
	authorizationPayeeMismatch  bool
	authorizeErr                error
	authorizeCalls              int
	authorizeRequestIDs         []string
	getAuthorizedCalls          int
	captureAuthorizationInputs  []paypal.CaptureAuthorizationInput
	captureAuthorizationResult  paypal.Capture
	captureAuthorizationErr     error
	getCaptureResult            paypal.Capture
	getCaptureErr               error
	captureStatus               string
	captureMinor                int64
	captureTime                 string
	payee                       string
	createErr                   error
	captureErr                  error
	refundStatus                string
	refundMinor                 int64
	refundCaptureID             string
	refundInvoiceID             string
	refundParentMismatch        bool
	refundMetadataMismatch      bool
	refundErr                   error
	createCalls                 int
	createRequestIDs            []string
	captureCalls                int
	refundCalls                 int
	refundRequestIDs            []string
	voidCalls                   int
	reauthorizeCalls            int
	reauthorizeInputs           []paypal.ReauthorizeAuthorizationInput
	reauthorizeErr              error
	refundHook                  func()
	verifyResponse              bool
	lastReturnURL               string
	getOrderCalls               int
	omitPostPayee               bool
}

func (p *fakeProvider) CreateOrder(_ context.Context, input paypal.CreateOrderInput) (paypal.Order, error) {
	p.createCalls++
	p.createRequestIDs = append(p.createRequestIDs, input.RequestID)
	p.lastReturnURL = input.ReturnURL
	if p.createErr != nil {
		return paypal.Order{}, p.createErr
	}
	return paypal.Order{ID: "PP-ORDER-1", Intent: "AUTHORIZE", Status: "PAYER_ACTION_REQUIRED",
		PayerActionURL: "https://sandbox.paypal.com/checkoutnow?token=PP-ORDER-1",
		AmountMinor:    input.AmountMinor, Currency: "USD", PayeeMerchant: p.payee}, nil
}

func (p *fakeProvider) GetOrder(_ context.Context, id string) (paypal.Order, error) {
	p.getOrderCalls++
	order := paypal.Order{ID: id, Intent: "AUTHORIZE", Status: p.orderStatus,
		PayerActionURL: "https://sandbox.paypal.com/checkoutnow?token=" + id,
		AmountMinor:    10600, Currency: "USD", PayeeMerchant: p.payee}
	if p.authorizationStatus != "" || p.orderStatus == "COMPLETED" {
		status := p.authorizationStatus
		if status == "" {
			status = "CREATED"
		}
		id := p.authorizationID
		if id == "" {
			id = "PP-AUTH-1"
		}
		order.Authorizations = []paypal.Authorization{{
			ID: id, Status: status, AmountMinor: 10600, Currency: "USD",
			CreateTime: "2026-08-19T11:59:30Z",
		}}
	}
	if p.orderStatus == "COMPLETED" || p.captureStatus != "" && p.orderStatus == "" {
		order.CaptureID = "PP-CAPTURE-1"
		order.CaptureStatus = p.captureStatus
		order.CaptureMinor = p.captureMinor
		order.CaptureTime = p.captureTime
		order.CaptureEconomicsReconciled = true
		order.ProcessorFeeMinor = 600
		order.NetReceivableMinor = p.captureMinor - 600
	}
	return order, nil
}

func (p *fakeProvider) AuthorizeOrder(
	_ context.Context,
	id, requestID string,
) (paypal.Order, error) {
	p.authorizeCalls++
	p.authorizeRequestIDs = append(p.authorizeRequestIDs, requestID)
	if p.authorizeErr != nil {
		return paypal.Order{}, p.authorizeErr
	}
	p.orderStatus = "COMPLETED"
	if p.authorizationStatus == "" {
		p.authorizationStatus = "CREATED"
	}
	return p.GetOrder(context.Background(), id)
}

func (p *fakeProvider) GetAuthorizedOrder(
	_ context.Context,
	id string,
) (paypal.Order, error) {
	p.getAuthorizedCalls++
	return p.GetOrder(context.Background(), id)
}

func (p *fakeProvider) GetAuthorization(
	_ context.Context,
	id string,
) (paypal.Authorization, error) {
	amountMinor := int64(10600)
	if count := len(p.reauthorizeInputs); count > 0 && id == "PP-AUTH-2" {
		amountMinor = p.reauthorizeInputs[count-1].AmountMinor
	}
	parentOrderID := "PP-ORDER-1"
	payeeMerchant := p.payee
	if p.authorizationParentMismatch {
		parentOrderID = "PP-ORDER-WRONG"
	}
	if p.authorizationPayeeMismatch {
		payeeMerchant = "MERCHANT-WRONG"
	}
	return paypal.Authorization{
		ID: id, Status: p.authorizationStatus,
		AmountMinor: amountMinor, Currency: "USD",
		ParentOrderID: parentOrderID, PayeeMerchant: payeeMerchant,
		CreateTime: p.authorizationCreateTime,
	}, nil
}

func (p *fakeProvider) CaptureAuthorization(
	_ context.Context,
	input paypal.CaptureAuthorizationInput,
) (paypal.Capture, error) {
	p.captureAuthorizationInputs = append(p.captureAuthorizationInputs, input)
	if p.captureAuthorizationResult.ID != "" || p.captureAuthorizationErr != nil {
		return p.captureAuthorizationResult, p.captureAuthorizationErr
	}
	return paypal.Capture{
		ID: "PP-MO-CAPTURE-1", Status: "COMPLETED",
		AmountMinor: input.AmountMinor, Currency: input.Currency,
		InvoiceID: input.InvoiceID, FinalCapture: input.FinalCapture,
		CreateTime: "2026-08-19T12:01:00Z",
	}, nil
}

func (p *fakeProvider) VoidAuthorization(context.Context, string, string) error {
	p.voidCalls++
	p.authorizationStatus = "VOIDED"
	return nil
}

func (p *fakeProvider) ReauthorizeAuthorization(
	_ context.Context,
	input paypal.ReauthorizeAuthorizationInput,
) (paypal.Authorization, error) {
	p.reauthorizeCalls++
	p.reauthorizeInputs = append(p.reauthorizeInputs, input)
	if p.reauthorizeErr != nil {
		return paypal.Authorization{}, p.reauthorizeErr
	}
	return paypal.Authorization{
		ID: "PP-AUTH-2", Status: "CREATED",
		AmountMinor: input.AmountMinor, Currency: input.Currency,
		ParentOrderID: input.OrderID, PayeeMerchant: input.PayeeMerchant,
		CreateTime: "2026-08-23T12:00:00Z",
	}, nil
}

func (p *fakeProvider) CaptureOrder(_ context.Context, id, requestID string) (paypal.Order, error) {
	p.captureCalls++
	if p.captureErr != nil {
		return paypal.Order{}, p.captureErr
	}
	p.orderStatus = "COMPLETED"
	payee := p.payee
	if p.omitPostPayee {
		payee = ""
	}
	return paypal.Order{ID: id, Status: "COMPLETED", CaptureID: "PP-CAPTURE-1",
		CaptureStatus: p.captureStatus, CaptureMinor: p.captureMinor,
		CaptureTime: p.captureTime, Currency: "USD", PayeeMerchant: payee,
		CaptureEconomicsReconciled: true, ProcessorFeeMinor: 600,
		NetReceivableMinor: p.captureMinor - 600}, nil
}

func (p *fakeProvider) GetCapture(_ context.Context, id string) (paypal.Capture, error) {
	if p.getCaptureResult.ID != "" || p.getCaptureErr != nil {
		return p.getCaptureResult, p.getCaptureErr
	}
	if count := len(p.captureAuthorizationInputs); count > 0 {
		input := p.captureAuthorizationInputs[count-1]
		return paypal.Capture{
			ID: id, Status: "COMPLETED", AmountMinor: input.AmountMinor,
			Currency: input.Currency, InvoiceID: input.InvoiceID,
			FinalCapture: input.FinalCapture, CreateTime: "2026-08-19T12:01:00Z",
		}, nil
	}
	return paypal.Capture{ID: id, Status: p.captureStatus,
		AmountMinor: p.captureMinor, Currency: "USD", EconomicsReconciled: true,
		ProcessorFeeMinor: 600, NetReceivableMinor: p.captureMinor - 600}, nil
}

func (p *fakeProvider) RefundCapture(_ context.Context, captureID, requestID string, amountMinor int64) (paypal.Refund, error) {
	p.refundCalls++
	p.refundRequestIDs = append(p.refundRequestIDs, requestID)
	if p.refundHook != nil {
		p.refundHook()
	}
	if p.refundErr != nil {
		return paypal.Refund{}, p.refundErr
	}
	p.refundMinor = amountMinor
	p.refundCaptureID = captureID
	p.refundInvoiceID = requestID
	if p.refundParentMismatch {
		p.refundCaptureID = "PP-CAPTURE-WRONG"
	}
	if p.refundMetadataMismatch {
		p.refundInvoiceID = "paypal:mo-refund:wrong"
	}
	return paypal.Refund{ID: fmt.Sprintf("PP-REFUND-%d", p.refundCalls), Status: p.refundStatus,
		AmountMinor: amountMinor, Currency: "USD", ParentCaptureID: p.refundCaptureID,
		InvoiceID: p.refundInvoiceID}, nil
}

func (p *fakeProvider) GetRefund(_ context.Context, id string) (paypal.Refund, error) {
	return paypal.Refund{
		ID: id, Status: p.refundStatus, AmountMinor: p.refundMinor, Currency: "USD",
		ParentCaptureID: p.refundCaptureID, InvoiceID: p.refundInvoiceID,
	}, nil
}

func (p *fakeProvider) VerifyWebhookSignature(_ context.Context, _ paypal.WebhookVerification) (bool, error) {
	return p.verifyResponse, nil
}

func newTestService(repository *fakeRepository, provider *fakeProvider) *Service {
	return newTestServiceWithInstructionGate(repository, provider, &fakeInstructionGate{})
}

func newTestServiceWithInstructionGate(
	repository *fakeRepository,
	provider *fakeProvider,
	consumer *fakeInstructionGate,
) *Service {
	return newTestServiceWithDependencies(repository, provider, consumer, directTransactor{})
}

func newTestServiceWithDependencies(
	repository *fakeRepository,
	provider *fakeProvider,
	consumer *fakeInstructionGate,
	transactor interface {
		WithinTransaction(context.Context, func(context.Context) error) error
	},
) *Service {
	return NewService(repository, provider, consumer, transactor, Config{
		Environment: "SANDBOX", WebhookID: "WH-1",
		PublicBaseURL: "https://app.vitlane.test",
	}, &fakeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}, &fakeIDs{})
}

func fixtureRepository() *fakeRepository {
	repository := newFakeRepository()
	repository.binding = &domain.AccountBinding{
		Environment: "SANDBOX", MerchantID: "MERCHANT-1", WebhookID: "WH-1",
		ClientIDFingerprint: "fp", VerifiedBy: "release-approver",
	}
	repository.instruction = domain.PayableInstruction{
		AgencyOrderID: "order-1", UserID: "user-1", Rail: "PAYPAL",
		ProviderEnvironment: "SANDBOX", Asset: "USD", EconomicEffect: "NO_REAL_VALUE",
		MerchantExecutionMode: "SIMULATED_NO_EFFECT",
		ExecutionProfileHash:  domain.PayPalSandboxExecutionProfileHash,
		SnapshotHash:          "0xhash",
		CustomerPayableMinor:  10600, Currency: "USD",
		ExpiresAt: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
	}
	return repository
}

func TestStartCheckoutCreatesOrderAndReturnsApprovalURL(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{payee: "MERCHANT-1"}
	service := newTestService(repository, provider)

	view, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	if view.ApprovalURL == "" || view.Payment.State != domain.PaymentActionRequired {
		t.Fatalf("unexpected view: %+v", view)
	}
	if provider.createCalls != 1 {
		t.Fatalf("create calls = %d", provider.createCalls)
	}
	// 복귀 URL은 SPA 라우트(camelCase)여야 한다 — kebab-case는 404로 떨어진다
	// (실 Sandbox conformance가 잡은 결함의 회귀 가드).
	if !strings.Contains(provider.lastReturnURL, "/agencyOrder/order-1/payment") {
		t.Fatalf("return URL must target the SPA route: %s", provider.lastReturnURL)
	}
	// 멱등: 같은 호출은 새 주문을 만들지 않고 같은 승인 URL을 돌려준다.
	provider.orderStatus = "PAYER_ACTION_REQUIRED"
	again, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatalf("StartCheckout replay: %v", err)
	}
	if provider.createCalls != 1 || again.Attempt.PayPalOrderID != "PP-ORDER-1" {
		t.Fatalf("replay created a new order: calls=%d %+v", provider.createCalls, again)
	}
}

func TestResumePreApprovalAdoptsFreshPayerActionURL(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{payee: "MERCHANT-1"}
	service := newTestService(repository, provider)

	started, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	repository.attempt.ApprovalURL = ""
	provider.orderStatus = "PAYER_ACTION_REQUIRED"

	resumed, err := service.Resume(context.Background(), ResumeInput{
		UserID: "user-1", AgencyOrderID: "order-1", ReturnNonce: started.ReturnNonce,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.ApprovalURL == "" || repository.attempt.ApprovalURL != resumed.ApprovalURL {
		t.Fatalf("fresh payer-action URL was not adopted: view=%q stored=%q",
			resumed.ApprovalURL, repository.attempt.ApprovalURL)
	}
}

func TestApprovedOrderWithBlankPayeeFailsClosedBeforeCapture(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{payee: "MERCHANT-1"}
	service := newTestService(repository, provider)
	view, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	provider.orderStatus = "APPROVED"
	provider.payee = ""

	_, err = service.Resume(context.Background(), ResumeInput{
		UserID: "user-1", AgencyOrderID: "order-1", ReturnNonce: view.ReturnNonce,
	})
	if !errors.Is(err, domain.ErrBindingMismatch) {
		t.Fatalf("blank payee error=%v want=%v", err, domain.ErrBindingMismatch)
	}
	if provider.captureCalls != 0 || len(repository.receipts) != 0 {
		t.Fatalf("blank payee reached capture: calls=%d receipts=%d",
			provider.captureCalls, len(repository.receipts))
	}
}

func TestExpiredInstructionSupersedesBeforeCapture(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{payee: "MERCHANT-1"}
	service := newTestService(repository, provider)
	view, _ := service.StartCheckout(context.Background(), "user-1", "order-1")
	repository.instruction.ExpiresAt = time.Date(2026, 8, 19, 11, 0, 0, 0, time.UTC)
	provider.orderStatus = "APPROVED"

	_, err := service.Resume(context.Background(), ResumeInput{
		UserID: "user-1", AgencyOrderID: "order-1", ReturnNonce: view.ReturnNonce,
	})
	if !errors.Is(err, domain.ErrInstructionExpired) {
		t.Fatalf("expected instruction expired, got %v", err)
	}
	if repository.payment.State != domain.PaymentSuperseded ||
		provider.captureCalls != 0 {
		t.Fatalf("expired instruction must not capture: %+v", repository.payment)
	}
}

// 주문 종결(TERMINAL) 판정은 AgencyOrderProcess가 소유한다(ADR-0052) — Payment
// sweeper는 전부 성공한 주문에 어떤 환불 effect도 만들지 않아야 한다.

func TestWebhookDeduplicatesDeliveries(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{payee: "MERCHANT-1", verifyResponse: true}
	service := newTestService(repository, provider)

	input := WebhookInput{
		ProviderEnvironment: "SANDBOX",
		TransmissionID:      "tx-1", EventID: "WH-EVT-1",
		EventType: "PAYMENT.CAPTURE.COMPLETED", RawBody: []byte(`{"id":"WH-EVT-1"}`),
	}
	if err := service.HandleWebhook(context.Background(), input); err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	if err := service.HandleWebhook(context.Background(), input); err != nil {
		t.Fatalf("duplicate webhook must be a safe no-op: %v", err)
	}
	provider.verifyResponse = false
	if err := service.HandleWebhook(context.Background(), WebhookInput{
		ProviderEnvironment: "SANDBOX",
		TransmissionID:      "tx-2", EventID: "WH-EVT-2", RawBody: []byte(`{}`),
	}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid signature must be rejected, got %v", err)
	}
}

func TestStartCheckoutFailsCloseWithoutBinding(t *testing.T) {
	repository := fixtureRepository()
	repository.binding = nil
	service := newTestService(repository, &fakeProvider{})
	if _, err := service.StartCheckout(context.Background(), "user-1", "order-1"); !errors.Is(err, domain.ErrRailUnavailable) {
		t.Fatalf("expected rail unavailable, got %v", err)
	}
}

func TestStartCheckoutRejectsNonPayPalInstruction(t *testing.T) {
	repository := fixtureRepository()
	repository.instruction.Rail = "GIWA"
	service := newTestService(repository, &fakeProvider{})
	if _, err := service.StartCheckout(context.Background(), "user-1", "order-1"); !errors.Is(err, domain.ErrInstructionMismatch) {
		t.Fatalf("expected instruction mismatch, got %v", err)
	}
}

func TestLiveIssueOnlyBlocksNewCheckoutBeforeProviderEffect(t *testing.T) {
	repository := fixtureRepository()
	repository.instruction.ProviderEnvironment = "LIVE"
	repository.instruction.EconomicEffect = "REAL_MONEY"
	repository.instruction.MerchantExecutionMode = "LIVE_MERCHANT_EFFECT"
	repository.instruction.ExecutionProfileHash = domain.PayPalLiveExecutionProfileHash
	repository.binding.Environment = "LIVE"
	repository.binding.WebhookID = "WH-LIVE"
	sandbox := &fakeProvider{payee: "MERCHANT-1"}
	live := &fakeProvider{payee: "MERCHANT-1"}
	registry, err := NewProviderRegistry(
		ProviderRegistration{
			Environment: "SANDBOX", Client: sandbox, WebhookID: "WH-SANDBOX",
			IssueEnabled: true, CaptureEnabled: true,
		},
		ProviderRegistration{
			Environment: "LIVE", Client: live, WebhookID: "WH-LIVE",
			IssueEnabled: true, CaptureEnabled: false,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithProviderRegistry(
		repository, registry, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "LIVE", PublicBaseURL: "https://app.vitlane.test"},
		&fakeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}, &fakeIDs{},
	)
	if _, err := service.StartCheckout(
		context.Background(), "user-1", "order-1",
	); !errors.Is(err, domain.ErrRailUnavailable) {
		t.Fatalf("StartCheckout error=%v", err)
	}
	if live.createCalls != 0 || sandbox.createCalls != 0 || repository.payment != nil {
		t.Fatalf("provider routing sandbox=%d live=%d", sandbox.createCalls, live.createCalls)
	}
}

func TestLiveGateOpenRoutesNewCheckoutByStoredEnvironment(t *testing.T) {
	repository := fixtureRepository()
	repository.instruction.ProviderEnvironment = "LIVE"
	repository.instruction.EconomicEffect = "REAL_MONEY"
	repository.instruction.MerchantExecutionMode = "LIVE_MERCHANT_EFFECT"
	repository.instruction.ExecutionProfileHash = domain.PayPalLiveExecutionProfileHash
	repository.binding.Environment = "LIVE"
	repository.binding.WebhookID = "WH-LIVE"
	sandbox := &fakeProvider{payee: "MERCHANT-1"}
	live := &fakeProvider{payee: "MERCHANT-1"}
	registry, err := NewProviderRegistry(
		ProviderRegistration{
			Environment: "SANDBOX", Client: sandbox, WebhookID: "WH-SANDBOX",
			IssueEnabled: true, CaptureEnabled: true,
		},
		ProviderRegistration{
			Environment: "LIVE", Client: live, WebhookID: "WH-LIVE",
			IssueEnabled: true, CaptureEnabled: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithProviderRegistry(
		repository, registry, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "LIVE", PublicBaseURL: "https://app.vitlane.test"},
		&fakeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}, &fakeIDs{},
	)
	service.EnableLiveMoneyGate(testLiveMoneyGate(true))
	if _, err := service.StartCheckout(context.Background(), "user-1", "order-1"); err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	if live.createCalls != 1 || sandbox.createCalls != 0 {
		t.Fatalf("provider routing sandbox=%d live=%d", sandbox.createCalls, live.createCalls)
	}
}

func TestStoredSandboxEnvironmentRoutesWithoutGlobalSelector(t *testing.T) {
	repository := fixtureRepository()
	live := &fakeProvider{payee: "MERCHANT-1"}
	registry, err := NewProviderRegistry(ProviderRegistration{
		Environment: "SANDBOX", Client: live, WebhookID: "WH-1",
		IssueEnabled: true, CaptureEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithProviderRegistry(
		repository, registry, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "LIVE", PublicBaseURL: "https://app.vitlane.test"},
		&fakeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}, &fakeIDs{},
	)
	if _, err := service.StartCheckout(
		context.Background(), "user-1", "order-1",
	); err != nil {
		t.Fatalf("StartCheckout error=%v", err)
	}
	if live.createCalls != 1 || repository.payment == nil {
		t.Fatalf("stored environment was not routed: calls=%d payment=%+v",
			live.createCalls, repository.payment)
	}
}

func TestCaptureGateOffPreventsTickFromCapturingPreviouslyApprovedOrder(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{payee: "MERCHANT-1"}
	openService := newTestService(repository, provider)
	if _, err := openService.StartCheckout(context.Background(), "user-1", "order-1"); err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	provider.orderStatus = "APPROVED"
	registry, err := NewProviderRegistry(ProviderRegistration{
		Environment: "SANDBOX", Client: provider, WebhookID: "WH-1",
		IssueEnabled: true, CaptureEnabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	pausedService := NewServiceWithProviderRegistry(
		repository, registry, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX", PublicBaseURL: "https://app.vitlane.test"},
		&fakeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}, &fakeIDs{},
	)
	if err := pausedService.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if provider.captureCalls != 0 || repository.attempt.State != domain.AttemptPayerApproved {
		t.Fatalf("paused Tick captureCalls=%d attempt=%s",
			provider.captureCalls, repository.attempt.State)
	}
}

func TestIssueOnlyDeploymentStillReconcilesPreviouslySentCreate(t *testing.T) {
	repository := fixtureRepository()
	repository.instruction.ProviderEnvironment = "LIVE"
	repository.instruction.EconomicEffect = "REAL_MONEY"
	repository.instruction.MerchantExecutionMode = "LIVE_MERCHANT_EFFECT"
	repository.instruction.ExecutionProfileHash = domain.PayPalLiveExecutionProfileHash
	repository.binding.Environment = "LIVE"
	repository.binding.WebhookID = "WH-LIVE"
	provider := &fakeProvider{
		payee: "MERCHANT-1", createErr: fmt.Errorf("%w: timeout", paypal.ErrOutcomeUnknown),
	}
	openRegistry, err := NewProviderRegistry(ProviderRegistration{
		Environment: "LIVE", Client: provider, WebhookID: "WH-LIVE",
		IssueEnabled: true, CaptureEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	openService := NewServiceWithProviderRegistry(
		repository, openRegistry, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "LIVE", PublicBaseURL: "https://app.vitlane.test"},
		&fakeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}, &fakeIDs{},
	)
	openService.EnableLiveMoneyGate(testLiveMoneyGate(true))
	if _, err := openService.StartCheckout(context.Background(), "user-1", "order-1"); err != nil {
		t.Fatalf("unknown StartCheckout: %v", err)
	}
	if repository.attempt.State != domain.AttemptOrderCreateUnknown || provider.createCalls != 1 {
		t.Fatalf("initial attempt=%s createCalls=%d", repository.attempt.State, provider.createCalls)
	}

	provider.createErr = nil
	pausedRegistry, err := NewProviderRegistry(ProviderRegistration{
		Environment: "LIVE", Client: provider, WebhookID: "WH-LIVE",
		IssueEnabled: true, CaptureEnabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	pausedService := NewServiceWithProviderRegistry(
		repository, pausedRegistry, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "LIVE", PublicBaseURL: "https://app.vitlane.test"},
		&fakeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}, &fakeIDs{},
	)
	pausedService.EnableLiveMoneyGate(testLiveMoneyGate(false))
	if _, err := pausedService.StartCheckout(context.Background(), "user-1", "order-1"); !errors.Is(err, domain.ErrRailUnavailable) {
		t.Fatalf("killed unknown create error=%v", err)
	}
	if provider.createCalls != 1 || repository.attempt.PayPalOrderID != "" {
		t.Fatalf("kill allowed another provider POST: createCalls=%d orderID=%q",
			provider.createCalls, repository.attempt.PayPalOrderID)
	}
}

type testLiveMoneyGate bool

func (g testLiveMoneyGate) AllowLiveMoneyEffect(context.Context) (bool, error) {
	return bool(g), nil
}

func TestNonconformingCreateResponseKeepsSameProviderRequestRecovery(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{
		payee: "MERCHANT-1", createErr: paypal.ErrNonconformingResponse,
	}
	service := newTestService(repository, provider)

	unknown, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil || unknown.Payment.State != domain.PaymentProcessing ||
		unknown.Attempt.State != domain.AttemptOrderCreateUnknown {
		t.Fatalf("nonconforming create was made terminal: view=%+v err=%v", unknown, err)
	}
	provider.createErr = nil
	recovered, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil || recovered.Attempt.PayPalOrderID != "PP-ORDER-1" ||
		provider.createCalls != 2 {
		t.Fatalf("nonconforming create did not recover: view=%+v calls=%d err=%v",
			recovered, provider.createCalls, err)
	}
	if len(provider.createRequestIDs) != 2 || provider.createRequestIDs[0] == "" ||
		provider.createRequestIDs[0] != provider.createRequestIDs[1] {
		t.Fatalf("create recovery changed provider request: %v", provider.createRequestIDs)
	}
}

func TestCreateWithoutProviderResourceClosesAfterIdempotencyDeadline(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{
		payee:     "MERCHANT-1",
		createErr: fmt.Errorf("%w: response lost", paypal.ErrOutcomeUnknown),
	}
	service := newTestService(repository, provider)

	unknown, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil || unknown.Attempt.State != domain.AttemptOrderCreateUnknown {
		t.Fatalf("initial create unknown: view=%+v err=%v", unknown, err)
	}
	repository.markOperationSentErr = domain.ErrIdempotencyExpired
	closed, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatalf("expired create must close durably: %v", err)
	}
	if closed.Payment.State != domain.PaymentAbandoned ||
		closed.Attempt.State != domain.AttemptAbandonedBeforeAuthorize ||
		repository.payment.LastReasonCode != "ORDER_CREATE_IDEMPOTENCY_EXPIRED_NO_RESOURCE" {
		t.Fatalf("expired create projection=%+v attempt=%+v",
			repository.payment, repository.attempt)
	}
	if provider.createCalls != 1 {
		t.Fatalf("expired create re-posted provider operation: calls=%d", provider.createCalls)
	}
}

func TestAuthorizeWithoutProviderResourceStaysReconciliationOnlyAfterDeadline(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{payee: "MERCHANT-1"}
	service := newTestService(repository, provider)
	started, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	provider.orderStatus = "APPROVED"
	repository.markOperationSentErr = domain.ErrIdempotencyExpired

	closed, err := service.Resume(context.Background(), ResumeInput{
		UserID: "user-1", AgencyOrderID: "order-1", ReturnNonce: started.ReturnNonce,
	})
	if err != nil {
		t.Fatalf("expired authorization must close durably: %v", err)
	}
	if closed.Payment.State != domain.PaymentOutcomeUnknown ||
		closed.Attempt.State != domain.AttemptAuthorizeOutcomeUnknown ||
		repository.payment.LastReasonCode != "AUTHORIZE_IDEMPOTENCY_EXPIRED_RECONCILIATION_ONLY" {
		t.Fatalf("expired authorization projection=%+v attempt=%+v",
			repository.payment, repository.attempt)
	}
	if provider.authorizeCalls != 0 {
		t.Fatalf("expired authorization re-posted provider operation: calls=%d",
			provider.authorizeCalls)
	}
	if _, _, found, err := repository.GetOpenPayment(
		context.Background(), "user-1", "order-1",
	); err != nil || !found {
		t.Fatalf("unknown authorization must keep the payment open: found=%v err=%v",
			found, err)
	}
}

func TestWebhookCannotCrossProviderEnvironment(t *testing.T) {
	repository := fixtureRepository()
	sandbox := &fakeProvider{payee: "MERCHANT-1", verifyResponse: true}
	live := &fakeProvider{verifyResponse: true}
	registry, err := NewProviderRegistry(
		ProviderRegistration{
			Environment: "SANDBOX", Client: sandbox, WebhookID: "WH-1",
			IssueEnabled: true, CaptureEnabled: true,
		},
		ProviderRegistration{Environment: "LIVE", Client: live, WebhookID: "WH-LIVE"},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithProviderRegistry(
		repository, registry, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX", PublicBaseURL: "https://app.vitlane.test"},
		&fakeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}, &fakeIDs{},
	)
	if _, err := service.StartCheckout(context.Background(), "user-1", "order-1"); err != nil {
		t.Fatal(err)
	}
	err = service.HandleWebhook(context.Background(), WebhookInput{
		ProviderEnvironment: "LIVE", TransmissionID: "tx-live", EventID: "event-live",
		RelatedOrderID: "PP-ORDER-1", RawBody: []byte(`{"id":"event-live"}`),
	})
	if !errors.Is(err, domain.ErrInstructionMismatch) {
		t.Fatalf("cross-environment webhook error=%v", err)
	}
	if len(repository.webhooks) != 0 {
		t.Fatalf("cross-environment event entered inbox: %+v", repository.webhooks)
	}
}

func TestSignedDisputeWebhookCreatesEnvironmentBoundCaseAndDeduplicates(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{payee: "MERCHANT-1", verifyResponse: true}
	service := newTestService(repository, provider)
	now := service.clock.Now()
	due := now.Add(48 * time.Hour)
	input := WebhookInput{
		ProviderEnvironment: "SANDBOX", TransmissionID: "tx-dispute-1",
		EventID: "WH-DISPUTE-1", EventType: domain.PayPalDisputeCreated,
		ResourceKind: "dispute", ResourceID: "PP-D-1", DisputeID: "PP-D-1",
		DisputedCaptureID: "PP-CAPTURE-1", DisputeStatus: "WAITING_FOR_SELLER_RESPONSE",
		DisputeReason:         "MERCHANDISE_OR_SERVICE_NOT_RECEIVED",
		DisputeLifecycleStage: "INQUIRY", SellerResponseDueAt: &due,
		ResourceOccurredAt: &now, RawBody: []byte(`{"id":"WH-DISPUTE-1"}`),
	}
	if err := service.HandleWebhook(context.Background(), input); err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	if err := service.HandleWebhook(context.Background(), input); err != nil {
		t.Fatalf("duplicate dispute webhook: %v", err)
	}
	if len(repository.disputes) != 1 {
		t.Fatalf("dispute cases=%d", len(repository.disputes))
	}
	items, err := service.ListPayPalDisputeQueue(context.Background(), DisputeQueueFilter{
		Environment: "SANDBOX", State: "OPEN",
	})
	if err != nil || len(items) != 1 || items[0].Environment != "SANDBOX" ||
		items[0].CustomerPaymentID != "payment-1" {
		t.Fatalf("queue=%+v err=%v", items, err)
	}
}

func TestDisputeWebhookRejectsCrossEnvironmentCapture(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{verifyResponse: true}
	service := newTestService(repository, provider)
	err := service.HandleWebhook(context.Background(), WebhookInput{
		ProviderEnvironment: "SANDBOX", TransmissionID: "tx-dispute-cross",
		EventID: "WH-DISPUTE-CROSS", EventType: domain.PayPalDisputeCreated,
		DisputeID: "PP-D-CROSS", DisputedCaptureID: "CROSS-ENV",
		RawBody: []byte(`{"id":"WH-DISPUTE-CROSS"}`),
	})
	if !errors.Is(err, domain.ErrInstructionMismatch) || len(repository.disputes) != 0 {
		t.Fatalf("cross-environment result err=%v disputes=%+v", err, repository.disputes)
	}
}

func TestManualDisputeActionIsCASAndIdempotent(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{verifyResponse: true}
	service := newTestService(repository, provider)
	now := service.clock.Now()
	if err := service.HandleWebhook(context.Background(), WebhookInput{
		ProviderEnvironment: "SANDBOX", TransmissionID: "tx-dispute-action",
		EventID: "WH-DISPUTE-ACTION", EventType: domain.PayPalDisputeCreated,
		DisputeID: "PP-D-ACTION", DisputedCaptureID: "PP-CAPTURE-1",
		RawBody: []byte(`{"id":"WH-DISPUTE-ACTION"}`),
	}); err != nil {
		t.Fatal(err)
	}
	items, _ := service.ListPayPalDisputeQueue(context.Background(), DisputeQueueFilter{
		Environment: "SANDBOX", State: "OPEN",
	})
	input := RecordPayPalDisputeActionInput{
		ExpectedVersion: items[0].Version, ActionKind: domain.DisputeActionCaseObserved,
		ExternalReference: "PP-RC-OBS-1", PublicRationale: "PayPal case was reviewed.",
		ObservedProviderStatus: domain.DisputeStatusResolved,
		ObservedOutcome:        domain.DisputeOutcomeSellerFavour,
		EvidenceSource:         domain.DisputeEvidencePayPalResolutionCenter,
		EvidenceHash:           "0x" + strings.Repeat("d", 64), ObservedAt: now,
	}
	result, err := service.RecordPayPalDisputeAction(
		context.Background(), "SANDBOX", items[0].ID, "operator-1", "idem-action-1", input,
	)
	if err != nil || result.Replay || result.Case.State != domain.DisputeResolved {
		t.Fatalf("record action result=%+v err=%v", result, err)
	}
	replay, err := service.RecordPayPalDisputeAction(
		context.Background(), "SANDBOX", items[0].ID, "operator-1", "idem-action-1", input,
	)
	if err != nil || !replay.Replay || replay.Action.ID != result.Action.ID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	input.PublicRationale = "Different request with same key."
	if _, err := service.RecordPayPalDisputeAction(
		context.Background(), "SANDBOX", items[0].ID, "operator-1", "idem-action-1", input,
	); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("changed replay error=%v", err)
	}
}
