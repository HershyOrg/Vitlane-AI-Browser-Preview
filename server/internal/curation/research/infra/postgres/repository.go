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
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct {
	database                          *sharedpostgres.Database
	catalogLimits                     map[string]researchapp.CatalogLocalLimits
	actorMonthlyCapMicros             int64
	backgroundAmazonDailyCalls        int
	backgroundAmazonForegroundReserve int64
}

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

func nullableString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func (r *Repository) NextRoundNumber(ctx context.Context, sessionID string) (int, error) {
	queryer := r.database.Queryer(ctx)
	if _, err := queryer.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"research-round:"+sessionID,
	); err != nil {
		return 0, fmt.Errorf("lock research round sequence: %w", err)
	}
	var number int
	if err := queryer.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(round_number), 0) + 1
		FROM research_rounds
		WHERE shopping_session_id=$1
	`, sessionID).Scan(&number); err != nil {
		return 0, fmt.Errorf("next research round number: %w", err)
	}
	return number, nil
}

func (r *Repository) CreateRound(
	ctx context.Context,
	round researchdomain.ResearchRound,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO research_rounds(
			id, shopping_session_id, user_id, round_number,
			context_schema, context_version, context_hash, context_snapshot,
			status, result_submission_id, failure_reason_code, failure_retryable,
			created_at, completed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	`, round.ID, round.ShoppingSessionID, round.UserID, round.RoundNumber,
		round.ContextSchema, round.ContextVersion, round.ContextHash,
		round.ContextSnapshot, round.Status, nil,
		nullableString(round.FailureReasonCode), round.FailureRetryable,
		round.CreatedAt, round.CompletedAt)
	if err != nil {
		return fmt.Errorf("insert research round: %w", err)
	}
	return nil
}

func (r *Repository) GetRound(
	ctx context.Context,
	userID, roundID string,
	forUpdate bool,
) (researchdomain.ResearchRound, error) {
	query := `
		SELECT id, shopping_session_id, user_id, round_number,
		       context_schema, context_version, context_hash, context_snapshot,
		       status, result_submission_id, failure_reason_code, failure_retryable,
		       first_discovered_at, first_context_read_at, last_agent_activity_at,
		       created_at, completed_at
		FROM research_rounds
		WHERE id=$1 AND user_id=$2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	return scanRound(r.database.Queryer(ctx).QueryRowContext(ctx, query, roundID, userID))
}

func (r *Repository) GetCurrentRound(
	ctx context.Context,
	userID, sessionID string,
) (researchdomain.ResearchRound, error) {
	return scanRound(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT r.id, r.shopping_session_id, r.user_id, r.round_number,
		       r.context_schema, r.context_version, r.context_hash, r.context_snapshot,
		       r.status, r.result_submission_id, r.failure_reason_code, r.failure_retryable,
		       r.first_discovered_at, r.first_context_read_at, r.last_agent_activity_at,
		       r.created_at, r.completed_at
		FROM shopping_sessions s
		JOIN research_rounds r ON r.id=s.current_research_round_id
		WHERE s.id=$1 AND s.user_id=$2
	`, sessionID, userID))
}

func (r *Repository) UpdateRound(
	ctx context.Context,
	round researchdomain.ResearchRound,
) error {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE research_rounds
		SET status=$1, result_submission_id=$2, failure_reason_code=$3,
		    failure_retryable=$4, completed_at=$5
		WHERE id=$6 AND user_id=$7
	`, round.Status, nil, nullableString(round.FailureReasonCode),
		round.FailureRetryable, round.CompletedAt, round.ID, round.UserID)
	if err != nil {
		return fmt.Errorf("update research round: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return researchdomain.ErrRoundNotFound
	}
	return nil
}

func (r *Repository) GetCandidate(
	ctx context.Context,
	userID, sessionID, candidateID string,
) (researchdomain.Candidate, error) {
	row := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT c.id, c.research_submission_id, c.shopping_session_id,
		       c.product_url, c.merchant_domain, c.category, c.name, c.description,
		       c.image_url, c.price_amount::text, c.price_currency, c.variant_discovery,
		       c.evidence, c.observed_at, c.order_support,
		       c.orderability, c.eligibility,
		       c.candidate_hash_schema, c.candidate_hash, c.order_index, c.created_at
		FROM candidates c
		JOIN shopping_sessions s ON s.id=c.shopping_session_id
		WHERE c.id=$1 AND c.shopping_session_id=$2 AND s.user_id=$3
	`, candidateID, sessionID, userID)
	return scanCandidate(row)
}

func (r *Repository) ListRounds(
	ctx context.Context,
	userID, sessionID string,
) ([]researchdomain.ResearchRound, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, shopping_session_id, user_id, round_number,
		       context_schema, context_version, context_hash, context_snapshot,
		       status, result_submission_id, failure_reason_code, failure_retryable,
		       first_discovered_at, first_context_read_at, last_agent_activity_at,
		       created_at, completed_at
		FROM research_rounds
		WHERE shopping_session_id=$1 AND user_id=$2
		ORDER BY round_number
	`, sessionID, userID)
	if err != nil {
		return nil, fmt.Errorf("list research rounds: %w", err)
	}
	defer rows.Close()
	result := []researchdomain.ResearchRound{}
	for rows.Next() {
		value, err := scanRound(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *Repository) InsertConfiguration(
	ctx context.Context,
	configuration researchdomain.CandidateConfiguration,
) (researchdomain.CandidateConfiguration, error) {
	fields, err := json.Marshal(configuration.Fields)
	if err != nil {
		return researchdomain.CandidateConfiguration{}, err
	}
	selections, err := json.Marshal(configuration.Selections)
	if err != nil {
		return researchdomain.CandidateConfiguration{}, err
	}
	err = r.database.Queryer(ctx).QueryRowContext(ctx, `
			INSERT INTO candidate_configurations(
				id, shopping_session_id, candidate_id, user_id,
				schema_version, fields, selections, confirms_no_options,
				configuration_hash, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (candidate_id, user_id, configuration_hash) DO UPDATE SET
				configuration_hash=EXCLUDED.configuration_hash
			RETURNING id, configuration_sequence, created_at
		`, configuration.ID, configuration.ShoppingSessionID,
		configuration.CandidateID, configuration.UserID,
		configuration.SchemaVersion,
		fields, selections, configuration.ConfirmsNoOptions,
		configuration.ConfigurationHash, configuration.CreatedAt,
	).Scan(
		&configuration.ID, &configuration.ConfigurationSequence,
		&configuration.CreatedAt,
	)
	if err != nil {
		return researchdomain.CandidateConfiguration{},
			fmt.Errorf("insert candidate configuration: %w", err)
	}
	return configuration, nil
}

func (r *Repository) GetLatestConfiguration(
	ctx context.Context,
	userID, sessionID, candidateID string,
) (researchdomain.CandidateConfiguration, error) {
	return scanConfiguration(r.database.Queryer(ctx).QueryRowContext(ctx, `
			SELECT id, configuration_sequence, shopping_session_id, candidate_id,
			       user_id, schema_version, fields, selections,
		       confirms_no_options, configuration_hash, created_at
		FROM candidate_configurations
		WHERE user_id=$1 AND shopping_session_id=$2 AND candidate_id=$3
		ORDER BY configuration_sequence DESC
		LIMIT 1
	`, userID, sessionID, candidateID))
}

func (r *Repository) GetConfiguration(
	ctx context.Context,
	userID, sessionID, candidateID, configurationID string,
) (researchdomain.CandidateConfiguration, error) {
	return scanConfiguration(r.database.Queryer(ctx).QueryRowContext(ctx, `
			SELECT id, configuration_sequence, shopping_session_id, candidate_id,
			       user_id, schema_version, fields, selections,
		       confirms_no_options, configuration_hash, created_at
		FROM candidate_configurations
		WHERE id=$1 AND user_id=$2 AND shopping_session_id=$3 AND candidate_id=$4
	`, configurationID, userID, sessionID, candidateID))
}

func (r *Repository) ListLikedVariantsV2(
	ctx context.Context,
	userID string,
	limit int,
) ([]researchdomain.LikedVariantV2, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT user_id, curation_id, candidate_id, variant_id,
		       product_title, variant_title, COALESCE(product_url,''), merchant,
		       price_minor, currency, target_title, updated_at, price_unknown
		FROM phase8_liked_variants
		WHERE user_id=$1
		ORDER BY updated_at DESC, candidate_id, variant_id
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list Phase 8 liked variants: %w", err)
	}
	defer rows.Close()
	values := []researchdomain.LikedVariantV2{}
	for rows.Next() {
		var value researchdomain.LikedVariantV2
		if err := rows.Scan(
			&value.UserID, &value.CurationID, &value.CandidateID, &value.VariantID,
			&value.ProductTitle, &value.VariantTitle, &value.ProductURL, &value.Merchant,
			&value.PriceMinor, &value.Currency, &value.TargetTitle, &value.UpdatedAt, &value.PriceUnknown,
		); err != nil {
			return nil, fmt.Errorf("scan Phase 8 liked variant: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Phase 8 liked variants: %w", err)
	}
	return values, nil
}

func (r *Repository) CreateFeedback(
	ctx context.Context,
	feedback researchdomain.ResearchFeedback,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
			INSERT INTO research_feedback(
				id, shopping_session_id, previous_round_id, next_round_id, user_id,
				feedback, interaction_snapshot, schema_version, feedback_version,
			feedback_hash, previous_round_status, status, client_request_id,
			request_hash, created_at, cancelled_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
	`, feedback.ID, feedback.ShoppingSessionID, feedback.PreviousRoundID,
		feedback.NextRoundID, feedback.UserID, feedback.Feedback,
		feedback.InteractionSnapshot, feedback.SchemaVersion, feedback.FeedbackVersion,
		feedback.FeedbackHash, feedback.PreviousRoundStatus, feedback.Status,
		feedback.ClientRequestID, feedback.RequestHash, feedback.CreatedAt,
		feedback.CancelledAt)
	if err != nil {
		return fmt.Errorf("insert research feedback: %w", err)
	}
	return nil
}

func (r *Repository) GetFeedbackForRound(
	ctx context.Context,
	userID, nextRoundID string,
	forUpdate bool,
) (researchdomain.ResearchFeedback, error) {
	query := `
			SELECT id, shopping_session_id, previous_round_id, next_round_id, user_id,
			       feedback, interaction_snapshot, schema_version, feedback_version,
		       feedback_hash, previous_round_status, status, client_request_id,
		       request_hash, created_at, cancelled_at
		FROM research_feedback
		WHERE next_round_id=$1 AND user_id=$2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	return scanFeedback(r.database.Queryer(ctx).QueryRowContext(
		ctx, query, nextRoundID, userID,
	))
}

func (r *Repository) FindFeedbackByRequest(
	ctx context.Context,
	userID, clientRequestID string,
) (researchdomain.ResearchFeedback, bool, error) {
	value, err := scanFeedback(r.database.Queryer(ctx).QueryRowContext(ctx, `
			SELECT id, shopping_session_id, previous_round_id, next_round_id, user_id,
			       feedback, interaction_snapshot, schema_version, feedback_version,
		       feedback_hash, previous_round_status, status, client_request_id,
		       request_hash, created_at, cancelled_at
		FROM research_feedback
		WHERE user_id=$1 AND client_request_id=$2
	`, userID, clientRequestID))
	if errors.Is(err, researchdomain.ErrFeedbackNotFound) {
		return researchdomain.ResearchFeedback{}, false, nil
	}
	return value, err == nil, err
}

func (r *Repository) UpdateFeedback(
	ctx context.Context,
	feedback researchdomain.ResearchFeedback,
) error {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE research_feedback
		SET status=$1, cancelled_at=$2
		WHERE id=$3 AND user_id=$4
	`, feedback.Status, feedback.CancelledAt, feedback.ID, feedback.UserID)
	if err != nil {
		return fmt.Errorf("update research feedback: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return researchdomain.ErrFeedbackNotFound
	}
	return nil
}

type scanner interface {
	Scan(...any) error
}

func scanCandidate(row scanner) (researchdomain.Candidate, error) {
	var candidate researchdomain.Candidate
	var amount, currency string
	var variantDiscovery, evidence, orderability, eligibility []byte
	err := row.Scan(
		&candidate.ID, &candidate.ResearchSubmissionID, &candidate.ShoppingSessionID,
		&candidate.ProductURL, &candidate.MerchantDomain, &candidate.Category,
		&candidate.Name, &candidate.Description, &candidate.ImageURL,
		&amount, &currency, &variantDiscovery,
		&evidence, &candidate.ObservedAt, &candidate.OrderSupport,
		&orderability, &eligibility,
		&candidate.CandidateHashSchema, &candidate.CandidateHash,
		&candidate.OrderIndex, &candidate.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return researchdomain.Candidate{}, researchdomain.ErrCandidateInvalid
	}
	if err != nil {
		return researchdomain.Candidate{}, fmt.Errorf("scan candidate: %w", err)
	}
	money, err := shareddomain.NewMoney(amount, currency)
	if err != nil {
		return researchdomain.Candidate{}, err
	}
	candidate.Price = money
	if err := json.Unmarshal(evidence, &candidate.Evidence); err != nil {
		return researchdomain.Candidate{}, err
	}
	if err := decodeCandidateVariantDiscovery(&candidate, variantDiscovery); err != nil {
		return researchdomain.Candidate{}, err
	}
	if err := json.Unmarshal(eligibility, &candidate.Eligibility); err != nil {
		return researchdomain.Candidate{}, err
	}
	if err := decodeCandidateOrderability(&candidate, orderability); err != nil {
		return researchdomain.Candidate{}, err
	}
	return candidate, nil
}

func decodeCandidateVariantDiscovery(
	candidate *researchdomain.Candidate,
	payload []byte,
) error {
	var discovery researchdomain.VariantDiscovery
	if err := json.Unmarshal(payload, &discovery); err != nil {
		return fmt.Errorf("decode candidate variant discovery: %w", err)
	}
	if discovery.SchemaVersion != researchdomain.VariantDiscoverySchemaV1 ||
		discovery.Status == "" ||
		discovery.Fields == nil ||
		discovery.ProviderVariantRefs == nil {
		return fmt.Errorf("%w: persisted variant discovery is incomplete", researchdomain.ErrCandidateInvalid)
	}
	candidate.VariantDiscovery = discovery
	return nil
}

func decodeCandidateOrderability(
	candidate *researchdomain.Candidate,
	payload []byte,
) error {
	var orderability researchdomain.Orderability
	if err := json.Unmarshal(payload, &orderability); err != nil {
		return fmt.Errorf("decode candidate orderability: %w", err)
	}
	if orderability.SchemaVersion != researchdomain.OrderabilitySchemaV1 ||
		orderability.ProviderKind == "" ||
		orderability.ExecutionMode == "" ||
		orderability.ExternalEffect == "" ||
		orderability.LiveOrderability == "" ||
		orderability.SettlementStatus == "" ||
		orderability.Status == "" ||
		orderability.ReasonCodes == nil {
		return fmt.Errorf("%w: persisted orderability is incomplete", researchdomain.ErrCandidateInvalid)
	}
	candidate.Orderability = orderability
	return nil
}

func scanConfiguration(row scanner) (researchdomain.CandidateConfiguration, error) {
	var configuration researchdomain.CandidateConfiguration
	var fields, selections []byte
	err := row.Scan(
		&configuration.ID, &configuration.ConfigurationSequence,
		&configuration.ShoppingSessionID, &configuration.CandidateID,
		&configuration.UserID, &configuration.SchemaVersion, &fields, &selections,
		&configuration.ConfirmsNoOptions, &configuration.ConfigurationHash,
		&configuration.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return researchdomain.CandidateConfiguration{},
			researchdomain.ErrConfigurationNotFound
	}
	if err != nil {
		return researchdomain.CandidateConfiguration{},
			fmt.Errorf("scan candidate configuration: %w", err)
	}
	if err := json.Unmarshal(fields, &configuration.Fields); err != nil {
		return researchdomain.CandidateConfiguration{}, err
	}
	if err := json.Unmarshal(selections, &configuration.Selections); err != nil {
		return researchdomain.CandidateConfiguration{}, err
	}
	if configuration.Fields == nil {
		configuration.Fields = []researchdomain.VariantField{}
	}
	if configuration.Selections == nil {
		configuration.Selections = []researchdomain.VariantSelection{}
	}
	return configuration, nil
}

func scanFeedback(row scanner) (researchdomain.ResearchFeedback, error) {
	var feedback researchdomain.ResearchFeedback
	err := row.Scan(
		&feedback.ID, &feedback.ShoppingSessionID, &feedback.PreviousRoundID,
		&feedback.NextRoundID, &feedback.UserID, &feedback.Feedback,
		&feedback.InteractionSnapshot, &feedback.SchemaVersion,
		&feedback.FeedbackVersion, &feedback.FeedbackHash,
		&feedback.PreviousRoundStatus, &feedback.Status,
		&feedback.ClientRequestID, &feedback.RequestHash,
		&feedback.CreatedAt, &feedback.CancelledAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return researchdomain.ResearchFeedback{}, researchdomain.ErrFeedbackNotFound
	}
	if err != nil {
		return researchdomain.ResearchFeedback{}, fmt.Errorf("scan research feedback: %w", err)
	}
	return feedback, nil
}

func scanRound(row scanner) (researchdomain.ResearchRound, error) {
	var round researchdomain.ResearchRound
	var retiredSubmissionID sql.NullString
	var failureReasonCode sql.NullString
	err := row.Scan(
		&round.ID, &round.ShoppingSessionID, &round.UserID, &round.RoundNumber,
		&round.ContextSchema, &round.ContextVersion, &round.ContextHash,
		&round.ContextSnapshot, &round.Status, &retiredSubmissionID,
		&failureReasonCode, &round.FailureRetryable,
		&round.FirstDiscoveredAt,
		&round.FirstContextReadAt, &round.LastAgentActivityAt,
		&round.CreatedAt, &round.CompletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return researchdomain.ResearchRound{}, researchdomain.ErrRoundNotFound
	}
	if err != nil {
		return researchdomain.ResearchRound{}, fmt.Errorf("scan research round: %w", err)
	}
	if failureReasonCode.Valid {
		round.FailureReasonCode = failureReasonCode.String
	}
	return round, nil
}
