package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type resumeRepository struct {
	payment       settlementdomain.Payment
	authorization settlementdomain.AuthorizationRecord
}

func (r resumeRepository) GetAuthorizationContext(_ context.Context, _, _ string) (settlementapp.AuthorizationContext, error) {
	return settlementapp.AuthorizationContext{}, nil
}

func (r resumeRepository) GetAuthorization(_ context.Context, _, _ string) (settlementdomain.AuthorizationRecord, error) {
	return r.authorization, nil
}

func (r resumeRepository) CreateAuthorization(_ context.Context, _ settlementdomain.AuthorizationRecord, _ settlementdomain.Payment) error {
	return nil
}

func (r resumeRepository) RecordSubmittedTransaction(_ context.Context, _, _, _ string, _ time.Time) error {
	return nil
}

func (r resumeRepository) RecordWalletTransaction(_ context.Context, _, _, _, _ string, _ time.Time) error {
	return nil
}

func (r resumeRepository) GetRefundIntent(_ context.Context, _, _ string, _ time.Time) (settlementdomain.RefundIntent, error) {
	return settlementdomain.RefundIntent{}, nil
}

func (r resumeRepository) RecordRefundTransaction(_ context.Context, _, _, _ string, _ time.Time) error {
	return nil
}

func (r resumeRepository) GetPayment(_ context.Context, _, _ string) (settlementdomain.Payment, error) {
	return r.payment, nil
}

func TestAgencyOrderSettlementReturnsPaymentAndAuthorizationForResume(t *testing.T) {
	repository := resumeRepository{
		payment: settlementdomain.Payment{
			ID:            "payment-1",
			AgencyOrderID: "order-1",
		},
		authorization: settlementdomain.AuthorizationRecord{
			ID:            "authorization-1",
			AgencyOrderID: "order-1",
		},
	}
	service := settlementapp.NewService(
		repository, nil, nil, nil, settlementdomain.SettlementConfig{},
	)
	handler := NewHandler(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agencyOrder/order-1/settlement", nil)
	request.SetPathValue("agencyOrderId", "order-1")
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "user-1"))
	recorder := httptest.NewRecorder()

	handler.GetAgencyOrderPayment(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Payment       settlementdomain.Payment             `json:"payment"`
		Authorization settlementdomain.AuthorizationRecord `json:"authorization"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Payment.ID != "payment-1" || response.Authorization.ID != "authorization-1" {
		t.Fatalf("response=%+v", response)
	}
	if response.Payment.AgencyOrderID != "order-1" ||
		response.Authorization.AgencyOrderID != "order-1" {
		t.Fatalf("AgencyOrder identity missing: response=%+v", response)
	}
}

func TestSettlementQuoteStaleIsConflict(t *testing.T) {
	recorder := httptest.NewRecorder()

	writeError(recorder, settlementdomain.ErrAuthorizationInstructionStale)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != "SETTLEMENT_INSTRUCTION_STALE" {
		t.Fatalf("code=%q body=%s", response.Error.Code, recorder.Body.String())
	}
	if response.Error.Message == "" {
		t.Fatalf("missing user guidance: body=%s", recorder.Body.String())
	}
}

func TestSettlementWalletOwnershipRejectionIsClientError(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeError(recorder, settlementdomain.ErrWalletOwnershipRequired)
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusUnprocessableEntity || response.Error.Code != "SETTLEMENT_WALLET_OWNERSHIP_REQUIRED" {
		t.Fatalf("status=%d code=%s", recorder.Code, response.Error.Code)
	}
}
