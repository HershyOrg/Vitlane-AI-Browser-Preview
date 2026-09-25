package domain

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestManualDecisionRequiresCustomerVisibleRationaleAndEvidence(t *testing.T) {
	now := time.Date(2026, 8, 27, 1, 0, 0, 0, time.UTC)
	if err := ValidateDecision(
		DecisionWithinAuthorization,
		"Matches the approved product and price.", "private accounting note",
		"The listed variant and total match the authorization.",
		EvidenceMerchantPage, "0123456789abcdef", now,
	); err != nil {
		t.Fatalf("valid decision: %v", err)
	}
	if err := ValidateDecision(
		DecisionWithinAuthorization, "short", "", "same",
		EvidenceMerchantPage, "0123456789abcdef", now,
	); !errors.Is(err, ErrDecisionInvalid) {
		t.Fatalf("short public rationale err=%v", err)
	}
}

func TestCustomerRequestTypesAreNarrow(t *testing.T) {
	if err := ValidateCustomerRequest(
		RequestConsent, "Accept this new return condition?",
		ResponseBooleanConsent, nil, "The merchant now requires this condition.",
	); err != nil {
		t.Fatalf("valid consent: %v", err)
	}
	if err := ValidateCustomerRequest(
		RequestInformation, "Choose a substitute", ResponseSingleChoice,
		[]string{"Blue", "Blue"}, "The selected color is unavailable.",
	); !errors.Is(err, ErrRequestInvalid) {
		t.Fatalf("duplicate choices err=%v", err)
	}
	if err := ValidateCustomerRequest(
		RequestConsent, "Accept this new return condition?",
		ResponseBooleanConsent, nil, "short",
	); !errors.Is(err, ErrRequestInvalid) {
		t.Fatalf("short public context err=%v", err)
	}
}

func TestCustomerResponseMatchesDeclaredType(t *testing.T) {
	if err := ValidateCustomerResponse(
		ResponseSingleChoice, []string{"Blue", "Black"},
		json.RawMessage(`{"choice":"Black"}`),
	); err != nil {
		t.Fatalf("valid choice: %v", err)
	}
	if err := ValidateCustomerResponse(
		ResponseSingleChoice, []string{"Blue", "Black"},
		json.RawMessage(`{"choice":"Red"}`),
	); !errors.Is(err, ErrRequestResponseInvalid) {
		t.Fatalf("unknown choice err=%v", err)
	}
	if err := ValidateCustomerResponse(
		ResponseBooleanConsent, nil,
		json.RawMessage(`{"accepted":false}`),
	); err != nil {
		t.Fatalf("explicit decline is typed evidence: %v", err)
	}
}
