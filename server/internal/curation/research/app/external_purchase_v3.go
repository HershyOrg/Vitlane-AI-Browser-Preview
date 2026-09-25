package app

import (
	"context"
	"encoding/json"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"time"
)

type ExternalPurchaseRecord struct {
	ProductRef  *researchdomain.SourceProductRef `json:"productRef,omitempty"`
	CandidateID string                           `json:"candidateId"`
	VariantRef  researchdomain.SourceVariantRef  `json:"variantRef,omitzero"`
	Checked     bool                             `json:"checked"`
	Version     int64                            `json:"version"`
	RecordedAt  time.Time                        `json:"recordedAt"`
	Evidence    string                           `json:"evidence"`
}
type PurchaseFeedback struct {
	SchemaVersion string                   `json:"schemaVersion"`
	Version       int64                    `json:"version"`
	Records       []ExternalPurchaseRecord `json:"records"`
}
type MarkExternalPurchaseInput struct {
	ProductRef      *researchdomain.SourceProductRef `json:"productRef,omitempty"`
	UserID          string                           `json:"-"`
	CurationID      string                           `json:"-"`
	CandidateID     string                           `json:"candidateId"`
	VariantRef      researchdomain.SourceVariantRef  `json:"variantRef,omitzero"`
	Checked         bool                             `json:"checked"`
	ExpectedVersion int64                            `json:"expectedVersion"`
	// Snapshot is the Amazon display context the client saw at check time (ADR-0075).
	// Korean product requests carry none: the server derives it from the saved observation.
	Snapshot       *researchdomain.PurchaseCheckSnapshot `json:"snapshot,omitempty"`
	IdempotencyKey string                                `json:"-"`
}
type ExternalPurchaseRepository interface {
	ReadPurchaseFeedback(context.Context, string, string) (PurchaseFeedback, error)
	MarkExternalPurchase(context.Context, MarkExternalPurchaseInput, time.Time) (PurchaseFeedback, error)
	PurchaseFeedbackForPlan(context.Context, string, string) (PurchaseFeedback, error)
}

// PurchaseCheckRepository is the account-scope projection of self-reported
// purchases (ADR-0075). It reads only; every write stays on the per-Candidate
// purchase-check command.
type PurchaseCheckRepository interface {
	ListPurchaseChecks(context.Context, string, int) ([]researchdomain.PurchaseCheck, error)
}

func (s *Service) ListPurchaseChecks(ctx context.Context, user string, limit int) ([]researchdomain.PurchaseCheck, error) {
	repository, ok := s.repository.(PurchaseCheckRepository)
	if !ok {
		return []researchdomain.PurchaseCheck{}, nil
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	return repository.ListPurchaseChecks(ctx, user, limit)
}

type AmazonConfigurationRepository interface {
	SaveAmazonConfiguration(context.Context, string, string, CatalogCandidateConfigurationV2, int64) error
}

func (s *LiveCatalogReviewServiceV2) saveAmazonConfiguration(ctx context.Context, in CatalogSaveConfigurationInputV2, c CatalogCandidateReferenceV2) error {
	grant, err := s.amazonGrant(ctx, in.UserID, in.CurationID, in.CandidateID, in.RelationToken)
	if err != nil {
		return err
	}
	options := []string{}
	found := false
	for _, v := range grant.Variants {
		if v.ASIN == in.VariantID {
			found = true
			for _, o := range v.Labels {
				options = append(options, o.Name+":"+o.Value)
			}
		}
	}
	if !found || in.ExpectedVersion < 0 {
		return fault.New(fault.InvalidInput, "AMAZON_VARIANT_RELATION_INVALID", false)
	}
	repo, ok := s.workspace.(AmazonConfigurationRepository)
	if !ok {
		return fault.New(fault.InternalFailure, "AMAZON_CONFIGURATION_UNAVAILABLE", false)
	}
	return repo.SaveAmazonConfiguration(ctx, in.UserID, in.CurationID, CatalogCandidateConfigurationV2{TargetID: c.PlanTargetID, CandidateID: c.CandidateID, VariantID: in.VariantID, SelectedOptions: options, ObservedAt: grant.ObservedAt, UpdatedAt: s.clock.Now()}, in.ExpectedVersion)
}
func (s *LiveCatalogReviewServiceV2) AmazonState(ctx context.Context, user, curation, candidate string) (map[string]any, error) {
	c, err := s.resolveCandidateV2(ctx, user, curation, candidate)
	if err != nil {
		return nil, err
	}
	if c.ProductRef().Source != researchdomain.SourceAmazon {
		return nil, fault.New(fault.InvalidInput, "EXTERNAL_SOURCE_REQUIRED", false)
	}
	stored, err := s.workspace.LoadCatalogWorkspaceStateV2(ctx, user, curation)
	if err != nil {
		return nil, err
	}
	asin := c.ProductRef().AnchorASIN
	version := int64(0)
	for _, v := range stored.Configurations {
		if v.CandidateID == candidate {
			asin = v.VariantID
			version = v.Version
		}
	}
	feedback, err := s.PurchaseFeedback(ctx, user, curation)
	if err != nil {
		return nil, err
	}
	return map[string]any{"schemaVersion": "vitlane.amazon-candidate-state.v3", "variantRef": researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: asin}, "configurationVersion": version, "purchaseFeedback": feedback}, nil
}
func (s *LiveCatalogReviewServiceV2) PurchaseFeedback(ctx context.Context, user, curation string) (PurchaseFeedback, error) {
	if _, err := s.workspace.CatalogCurationMarketContextV2(ctx, user, curation); err != nil {
		return PurchaseFeedback{}, err
	}
	repo, ok := s.workspace.(ExternalPurchaseRepository)
	if !ok {
		return PurchaseFeedback{}, fault.New(fault.InternalFailure, "PURCHASE_RECORD_UNAVAILABLE", false)
	}
	return repo.ReadPurchaseFeedback(ctx, user, curation)
}
func (s *LiveCatalogReviewServiceV2) MarkExternalPurchase(ctx context.Context, in MarkExternalPurchaseInput) (PurchaseFeedback, error) {
	validSubject := in.ProductRef == nil && in.VariantRef.Validate() == nil && in.VariantRef.Source == researchdomain.SourceAmazon
	if in.ProductRef != nil {
		validSubject = in.ProductRef.Validate() == nil && in.ProductRef.Source.KoreanExternal() && in.VariantRef == (researchdomain.SourceVariantRef{})
	}
	if !validSubject || in.ExpectedVersion < 0 || len(in.IdempotencyKey) == 0 || len(in.IdempotencyKey) > 200 {
		return PurchaseFeedback{}, fault.New(fault.InvalidInput, "PURCHASE_RECORD_INVALID", false)
	}
	if in.Snapshot != nil {
		// Only the Amazon subject takes a client snapshot; the Korean snapshot is server-owned.
		if in.ProductRef != nil {
			return PurchaseFeedback{}, fault.New(fault.InvalidInput, "PURCHASE_RECORD_INVALID", false)
		}
		snapshot, err := researchdomain.NewPurchaseCheckSnapshot(*in.Snapshot)
		if err != nil {
			return PurchaseFeedback{}, fault.New(fault.InvalidInput, "PURCHASE_RECORD_INVALID", false)
		}
		in.Snapshot = &snapshot
	}
	c, err := s.resolveCandidateV2(ctx, in.UserID, in.CurationID, in.CandidateID)
	if err != nil {
		return PurchaseFeedback{}, err
	}
	if err := s.guardTargetWriteV2(ctx, in.UserID, c.PlanTargetID); err != nil {
		return PurchaseFeedback{}, err
	}
	if (in.ProductRef == nil && c.ProductRef().Source != researchdomain.SourceAmazon) || (in.ProductRef != nil && c.ProductRef() != *in.ProductRef) {
		return PurchaseFeedback{}, fault.New(fault.InvalidInput, "EXTERNAL_SOURCE_REQUIRED", false)
	}
	repo, ok := s.workspace.(ExternalPurchaseRepository)
	if !ok {
		return PurchaseFeedback{}, fault.New(fault.InternalFailure, "PURCHASE_RECORD_UNAVAILABLE", false)
	}
	result, err := repo.MarkExternalPurchase(ctx, in, s.clock.Now())
	if err == nil {
		action := "REPORTED"
		if !in.Checked {
			action = "RETRACTED"
		}
		sharedapp.RecordAnalytics(ctx, sharedapp.AnalyticsEvent{Name: "external_purchase_reported", UserID: in.UserID, Key: in.IdempotencyKey, CurationID: in.CurationID, Source: string(c.ProductRef().Source), Action: action})
	}
	return result, err
}

// The snapshot is enriched only during Round creation inside the caller's transaction.
func (s *Service) attachPurchaseFeedback(ctx context.Context, user, plan string, raw json.RawMessage) (json.RawMessage, error) {
	var settingsErr error
	raw, settingsErr = s.attachCriteria(ctx, user, plan, raw)
	if settingsErr != nil {
		return nil, settingsErr
	}
	raw, settingsErr = s.attachResearchSettings(ctx, user, plan, raw)
	if settingsErr != nil {
		return nil, settingsErr
	}
	raw, settingsErr = s.attachResearchBudget(ctx, user, plan, raw)
	if settingsErr != nil {
		return nil, settingsErr
	}
	repo, ok := s.repository.(ExternalPurchaseRepository)
	if !ok {
		return raw, nil
	}
	feedback, err := repo.PurchaseFeedbackForPlan(ctx, user, plan)
	if err != nil {
		return nil, err
	}
	var snapshot ResearchContext
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return nil, err
	}
	snapshot.PurchaseFeedbackVersion = feedback.Version
	snapshot.AlreadyPurchased = []ExternalPurchaseRecord{}
	for _, record := range feedback.Records {
		if record.Checked {
			snapshot.AlreadyPurchased = append(snapshot.AlreadyPurchased, record)
		}
	}
	return json.Marshal(snapshot)
}
