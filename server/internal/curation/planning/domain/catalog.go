package domain

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

const (
	ShoppingPlanSchemaV2               = "vitlane.shopping-plan.v2"
	ExplicitSettingsSnapshotSchemaV1   = "vitlane.explicit-settings.v1"
	IntentInterpretationSchemaV1       = "vitlane.intent-interpretation.v1"
	PlanningProposalSchemaV2           = "vitlane.planning-proposal.v2"
	MaximumIntentInterpretationTargets = 10
)

var (
	ErrCatalogPlanInvalid              = errors.New("PHASE8_SHOPPING_PLAN_INVALID")
	ErrExplicitSettingsSnapshotInvalid = errors.New(
		"EXPLICIT_SETTINGS_SNAPSHOT_INVALID",
	)
	ErrIntentInterpretationInvalid = errors.New(
		"INTENT_INTERPRETATION_INVALID",
	)
	ErrIntentInterpretationNotEnglish = errors.New(
		"INTENT_INTERPRETATION_NOT_ENGLISH",
	)
	ErrIntentConstraintProvenanceInvalid = errors.New(
		"INTENT_CONSTRAINT_PROVENANCE_INVALID",
	)
	ErrIntentConstraintInvented = errors.New(
		"INTENT_CONSTRAINT_INVENTED",
	)
	ErrPlanningProposalV2Invalid = errors.New(
		"PLANNING_PROPOSAL_V2_INVALID",
	)
)

// ExplicitSettingsSnapshotV2 is server-authored. The model never receives an
// opportunity to turn a qualitative preference into a numeric price bound.
type ExplicitSettingsSnapshotV2 struct {
	SchemaVersion            string                               `json:"schemaVersion"`
	ResearchPriceConstraint  shareddomain.ResearchPriceConstraint `json:"researchPriceConstraint"`
	PriceConstraintSourceRef string                               `json:"priceConstraintSourceRef,omitempty"`
	ContentHash              string                               `json:"contentHash"`
}

func NewExplicitSettingsSnapshotV2(
	constraint shareddomain.ResearchPriceConstraint,
	priceConstraintSourceRef string,
) (ExplicitSettingsSnapshotV2, error) {
	snapshot := ExplicitSettingsSnapshotV2{
		SchemaVersion:            ExplicitSettingsSnapshotSchemaV1,
		ResearchPriceConstraint:  constraint.Clone(),
		PriceConstraintSourceRef: strings.TrimSpace(priceConstraintSourceRef),
	}
	if err := snapshot.validateShape(); err != nil {
		return ExplicitSettingsSnapshotV2{}, err
	}
	hash, err := snapshot.calculateHash()
	if err != nil {
		return ExplicitSettingsSnapshotV2{}, err
	}
	snapshot.ContentHash = hash
	return snapshot, nil
}

func (s ExplicitSettingsSnapshotV2) Validate() error {
	if err := s.validateShape(); err != nil {
		return err
	}
	hash, err := s.calculateHash()
	if err != nil {
		return err
	}
	if s.ContentHash != hash {
		return fmt.Errorf(
			"%w: content hash mismatch",
			ErrExplicitSettingsSnapshotInvalid,
		)
	}
	return nil
}

func (s ExplicitSettingsSnapshotV2) validateShape() error {
	if s.SchemaVersion != ExplicitSettingsSnapshotSchemaV1 {
		return fmt.Errorf(
			"%w: schema version",
			ErrExplicitSettingsSnapshotInvalid,
		)
	}
	if err := s.ResearchPriceConstraint.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrExplicitSettingsSnapshotInvalid, err)
	}
	sourceRef := strings.TrimSpace(s.PriceConstraintSourceRef)
	if len(sourceRef) > 200 {
		return fmt.Errorf(
			"%w: source reference is too long",
			ErrExplicitSettingsSnapshotInvalid,
		)
	}
	if s.ResearchPriceConstraint.Kind ==
		shareddomain.ResearchPriceConstraintExplicit && sourceRef == "" {
		return fmt.Errorf(
			"%w: explicit price requires a user setting reference",
			ErrExplicitSettingsSnapshotInvalid,
		)
	}
	if s.ResearchPriceConstraint.Kind ==
		shareddomain.ResearchPriceConstraintNone && sourceRef != "" {
		return fmt.Errorf(
			"%w: NONE cannot claim a price source",
			ErrExplicitSettingsSnapshotInvalid,
		)
	}
	return nil
}

func (s ExplicitSettingsSnapshotV2) calculateHash() (string, error) {
	return shareddomain.CanonicalJSONHash(struct {
		SchemaVersion            string                               `json:"schemaVersion"`
		ResearchPriceConstraint  shareddomain.ResearchPriceConstraint `json:"researchPriceConstraint"`
		PriceConstraintSourceRef string                               `json:"priceConstraintSourceRef,omitempty"`
	}{
		SchemaVersion:           s.SchemaVersion,
		ResearchPriceConstraint: s.ResearchPriceConstraint,
		PriceConstraintSourceRef: strings.TrimSpace(
			s.PriceConstraintSourceRef,
		),
	})
}

type IntelligenceProviderSnapshotV2 struct {
	AgentMode     AgentMode `json:"agentMode"`
	ModelKey      string    `json:"modelKey,omitempty"`
	PolicyVersion string    `json:"policyVersion"`
}

func (s IntelligenceProviderSnapshotV2) Validate() error {
	if !s.AgentMode.Valid() || strings.TrimSpace(s.PolicyVersion) == "" ||
		len(strings.TrimSpace(s.PolicyVersion)) > 100 {
		return ErrCatalogPlanInvalid
	}
	modelKey := strings.TrimSpace(s.ModelKey)
	if (s.AgentMode == AgentModeManaged) != (modelKey != "") {
		return ErrCatalogPlanInvalid
	}
	return nil
}

// ShoppingPlanV2 is a dormant Phase 8 value model. It does not share the V1
// TotalBudget field, and therefore cannot manufacture a budget to satisfy a
// legacy database constraint.
type ShoppingPlanV2 struct {
	SchemaVersion                string                               `json:"schemaVersion"`
	ID                           ShoppingPlanID                       `json:"id"`
	UserID                       UserID                               `json:"userId"`
	ResearchContractVersion      shareddomain.ResearchContractVersion `json:"researchContractVersion"`
	OriginalIntent               string                               `json:"originalIntent"`
	PlanningMode                 PlanningMode                         `json:"planningMode"`
	ExecutionMode                ExecutionMode                        `json:"executionMode"`
	MarketContext                shareddomain.MarketContext           `json:"marketContext"`
	ResearchPriceConstraint      shareddomain.ResearchPriceConstraint `json:"researchPriceConstraint"`
	ExplicitSettingsSnapshot     ExplicitSettingsSnapshotV2           `json:"explicitSettingsSnapshot"`
	IntelligenceProviderSnapshot IntelligenceProviderSnapshotV2       `json:"intelligenceProviderSnapshot"`
	CreatedAt                    time.Time                            `json:"createdAt"`
	ContentHash                  string                               `json:"contentHash"`
}

type NewShoppingPlanV2Input struct {
	PlanID                       ShoppingPlanID
	UserID                       UserID
	OriginalIntent               string
	PlanningMode                 PlanningMode
	ExecutionMode                ExecutionMode
	MarketContext                shareddomain.MarketContext
	ResearchPriceConstraint      shareddomain.ResearchPriceConstraint
	ExplicitSettingsSnapshot     ExplicitSettingsSnapshotV2
	IntelligenceProviderSnapshot IntelligenceProviderSnapshotV2
	Now                          time.Time
}

func NewShoppingPlanV2(input NewShoppingPlanV2Input) (ShoppingPlanV2, error) {
	explicitSettings := input.ExplicitSettingsSnapshot
	explicitSettings.ResearchPriceConstraint =
		explicitSettings.ResearchPriceConstraint.Clone()
	plan := ShoppingPlanV2{
		SchemaVersion:                ShoppingPlanSchemaV2,
		ID:                           input.PlanID,
		UserID:                       input.UserID,
		ResearchContractVersion:      shareddomain.CatalogResearchContractVersionV2,
		OriginalIntent:               strings.TrimSpace(input.OriginalIntent),
		PlanningMode:                 input.PlanningMode,
		ExecutionMode:                input.ExecutionMode,
		MarketContext:                input.MarketContext,
		ResearchPriceConstraint:      input.ResearchPriceConstraint.Clone(),
		ExplicitSettingsSnapshot:     explicitSettings,
		IntelligenceProviderSnapshot: input.IntelligenceProviderSnapshot,
		CreatedAt:                    input.Now,
	}
	if err := plan.validateShape(); err != nil {
		return ShoppingPlanV2{}, err
	}
	hash, err := plan.calculateHash()
	if err != nil {
		return ShoppingPlanV2{}, err
	}
	plan.ContentHash = hash
	return plan, nil
}

func (p ShoppingPlanV2) Validate() error {
	if err := p.validateShape(); err != nil {
		return err
	}
	hash, err := p.calculateHash()
	if err != nil {
		return err
	}
	if p.ContentHash != hash {
		return fmt.Errorf("%w: content hash mismatch", ErrCatalogPlanInvalid)
	}
	return nil
}

func (p ShoppingPlanV2) validateShape() error {
	if p.SchemaVersion != ShoppingPlanSchemaV2 ||
		p.ResearchContractVersion !=
			shareddomain.CatalogResearchContractVersionV2 ||
		strings.TrimSpace(string(p.ID)) == "" ||
		strings.TrimSpace(string(p.UserID)) == "" ||
		strings.TrimSpace(p.OriginalIntent) == "" || p.CreatedAt.IsZero() {
		return ErrCatalogPlanInvalid
	}
	if p.PlanningMode != PlanningModeSingle && p.PlanningMode != PlanningModeAuto {
		return ErrCatalogPlanInvalid
	}
	if p.ExecutionMode != ExecutionModeExperiment &&
		p.ExecutionMode != ExecutionModeLive {
		return ErrCatalogPlanInvalid
	}
	if err := p.ResearchPriceConstraint.ValidateForMarket(p.MarketContext); err != nil {
		return fmt.Errorf("%w: %v", ErrCatalogPlanInvalid, err)
	}
	if p.ExecutionMode == ExecutionModeLive &&
		((p.MarketContext.Country != "US" && p.MarketContext.Country != "KR") || (p.MarketContext.Currency != "USD" && p.MarketContext.Currency != "KRW")) {
		return fmt.Errorf("%w: LIVE research market must be US/KR and USD/KRW", ErrCatalogPlanInvalid)
	}
	if err := p.ExplicitSettingsSnapshot.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrCatalogPlanInvalid, err)
	}
	if !p.ResearchPriceConstraint.Equal(
		p.ExplicitSettingsSnapshot.ResearchPriceConstraint,
	) {
		return fmt.Errorf(
			"%w: price must equal the server settings snapshot",
			ErrCatalogPlanInvalid,
		)
	}
	if err := p.IntelligenceProviderSnapshot.Validate(); err != nil {
		return err
	}
	return nil
}

func (p ShoppingPlanV2) calculateHash() (string, error) {
	return shareddomain.CanonicalJSONHash(struct {
		SchemaVersion                string                               `json:"schemaVersion"`
		ID                           ShoppingPlanID                       `json:"id"`
		UserID                       UserID                               `json:"userId"`
		ResearchContractVersion      shareddomain.ResearchContractVersion `json:"researchContractVersion"`
		OriginalIntent               string                               `json:"originalIntent"`
		PlanningMode                 PlanningMode                         `json:"planningMode"`
		ExecutionMode                ExecutionMode                        `json:"executionMode"`
		MarketContext                shareddomain.MarketContext           `json:"marketContext"`
		ResearchPriceConstraint      shareddomain.ResearchPriceConstraint `json:"researchPriceConstraint"`
		ExplicitSettingsSnapshot     ExplicitSettingsSnapshotV2           `json:"explicitSettingsSnapshot"`
		IntelligenceProviderSnapshot IntelligenceProviderSnapshotV2       `json:"intelligenceProviderSnapshot"`
		CreatedAt                    time.Time                            `json:"createdAt"`
	}{
		SchemaVersion: p.SchemaVersion, ID: p.ID, UserID: p.UserID,
		ResearchContractVersion:      p.ResearchContractVersion,
		OriginalIntent:               p.OriginalIntent,
		PlanningMode:                 p.PlanningMode,
		ExecutionMode:                p.ExecutionMode,
		MarketContext:                p.MarketContext,
		ResearchPriceConstraint:      p.ResearchPriceConstraint,
		ExplicitSettingsSnapshot:     p.ExplicitSettingsSnapshot,
		IntelligenceProviderSnapshot: p.IntelligenceProviderSnapshot,
		CreatedAt:                    p.CreatedAt,
	})
}

type InterpretationConstraintOriginV2 string

const (
	InterpretationOriginUserExplicit    InterpretationConstraintOriginV2 = "USER_EXPLICIT"
	InterpretationOriginApprovedProfile InterpretationConstraintOriginV2 = "APPROVED_PROFILE"
	InterpretationOriginPolicy          InterpretationConstraintOriginV2 = "POLICY"
	InterpretationOriginModelDerived    InterpretationConstraintOriginV2 = "MODEL_DERIVED"
)

func (o InterpretationConstraintOriginV2) Valid() bool {
	return o == InterpretationOriginUserExplicit ||
		o == InterpretationOriginApprovedProfile ||
		o == InterpretationOriginPolicy ||
		o == InterpretationOriginModelDerived
}

type InterpretationProvenanceV2 struct {
	Field     string                           `json:"field"`
	Value     string                           `json:"value"`
	Origin    InterpretationConstraintOriginV2 `json:"origin"`
	SourceRef string                           `json:"sourceRef"`
}

type TargetSizeDraftV2 struct {
	Value        string `json:"value"`
	SizingSystem string `json:"sizingSystem,omitempty"`
}

type TargetAttributesDraftV2 struct {
	Colors       []string            `json:"colors"`
	Sizes        []TargetSizeDraftV2 `json:"sizes"`
	TargetGender []string            `json:"targetGender"`
}

type TargetDraftV2 struct {
	ProductType         string                       `json:"productType"`
	UseCases            []string                     `json:"useCases"`
	BrandTerms          []string                     `json:"brandTerms"`
	ModelTerms          []string                     `json:"modelTerms"`
	Materials           []string                     `json:"materials"`
	Styles              []string                     `json:"styles"`
	HardRequirements    []string                     `json:"hardRequirements"`
	SoftPreferences     []string                     `json:"softPreferences"`
	Exclusions          []string                     `json:"exclusions"`
	Attributes          TargetAttributesDraftV2      `json:"attributes"`
	Condition           []string                     `json:"condition"`
	PriceTierPreference string                       `json:"priceTierPreference,omitempty"`
	ReferenceURLs       []string                     `json:"referenceUrls"`
	TaxonomyRef         string                       `json:"taxonomyRef,omitempty"`
	Provenance          []InterpretationProvenanceV2 `json:"provenance"`
}

// IntentInterpretationV2 is the immutable English artifact created from the
// user's original intent. TargetDraftV2 intentionally has no numeric price,
// market country, or currency field; those server-owned values come from the
// ShoppingPlan when Curation materializes a Target profile.
type IntentInterpretationV2 struct {
	ID                      string                               `json:"interpretationId"`
	ShoppingPlanID          ShoppingPlanID                       `json:"shoppingPlanId"`
	PlanningTaskID          string                               `json:"planningTaskId"`
	ResearchContractVersion shareddomain.ResearchContractVersion `json:"researchContractVersion"`
	SchemaVersion           string                               `json:"schemaVersion"`
	NormalizedIntentEnglish string                               `json:"normalizedIntentEnglish"`
	TargetDrafts            []TargetDraftV2                      `json:"targetDrafts"`
	SourceLanguage          string                               `json:"sourceLanguage"`
	PolicyVersion           string                               `json:"policyVersion"`
	SourceCatalogHash       string                               `json:"sourceCatalogHash"`
	CreatedAt               time.Time                            `json:"createdAt"`
	ContentHash             string                               `json:"contentHash"`
}

type NewIntentInterpretationV2Input struct {
	InterpretationID            string
	PlanningTaskID              string
	NormalizedIntentEnglish     string
	TargetDrafts                []TargetDraftV2
	SourceLanguage              string
	PolicyVersion               string
	TrustedConstraintSourceRefs []string
	Now                         time.Time
}

func NewIntentInterpretationV2(
	plan ShoppingPlanV2,
	input NewIntentInterpretationV2Input,
) (IntentInterpretationV2, error) {
	if err := plan.Validate(); err != nil {
		return IntentInterpretationV2{}, err
	}
	interpretation := IntentInterpretationV2{
		ID: strings.TrimSpace(input.InterpretationID), ShoppingPlanID: plan.ID,
		PlanningTaskID:          strings.TrimSpace(input.PlanningTaskID),
		ResearchContractVersion: shareddomain.CatalogResearchContractVersionV2,
		SchemaVersion:           IntentInterpretationSchemaV1,
		NormalizedIntentEnglish: strings.TrimSpace(input.NormalizedIntentEnglish),
		TargetDrafts:            cloneAndNormalizeTargetDrafts(input.TargetDrafts),
		SourceLanguage:          strings.ToLower(strings.TrimSpace(input.SourceLanguage)),
		PolicyVersion:           strings.TrimSpace(input.PolicyVersion),
		CreatedAt:               input.Now,
	}
	trustedSourceRefs := cleanTextValues(input.TrustedConstraintSourceRefs)
	slices.Sort(trustedSourceRefs)
	sourceCatalogHash, err := shareddomain.CanonicalJSONHash(trustedSourceRefs)
	if err != nil {
		return IntentInterpretationV2{}, err
	}
	interpretation.SourceCatalogHash = sourceCatalogHash
	if err := interpretation.validateShape(
		plan, sourceRefSet(trustedSourceRefs),
	); err != nil {
		return IntentInterpretationV2{}, err
	}
	hash, err := interpretation.calculateHash()
	if err != nil {
		return IntentInterpretationV2{}, err
	}
	interpretation.ContentHash = hash
	return interpretation, nil
}

func (i IntentInterpretationV2) Validate(plan ShoppingPlanV2) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if err := i.validateShape(plan, nil); err != nil {
		return err
	}
	hash, err := i.calculateHash()
	if err != nil {
		return err
	}
	if i.ContentHash != hash {
		return fmt.Errorf(
			"%w: content hash mismatch",
			ErrIntentInterpretationInvalid,
		)
	}
	return nil
}

func (i IntentInterpretationV2) validateShape(
	plan ShoppingPlanV2,
	trustedSourceRefs map[string]struct{},
) error {
	if i.SchemaVersion != IntentInterpretationSchemaV1 ||
		i.ResearchContractVersion !=
			shareddomain.CatalogResearchContractVersionV2 ||
		i.ShoppingPlanID != plan.ID || strings.TrimSpace(i.ID) == "" ||
		strings.TrimSpace(i.PlanningTaskID) == "" ||
		strings.TrimSpace(i.PolicyVersion) == "" ||
		strings.TrimSpace(i.SourceCatalogHash) == "" ||
		strings.TrimSpace(i.SourceLanguage) == "" || i.CreatedAt.IsZero() {
		return ErrIntentInterpretationInvalid
	}
	if !isEnglishSemanticText(i.NormalizedIntentEnglish) {
		return ErrIntentInterpretationNotEnglish
	}
	if len(i.TargetDrafts) == 0 ||
		len(i.TargetDrafts) > MaximumIntentInterpretationTargets ||
		(plan.PlanningMode == PlanningModeSingle && len(i.TargetDrafts) != 1) {
		return ErrIntentInterpretationInvalid
	}
	for index := range i.TargetDrafts {
		if err := validateTargetDraftV2(
			i.TargetDrafts[index], trustedSourceRefs,
		); err != nil {
			return fmt.Errorf("targetDrafts[%d]: %w", index, err)
		}
	}
	return nil
}

func (i IntentInterpretationV2) calculateHash() (string, error) {
	return shareddomain.CanonicalJSONHash(struct {
		ID                      string                               `json:"interpretationId"`
		ShoppingPlanID          ShoppingPlanID                       `json:"shoppingPlanId"`
		PlanningTaskID          string                               `json:"planningTaskId"`
		ResearchContractVersion shareddomain.ResearchContractVersion `json:"researchContractVersion"`
		SchemaVersion           string                               `json:"schemaVersion"`
		NormalizedIntentEnglish string                               `json:"normalizedIntentEnglish"`
		TargetDrafts            []TargetDraftV2                      `json:"targetDrafts"`
		SourceLanguage          string                               `json:"sourceLanguage"`
		PolicyVersion           string                               `json:"policyVersion"`
		SourceCatalogHash       string                               `json:"sourceCatalogHash"`
		CreatedAt               time.Time                            `json:"createdAt"`
	}{
		ID: i.ID, ShoppingPlanID: i.ShoppingPlanID,
		PlanningTaskID:          i.PlanningTaskID,
		ResearchContractVersion: i.ResearchContractVersion,
		SchemaVersion:           i.SchemaVersion,
		NormalizedIntentEnglish: i.NormalizedIntentEnglish,
		TargetDrafts:            i.TargetDrafts, SourceLanguage: i.SourceLanguage,
		PolicyVersion: i.PolicyVersion, SourceCatalogHash: i.SourceCatalogHash,
		CreatedAt: i.CreatedAt,
	})
}

type ProposalValidationStatusV2 string

const ProposalValidationAcceptedV2 ProposalValidationStatusV2 = "ACCEPTED"

type PlanningProposalV2 struct {
	ID                      string                               `json:"id"`
	TaskID                  string                               `json:"taskId"`
	PlanID                  ShoppingPlanID                       `json:"planId"`
	UserID                  UserID                               `json:"userId"`
	IntelligenceJobID       string                               `json:"intelligenceJobId"`
	ClientProposalID        string                               `json:"clientProposalId"`
	ResearchContractVersion shareddomain.ResearchContractVersion `json:"researchContractVersion"`
	ContextHash             string                               `json:"contextHash"`
	SchemaVersion           string                               `json:"schemaVersion"`
	InterpretationID        string                               `json:"interpretationId"`
	InterpretationHash      string                               `json:"interpretationHash"`
	ValidationStatus        ProposalValidationStatusV2           `json:"validationStatus"`
	ProposalHash            string                               `json:"proposalHash"`
	SubmittedAt             time.Time                            `json:"submittedAt"`
}

type NewPlanningProposalV2Input struct {
	ProposalID        string
	IntelligenceJobID string
	ClientProposalID  string
	SubmittedAt       time.Time
}

func NewPlanningProposalV2(
	plan ShoppingPlanV2,
	interpretation IntentInterpretationV2,
	input NewPlanningProposalV2Input,
) (PlanningProposalV2, error) {
	if err := plan.Validate(); err != nil {
		return PlanningProposalV2{}, err
	}
	if err := interpretation.Validate(plan); err != nil {
		return PlanningProposalV2{}, err
	}
	proposal := PlanningProposalV2{
		ID:     strings.TrimSpace(input.ProposalID),
		TaskID: interpretation.PlanningTaskID, PlanID: plan.ID, UserID: plan.UserID,
		IntelligenceJobID:       strings.TrimSpace(input.IntelligenceJobID),
		ClientProposalID:        strings.TrimSpace(input.ClientProposalID),
		ResearchContractVersion: shareddomain.CatalogResearchContractVersionV2,
		ContextHash:             plan.ContentHash, SchemaVersion: PlanningProposalSchemaV2,
		InterpretationID:   interpretation.ID,
		InterpretationHash: interpretation.ContentHash,
		ValidationStatus:   ProposalValidationAcceptedV2,
		SubmittedAt:        input.SubmittedAt,
	}
	if err := proposal.validateShape(); err != nil {
		return PlanningProposalV2{}, err
	}
	hash, err := proposal.calculateHash()
	if err != nil {
		return PlanningProposalV2{}, err
	}
	proposal.ProposalHash = hash
	return proposal, nil
}

func (p PlanningProposalV2) Validate(
	plan ShoppingPlanV2,
	interpretation IntentInterpretationV2,
) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if err := interpretation.Validate(plan); err != nil {
		return err
	}
	if err := p.validateShape(); err != nil {
		return err
	}
	if p.PlanID != plan.ID || p.UserID != plan.UserID ||
		p.ContextHash != plan.ContentHash || p.TaskID != interpretation.PlanningTaskID ||
		p.InterpretationID != interpretation.ID ||
		p.InterpretationHash != interpretation.ContentHash {
		return ErrPlanningProposalV2Invalid
	}
	hash, err := p.calculateHash()
	if err != nil {
		return err
	}
	if p.ProposalHash != hash {
		return fmt.Errorf(
			"%w: proposal hash mismatch",
			ErrPlanningProposalV2Invalid,
		)
	}
	return nil
}

func (p PlanningProposalV2) validateShape() error {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.TaskID) == "" ||
		strings.TrimSpace(string(p.PlanID)) == "" ||
		strings.TrimSpace(string(p.UserID)) == "" ||
		strings.TrimSpace(p.IntelligenceJobID) == "" ||
		strings.TrimSpace(p.ClientProposalID) == "" ||
		p.ResearchContractVersion !=
			shareddomain.CatalogResearchContractVersionV2 ||
		strings.TrimSpace(p.ContextHash) == "" ||
		p.SchemaVersion != PlanningProposalSchemaV2 ||
		strings.TrimSpace(p.InterpretationID) == "" ||
		strings.TrimSpace(p.InterpretationHash) == "" ||
		p.ValidationStatus != ProposalValidationAcceptedV2 || p.SubmittedAt.IsZero() {
		return ErrPlanningProposalV2Invalid
	}
	return nil
}

func (p PlanningProposalV2) calculateHash() (string, error) {
	return shareddomain.CanonicalJSONHash(struct {
		ID                      string                               `json:"id"`
		TaskID                  string                               `json:"taskId"`
		PlanID                  ShoppingPlanID                       `json:"planId"`
		UserID                  UserID                               `json:"userId"`
		IntelligenceJobID       string                               `json:"intelligenceJobId"`
		ClientProposalID        string                               `json:"clientProposalId"`
		ResearchContractVersion shareddomain.ResearchContractVersion `json:"researchContractVersion"`
		ContextHash             string                               `json:"contextHash"`
		SchemaVersion           string                               `json:"schemaVersion"`
		InterpretationID        string                               `json:"interpretationId"`
		InterpretationHash      string                               `json:"interpretationHash"`
		ValidationStatus        ProposalValidationStatusV2           `json:"validationStatus"`
		SubmittedAt             time.Time                            `json:"submittedAt"`
	}{
		ID: p.ID, TaskID: p.TaskID, PlanID: p.PlanID, UserID: p.UserID,
		IntelligenceJobID:       p.IntelligenceJobID,
		ClientProposalID:        p.ClientProposalID,
		ResearchContractVersion: p.ResearchContractVersion,
		ContextHash:             p.ContextHash, SchemaVersion: p.SchemaVersion,
		InterpretationID:   p.InterpretationID,
		InterpretationHash: p.InterpretationHash,
		ValidationStatus:   p.ValidationStatus, SubmittedAt: p.SubmittedAt,
	})
}

func validateTargetDraftV2(
	draft TargetDraftV2,
	trustedSourceRefs map[string]struct{},
) error {
	semanticFields := map[string][]string{
		"productType":             {draft.ProductType},
		"useCases":                draft.UseCases,
		"brandTerms":              draft.BrandTerms,
		"modelTerms":              draft.ModelTerms,
		"materials":               draft.Materials,
		"styles":                  draft.Styles,
		"hardRequirements":        draft.HardRequirements,
		"softPreferences":         draft.SoftPreferences,
		"exclusions":              draft.Exclusions,
		"attributes.colors":       draft.Attributes.Colors,
		"attributes.targetGender": draft.Attributes.TargetGender,
		"condition":               draft.Condition,
	}
	if draft.PriceTierPreference != "" {
		semanticFields["priceTierPreference"] = []string{draft.PriceTierPreference}
	}
	for _, size := range draft.Attributes.Sizes {
		semanticFields["attributes.sizes"] = append(
			semanticFields["attributes.sizes"], sizeProvenanceValue(size),
		)
		if !isEnglishOrNumericSemanticText(size.Value) ||
			(size.SizingSystem != "" && !isEnglishSemanticText(size.SizingSystem)) {
			return ErrIntentInterpretationNotEnglish
		}
	}
	if strings.TrimSpace(draft.ProductType) == "" {
		return ErrIntentInterpretationInvalid
	}
	for _, values := range semanticFields {
		for _, value := range values {
			if !isEnglishSemanticText(value) {
				return ErrIntentInterpretationNotEnglish
			}
		}
	}

	expected := make(map[string]struct{})
	for field, values := range semanticFields {
		for _, value := range values {
			expected[field+"\x00"+value] = struct{}{}
		}
	}
	for _, referenceURL := range draft.ReferenceURLs {
		parsed, err := url.ParseRequestURI(referenceURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Host == "" {
			return ErrIntentInterpretationInvalid
		}
		expected["referenceUrls\x00"+referenceURL] = struct{}{}
	}
	if draft.TaxonomyRef != "" {
		expected["taxonomyRef\x00"+draft.TaxonomyRef] = struct{}{}
	}

	provenance := make(map[string]InterpretationProvenanceV2, len(draft.Provenance))
	for _, source := range draft.Provenance {
		source.Field = strings.TrimSpace(source.Field)
		source.Value = strings.TrimSpace(source.Value)
		source.SourceRef = strings.TrimSpace(source.SourceRef)
		if source.Field == "" || source.Value == "" || !source.Origin.Valid() ||
			source.SourceRef == "" || len(source.SourceRef) > 200 {
			return ErrIntentConstraintProvenanceInvalid
		}
		key := source.Field + "\x00" + source.Value
		if _, exists := provenance[key]; exists {
			return ErrIntentConstraintProvenanceInvalid
		}
		if _, exists := expected[key]; !exists {
			return fmt.Errorf(
				"%w: provenance does not match a draft value",
				ErrIntentConstraintProvenanceInvalid,
			)
		}
		if trustedSourceRefs != nil &&
			source.Origin != InterpretationOriginModelDerived {
			if _, trusted := trustedSourceRefs[source.SourceRef]; !trusted {
				return fmt.Errorf(
					"%w: untrusted source reference",
					ErrIntentConstraintInvented,
				)
			}
		}
		provenance[key] = source
	}
	if len(provenance) != len(expected) {
		return ErrIntentConstraintProvenanceInvalid
	}
	for field, values := range semanticFields {
		for _, value := range values {
			source, exists := provenance[field+"\x00"+value]
			if !exists {
				return fmt.Errorf(
					"%w: missing %s=%q",
					ErrIntentConstraintProvenanceInvalid,
					field,
					value,
				)
			}
			if protectedInterpretationField(field) &&
				source.Origin == InterpretationOriginModelDerived {
				return fmt.Errorf(
					"%w: %s=%q",
					ErrIntentConstraintInvented,
					field,
					value,
				)
			}
		}
	}
	for _, referenceURL := range draft.ReferenceURLs {
		source, exists := provenance["referenceUrls\x00"+strings.TrimSpace(referenceURL)]
		if !exists || source.Origin == InterpretationOriginModelDerived {
			return ErrIntentConstraintInvented
		}
	}
	if draft.TaxonomyRef != "" {
		source, exists := provenance["taxonomyRef\x00"+draft.TaxonomyRef]
		if !exists || source.Origin != InterpretationOriginPolicy {
			return ErrIntentConstraintInvented
		}
	}
	return nil
}

func protectedInterpretationField(field string) bool {
	switch field {
	case "brandTerms", "modelTerms", "attributes.colors",
		"attributes.sizes", "attributes.targetGender", "condition",
		"priceTierPreference":
		return true
	default:
		return false
	}
}

func cloneAndNormalizeTargetDrafts(input []TargetDraftV2) []TargetDraftV2 {
	result := make([]TargetDraftV2, len(input))
	for index, value := range input {
		value.ProductType = strings.TrimSpace(value.ProductType)
		value.UseCases = cleanTextValues(value.UseCases)
		value.BrandTerms = cleanTextValues(value.BrandTerms)
		value.ModelTerms = cleanTextValues(value.ModelTerms)
		value.Materials = cleanTextValues(value.Materials)
		value.Styles = cleanTextValues(value.Styles)
		value.HardRequirements = cleanTextValues(value.HardRequirements)
		value.SoftPreferences = cleanTextValues(value.SoftPreferences)
		value.Exclusions = cleanTextValues(value.Exclusions)
		value.Attributes.Colors = cleanTextValues(value.Attributes.Colors)
		value.Attributes.TargetGender = cleanTextValues(
			value.Attributes.TargetGender,
		)
		value.Condition = cleanTextValues(value.Condition)
		value.PriceTierPreference = strings.TrimSpace(value.PriceTierPreference)
		value.ReferenceURLs = cleanTextValues(value.ReferenceURLs)
		value.TaxonomyRef = strings.TrimSpace(value.TaxonomyRef)
		value.Attributes.Sizes = append([]TargetSizeDraftV2(nil), value.Attributes.Sizes...)
		for sizeIndex := range value.Attributes.Sizes {
			value.Attributes.Sizes[sizeIndex].Value = strings.TrimSpace(
				value.Attributes.Sizes[sizeIndex].Value,
			)
			value.Attributes.Sizes[sizeIndex].SizingSystem = strings.TrimSpace(
				value.Attributes.Sizes[sizeIndex].SizingSystem,
			)
		}
		value.Provenance = append([]InterpretationProvenanceV2(nil), value.Provenance...)
		for sourceIndex := range value.Provenance {
			value.Provenance[sourceIndex].Field = strings.TrimSpace(
				value.Provenance[sourceIndex].Field,
			)
			value.Provenance[sourceIndex].Value = strings.TrimSpace(
				value.Provenance[sourceIndex].Value,
			)
			value.Provenance[sourceIndex].SourceRef = strings.TrimSpace(
				value.Provenance[sourceIndex].SourceRef,
			)
		}
		slices.SortFunc(value.Provenance, compareInterpretationProvenance)
		result[index] = value
	}
	return result
}

func cleanTextValues(input []string) []string {
	if input == nil {
		return []string{}
	}
	result := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, value := range input {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func isEnglishSemanticText(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	hasLatin := false
	for _, current := range value {
		if unicode.IsLetter(current) {
			if !unicode.In(current, unicode.Latin) {
				return false
			}
			hasLatin = true
		}
	}
	return hasLatin
}

func isEnglishOrNumericSemanticText(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	hasLatinOrDigit := false
	for _, current := range value {
		if unicode.IsLetter(current) {
			if !unicode.In(current, unicode.Latin) {
				return false
			}
			hasLatinOrDigit = true
		}
		if unicode.IsDigit(current) {
			hasLatinOrDigit = true
		}
	}
	return hasLatinOrDigit
}

func sizeProvenanceValue(value TargetSizeDraftV2) string {
	size := strings.TrimSpace(value.Value)
	system := strings.TrimSpace(value.SizingSystem)
	if system == "" {
		return size
	}
	return system + ":" + size
}

func compareInterpretationProvenance(
	left, right InterpretationProvenanceV2,
) int {
	leftKey := left.Field + "\x00" + left.Value + "\x00" +
		string(left.Origin) + "\x00" + left.SourceRef
	rightKey := right.Field + "\x00" + right.Value + "\x00" +
		string(right.Origin) + "\x00" + right.SourceRef
	return strings.Compare(leftKey, rightKey)
}

func sourceRefSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
