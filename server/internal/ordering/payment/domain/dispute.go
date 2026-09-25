package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrDisputeInvalid         = errors.New("PAYPAL_DISPUTE_INVALID")
	ErrDisputeNotFound        = errors.New("PAYPAL_DISPUTE_NOT_FOUND")
	ErrRefundBlockedByDispute = errors.New("PAYPAL_REFUND_BLOCKED_BY_DISPUTE")
)

const (
	PayPalDisputeCreated  = "CUSTOMER.DISPUTE.CREATED"
	PayPalDisputeUpdated  = "CUSTOMER.DISPUTE.UPDATED"
	PayPalDisputeResolved = "CUSTOMER.DISPUTE.RESOLVED"
)

type DisputeState string

const (
	DisputeOpen     DisputeState = "OPEN"
	DisputeResolved DisputeState = "RESOLVED"
)

type DisputeProviderStatus string

const (
	DisputeStatusUnknown                  DisputeProviderStatus = "UNKNOWN"
	DisputeStatusOpen                     DisputeProviderStatus = "OPEN"
	DisputeStatusWaitingForSellerResponse DisputeProviderStatus = "WAITING_FOR_SELLER_RESPONSE"
	DisputeStatusWaitingForBuyerResponse  DisputeProviderStatus = "WAITING_FOR_BUYER_RESPONSE"
	DisputeStatusUnderReview              DisputeProviderStatus = "UNDER_REVIEW"
	DisputeStatusResolved                 DisputeProviderStatus = "RESOLVED"
)

type DisputeOutcome string

const (
	DisputeOutcomeNone            DisputeOutcome = "NONE"
	DisputeOutcomeBuyerFavour     DisputeOutcome = "RESOLVED_BUYER_FAVOUR"
	DisputeOutcomeSellerFavour    DisputeOutcome = "RESOLVED_SELLER_FAVOUR"
	DisputeOutcomeWithPayout      DisputeOutcome = "RESOLVED_WITH_PAYOUT"
	DisputeOutcomeCanceledByBuyer DisputeOutcome = "CANCELED_BY_BUYER"
	DisputeOutcomeAccepted        DisputeOutcome = "ACCEPTED"
	DisputeOutcomeDenied          DisputeOutcome = "DENIED"
)

type DisputeLifecycleStage string

const (
	DisputeStageUnknown        DisputeLifecycleStage = "UNKNOWN"
	DisputeStageInquiry        DisputeLifecycleStage = "INQUIRY"
	DisputeStageChargeback     DisputeLifecycleStage = "CHARGEBACK"
	DisputeStagePreArbitration DisputeLifecycleStage = "PRE_ARBITRATION"
	DisputeStageArbitration    DisputeLifecycleStage = "ARBITRATION"
)

// PayPalDisputeCase is the Payment-owned, environment-bound case projection.
// It intentionally contains no buyer identity or provider payload.
type PayPalDisputeCase struct {
	ID                  string                `json:"id"`
	Environment         string                `json:"environment"`
	DisputeID           string                `json:"disputeId"`
	AgencyOrderID       string                `json:"agencyOrderId"`
	CustomerPaymentID   string                `json:"customerPaymentId"`
	MOCashReceiptID     string                `json:"moCashReceiptId"`
	CaptureID           string                `json:"captureId"`
	State               DisputeState          `json:"state"`
	ProviderStatus      DisputeProviderStatus `json:"providerStatus"`
	Outcome             DisputeOutcome        `json:"outcome"`
	Reason              string                `json:"reason"`
	LifecycleStage      DisputeLifecycleStage `json:"lifecycleStage"`
	SellerResponseDueAt *time.Time            `json:"sellerResponseDueAt,omitempty"`
	LatestEventID       string                `json:"latestEventId"`
	LatestEventType     string                `json:"latestEventType"`
	OpenedAt            time.Time             `json:"openedAt"`
	LastObservedAt      time.Time             `json:"lastObservedAt"`
	ResolvedAt          *time.Time            `json:"resolvedAt,omitempty"`
	Version             int64                 `json:"version"`
	CreatedAt           time.Time             `json:"createdAt"`
	UpdatedAt           time.Time             `json:"updatedAt"`
}

// BlocksDirectRefund is intentionally conservative. A case only releases a
// separate Vitlane refund after PayPal is known not to have moved buyer funds.
func (d PayPalDisputeCase) BlocksDirectRefund() bool {
	if d.State != DisputeResolved {
		return true
	}
	switch d.Outcome {
	case DisputeOutcomeSellerFavour, DisputeOutcomeCanceledByBuyer, DisputeOutcomeDenied:
		return false
	default:
		return true
	}
}

type DisputeObservation struct {
	Environment         string
	EventID             string
	EventType           string
	DisputeID           string
	CaptureID           string
	ProviderStatus      DisputeProviderStatus
	Outcome             DisputeOutcome
	Reason              string
	LifecycleStage      DisputeLifecycleStage
	SellerResponseDueAt *time.Time
	ObservedAt          time.Time
}

func IsPayPalDisputeEvent(eventType string) bool {
	switch strings.ToUpper(strings.TrimSpace(eventType)) {
	case PayPalDisputeCreated, PayPalDisputeUpdated, PayPalDisputeResolved:
		return true
	default:
		return false
	}
}

var providerReferencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,254}$`)
var evidenceHashPattern = regexp.MustCompile(`^0x[0-9a-f]{64}$`)

func NewDisputeObservation(
	environment, eventID, eventType, disputeID, captureID, providerStatus,
	outcome, reason, lifecycleStage string,
	sellerResponseDueAt *time.Time,
	observedAt time.Time,
) (DisputeObservation, error) {
	environment = strings.ToUpper(strings.TrimSpace(environment))
	eventID = strings.TrimSpace(eventID)
	eventType = strings.ToUpper(strings.TrimSpace(eventType))
	disputeID = strings.TrimSpace(disputeID)
	captureID = strings.TrimSpace(captureID)
	if (environment != "SANDBOX" && environment != "LIVE") ||
		!providerReferencePattern.MatchString(eventID) ||
		!providerReferencePattern.MatchString(disputeID) ||
		(captureID != "" && !providerReferencePattern.MatchString(captureID)) ||
		!IsPayPalDisputeEvent(eventType) || observedAt.IsZero() {
		return DisputeObservation{}, ErrDisputeInvalid
	}
	status := normalizeDisputeProviderStatus(providerStatus)
	finalOutcome := normalizeDisputeOutcome(outcome)
	if eventType == PayPalDisputeResolved {
		status = DisputeStatusResolved
	}
	if status != DisputeStatusResolved {
		finalOutcome = DisputeOutcomeNone
	}
	if sellerResponseDueAt != nil {
		due := sellerResponseDueAt.UTC()
		sellerResponseDueAt = &due
	}
	return DisputeObservation{
		Environment: environment, EventID: eventID, EventType: eventType,
		DisputeID: disputeID, CaptureID: captureID, ProviderStatus: status,
		Outcome: finalOutcome, Reason: normalizeDisputeReason(reason),
		LifecycleStage:      normalizeDisputeLifecycleStage(lifecycleStage),
		SellerResponseDueAt: sellerResponseDueAt, ObservedAt: observedAt.UTC(),
	}, nil
}

func (o DisputeObservation) State() DisputeState {
	if o.ProviderStatus == DisputeStatusResolved || o.EventType == PayPalDisputeResolved {
		return DisputeResolved
	}
	return DisputeOpen
}

func normalizeDisputeProviderStatus(value string) DisputeProviderStatus {
	switch DisputeProviderStatus(strings.ToUpper(strings.TrimSpace(value))) {
	case DisputeStatusOpen, DisputeStatusWaitingForSellerResponse,
		DisputeStatusWaitingForBuyerResponse, DisputeStatusUnderReview,
		DisputeStatusResolved:
		return DisputeProviderStatus(strings.ToUpper(strings.TrimSpace(value)))
	default:
		return DisputeStatusUnknown
	}
}

func normalizeDisputeOutcome(value string) DisputeOutcome {
	switch DisputeOutcome(strings.ToUpper(strings.TrimSpace(value))) {
	case DisputeOutcomeBuyerFavour, DisputeOutcomeSellerFavour,
		DisputeOutcomeWithPayout, DisputeOutcomeCanceledByBuyer,
		DisputeOutcomeAccepted, DisputeOutcomeDenied:
		return DisputeOutcome(strings.ToUpper(strings.TrimSpace(value)))
	default:
		return DisputeOutcomeNone
	}
}

func normalizeDisputeLifecycleStage(value string) DisputeLifecycleStage {
	switch DisputeLifecycleStage(strings.ToUpper(strings.TrimSpace(value))) {
	case DisputeStageInquiry, DisputeStageChargeback,
		DisputeStagePreArbitration, DisputeStageArbitration:
		return DisputeLifecycleStage(strings.ToUpper(strings.TrimSpace(value)))
	default:
		return DisputeStageUnknown
	}
}

var disputeReasonPattern = regexp.MustCompile(`^[A-Z0-9_]{1,64}$`)

func normalizeDisputeReason(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if !disputeReasonPattern.MatchString(value) {
		return "OTHER"
	}
	return value
}

type DisputeActionKind string

const (
	DisputeActionCaseObserved      DisputeActionKind = "CASE_OBSERVED"
	DisputeActionMessageSent       DisputeActionKind = "MESSAGE_SENT"
	DisputeActionEvidenceSubmitted DisputeActionKind = "EVIDENCE_SUBMITTED"
	DisputeActionOfferMade         DisputeActionKind = "OFFER_MADE"
	DisputeActionClaimAccepted     DisputeActionKind = "CLAIM_ACCEPTED"
	DisputeActionAppealSubmitted   DisputeActionKind = "APPEAL_SUBMITTED"
	DisputeActionOther             DisputeActionKind = "OTHER"
)

type DisputeEvidenceSource string

const (
	DisputeEvidencePayPalResolutionCenter DisputeEvidenceSource = "PAYPAL_RESOLUTION_CENTER"
	DisputeEvidencePayPalEmail            DisputeEvidenceSource = "PAYPAL_EMAIL"
	DisputeEvidenceInternalOrderRecord    DisputeEvidenceSource = "INTERNAL_ORDER_RECORD"
	DisputeEvidenceCarrier                DisputeEvidenceSource = "CARRIER"
	DisputeEvidenceMerchantReceipt        DisputeEvidenceSource = "MERCHANT_RECEIPT"
	DisputeEvidenceOther                  DisputeEvidenceSource = "OTHER"
)

type PayPalDisputeManualAction struct {
	ID                     string                `json:"id"`
	DisputeCaseID          string                `json:"disputeCaseId"`
	ActionKind             DisputeActionKind     `json:"actionKind"`
	ExternalReference      string                `json:"externalReference"`
	PublicRationale        string                `json:"publicRationale"`
	InternalNote           string                `json:"internalNote,omitempty"`
	ActorUserID            string                `json:"actorUserId"`
	ObservedProviderStatus DisputeProviderStatus `json:"observedProviderStatus,omitempty"`
	ObservedOutcome        DisputeOutcome        `json:"observedOutcome,omitempty"`
	EvidenceSource         DisputeEvidenceSource `json:"evidenceSource"`
	EvidenceHash           string                `json:"evidenceHash"`
	ObservedAt             time.Time             `json:"observedAt"`
	IdempotencyKeyHash     string                `json:"-"`
	RequestHash            string                `json:"-"`
	CreatedAt              time.Time             `json:"createdAt"`
}

func (a PayPalDisputeManualAction) Validate(now time.Time) error {
	if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(a.DisputeCaseID) == "" ||
		strings.TrimSpace(a.ActorUserID) == "" ||
		!validDisputeActionKind(a.ActionKind) ||
		!providerReferencePattern.MatchString(strings.TrimSpace(a.ExternalReference)) ||
		utf8.RuneCountInString(strings.TrimSpace(a.PublicRationale)) < 1 ||
		utf8.RuneCountInString(strings.TrimSpace(a.PublicRationale)) > 2000 ||
		utf8.RuneCountInString(a.InternalNote) > 4000 || !validDisputeEvidenceSource(a.EvidenceSource) ||
		!evidenceHashPattern.MatchString(strings.ToLower(strings.TrimSpace(a.EvidenceHash))) ||
		a.ObservedAt.IsZero() || a.ObservedAt.After(now.Add(5*time.Minute)) ||
		!evidenceHashPattern.MatchString(strings.ToLower(strings.TrimSpace(a.IdempotencyKeyHash))) ||
		!evidenceHashPattern.MatchString(strings.ToLower(strings.TrimSpace(a.RequestHash))) {
		return ErrDisputeInvalid
	}
	if a.ObservedProviderStatus != "" {
		if normalizeDisputeProviderStatus(string(a.ObservedProviderStatus)) != a.ObservedProviderStatus ||
			a.ObservedProviderStatus == DisputeStatusUnknown {
			return ErrDisputeInvalid
		}
		if a.ObservedProviderStatus != DisputeStatusResolved &&
			a.ObservedOutcome != "" && a.ObservedOutcome != DisputeOutcomeNone {
			return ErrDisputeInvalid
		}
	}
	if a.ObservedOutcome != "" {
		if a.ObservedProviderStatus != DisputeStatusResolved ||
			normalizeDisputeOutcome(string(a.ObservedOutcome)) != a.ObservedOutcome {
			return ErrDisputeInvalid
		}
	}
	return nil
}

func validDisputeActionKind(value DisputeActionKind) bool {
	switch value {
	case DisputeActionCaseObserved, DisputeActionMessageSent,
		DisputeActionEvidenceSubmitted, DisputeActionOfferMade,
		DisputeActionClaimAccepted, DisputeActionAppealSubmitted, DisputeActionOther:
		return true
	default:
		return false
	}
}

func validDisputeEvidenceSource(value DisputeEvidenceSource) bool {
	switch value {
	case DisputeEvidencePayPalResolutionCenter, DisputeEvidencePayPalEmail,
		DisputeEvidenceInternalOrderRecord, DisputeEvidenceCarrier,
		DisputeEvidenceMerchantReceipt, DisputeEvidenceOther:
		return true
	default:
		return false
	}
}
