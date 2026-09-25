package domain

import (
	"errors"
	"testing"
	"time"
)

func TestPaymentStateForCoversAttemptStates(t *testing.T) {
	cases := map[AttemptState]PaymentState{
		AttemptOrderPrepared:             PaymentProcessing,
		AttemptPayerActionRequired:       PaymentActionRequired,
		AttemptCancelledByUser:           PaymentActionRequired,
		AttemptAuthorizeSubmitted:        PaymentProcessing,
		AttemptAuthorizePending:          PaymentProcessing,
		AttemptAuthorizeOutcomeUnknown:   PaymentOutcomeUnknown,
		AttemptAuthorizeCompleted:        PaymentAuthorized,
		AttemptAuthorizeDeclined:         PaymentFailed,
		AttemptSupersededBeforeAuthorize: PaymentSuperseded,
		AttemptAbandonedBeforeAuthorize:  PaymentAbandoned,
	}
	for attempt, expected := range cases {
		if got := PaymentStateFor(attempt); got != expected {
			t.Fatalf("PaymentStateFor(%s) = %s, want %s", attempt, got, expected)
		}
	}
}

func TestPayableInstructionValidation(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	valid := PayableInstruction{
		Rail: "PAYPAL", ProviderEnvironment: "SANDBOX", Asset: "USD",
		EconomicEffect: "NO_REAL_VALUE", MerchantExecutionMode: "SIMULATED_NO_EFFECT",
		ExecutionProfileHash: PayPalSandboxExecutionProfileHash,
		CustomerPayableMinor: 10600, Currency: "USD",
		ExpiresAt: now.Add(time.Hour),
	}
	if err := valid.ValidateForPayPalSandbox(now); err != nil {
		t.Fatalf("valid instruction rejected: %v", err)
	}
	giwa := valid
	giwa.Rail = "GIWA"
	if err := giwa.ValidateForPayPalSandbox(now); !errors.Is(err, ErrInstructionMismatch) {
		t.Fatalf("GIWA instruction must mismatch, got %v", err)
	}
	expired := valid
	expired.ExpiresAt = now
	if err := expired.ValidateForPayPalSandbox(now); !errors.Is(err, ErrInstructionExpired) {
		t.Fatalf("expired instruction must fail, got %v", err)
	}
	live := PayableInstruction{
		Rail: "PAYPAL", ProviderEnvironment: "LIVE", Asset: "USD",
		EconomicEffect: "REAL_MONEY", MerchantExecutionMode: "LIVE_MERCHANT_EFFECT",
		ExecutionProfileHash: PayPalLiveExecutionProfileHash,
		CustomerPayableMinor: 10600, Currency: "USD", ExpiresAt: now.Add(time.Hour),
	}
	if err := live.ValidateForPayPal("LIVE", now); err != nil {
		t.Fatalf("valid dormant Live instruction rejected: %v", err)
	}
	if err := live.ValidateForPayPalSandbox(now); !errors.Is(err, ErrInstructionMismatch) {
		t.Fatalf("Live instruction must not cross the Sandbox adapter: %v", err)
	}
	live.ExecutionProfileHash = PayPalSandboxExecutionProfileHash
	if err := live.ValidateForPayPal("LIVE", now); !errors.Is(err, ErrInstructionMismatch) {
		t.Fatalf("mixed Live/Sandbox profile must mismatch: %v", err)
	}
}

func TestReceiptExecutionProfileValidation(t *testing.T) {
	receipt := FundsReceipt{
		Kind: "PAYPAL_CAPTURE", ProviderEnvironment: "LIVE",
		ExecutionProfileHash: PayPalLiveExecutionProfileHash,
		CaptureID:            "capture-live", Accepted: true,
	}
	if err := receipt.ValidatePayPalExecutionProfile(); err != nil {
		t.Fatalf("valid Live receipt rejected: %v", err)
	}
	receipt.ExecutionProfileHash = PayPalSandboxExecutionProfileHash
	if err := receipt.ValidatePayPalExecutionProfile(); !errors.Is(err, ErrInstructionMismatch) {
		t.Fatalf("mixed receipt profile must mismatch: %v", err)
	}
}
