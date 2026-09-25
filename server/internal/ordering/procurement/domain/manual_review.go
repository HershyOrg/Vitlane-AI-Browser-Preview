package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrManualReviewUnavailable = errors.New("PROCUREMENT_MANUAL_REVIEW_UNAVAILABLE")
	ErrDecisionInvalid         = errors.New("PROCUREMENT_DECISION_INVALID")
	ErrRequestInvalid          = errors.New("PROCUREMENT_CUSTOMER_REQUEST_INVALID")
	ErrRequestNotFound         = errors.New("PROCUREMENT_CUSTOMER_REQUEST_NOT_FOUND")
	ErrRequestStateConflict    = errors.New("PROCUREMENT_CUSTOMER_REQUEST_STATE_CONFLICT")
	ErrRequestResponseInvalid  = errors.New("PROCUREMENT_CUSTOMER_RESPONSE_INVALID")
	ErrNoResponseTooEarly      = errors.New("PROCUREMENT_NO_RESPONSE_TOO_EARLY")
	ErrManualDecisionRequired  = errors.New("PROCUREMENT_MANUAL_DECISION_REQUIRED")
	ErrAuthorizationMissing    = errors.New("PROCUREMENT_AUTHORIZATION_MISSING")
	ErrOpenCustomerRequest     = errors.New("PROCUREMENT_CUSTOMER_REQUEST_OPEN")
	ErrEffectAlreadyStarted    = errors.New("PROCUREMENT_EFFECT_ALREADY_STARTED")
)

// ProcurementAuthorizationKind identifies the customer-approved scope that
// permits a manual operator purchase.
type ProcurementAuthorizationKind string

const (
	AuthorizationManualOperatorPurchase ProcurementAuthorizationKind = "MANUAL_OPERATOR_PURCHASE"
)

type ManualDecision string

const (
	DecisionWithinAuthorization ManualDecision = "WITHIN_AUTHORIZATION"
	DecisionImmaterialVariance  ManualDecision = "IMMATERIAL_VARIANCE"
	DecisionMaterialCondition   ManualDecision = "MATERIAL_NEW_CONDITION"
	DecisionUnableToPurchase    ManualDecision = "UNABLE_TO_PURCHASE"
)

type EvidenceSource string

const (
	EvidenceOperatorObservation EvidenceSource = "OPERATOR_OBSERVATION"
	EvidenceMerchantPage        EvidenceSource = "MERCHANT_PAGE"
	EvidenceMerchantPolicy      EvidenceSource = "MERCHANT_POLICY"
	EvidenceReceipt             EvidenceSource = "RECEIPT"
	EvidenceOther               EvidenceSource = "OTHER"
)

type DecisionRecord struct {
	ID                   string         `json:"id"`
	MerchantOrderID      string         `json:"merchantOrderId"`
	AgencyOrderID        string         `json:"agencyOrderId"`
	TaskID               string         `json:"taskId"`
	Decision             ManualDecision `json:"decision"`
	PublicRationale      string         `json:"publicRationale"`
	InternalNote         string         `json:"internalNote,omitempty"`
	ObservedCondition    string         `json:"observedCondition"`
	EvidenceSource       EvidenceSource `json:"evidenceSource"`
	EvidenceHash         string         `json:"evidenceHash"`
	ObservedAt           time.Time      `json:"observedAt"`
	DecidedByUserID      string         `json:"decidedByUserId"`
	TaskVersion          int64          `json:"taskVersion"`
	AuthorizationHash    string         `json:"authorizationHash"`
	ExecutionProfileHash string         `json:"executionProfileHash"`
	CreatedAt            time.Time      `json:"createdAt"`
}

func ValidateDecision(
	decision ManualDecision,
	publicRationale, internalNote, observedCondition string,
	source EvidenceSource,
	evidenceHash string,
	observedAt time.Time,
) error {
	switch decision {
	case DecisionWithinAuthorization, DecisionImmaterialVariance,
		DecisionMaterialCondition, DecisionUnableToPurchase:
	default:
		return ErrDecisionInvalid
	}
	switch source {
	case EvidenceOperatorObservation, EvidenceMerchantPage,
		EvidenceMerchantPolicy, EvidenceReceipt, EvidenceOther:
	default:
		return ErrDecisionInvalid
	}
	if runeLength(publicRationale) < 8 || runeLength(publicRationale) > 2000 ||
		runeLength(internalNote) > 4000 || runeLength(observedCondition) < 1 ||
		runeLength(observedCondition) > 2000 ||
		len(strings.TrimSpace(evidenceHash)) < 16 || len(strings.TrimSpace(evidenceHash)) > 256 ||
		observedAt.IsZero() {
		return ErrDecisionInvalid
	}
	return nil
}

type CustomerRequestKind string

const (
	RequestInformation CustomerRequestKind = "INFORMATION"
	RequestConsent     CustomerRequestKind = "CONSENT"
)

type CustomerResponseType string

const (
	ResponseText           CustomerResponseType = "TEXT"
	ResponseSingleChoice   CustomerResponseType = "SINGLE_CHOICE"
	ResponseBooleanConsent CustomerResponseType = "BOOLEAN_CONSENT"
)

type CustomerRequestState string

const (
	RequestPending          CustomerRequestState = "PENDING"
	RequestAnswered         CustomerRequestState = "ANSWERED"
	RequestDeclined         CustomerRequestState = "DECLINED"
	RequestFailedNoResponse CustomerRequestState = "FAILED_NO_RESPONSE"
	RequestCancelled        CustomerRequestState = "CANCELLED"
)

type CustomerRequest struct {
	ID                string               `json:"id"`
	MerchantOrderID   string               `json:"merchantOrderId"`
	AgencyOrderID     string               `json:"agencyOrderId"`
	UserID            string               `json:"-"`
	Kind              CustomerRequestKind  `json:"kind"`
	Prompt            string               `json:"prompt"`
	ResponseType      CustomerResponseType `json:"responseType"`
	ResponseOptions   []string             `json:"responseOptions,omitempty"`
	PublicContext     string               `json:"publicContext"`
	State             CustomerRequestState `json:"state"`
	Response          json.RawMessage      `json:"response,omitempty"`
	RequestedByUserID string               `json:"requestedByUserId"`
	RequestedAt       time.Time            `json:"requestedAt"`
	DueAt             time.Time            `json:"dueAt"`
	ResolvedAt        *time.Time           `json:"resolvedAt,omitempty"`
	ResolvedByUserID  string               `json:"resolvedByUserId,omitempty"`
	ResolutionReason  string               `json:"resolutionReason,omitempty"`
	SourceDecisionID  string               `json:"sourceDecisionId"`
	Version           int64                `json:"version"`
}

func ValidateCustomerRequest(
	kind CustomerRequestKind,
	prompt string,
	responseType CustomerResponseType,
	responseOptions []string,
	publicContext string,
) error {
	if kind != RequestInformation && kind != RequestConsent {
		return ErrRequestInvalid
	}
	// 고객 공개 배경은 판단 공개 근거(ValidateDecision)와 같은 최소 8자다 — 고객이
	// 읽을 설명이며, 웹 폼의 규칙과 서버 규칙을 같게 둔다.
	if runeLength(prompt) < 1 || runeLength(prompt) > 2000 ||
		runeLength(publicContext) < 8 || runeLength(publicContext) > 2000 {
		return ErrRequestInvalid
	}
	switch responseType {
	case ResponseText:
		if len(responseOptions) != 0 || kind != RequestInformation {
			return ErrRequestInvalid
		}
	case ResponseSingleChoice:
		if len(responseOptions) < 2 || len(responseOptions) > 10 {
			return ErrRequestInvalid
		}
		seen := map[string]bool{}
		for _, option := range responseOptions {
			option = strings.TrimSpace(option)
			if runeLength(option) < 1 || runeLength(option) > 200 || seen[option] {
				return ErrRequestInvalid
			}
			seen[option] = true
		}
	case ResponseBooleanConsent:
		if kind != RequestConsent || len(responseOptions) != 0 {
			return ErrRequestInvalid
		}
	default:
		return ErrRequestInvalid
	}
	return nil
}

// ValidateCustomerResponse treats a response as typed customer evidence.  A
// late answer is valid until an operator explicitly wins the no-response CAS;
// dueAt is intentionally not consulted here.
func ValidateCustomerResponse(
	responseType CustomerResponseType,
	options []string,
	response json.RawMessage,
) error {
	if len(response) == 0 || !json.Valid(response) {
		return ErrRequestResponseInvalid
	}
	switch responseType {
	case ResponseText:
		var value struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(response, &value) != nil || runeLength(value.Text) < 1 || runeLength(value.Text) > 2000 {
			return ErrRequestResponseInvalid
		}
	case ResponseSingleChoice:
		var value struct {
			Choice string `json:"choice"`
		}
		if json.Unmarshal(response, &value) != nil {
			return ErrRequestResponseInvalid
		}
		found := false
		for _, option := range options {
			found = found || strings.TrimSpace(option) == strings.TrimSpace(value.Choice)
		}
		if !found {
			return ErrRequestResponseInvalid
		}
	case ResponseBooleanConsent:
		var value struct {
			Accepted *bool `json:"accepted"`
		}
		if json.Unmarshal(response, &value) != nil || value.Accepted == nil {
			return ErrRequestResponseInvalid
		}
	default:
		return ErrRequestResponseInvalid
	}
	return nil
}

func runeLength(value string) int { return utf8.RuneCountInString(strings.TrimSpace(value)) }
