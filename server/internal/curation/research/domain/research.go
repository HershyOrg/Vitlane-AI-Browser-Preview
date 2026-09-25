package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"golang.org/x/text/unicode/norm"
)

// MaxConcurrentUserActions bounds one user's concurrent CurationActions.
const MaxConcurrentUserActions = 3

var (
	ErrRoundNotFound       = errors.New("RESEARCH_TASK_NOT_FOUND")
	ErrRoundClosed         = errors.New("RESEARCH_ROUND_CLOSED")
	ErrRoundInvalid        = errors.New("RESEARCH_ROUND_INVALID")
	ErrContextMismatch     = errors.New("RESEARCH_CONTEXT_MISMATCH")
	ErrIdempotencyConflict = errors.New("RESEARCH_IDEMPOTENCY_CONFLICT")
	ErrCandidateInvalid    = errors.New("CANDIDATE_INVALID")
	ErrCandidatePrice      = errors.New("CANDIDATE_PRICE_OUT_OF_RANGE")
	ErrCandidateCurrency   = errors.New("CANDIDATE_CURRENCY_MISMATCH")
	ErrCandidateDuplicate  = errors.New("CANDIDATE_DUPLICATE")
	ErrFeedbackNotFound    = errors.New("RESEARCH_FEEDBACK_NOT_FOUND")
	ErrFeedbackMismatch    = errors.New("RESEARCH_FEEDBACK_MISMATCH")
	ErrFeedbackInvalid     = errors.New("RESEARCH_FEEDBACK_INVALID")
	// ErrInvalidResearchCommand rejects a command whose required identifiers or
	// version expectations are missing or inconsistent.
	ErrInvalidResearchCommand = errors.New("RESEARCH_COMMAND_INVALID")
	// The action limits a user experiences. A plan runs one action at a time
	// and a user runs at most MaxConcurrentUserActions across their plans.
	ErrResearchActionInProgress = errors.New("CURATION_EXPANSION_IN_PROGRESS")
	ErrTooManyActiveActions     = errors.New("CURATION_TOO_MANY_ACTIVE_ACTIONS")

	ErrConfigurationInvalid  = errors.New("CANDIDATE_CONFIGURATION_INVALID")
	ErrConfigurationNotFound = errors.New("CANDIDATE_CONFIGURATION_NOT_FOUND")
)

const (
	ContextSchemaV1            = "vitlane.research-context.v1"
	ContextSchemaV3            = "vitlane.research-context.v3"
	FeedbackSchemaV3           = "vitlane.research-feedback.v3"
	CandidateHashSchemaV3      = "vitlane.candidate.v3"
	VariantDiscoverySchemaV1   = "vitlane.variant-discovery.v1"
	ConfigurationSchemaV1      = "vitlane.candidate-configuration.v1"
	OrderabilitySchemaV1       = "vitlane.orderability.v1"
	MaxCandidatesPerResearch   = 20
	MaxCandidateImageURLLength = 2048
	CatalogProviderShopifyUCP  = "SHOPIFY_UCP_GLOBAL"
)

type RoundStatus string

const (
	RoundStatusRequested    RoundStatus = "REQUESTED"
	RoundStatusResultsReady RoundStatus = "RESULTS_READY"
	RoundStatusNoResults    RoundStatus = "NO_RESULTS"
	RoundStatusFailed       RoundStatus = "FAILED"
	RoundStatusCancelled    RoundStatus = "CANCELLED"
	RoundStatusSuperseded   RoundStatus = "SUPERSEDED"
)

type ResearchRound struct {
	ID                  string          `json:"id"`
	ShoppingSessionID   string          `json:"shoppingSessionId"`
	UserID              string          `json:"userId"`
	RoundNumber         int             `json:"roundNumber"`
	ContextSchema       string          `json:"contextSchema"`
	ContextVersion      int64           `json:"contextVersion"`
	ContextHash         string          `json:"contextHash"`
	ContextSnapshot     json.RawMessage `json:"-"`
	Status              RoundStatus     `json:"status"`
	FailureReasonCode   string          `json:"failureReasonCode,omitempty"`
	FailureRetryable    *bool           `json:"failureRetryable,omitempty"`
	FirstDiscoveredAt   *time.Time      `json:"firstDiscoveredAt,omitempty"`
	FirstContextReadAt  *time.Time      `json:"firstContextReadAt,omitempty"`
	LastAgentActivityAt *time.Time      `json:"lastAgentActivityAt,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
	CompletedAt         *time.Time      `json:"completedAt,omitempty"`
}

// Fail closes a requested round without publishing a CandidatePool. The
// IntelligenceJob remains the execution authority and carries the same safe
// reason; duplicating it here lets Research release its Session projection
// without making the UI infer product lifecycle from a foreign aggregate.
func (r *ResearchRound) Fail(
	reasonCode string,
	retryable bool,
	now time.Time,
) error {
	reasonCode = strings.TrimSpace(reasonCode)
	if r.Status != RoundStatusRequested {
		return ErrRoundClosed
	}
	if reasonCode == "" || len(reasonCode) > 200 {
		return ErrRoundInvalid
	}
	r.Status = RoundStatusFailed
	r.FailureReasonCode = reasonCode
	r.FailureRetryable = &retryable
	r.CompletedAt = &now
	return nil
}

func NewRound(
	id, sessionID, userID string,
	roundNumber int,
	context json.RawMessage,
	now time.Time,
) (ResearchRound, error) {
	if roundNumber < 1 || len(context) == 0 {
		return ResearchRound{}, ErrRoundInvalid
	}
	hash := sha256.Sum256(context)
	return ResearchRound{
		ID: id, ShoppingSessionID: sessionID, UserID: userID,
		RoundNumber: roundNumber, ContextSchema: ContextSchemaV3,
		ContextVersion: 1, ContextHash: hex.EncodeToString(hash[:]),
		ContextSnapshot: context, Status: RoundStatusRequested, CreatedAt: now,
	}, nil
}

// CompleteFromCandidatePool closes a ResearchRound against the canonical
// CandidatePool instead of the retired ResearchSubmission/Candidate graph.
// The pool finalize and this transition are committed in one PostgreSQL
// transaction by the Research application service, so a cancelled or stale
// round can never publish a late Candidate set.
func (r *ResearchRound) CompleteFromCandidatePool(
	hasResults bool,
	now time.Time,
) error {
	if r.Status != RoundStatusRequested {
		return ErrRoundClosed
	}
	if hasResults {
		r.Status = RoundStatusResultsReady
	} else {
		r.Status = RoundStatusNoResults
	}
	r.CompletedAt = &now
	return nil
}

func (r *ResearchRound) Cancel(now time.Time) error {
	if r.Status != RoundStatusRequested {
		return ErrRoundClosed
	}
	r.Status = RoundStatusCancelled
	r.CompletedAt = &now
	return nil
}

func (r *ResearchRound) Supersede(now time.Time) (RoundStatus, error) {
	if r.Status != RoundStatusResultsReady && r.Status != RoundStatusNoResults {
		return "", ErrRoundClosed
	}
	previous := r.Status
	r.Status = RoundStatusSuperseded
	r.CompletedAt = &now
	return previous, nil
}

func (r *ResearchRound) Restore(status RoundStatus, now time.Time) error {
	if r.Status != RoundStatusSuperseded ||
		(status != RoundStatusResultsReady && status != RoundStatusNoResults) {
		return ErrRoundClosed
	}
	r.Status = status
	r.CompletedAt = &now
	return nil
}

type CatalogVariantSentiment string

const (
	CatalogVariantSentimentNone    CatalogVariantSentiment = "NONE"
	CatalogVariantSentimentLike    CatalogVariantSentiment = "LIKE"
	CatalogVariantSentimentDislike CatalogVariantSentiment = "DISLIKE"
)

type InteractionSnapshot struct {
	Variants []CatalogVariantInteractionSnapshot `json:"variants"`
	Products []ProductInteractionSnapshot        `json:"products,omitempty"`
}

type ProductInteractionSnapshot struct {
	CandidateID string           `json:"candidateId"`
	ProductRef  SourceProductRef `json:"productRef"`
	Pinned      bool             `json:"pinned"`
	Sentiment   string           `json:"sentiment"`
}

// CatalogVariantInteractionSnapshot carries user-owned preference context into
// a re-research round without copying price, inventory, title, URL, or any
// other Shopify fact. CandidatePool and the provider observation remain the
// only product authority.
type CatalogVariantInteractionSnapshot struct {
	CandidateID string                  `json:"candidateId"`
	VariantID   string                  `json:"variantId"`
	Pinned      bool                    `json:"pinned"`
	Sentiment   CatalogVariantSentiment `json:"sentiment"`
}

type FeedbackStatus string

const (
	FeedbackStatusActive    FeedbackStatus = "ACTIVE"
	FeedbackStatusCancelled FeedbackStatus = "CANCELLED"
)

type ResearchFeedback struct {
	ID                  string          `json:"id"`
	ShoppingSessionID   string          `json:"shoppingSessionId"`
	PreviousRoundID     string          `json:"previousRoundId"`
	NextRoundID         string          `json:"nextRoundId"`
	UserID              string          `json:"userId"`
	Feedback            string          `json:"feedback"`
	InteractionSnapshot json.RawMessage `json:"interactionSnapshot"`
	SchemaVersion       string          `json:"schemaVersion"`
	FeedbackVersion     int64           `json:"feedbackVersion"`
	FeedbackHash        string          `json:"feedbackHash"`
	PreviousRoundStatus RoundStatus     `json:"previousRoundStatus"`
	Status              FeedbackStatus  `json:"status"`
	ClientRequestID     string          `json:"clientRequestId"`
	RequestHash         string          `json:"-"`
	CreatedAt           time.Time       `json:"createdAt"`
	CancelledAt         *time.Time      `json:"cancelledAt,omitempty"`
}

type FeedbackEnvelope struct {
	SchemaVersion     string              `json:"schemaVersion"`
	FeedbackVersion   int64               `json:"feedbackVersion"`
	ShoppingSessionID string              `json:"shoppingSessionId"`
	PreviousRoundID   string              `json:"previousRoundId"`
	NextRoundID       string              `json:"nextRoundId"`
	Feedback          string              `json:"feedback"`
	Interactions      InteractionSnapshot `json:"interactions"`
}

func NewFeedback(
	id, sessionID, previousRoundID, nextRoundID, userID, feedback,
	clientRequestID, requestHash string,
	previousStatus RoundStatus,
	snapshot InteractionSnapshot,
	now time.Time,
) (ResearchFeedback, error) {
	feedback = strings.TrimSpace(feedback)
	if id == "" || sessionID == "" || previousRoundID == "" || nextRoundID == "" ||
		userID == "" || clientRequestID == "" || requestHash == "" || len(feedback) > 2000 ||
		(previousStatus != RoundStatusResultsReady && previousStatus != RoundStatusNoResults) {
		return ResearchFeedback{}, ErrFeedbackInvalid
	}
	snapshot.Variants = nonNilCatalogVariantInteractionSnapshots(snapshot.Variants)
	envelope := FeedbackEnvelope{
		SchemaVersion: FeedbackSchemaV3, FeedbackVersion: 1,
		ShoppingSessionID: sessionID, PreviousRoundID: previousRoundID,
		NextRoundID: nextRoundID, Feedback: feedback, Interactions: snapshot,
	}
	hash, _, err := HashJSON(envelope)
	if err != nil {
		return ResearchFeedback{}, err
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return ResearchFeedback{}, err
	}
	return ResearchFeedback{
		ID: id, ShoppingSessionID: sessionID, PreviousRoundID: previousRoundID,
		NextRoundID: nextRoundID, UserID: userID, Feedback: feedback,
		InteractionSnapshot: snapshotJSON, SchemaVersion: FeedbackSchemaV3,
		FeedbackVersion: 1, FeedbackHash: hash, PreviousRoundStatus: previousStatus,
		Status: FeedbackStatusActive, ClientRequestID: clientRequestID,
		RequestHash: requestHash, CreatedAt: now,
	}, nil
}

func (f *ResearchFeedback) Cancel(now time.Time) error {
	if f.Status != FeedbackStatusActive {
		return ErrFeedbackInvalid
	}
	f.Status = FeedbackStatusCancelled
	f.CancelledAt = &now
	return nil
}

func (f ResearchFeedback) Envelope() (FeedbackEnvelope, error) {
	var snapshot InteractionSnapshot
	if err := json.Unmarshal(f.InteractionSnapshot, &snapshot); err != nil {
		return FeedbackEnvelope{}, err
	}
	return FeedbackEnvelope{
		SchemaVersion: f.SchemaVersion, FeedbackVersion: f.FeedbackVersion,
		ShoppingSessionID: f.ShoppingSessionID, PreviousRoundID: f.PreviousRoundID,
		NextRoundID: f.NextRoundID, Feedback: f.Feedback, Interactions: snapshot,
	}, nil
}

func nonNilCatalogVariantInteractionSnapshots(
	value []CatalogVariantInteractionSnapshot,
) []CatalogVariantInteractionSnapshot {
	if value == nil {
		return []CatalogVariantInteractionSnapshot{}
	}
	return value
}

type VariantDiscoveryStatus string

const (
	VariantDiscoveryNotApplicable      VariantDiscoveryStatus = "NOT_APPLICABLE"
	VariantDiscoveryObservedPartial    VariantDiscoveryStatus = "OBSERVED_PARTIAL"
	VariantDiscoveryCompleteUnverified VariantDiscoveryStatus = "COMPLETE_UNVERIFIED"
	VariantDiscoveryProviderVerified   VariantDiscoveryStatus = "PROVIDER_VERIFIED"
	VariantDiscoveryUnknown            VariantDiscoveryStatus = "UNKNOWN"
)

type VariantInputKind string

const (
	VariantInputEnum        VariantInputKind = "ENUM"
	VariantInputEnumOrValue VariantInputKind = "ENUM_OR_VALUE"
	VariantInputValue       VariantInputKind = "VALUE"
)

type VariantFieldSource string

const (
	VariantFieldAgentObservation VariantFieldSource = "AGENT_OBSERVATION"
	VariantFieldProvider         VariantFieldSource = "PROVIDER"
	VariantFieldUser             VariantFieldSource = "USER"
)

type VariantKnownValue struct {
	Value string `json:"value" jsonschema:"Exact value observed from the product page or structured provider; never invent a value."`
	Label string `json:"label" jsonschema:"Human-readable label exactly as observed."`
}

type VariantField struct {
	Key             string                 `json:"key" jsonschema:"Stable lowercase key for an actually observed option field."`
	Label           string                 `json:"label"`
	InputKind       VariantInputKind       `json:"inputKind" jsonschema:"ENUM only when known values are closed; use ENUM_OR_VALUE for partial observations and VALUE when no enum is known."`
	Required        bool                   `json:"required"`
	KnownValues     []VariantKnownValue    `json:"knownValues" jsonschema:"Only values directly observed; an incomplete list must use ENUM_OR_VALUE."`
	Source          VariantFieldSource     `json:"source" jsonschema:"AGENT_OBSERVATION or PROVIDER for intelligence submissions."`
	DiscoveryStatus VariantDiscoveryStatus `json:"discoveryStatus"`
}

type ProviderVariantRef struct {
	Provider           string `json:"provider" jsonschema:"Provider that actually supplied this structured identifier."`
	SchemaVersion      string `json:"schemaVersion"`
	SellerID           string `json:"sellerId,omitempty"`
	ProductID          string `json:"productId"`
	VariantID          string `json:"variantId"`
	OfferID            string `json:"offerId,omitempty"`
	ProviderDetailHash string `json:"providerDetailHash" jsonschema:"Hash of the immutable structured provider detail evidence."`
}

type VariantDiscoveryEvidence struct {
	Summary      string   `json:"summary"`
	SourceURLs   []string `json:"sourceUrls"`
	EvidenceHash string   `json:"evidenceHash,omitempty"`
}

type VariantDiscovery struct {
	SchemaVersion       string                   `json:"schemaVersion"`
	Status              VariantDiscoveryStatus   `json:"status" jsonschema:"Use UNKNOWN or OBSERVED_PARTIAL whenever the option structure or combinations are uncertain. Never upgrade based on inference."`
	Fields              []VariantField           `json:"fields" jsonschema:"Best-effort observed fields. Never guess a missing field or value."`
	ProviderVariantRefs []ProviderVariantRef     `json:"providerVariantRefs" jsonschema:"Optional structured IDs only when returned by the provider; never derive or fabricate them."`
	ObservedAt          time.Time                `json:"observedAt,omitempty"`
	Evidence            VariantDiscoveryEvidence `json:"evidence"`
}

type VariantSelectionSource string

const (
	VariantSelectionUserConfirmed VariantSelectionSource = "USER_CONFIRMED"
	VariantSelectionUser          VariantSelectionSource = "USER"
)

type VariantSelection struct {
	Key        string                 `json:"key"`
	Label      string                 `json:"label"`
	Value      string                 `json:"value"`
	ValueLabel string                 `json:"valueLabel"`
	Source     VariantSelectionSource `json:"source"`
}

type CandidateConfigurationInput struct {
	Fields            []VariantField    `json:"fields"`
	Selections        map[string]string `json:"selections"`
	ConfirmsNoOptions bool              `json:"confirmsNoOptions"`
}

type CandidateConfiguration struct {
	ID                    string             `json:"id"`
	ConfigurationSequence int64              `json:"configurationSequence"`
	ShoppingSessionID     string             `json:"shoppingSessionId"`
	CandidateID           string             `json:"candidateId"`
	UserID                string             `json:"userId"`
	SchemaVersion         string             `json:"schemaVersion"`
	Fields                []VariantField     `json:"fields"`
	Selections            []VariantSelection `json:"selections"`
	ConfirmsNoOptions     bool               `json:"confirmsNoOptions"`
	ConfigurationHash     string             `json:"configurationHash"`
	CreatedAt             time.Time          `json:"createdAt"`
}

func NewCandidateConfiguration(
	id, sessionID, candidateID, candidateHash, userID string,
	discovery VariantDiscovery,
	input CandidateConfigurationInput,
	now time.Time,
) (CandidateConfiguration, error) {
	if id == "" || sessionID == "" || candidateID == "" || candidateHash == "" ||
		userID == "" {
		return CandidateConfiguration{}, ErrConfigurationInvalid
	}
	fields, err := canonicalConfigurationFields(input.Fields, discovery)
	if err != nil {
		return CandidateConfiguration{}, err
	}
	if input.ConfirmsNoOptions {
		if len(fields) != 0 || len(input.Selections) != 0 {
			return CandidateConfiguration{}, ErrConfigurationInvalid
		}
	} else if len(fields) == 0 {
		return CandidateConfiguration{}, ErrConfigurationInvalid
	}
	fieldByKey := make(map[string]VariantField, len(fields))
	for _, field := range fields {
		fieldByKey[field.Key] = field
	}
	selectionByKey := make(map[string]string, len(input.Selections))
	for key, value := range input.Selections {
		key = canonicalVariantKey(key)
		if _, ok := fieldByKey[key]; !ok {
			return CandidateConfiguration{}, ErrConfigurationInvalid
		}
		if _, duplicate := selectionByKey[key]; duplicate {
			return CandidateConfiguration{}, ErrConfigurationInvalid
		}
		selectionByKey[key] = value
	}
	selections := make([]VariantSelection, 0, len(input.Selections))
	for _, field := range fields {
		raw, selected := selectionByKey[field.Key]
		value := strings.TrimSpace(norm.NFKC.String(raw))
		if field.Required && (!selected || value == "") {
			return CandidateConfiguration{}, ErrConfigurationInvalid
		}
		if !selected || value == "" {
			continue
		}
		valueLabel := value
		known := false
		for _, option := range field.KnownValues {
			if strings.EqualFold(option.Value, value) {
				value = option.Value
				valueLabel = option.Label
				known = true
				break
			}
		}
		if field.InputKind == VariantInputEnum && !known {
			return CandidateConfiguration{}, ErrConfigurationInvalid
		}
		source := VariantSelectionUserConfirmed
		if field.Source == VariantFieldUser {
			source = VariantSelectionUser
		}
		selections = append(selections, VariantSelection{
			Key: field.Key, Label: field.Label, Value: value,
			ValueLabel: valueLabel, Source: source,
		})
	}
	hash, _, err := HashJSON(struct {
		SchemaVersion     string             `json:"schemaVersion"`
		CandidateID       string             `json:"candidateId"`
		CandidateHash     string             `json:"candidateHash"`
		Fields            []VariantField     `json:"fields"`
		Selections        []VariantSelection `json:"selections"`
		ConfirmsNoOptions bool               `json:"confirmsNoOptions"`
	}{
		SchemaVersion: ConfigurationSchemaV1, CandidateID: candidateID,
		CandidateHash: candidateHash, Fields: fields, Selections: selections,
		ConfirmsNoOptions: input.ConfirmsNoOptions,
	})
	if err != nil {
		return CandidateConfiguration{}, err
	}
	return CandidateConfiguration{
		ID: id, ShoppingSessionID: sessionID, CandidateID: candidateID,
		UserID:        userID,
		SchemaVersion: ConfigurationSchemaV1, Fields: fields,
		Selections: selections, ConfirmsNoOptions: input.ConfirmsNoOptions,
		ConfigurationHash: hash, CreatedAt: now,
	}, nil
}

func canonicalConfigurationFields(
	input []VariantField,
	discovery VariantDiscovery,
) ([]VariantField, error) {
	discovered := make(map[string]VariantField, len(discovery.Fields))
	for _, field := range discovery.Fields {
		discovered[canonicalVariantKey(field.Key)] = field
	}
	result := make([]VariantField, 0, len(input))
	seen := map[string]struct{}{}
	for _, field := range input {
		field.Key = canonicalVariantKey(field.Key)
		field.Label = strings.TrimSpace(norm.NFKC.String(field.Label))
		if !validVariantKey(field.Key) || field.Label == "" ||
			len(field.Label) > 100 || !validVariantInputKind(field.InputKind) {
			return nil, ErrConfigurationInvalid
		}
		if _, exists := seen[field.Key]; exists {
			return nil, ErrConfigurationInvalid
		}
		seen[field.Key] = struct{}{}
		field.KnownValues = canonicalKnownValues(field.KnownValues)
		if field.InputKind == VariantInputEnum && len(field.KnownValues) == 0 {
			return nil, ErrConfigurationInvalid
		}
		field.Source = VariantFieldUser
		field.DiscoveryStatus = VariantDiscoveryUnknown
		if observed, ok := discovered[field.Key]; ok &&
			equivalentVariantField(field, observed) {
			field.Source = observed.Source
			field.DiscoveryStatus = observed.DiscoveryStatus
		}
		result = append(result, field)
	}
	slices.SortFunc(result, func(left, right VariantField) int {
		return strings.Compare(left.Key, right.Key)
	})
	return result, nil
}

func canonicalKnownValues(values []VariantKnownValue) []VariantKnownValue {
	result := make([]VariantKnownValue, 0, len(values))
	seen := map[string]struct{}{}
	for _, known := range values {
		known.Value = strings.TrimSpace(norm.NFKC.String(known.Value))
		known.Label = strings.TrimSpace(norm.NFKC.String(known.Label))
		if known.Value == "" || known.Label == "" {
			continue
		}
		key := strings.ToLower(known.Value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, known)
	}
	slices.SortFunc(result, func(left, right VariantKnownValue) int {
		return strings.Compare(strings.ToLower(left.Value), strings.ToLower(right.Value))
	})
	return result
}

func equivalentVariantField(candidate, observed VariantField) bool {
	observed.Key = canonicalVariantKey(observed.Key)
	observed.Label = strings.TrimSpace(norm.NFKC.String(observed.Label))
	observed.KnownValues = canonicalKnownValues(observed.KnownValues)
	return candidate.Key == observed.Key && candidate.Label == observed.Label &&
		candidate.InputKind == observed.InputKind && candidate.Required == observed.Required &&
		slices.Equal(candidate.KnownValues, observed.KnownValues)
}

func canonicalVariantKey(value string) string {
	return strings.ToLower(strings.TrimSpace(norm.NFKC.String(value)))
}

func validVariantInputKind(value VariantInputKind) bool {
	return value == VariantInputEnum || value == VariantInputEnumOrValue ||
		value == VariantInputValue
}

func validVariantKey(value string) bool {
	if len(value) == 0 || len(value) > 64 ||
		value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

type Evidence struct {
	Summary         string           `json:"summary"`
	MatchedCriteria []string         `json:"matchedCriteria"`
	Tradeoffs       []string         `json:"tradeoffs"`
	SourceURLs      []string         `json:"sourceUrls"`
	Catalog         *CatalogEvidence `json:"catalog,omitempty"`
}

type CatalogEvidence struct {
	ObservationID   string `json:"observationId"`
	Provider        string `json:"provider"`
	ProtocolVersion string `json:"protocolVersion"`
	ProductID       string `json:"productId"`
	VariantID       string `json:"variantId"`
	SellerID        string `json:"sellerId"`
	SellerDomain    string `json:"sellerDomain"`
	PayloadHash     string `json:"payloadHash"`
}

type CatalogObservation struct {
	ID                string             `json:"id"`
	ResearchRoundID   string             `json:"researchRoundId"`
	UserID            string             `json:"userId"`
	IntelligenceJobID string             `json:"intelligenceJobId,omitempty"`
	Provider          string             `json:"provider"`
	ProtocolVersion   string             `json:"protocolVersion"`
	ProductID         string             `json:"productId"`
	VariantID         string             `json:"variantId"`
	SellerID          string             `json:"sellerId"`
	SellerName        string             `json:"sellerName"`
	SellerDomain      string             `json:"sellerDomain"`
	ProductURL        string             `json:"productUrl"`
	Name              string             `json:"name"`
	Description       string             `json:"description"`
	ImageURL          string             `json:"imageUrl,omitempty"`
	Price             shareddomain.Money `json:"price"`
	Available         bool               `json:"available"`
	PayloadHash       string             `json:"payloadHash"`
	ObservedAt        time.Time          `json:"observedAt"`
	ExpiresAt         time.Time          `json:"expiresAt"`
}

type Eligibility struct {
	HardChecks     string   `json:"hardChecks"`
	SemanticReview string   `json:"semanticReview"`
	ReasonCodes    []string `json:"reasonCodes"`
	PolicyVersion  string   `json:"policyVersion"`
}

type Orderability struct {
	SchemaVersion    string   `json:"schemaVersion"`
	ProviderKind     string   `json:"providerKind"`
	ExecutionMode    string   `json:"executionMode"`
	ExternalEffect   string   `json:"externalEffect"`
	LiveOrderability string   `json:"liveOrderability"`
	SettlementStatus string   `json:"settlementStatus"`
	Status           string   `json:"status"`
	ReasonCodes      []string `json:"reasonCodes"`
}

type Candidate struct {
	ID                   string             `json:"id"`
	ResearchSubmissionID string             `json:"researchSubmissionId"`
	ShoppingSessionID    string             `json:"shoppingSessionId"`
	ProductURL           string             `json:"productUrl"`
	MerchantDomain       string             `json:"merchantDomain"`
	Category             string             `json:"category"`
	Name                 string             `json:"name"`
	Description          string             `json:"description"`
	ImageURL             string             `json:"imageUrl,omitempty"`
	Price                shareddomain.Money `json:"price"`
	VariantDiscovery     VariantDiscovery   `json:"variantDiscovery"`
	Evidence             Evidence           `json:"evidence"`
	ObservedAt           time.Time          `json:"observedAt"`
	OrderSupport         string             `json:"orderSupport"`
	Orderability         Orderability       `json:"orderability"`
	Eligibility          Eligibility        `json:"eligibility"`
	CandidateHashSchema  string             `json:"candidateHashSchema"`
	CandidateHash        string             `json:"candidateHash"`
	OrderIndex           int                `json:"orderIndex"`
	CreatedAt            time.Time          `json:"createdAt"`
}

func CanonicalHTTPSURL(value string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return "", "", fmt.Errorf("%w: product URL must be HTTPS without credentials", ErrCandidateInvalid)
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return "", "", fmt.Errorf(
			"%w: product URL must use the standard HTTPS port",
			ErrCandidateInvalid,
		)
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if !isPublicHostname(host, parsed.Host) {
		return "", "", fmt.Errorf(
			"%w: product URL must use a public hostname",
			ErrCandidateInvalid,
		)
	}
	parsed.Fragment = ""
	parsed.Scheme = "https"
	parsed.Host = host
	return parsed.String(), host, nil
}

func CanonicalCandidateImageURL(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) > MaxCandidateImageURLLength {
		return "", fmt.Errorf(
			"%w: image URL must be between 1 and %d bytes",
			ErrCandidateInvalid,
			MaxCandidateImageURLLength,
		)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return "", fmt.Errorf(
			"%w: image URL must be HTTPS without credentials",
			ErrCandidateInvalid,
		)
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return "", fmt.Errorf(
			"%w: image URL must use the standard HTTPS port",
			ErrCandidateInvalid,
		)
	}

	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if !isPublicHostname(host, parsed.Host) {
		return "", fmt.Errorf(
			"%w: image URL must use a non-local hostname",
			ErrCandidateInvalid,
		)
	}

	parsed.Fragment = ""
	parsed.Scheme = "https"
	parsed.Host = host
	return parsed.String(), nil
}

func isPublicHostname(host, originalHost string) bool {
	ipHost := host
	if zoneIndex := strings.LastIndex(ipHost, "%"); zoneIndex >= 0 {
		ipHost = ipHost[:zoneIndex]
	}
	return host != "" &&
		strings.Contains(host, ".") &&
		host != "localhost" &&
		!strings.HasSuffix(host, ".localhost") &&
		host != "local" &&
		!strings.HasSuffix(host, ".local") &&
		!strings.HasSuffix(host, ".internal") &&
		!strings.HasSuffix(host, ".lan") &&
		!strings.HasPrefix(originalHost, "[") &&
		net.ParseIP(ipHost) == nil
}

func HashJSON(value any) (string, []byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", nil, err
	}
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:]), payload, nil
}
