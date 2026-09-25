package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	planningdomain "github.com/vitlane/vitlane/server/internal/curation/planning/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct {
	database *sharedpostgres.Database
}

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

func (r *Repository) CreateIdempotent(
	ctx context.Context,
	plan planningdomain.ShoppingPlan,
	idempotencyKey string,
	requestHash []byte,
) (planningdomain.ShoppingPlan, bool, error) {
	queryer := r.database.Queryer(ctx)
	reservation, err := queryer.ExecContext(ctx, `
		INSERT INTO plan_creation_requests(
			user_id, idempotency_key, request_hash, shopping_plan_id, created_at, completed_at
		) VALUES ($1,$2,$3,NULL,$4,NULL)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
	`, plan.UserID, idempotencyKey, requestHash, plan.CreatedAt)
	if err != nil {
		return planningdomain.ShoppingPlan{}, false,
			fmt.Errorf("reserve plan creation request: %w", err)
	}
	inserted, _ := reservation.RowsAffected()
	if inserted == 0 {
		var storedHash []byte
		var planID sql.NullString
		if err := queryer.QueryRowContext(ctx, `
			SELECT request_hash, shopping_plan_id::text
			FROM plan_creation_requests
			WHERE user_id=$1 AND idempotency_key=$2
		`, plan.UserID, idempotencyKey).Scan(&storedHash, &planID); err != nil {
			return planningdomain.ShoppingPlan{}, false,
				fmt.Errorf("read plan creation request: %w", err)
		}
		if !bytes.Equal(storedHash, requestHash) || !planID.Valid {
			return planningdomain.ShoppingPlan{}, false,
				planningdomain.ErrIdempotencyKeyReused
		}
		stored, getErr := r.Get(ctx, string(plan.UserID), planID.String, false)
		return stored, true, getErr
	}

	researchScopeSnapshot, err := json.Marshal(plan.ResearchScope)
	if err != nil {
		return planningdomain.ShoppingPlan{}, false,
			fmt.Errorf("encode plan research scope: %w", err)
	}
	if _, err := queryer.ExecContext(ctx, `
		INSERT INTO shopping_plans(
			id, user_id, original_intent, plan_mode, execution_mode,
			budget_amount, budget_currency, country, city,
			research_scope_snapshot, agent_mode, model_key, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`, plan.ID, plan.UserID, plan.OriginalIntent, plan.PlanningMode, plan.ExecutionMode,
		plan.TotalBudget.Amount, plan.TotalBudget.Currency, plan.LocationContext.Country,
		plan.LocationContext.City, researchScopeSnapshot,
		plan.AgentMode, nullableString(plan.ModelKey), plan.CreatedAt,
	); err != nil {
		return planningdomain.ShoppingPlan{}, false,
			fmt.Errorf("insert shopping plan: %w", err)
	}
	return plan, false, nil
}

func (r *Repository) CompleteCreation(
	ctx context.Context,
	plan planningdomain.ShoppingPlan,
	idempotencyKey string,
) error {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE plan_creation_requests
		SET shopping_plan_id=$1, completed_at=$2
		WHERE user_id=$3 AND idempotency_key=$4
		  AND shopping_plan_id IS NULL
	`, plan.ID, plan.CreatedAt, plan.UserID, idempotencyKey)
	if err != nil {
		return fmt.Errorf("complete plan creation request: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return planningdomain.ErrIdempotencyKeyReused
	}
	return nil
}

func (r *Repository) Get(
	ctx context.Context,
	userID, planID string,
	forUpdate bool,
) (planningdomain.ShoppingPlan, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	return scanPlan(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT
			id, user_id, original_intent, plan_mode, execution_mode,
			budget_amount::text, budget_currency, country, city,
			research_scope_snapshot::text, agent_mode, model_key, created_at
		FROM shopping_plans
		WHERE id=$1 AND user_id=$2`+lock,
		planID, userID,
	))
}

type rowScanner interface {
	Scan(...any) error
}

func scanPlan(row rowScanner) (planningdomain.ShoppingPlan, error) {
	var plan planningdomain.ShoppingPlan
	var budgetAmount, budgetCurrency, country string
	var researchScopeSnapshot, modelKey sql.NullString
	err := row.Scan(
		&plan.ID, &plan.UserID, &plan.OriginalIntent, &plan.PlanningMode,
		&plan.ExecutionMode, &budgetAmount, &budgetCurrency,
		&country, &plan.LocationContext.City, &researchScopeSnapshot,
		&plan.AgentMode, &modelKey, &plan.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return planningdomain.ShoppingPlan{}, planningdomain.ErrPlanNotFound
	}
	if err != nil {
		return planningdomain.ShoppingPlan{}, fmt.Errorf("scan shopping plan: %w", err)
	}
	plan.ModelKey = modelKey.String
	plan.TotalBudget, err = shareddomain.NewMoney(budgetAmount, budgetCurrency)
	if err != nil {
		return planningdomain.ShoppingPlan{}, err
	}
	plan.LocationContext.Country, err = shareddomain.NewCountryCode(country)
	if err != nil {
		return planningdomain.ShoppingPlan{}, err
	}
	if researchScopeSnapshot.Valid {
		if err := json.Unmarshal(
			[]byte(researchScopeSnapshot.String),
			&plan.ResearchScope,
		); err != nil {
			return planningdomain.ShoppingPlan{},
				fmt.Errorf("scan plan research scope: %w", err)
		}
	} else {
		plan.ResearchScope = planningdomain.ResearchScope{
			Country:      plan.LocationContext.Country,
			City:         plan.LocationContext.City,
			AllowedItems: []string{},
			BlockedItems: []string{},
			URLMode:      planningdomain.URLModeNone,
		}
	}
	return plan, nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
