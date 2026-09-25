package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/infra/paypal"
)

// ensureAuthorized verifies the approved PayPal Order and creates one
// full-order authorization. It never captures customer funds.
func (s *Service) ensureAuthorized(
	ctx context.Context,
	binding domain.AccountBinding,
	instruction domain.PayableInstruction,
	payment domain.CustomerPayment,
	attempt domain.PayPalAttempt,
) (CheckoutView, error) {
	if attempt.PayPalOrderID == "" {
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	}
	if attempt.State == domain.AttemptAuthorizeCompleted {
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	}
	if !payment.MatchesInstruction(instruction) {
		return CheckoutView{}, domain.ErrInstructionMismatch
	}
	registration, err := s.providerFor(payment.ProviderEnvironment)
	if err != nil {
		return CheckoutView{}, domain.ErrRailUnavailable
	}
	provider, ok := registration.Client.(authorizationProviderClient)
	if !ok {
		return CheckoutView{}, domain.ErrAuthorizationBlocked
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.config.QueryTimeout)
	order, err := provider.GetOrder(queryCtx, attempt.PayPalOrderID)
	cancel()
	if err != nil {
		return CheckoutView{Payment: payment, Attempt: attempt,
			ApprovalURL: attempt.ApprovalURL, ReturnNonce: attempt.ReturnNonce}, nil
	}
	now := s.clock.Now()
	if len(order.Authorizations) > 0 || order.Status == "COMPLETED" {
		return s.adoptAuthorizationResult(ctx, binding, payment, attempt, order)
	}
	switch order.Status {
	case "APPROVED":
		if order.PayeeMerchant != binding.MerchantID {
			return CheckoutView{}, domain.ErrBindingMismatch
		}
		if order.AmountMinor != instruction.CustomerPayableMinor || order.Currency != "USD" {
			return CheckoutView{}, domain.ErrInstructionMismatch
		}
		if !now.Before(instruction.ExpiresAt) {
			if err := s.repository.RecordAttemptOutcome(
				ctx, attempt.ID, domain.AttemptSupersededBeforeAuthorize,
				payment.ID, domain.PaymentSuperseded, "", "",
				"INSTRUCTION_EXPIRED", now,
			); err != nil {
				return CheckoutView{}, err
			}
			return CheckoutView{}, domain.ErrInstructionExpired
		}
		if err := s.repository.MarkPayerApproved(ctx, attempt.ID, payment.ID, now); err != nil {
			return CheckoutView{}, err
		}
		if !registration.CaptureEnabled {
			return CheckoutView{}, domain.ErrAuthorizationBlocked
		}
		return s.submitAuthorization(ctx, binding, payment, attempt, provider)
	case "CREATED", "PAYER_ACTION_REQUIRED":
		// GET is authoritative for the current approval link. Persist it before
		// returning so an earlier malformed/empty create response cannot strand
		// the checkout on every subsequent resume.
		if order.PayerActionURL != attempt.ApprovalURL {
			if err := s.repository.RecordOrderCreated(
				ctx, attempt.ID, payment.ID, order.ID, order.PayerActionURL, now,
			); err != nil {
				return CheckoutView{}, err
			}
			attempt.ApprovalURL = order.PayerActionURL
		}
		return CheckoutView{Payment: payment, Attempt: attempt,
			ApprovalURL: order.PayerActionURL, ReturnNonce: attempt.ReturnNonce}, nil
	case "VOIDED", "EXPIRED":
		if err := s.repository.RecordAttemptOutcome(
			ctx, attempt.ID, domain.AttemptExpired, payment.ID, domain.PaymentFailed,
			"", "", "PAYPAL_ORDER_"+order.Status, now,
		); err != nil {
			return CheckoutView{}, err
		}
		payment.State, attempt.State = domain.PaymentFailed, domain.AttemptExpired
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	default:
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	}
}

func (s *Service) submitAuthorization(
	ctx context.Context,
	binding domain.AccountBinding,
	payment domain.CustomerPayment,
	attempt domain.PayPalAttempt,
	provider authorizationProviderClient,
) (CheckoutView, error) {
	repository, ok := s.repository.(authorizationRepository)
	if !ok {
		return CheckoutView{}, domain.ErrAuthorizationBlocked
	}
	operation := s.newOperation(
		domain.OperationPayPalAuthorize, "PAYPAL_ATTEMPT", attempt.ID,
		fmt.Sprintf("paypal:authorize:%s:1", attempt.ID), payment.AmountMinor,
	)
	operation, created, err := repository.PrepareAuthorizationOperation(
		ctx, attempt.ID, operation, s.clock.Now(),
	)
	if err != nil {
		return CheckoutView{}, err
	}
	if !created && operation.State != domain.OperationPrepared &&
		operation.State != domain.OperationUnknown {
		return s.reconcileAuthorization(ctx, binding, payment, attempt)
	}
	if err := s.requireLiveMoney(ctx, payment.ProviderEnvironment); err != nil {
		return CheckoutView{}, err
	}
	now := s.clock.Now()
	if err := s.repository.MarkOperationSent(
		ctx, operation, attempt.ID, domain.AttemptAuthorizeSubmitted,
		payment.ID, domain.PaymentProcessing, now,
		now.Add(s.config.AuthorizeRetryWindow),
	); err != nil {
		if errors.Is(err, domain.ErrIdempotencyExpired) {
			// The POST may have created an authorization hold even though no
			// authorization ID reached Vitlane. The provider request may no longer
			// be resent, but the known PayPal Order remains a durable GET anchor.
			// Keep this payment open and reconciliation-only so another payment
			// cannot create a second hold.
			if recordErr := s.repository.RecordAttemptOutcome(
				ctx, attempt.ID, domain.AttemptAuthorizeOutcomeUnknown,
				payment.ID, domain.PaymentOutcomeUnknown,
				domain.OperationPayPalAuthorize, domain.OperationUnknown,
				"AUTHORIZE_IDEMPOTENCY_EXPIRED_RECONCILIATION_ONLY", now,
			); recordErr != nil {
				return CheckoutView{}, recordErr
			}
			payment.State = domain.PaymentOutcomeUnknown
			attempt.State = domain.AttemptAuthorizeOutcomeUnknown
			return CheckoutView{Payment: payment, Attempt: attempt}, nil
		}
		if errors.Is(err, domain.ErrConflict) {
			// A concurrent callback owns the provider POST. Reconciliation is
			// read-only and can observe the authorization without a second write.
			payment.State = domain.PaymentProcessing
			attempt.State = domain.AttemptAuthorizeSubmitted
			return s.reconcileAuthorization(ctx, binding, payment, attempt)
		}
		return CheckoutView{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.config.CaptureTimeout)
	_, err = provider.AuthorizeOrder(callCtx, attempt.PayPalOrderID, operation.IdempotencyKey)
	cancel()
	now = s.clock.Now()
	if err != nil {
		state := domain.AttemptAuthorizeFailed
		paymentState := domain.PaymentFailed
		operationState := domain.OperationFailed
		reason := providerReason(err)
		if errors.Is(err, paypal.ErrOutcomeUnknown) ||
			errors.Is(err, paypal.ErrNonconformingResponse) {
			state, paymentState = domain.AttemptAuthorizeOutcomeUnknown, domain.PaymentOutcomeUnknown
			operationState, reason = domain.OperationUnknown, "AUTHORIZE_UNKNOWN"
			if errors.Is(err, paypal.ErrNonconformingResponse) {
				reason = "AUTHORIZE_RESPONSE_NONCONFORMING"
			}
		}
		if recordErr := s.repository.RecordAttemptOutcome(
			ctx, attempt.ID, state, payment.ID, paymentState,
			domain.OperationPayPalAuthorize, operationState, reason, now,
		); recordErr != nil {
			return CheckoutView{}, recordErr
		}
		payment.State, attempt.State = paymentState, state
		if errors.Is(err, paypal.ErrOutcomeUnknown) ||
			errors.Is(err, paypal.ErrNonconformingResponse) {
			return CheckoutView{Payment: payment, Attempt: attempt}, nil
		}
		return CheckoutView{}, err
	}
	payment.State, attempt.State = domain.PaymentProcessing, domain.AttemptAuthorizeSubmitted
	return s.reconcileAuthorization(ctx, binding, payment, attempt)
}

func (s *Service) reconcileAuthorization(
	ctx context.Context,
	binding domain.AccountBinding,
	payment domain.CustomerPayment,
	attempt domain.PayPalAttempt,
) (CheckoutView, error) {
	if attempt.PayPalOrderID == "" {
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	}
	registration, err := s.providerFor(payment.ProviderEnvironment)
	if err != nil {
		return CheckoutView{}, err
	}
	provider, ok := registration.Client.(authorizationProviderClient)
	if !ok {
		return CheckoutView{}, domain.ErrAuthorizationBlocked
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.config.QueryTimeout)
	order, err := provider.GetAuthorizedOrder(queryCtx, attempt.PayPalOrderID)
	cancel()
	if err != nil {
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	}
	return s.adoptAuthorizationResult(ctx, binding, payment, attempt, order)
}

func (s *Service) adoptAuthorizationResult(
	ctx context.Context,
	binding domain.AccountBinding,
	payment domain.CustomerPayment,
	attempt domain.PayPalAttempt,
	order paypal.Order,
) (CheckoutView, error) {
	if order.PayeeMerchant != binding.MerchantID || order.Currency != "USD" ||
		order.AmountMinor != payment.AmountMinor || len(order.Authorizations) != 1 {
		return CheckoutView{}, domain.ErrInstructionMismatch
	}
	providerAuthorization := order.Authorizations[0]
	now := s.clock.Now()
	switch providerAuthorization.Status {
	case "CREATED":
		if providerAuthorization.AmountMinor != payment.AmountMinor ||
			providerAuthorization.Currency != "USD" {
			return CheckoutView{}, domain.ErrInstructionMismatch
		}
		authorizedAt := now
		if value := strings.TrimSpace(providerAuthorization.CreateTime); value != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
				authorizedAt = parsed.UTC()
			}
		}
		repository, ok := s.repository.(authorizationRepository)
		if !ok {
			return CheckoutView{}, domain.ErrAuthorizationBlocked
		}
		authorization := domain.PayPalAuthorization{
			ID: s.ids.NewID(), CustomerPaymentID: payment.ID,
			AgencyOrderID: payment.AgencyOrderID, PayPalAttemptID: attempt.ID,
			ProviderEnvironment: payment.ProviderEnvironment,
			PayPalOrderID:       order.ID, PayeeMerchantID: binding.MerchantID,
			PayPalAuthorizationID: providerAuthorization.ID,
			AmountMinor:           providerAuthorization.AmountMinor, Currency: "USD",
			State: domain.AuthorizationAuthorized, Version: 1,
			AuthorizedAt: &authorizedAt, CreatedAt: now, UpdatedAt: now,
		}
		instruction, err := s.repository.GetPayableInstruction(ctx, payment.UserID, payment.AgencyOrderID)
		if err != nil {
			return CheckoutView{}, err
		}
		if !payment.MatchesInstruction(instruction) || s.instructions == nil {
			return CheckoutView{}, domain.ErrInstructionMismatch
		}
		claim := procmsg.InstructionClaim{AgencyOrderID: payment.AgencyOrderID, Rail: payment.Rail, ReferenceID: payment.ID, SnapshotHash: instruction.SnapshotHash, ValidAt: authorizedAt}
		if err = s.instructions.ConfirmInstruction(ctx, claim); err != nil {
			if errors.Is(err, procmsg.ErrInstructionPending) {
				payment.State = domain.PaymentProcessing
				payment.LastReasonCode = procmsg.ErrInstructionPending.Error()
				return CheckoutView{Payment: payment, Attempt: attempt}, nil
			}
			return CheckoutView{}, domain.ErrInstructionNotConsumable
		}
		err = s.transactor.WithinTransaction(ctx, func(tx context.Context) error {
			_, recordErr := repository.RecordAuthorizationCompleted(tx, attempt.ID, payment.ID, authorization, now)
			return recordErr
		})
		if err != nil {
			return CheckoutView{}, err
		}
		payment.State, attempt.State = domain.PaymentAuthorized, domain.AttemptAuthorizeCompleted
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	case "PARTIALLY_CAPTURED", "CAPTURED":
		// No capture can legitimately precede the durable per-MO funding
		// positions. Adopting this authorization would make a later Procurement
		// capture charge the customer twice, so preserve it for reconciliation
		// without opening any merchant effect.
		if err := s.repository.RecordAttemptOutcome(
			ctx, attempt.ID, domain.AttemptAuthorizeOutcomeUnknown, payment.ID,
			domain.PaymentOutcomeUnknown, domain.OperationPayPalAuthorize,
			domain.OperationUnknown, "AUTHORIZATION_ALREADY_CAPTURED", now,
		); err != nil {
			return CheckoutView{}, err
		}
		payment.State = domain.PaymentOutcomeUnknown
		attempt.State = domain.AttemptAuthorizeOutcomeUnknown
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	case "PENDING":
		if err := s.repository.RecordAttemptOutcome(
			ctx, attempt.ID, domain.AttemptAuthorizePending, payment.ID,
			domain.PaymentProcessing, domain.OperationPayPalAuthorize,
			domain.OperationSucceeded, "AUTHORIZE_PENDING", now,
		); err != nil {
			return CheckoutView{}, err
		}
		payment.State, attempt.State = domain.PaymentProcessing, domain.AttemptAuthorizePending
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	case "DENIED":
		if err := s.repository.RecordAttemptOutcome(
			ctx, attempt.ID, domain.AttemptAuthorizeDeclined, payment.ID,
			domain.PaymentFailed, domain.OperationPayPalAuthorize,
			domain.OperationFailed, "AUTHORIZE_DENIED", now,
		); err != nil {
			return CheckoutView{}, err
		}
		payment.State, attempt.State = domain.PaymentFailed, domain.AttemptAuthorizeDeclined
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	case "VOIDED", "EXPIRED":
		if err := s.repository.RecordAttemptOutcome(
			ctx, attempt.ID, domain.AttemptAuthorizeFailed, payment.ID,
			domain.PaymentFailed, domain.OperationPayPalAuthorize,
			domain.OperationFailed, "AUTHORIZE_"+providerAuthorization.Status, now,
		); err != nil {
			return CheckoutView{}, err
		}
		payment.State, attempt.State = domain.PaymentFailed, domain.AttemptAuthorizeFailed
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	default:
		return CheckoutView{Payment: payment, Attempt: attempt}, nil
	}
}
