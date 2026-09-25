package domain

import (
	"errors"
	"strings"
)

var (
	ErrModelUnknown       = errors.New("MANAGED_RUNNER_MODEL_UNKNOWN")
	ErrModelRegistryEmpty = errors.New("MANAGED_RUNNER_MODEL_REGISTRY_EMPTY")
	ErrUserDailyLimit     = errors.New("MANAGED_RUNNER_USER_DAILY_LIMIT")
	ErrServerDailyLimit   = errors.New("MANAGED_RUNNER_SERVER_DAILY_LIMIT")
	ErrModelResponse      = errors.New("MANAGED_RUNNER_MODEL_RESPONSE_INVALID")
	ErrReservationExists  = errors.New("MANAGED_RUNNER_RESERVATION_REQUEST_EXISTS")
	ErrCatalogUnavailable = errors.New("MANAGED_RUNNER_CATALOG_UNAVAILABLE")
	ErrDisabled           = errors.New("MANAGED_RUNNER_DISABLED")

	// GAP-024 operator reconciliation of UNKNOWN reservations.
	ErrReservationNotFound   = errors.New("MANAGED_RUNNER_RESERVATION_NOT_FOUND")
	ErrReservationNotUnknown = errors.New("MANAGED_RUNNER_RESERVATION_NOT_UNKNOWN")
)

// Reason codes surfaced to the Web projection. They carry no free text so a
// failure never leaks intent, prompt, or model output.
const (
	ReasonUserDailyLimit    = "USER_DAILY_LIMIT"
	ReasonServerDailyLimit  = "SERVER_DAILY_LIMIT"
	ReasonModelUnavailable  = "MODEL_UNAVAILABLE"
	ReasonModelResponse     = "MODEL_RESPONSE_INVALID"
	ReasonCatalogEmpty      = "CATALOG_NO_RESULTS"
	ReasonCatalogFailed     = "CATALOG_UNAVAILABLE"
	ReasonProposalRejected  = "PROPOSAL_REJECTED"
	ReasonSubmissionInvalid = "SUBMISSION_REJECTED"
)

// Model is one selectable entry in the routing registry. Prices are USD micros
// (1e-6 USD) per million tokens so cost arithmetic stays in integers and never
// depends on float rounding.
type Model struct {
	SupportsLowImages      bool
	LowImageInputTokens    int64
	AssessmentOutputTokens int64
	Key                    string
	Label                  string
	ProviderModelID        string
	InputMicrosPerMTok     int64
	OutputMicrosPerMTok    int64
	// MaxOutputTokens must cover reasoning as well as the visible answer. The
	// gpt-5 family spends reasoning tokens first and bills them as output, so a
	// budget sized only to the JSON gets consumed before any JSON is emitted
	// and the call returns finish_reason=length.
	MaxOutputTokens int64
	// ReasoningEffort keeps that spend bounded. Empty means the provider
	// default, which for a reasoning model is considerably more expensive.
	ReasoningEffort string
}

func (m Model) Valid() bool {
	return strings.TrimSpace(m.Key) != "" &&
		strings.TrimSpace(m.ProviderModelID) != "" &&
		m.InputMicrosPerMTok >= 0 && m.OutputMicrosPerMTok >= 0 &&
		m.MaxOutputTokens > 0
}

// Cost converts a token count into USD micros, rounding up so a partially
// consumed million never settles as zero.
func (m Model) Cost(usage TokenUsage) int64 {
	return divideCeil(usage.InputTokens*m.InputMicrosPerMTok, 1_000_000) +
		divideCeil(usage.OutputTokens*m.OutputMicrosPerMTok, 1_000_000)
}

// WorstCost is what the ledger reserves before a call. The prompt estimate is
// supplied by the caller because only the pipeline knows how large the context
// it is about to send is.
func (m Model) WorstCost(estimatedInputTokens int64) int64 {
	if estimatedInputTokens < 0 {
		estimatedInputTokens = 0
	}
	return m.Cost(TokenUsage{
		InputTokens:  estimatedInputTokens,
		OutputTokens: m.MaxOutputTokens,
	})
}

func divideCeil(value, divisor int64) int64 {
	if value <= 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}

type TokenUsage struct {
	InputTokens  int64
	OutputTokens int64
}

// DefaultModels is the ADR-0032 routing registry.
//
// Prices are USD micros per million tokens. Cached-input discounts are real but
// deliberately not modelled: the ledger reserves before the call, when nothing
// yet knows how much of the prompt will hit cache. Reserving at the uncached
// price over-reserves, which is the safe direction for a hard cap — the
// difference is returned at settle time.
//
// MaxOutputTokens bounds ordinary step reservations. AssessmentModel derives
// the assessment reservation from the actual candidate and axis counts.
func DefaultModels() []Model {
	return []Model{
		{
			AssessmentOutputTokens: 4000, Key: "gpt-5-nano",
			Label:               "빠름 · gpt-5-nano",
			ProviderModelID:     "gpt-5-nano",
			InputMicrosPerMTok:  50_000,  // $0.05 / 1M
			OutputMicrosPerMTok: 400_000, // $0.40 / 1M
			MaxOutputTokens:     8_000,
			ReasoningEffort:     "low",
		},
		{
			SupportsLowImages: true, LowImageInputTokens: 320, AssessmentOutputTokens: 4000,
			Key:                 "gpt-5.6-luna",
			Label:               "정밀 · gpt-5.6-luna",
			ProviderModelID:     "gpt-5.6-luna",
			InputMicrosPerMTok:  200_000,   // $0.20 / 1M
			OutputMicrosPerMTok: 1_200_000, // $1.20 / 1M
			MaxOutputTokens:     8_000,
			ReasoningEffort:     "low",
		},
	}
}

// DefaultModelKey favors the higher-quality Luna path for a user who has not
// saved a model preference. Daily caps remain the independent spend boundary.
const DefaultModelKey = "gpt-5.6-luna"

// Registry is the closed set of models the Web may choose from. Web only ever
// sees Key and Label, so swapping providers or model IDs is a config change.
type Registry struct {
	models     []Model
	defaultKey string
}

func NewRegistry(models []Model, defaultKey string) (Registry, error) {
	if len(models) == 0 {
		return Registry{}, ErrModelRegistryEmpty
	}
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if !model.Valid() {
			return Registry{}, ErrModelUnknown
		}
		if _, exists := seen[model.Key]; exists {
			return Registry{}, ErrModelUnknown
		}
		seen[model.Key] = struct{}{}
	}
	if defaultKey == "" {
		defaultKey = models[0].Key
	}
	if _, exists := seen[defaultKey]; !exists {
		return Registry{}, ErrModelUnknown
	}
	return Registry{models: models, defaultKey: defaultKey}, nil
}

func (r Registry) Models() []Model {
	return append([]Model(nil), r.models...)
}

func (r Registry) DefaultKey() string {
	return r.defaultKey
}

func (r Registry) Lookup(key string) (Model, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		key = r.defaultKey
	}
	for _, model := range r.models {
		if model.Key == key {
			return model, nil
		}
	}
	return Model{}, ErrModelUnknown
}

// AssessmentModel reserves output against actual evaluation width. The model
// token ceiling remains separate from CandidateUpdateSize and is not a goal.
//
// The per-candidate allowance is calibrated on a 2026-09-17 live measurement:
// with eight axes the model wrote 356–462 output tokens per candidate over 29
// calls, so 160 + 80×axes (800 at eight axes) keeps a 1.7× margin above the
// observed maximum without reserving the 3× the previous formula did.
// AssessmentOutputTokens is the floor for tiny batches; it used to be 16,000,
// which made even a three-product supplement call reserve $0.019.
func (m Model) AssessmentModel(candidates, axes int) Model {
	if m.AssessmentOutputTokens <= 0 {
		return m
	}
	perCandidate := int64(160 + 80*min(8, max(1, axes)))
	m.MaxOutputTokens = min(int64(64000), max(m.AssessmentOutputTokens, int64(max(0, candidates))*perCandidate))
	return m
}

// MinimumResearchHeadroom is the least a research round can cost before its
// first paid provider call: one query reservation plus the smallest possible
// evaluation reservation. A user whose daily allowance cannot cover it is told
// so before any catalog API is charged for a round that could not finish.
func (m Model) MinimumResearchHeadroom() int64 {
	const promptEstimate = 2048
	return m.WorstCost(promptEstimate) + m.AssessmentModel(1, 1).WorstCost(promptEstimate)
}
