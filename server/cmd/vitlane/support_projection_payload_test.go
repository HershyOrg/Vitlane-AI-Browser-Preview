package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	procurementdomain "github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

func TestRefundRequestSupportPayloadRemainsStableAfterReview(t *testing.T) {
	requested := agencydomain.RefundRequest{
		ID: "refund-request-1", AgencyOrderID: "order-1",
		MerchantOrderID: "merchant-order-1", AllocationID: "allocation-1",
		RequestedGrossAmount: agencydomain.Money{AmountMinor: 1234, Currency: "USD"},
		State:                agencydomain.RefundRequestRequested,
		ReasonCode:           "OTHER", PublicRationale: "Customer explanation",
		CreatedAt: time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC),
	}
	resolved := requested
	resolved.State = agencydomain.RefundRequestResolved
	resolved.Decision = agencydomain.RefundDecisionApproved
	resolved.DecisionPublicRationale = "Approved after reviewing the whole MO"
	if !bytes.Equal(
		refundRequestSupportPayload(requested),
		refundRequestSupportPayload(resolved),
	) {
		t.Fatal("request card payload changed after refund review")
	}
}

func TestProcurementRequestSupportPayloadRemainsStableAfterResolution(t *testing.T) {
	requested := procurementdomain.CustomerRequest{
		ID: "request-1", AgencyOrderID: "order-1",
		Kind:   procurementdomain.RequestConsent,
		Prompt: "Continue?", PublicContext: "Material condition changed",
		ResponseType:    procurementdomain.ResponseBooleanConsent,
		ResponseOptions: []string{}, State: procurementdomain.RequestPending,
		DueAt: time.Date(2026, 9, 3, 1, 2, 3, 0, time.UTC), Version: 1,
	}
	resolved := requested
	resolved.State = procurementdomain.RequestAnswered
	resolved.Response = json.RawMessage(`{"accepted":true}`)
	resolved.Version = 2
	if !bytes.Equal(
		procurementRequestSupportPayload(requested),
		procurementRequestSupportPayload(resolved),
	) {
		t.Fatal("request card payload changed after customer resolution")
	}
}
