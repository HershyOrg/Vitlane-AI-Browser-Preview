package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
)

func TestWalletAndKYCErrorsExplainTheRequiredNextStep(t *testing.T) {
	tests := []struct {
		err     error
		status  int
		message string
		retry   string
	}{
		{
			err: &accountapp.RateLimitError{
				Policy:     accountapp.RatePolicyWalletRegistrationCreate,
				RetryAfter: 42 * time.Second,
			},
			status:  http.StatusTooManyRequests,
			message: "지갑 인증 요청이 너무 많습니다. 잠시 후 다시 시도해 주세요.",
			retry:   "42",
		},
		{
			err:     accountdomain.ErrWalletNotRegistered,
			status:  http.StatusUnprocessableEntity,
			message: "이 계정에 등록된 지갑만 사용할 수 있습니다.",
		},
		{
			err:     accountdomain.ErrWalletAlreadyRegistered,
			status:  http.StatusConflict,
			message: "현재 결제 지갑이 이미 등록되어 있습니다. 다른 지갑으로 바꾸려면 현재 지갑을 먼저 등록 해제해 주세요.",
		},
		{
			err:     accountdomain.ErrWalletOwnershipProofNotFresh,
			status:  http.StatusUnprocessableEntity,
			message: "KYC를 시작하려면 최근 10분 이내의 지갑 서명 인증이 필요합니다.",
		},
		{
			err:     accountdomain.ErrKYCVerificationState,
			status:  http.StatusConflict,
			message: "이 KYC 케이스는 현재 상태에서 다시 확인할 수 없습니다.",
		},
		{
			err:     accountdomain.ErrKYCProviderUnavailable,
			status:  http.StatusServiceUnavailable,
			message: "MockDojang TEST KYC 응답을 받지 못했습니다. 같은 작업을 다시 시도해 주세요.",
		},
		{
			err:     accountdomain.ErrKYCCredentialInvalid,
			status:  http.StatusUnprocessableEntity,
			message: "MockDojang의 credential 또는 evidence 결과가 유효하지 않습니다.",
		},
	}

	for _, test := range tests {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/wallets", nil)
		writeWalletError(response, request, test.err)
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Message != test.message {
			t.Fatalf("message=%q want=%q", body.Error.Message, test.message)
		}
		if response.Code != test.status {
			t.Fatalf("status=%d want=%d", response.Code, test.status)
		}
		if retry := response.Header().Get("Retry-After"); retry != test.retry {
			t.Fatalf("Retry-After=%q want=%q", retry, test.retry)
		}
	}
}

func TestWalletProjectionJSONUsesTypedNextActionsAndSimulatedDisclosure(
	t *testing.T,
) {
	retryable := false
	projection := accountapp.WalletProjection{
		Ownership: accountdomain.WalletOwnershipProjection{
			Status: accountdomain.WalletOwnershipReauthRequired,
			NextAction: accountdomain.WalletNextAction{
				Kind: accountdomain.WalletNextActionReauthenticate,
			},
		},
		KYC: accountapp.WalletKYCProjection{
			Eligibility:    accountapp.KYCEligibilityPending,
			ProviderKind:   accountdomain.KYCProviderMockDojang,
			ExternalEffect: accountdomain.KYCEffectSimulated,
			Disclosure:     accountapp.MockDojangDisclosure,
			FailureCode:    "INVALID_PROVIDER_RESULT",
			Retryable:      &retryable,
			NextAction: accountdomain.WalletNextAction{
				Kind: accountdomain.WalletNextActionContactSupport,
			},
		},
	}
	body, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Ownership struct {
			NextAction struct {
				Kind string `json:"kind"`
			} `json:"nextAction"`
		} `json:"ownership"`
		KYC struct {
			ExternalEffect string `json:"externalEffect"`
			Disclosure     string `json:"disclosure"`
			FailureCode    string `json:"failureCode"`
			Retryable      *bool  `json:"retryable"`
			NextAction     struct {
				Kind string `json:"kind"`
			} `json:"nextAction"`
		} `json:"kyc"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Ownership.NextAction.Kind != "REAUTHENTICATE" ||
		decoded.KYC.NextAction.Kind != "CONTACT_SUPPORT" ||
		decoded.KYC.ExternalEffect != "SIMULATED" ||
		decoded.KYC.Disclosure != accountapp.MockDojangDisclosure ||
		decoded.KYC.FailureCode != "INVALID_PROVIDER_RESULT" ||
		decoded.KYC.Retryable == nil || *decoded.KYC.Retryable {
		t.Fatalf("projection JSON mismatch: %s", body)
	}
}
