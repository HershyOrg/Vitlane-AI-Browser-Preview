package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	browserapp "github.com/vitlane/vitlane/server/internal/browserrun/app"
	browserdomain "github.com/vitlane/vitlane/server/internal/browserrun/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct {
	database *sharedpostgres.Database
}

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

func (r *Repository) ResolveCandidate(
	ctx context.Context,
	userID, curationID, candidateID string,
) (browserdomain.CandidateReference, error) {
	var rawURL string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT product_url
		FROM phase8_research_candidates
		WHERE user_id=$1 AND curation_id=$2 AND candidate_id=$3
		  AND visible=true AND locator_kind='PRODUCT_URL'
	`, userID, curationID, candidateID).Scan(&rawURL)
	if errors.Is(err, sql.ErrNoRows) {
		return browserdomain.CandidateReference{}, browserdomain.ErrCandidateNotFound
	}
	if err != nil {
		return browserdomain.CandidateReference{}, fmt.Errorf("resolve browser run candidate: %w", err)
	}
	return browserdomain.NewCandidateReference(userID, curationID, candidateID, rawURL)
}

func (r *Repository) FindCommand(
	ctx context.Context,
	userID, _commandType, idempotencyKey string,
) (browserapp.CommandRecord, bool, error) {
	queryer := r.database.Queryer(ctx)
	// Application commands call this inside Database.WithinTransaction. The
	// owner/key advisory lock closes the concurrent first-request race before
	// either transaction inserts the unique command receipt.
	if _, err := queryer.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"browser-run-command:"+userID+":"+idempotencyKey,
	); err != nil {
		return browserapp.CommandRecord{}, false, fmt.Errorf("lock browser run command: %w", err)
	}
	var value browserapp.CommandRecord
	err := queryer.QueryRowContext(ctx, `
		SELECT user_id::text, run_id::text, command_type, idempotency_key,
		       request_hash, response_version, created_at
		FROM browser_run_commands
		WHERE user_id=$1 AND idempotency_key=$2
	`, userID, idempotencyKey).Scan(
		&value.UserID, &value.RunID, &value.CommandType, &value.IdempotencyKey,
		&value.RequestHash, &value.ResponseVersion, &value.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return browserapp.CommandRecord{}, false, nil
	}
	if err != nil {
		return browserapp.CommandRecord{}, false, fmt.Errorf("find browser run command: %w", err)
	}
	return value, true, nil
}

func (r *Repository) InsertRun(ctx context.Context, run browserdomain.Run) error {
	steps, err := preparationStepsJSON(run.Preparation)
	if err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO browser_runs(
			id,user_id,curation_id,candidate_id,product_url,merchant_origin,
			merchant_host,state,control_owner,version,navigation_approved_at,
			plan_revision,quote_digest,allowed_preparation_steps,
			price_ceiling_minor,price_currency,preparation_approved_at,
			latest_observation_revision,latest_observation_kind,
			latest_observation_origin,latest_page_identity_digest,
			latest_session_state_hint,latest_observed_at,
			handoff_reason,resume_state,resume_after_observation_revision,
			fresh_observation_required,result_outcome,result_evidence_source,
			result_evidence_digest,result_observation_revision,result_verified_at,
			created_at,updated_at,terminal_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,
			$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35
		)
	`, run.ID, run.UserID, run.CurationID, run.CandidateID, run.ProductURL,
		run.MerchantOrigin, run.MerchantHost, run.State, run.ControlOwner, run.Version,
		nullableTime(run.NavigationApprovedAt), preparationPlanRevision(run.Preparation),
		preparationQuoteDigest(run.Preparation), steps, preparationPriceMinor(run.Preparation),
		preparationCurrency(run.Preparation), preparationApprovedAt(run.Preparation),
		observationRevision(run.LatestObservation), observationKind(run.LatestObservation),
		observationOrigin(run.LatestObservation), observationDigest(run.LatestObservation),
		observationSessionStateHint(run.LatestObservation), observationTime(run.LatestObservation),
		nullableString(string(run.HandoffReason)),
		nullableString(string(run.ResumeState)), run.ResumeAfterRevision,
		run.FreshObservationRequired, resultOutcome(run.Result), resultSource(run.Result),
		resultDigest(run.Result), resultObservationRevision(run.Result), resultVerifiedAt(run.Result),
		run.CreatedAt, run.UpdatedAt, nullableTime(run.TerminalAt),
	)
	if err != nil {
		return fmt.Errorf("insert browser run: %w", err)
	}
	return nil
}

func (r *Repository) GetRun(
	ctx context.Context,
	userID, runID string,
	forUpdate bool,
) (browserdomain.Run, error) {
	query := `
		SELECT id::text,user_id::text,curation_id::text,candidate_id,product_url,
		       merchant_origin,merchant_host,state,control_owner,version,
		       navigation_approved_at,plan_revision,quote_digest,
		       allowed_preparation_steps::text,price_ceiling_minor,price_currency,
		       preparation_approved_at,latest_observation_revision,
		       latest_observation_kind,latest_observation_origin,
		       latest_page_identity_digest,latest_session_state_hint,
		       latest_observed_at,handoff_reason,
		       resume_state,resume_after_observation_revision,fresh_observation_required,
		       result_outcome,result_evidence_source,result_evidence_digest,
		       result_observation_revision,result_verified_at,created_at,updated_at,terminal_at
		FROM browser_runs WHERE id=$1 AND user_id=$2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	run, err := scanRun(r.database.Queryer(ctx).QueryRowContext(ctx, query, runID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return browserdomain.Run{}, browserdomain.ErrRunNotFound
	}
	if err != nil {
		return browserdomain.Run{}, fmt.Errorf("get browser run: %w", err)
	}
	return run, nil
}

func (r *Repository) UpdateRun(
	ctx context.Context,
	run browserdomain.Run,
	expectedVersion int64,
) error {
	steps, err := preparationStepsJSON(run.Preparation)
	if err != nil {
		return err
	}
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE browser_runs SET
			state=$1,control_owner=$2,version=$3,navigation_approved_at=$4,
			plan_revision=$5,quote_digest=$6,allowed_preparation_steps=$7,
			price_ceiling_minor=$8,price_currency=$9,preparation_approved_at=$10,
			latest_observation_revision=$11,latest_observation_kind=$12,
			latest_observation_origin=$13,latest_page_identity_digest=$14,
			latest_session_state_hint=$15,latest_observed_at=$16,handoff_reason=$17,
			resume_state=$18,resume_after_observation_revision=$19,
			fresh_observation_required=$20,result_outcome=$21,
			result_evidence_source=$22,result_evidence_digest=$23,
			result_observation_revision=$24,result_verified_at=$25,
			updated_at=$26,terminal_at=$27
		WHERE id=$28 AND user_id=$29 AND version=$30
	`, run.State, run.ControlOwner, run.Version, nullableTime(run.NavigationApprovedAt),
		preparationPlanRevision(run.Preparation), preparationQuoteDigest(run.Preparation), steps,
		preparationPriceMinor(run.Preparation), preparationCurrency(run.Preparation),
		preparationApprovedAt(run.Preparation), observationRevision(run.LatestObservation),
		observationKind(run.LatestObservation), observationOrigin(run.LatestObservation),
		observationDigest(run.LatestObservation), observationSessionStateHint(run.LatestObservation),
		observationTime(run.LatestObservation),
		nullableString(string(run.HandoffReason)), nullableString(string(run.ResumeState)),
		run.ResumeAfterRevision, run.FreshObservationRequired, resultOutcome(run.Result),
		resultSource(run.Result), resultDigest(run.Result), resultObservationRevision(run.Result),
		resultVerifiedAt(run.Result), run.UpdatedAt, nullableTime(run.TerminalAt),
		run.ID, run.UserID, expectedVersion,
	)
	if err != nil {
		return fmt.Errorf("update browser run: %w", err)
	}
	if count, countErr := result.RowsAffected(); countErr != nil {
		return countErr
	} else if count != 1 {
		return browserdomain.ErrVersionConflict
	}
	return nil
}

func (r *Repository) AppendEvent(ctx context.Context, event browserapp.Event) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO browser_run_events(
			id,user_id,run_id,sequence,event_type,payload,created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, event.ID, event.UserID, event.RunID, event.Sequence, event.Type, event.Payload, event.CreatedAt)
	if err != nil {
		return fmt.Errorf("append browser run event: %w", err)
	}
	return nil
}

func (r *Repository) InsertCommand(ctx context.Context, command browserapp.CommandRecord) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO browser_run_commands(
			user_id,run_id,command_type,idempotency_key,request_hash,
			response_version,created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, command.UserID, command.RunID, command.CommandType, command.IdempotencyKey,
		command.RequestHash, command.ResponseVersion, command.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert browser run command: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanRun(row rowScanner) (browserdomain.Run, error) {
	var run browserdomain.Run
	var state, owner string
	var navigationApprovedAt, preparationApprovedAt, latestObservedAt sql.NullTime
	var planRevision, priceMinor, resultObservationRevision sql.NullInt64
	var quoteDigest, stepsJSON, priceCurrency sql.NullString
	var observationKindValue, observationOriginValue, observationDigestValue sql.NullString
	var sessionStateHint sql.NullString
	var handoff, resumeState sql.NullString
	var resultOutcomeValue, resultSourceValue, resultDigestValue sql.NullString
	var resultVerifiedAt, terminalAt sql.NullTime
	var latestRevision int64
	if err := row.Scan(
		&run.ID, &run.UserID, &run.CurationID, &run.CandidateID, &run.ProductURL,
		&run.MerchantOrigin, &run.MerchantHost, &state, &owner, &run.Version,
		&navigationApprovedAt, &planRevision, &quoteDigest, &stepsJSON, &priceMinor,
		&priceCurrency, &preparationApprovedAt, &latestRevision, &observationKindValue,
		&observationOriginValue, &observationDigestValue, &sessionStateHint,
		&latestObservedAt, &handoff,
		&resumeState, &run.ResumeAfterRevision, &run.FreshObservationRequired,
		&resultOutcomeValue, &resultSourceValue, &resultDigestValue,
		&resultObservationRevision, &resultVerifiedAt, &run.CreatedAt, &run.UpdatedAt,
		&terminalAt,
	); err != nil {
		return browserdomain.Run{}, err
	}
	run.State, run.ControlOwner = browserdomain.State(state), browserdomain.ControlOwner(owner)
	if navigationApprovedAt.Valid {
		value := navigationApprovedAt.Time
		run.NavigationApprovedAt = &value
	}
	if planRevision.Valid {
		var steps []browserdomain.PreparationStep
		if !stepsJSON.Valid || json.Unmarshal([]byte(stepsJSON.String), &steps) != nil {
			return browserdomain.Run{}, fmt.Errorf("decode browser run preparation steps")
		}
		run.Preparation = &browserdomain.PreparationApproval{
			PlanRevision: planRevision.Int64, QuoteDigest: quoteDigest.String,
			AllowedPreparationSteps: steps, PriceCeilingMinor: priceMinor.Int64,
			PriceCurrency: priceCurrency.String, ApprovedAt: preparationApprovedAt.Time,
		}
	}
	if latestRevision > 0 {
		run.LatestObservation = &browserdomain.Observation{
			Revision: latestRevision, Kind: browserdomain.ObservationKind(observationKindValue.String),
			Origin: observationOriginValue.String, PageIdentityDigest: observationDigestValue.String,
			SessionStateHint: browserdomain.SessionStateHint(sessionStateHint.String),
			ObservedAt:       latestObservedAt.Time,
		}
	}
	run.HandoffReason, run.ResumeState = browserdomain.HandoffReason(handoff.String), browserdomain.State(resumeState.String)
	if resultOutcomeValue.Valid {
		run.Result = &browserdomain.ResultVerification{
			Outcome:             browserdomain.ResultOutcome(resultOutcomeValue.String),
			EvidenceSource:      browserdomain.ResultEvidenceSource(resultSourceValue.String),
			EvidenceDigest:      resultDigestValue.String,
			ObservationRevision: resultObservationRevision.Int64,
			VerifiedAt:          resultVerifiedAt.Time,
		}
	}
	if terminalAt.Valid {
		value := terminalAt.Time
		run.TerminalAt = &value
	}
	return run, nil
}

func preparationStepsJSON(value *browserdomain.PreparationApproval) (any, error) {
	if value == nil {
		return nil, nil
	}
	payload, err := json.Marshal(value.AllowedPreparationSteps)
	if err != nil {
		return nil, fmt.Errorf("encode browser run preparation steps: %w", err)
	}
	return json.RawMessage(payload), nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func preparationPlanRevision(value *browserdomain.PreparationApproval) any {
	if value == nil {
		return nil
	}
	return value.PlanRevision
}

func preparationQuoteDigest(value *browserdomain.PreparationApproval) any {
	if value == nil {
		return nil
	}
	return value.QuoteDigest
}

func preparationPriceMinor(value *browserdomain.PreparationApproval) any {
	if value == nil {
		return nil
	}
	return value.PriceCeilingMinor
}

func preparationCurrency(value *browserdomain.PreparationApproval) any {
	if value == nil {
		return nil
	}
	return value.PriceCurrency
}

func preparationApprovedAt(value *browserdomain.PreparationApproval) any {
	if value == nil {
		return nil
	}
	return value.ApprovedAt
}

func observationRevision(value *browserdomain.Observation) int64 {
	if value == nil {
		return 0
	}
	return value.Revision
}

func observationKind(value *browserdomain.Observation) any {
	if value == nil {
		return nil
	}
	return value.Kind
}

func observationOrigin(value *browserdomain.Observation) any {
	if value == nil {
		return nil
	}
	return value.Origin
}

func observationDigest(value *browserdomain.Observation) any {
	if value == nil {
		return nil
	}
	return value.PageIdentityDigest
}

func observationSessionStateHint(value *browserdomain.Observation) any {
	if value == nil {
		return nil
	}
	return value.SessionStateHint
}

func observationTime(value *browserdomain.Observation) any {
	if value == nil {
		return nil
	}
	return value.ObservedAt
}

func resultOutcome(value *browserdomain.ResultVerification) any {
	if value == nil {
		return nil
	}
	return value.Outcome
}

func resultSource(value *browserdomain.ResultVerification) any {
	if value == nil {
		return nil
	}
	return value.EvidenceSource
}

func resultDigest(value *browserdomain.ResultVerification) any {
	if value == nil || value.EvidenceDigest == "" {
		return nil
	}
	return value.EvidenceDigest
}

func resultObservationRevision(value *browserdomain.ResultVerification) any {
	if value == nil || value.ObservationRevision == 0 {
		return nil
	}
	return value.ObservationRevision
}

func resultVerifiedAt(value *browserdomain.ResultVerification) any {
	if value == nil {
		return nil
	}
	return value.VerifiedAt
}
