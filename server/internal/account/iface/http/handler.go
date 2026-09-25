package http

import (
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type Handler struct {
	preferences        *accountapp.PreferencesService
	service            *accountapp.Service
	walletVerification *accountapp.WalletVerificationService
	kyc                *accountapp.KYCService
	shipping           *accountapp.ShippingService
	trustedProxies     []netip.Prefix
}

func (h *Handler) EnablePreferences(service *accountapp.PreferencesService) {
	h.preferences = service
}

func NewHandler(service *accountapp.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) EnableTrustedProxies(prefixes []netip.Prefix) {
	h.trustedProxies = append([]netip.Prefix(nil), prefixes...)
}

func (h *Handler) EnableWalletVerification(service *accountapp.WalletVerificationService) {
	h.walletVerification = service
}

func (h *Handler) EnableKYC(service *accountapp.KYCService) {
	h.kyc = service
}

func (h *Handler) EnableShipping(service *accountapp.ShippingService) {
	h.shipping = service
}

func (h *Handler) CreateDevelopmentUser(w http.ResponseWriter, r *http.Request) {
	user, err := h.service.CreateDevelopmentUser(r.Context())
	if err != nil {
		if httpapi.WriteFaultIfClassified(w, r, err, "개발 사용자를 만들지 못했습니다.") {
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "개발 사용자를 만들지 못했습니다.")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (h *Handler) AccountOverview(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	overview, err := h.service.GetOverview(r.Context(), userID)
	if err != nil {
		if httpapi.WriteFaultIfClassified(w, r, err, "계정 정보를 불러오지 못했습니다.") {
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "계정 정보를 불러오지 못했습니다.")
		return
	}
	shippingProfiles := []accountdomain.ShippingProfile{}
	if h.shipping != nil {
		shippingProfiles, err = h.shipping.ListProfiles(r.Context(), userID)
		if err != nil {
			if httpapi.WriteFaultIfClassified(w, r, err, "배송 정보를 불러오지 못했습니다.") {
				return
			}
			httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "배송 정보를 불러오지 못했습니다.")
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"account": struct {
			accountapp.AccountOverview
			ShippingProfiles []accountdomain.ShippingProfile `json:"shippingProfiles"`
		}{AccountOverview: overview, ShippingProfiles: shippingProfiles},
	})
}

type saveShippingProfileRequest struct {
	Label         string `json:"label"`
	RecipientName string `json:"recipientName"`
	AddressLine1  string `json:"addressLine1"`
	AddressLine2  string `json:"addressLine2"`
	City          string `json:"city"`
	Region        string `json:"region"`
	PostalCode    string `json:"postalCode"`
	Country       string `json:"country"`
	Phone         string `json:"phone"`
}

func (h *Handler) SaveDefaultShippingProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.shipping == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "SHIPPING_DISABLED", "배송정보 저장이 비활성화되어 있습니다.")
		return
	}
	var request saveShippingProfileRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	profile, err := h.shipping.SaveDefaultProfile(r.Context(), accountapp.SaveShippingProfileInput{
		UserID: userID, Label: request.Label,
		Address: accountdomain.ShippingAddress{
			RecipientName: request.RecipientName,
			AddressLine1:  request.AddressLine1, AddressLine2: request.AddressLine2,
			City: request.City, Region: request.Region, PostalCode: request.PostalCode,
			Country: request.Country, Phone: request.Phone,
		},
	})
	if err != nil {
		if errors.Is(err, accountdomain.ErrShippingAddressInvalid) {
			httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "실제 배송에 필요한 주소 항목을 확인해 주세요.")
			return
		}
		if httpapi.WriteFaultIfClassified(w, r, err, "배송정보를 암호화해 저장하지 못했습니다.") {
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "배송정보를 암호화해 저장하지 못했습니다.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{"shippingProfile": profile})
}

func (h *Handler) RetireShippingProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.shipping == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "SHIPPING_DISABLED", "배송정보 저장이 비활성화되어 있습니다.")
		return
	}
	if err := h.shipping.RetireProfile(r.Context(), userID, r.PathValue("profileId")); err != nil {
		if errors.Is(err, accountdomain.ErrShippingProfileMissing) {
			httpapi.WriteError(w, http.StatusNotFound, err.Error(), "배송정보를 찾을 수 없습니다.")
			return
		}
		if httpapi.WriteFaultIfClassified(w, r, err, "배송정보를 삭제하지 못했습니다.") {
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "배송정보를 삭제하지 못했습니다.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) RevealShippingProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.shipping == nil {
		httpapi.WriteError(
			w, http.StatusServiceUnavailable, "SHIPPING_DISABLED",
			"배송정보 조회가 비활성화되어 있습니다.",
		)
		return
	}
	address, err := h.shipping.RevealProfile(
		r.Context(), userID, r.PathValue("profileId"),
	)
	if err != nil {
		writeOwnerShippingError(w, err)
		return
	}
	writePrivateAddress(w, address)
}

func writePrivateAddress(w http.ResponseWriter, address accountdomain.ShippingAddress) {
	w.Header().Set("Cache-Control", "private, no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"address": address})
}

func writeOwnerShippingError(w http.ResponseWriter, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, nil, err, "배송정보를 불러오지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, accountdomain.ErrShippingProfileMissing):
		httpapi.WriteError(
			w, http.StatusNotFound, err.Error(),
			"본인 소유의 배송정보를 찾을 수 없습니다.",
		)
	case errors.Is(err, accountdomain.ErrShippingSnapshotPurged):
		httpapi.WriteError(
			w, http.StatusGone, err.Error(),
			"보존 기간이 지난 배송정보 원문은 폐기되었습니다.",
		)
	default:
		httpapi.WriteError(
			w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"배송정보를 복호화하지 못했습니다.",
		)
	}
}

func (h *Handler) DeregisterWallet(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if err := h.service.DeregisterWallet(r.Context(), userID, r.PathValue("walletId")); err != nil {
		writeWalletError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type acceptPolicyRequest struct {
	PolicyVersion string `json:"policyVersion"`
}

func (h *Handler) AcceptTestSettlementPolicy(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request acceptPolicyRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	acceptance, err := h.service.AcceptTestSettlementPolicy(
		r.Context(), userID, request.PolicyVersion,
	)
	if err != nil {
		httpapi.WriteError(
			w, http.StatusUnprocessableEntity, err.Error(),
			"현재 TEST 정산 고지 버전을 다시 확인해 주세요.",
		)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"acceptance": acceptance})
}

type createWalletRegistrationAttemptRequest struct {
	Address           string `json:"address"`
	ChainID           string `json:"chainId"`
	ClientOperationID string `json:"clientOperationId"`
}

func (h *Handler) CreateWalletRegistrationAttempt(
	w http.ResponseWriter,
	r *http.Request,
) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.walletVerification == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "SETTLEMENT_DISABLED", "테스트 정산이 비활성화되어 있습니다.")
		return
	}
	var request createWalletRegistrationAttemptRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	result, err := h.walletVerification.CreateRegistrationAttempt(
		r.Context(),
		accountapp.CreateRegistrationAttemptInput{
			UserID: userID, Address: request.Address, ChainID: request.ChainID,
			ClientOperationID: request.ClientOperationID,
			Origin:            strings.TrimSpace(r.Header.Get("Origin")),
			Source:            registrationClientKey(r, h.trustedProxies),
		},
	)
	if err != nil {
		writeWalletError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusCreated, result)
}

type completeWalletRegistrationAttemptRequest struct {
	Nonce             string `json:"nonce"`
	Signature         string `json:"signature"`
	ClientOperationID string `json:"clientOperationId"`
}

func (h *Handler) CompleteWalletRegistrationAttempt(
	w http.ResponseWriter,
	r *http.Request,
) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.walletVerification == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "SETTLEMENT_DISABLED", "테스트 정산이 비활성화되어 있습니다.")
		return
	}
	var request completeWalletRegistrationAttemptRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	result, err := h.walletVerification.CompleteRegistrationAttempt(
		r.Context(),
		accountapp.CompleteRegistrationAttemptInput{
			UserID: userID, AttemptID: r.PathValue("attemptId"),
			Nonce: request.Nonce, Signature: request.Signature,
			ClientOperationID: request.ClientOperationID,
			Origin:            strings.TrimSpace(r.Header.Get("Origin")),
			Source:            registrationClientKey(r, h.trustedProxies),
		},
	)
	if err != nil {
		writeWalletError(w, r, err)
		return
	}
	overview, err := h.service.GetOverview(r.Context(), userID)
	if err != nil {
		if httpapi.WriteFaultIfClassified(w, r, err, "등록된 지갑의 최신 상태를 불러오지 못했습니다.") {
			return
		}
		httpapi.WriteError(
			w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"등록된 지갑의 최신 상태를 불러오지 못했습니다.",
		)
		return
	}
	var projection *accountapp.WalletProjection
	for index := range overview.Wallets {
		if overview.Wallets[index].Wallet.ID == result.Wallet.ID {
			projection = &overview.Wallets[index]
			break
		}
	}
	if projection == nil {
		httpapi.WriteError(
			w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"등록된 지갑의 최신 상태를 찾지 못했습니다.",
		)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"registration": map[string]any{
			"walletId":         result.OwnershipProof.WalletID,
			"ownershipProofId": result.OwnershipProof.ID,
			"address":          result.OwnershipProof.Address,
			"accountId":        result.OwnershipProof.AccountID,
			"chainId":          result.OwnershipProof.ChainID,
			"verifiedAt":       result.OwnershipProof.VerifiedAt,
			"validUntil":       result.OwnershipProof.ValidUntil,
		},
		"latestWallet":   projection,
		"ownershipProof": result.OwnershipProof,
		"replay":         result.Replay,
	})
}

type startKYCVerificationRequest struct {
	OwnershipProofID  string `json:"ownershipProofId"`
	ClientOperationID string `json:"clientOperationId"`
}

func (h *Handler) StartKYCVerification(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.kyc == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "KYC_DISABLED", "KYC 검증이 비활성화되어 있습니다.")
		return
	}
	var request startKYCVerificationRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	result, err := h.kyc.Start(r.Context(), accountapp.StartKYCInput{
		UserID: userID, WalletID: r.PathValue("walletId"),
		OwnershipProofID:  request.OwnershipProofID,
		ClientOperationID: request.ClientOperationID,
	})
	if err != nil {
		writeWalletError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusCreated, result)
}

type checkKYCRequest struct {
	ClientOperationID string `json:"clientOperationId"`
}

func (h *Handler) CheckKYC(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.kyc == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "KYC_DISABLED", "KYC 검증이 비활성화되어 있습니다.")
		return
	}
	var request checkKYCRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	result, err := h.kyc.Check(r.Context(), accountapp.CheckKYCInput{
		UserID: userID, CaseID: r.PathValue("caseId"),
		ClientOperationID: request.ClientOperationID,
	})
	if err != nil {
		writeWalletError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, result)
}

func writeWalletError(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "외부 KYC 작업을 완료하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, accountapp.ErrRateLimited):
		w.Header().Set(
			"Retry-After", strconv.Itoa(accountapp.RateLimitRetryAfter(err)),
		)
		httpapi.WriteError(
			w, http.StatusTooManyRequests, accountapp.ErrRateLimited.Error(),
			"지갑 인증 요청이 너무 많습니다. 잠시 후 다시 시도해 주세요.",
		)
	case errors.Is(err, accountdomain.ErrWalletNotRegistered):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "이 계정에 등록된 지갑만 사용할 수 있습니다.")
	case errors.Is(err, accountdomain.ErrWalletOwnershipProofExpired):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "지갑 소유권 증명이 만료되었습니다. 서명 인증을 다시 완료해 주세요.")
	case errors.Is(err, accountdomain.ErrWalletOwnershipProofNotFresh):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "KYC를 시작하려면 최근 10분 이내의 지갑 서명 인증이 필요합니다.")
	case errors.Is(err, accountdomain.ErrKYCVerificationState):
		httpapi.WriteError(w, http.StatusConflict, err.Error(), "이 KYC 케이스는 현재 상태에서 다시 확인할 수 없습니다.")
	case errors.Is(err, accountdomain.ErrKYCProviderUnavailable):
		httpapi.WriteError(
			w,
			http.StatusServiceUnavailable,
			accountdomain.ErrKYCProviderUnavailable.Error(),
			"MockDojang TEST KYC 응답을 받지 못했습니다. 같은 작업을 다시 시도해 주세요.",
		)
	case errors.Is(err, accountdomain.ErrAssuranceInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "KYC 제공자의 유효한 확인 결과를 받지 못했습니다.")
	case errors.Is(err, accountdomain.ErrKYCCredentialInvalid),
		errors.Is(err, accountdomain.ErrKYCObservationInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "MockDojang의 credential 또는 evidence 결과가 유효하지 않습니다.")
	case errors.Is(err, accountdomain.ErrWalletAddressInvalid),
		errors.Is(err, accountdomain.ErrChainIDInvalid),
		errors.Is(err, accountdomain.ErrWalletSignatureInvalid),
		errors.Is(err, accountdomain.ErrWalletRegistrationAttemptInvalid),
		errors.Is(err, accountdomain.ErrWalletOwnershipProofInvalid),
		errors.Is(err, accountdomain.ErrKYCVerificationInvalid):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "지갑 등록 또는 TEST KYC 요청을 확인해 주세요.")
	case errors.Is(err, accountdomain.ErrWalletRegistrationAttemptMissing):
		httpapi.WriteError(w, http.StatusNotFound, err.Error(), "지갑 등록 시도를 찾지 못했습니다.")
	case errors.Is(err, accountdomain.ErrWalletOwnershipProofMissing):
		httpapi.WriteError(w, http.StatusNotFound, err.Error(), "지갑 소유권 증명을 찾지 못했습니다.")
	case errors.Is(err, accountdomain.ErrKYCVerificationMissing):
		httpapi.WriteError(w, http.StatusNotFound, err.Error(), "KYC 케이스를 찾지 못했습니다.")
	case errors.Is(err, accountdomain.ErrWalletRegistrationAttemptExpired):
		httpapi.WriteError(w, http.StatusGone, err.Error(), "지갑 등록 시도가 만료되었습니다. 새 서명 요청을 시작해 주세요.")
	case errors.Is(err, accountdomain.ErrWalletAlreadyRegistered):
		httpapi.WriteError(
			w, http.StatusConflict, err.Error(),
			"현재 결제 지갑이 이미 등록되어 있습니다. 다른 지갑으로 바꾸려면 현재 지갑을 먼저 등록 해제해 주세요.",
		)
	case errors.Is(err, accountdomain.ErrWalletRegistrationAttemptClosed),
		errors.Is(err, accountdomain.ErrWalletRegistrationOperationReused),
		errors.Is(err, accountdomain.ErrKYCIdempotencyReused):
		httpapi.WriteError(w, http.StatusConflict, err.Error(), "clientOperationId가 다른 요청에 사용되었거나 요청이 이미 종료되었습니다.")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "지갑 또는 KYC 요청을 처리하지 못했습니다.")
	}
}
