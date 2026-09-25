package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	planningapp "github.com/vitlane/vitlane/server/internal/curation/planning/app"
	planningdomain "github.com/vitlane/vitlane/server/internal/curation/planning/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct {
	database *sharedpostgres.Database
	plans    planningapp.Repository
}

func NewRepository(
	database *sharedpostgres.Database,
	plans planningapp.Repository,
) *Repository {
	return &Repository{database: database, plans: plans}
}

func (r *Repository) CreateIdempotent(
	ctx context.Context,
	plan curationdomain.PlanSnapshot,
	targets []curationdomain.PlanTarget,
	task *curationdomain.PlanningTask,
	idempotencyKey string,
	requestHash []byte,
) (curationapp.PlanRecord, bool, error) {
	queryer := r.database.Queryer(ctx)
	storedPlan, duplicate, err := r.plans.CreateIdempotent(
		ctx, toShoppingPlan(plan), idempotencyKey, requestHash,
	)
	if err != nil {
		if errors.Is(err, planningdomain.ErrIdempotencyKeyReused) {
			return curationapp.PlanRecord{}, false, curationdomain.ErrIdempotencyKeyReused
		}
		return curationapp.PlanRecord{}, false, err
	}
	if duplicate {
		record, getErr := r.Get(
			ctx, string(storedPlan.UserID), string(storedPlan.ID), false,
		)
		return record, true, getErr
	}
	planSnapshot := toPlanSnapshot(storedPlan)
	planSnapshot.BudgetRequest = plan.BudgetRequest
	curation, err := scanCuration(queryer.QueryRowContext(ctx, `
		INSERT INTO curations(
			id, shopping_plan_id, user_id, phase,
			version, created_at, updated_at, research_country
		) VALUES (gen_random_uuid(),$1,$2,'PLANNING',1,$3,$3,$4)
		RETURNING
			id, shopping_plan_id, user_id, phase, version,
			created_at, updated_at, archived_at, archived_by_user_id
	`, planSnapshot.ID, planSnapshot.UserID, planSnapshot.CreatedAt, planSnapshot.LocationContext.Country))
	if err != nil {
		return curationapp.PlanRecord{}, false, fmt.Errorf("insert curation: %w", err)
	}
	var initialRequest any
	if plan.BudgetRequest != nil {
		raw, e := json.Marshal(plan.BudgetRequest)
		if e != nil {
			return curationapp.PlanRecord{}, false, e
		}
		initialRequest = string(raw)
	}
	currency := string(plan.TotalBudget.Currency)
	if currency != "USD" {
		currency = "KRW"
	}
	if _, e := queryer.ExecContext(ctx, `INSERT INTO curation_budgets(curation_id,currency,initial_request,initial_materialized) VALUES($1,$2,$3::jsonb,$4)`, curation.ID, currency, initialRequest, plan.BudgetRequest == nil); e != nil {
		return curationapp.PlanRecord{}, false, e
	}
	for index := range targets {
		targets[index].CurationID = curation.ID
		targets[index].UserID = planSnapshot.UserID
		target := targets[index]
		if err := insertTarget(ctx, queryer, target); err != nil {
			return curationapp.PlanRecord{}, false, err
		}
	}
	if task != nil {
		if _, err := queryer.ExecContext(ctx, `
			INSERT INTO planning_tasks(
				id, plan_id, user_id, context_version, context_hash, status,
				expires_at, completed_at, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		`, task.ID, task.PlanID, task.UserID, task.ContextVersion, task.ContextHash,
			task.Status, task.ExpiresAt, task.CompletedAt, task.CreatedAt, task.UpdatedAt,
		); err != nil {
			return curationapp.PlanRecord{}, false, fmt.Errorf("insert planning task: %w", err)
		}
		if _, err := queryer.ExecContext(ctx, `
			INSERT INTO curation_runs(
				id, curation_id, user_id, kind, instruction,
				planning_task_id, status, idempotency_key, request_hash,
				created_at, updated_at
			) VALUES ($1,$2,$3,'INITIAL',$4,$1,'REQUESTED',$5,$6,$7,$7)
			ON CONFLICT (user_id, idempotency_key) DO NOTHING
		`, task.ID, curation.ID, planSnapshot.UserID, planSnapshot.OriginalIntent,
			idempotencyKey, requestHash, planSnapshot.CreatedAt); err != nil {
			return curationapp.PlanRecord{}, false, fmt.Errorf(
				"insert initial curation run: %w", err,
			)
		}
	}
	if err := r.plans.CompleteCreation(ctx, storedPlan, idempotencyKey); err != nil {
		if errors.Is(err, planningdomain.ErrIdempotencyKeyReused) {
			return curationapp.PlanRecord{}, false, curationdomain.ErrIdempotencyKeyReused
		}
		return curationapp.PlanRecord{}, false, err
	}
	return curationapp.PlanRecord{
		Plan: planSnapshot, Curation: curation, Targets: targets,
	}, false, nil
}

type execQueryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
}

func insertTarget(
	ctx context.Context,
	queryer execQueryer,
	target curationdomain.PlanTarget,
) error {
	minAmount, maxAmount, priceCurrency := priceValues(target)
	_, err := queryer.ExecContext(ctx, `
		INSERT INTO plan_targets(
			id, curation_id, user_id, plan_id,
			title, normalized_intent, category,
			allocated_amount, allocated_currency, country, city,
			allowed_items, blocked_items, min_price_amount, max_price_amount, price_currency,
			reference_url, url_mode, order_index, confirmed_at, target_hash,
			target_hash_schema, version, created_at, updated_at,
			created_by_curation_run_id, removed_at, removed_by_user_id, product_vertical
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,
			$21,$22,$23,$24,$25,$26,$27,$28,$29
		)
	`, target.ID, target.CurationID, target.UserID, target.PlanID,
		target.Title, target.NormalizedIntent, target.Category,
		target.AllocatedBudget.Amount, target.AllocatedBudget.Currency,
		target.ResearchScope.Country, target.ResearchScope.City,
		target.ResearchScope.AllowedItems, target.ResearchScope.BlockedItems,
		minAmount, maxAmount, priceCurrency, target.ResearchScope.ReferenceURL,
		target.ResearchScope.URLMode, target.OrderIndex, target.ConfirmedAt,
		target.TargetHash, target.TargetHashSchema, target.Version,
		target.CreatedAt, target.UpdatedAt, target.CreatedByCurationRunID,
		target.RemovedAt, target.RemovedByUserID, target.ProductVertical,
	)
	if err != nil {
		return fmt.Errorf("insert plan target: %w", err)
	}
	return nil
}

func (r *Repository) Get(
	ctx context.Context,
	userID, planID string,
	forUpdate bool,
) (curationapp.PlanRecord, error) {
	plan, err := r.plans.Get(ctx, userID, planID, forUpdate)
	if err != nil {
		if errors.Is(err, planningdomain.ErrPlanNotFound) {
			return curationapp.PlanRecord{}, curationdomain.ErrPlanNotFound
		}
		return curationapp.PlanRecord{}, err
	}
	curation, err := r.getCurationByPlan(ctx, userID, planID, forUpdate)
	if err != nil {
		return curationapp.PlanRecord{}, err
	}
	targetLock := ""
	if forUpdate {
		targetLock = " FOR UPDATE"
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT
			id, curation_id, user_id, plan_id,
			title, normalized_intent, category,
			allocated_amount::text, allocated_currency, country, city,
			array_to_json(allowed_items)::text, array_to_json(blocked_items)::text,
			min_price_amount::text, max_price_amount::text, price_currency,
			reference_url, url_mode, order_index, confirmed_at, target_hash,
			target_hash_schema, version, created_at, updated_at,
			created_by_curation_run_id, removed_at, removed_by_user_id, product_vertical
		FROM plan_targets
		WHERE plan_id=$1 AND removed_at IS NULL
		ORDER BY order_index`+targetLock,
		planID,
	)
	if err != nil {
		return curationapp.PlanRecord{}, fmt.Errorf("list plan targets: %w", err)
	}
	defer rows.Close()
	targets := []curationdomain.PlanTarget{}
	for rows.Next() {
		target, scanErr := scanTarget(rows)
		if scanErr != nil {
			return curationapp.PlanRecord{}, scanErr
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return curationapp.PlanRecord{}, fmt.Errorf("iterate plan targets: %w", err)
	}
	view := toPlanSnapshot(plan)
	view.BudgetRequest, err = r.initialBudgetRequest(ctx, string(curation.ID))
	if err != nil {
		return curationapp.PlanRecord{}, err
	}
	return curationapp.PlanRecord{
		Plan: view, Curation: curation, Targets: targets,
	}, nil
}

type rowScanner interface {
	Scan(...any) error
}

func toPlanSnapshot(plan planningdomain.ShoppingPlan) curationdomain.PlanSnapshot {
	return curationdomain.PlanSnapshot{
		ID:              curationdomain.ShoppingPlanID(plan.ID),
		UserID:          curationdomain.UserID(plan.UserID),
		OriginalIntent:  plan.OriginalIntent,
		PlanningMode:    curationdomain.PlanningMode(plan.PlanningMode),
		ExecutionMode:   curationdomain.ExecutionMode(plan.ExecutionMode),
		TotalBudget:     plan.TotalBudget,
		LocationContext: plan.LocationContext,
		ResearchScope: curationdomain.ResearchScope{
			Category:     plan.ResearchScope.Category,
			Country:      plan.ResearchScope.Country,
			City:         plan.ResearchScope.City,
			AllowedItems: plan.ResearchScope.AllowedItems,
			BlockedItems: plan.ResearchScope.BlockedItems,
			MinPrice:     plan.ResearchScope.MinPrice,
			MaxPrice:     plan.ResearchScope.MaxPrice,
			ReferenceURL: plan.ResearchScope.ReferenceURL,
			URLMode:      curationdomain.URLMode(plan.ResearchScope.URLMode),
		},
		AgentMode: curationdomain.AgentMode(plan.AgentMode),
		ModelKey:  plan.ModelKey,
		CreatedAt: plan.CreatedAt,
	}
}

func toShoppingPlan(plan curationdomain.PlanSnapshot) planningdomain.ShoppingPlan {
	return planningdomain.ShoppingPlan{
		ID:              planningdomain.ShoppingPlanID(plan.ID),
		UserID:          planningdomain.UserID(plan.UserID),
		OriginalIntent:  plan.OriginalIntent,
		PlanningMode:    planningdomain.PlanningMode(plan.PlanningMode),
		ExecutionMode:   planningdomain.ExecutionMode(plan.ExecutionMode),
		TotalBudget:     plan.TotalBudget,
		LocationContext: plan.LocationContext,
		ResearchScope: planningdomain.ResearchScope{
			Category:     plan.ResearchScope.Category,
			Country:      plan.ResearchScope.Country,
			City:         plan.ResearchScope.City,
			AllowedItems: plan.ResearchScope.AllowedItems,
			BlockedItems: plan.ResearchScope.BlockedItems,
			MinPrice:     plan.ResearchScope.MinPrice,
			MaxPrice:     plan.ResearchScope.MaxPrice,
			ReferenceURL: plan.ResearchScope.ReferenceURL,
			URLMode:      planningdomain.URLMode(plan.ResearchScope.URLMode),
		},
		AgentMode: planningdomain.AgentMode(plan.AgentMode),
		ModelKey:  plan.ModelKey,
		CreatedAt: plan.CreatedAt,
	}
}

func (r *Repository) getCurationByPlan(
	ctx context.Context,
	userID, planID string,
	forUpdate bool,
) (curationdomain.Curation, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	return scanCuration(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT
			id, shopping_plan_id, user_id, phase, version,
			created_at, updated_at, archived_at, archived_by_user_id
		FROM curations
		WHERE shopping_plan_id=$1 AND user_id=$2`+lock,
		planID, userID,
	))
}

func (r *Repository) GetCuration(
	ctx context.Context,
	userID, curationID string,
	forUpdate bool,
) (curationdomain.Curation, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	return scanCuration(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT
			id, shopping_plan_id, user_id, phase, version,
			created_at, updated_at, archived_at, archived_by_user_id
		FROM curations
		WHERE id=$1 AND user_id=$2`+lock,
		curationID, userID,
	))
}

func (r *Repository) SaveCuration(
	ctx context.Context,
	previousVersion int64,
	curation curationdomain.Curation,
) error {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE curations
		SET phase=$1, version=$2, updated_at=$3,
		    archived_at=$4, archived_by_user_id=$5
		WHERE id=$6 AND user_id=$7 AND version=$8
	`, curation.Phase, curation.Version, curation.UpdatedAt,
		curation.ArchivedAt, curation.ArchivedBy,
		curation.ID, curation.UserID, previousVersion,
	)
	if err != nil {
		return fmt.Errorf("save curation: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return curationdomain.ErrVersionConflict
	}
	return nil
}

func scanCuration(row rowScanner) (curationdomain.Curation, error) {
	var curation curationdomain.Curation
	var archivedAt sql.NullTime
	var archivedBy sql.NullString
	err := row.Scan(
		&curation.ID, &curation.ShoppingPlanID, &curation.UserID,
		&curation.Phase, &curation.Version, &curation.CreatedAt,
		&curation.UpdatedAt, &archivedAt, &archivedBy,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return curationdomain.Curation{}, curationdomain.ErrCurationNotFound
	}
	if err != nil {
		return curationdomain.Curation{}, fmt.Errorf("scan curation: %w", err)
	}
	if archivedAt.Valid {
		curation.ArchivedAt = &archivedAt.Time
	}
	if archivedBy.Valid {
		value := curationdomain.UserID(archivedBy.String)
		curation.ArchivedBy = &value
	}
	if err := curation.Validate(); err != nil {
		return curationdomain.Curation{}, err
	}
	return curation, nil
}

func scanTarget(row rowScanner) (curationdomain.PlanTarget, error) {
	var target curationdomain.PlanTarget
	var allocatedAmount, allocatedCurrency, country string
	var allowedJSON, blockedJSON string
	var minAmount, maxAmount, priceCurrency sql.NullString
	var createdByRunID, removedByUserID sql.NullString
	var removedAt sql.NullTime
	err := row.Scan(
		&target.ID, &target.CurationID, &target.UserID, &target.PlanID,
		&target.Title, &target.NormalizedIntent,
		&target.Category, &allocatedAmount, &allocatedCurrency, &country,
		&target.ResearchScope.City, &allowedJSON, &blockedJSON,
		&minAmount, &maxAmount, &priceCurrency,
		&target.ResearchScope.ReferenceURL, &target.ResearchScope.URLMode,
		&target.OrderIndex, &target.ConfirmedAt, &target.TargetHash,
		&target.TargetHashSchema, &target.Version, &target.CreatedAt, &target.UpdatedAt,
		&createdByRunID, &removedAt, &removedByUserID, &target.ProductVertical,
	)
	if err != nil {
		return curationdomain.PlanTarget{}, fmt.Errorf("scan plan target: %w", err)
	}
	target.AllocatedBudget, err = shareddomain.NewMoney(allocatedAmount, allocatedCurrency)
	if err != nil {
		return curationdomain.PlanTarget{}, err
	}
	target.ResearchScope.Country, err = shareddomain.NewCountryCode(country)
	if err != nil {
		return curationdomain.PlanTarget{}, err
	}
	if err := json.Unmarshal([]byte(allowedJSON), &target.ResearchScope.AllowedItems); err != nil {
		return curationdomain.PlanTarget{}, fmt.Errorf("scan allowed items: %w", err)
	}
	if err := json.Unmarshal([]byte(blockedJSON), &target.ResearchScope.BlockedItems); err != nil {
		return curationdomain.PlanTarget{}, fmt.Errorf("scan blocked items: %w", err)
	}
	if minAmount.Valid {
		value, moneyErr := shareddomain.NewMoney(minAmount.String, priceCurrency.String)
		if moneyErr != nil {
			return curationdomain.PlanTarget{}, moneyErr
		}
		target.ResearchScope.MinPrice = &value
	}
	if maxAmount.Valid {
		value, moneyErr := shareddomain.NewMoney(maxAmount.String, priceCurrency.String)
		if moneyErr != nil {
			return curationdomain.PlanTarget{}, moneyErr
		}
		target.ResearchScope.MaxPrice = &value
	}
	target.ResearchScope.Category = target.Category
	if createdByRunID.Valid {
		value := curationdomain.CurationRunID(createdByRunID.String)
		target.CreatedByCurationRunID = &value
	}
	if removedAt.Valid {
		target.RemovedAt = &removedAt.Time
	}
	if removedByUserID.Valid {
		value := curationdomain.UserID(removedByUserID.String)
		target.RemovedByUserID = &value
	}
	return target, nil
}

func (r *Repository) InsertTargets(ctx context.Context, targets []curationdomain.PlanTarget) error {
	return r.InsertBudgetedTargets(ctx, targets, nil, nil)
}

func (r *Repository) InsertBudgetedTargets(ctx context.Context, targets []curationdomain.PlanTarget, resolved *curationdomain.InitialBudgetRequest, quantities []int) error {
	queryer := r.database.Queryer(ctx)
	for _, target := range targets {
		if target.CurationID == "" || target.UserID == "" ||
			target.ConfirmedAt == nil || target.TargetHash == "" {
			return curationdomain.ErrTargetIncomplete
		}
		if err := insertTarget(ctx, queryer, target); err != nil {
			return err
		}
	}
	return r.addTargetBudgets(ctx, targets, resolved, quantities)
}

func (r *Repository) GetPlanningTask(
	ctx context.Context,
	userID, planID string,
	forUpdate bool,
) (curationdomain.PlanningTask, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	return scanPlanningTask(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, plan_id, user_id, context_version, context_hash, status,
		       expires_at, completed_at,
		       first_discovered_at, first_context_read_at, last_agent_activity_at,
		       created_at, updated_at
		FROM planning_tasks
		WHERE user_id=$1 AND plan_id=$2
		ORDER BY created_at DESC
		LIMIT 1`+lock,
		userID, planID,
	))
}

func (r *Repository) FindIntelligenceProposal(
	ctx context.Context,
	taskID, jobID, clientProposalID string,
) (curationdomain.PlanningProposal, bool, error) {
	proposal, err := scanProposal(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, task_id, plan_id, user_id,
		       client_proposal_id, context_version, context_hash, schema_version,
		       proposal_hash, payload, validation_status,
		       array_to_json(validation_reason_codes)::text, submitted_at
		FROM planning_proposals
		WHERE task_id=$1
		  AND intelligence_job_id=$2
		  AND client_proposal_id=$3
	`, taskID, jobID, clientProposalID))
	if errors.Is(err, curationdomain.ErrProposalInvalid) {
		return curationdomain.PlanningProposal{}, false, nil
	}
	return proposal, err == nil, err
}

func (r *Repository) GetPlanningTaskByID(
	ctx context.Context,
	userID, taskID string,
	forUpdate bool,
) (curationdomain.PlanningTask, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	return scanPlanningTask(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, plan_id, user_id, context_version, context_hash, status,
		       expires_at, completed_at,
		       first_discovered_at, first_context_read_at, last_agent_activity_at,
		       created_at, updated_at
		FROM planning_tasks
		WHERE id=$1 AND user_id=$2`+lock,
		taskID, userID,
	))
}

func (r *Repository) ListPlanningTasks(
	ctx context.Context,
	userID, planID string,
	limit int,
) ([]curationdomain.PlanningTask, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, plan_id, user_id, context_version, context_hash, status,
		       expires_at, completed_at,
		       first_discovered_at, first_context_read_at, last_agent_activity_at,
		       created_at, updated_at
		FROM planning_tasks
		WHERE user_id=$1 AND plan_id=$2 AND status='REQUESTED' AND expires_at>now()
		ORDER BY created_at ASC
		LIMIT $3
	`, userID, planID, limit)
	if err != nil {
		return nil, fmt.Errorf("list planning tasks: %w", err)
	}
	defer rows.Close()
	tasks := []curationdomain.PlanningTask{}
	for rows.Next() {
		task, scanErr := scanPlanningTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate planning tasks: %w", err)
	}
	return tasks, nil
}

func scanPlanningTask(row rowScanner) (curationdomain.PlanningTask, error) {
	var task curationdomain.PlanningTask
	err := row.Scan(
		&task.ID, &task.PlanID, &task.UserID, &task.ContextVersion,
		&task.ContextHash, &task.Status, &task.ExpiresAt, &task.CompletedAt,
		&task.FirstDiscoveredAt,
		&task.FirstContextReadAt, &task.LastAgentActivityAt,
		&task.CreatedAt, &task.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return curationdomain.PlanningTask{}, curationdomain.ErrPlanningTaskNotFound
	}
	if err != nil {
		return curationdomain.PlanningTask{}, fmt.Errorf("scan planning task: %w", err)
	}
	return task, nil
}

func (r *Repository) UpdatePlanningTask(
	ctx context.Context,
	task curationdomain.PlanningTask,
) error {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE planning_tasks
		SET status=$1, completed_at=$2, updated_at=$3
		WHERE id=$4 AND plan_id=$5
	`, task.Status, task.CompletedAt, task.UpdatedAt, task.ID, task.PlanID)
	if err != nil {
		return fmt.Errorf("update planning task: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return curationdomain.ErrPlanningTaskNotFound
	}
	return nil
}

func (r *Repository) GetPlanningProposalResult(
	ctx context.Context,
	userID, taskID, proposalID string,
) (curationdomain.PlanningProposal, error) {
	return scanProposal(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, task_id, plan_id, user_id,
		       client_proposal_id, context_version, context_hash, schema_version,
		       proposal_hash, payload, validation_status,
		       array_to_json(validation_reason_codes)::text, submitted_at
		FROM planning_proposals
		WHERE id=$1 AND task_id=$2 AND user_id=$3
	`, proposalID, taskID, userID))
}

func scanProposal(row rowScanner) (curationdomain.PlanningProposal, error) {
	var proposal curationdomain.PlanningProposal
	var payload []byte
	var reasonCodesJSON string
	err := row.Scan(
		&proposal.ID, &proposal.TaskID, &proposal.PlanID, &proposal.UserID,
		&proposal.ClientProposalID,
		&proposal.ContextVersion, &proposal.ContextHash, &proposal.SchemaVersion,
		&proposal.ProposalHash, &payload, &proposal.ValidationStatus,
		&reasonCodesJSON, &proposal.SubmittedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return curationdomain.PlanningProposal{}, curationdomain.ErrProposalInvalid
	}
	if err != nil {
		return curationdomain.PlanningProposal{}, fmt.Errorf("scan planning proposal: %w", err)
	}
	proposal.Payload = json.RawMessage(payload)
	if err := json.Unmarshal([]byte(reasonCodesJSON), &proposal.ValidationReasonCodes); err != nil {
		return curationdomain.PlanningProposal{}, fmt.Errorf(
			"decode planning proposal reason codes: %w",
			err,
		)
	}
	return proposal, nil
}

func (r *Repository) InsertProposal(
	ctx context.Context,
	proposal curationdomain.PlanningProposal,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO planning_proposals(
			id, task_id, plan_id, user_id, intelligence_job_id,
			client_proposal_id, context_version, context_hash, schema_version,
			proposal_hash, payload, validation_status,
			validation_reason_codes, submitted_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	`, proposal.ID, proposal.TaskID, proposal.PlanID, proposal.UserID,
		nullablePlanningUUID(proposal.IntelligenceJobID),
		proposal.ClientProposalID,
		proposal.ContextVersion, proposal.ContextHash, proposal.SchemaVersion,
		proposal.ProposalHash, proposal.Payload, proposal.ValidationStatus,
		proposal.ValidationReasonCodes, proposal.SubmittedAt)
	if err != nil {
		return fmt.Errorf("insert planning proposal: %w", err)
	}
	return nil
}

func nullablePlanningUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func priceValues(target curationdomain.PlanTarget) (any, any, any) {
	var minAmount, maxAmount, currency any
	if target.ResearchScope.MinPrice != nil {
		minAmount = target.ResearchScope.MinPrice.Amount
		currency = target.ResearchScope.MinPrice.Currency
	}
	if target.ResearchScope.MaxPrice != nil {
		maxAmount = target.ResearchScope.MaxPrice.Amount
		currency = target.ResearchScope.MaxPrice.Currency
	}
	return minAmount, maxAmount, currency
}

func (r *Repository) CreateExpansion(
	ctx context.Context,
	run curationdomain.CurationRun,
	task curationdomain.PlanningTask,
) (curationdomain.CurationRun, curationdomain.PlanningTask, bool, error) {
	queryer := r.database.Queryer(ctx)
	result, err := queryer.ExecContext(ctx, `
		INSERT INTO curation_runs(
			id, curation_id, user_id, kind, instruction,
			planning_task_id, status, idempotency_key, request_hash,
			created_at, updated_at, completed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
	`, run.ID, run.CurationID, run.UserID, run.Kind, run.Instruction,
		run.PlanningTaskID, run.Status, run.IdempotencyKey, run.RequestHash,
		run.CreatedAt, run.UpdatedAt, run.CompletedAt)
	if err != nil {
		return curationdomain.CurationRun{}, curationdomain.PlanningTask{}, false,
			fmt.Errorf("insert curation expansion run: %w", err)
	}
	inserted, _ := result.RowsAffected()
	if inserted == 0 {
		existing, getErr := r.GetCurationRunByIdempotency(
			ctx, string(run.UserID), run.IdempotencyKey, false,
		)
		if getErr != nil {
			return curationdomain.CurationRun{}, curationdomain.PlanningTask{}, false,
				getErr
		}
		if !bytes.Equal(existing.RequestHash, run.RequestHash) {
			return curationdomain.CurationRun{}, curationdomain.PlanningTask{}, false,
				curationdomain.ErrIdempotencyKeyReused
		}
		existingTask, taskErr := r.getPlanningTaskByID(
			ctx, string(existing.UserID), string(existing.PlanID),
			string(existing.PlanningTaskID), false,
		)
		return existing, existingTask, true, taskErr
	}
	if _, err := queryer.ExecContext(ctx, `
		INSERT INTO planning_tasks(
			id, plan_id, user_id, context_version, context_hash, status,
			expires_at, completed_at, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, task.ID, task.PlanID, task.UserID, task.ContextVersion, task.ContextHash,
		task.Status, task.ExpiresAt, task.CompletedAt, task.CreatedAt, task.UpdatedAt,
	); err != nil {
		return curationdomain.CurationRun{}, curationdomain.PlanningTask{}, false,
			fmt.Errorf("insert expansion planning task: %w", err)
	}
	return run, task, false, nil
}

func (r *Repository) GetCurationRun(
	ctx context.Context,
	userID, planID, runID string,
	forUpdate bool,
) (curationdomain.CurationRun, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE OF run"
	}
	run, err := scanCurationRun(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT
			run.id, run.curation_id, curation.shopping_plan_id, run.user_id,
			run.kind, run.instruction, run.planning_task_id, run.status,
			run.idempotency_key::text, run.request_hash,
			run.created_at, run.updated_at, run.completed_at
		FROM curation_runs AS run
		JOIN curations AS curation
		  ON curation.id=run.curation_id
		 AND curation.user_id=run.user_id
		WHERE run.id=$1
		  AND curation.shopping_plan_id=$2
		  AND run.user_id=$3`+lock,
		runID, planID, userID,
	))
	if err != nil {
		return curationdomain.CurationRun{}, err
	}
	return run, nil
}

func (r *Repository) FindOpenExpansion(
	ctx context.Context,
	userID, planID string,
) (curationdomain.CurationRun, bool, error) {
	var runID string
	var taskStatus curationdomain.PlanningTaskStatus
	var taskExpired bool
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT run.id::text, task.status, task.expires_at <= now()
		FROM curation_runs run
		JOIN planning_tasks task ON task.id=run.planning_task_id
		JOIN curations curation
		  ON curation.id=run.curation_id
		 AND curation.user_id=run.user_id
		WHERE curation.shopping_plan_id=$1 AND run.user_id=$2
		  AND run.kind='EXPANSION'
		  AND run.status IN (
		      'REQUESTED', 'MATERIALIZING'
		  )
		ORDER BY run.created_at DESC
		LIMIT 1
		FOR UPDATE OF run, task
	`, planID, userID).Scan(&runID, &taskStatus, &taskExpired)
	if errors.Is(err, sql.ErrNoRows) {
		return curationdomain.CurationRun{}, false, nil
	}
	if err != nil {
		return curationdomain.CurationRun{}, false,
			fmt.Errorf("find open curation expansion: %w", err)
	}
	run, err := r.GetCurationRun(ctx, userID, planID, runID, true)
	if err != nil {
		return curationdomain.CurationRun{}, false, err
	}
	if (run.Status == curationdomain.CurationRunRequested ||
		run.Status == curationdomain.CurationRunMaterializing) &&
		(taskStatus == curationdomain.PlanningTaskStatusCancelled ||
			(taskStatus == curationdomain.PlanningTaskStatusRequested && taskExpired)) {
		if taskStatus == curationdomain.PlanningTaskStatusRequested {
			if _, err := r.database.Queryer(ctx).ExecContext(ctx, `
				UPDATE planning_tasks
				SET status='CANCELLED', updated_at=now(),
				    completed_at=COALESCE(completed_at, now())
				WHERE id=$1 AND status='REQUESTED' AND expires_at<=now()
			`, run.PlanningTaskID); err != nil {
				return curationdomain.CurationRun{}, false,
					fmt.Errorf("cancel expired expansion planning task: %w", err)
			}
		}
		if _, err := r.database.Queryer(ctx).ExecContext(ctx, `
			UPDATE curation_runs
			SET status='CANCELLED', updated_at=now(),
			    completed_at=COALESCE(completed_at, now())
			WHERE id=$1
		`, run.ID); err != nil {
			return curationdomain.CurationRun{}, false,
				fmt.Errorf("cancel abandoned curation expansion: %w", err)
		}
		return curationdomain.CurationRun{}, false, nil
	}
	if taskStatus == curationdomain.PlanningTaskStatusCompleted &&
		run.Status != curationdomain.CurationRunCompleted {
		return curationdomain.CurationRun{}, false, curationdomain.ErrCurationRunClosed
	}
	if run.Status == curationdomain.CurationRunCompleted ||
		run.Status == curationdomain.CurationRunCancelled ||
		run.Status == curationdomain.CurationRunFailed {
		return curationdomain.CurationRun{}, false, nil
	}
	return run, true, nil
}

func (r *Repository) GetCurationRunByTask(
	ctx context.Context,
	userID, planID, taskID string,
	forUpdate bool,
) (curationdomain.CurationRun, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE OF run"
	}
	return scanCurationRun(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT
			run.id, run.curation_id, curation.shopping_plan_id, run.user_id,
			run.kind, run.instruction, run.planning_task_id, run.status,
			run.idempotency_key::text, run.request_hash,
			run.created_at, run.updated_at, run.completed_at
		FROM curation_runs AS run
		JOIN curations AS curation
		  ON curation.id=run.curation_id
		 AND curation.user_id=run.user_id
		WHERE run.planning_task_id=$1
		  AND curation.shopping_plan_id=$2
		  AND run.user_id=$3`+lock,
		taskID, planID, userID,
	))
}

func (r *Repository) GetCurationRunByIdempotency(
	ctx context.Context,
	userID, idempotencyKey string,
	forUpdate bool,
) (curationdomain.CurationRun, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE OF run"
	}
	return scanCurationRun(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT
			run.id, run.curation_id, curation.shopping_plan_id, run.user_id,
			run.kind, run.instruction, run.planning_task_id, run.status,
			run.idempotency_key::text, run.request_hash,
			run.created_at, run.updated_at, run.completed_at
		FROM curation_runs AS run
		JOIN curations AS curation
		  ON curation.id=run.curation_id
		 AND curation.user_id=run.user_id
		WHERE run.user_id=$1 AND run.idempotency_key=$2`+lock,
		userID, idempotencyKey,
	))
}

func (r *Repository) GetExpansionByIdempotency(
	ctx context.Context,
	userID, idempotencyKey string,
	forUpdate bool,
) (curationdomain.CurationRun, curationdomain.PlanningTask, error) {
	run, err := r.GetCurationRunByIdempotency(
		ctx, userID, idempotencyKey, forUpdate,
	)
	if err != nil {
		return curationdomain.CurationRun{}, curationdomain.PlanningTask{}, err
	}
	task, err := r.getPlanningTaskByID(
		ctx, userID, string(run.PlanID), string(run.PlanningTaskID), forUpdate,
	)
	if err != nil {
		return curationdomain.CurationRun{}, curationdomain.PlanningTask{}, err
	}
	return run, task, nil
}

func scanCurationRun(row rowScanner) (curationdomain.CurationRun, error) {
	var run curationdomain.CurationRun
	err := row.Scan(
		&run.ID, &run.CurationID, &run.PlanID, &run.UserID,
		&run.Kind, &run.Instruction,
		&run.PlanningTaskID, &run.Status, &run.IdempotencyKey, &run.RequestHash,
		&run.CreatedAt, &run.UpdatedAt, &run.CompletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return curationdomain.CurationRun{}, curationdomain.ErrCurationRunNotFound
	}
	if err != nil {
		return curationdomain.CurationRun{}, fmt.Errorf("scan curation run: %w", err)
	}
	return run, nil
}

func (r *Repository) UpdateCurationRun(
	ctx context.Context,
	run curationdomain.CurationRun,
) error {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE curation_runs
		SET status=$1, updated_at=$2, completed_at=$3
		WHERE id=$4 AND curation_id=$5 AND user_id=$6
	`, run.Status, run.UpdatedAt, run.CompletedAt,
		run.ID, run.CurationID, run.UserID)
	if err != nil {
		return fmt.Errorf("update curation run: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return curationdomain.ErrCurationRunNotFound
	}
	return nil
}

func (r *Repository) getPlanningTaskByID(
	ctx context.Context,
	userID, planID, taskID string,
	forUpdate bool,
) (curationdomain.PlanningTask, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	return scanPlanningTask(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, plan_id, user_id, context_version, context_hash, status,
		       expires_at, completed_at,
		       first_discovered_at, first_context_read_at, last_agent_activity_at,
		       created_at, updated_at
		FROM planning_tasks
		WHERE id=$1 AND user_id=$2 AND plan_id=$3`+lock,
		taskID, userID, planID,
	))
}
