package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

// PlacementEvidenceKind keeps TEST evidence distinct from a real merchant
// effect. The fields may have the same shape, but their economic meaning is
// fixed by the immutable MerchantOrder execution mode.
type PlacementEvidenceKind string
type PlacementAmountMode string

const (
	PlacementEvidenceSandboxTest PlacementEvidenceKind = "SANDBOX_TEST_EVIDENCE"
	PlacementEvidenceLiveEffect  PlacementEvidenceKind = "LIVE_MERCHANT_EFFECT_EVIDENCE"
	PlacementAmountUnchanged     PlacementAmountMode   = "UNCHANGED"
	PlacementAmountChanged       PlacementAmountMode   = "CHANGED"
)

// PlacementEvidence is the immutable proof attached to one MerchantOrder
// placement. The operator authors only the safe refs, amount, source, and kind;
// the server owns hash, actor, and timestamps. ExternalOrderRef and
// ReceiptSafeRef must be opaque references; URLs, credentials, receipt payloads,
// and payment instruments do not belong here.
type PlacementEvidence struct {
	// AmountMode is a command-only instruction. The immutable evidence stores
	// the server-resolved actual amount, not a duplicate UI state.
	AmountMode         PlacementAmountMode   `json:"-"`
	Kind               PlacementEvidenceKind `json:"kind"`
	ExternalOrderRef   string                `json:"externalOrderRef"`
	ReceiptSafeRef     string                `json:"receiptSafeRef"`
	ActualAmountMinor  int64                 `json:"actualAmountMinor"`
	Currency           string                `json:"currency"`
	EvidenceSource     EvidenceSource        `json:"evidenceSource"`
	EvidenceHash       string                `json:"evidenceHash"`
	ObservedAt         time.Time             `json:"observedAt"`
	RecordedByUserID   string                `json:"recordedByUserId,omitempty"`
	RecordedAt         time.Time             `json:"recordedAt,omitempty"`
	ClaimsExternalLive bool                  `json:"claimsExternalLiveEffect"`
}

// FinalizePlacementEvidence turns the operator-authored fields into the
// immutable server record. observedAt, actor identity, and EvidenceHash are
// server-owned: clients cannot choose or replay those integrity fields.
func FinalizePlacementEvidence(
	merchantOrderID, operatorUserID string,
	observedAt time.Time,
	evidence PlacementEvidence,
) PlacementEvidence {
	evidence.ExternalOrderRef = strings.TrimSpace(evidence.ExternalOrderRef)
	evidence.ReceiptSafeRef = strings.TrimSpace(evidence.ReceiptSafeRef)
	evidence.Currency = strings.ToUpper(strings.TrimSpace(evidence.Currency))
	evidence.ObservedAt = observedAt.UTC().Truncate(time.Microsecond)
	evidence.RecordedByUserID = strings.TrimSpace(operatorUserID)
	evidence.RecordedAt = evidence.ObservedAt
	payload, _ := json.Marshal(struct {
		SchemaVersion      string                `json:"schemaVersion"`
		MerchantOrderID    string                `json:"merchantOrderId"`
		Kind               PlacementEvidenceKind `json:"kind"`
		ExternalOrderRef   string                `json:"externalOrderRef"`
		ReceiptSafeRef     string                `json:"receiptSafeRef"`
		ActualAmountMinor  int64                 `json:"actualAmountMinor"`
		Currency           string                `json:"currency"`
		EvidenceSource     EvidenceSource        `json:"evidenceSource"`
		ObservedAt         string                `json:"observedAt"`
		RecordedByUserID   string                `json:"recordedByUserId"`
		ClaimsExternalLive bool                  `json:"claimsExternalLiveEffect"`
	}{
		SchemaVersion:      "vitlane.procurement-placement-evidence.v1",
		MerchantOrderID:    strings.TrimSpace(merchantOrderID),
		Kind:               evidence.Kind,
		ExternalOrderRef:   evidence.ExternalOrderRef,
		ReceiptSafeRef:     evidence.ReceiptSafeRef,
		ActualAmountMinor:  evidence.ActualAmountMinor,
		Currency:           evidence.Currency,
		EvidenceSource:     evidence.EvidenceSource,
		ObservedAt:         evidence.ObservedAt.Format(time.RFC3339Nano),
		RecordedByUserID:   evidence.RecordedByUserID,
		ClaimsExternalLive: evidence.ClaimsExternalLive,
	})
	digest := sha256.Sum256(payload)
	evidence.EvidenceHash = "sha256:" + hex.EncodeToString(digest[:])
	return evidence
}

// ValidatePlacementEvidence binds a complete evidence record to the stored
// execution mode and immutable approved maximum. The operator records the
// amount actually paid; a lower amount is valid, while spending above the
// customer-approved maximum fails closed. Sandbox accepts realistic test
// references but can never claim that a real merchant effect occurred.
func ValidatePlacementEvidence(
	mode ExecutionMode,
	authorizedAmountMinor int64,
	evidence PlacementEvidence,
) error {
	evidence.ExternalOrderRef = strings.TrimSpace(evidence.ExternalOrderRef)
	evidence.ReceiptSafeRef = strings.TrimSpace(evidence.ReceiptSafeRef)
	evidence.Currency = strings.ToUpper(strings.TrimSpace(evidence.Currency))
	evidence.EvidenceHash = strings.TrimSpace(evidence.EvidenceHash)
	if authorizedAmountMinor <= 0 || evidence.ActualAmountMinor <= 0 ||
		evidence.ActualAmountMinor > authorizedAmountMinor ||
		evidence.Currency != "USD" || evidence.ObservedAt.IsZero() ||
		!validOpaquePlacementRef(evidence.ExternalOrderRef, 255) ||
		!validOpaquePlacementRef(evidence.ReceiptSafeRef, 512) ||
		!validServerEvidenceHash(evidence.EvidenceHash) {
		return ErrEvidenceInvalid
	}
	switch evidence.EvidenceSource {
	case EvidenceOperatorObservation, EvidenceMerchantPage, EvidenceMerchantPolicy,
		EvidenceReceipt, EvidenceOther:
	default:
		return ErrEvidenceInvalid
	}
	switch mode {
	case ModeSimulatedNoEffect:
		if evidence.Kind != PlacementEvidenceSandboxTest || evidence.ClaimsExternalLive {
			return ErrEvidenceInvalid
		}
	case ModeLiveMerchantEffect:
		if evidence.Kind != PlacementEvidenceLiveEffect || !evidence.ClaimsExternalLive {
			return ErrEvidenceInvalid
		}
	default:
		return ErrEvidenceInvalid
	}
	return nil
}

// ResolvePlacementAmount derives the default amount from the immutable
// approved merchant total. A changed amount must be explicitly supplied.
func ResolvePlacementAmount(authorizedAmountMinor int64, evidence PlacementEvidence) (PlacementEvidence, error) {
	if authorizedAmountMinor <= 0 {
		return evidence, ErrEvidenceInvalid
	}
	switch evidence.AmountMode {
	case PlacementAmountUnchanged:
		if evidence.ActualAmountMinor != 0 {
			return evidence, ErrEvidenceInvalid
		}
		evidence.ActualAmountMinor = authorizedAmountMinor
	case PlacementAmountChanged:
		if evidence.ActualAmountMinor <= 0 || evidence.ActualAmountMinor > authorizedAmountMinor {
			return evidence, ErrEvidenceInvalid
		}
	default:
		return evidence, ErrEvidenceInvalid
	}
	return evidence, nil
}

func validServerEvidenceHash(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
}

func validOpaquePlacementRef(value string, maxLength int) bool {
	length := utf8.RuneCountInString(value)
	if length < 1 || length > maxLength || strings.ContainsAny(value, "\r\n\t") {
		return false
	}
	lower := strings.ToLower(value)
	return !strings.Contains(lower, "http://") && !strings.Contains(lower, "https://")
}
