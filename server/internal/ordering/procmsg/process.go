package procmsg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// RequestKind is a closed business input catalog. Requested never means that an
// Owner has already performed the action or verified an external result.
type RequestKind string

const (
	RequestPurchase              RequestKind = "PURCHASE"
	RequestCancel                RequestKind = "CANCEL"
	RequestClaimTask             RequestKind = "CLAIM_TASK"
	RequestManualDecision        RequestKind = "MANUAL_DECISION"
	RequestCustomerQuestion      RequestKind = "CUSTOMER_QUESTION"
	RequestCustomerResponse      RequestKind = "CUSTOMER_RESPONSE"
	RequestCloseCustomerQuestion RequestKind = "CLOSE_CUSTOMER_QUESTION"
	RequestPlacementEvidence     RequestKind = "PLACEMENT_EVIDENCE"
	RequestFailureEvidence       RequestKind = "FAILURE_EVIDENCE"
	RequestRevealShipping        RequestKind = "REVEAL_SHIPPING"
	RequestRevealCheckout        RequestKind = "REVEAL_CHECKOUT"
	RequestRefund                RequestKind = "REFUND_REQUEST"
	RequestRefundDecision        RequestKind = "REFUND_DECISION"
	RequestRetryEffect           RequestKind = "RETRY_EFFECT"
)

const (
	RequestCreateShipment  RequestKind = "CREATE_SHIPMENT"
	RequestShipmentEvent   RequestKind = "SHIPMENT_EVENT"
	RequestConfirmDelivery RequestKind = "CONFIRM_DELIVERY"
	RequestResolveDelivery RequestKind = "RESOLVE_DELIVERY"
	RequestCreateReturn    RequestKind = "CREATE_RETURN"
	RequestUpdateReturn    RequestKind = "UPDATE_RETURN"
)

const EventActionRequested = "process.action.requested.v1"

// ActionRequest contains scope and immutable input references, never a caller
// supplied Owner success or a callback. ActorID/Role are installed by trusted
// ingress after authentication. Sensitive input is kept in its Owner's vault.
type ActionRequest struct {
	ID              string      `json:"requestId"`
	Kind            RequestKind `json:"kind"`
	AgencyOrderID   string      `json:"agencyOrderId"`
	MerchantOrderID string      `json:"merchantOrderId,omitempty"`
	AllocationID    string      `json:"allocationId,omitempty"`
	TaskID          string      `json:"taskId,omitempty"`
	ActorID         string      `json:"actorId"`
	ActorRole       string      `json:"actorRole"`
	CancelKind      string      `json:"cancelKind,omitempty"`
	ReferenceID     string      `json:"referenceId,omitempty"`
	InputRef        string      `json:"inputRef,omitempty"`
	InputHash       string      `json:"inputHash,omitempty"`
	Decision        string      `json:"decision,omitempty"`
}

var ErrRequestInvalid = errors.New("ORDER_PROCESS_REQUEST_INVALID")

func (r ActionRequest) Validate() error {
	if strings.TrimSpace(r.ID) == "" || len(r.ID) > 200 || strings.TrimSpace(r.AgencyOrderID) == "" || strings.TrimSpace(r.ActorID) == "" {
		return ErrRequestInvalid
	}
	if r.ActorRole != "CUSTOMER" && r.ActorRole != "OPERATOR" && r.ActorRole != "SYSTEM" {
		return ErrRequestInvalid
	}
	switch r.Kind {
	case RequestPurchase, RequestClaimTask, RequestManualDecision, RequestCustomerQuestion,
		RequestCustomerResponse, RequestCloseCustomerQuestion, RequestPlacementEvidence,
		RequestFailureEvidence, RequestRevealShipping, RequestRevealCheckout,
		RequestRefund, RequestRefundDecision, RequestCreateShipment, RequestShipmentEvent, RequestConfirmDelivery, RequestResolveDelivery, RequestCreateReturn, RequestUpdateReturn:
		if r.MerchantOrderID == "" {
			return ErrRequestInvalid
		}
	case RequestCancel:
		if r.MerchantOrderID == "" || (r.CancelKind != "PRE_EFFECT" && r.CancelKind != "DELAY_RULE") {
			return ErrRequestInvalid
		}
	case RequestRetryEffect:
		if r.ReferenceID == "" {
			return ErrRequestInvalid
		}
	default:
		return ErrRequestInvalid
	}
	return nil
}

// CanonicalHash deliberately includes actor, target and immutable input hash.
// A browser key reused for a different MO or body is never an idempotent replay.
func (r ActionRequest) CanonicalHash() string {
	r.ID = ""
	b, _ := json.Marshal(r)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// EffectIdentity is stable across delivery claims, restarts and Owner polling.
// It is an authorization issued by the reducer, not a worker lease.
func EffectIdentity(orderID, key string) string {
	sum := sha256.Sum256([]byte("vitlane.process-effect.v1\x00" + orderID + "\x00" + key))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// RequestFlowID is shared by receipts, effects and Owner result events. Browser
// request keys remain separate so aliases can point at the original flow.
func RequestFlowID(orderID, requestID string) string {
	return EffectIdentity(orderID, "request:"+requestID)
}

// PurchaseContext is the minimum verified information an Owner needs. The
// reducer obtains it from Owner facts; no executing Owner reads another Owner's
// mutable rows. Immutable authorization/profile values are bound by hash.
type PurchaseContext struct {
	RequestID            string   `json:"requestId"`
	ActorID              string   `json:"actorId"`
	TaskID               string   `json:"taskId"`
	MerchantOrderID      string   `json:"merchantOrderId"`
	AllocationID         string   `json:"allocationId"`
	AuthorizationKind    string   `json:"authorizationKind"`
	AuthorizationHash    string   `json:"authorizationHash"`
	ExecutionProfileHash string   `json:"executionProfileHash"`
	ExecutionMode        string   `json:"executionMode"`
	FundingPositionID    string   `json:"fundingPositionId,omitempty"`
	FundingState         string   `json:"fundingState,omitempty"`
	UnitIDs              []string `json:"unitIds,omitempty"`
}

type OrderAuthorization struct {
	Kind                 string `json:"kind"`
	Hash                 string `json:"hash"`
	ExecutionProfileHash string `json:"executionProfileHash"`
	ExecutionMode        string `json:"executionMode"`
}

const (
	EffectEnsureMOFunding             = "payment.ensure_mo_funding.v1"
	EffectConfirmPurchaseRegistration = "logistics.confirm_purchase_registration.v1"
	EffectReserveCancellation         = "logistics.reserve_cancellation.v1"
	EffectReleaseCancellation         = "logistics.release_cancellation.v1"
	EffectReleasePurchase             = "procurement.release_purchase.v1"
	EffectApplyLogisticsCancellation  = "logistics.apply_cancellation.v1"
	EffectApplyOwnerAction            = "owner.apply_order_action.v1"
)

// EffectReport is a validated Owner outcome. A delivery ACK never constructs
// this report. Result has the kind-specific schema checked by the reducer.
type EffectReport struct {
	EffectID        string          `json:"effectId"`
	EffectType      string          `json:"effectType"`
	MerchantOrderID string          `json:"merchantOrderId,omitempty"`
	RequestID       string          `json:"requestId,omitempty"`
	Outcome         string          `json:"outcome"`
	Code            string          `json:"code,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
}

const EventEffectReported = "owner.effect.reported.v1"

// ProcessEffect is an immutable message authored by the reducer. Delivery
// metadata is deliberately kept in a separate type owned by the queue.
type ProcessEffect struct {
	ID              string          `json:"effectId"`
	AgencyOrderID   string          `json:"agencyOrderId"`
	MerchantOrderID string          `json:"merchantOrderId,omitempty"`
	FlowID          string          `json:"flowId,omitempty"`
	RequestID       string          `json:"requestId,omitempty"`
	Target          string          `json:"target"`
	Type            string          `json:"type"`
	IdempotencyKey  string          `json:"idempotencyKey"`
	InputHash       string          `json:"inputHash"`
	Payload         json.RawMessage `json:"payload"`
	CausedByEventID int64           `json:"causedByEventId"`
	CreatedAt       time.Time       `json:"createdAt"`
}

func PayloadHash(raw []byte) string {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) == nil {
		if canonical, err := json.Marshal(value); err == nil {
			raw = canonical
		}
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type FundingResult struct {
	PositionID string `json:"positionId"`
	State      string `json:"state"`
}

type UnitManifestEntry struct {
	ID        string `json:"id"`
	LineID    string `json:"lineId"`
	UnitIndex int    `json:"unitIndex"`
}

type CancellationContext struct {
	ActionRequest
	UserID           string    `json:"userId"`
	IssuedAt         time.Time `json:"issuedAt"`
	FundingState     string    `json:"fundingState"`
	OwnerState       string    `json:"ownerState"`
	UnitIDs          []string  `json:"unitIds"`
	ReservationID    string    `json:"reservationId,omitempty"`
	UndeliveredUnits int       `json:"undeliveredUnits"`
}

type CancellationReservation struct {
	ReservationID    string `json:"reservationId"`
	UndeliveredUnits int    `json:"undeliveredUnits"`
}

// OwnerActionContext binds a business request to facts observed at issuance.
// Only the reducer constructs it. Each recipient validates its own rows.
type OwnerActionContext struct {
	ActionRequest
	MerchantState   string   `json:"merchantState"`
	FundingState    string   `json:"fundingState"`
	HasCompensation bool     `json:"hasCompensation"`
	UnitIDs         []string `json:"unitIds,omitempty"`
}

const EffectRetryDelivery = "owner.retry_effect_delivery.v1"
