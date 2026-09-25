package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func (r *Repository) AuthorizeTargetV2(
	ctx context.Context,
	userID, curationID, targetID string,
) error {
	var exists bool
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM plan_targets target
			JOIN curations curation
			  ON curation.id=target.curation_id
			 AND curation.user_id=target.user_id
			WHERE target.user_id=$1
			  AND target.curation_id=$2
			  AND target.id=$3
			  AND target.removed_at IS NULL
			  AND curation.archived_at IS NULL
		)
	`, userID, curationID, targetID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("authorize catalog target: %w", err)
	}
	if !exists {
		return fault.New(fault.InvalidInput, "PHASE8_TARGET_NOT_AVAILABLE", false)
	}
	return nil
}

func (r *Repository) CatalogTargetMarketContextV2(
	ctx context.Context,
	userID, curationID, targetID string,
) (researchapp.CatalogMarketContextV2, error) {
	var result researchapp.CatalogMarketContextV2
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		-- Saved Shopify detail/option lookup uses its supported market, not the research budget.
		SELECT 'US', 'USD'
		FROM plan_targets target
		JOIN curations curation
		  ON curation.id=target.curation_id AND curation.user_id=target.user_id
		JOIN shopping_plans plan
		  ON plan.id=target.plan_id AND plan.user_id=target.user_id
		WHERE target.user_id=$1 AND target.curation_id=$2 AND target.id=$3
		  AND target.removed_at IS NULL AND curation.archived_at IS NULL
	`, userID, curationID, targetID).Scan(&result.Country, &result.Currency)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fault.New(fault.InvalidInput, "PHASE8_TARGET_NOT_AVAILABLE", false)
	}
	if err != nil {
		return result, fmt.Errorf("read catalog target market context: %w", err)
	}
	return result, nil
}

func (r *Repository) CatalogTargetSearchProfileV2(
	ctx context.Context,
	userID, curationID, targetID string,
) (researchapp.CatalogTargetSearchProfileV2, error) {
	var result researchapp.CatalogTargetSearchProfileV2
	var minimumAmount, maximumAmount, priceCurrency sql.NullString
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT target.id, target.normalized_intent, target.category,
		       target.target_hash, COALESCE(curation.research_country,plan.country), plan.budget_currency,
		       target.min_price_amount::text, target.max_price_amount::text,
		       target.price_currency
		FROM plan_targets target
		JOIN curations curation
		  ON curation.id=target.curation_id AND curation.user_id=target.user_id
		JOIN shopping_plans plan
		  ON plan.id=target.plan_id AND plan.user_id=target.user_id
		WHERE target.user_id=$1 AND target.curation_id=$2 AND target.id=$3
		  AND target.removed_at IS NULL AND curation.archived_at IS NULL
	`, userID, curationID, targetID).Scan(
		&result.TargetID,
		&result.NormalizedIntent,
		&result.Category,
		&result.TargetHash,
		&result.Market.Country,
		&result.Market.Currency,
		&minimumAmount,
		&maximumAmount,
		&priceCurrency,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fault.New(fault.InvalidInput, "PHASE8_TARGET_NOT_AVAILABLE", false)
	}
	if err != nil {
		return result, fmt.Errorf("read catalog target search profile: %w", err)
	}
	if minimumAmount.Valid || maximumAmount.Valid {
		currency := strings.ToUpper(strings.TrimSpace(priceCurrency.String))
		if !priceCurrency.Valid || currency != strings.ToUpper(strings.TrimSpace(result.Market.Currency)) {
			return result, fault.New(fault.InvalidInput, "PHASE8_PRICE_CONSTRAINT_INVALID", false)
		}
		if minimumAmount.Valid {
			value, moneyErr := shareddomain.NewMoney(minimumAmount.String, currency)
			if moneyErr != nil {
				return result, fault.Wrap(moneyErr, fault.InvalidInput, "PHASE8_PRICE_CONSTRAINT_INVALID", false)
			}
			result.MinimumPrice = &value
		}
		if maximumAmount.Valid {
			value, moneyErr := shareddomain.NewMoney(maximumAmount.String, currency)
			if moneyErr != nil {
				return result, fault.Wrap(moneyErr, fault.InvalidInput, "PHASE8_PRICE_CONSTRAINT_INVALID", false)
			}
			result.MaximumPrice = &value
		}
	}
	return result, nil
}

func (r *Repository) CatalogCurationMarketContextV2(
	ctx context.Context,
	userID, curationID string,
) (researchapp.CatalogMarketContextV2, error) {
	var result researchapp.CatalogMarketContextV2
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		-- Saved Shopify detail/option lookup uses its supported market, not the research budget.
		SELECT 'US', 'USD'
		FROM curations curation
		JOIN shopping_plans plan
		  ON plan.id=curation.shopping_plan_id AND plan.user_id=curation.user_id
		WHERE curation.user_id=$1 AND curation.id=$2 AND curation.archived_at IS NULL
	`, userID, curationID).Scan(&result.Country, &result.Currency)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fault.New(fault.InvalidInput, "PHASE8_CURATION_NOT_AVAILABLE", false)
	}
	if err != nil {
		return result, fmt.Errorf("read catalog curation market context: %w", err)
	}
	return result, nil
}

func (r *Repository) LoadCatalogWorkspaceStateV2(
	ctx context.Context,
	userID, curationID string,
) (researchapp.CatalogWorkspaceStoredStateV2, error) {
	state := researchapp.CatalogWorkspaceStoredStateV2{
		Pools:          []researchapp.CatalogPoolMetadataV2{},
		Candidates:     []researchapp.CatalogCandidateReferenceV2{},
		Configurations: []researchapp.CatalogCandidateConfigurationV2{},
		Interactions:   []researchapp.CatalogVariantInteractionV2{},
	}
	queryer := r.database.Queryer(ctx)
	rows, err := queryer.QueryContext(ctx, `
		SELECT plan_target_id, version, expand_ordinal,
		       COALESCE(latest_mode, ''), latest_duration_milliseconds,
		       latest_shopify_calls, latest_rate_remaining, source_coverage, discovery_outcome
		FROM phase8_research_pools
		WHERE user_id=$1 AND curation_id=$2
		ORDER BY plan_target_id
	`, userID, curationID)
	if err != nil {
		return state, fmt.Errorf("list catalog pools: %w", err)
	}
	for rows.Next() {
		var value researchapp.CatalogPoolMetadataV2
		var mode string
		var sourceCoverage, discoveryOutcome []byte
		if err := rows.Scan(&value.TargetID, &value.Version, &value.ExpandOrdinal,
			&mode, &value.LatestDurationMilliseconds, &value.LatestShopifyCalls,
			&value.LatestRateRemaining, &sourceCoverage, &discoveryOutcome); err != nil {
			rows.Close()
			return state, fmt.Errorf("scan catalog pool: %w", err)
		}
		if len(discoveryOutcome) > 0 {
			if err := json.Unmarshal(discoveryOutcome, &value.DiscoveryOutcome); err != nil {
				rows.Close()
				return state, err
			}
		}
		if err := json.Unmarshal(sourceCoverage, &value.SourceCoverage); err != nil {
			rows.Close()
			return state, err
		}
		value.LatestMode = researchapp.CatalogResearchModeV2(mode)
		state.Pools = append(state.Pools, value)
	}
	if err := rows.Close(); err != nil {
		return state, err
	}
	rows, err = queryer.QueryContext(ctx, `
		SELECT plan_target_id, candidate_id, provider_product_id, source_kind,
		       identity_key, locator_kind, product_url, variant_id, seller_domain,
		       intent_point_snapshot, feature_lines_snapshot,
		       specification_lines_snapshot,
		       visible, display_order, first_seen_at, last_seen_at, external_observation, axis_assessment,
		       amazon_observation, COALESCE(evaluation_round_id::text, '')
		FROM phase8_research_candidates
		WHERE user_id=$1 AND curation_id=$2
		ORDER BY plan_target_id, display_order, candidate_id
	`, userID, curationID)
	if err != nil {
		return state, fmt.Errorf("list catalog candidates: %w", err)
	}
	for rows.Next() {
		value, err := scanCatalogCandidateReference(rows, userID, curationID)
		if err != nil {
			rows.Close()
			return state, err
		}
		state.Candidates = append(state.Candidates, value)
	}
	if err := rows.Close(); err != nil {
		return state, err
	}
	rows, err = queryer.QueryContext(ctx, `
		SELECT plan_target_id, candidate_id, variant_id, selected_options,
		       observed_at, updated_at, version
		FROM phase8_candidate_configurations
		WHERE user_id=$1 AND curation_id=$2
	`, userID, curationID)
	if err != nil {
		return state, fmt.Errorf("list catalog configurations: %w", err)
	}
	for rows.Next() {
		var value researchapp.CatalogCandidateConfigurationV2
		var raw []byte
		if err := rows.Scan(&value.TargetID, &value.CandidateID, &value.VariantID,
			&raw, &value.ObservedAt, &value.UpdatedAt, &value.Version); err != nil {
			rows.Close()
			return state, fmt.Errorf("scan catalog configuration: %w", err)
		}
		if err := json.Unmarshal(raw, &value.SelectedOptions); err != nil {
			rows.Close()
			return state, fmt.Errorf("decode catalog configuration options: %w", err)
		}
		state.Configurations = append(state.Configurations, value)
	}
	if err := rows.Close(); err != nil {
		return state, err
	}
	rows, err = queryer.QueryContext(ctx, `
		SELECT plan_target_id, candidate_id, variant_id, pinned, sentiment, updated_at
		FROM phase8_variant_interactions
		WHERE user_id=$1 AND curation_id=$2
	`, userID, curationID)
	if err != nil {
		return state, fmt.Errorf("list catalog interactions: %w", err)
	}
	for rows.Next() {
		var value researchapp.CatalogVariantInteractionV2
		if err := rows.Scan(&value.TargetID, &value.CandidateID, &value.VariantID,
			&value.Pinned, &value.Sentiment, &value.UpdatedAt); err != nil {
			rows.Close()
			return state, fmt.Errorf("scan catalog interaction: %w", err)
		}
		state.Interactions = append(state.Interactions, value)
	}
	if err := rows.Close(); err != nil {
		return state, err
	}
	if err := rows.Err(); err != nil {
		return state, err
	}
	state.ProductInteractions, err = r.ListProductReactions(ctx, userID, curationID)
	return state, err
}

func (r *Repository) ResolveCatalogCandidateV2(
	ctx context.Context,
	userID, curationID, candidateID string,
) (researchapp.CatalogCandidateReferenceV2, error) {
	row := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT plan_target_id, candidate_id, provider_product_id, source_kind,
		       identity_key, locator_kind, product_url, variant_id, seller_domain,
		       intent_point_snapshot, feature_lines_snapshot,
		       specification_lines_snapshot,
		       visible, display_order, first_seen_at, last_seen_at, external_observation, axis_assessment,
		       amazon_observation, COALESCE(evaluation_round_id::text, '')
		FROM phase8_research_candidates
		WHERE user_id=$1 AND curation_id=$2 AND candidate_id=$3
	`, userID, curationID, candidateID)
	value, err := scanCatalogCandidateReference(row, userID, curationID)
	if errors.Is(err, sql.ErrNoRows) {
		return researchapp.CatalogCandidateReferenceV2{}, researchapp.ErrCatalogCandidateNotFoundV2
	}
	return value, err
}

func (r *Repository) SaveCatalogCandidateConfigurationV2(
	ctx context.Context,
	userID, curationID string,
	value researchapp.CatalogCandidateConfigurationV2,
) error {
	options, err := json.Marshal(value.SelectedOptions)
	if err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO phase8_candidate_configurations(
			user_id, curation_id, plan_target_id, candidate_id,
			variant_id, selected_options, observed_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (user_id, curation_id, candidate_id)
		DO UPDATE SET variant_id=EXCLUDED.variant_id,
		              selected_options=EXCLUDED.selected_options,
		              observed_at=EXCLUDED.observed_at,
		              updated_at=EXCLUDED.updated_at
	`, userID, curationID, value.TargetID, value.CandidateID,
		value.VariantID, options, value.ObservedAt, value.UpdatedAt)
	if err != nil {
		return fmt.Errorf("save catalog candidate configuration: %w", err)
	}
	return nil
}

func (r *Repository) SaveCatalogVariantPreferenceV2(
	ctx context.Context,
	userID, curationID string,
	value researchapp.CatalogVariantInteractionV2,
	liked *researchdomain.LikedVariantV2,
) error {
	if (value.Sentiment == "LIKE") != (liked != nil) {
		return researchdomain.ErrLikedVariantInvalid
	}
	if liked != nil && (liked.UserID != userID || liked.CurationID != curationID ||
		liked.CandidateID != value.CandidateID || liked.VariantID != value.VariantID) {
		return researchdomain.ErrLikedVariantInvalid
	}
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if _, err := queryer.ExecContext(txContext, `
			INSERT INTO phase8_variant_interactions(
				user_id, curation_id, plan_target_id, candidate_id,
				variant_id, pinned, sentiment, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (user_id, curation_id, candidate_id, variant_id)
			DO UPDATE SET pinned=EXCLUDED.pinned,
			              sentiment=EXCLUDED.sentiment,
			              updated_at=EXCLUDED.updated_at
		`, userID, curationID, value.TargetID, value.CandidateID,
			value.VariantID, value.Pinned, value.Sentiment, value.UpdatedAt); err != nil {
			return fmt.Errorf("save catalog variant interaction: %w", err)
		}
		if liked == nil {
			if _, err := queryer.ExecContext(txContext, `
				DELETE FROM phase8_liked_variants
				WHERE user_id=$1 AND curation_id=$2
				  AND candidate_id=$3 AND variant_id=$4
			`, userID, curationID, value.CandidateID, value.VariantID); err != nil {
				return fmt.Errorf("delete catalog liked variant with interaction: %w", err)
			}
			return nil
		}
		if _, err := queryer.ExecContext(txContext, `
			INSERT INTO phase8_liked_variants(
				user_id, curation_id, candidate_id, variant_id,
				product_title, variant_title, product_url, merchant,
				price_minor, currency, target_title, updated_at, price_unknown
			) VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13)
			ON CONFLICT (user_id, candidate_id, variant_id) DO UPDATE SET
				curation_id=EXCLUDED.curation_id,
				product_title=EXCLUDED.product_title,
				variant_title=EXCLUDED.variant_title,
				product_url=EXCLUDED.product_url,
				merchant=EXCLUDED.merchant,
				price_minor=EXCLUDED.price_minor,
				price_unknown=EXCLUDED.price_unknown,
				currency=EXCLUDED.currency,
				target_title=EXCLUDED.target_title,
				updated_at=EXCLUDED.updated_at
		`, liked.UserID, liked.CurationID, liked.CandidateID, liked.VariantID,
			liked.ProductTitle, liked.VariantTitle, liked.ProductURL, liked.Merchant,
			liked.PriceMinor, liked.Currency, liked.TargetTitle, liked.UpdatedAt, liked.PriceUnknown); err != nil {
			return fmt.Errorf("upsert catalog liked variant with interaction: %w", err)
		}
		return nil
	})
}

type catalogRow interface{ Scan(...any) error }

func scanCatalogCandidateReference(
	row catalogRow,
	userID, curationID string,
) (researchapp.CatalogCandidateReferenceV2, error) {
	var value researchapp.CatalogCandidateReferenceV2
	var locatorKind string
	var productURL, variantID, sellerDomain sql.NullString
	var featureLines, specificationLines, externalObservation, axisAssessment, amazonObservation []byte
	err := row.Scan(&value.PlanTargetID, &value.CandidateID,
		&value.ProviderProductID, &value.SourceKind, &value.IdentityKey,
		&locatorKind, &productURL, &variantID, &sellerDomain,
		&value.Assessment.IntentPoint, &featureLines, &specificationLines,
		&value.Visible, &value.DisplayOrder, &value.FirstSeenAt, &value.LastSeenAt, &externalObservation, &axisAssessment, &amazonObservation,
		&value.EvaluationRoundID)
	if err != nil {
		return value, err
	}
	if len(axisAssessment) > 0 {
		if err := json.Unmarshal(axisAssessment, &value.Assessment.AxisAssessment); err != nil {
			return value, err
		}
	}
	if len(amazonObservation) > 0 {
		if err := json.Unmarshal(amazonObservation, &value.AmazonObservation); err != nil {
			return value, err
		}
		if value.AmazonObservation == nil || value.AmazonObservation.Validate() != nil {
			return value, researchdomain.ErrSourceReference
		}
	}
	if len(externalObservation) > 0 {
		if err := json.Unmarshal(externalObservation, &value.ExternalObservation); err != nil {
			return value, err
		}
		if value.ExternalObservation == nil || value.ExternalObservation.Validate() != nil || value.ExternalObservation.ProductRef != value.ProductRef() {
			return value, researchdomain.ErrSourceReference
		}
	}
	value.UserID, value.CurationID = userID, curationID
	value.Locator.Kind = researchapp.CatalogLocatorKind(locatorKind)
	if productURL.Valid {
		value.Locator.ProductURL = &researchapp.CatalogProductURLLocator{CanonicalURL: productURL.String}
	}
	if variantID.Valid && sellerDomain.Valid {
		value.Locator.MerchantVariant = &researchapp.CatalogMerchantVariantLocator{
			VariantID: variantID.String, SellerDomain: sellerDomain.String,
		}
	}
	if err := json.Unmarshal(featureLines, &value.Assessment.Features); err != nil {
		return value, fmt.Errorf("decode catalog candidate features: %w", err)
	}
	if err := json.Unmarshal(specificationLines, &value.Assessment.Specifications); err != nil {
		return value, fmt.Errorf("decode catalog candidate specifications: %w", err)
	}
	return value, nil
}

func catalogAssessmentColumnsV2(
	assessment researchapp.LiveCandidateAssessmentV2,
) (string, []byte, []byte, error) {
	features := append([]string{}, assessment.Features...)
	specifications := append([]string{}, assessment.Specifications...)
	featureLines, err := json.Marshal(features)
	if err != nil {
		return "", nil, nil, fmt.Errorf("encode catalog candidate features: %w", err)
	}
	specificationLines, err := json.Marshal(specifications)
	if err != nil {
		return "", nil, nil, fmt.Errorf("encode catalog candidate specifications: %w", err)
	}
	return strings.TrimSpace(assessment.IntentPoint), featureLines, specificationLines, nil
}

func catalogLocatorColumns(
	locator researchapp.CatalogProductLocator,
) (string, any, any, any, error) {
	if err := locator.Validate(); err != nil {
		return "", nil, nil, nil, err
	}
	if locator.ProductURL != nil {
		return string(researchapp.CatalogLocatorProductURL),
			locator.ProductURL.CanonicalURL, nil, nil, nil
	}
	return string(researchapp.CatalogLocatorMerchantVariant), nil,
		locator.MerchantVariant.VariantID, locator.MerchantVariant.SellerDomain, nil
}

// Expand the last searched provider context. Editing the next-round setting
// does not reinterpret the existing pool's query, prices or country.
func (r *Repository) CatalogExpansionSearchProfileV2(ctx context.Context, user, curation, target string) (researchapp.CatalogTargetSearchProfileV2, error) {
	p, err := r.CatalogTargetSearchProfileV2(ctx, user, curation, target)
	if err != nil {
		return p, err
	}
	err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT COALESCE(pool.provider_country,t.country),COALESCE(pool.provider_query,t.normalized_intent) FROM plan_targets t LEFT JOIN phase8_research_pools pool ON pool.user_id=t.user_id AND pool.curation_id=t.curation_id AND pool.plan_target_id=t.id WHERE t.user_id=$1 AND t.curation_id=$2 AND t.id=$3 AND t.removed_at IS NULL`, user, curation, target).Scan(&p.Market.Country, &p.NormalizedIntent)
	return p, err
}
