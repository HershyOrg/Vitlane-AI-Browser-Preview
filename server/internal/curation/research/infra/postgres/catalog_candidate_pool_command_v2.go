package postgres

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

const (
	catalogCandidatePoolMaximumV2      = 50
	catalogCandidatePoolLeaseSecondsV2 = 120
)

func (r *Repository) PreflightCatalogSearchV2(
	ctx context.Context,
	command researchapp.CatalogCandidatePoolCommandV2,
) (researchapp.CatalogCandidatePoolPreflightV2, error) {
	var result researchapp.CatalogCandidatePoolPreflightV2
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if _, err := queryer.ExecContext(txContext, `
			SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
		`, "phase8-pool-command:"+command.UserID+":"+command.IdempotencyKey); err != nil {
			return fmt.Errorf("lock catalog pool command idempotency: %w", err)
		}

		if err := lockCatalogPoolCommandTargetV2(txContext, queryer, command); err != nil {
			return err
		}

		existing, found, err := readCatalogPoolCommandV2(
			txContext, queryer, command.UserID, command.IdempotencyKey, true,
		)
		if err != nil {
			return err
		}
		reclaimExpired := false
		if found {
			if !sameCatalogPoolCommandV2(existing, command) {
				return fault.New(
					fault.Conflict,
					researchapp.CatalogCandidatePoolIdempotencyConflictV2,
					false,
				)
			}
			if existing.Status == catalogPoolCommandCompletedV2 {
				currentCandidateIDs, err := readCatalogCurrentCandidateIDsV2(
					txContext, queryer, command.UserID, command.CurationID, command.TargetID,
				)
				if err != nil {
					return err
				}
				result.Replay = true
				result.Pool = existing.Pool
				// Shopify display observations and messages are deliberately not
				// durable. Return the current durable visible projection so an HTTP
				// retry after a lost success response can converge without executing
				// the catalog command or calling Shopify again.
				result.Pool.AdmittedCandidateIDs = currentCandidateIDs
				return nil
			}
			if existing.LeaseActive {
				return fault.New(
					fault.Conflict,
					researchapp.CatalogCandidatePoolSearchInProgressV2,
					true,
				)
			}
			reclaimExpired = true
		}

		currentVersion, candidateCount, err := lockCatalogPoolStateV2(
			txContext, queryer, command.UserID, command.CurationID, command.TargetID,
		)
		if err != nil {
			return err
		}
		if currentVersion != command.ExpectedPoolVersion {
			return fault.New(
				fault.Conflict,
				researchapp.CatalogCandidatePoolVersionConflictV2,
				true,
			)
		}
		// Only pending rows of this exact open Round can resume at capacity.
		// New identities remain guarded by the normal 50-row write limit.
		if command.Mode == researchapp.CatalogResearchAppendV2 && candidateCount >= catalogCandidatePoolMaximumV2 {
			var resumable bool
			if command.ResumeRoundID != "" {
				if err := queryer.QueryRowContext(txContext, `SELECT EXISTS (
				 SELECT 1 FROM phase8_research_candidates c
				 JOIN research_rounds rr ON rr.id=c.evaluation_round_id
				 WHERE c.user_id=$1 AND c.curation_id=$2 AND c.plan_target_id=$3
				 AND c.evaluation_round_id::text=$4 AND c.axis_assessment IS NULL
				 AND rr.user_id=$1 AND rr.status='REQUESTED')`, command.UserID, command.CurationID, command.TargetID, command.ResumeRoundID).Scan(&resumable); err != nil {
					return err
				}
			}
			if !resumable {
				return fault.New(fault.Conflict, researchapp.CatalogCandidatePoolCapacityReachedV2, false)
			}
		}

		fencingToken, err := newCatalogPoolFencingTokenV2()
		if err != nil {
			return fmt.Errorf("issue catalog pool fencing token: %w", err)
		}
		if reclaimExpired {
			updated, err := queryer.QueryContext(txContext, `
				UPDATE phase8_research_pool_commands
				SET fencing_token=$3,
				    lease_expires_at=CURRENT_TIMESTAMP +
				        make_interval(secs => $4::double precision),
				    updated_at=CURRENT_TIMESTAMP
				WHERE user_id=$1 AND idempotency_key=$2
				  AND status='RUNNING'
				  AND lease_expires_at <= CURRENT_TIMESTAMP
				RETURNING lease_expires_at
			`, command.UserID, command.IdempotencyKey, fencingToken,
				catalogCandidatePoolLeaseSecondsV2)
			if err != nil {
				return fmt.Errorf("reclaim catalog pool command: %w", err)
			}
			defer updated.Close()
			if !updated.Next() {
				if err := updated.Err(); err != nil {
					return fmt.Errorf("read reclaimed catalog pool command: %w", err)
				}
				return fault.New(
					fault.Conflict,
					researchapp.CatalogCandidatePoolSearchInProgressV2,
					true,
				)
			}
			if err := updated.Scan(&result.LeaseExpiresAt); err != nil {
				return fmt.Errorf("scan reclaimed catalog pool command: %w", err)
			}
			result.FencingToken = fencingToken
			return nil
		}

		// The partial unique index deliberately treats an expired row as
		// RUNNING until a serialized preflight reclaims the Target. Removing the
		// stale row here lets a different idempotency key proceed while its late
		// provider response remains fenced by the now-missing token.
		if _, err := queryer.ExecContext(txContext, `
			DELETE FROM phase8_research_pool_commands
			WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
			  AND status='RUNNING'
			  AND lease_expires_at <= CURRENT_TIMESTAMP
		`, command.UserID, command.CurationID, command.TargetID); err != nil {
			return fmt.Errorf("remove expired catalog pool command: %w", err)
		}

		var active bool
		if err := queryer.QueryRowContext(txContext, `
			SELECT EXISTS(
				SELECT 1
				FROM phase8_research_pool_commands
				WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
				  AND status='RUNNING'
				  AND lease_expires_at > CURRENT_TIMESTAMP
			)
		`, command.UserID, command.CurationID, command.TargetID).Scan(&active); err != nil {
			return fmt.Errorf("read active catalog pool command: %w", err)
		}
		if active {
			return fault.New(
				fault.Conflict,
				researchapp.CatalogCandidatePoolSearchInProgressV2,
				true,
			)
		}
		var budgetSnapshot any
		if command.Budget != nil {
			raw, e := json.Marshal(command.Budget)
			if e != nil {
				return e
			}
			budgetSnapshot = string(raw)
		}
		if err := queryer.QueryRowContext(txContext, `
			INSERT INTO phase8_research_pool_commands(
				user_id, curation_id, plan_target_id, idempotency_key,
				request_hash, expected_pool_version, mode, status,
				fencing_token, lease_expires_at, created_at, updated_at, budget_snapshot
			) VALUES (
				$1,$2,$3,$4,$5,$6,$7,'RUNNING',$8,
				CURRENT_TIMESTAMP + make_interval(secs => $9::double precision),
				CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,$10::jsonb
			)
			RETURNING lease_expires_at
		`, command.UserID, command.CurationID, command.TargetID,
			command.IdempotencyKey, command.RequestHash,
			command.ExpectedPoolVersion, string(command.Mode), fencingToken,
			catalogCandidatePoolLeaseSecondsV2, budgetSnapshot).Scan(&result.LeaseExpiresAt); err != nil {
			return fmt.Errorf("reserve catalog pool command: %w", err)
		}
		result.FencingToken = fencingToken
		return nil
	})
	return result, err
}

func readCatalogCurrentCandidateIDsV2(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (sharedpostgres.Rows, error)
	},
	userID, curationID, targetID string,
) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT candidate_id
		FROM phase8_research_candidates
		WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
		  AND visible=true
		ORDER BY display_order, candidate_id
	`, userID, curationID, targetID)
	if err != nil {
		return nil, fmt.Errorf("list catalog replay candidate projection: %w", err)
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var candidateID string
		if err := rows.Scan(&candidateID); err != nil {
			return nil, fmt.Errorf("scan catalog replay candidate projection: %w", err)
		}
		result = append(result, candidateID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read catalog replay candidate projection: %w", err)
	}
	return result, nil
}

func (r *Repository) CompleteCatalogSearchV2(
	ctx context.Context,
	command researchapp.CatalogCandidatePoolCommandV2,
	candidates []researchapp.CatalogCandidateReferenceV2,
	metrics researchapp.LiveCatalogReviewMetricsV2,
) (researchapp.CatalogPoolMetadataV2, error) {
	return r.stageCatalogSearchV2(ctx, command, candidates, metrics, false)
}

// PublishObservedCandidatesV2 admits the observed products exactly as a
// completion would, but leaves the command RUNNING with a renewed lease so
// the same fencing token can fill assessments and finalize later. A row that
// already waits for this Round's evaluation (a retried attempt) is re-admitted
// instead of skipped as a duplicate.
func (r *Repository) PublishObservedCandidatesV2(
	ctx context.Context,
	command researchapp.CatalogCandidatePoolCommandV2,
	candidates []researchapp.CatalogCandidateReferenceV2,
	metrics researchapp.LiveCatalogReviewMetricsV2,
) (researchapp.CatalogPoolMetadataV2, error) {
	return r.stageCatalogSearchV2(ctx, command, candidates, metrics, true)
}

func (r *Repository) stageCatalogSearchV2(
	ctx context.Context,
	command researchapp.CatalogCandidatePoolCommandV2,
	candidates []researchapp.CatalogCandidateReferenceV2,
	metrics researchapp.LiveCatalogReviewMetricsV2,
	publishOnly bool,
) (researchapp.CatalogPoolMetadataV2, error) {
	if strings.TrimSpace(command.FencingToken) == "" {
		return researchapp.CatalogPoolMetadataV2{}, catalogPoolReservationLostV2()
	}
	var result researchapp.CatalogPoolMetadataV2
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		// Target removal/archive can race the response-scoped provider read.
		// Rechecking under the same lock used by preflight prevents a late result
		// from materializing into a no-longer-active Target.
		if err := lockCatalogPoolCommandTargetV2(txContext, queryer, command); err != nil {
			return err
		}
		existing, found, err := readCatalogPoolCommandV2(
			txContext, queryer, command.UserID, command.IdempotencyKey, true,
		)
		if err != nil {
			return err
		}
		if !found {
			return catalogPoolReservationLostV2()
		}
		if !sameCatalogPoolCommandV2(existing, command) {
			return fault.New(
				fault.Conflict,
				researchapp.CatalogCandidatePoolIdempotencyConflictV2,
				false,
			)
		}
		if !ownsCatalogPoolReservationV2(existing, command) {
			return catalogPoolReservationLostV2()
		}
		if existing.Status == catalogPoolCommandCompletedV2 {
			result = existing.Pool
			return nil
		}
		if !existing.LeaseActive {
			return catalogPoolReservationLostV2()
		}

		currentVersion, currentCount, err := lockCatalogPoolStateV2(
			txContext, queryer, command.UserID, command.CurationID, command.TargetID,
		)
		if err != nil {
			return err
		}
		if currentVersion != command.ExpectedPoolVersion {
			return fault.New(
				fault.Conflict,
				researchapp.CatalogCandidatePoolVersionConflictV2,
				true,
			)
		}

		now := metrics.CompletedAt.UTC()
		if now.IsZero() {
			now = time.Now().UTC()
		}
		resultVersion := command.ExpectedPoolVersion + 1
		poolExists := currentVersion > 0
		if !poolExists {
			if _, err := queryer.ExecContext(txContext, `
				INSERT INTO phase8_research_pools(
					user_id, curation_id, plan_target_id,
					version, expand_ordinal, created_at, updated_at
				) VALUES ($1,$2,$3,$4,0,$5,$5)
			`, command.UserID, command.CurationID, command.TargetID, resultVersion, now); err != nil {
				return fmt.Errorf("create catalog research pool: %w", err)
			}
		}

		var expandOrdinal int
		if poolExists {
			if err := queryer.QueryRowContext(txContext, `
				SELECT expand_ordinal
				FROM phase8_research_pools
				WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
			`, command.UserID, command.CurationID, command.TargetID).Scan(&expandOrdinal); err != nil {
				return fmt.Errorf("read catalog pool ordinal: %w", err)
			}
		}
		if command.Mode == researchapp.CatalogResearchAppendV2 {
			expandOrdinal++
		}

		var nextOrder int
		if err := queryer.QueryRowContext(txContext, `
			SELECT COALESCE(max(display_order), -1) + 1
			FROM phase8_research_candidates
			WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
		`, command.UserID, command.CurationID, command.TargetID).Scan(&nextOrder); err != nil {
			return fmt.Errorf("read catalog candidate order: %w", err)
		}

		admitted := make([]catalogCandidateAdmissionV2, 0, len(candidates))
		seen := make(map[string]struct{}, len(candidates))
		for _, candidate := range candidates {
			identityKey := strings.TrimSpace(candidate.IdentityKey)
			providerProductID := strings.TrimSpace(candidate.ProviderProductID)
			canonicalIdentityKey := candidate.ProductRef().IdentityKey()
			if identityKey == "" || strings.TrimSpace(candidate.CandidateID) == "" ||
				providerProductID == "" || identityKey != canonicalIdentityKey ||
				candidate.UserID != command.UserID || candidate.CurationID != command.CurationID ||
				candidate.PlanTargetID != command.TargetID {
				continue
			}
			if _, duplicate := seen[providerProductID]; duplicate {
				continue
			}
			locatorKind, productURL, variantID, sellerDomain, err := catalogLocatorColumns(candidate.Locator)
			if err != nil {
				continue
			}
			seen[providerProductID] = struct{}{}
			var existingCandidateID, existingEvaluationRound string
			var existingDisplayOrder int
			var existingVisible bool
			err = queryer.QueryRowContext(txContext, `
				SELECT candidate_id, display_order, visible, COALESCE(evaluation_round_id::text, '')
				FROM phase8_research_candidates
				WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
				  AND provider_product_id=$4 AND source_kind=$5
			`, command.UserID, command.CurationID, command.TargetID,
				providerProductID, candidate.SourceKind).Scan(
				&existingCandidateID, &existingDisplayOrder, &existingVisible, &existingEvaluationRound,
			)
			alreadyExists := err == nil
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("read catalog provider product identity: %w", err)
			}
			pendingSameRound := publishOnly && candidate.EvaluationRoundID != "" &&
				existingEvaluationRound == candidate.EvaluationRoundID
			if alreadyExists && command.Mode == researchapp.CatalogResearchAppendV2 && !pendingSameRound {
				continue
			}
			candidate.ProviderProductID = providerProductID
			candidate.IdentityKey = canonicalIdentityKey
			if !alreadyExists && currentCount >= catalogCandidatePoolMaximumV2 {
				continue
			}
			displayOrder := len(admitted)
			updateDisplayOrder := command.Mode == researchapp.CatalogResearchReplaceV2
			if command.Mode == researchapp.CatalogResearchAppendV2 {
				if alreadyExists && existingVisible {
					displayOrder = existingDisplayOrder
				} else {
					displayOrder = nextOrder
					nextOrder++
					// A previously hidden Candidate re-enters the default-visible
					// projection at its tail. Retaining its historical rank would
					// make the canonical reload jump ahead of the immediate APPEND UI.
					updateDisplayOrder = alreadyExists
				}
			}
			admitted = append(admitted, catalogCandidateAdmissionV2{
				Candidate: candidate, LocatorKind: locatorKind,
				ProductURL: productURL, VariantID: variantID,
				SellerDomain: sellerDomain, Existing: alreadyExists,
				DisplayOrder: displayOrder, UpdateDisplayOrder: updateDisplayOrder,
			})
			if !alreadyExists {
				currentCount++
			}
		}
		retainedPinnedCandidateIDs := []string{}
		preserve := []string{}
		if command.Mode == researchapp.CatalogResearchReplaceV2 && len(admitted) > 0 {
			for _, coverage := range metrics.SourceCoverage {
				if coverage.Status == "FAILED" || coverage.Status == "PARTIAL" || coverage.Status == "UNSUPPORTED" {
					kind := string(coverage.Source)
					if kind == "SHOPIFY" {
						kind = "SHOPIFY_LIVE"
					}
					preserve = append(preserve, kind)
				}
			}
			retainedPinnedCandidateIDs, err = readCatalogRetainedPinnedCandidateIDsV2(
				txContext, queryer, command.UserID, command.CurationID,
				command.TargetID, seen, preserve,
			)
			if err != nil {
				return err
			}
		}

		// REPLACE changes the default-visible projection only when at least one
		// valid result is admitted. Empty, invalid-only and capacity-only results
		// preserve the previous pointer-equivalent visibility set.
		if command.Mode == researchapp.CatalogResearchReplaceV2 && len(admitted) > 0 {
			if _, err := queryer.ExecContext(txContext, `
				UPDATE phase8_research_candidates candidate
				SET visible=false, last_seen_at=$4
				WHERE candidate.user_id=$1
				  AND candidate.curation_id=$2
				  AND candidate.plan_target_id=$3
 AND NOT (candidate.source_kind=ANY($5::text[]))
				  AND NOT EXISTS (
					SELECT 1 FROM phase8_variant_interactions interaction
					WHERE interaction.user_id=candidate.user_id
					  AND interaction.curation_id=candidate.curation_id
					  AND interaction.plan_target_id=candidate.plan_target_id
					  AND interaction.candidate_id=candidate.candidate_id
					  AND interaction.pinned=true
			UNION ALL
			SELECT 1 FROM research_product_reactions interaction
					WHERE interaction.user_id=candidate.user_id
					  AND interaction.curation_id=candidate.curation_id
					  AND interaction.plan_target_id=candidate.plan_target_id
					  AND interaction.candidate_id=candidate.candidate_id
					  AND interaction.pinned=true
				  )
			`, command.UserID, command.CurationID, command.TargetID, now, preserve); err != nil {
				return fmt.Errorf("hide replaced catalog candidates: %w", err)
			}
		}

		for index := range admitted {
			admission := &admitted[index]
			candidate := admission.Candidate
			intentPoint, featureLines, specificationLines, assessmentErr := catalogAssessmentColumnsV2(
				candidate.Assessment,
			)
			if assessmentErr != nil {
				return assessmentErr
			}
			var axisJSON []byte
			if candidate.Assessment.AxisAssessment != nil {
				axisJSON, err = json.Marshal(candidate.Assessment.AxisAssessment)
				if err != nil {
					return err
				}
			}
			var observationJSON []byte
			if candidate.ExternalObservation != nil {
				if !candidate.ProductRef().Source.KoreanExternal() || candidate.ExternalObservation.Validate() != nil || candidate.ExternalObservation.ProductRef != candidate.ProductRef() {
					return researchdomain.ErrSourceReference
				}
				observationJSON, err = json.Marshal(candidate.ExternalObservation)
				if err != nil {
					return err
				}
			} else if candidate.ProductRef().Source.KoreanExternal() {
				return researchdomain.ErrSourceReference
			}
			var evaluationRound any
			if candidate.EvaluationRoundID != "" {
				evaluationRound = candidate.EvaluationRoundID
			}
			var durableCandidateID string
			if err := queryer.QueryRowContext(txContext, `
				INSERT INTO phase8_research_candidates(
					user_id, curation_id, plan_target_id, candidate_id,
					provider_product_id, source_kind, identity_key, locator_kind,
					product_url, variant_id, seller_domain, intent_point_snapshot,
					feature_lines_snapshot, specification_lines_snapshot,
					visible, display_order, first_seen_at, last_seen_at, external_observation, axis_assessment,
					evaluation_round_id
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,true,$15,$16,$16,$18,$19,$20::uuid)
				ON CONFLICT (user_id, curation_id, plan_target_id, source_kind, provider_product_id)
				DO UPDATE SET visible=EXCLUDED.visible, display_order=CASE WHEN $17::boolean THEN EXCLUDED.display_order ELSE phase8_research_candidates.display_order END,
				  evaluation_round_id=COALESCE(EXCLUDED.evaluation_round_id, phase8_research_candidates.evaluation_round_id)

				RETURNING candidate_id
			`, command.UserID, command.CurationID, command.TargetID,
				candidate.CandidateID, candidate.ProviderProductID, candidate.SourceKind,
				candidate.IdentityKey, admission.LocatorKind, admission.ProductURL,
				admission.VariantID, admission.SellerDomain, intentPoint,
				featureLines, specificationLines, admission.DisplayOrder, now,
				admission.UpdateDisplayOrder, observationJSON, axisJSON, evaluationRound,
			).Scan(&durableCandidateID); err != nil {
				return fmt.Errorf("upsert catalog candidate: %w", err)
			}
			admission.DurableCandidateID = durableCandidateID
		}
		// Fresh REPLACE results own ranks [0,n). Pinned Candidates retained from
		// the previous visible projection follow them in their prior relative
		// order, so the immediate response and canonical reload cannot disagree
		// or collide on display_order.
		for index, candidateID := range retainedPinnedCandidateIDs {
			if _, err := queryer.ExecContext(txContext, `
				UPDATE phase8_research_candidates candidate
				SET display_order=$5
				WHERE candidate.user_id=$1
				  AND candidate.curation_id=$2
				  AND candidate.plan_target_id=$3
				  AND candidate.candidate_id=$4
				  AND candidate.visible=true
				  AND EXISTS (
					SELECT 1 FROM phase8_variant_interactions interaction
					WHERE interaction.user_id=candidate.user_id
					  AND interaction.curation_id=candidate.curation_id
					  AND interaction.plan_target_id=candidate.plan_target_id
					  AND interaction.candidate_id=candidate.candidate_id
					  AND interaction.pinned=true
			UNION ALL
			SELECT 1 FROM research_product_reactions interaction
					WHERE interaction.user_id=candidate.user_id
					  AND interaction.curation_id=candidate.curation_id
					  AND interaction.plan_target_id=candidate.plan_target_id
					  AND interaction.candidate_id=candidate.candidate_id
					  AND interaction.pinned=true
				  )
			`, command.UserID, command.CurationID, command.TargetID,
				candidateID, len(admitted)+index); err != nil {
				return fmt.Errorf("order retained pinned catalog candidate: %w", err)
			}
		}

		coverage := metrics.SourceCoverage
		if coverage == nil {
			coverage = []researchapp.SourceCoverage{}
		}
		coverageJSON, coverageErr := json.Marshal(coverage)
		if coverageErr != nil {
			return coverageErr
		}
		if _, err := queryer.ExecContext(txContext, `UPDATE phase8_research_pools SET source_coverage=$4,provider_country=COALESCE(NULLIF($5,''),provider_country),provider_query=COALESCE(NULLIF($6,''),provider_query) WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3`, command.UserID, command.CurationID, command.TargetID, coverageJSON, metrics.ProviderCountry, metrics.ProviderQuery); err != nil {
			return err
		}
		if metrics.DiscoveryOutcome != nil {
			raw, err := json.Marshal(metrics.DiscoveryOutcome)
			if err != nil {
				return err
			}
			if _, err = queryer.ExecContext(txContext, `UPDATE phase8_research_pools SET discovery_outcome=$4 WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3`, command.UserID, command.CurationID, command.TargetID, raw); err != nil {
				return err
			}
		}
		durationMilliseconds := metrics.Duration.Milliseconds()
		if durationMilliseconds < 0 {
			durationMilliseconds = 0
		}
		shopifyCalls := max(0, metrics.ShopifyCallCount)
		rateRemaining := max(0, metrics.LocalCallsRemaining)
		if poolExists {
			update, err := queryer.ExecContext(txContext, `
				UPDATE phase8_research_pools
				SET version=$4, expand_ordinal=$5, latest_mode=$6,
				    latest_duration_milliseconds=$7, latest_shopify_calls=$8,
				    latest_rate_remaining=$9, updated_at=$10
				WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
				  AND version=$11
			`, command.UserID, command.CurationID, command.TargetID,
				resultVersion, expandOrdinal, string(command.Mode),
				durationMilliseconds, shopifyCalls, rateRemaining, now,
				command.ExpectedPoolVersion)
			if err != nil {
				return fmt.Errorf("CAS catalog research pool: %w", err)
			}
			updated, err := update.RowsAffected()
			if err != nil {
				return fmt.Errorf("read catalog pool CAS result: %w", err)
			}
			if updated != 1 {
				return fault.New(
					fault.Conflict,
					researchapp.CatalogCandidatePoolVersionConflictV2,
					true,
				)
			}
		} else {
			if _, err := queryer.ExecContext(txContext, `
				UPDATE phase8_research_pools
				SET expand_ordinal=$4, latest_mode=$5,
				    latest_duration_milliseconds=$6, latest_shopify_calls=$7,
				    latest_rate_remaining=$8, updated_at=$9
				WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
			`, command.UserID, command.CurationID, command.TargetID,
				expandOrdinal, string(command.Mode), durationMilliseconds,
				shopifyCalls, rateRemaining, now); err != nil {
				return fmt.Errorf("finalize initial catalog pool: %w", err)
			}
		}

		var completion sql.Result
		if publishOnly {
			// The command stays RUNNING for the evaluation stages; renewing the
			// lease here is what keeps another Round from reclaiming it while
			// the model answers.
			completion, err = queryer.ExecContext(txContext, `
				UPDATE phase8_research_pool_commands
				SET lease_expires_at=CURRENT_TIMESTAMP + make_interval(secs => $6::double precision),
				    updated_at=$7
				WHERE user_id=$1 AND idempotency_key=$2
				  AND request_hash=$3 AND status='RUNNING' AND mode=$4
				  AND fencing_token=$5
				  AND lease_expires_at > CURRENT_TIMESTAMP
			`, command.UserID, command.IdempotencyKey, command.RequestHash,
				string(command.Mode), command.FencingToken, catalogCandidatePoolLeaseSecondsV2, now)
		} else {
			completion, err = queryer.ExecContext(txContext, `
				UPDATE phase8_research_pool_commands
				SET status='COMPLETED', result_pool_version=$6,
				    result_expand_ordinal=$7, result_duration_milliseconds=$8,
				    result_shopify_calls=$9, result_rate_remaining=$10,
				    completed_at=$11, updated_at=$11
				WHERE user_id=$1 AND idempotency_key=$2
				  AND request_hash=$3 AND status='RUNNING' AND mode=$4
				  AND fencing_token=$5
				  AND lease_expires_at > CURRENT_TIMESTAMP
			`, command.UserID, command.IdempotencyKey, command.RequestHash,
				string(command.Mode), command.FencingToken, resultVersion, expandOrdinal,
				durationMilliseconds, shopifyCalls, rateRemaining, now)
		}
		if err != nil {
			return fmt.Errorf("complete catalog pool command: %w", err)
		}
		completed, err := completion.RowsAffected()
		if err != nil {
			return fmt.Errorf("read catalog pool command completion: %w", err)
		}
		if completed != 1 {
			return catalogPoolReservationLostV2()
		}
		result = researchapp.CatalogPoolMetadataV2{
			TargetID: command.TargetID, Version: resultVersion,
			ExpandOrdinal: expandOrdinal, LatestMode: command.Mode,
			LatestDurationMilliseconds: durationMilliseconds,
			LatestShopifyCalls:         shopifyCalls, LatestRateRemaining: rateRemaining,
		}
		result.SourceCoverage = append([]researchapp.SourceCoverage(nil), metrics.SourceCoverage...)
		result.AdmittedCandidateIDs = make([]string, 0, len(admitted))
		result.CandidateIDBindings = make(
			[]researchapp.CatalogCandidateIDBindingV2, 0, len(admitted),
		)
		for _, admission := range admitted {
			result.AdmittedCandidateIDs = append(
				result.AdmittedCandidateIDs, admission.DurableCandidateID,
			)
			result.CandidateIDBindings = append(
				result.CandidateIDBindings,
				researchapp.CatalogCandidateIDBindingV2{
					ProposedCandidateID: admission.Candidate.CandidateID,
					DurableCandidateID:  admission.DurableCandidateID,
				},
			)
		}
		return nil
	})
	return result, err
}

// stagedCatalogCommandV2 verifies, under the same locks as a completion, that
// the caller still owns a RUNNING, leased command whose pool is at the
// version it last saw. It returns the locked pool version.
func stagedCatalogCommandV2(
	txContext context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
	},
	command researchapp.CatalogCandidatePoolCommandV2,
	expectedVersion int64,
) error {
	if err := lockCatalogPoolCommandTargetV2(txContext, queryer, command); err != nil {
		return err
	}
	existing, found, err := readCatalogPoolCommandV2(
		txContext, queryer, command.UserID, command.IdempotencyKey, true,
	)
	if err != nil {
		return err
	}
	if !found {
		return catalogPoolReservationLostV2()
	}
	if !sameCatalogPoolCommandV2(existing, command) {
		return fault.New(fault.Conflict, researchapp.CatalogCandidatePoolIdempotencyConflictV2, false)
	}
	if !ownsCatalogPoolReservationV2(existing, command) || existing.Status != catalogPoolCommandRunningV2 || !existing.LeaseActive {
		return catalogPoolReservationLostV2()
	}
	currentVersion, _, err := lockCatalogPoolStateV2(
		txContext, queryer, command.UserID, command.CurationID, command.TargetID,
	)
	if err != nil {
		return err
	}
	if currentVersion != expectedVersion {
		return fault.New(fault.Conflict, researchapp.CatalogCandidatePoolVersionConflictV2, true)
	}
	return nil
}

// FillCandidateAssessmentsV2 writes assessments once into the Round's
// candidates that have none. It is the nil→value fill of ADR-0083: a saved
// assessment is never overwritten, the pool version moves so the workspace
// refreshes, and the lease is renewed for the finalize that follows.
func (r *Repository) FillCandidateAssessmentsV2(
	ctx context.Context,
	command researchapp.CatalogCandidatePoolCommandV2,
	expectedVersion int64,
	assessments map[string]researchapp.LiveCandidateAssessmentV2,
	now time.Time,
) (int64, int, error) {
	if strings.TrimSpace(command.FencingToken) == "" {
		return 0, 0, catalogPoolReservationLostV2()
	}
	filled := 0
	resultVersion := expectedVersion + 1
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if err := stagedCatalogCommandV2(txContext, queryer, command, expectedVersion); err != nil {
			return err
		}
		for candidateID, assessment := range assessments {
			intentPoint, featureLines, specificationLines, err := catalogAssessmentColumnsV2(assessment)
			if err != nil {
				return err
			}
			var axisJSON []byte
			if assessment.AxisAssessment != nil {
				if axisJSON, err = json.Marshal(assessment.AxisAssessment); err != nil {
					return err
				}
			}
			updated, err := queryer.ExecContext(txContext, `
				UPDATE phase8_research_candidates
				SET axis_assessment=$5, intent_point_snapshot=$6,
				    feature_lines_snapshot=$7, specification_lines_snapshot=$8,
				    evaluation_round_id=CASE WHEN $5::jsonb IS NULL THEN evaluation_round_id ELSE NULL END,
				    last_seen_at=$9
				WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
				  AND candidate_id=$4 AND axis_assessment IS NULL
			`, command.UserID, command.CurationID, command.TargetID, candidateID,
				axisJSON, intentPoint, featureLines, specificationLines, now.UTC())
			if err != nil {
				return fmt.Errorf("fill catalog candidate assessment: %w", err)
			}
			if count, err := updated.RowsAffected(); err == nil && count == 1 && axisJSON != nil {
				filled++
			}
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE phase8_research_pools SET version=$4, updated_at=$5
			WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3 AND version=$6
		`, command.UserID, command.CurationID, command.TargetID, resultVersion, now.UTC(), expectedVersion); err != nil {
			return fmt.Errorf("bump catalog pool version after fill: %w", err)
		}
		renewed, err := queryer.ExecContext(txContext, `
			UPDATE phase8_research_pool_commands
			SET lease_expires_at=CURRENT_TIMESTAMP + make_interval(secs => $5::double precision), updated_at=$6
			WHERE user_id=$1 AND idempotency_key=$2 AND request_hash=$3 AND status='RUNNING'
			  AND fencing_token=$4 AND lease_expires_at > CURRENT_TIMESTAMP
		`, command.UserID, command.IdempotencyKey, command.RequestHash, command.FencingToken,
			catalogCandidatePoolLeaseSecondsV2, now.UTC())
		if err != nil {
			return fmt.Errorf("renew catalog pool command lease: %w", err)
		}
		if count, err := renewed.RowsAffected(); err != nil || count != 1 {
			return catalogPoolReservationLostV2()
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return resultVersion, filled, nil
}

// FinalizeStagedSearchV2 closes a staged Round: coverage, discovery outcome
// and latest metrics land on the pool, every candidate still marked as
// waiting for this Round is released (it stays unevaluated), the version
// moves once more and the command completes.
func (r *Repository) FinalizeStagedSearchV2(
	ctx context.Context,
	command researchapp.CatalogCandidatePoolCommandV2,
	expectedVersion int64,
	roundID string,
	published researchapp.CatalogPoolMetadataV2,
	metrics researchapp.LiveCatalogReviewMetricsV2,
) (researchapp.CatalogPoolMetadataV2, error) {
	if strings.TrimSpace(command.FencingToken) == "" {
		return researchapp.CatalogPoolMetadataV2{}, catalogPoolReservationLostV2()
	}
	var result researchapp.CatalogPoolMetadataV2
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if err := stagedCatalogCommandV2(txContext, queryer, command, expectedVersion); err != nil {
			return err
		}
		now := metrics.CompletedAt.UTC()
		if now.IsZero() {
			now = time.Now().UTC()
		}
		resultVersion := expectedVersion + 1
		coverage := metrics.SourceCoverage
		if coverage == nil {
			coverage = []researchapp.SourceCoverage{}
		}
		coverageJSON, err := json.Marshal(coverage)
		if err != nil {
			return err
		}
		var outcomeJSON []byte
		if metrics.DiscoveryOutcome != nil {
			if outcomeJSON, err = json.Marshal(metrics.DiscoveryOutcome); err != nil {
				return err
			}
		}
		durationMilliseconds := max(int64(0), metrics.Duration.Milliseconds())
		shopifyCalls := max(0, metrics.ShopifyCallCount)
		rateRemaining := max(0, metrics.LocalCallsRemaining)
		var expandOrdinal int
		if err := queryer.QueryRowContext(txContext, `
			UPDATE phase8_research_pools
			SET version=$4, latest_mode=$5, latest_duration_milliseconds=$6,
			    latest_shopify_calls=$7, latest_rate_remaining=$8, source_coverage=$9,
			    discovery_outcome=COALESCE($10::jsonb, discovery_outcome),
			    provider_country=COALESCE(NULLIF($11,''),provider_country),
			    provider_query=COALESCE(NULLIF($12,''),provider_query), updated_at=$13
			WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3 AND version=$14
			RETURNING expand_ordinal
		`, command.UserID, command.CurationID, command.TargetID, resultVersion, string(command.Mode),
			durationMilliseconds, shopifyCalls, rateRemaining, coverageJSON, outcomeJSON,
			metrics.ProviderCountry, metrics.ProviderQuery, now, expectedVersion).Scan(&expandOrdinal); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fault.New(fault.Conflict, researchapp.CatalogCandidatePoolVersionConflictV2, true)
			}
			return fmt.Errorf("finalize catalog research pool: %w", err)
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE phase8_research_candidates SET evaluation_round_id=NULL
			WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3 AND evaluation_round_id=$4::uuid
		`, command.UserID, command.CurationID, command.TargetID, roundID); err != nil {
			return fmt.Errorf("release catalog pending evaluations: %w", err)
		}
		completion, err := queryer.ExecContext(txContext, `
			UPDATE phase8_research_pool_commands
			SET status='COMPLETED', result_pool_version=$6,
			    result_expand_ordinal=$7, result_duration_milliseconds=$8,
			    result_shopify_calls=$9, result_rate_remaining=$10,
			    completed_at=$11, updated_at=$11
			WHERE user_id=$1 AND idempotency_key=$2
			  AND request_hash=$3 AND status='RUNNING' AND mode=$4
			  AND fencing_token=$5
			  AND lease_expires_at > CURRENT_TIMESTAMP
		`, command.UserID, command.IdempotencyKey, command.RequestHash,
			string(command.Mode), command.FencingToken, resultVersion, expandOrdinal,
			durationMilliseconds, shopifyCalls, rateRemaining, now)
		if err != nil {
			return fmt.Errorf("complete staged catalog pool command: %w", err)
		}
		if count, err := completion.RowsAffected(); err != nil || count != 1 {
			return catalogPoolReservationLostV2()
		}
		result = published
		result.TargetID = command.TargetID
		result.Version = resultVersion
		result.ExpandOrdinal = expandOrdinal
		result.LatestMode = command.Mode
		result.LatestDurationMilliseconds = durationMilliseconds
		result.LatestShopifyCalls = shopifyCalls
		result.LatestRateRemaining = rateRemaining
		result.SourceCoverage = append([]researchapp.SourceCoverage(nil), metrics.SourceCoverage...)
		result.DiscoveryOutcome = metrics.DiscoveryOutcome
		return nil
	})
	return result, err
}

func readCatalogRetainedPinnedCandidateIDsV2(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (sharedpostgres.Rows, error)
	},
	userID, curationID, targetID string,
	admittedProviderProductIDs map[string]struct{},
	preserve []string,
) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT candidate.candidate_id, candidate.provider_product_id
		FROM phase8_research_candidates candidate
		WHERE candidate.user_id=$1
		  AND candidate.curation_id=$2
		  AND candidate.plan_target_id=$3
		  AND candidate.visible=true
		  AND (candidate.source_kind=ANY($4::text[]) OR EXISTS (
			SELECT 1 FROM phase8_variant_interactions interaction
			WHERE interaction.user_id=candidate.user_id
			  AND interaction.curation_id=candidate.curation_id
			  AND interaction.plan_target_id=candidate.plan_target_id
			  AND interaction.candidate_id=candidate.candidate_id
			  AND interaction.pinned=true
			UNION ALL
			SELECT 1 FROM research_product_reactions interaction
			WHERE interaction.user_id=candidate.user_id
			  AND interaction.curation_id=candidate.curation_id
			  AND interaction.plan_target_id=candidate.plan_target_id
			  AND interaction.candidate_id=candidate.candidate_id
			  AND interaction.pinned=true
		  ))
		ORDER BY candidate.display_order, candidate.candidate_id
	`, userID, curationID, targetID, preserve)
	if err != nil {
		return nil, fmt.Errorf("list retained pinned catalog candidates: %w", err)
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var candidateID, providerProductID string
		if err := rows.Scan(&candidateID, &providerProductID); err != nil {
			return nil, fmt.Errorf("scan retained pinned catalog candidate: %w", err)
		}
		if _, observed := admittedProviderProductIDs[providerProductID]; !observed {
			result = append(result, candidateID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read retained pinned catalog candidates: %w", err)
	}
	return result, nil
}

func (r *Repository) AbortCatalogSearchV2(
	ctx context.Context,
	command researchapp.CatalogCandidatePoolCommandV2,
) error {
	if strings.TrimSpace(command.FencingToken) == "" {
		return catalogPoolReservationLostV2()
	}
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		existing, found, err := readCatalogPoolCommandV2(
			txContext, queryer, command.UserID, command.IdempotencyKey, true,
		)
		if err != nil {
			return err
		}
		if !found {
			return catalogPoolReservationLostV2()
		}
		if !sameCatalogPoolCommandV2(existing, command) {
			return fault.New(
				fault.Conflict,
				researchapp.CatalogCandidatePoolIdempotencyConflictV2,
				false,
			)
		}
		if !ownsCatalogPoolReservationV2(existing, command) {
			return catalogPoolReservationLostV2()
		}
		if existing.Status == catalogPoolCommandCompletedV2 {
			return nil
		}
		deleted, err := queryer.ExecContext(txContext, `
			DELETE FROM phase8_research_pool_commands
			WHERE user_id=$1 AND idempotency_key=$2
			  AND request_hash=$3 AND status='RUNNING'
			  AND fencing_token=$4
		`, command.UserID, command.IdempotencyKey, command.RequestHash,
			command.FencingToken)
		if err != nil {
			return fmt.Errorf("abort catalog pool command: %w", err)
		}
		count, err := deleted.RowsAffected()
		if err != nil {
			return fmt.Errorf("read aborted catalog pool command: %w", err)
		}
		if count != 1 {
			return catalogPoolReservationLostV2()
		}
		return nil
	})
}

type catalogPoolCommandStatusV2 string

const (
	catalogPoolCommandRunningV2   catalogPoolCommandStatusV2 = "RUNNING"
	catalogPoolCommandCompletedV2 catalogPoolCommandStatusV2 = "COMPLETED"
)

type catalogPoolCommandRecordV2 struct {
	Command        researchapp.CatalogCandidatePoolCommandV2
	Status         catalogPoolCommandStatusV2
	Pool           researchapp.CatalogPoolMetadataV2
	LeaseExpiresAt time.Time
	LeaseActive    bool
}

type catalogCandidateAdmissionV2 struct {
	Candidate          researchapp.CatalogCandidateReferenceV2
	LocatorKind        string
	ProductURL         any
	VariantID          any
	SellerDomain       any
	Existing           bool
	DisplayOrder       int
	UpdateDisplayOrder bool
	DurableCandidateID string
}

func readCatalogPoolCommandV2(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
	},
	userID, idempotencyKey string,
	forUpdate bool,
) (catalogPoolCommandRecordV2, bool, error) {
	query := `
		SELECT curation_id, plan_target_id, request_hash,
		       expected_pool_version, mode, status,
		       fencing_token, lease_expires_at,
		       lease_expires_at > CURRENT_TIMESTAMP,
		       result_pool_version, result_expand_ordinal,
		       result_duration_milliseconds, result_shopify_calls,
		       result_rate_remaining
		FROM phase8_research_pool_commands
		WHERE user_id=$1 AND idempotency_key=$2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var record catalogPoolCommandRecordV2
	var mode, status string
	var resultVersion sql.NullInt64
	var resultOrdinal, resultDuration, resultCalls, resultRemaining sql.NullInt64
	err := queryer.QueryRowContext(ctx, query, userID, idempotencyKey).Scan(
		&record.Command.CurationID, &record.Command.TargetID,
		&record.Command.RequestHash, &record.Command.ExpectedPoolVersion,
		&mode, &status, &record.Command.FencingToken, &record.LeaseExpiresAt,
		&record.LeaseActive, &resultVersion, &resultOrdinal, &resultDuration,
		&resultCalls, &resultRemaining,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return record, false, nil
	}
	if err != nil {
		return record, false, fmt.Errorf("read catalog pool command: %w", err)
	}
	record.Command.UserID = userID
	record.Command.IdempotencyKey = idempotencyKey
	record.Command.Mode = researchapp.CatalogResearchModeV2(mode)
	record.Status = catalogPoolCommandStatusV2(status)
	if record.Status == catalogPoolCommandCompletedV2 {
		record.Pool = researchapp.CatalogPoolMetadataV2{
			TargetID: record.Command.TargetID, Version: resultVersion.Int64,
			ExpandOrdinal: int(resultOrdinal.Int64), LatestMode: record.Command.Mode,
			LatestDurationMilliseconds: resultDuration.Int64,
			LatestShopifyCalls:         int(resultCalls.Int64),
			LatestRateRemaining:        int(resultRemaining.Int64),
		}
	}
	return record, true, nil
}

func lockCatalogPoolStateV2(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
	},
	userID, curationID, targetID string,
) (int64, int, error) {
	var version int64
	err := queryer.QueryRowContext(ctx, `
		SELECT version
		FROM phase8_research_pools
		WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
		FOR UPDATE
	`, userID, curationID, targetID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("lock catalog research pool: %w", err)
	}
	var count int
	if err := queryer.QueryRowContext(ctx, `
		SELECT count(*)
		FROM phase8_research_candidates
		WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
	`, userID, curationID, targetID).Scan(&count); err != nil {
		return 0, 0, fmt.Errorf("count catalog candidates: %w", err)
	}
	return version, count, nil
}

func lockCatalogPoolCommandTargetV2(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
	},
	command researchapp.CatalogCandidatePoolCommandV2,
) error {
	var targetMarker int
	err := queryer.QueryRowContext(ctx, `
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
		FOR UPDATE OF target
	`, command.UserID, command.CurationID, command.TargetID).Scan(&targetMarker)
	if errors.Is(err, sql.ErrNoRows) {
		return fault.New(fault.InvalidInput, "PHASE8_TARGET_NOT_AVAILABLE", false)
	}
	if err != nil {
		return fmt.Errorf("lock catalog pool command target: %w", err)
	}
	return nil
}

func sameCatalogPoolCommandV2(
	record catalogPoolCommandRecordV2,
	command researchapp.CatalogCandidatePoolCommandV2,
) bool {
	return record.Command.UserID == command.UserID &&
		record.Command.CurationID == command.CurationID &&
		record.Command.TargetID == command.TargetID &&
		record.Command.Mode == command.Mode &&
		record.Command.ExpectedPoolVersion == command.ExpectedPoolVersion &&
		record.Command.IdempotencyKey == command.IdempotencyKey &&
		record.Command.RequestHash == command.RequestHash
}

func ownsCatalogPoolReservationV2(
	record catalogPoolCommandRecordV2,
	command researchapp.CatalogCandidatePoolCommandV2,
) bool {
	stored := []byte(record.Command.FencingToken)
	provided := []byte(command.FencingToken)
	return len(stored) > 0 && len(stored) == len(provided) &&
		subtle.ConstantTimeCompare(stored, provided) == 1
}

func newCatalogPoolFencingTokenV2() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random), nil
}

func catalogPoolReservationLostV2() error {
	return fault.New(
		fault.Conflict,
		researchapp.CatalogCandidatePoolReservationLostV2,
		true,
	)
}

func (r *Repository) ReadSearchBudgetSnapshot(ctx context.Context, user, curation, target, key string) (*researchapp.ResearchBudgetSnapshot, error) {
	var raw []byte
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT budget_snapshot FROM phase8_research_pool_commands WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3 AND idempotency_key=$4`, user, curation, target, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var snapshot researchapp.ResearchBudgetSnapshot
	err = json.Unmarshal(raw, &snapshot)
	return &snapshot, err
}
