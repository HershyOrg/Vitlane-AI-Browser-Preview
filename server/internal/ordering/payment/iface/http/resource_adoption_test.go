package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type fakePayPalResourceAdoptionService struct {
	reauthorizationInput paymentapp.AdoptMOReauthorizationInput
	merchantOrderID      string
	actorUserID          string
	idempotencyKey       string
	reauthorization      paymentapp.AdoptMOReauthorizationResult
	reauthorizationErr   error
	refundInput          paymentapp.AdoptPayPalMORefundInput
	refund               paymentapp.AdoptPayPalMORefundResult
	refundErr            error
}

func (f *fakePayPalResourceAdoptionService) AdoptMOReauthorization(
	_ context.Context,
	merchantOrderID, actorUserID, idempotencyKey string,
	input paymentapp.AdoptMOReauthorizationInput,
) (paymentapp.AdoptMOReauthorizationResult, error) {
	f.merchantOrderID = merchantOrderID
	f.actorUserID = actorUserID
	f.idempotencyKey = idempotencyKey
	f.reauthorizationInput = input
	return f.reauthorization, f.reauthorizationErr
}

func (f *fakePayPalResourceAdoptionService) AdoptPayPalMORefund(
	_ context.Context,
	input paymentapp.AdoptPayPalMORefundInput,
) (paymentapp.AdoptPayPalMORefundResult, error) {
	f.refundInput = input
	return f.refund, f.refundErr
}

func authenticatedAdoptionRequest(
	method, target string,
	body any,
) *http.Request {
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(method, target, bytes.NewReader(encoded))
	return request.WithContext(sharedapp.WithAuthenticatedUserID(
		request.Context(), "11111111-1111-4111-8111-111111111111",
	))
}

func TestAdoptMOReauthorizationPassesExplicitEvidenceToGETOnlyService(t *testing.T) {
	observedAt := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	fake := &fakePayPalResourceAdoptionService{
		reauthorization: paymentapp.AdoptMOReauthorizationResult{
			Adoption: paymentapp.PayPalReauthorizationAdoption{
				ID: "adoption-1", OperationState: domain.OperationSucceeded,
			},
		},
	}
	handler := &Handler{adoption: fake}
	request := authenticatedAdoptionRequest(http.MethodPost, "/reauthorization-adoptions", map[string]any{
		"providerAuthorizationId": "AUTHORIZATION-NEW-1",
		"evidenceSource":          "PAYPAL_DASHBOARD",
		"evidenceHash":            "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"internalNote":            "PayPal activity review",
		"observedAt":              observedAt,
	})
	request.SetPathValue("merchantOrderId", "22222222-2222-4222-8222-222222222222")
	request.Header.Set("Idempotency-Key", "paypal-adoption-request-1")
	response := httptest.NewRecorder()

	handler.AdoptMOReauthorization(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.merchantOrderID != "22222222-2222-4222-8222-222222222222" ||
		fake.actorUserID != "11111111-1111-4111-8111-111111111111" ||
		fake.idempotencyKey != "paypal-adoption-request-1" ||
		fake.reauthorizationInput.ProviderAuthorizationID != "AUTHORIZATION-NEW-1" ||
		fake.reauthorizationInput.InternalNote != "PayPal activity review" ||
		!fake.reauthorizationInput.ObservedAt.Equal(observedAt) {
		t.Fatalf("captured reauthorization call=%+v actor=%q key=%q",
			fake.reauthorizationInput, fake.actorUserID, fake.idempotencyKey)
	}
	if response.Header().Get("Cache-Control") != "no-store" ||
		!bytes.Contains(response.Body.Bytes(), []byte("vitlane.paypal-reauthorization-adoption.v1")) {
		t.Fatalf("headers=%v body=%s", response.Header(), response.Body.String())
	}
}

func TestAdoptPayPalMORefundReturnsPersistedPendingObservation(t *testing.T) {
	observedAt := time.Date(2026, 8, 27, 8, 30, 0, 0, time.UTC)
	fake := &fakePayPalResourceAdoptionService{
		refund: paymentapp.AdoptPayPalMORefundResult{
			Adoption: domain.PayPalRefundAdoption{
				ID: "adoption-2", ProviderStatus: "PENDING",
			},
		},
		refundErr: domain.ErrCompensationOutcomeUnknown,
	}
	handler := &Handler{adoption: fake}
	request := authenticatedAdoptionRequest(http.MethodPost, "/refund-adoptions", map[string]any{
		"providerRefundId": "REFUND-EXISTING-1",
		"publicRationale":  "PayPal Dashboard shows the original refund as pending.",
		"evidenceSource":   "PAYPAL_DASHBOARD",
		"evidenceHash":     "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"observedAt":       observedAt,
	})
	request.SetPathValue("compensationId", "33333333-3333-4333-8333-333333333333")
	response := httptest.NewRecorder()

	handler.AdoptPayPalMORefund(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.refundInput.CompensationID != "33333333-3333-4333-8333-333333333333" ||
		fake.refundInput.ProviderRefundID != "REFUND-EXISTING-1" ||
		fake.refundInput.OperatorUserID != "11111111-1111-4111-8111-111111111111" ||
		fake.refundInput.PublicRationale != "PayPal Dashboard shows the original refund as pending." ||
		fake.refundInput.EvidenceSource != domain.PayPalRefundAdoptionEvidenceDashboard ||
		!fake.refundInput.ObservedAt.Equal(observedAt) {
		t.Fatalf("captured refund call=%+v", fake.refundInput)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte("\"providerStatus\":\"PENDING\"")) {
		t.Fatalf("pending adoption result was lost: %s", response.Body.String())
	}
}

func TestPayPalResourceAdoptionMismatchIsStableUnprocessableEntity(t *testing.T) {
	fake := &fakePayPalResourceAdoptionService{
		refundErr: domain.ErrPayPalRefundAdoptionMismatch,
	}
	handler := &Handler{adoption: fake}
	request := authenticatedAdoptionRequest(http.MethodPost, "/refund-adoptions", map[string]any{})
	request.SetPathValue("compensationId", "compensation-1")
	response := httptest.NewRecorder()

	handler.AdoptPayPalMORefund(response, request)

	if response.Code != http.StatusUnprocessableEntity ||
		!bytes.Contains(response.Body.Bytes(), []byte("PAYPAL_RESOURCE_ADOPTION_MISMATCH")) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPayPalResourceAdoptionRequiresAuthentication(t *testing.T) {
	handler := &Handler{adoption: &fakePayPalResourceAdoptionService{
		refundErr: errors.New("must not be called"),
	}}
	request := httptest.NewRequest(http.MethodPost, "/refund-adoptions", bytes.NewReader([]byte(`{}`)))
	response := httptest.NewRecorder()

	handler.AdoptPayPalMORefund(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
