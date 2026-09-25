package main

import (
	"context"
	"fmt"
	c "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	i "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	id "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	adapter "github.com/vitlane/vitlane/server/internal/curation/intelligence/infra/curation"
	research "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sharedomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"sort"
	"strings"
	"time"
)

type threadPrimitives struct {
	curation     *c.Service
	research     *research.Service
	intelligence *i.Service
	products     *adapter.ProductAdapter
}

func (p threadPrimitives) ResearchThreadTarget(ctx context.Context, t d.CurationThread, s d.CurationAction, target c.ThreadTarget) error {
	plan, err := p.curation.GetByCuration(ctx, t.UserID, t.CurationID)
	if err != nil {
		return err
	}
	instruction := s.Instruction
	if instruction == "" {
		instruction = t.Request
	}
	_, err = p.research.ResearchAgain(ctx, research.ResearchAgainInput{UserID: t.UserID, AuthSessionID: t.AuthSessionID, CurationID: t.CurationID, TargetID: target.ID, SessionID: target.SessionID, CurationActionID: s.ID, ClientRequestID: s.ID, Feedback: instruction, ExpectedCurationVersion: plan.Curation.Version, ExpectedSessionVersion: target.SessionVersion})
	return err
}
func (p threadPrimitives) StartThreadResearch(ctx context.Context, t d.CurationThread, source string) error {
	_, err := p.products.StartCurating(ctx, t.UserID, t.PlanID, source)
	return err
}
func (p threadPrimitives) CancelThreadAction(ctx context.Context, user, action string) error {
	if p.intelligence == nil {
		return nil
	}
	_, err := p.intelligence.CancelAction(ctx, user, action)
	return err
}

func (p threadPrimitives) StartInterpretation(ctx context.Context, t d.CurationThread, a d.CurationAction) error {
	if p.intelligence == nil {
		return d.ErrCurationActionUnavailable
	}
	plan, e := p.curation.GetByCuration(ctx, t.UserID, t.CurationID)
	if e != nil {
		return e
	}
	_, e = p.intelligence.CreateJob(ctx, i.CreateJobInput{UserID: t.UserID, CurationID: t.CurationID, CurationActionID: a.ID, PlanID: t.PlanID, Target: id.JobTarget{Kind: id.TargetActionInterpretation, ID: a.ID, Revision: a.InputRevision}, Provider: id.ProviderManaged, ModelKey: plan.Plan.ModelKey})
	return e
}

// The composition adapter reuses the authorized, rate-limited workspace lookup.
// Only saved visible candidates are enriched; no research or catalog write occurs.
type responseResearchReader interface {
	LoadWorkspaceV2(context.Context, string, string, string, string, bool) (research.CatalogWorkspaceViewV2, error)
	HydrateWorkspaceV2(context.Context, research.CatalogResearchHydrationInputV2) (research.CatalogWorkspaceViewV2, error)
}
type responseExchangeReader interface {
	View(context.Context) (research.ExchangeRateView, error)
}
type threadResponseSource struct {
	research responseResearchReader
	fx       responseExchangeReader
}

func (s threadResponseSource) SavedCandidates(ctx context.Context, userID, curationID string, targetIDs []string) (map[string][]c.SavedCandidate, error) {
	out := map[string][]c.SavedCandidate{}
	if s.research == nil || len(targetIDs) == 0 {
		return out, nil
	}
	wanted := map[string]bool{}
	for _, id := range targetIDs {
		wanted[id] = true
	}
	view, err := s.research.LoadWorkspaceV2(ctx, userID, curationID, "", "", false)
	if err != nil {
		return nil, err
	}
	sourceState, err := responseSourceState(view)
	if err != nil {
		return nil, err
	}
	savedConfigs := map[string]research.CatalogWorkspaceConfigurationViewV2{}
	for _, config := range view.Configurations {
		savedConfigs[config.CandidateID] = config
	}
	// One bounded lookup window for the reply. Failure leaves skeletons unknown.
	lookupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, pool := range view.Pools {
		if !wanted[pool.Metadata.TargetID] {
			continue
		}
		needsLookup := false
		for _, p := range pool.Products {
			needsLookup = needsLookup || p.Source() == researchdomain.SourceShopify
		}
		hydrated := map[string]research.CatalogProductObservation{}
		configured := map[string]research.LiveVariantReviewRowV2{}
		observedAt := ""
		if needsLookup {
			fresh, lookupErr := s.research.HydrateWorkspaceV2(lookupCtx, research.CatalogResearchHydrationInputV2{
				UserID: userID, CurationID: curationID, TargetID: pool.Metadata.TargetID,
				Scope: research.CatalogResearchHydrationVisibleTargetV2, Source: researchdomain.SourceShopify,
			})
			if lookupErr == nil {
				observedAt = fresh.Metrics.CompletedAt.UTC().Format(time.RFC3339Nano)
				for _, fp := range fresh.Pools {
					for _, product := range fp.Products {
						if fp.Hydrations[product.ProviderProductID].Status == research.CatalogCandidateHydrationReadyV2 {
							hydrated[product.ProviderProductID] = product
						}
					}
				}
				for _, config := range fresh.Configurations {
					configured[config.CandidateID] = config.Variant
				}
			}
		}
		for _, product := range pool.Products {
			// Preserve the DB snapshot's identity/order/assessment and visible membership.
			if current, ok := hydrated[product.ProviderProductID]; ok {
				product = current
			}
			assessment, assessed := pool.Assessments[product.ProviderProductID]
			candidate := savedCandidate(product, assessment, assessed)
			candidate.SourceState = sourceState
			if config, ok := savedConfigs[candidate.CandidateID]; ok {
				candidate.VariantID = config.Variant.VariantID
				candidate.ConfigurationVersion = config.Version
			}
			if product.Source() == researchdomain.SourceShopify && candidate.Price != nil {
				candidate.Price.ObservedAt = observedAt
				if variant, ok := configured[candidate.CandidateID]; ok {
					candidate.PriceMinor, candidate.Price = nil, nil
					if !variant.PriceUnknown && (variant.Currency == "USD" || variant.Currency == "KRW") && variant.PriceMinor >= 0 {
						candidate.Currency = variant.Currency
						amount := variant.PriceMinor
						candidate.PriceMinor = &amount
						candidate.Price = &c.ResponsePrice{MinimumMinor: amount, MaximumMinor: amount, Currency: variant.Currency, Basis: "SELECTED_VARIANT", ObservedAt: observedAt}
						for _, option := range variant.SelectedOptions {
							candidate.Price.SelectedOptions = append(candidate.Price.SelectedOptions, c.ResponseOption{Name: option.Name, Value: option.Value})
						}
					}
				}
			}
			for _, reaction := range view.ProductReactions {
				if reaction.CandidateID == candidate.CandidateID && reaction.Sentiment == "DISLIKE" {
					candidate.Excluded = true
				}
			}
			for _, reaction := range view.Interactions {
				if reaction.CandidateID == candidate.CandidateID && reaction.VariantID == candidate.VariantID && reaction.Sentiment == "DISLIKE" {
					candidate.Excluded = true
				}
			}
			out[pool.Metadata.TargetID] = append(out[pool.Metadata.TargetID], candidate)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.fx != nil {
		fxCtx, cancelFX := context.WithTimeout(ctx, 3*time.Second)
		defer cancelFX()
		fx, fxErr := s.fx.View(fxCtx)
		if fxErr == nil && fx.Rate != nil {
			for targetID, candidates := range out {
				for index := range candidates {
					candidate := &candidates[index]
					if candidate.PriceMinor == nil || candidate.Price == nil {
						continue
					}
					to := "USD"
					if candidate.Currency == "USD" {
						to = "KRW"
					}
					converted, err := researchdomain.ConvertResearchMinor(*candidate.PriceMinor, candidate.Currency, to, *fx.Rate, time.Now())
					if err == nil {
						candidate.Price.ConvertedMinor = map[string]int64{to: converted}
						candidate.Price.ExchangeRateAsOf = fx.Rate.AsOf
						if maximum, conversionErr := researchdomain.ConvertResearchMinor(candidate.Price.MaximumMinor, candidate.Currency, to, *fx.Rate, time.Now()); conversionErr == nil {
							candidate.Price.ConvertedMaximumMinor = map[string]int64{to: maximum}
						}
					}
				}
				out[targetID] = candidates
			}
		}
	}
	return out, nil
}

// savedCandidate projects a stored external observation or a freshly hydrated Shopify listing.
func savedCandidate(product research.CatalogProductObservation, assessment research.LiveCandidateAssessmentV2, assessed bool) c.SavedCandidate {
	candidate := c.SavedCandidate{Description: product.Description.Plain, CandidateID: product.ProviderProductID, Title: product.Title, Source: string(product.Source()), Currency: product.PriceRange.Minimum.Currency}
	// A Shopify skeleton has no currency, so its zero-valued range is unknown,
	// never free. A successful hydration supplies the original amount and currency.
	unknown := (product.PriceRange.Minimum.Currency != "USD" && product.PriceRange.Minimum.Currency != "KRW") || product.PriceRange.Minimum.AmountMinor < 0 ||
		(product.Source() == researchdomain.SourceAmazon && (product.VariantObservation == nil || product.VariantObservation.Price.Kind == "UNKNOWN")) ||
		(product.Source().KoreanExternal() && (product.ExternalObservation == nil || product.ExternalObservation.Price.Kind == "UNKNOWN"))
	if !unknown {
		amount := product.PriceRange.Minimum.AmountMinor
		candidate.PriceMinor = &amount
		maximum := product.PriceRange.Maximum.AmountMinor
		if maximum < amount {
			maximum = amount
		}
		basis := "OBSERVED"
		if product.Source() == researchdomain.SourceShopify {
			basis = "PRODUCT_RANGE"
		}
		candidate.Price = &c.ResponsePrice{MinimumMinor: amount, MaximumMinor: maximum, Currency: candidate.Currency, Basis: basis}
		if product.ExternalObservation != nil {
			candidate.Price.ObservedAt = product.ExternalObservation.ObservedAt.UTC().Format(time.RFC3339Nano)
		}
		if product.VariantObservation != nil {
			candidate.Price.ObservedAt = product.VariantObservation.ObservedAt.UTC().Format(time.RFC3339Nano)
		}
	}
	if product.PreviewVariant != nil && product.PreviewVariant.Seller != nil {
		candidate.Seller = product.PreviewVariant.Seller.Name
	}
	if assessed {
		for _, fact := range append(append([]string{}, assessment.Features...), assessment.Specifications...) {
			if len(candidate.AssessmentFacts) == 6 {
				break
			}
			runes := []rune(fact)
			if len(runes) > 240 {
				runes = runes[:240]
			}
			candidate.AssessmentFacts = append(candidate.AssessmentFacts, string(runes))
		}
	}
	if assessed && assessment.AxisAssessment != nil {
		candidate.Assessed = true
		candidate.TotalScore = assessment.AxisAssessment.TotalScore
		candidate.RoundID = assessment.AxisAssessment.RoundID
		candidate.IntentPoint = assessment.IntentPoint
		labels := map[string]string{}
		for _, axis := range assessment.AxisAssessment.Criteria.Axes {
			labels[axis.AxisID] = axis.Label
		}
		for _, score := range assessment.AxisAssessment.Scores {
			candidate.Axes = append(candidate.Axes, c.ResponseAxis{Label: labels[score.AxisID], Score: score.ScorePercent, Basis: score.Basis, Explanation: score.Explanation})
		}
	}
	return candidate
}

// CombinationState only reads durable identities and user configuration. It is
// safe inside the Cart transaction and makes no provider request.
func (s threadResponseSource) CombinationState(ctx context.Context, user, curation string) (string, error) {
	view, err := s.research.LoadWorkspaceV2(ctx, user, curation, "", "", false)
	if err != nil {
		return "", err
	}
	return responseSourceState(view)
}
func responseSourceState(view research.CatalogWorkspaceViewV2) (string, error) {
	parts := []string{}
	for _, pool := range view.Pools {
		parts = append(parts, fmt.Sprintf("pool:%s:%d", pool.Metadata.TargetID, pool.Metadata.Version))
		for _, product := range pool.Products {
			parts = append(parts, "visible:"+pool.Metadata.TargetID+":"+product.ProviderProductID)
		}
	}
	for _, config := range view.Configurations {
		parts = append(parts, fmt.Sprintf("option:%s:%d:%s", config.CandidateID, config.Version, config.Variant.VariantID))
	}
	for _, reaction := range view.Interactions {
		parts = append(parts, fmt.Sprintf("reaction:%s:%s:%t:%s", reaction.CandidateID, reaction.VariantID, reaction.Pinned, reaction.Sentiment))
	}
	for _, reaction := range view.ProductReactions {
		parts = append(parts, fmt.Sprintf("product-reaction:%s:%d:%t:%s", reaction.CandidateID, reaction.Version, reaction.Pinned, reaction.Sentiment))
	}
	sort.Strings(parts)
	return sharedomain.CanonicalJSONHash(strings.Join(parts, "\n"))
}
