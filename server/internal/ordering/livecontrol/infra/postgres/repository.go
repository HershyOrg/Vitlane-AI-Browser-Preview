package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	liveapp "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/app"
	livedomain "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct{ database *sharedpostgres.Database }

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

func (r *Repository) Get(ctx context.Context) (livedomain.State, error) {
	var state livedomain.State
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT version, order_issue_killed, paypal_money_killed,
		       merchant_effect_killed, changed_at, changed_by, reason
		FROM ordering_live_control WHERE singleton = TRUE
	`).Scan(&state.Version, &state.OrderIssueKilled, &state.PayPalMoneyKilled,
		&state.MerchantEffectKilled, &state.ChangedAt, &state.ChangedBy, &state.Reason)
	return state, err
}

func (r *Repository) LockForAdmission(ctx context.Context) (livedomain.State, error) {
	var state livedomain.State
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT version, order_issue_killed, paypal_money_killed,
		       merchant_effect_killed, changed_at, changed_by, reason
		FROM ordering_live_control WHERE singleton = TRUE FOR SHARE
	`).Scan(&state.Version, &state.OrderIssueKilled, &state.PayPalMoneyKilled,
		&state.MerchantEffectKilled, &state.ChangedAt, &state.ChangedBy, &state.Reason)
	return state, err
}

func (r *Repository) Apply(ctx context.Context, change liveapp.Change) (livedomain.State, error) {
	var result livedomain.State
	var conflictObserved int64
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		var current livedomain.State
		if err := r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT version, order_issue_killed, paypal_money_killed,
			       merchant_effect_killed, changed_at, changed_by, reason
			FROM ordering_live_control WHERE singleton = TRUE FOR UPDATE
		`).Scan(&current.Version, &current.OrderIssueKilled, &current.PayPalMoneyKilled,
			&current.MerchantEffectKilled, &current.ChangedAt, &current.ChangedBy,
			&current.Reason); err != nil {
			return err
		}
		if current.Version != change.ExpectedVersion {
			conflictObserved = current.Version
			return livedomain.ErrVersionConflict
		}
		orderKilled, moneyKilled, merchantKilled := current.OrderIssueKilled,
			current.PayPalMoneyKilled, current.MerchantEffectKilled
		switch change.Scope {
		case livedomain.ScopeAll:
			orderKilled, moneyKilled, merchantKilled = change.Kill, change.Kill, change.Kill
		case livedomain.ScopeOrderIssue:
			orderKilled = change.Kill
		case livedomain.ScopePayPalMoney:
			moneyKilled = change.Kill
		case livedomain.ScopeMerchantEffect:
			merchantKilled = change.Kill
		default:
			return livedomain.ErrInvalid
		}
		if err := r.database.Queryer(tx).QueryRowContext(tx, `
			UPDATE ordering_live_control
			SET version = version + 1, order_issue_killed = $1,
			    paypal_money_killed = $2, merchant_effect_killed = $3,
			    changed_at = $4, changed_by = $5, reason = $6
			WHERE singleton = TRUE
			RETURNING version, order_issue_killed, paypal_money_killed,
			          merchant_effect_killed, changed_at, changed_by, reason
		`, orderKilled, moneyKilled, merchantKilled, change.ChangedAt,
			change.Actor, change.Reason).Scan(&result.Version, &result.OrderIssueKilled,
			&result.PayPalMoneyKilled, &result.MerchantEffectKilled, &result.ChangedAt,
			&result.ChangedBy, &result.Reason); err != nil {
			return err
		}
		return r.insertAudit(tx, change.AuditID, action(change.Kill), string(change.Scope),
			change.Actor, change.Reason, change.ExpectedVersion, current.Version,
			"SUCCEEDED", "", change.ChangedAt)
	})
	if errors.Is(err, livedomain.ErrVersionConflict) {
		// WithinTransaction rolls back domain errors, so persist the rejected
		// attempt in a separate transaction without mutating control state.
		if conflictObserved > 0 {
			if auditErr := r.database.WithinTransaction(ctx, func(tx context.Context) error {
				return r.insertAudit(tx, change.AuditID, action(change.Kill), string(change.Scope),
					change.Actor, change.Reason, change.ExpectedVersion, conflictObserved,
					"REJECTED", "LIVE_CONTROL_VERSION_CONFLICT", change.ChangedAt)
			}); auditErr != nil {
				return result, auditErr
			}
		}
	}
	return result, err
}

func (r *Repository) RecordRejected(ctx context.Context, attempt liveapp.Attempt) error {
	var observed int64
	if err := r.database.Queryer(ctx).QueryRowContext(ctx,
		`SELECT version FROM ordering_live_control WHERE singleton = TRUE`).Scan(&observed); err != nil {
		return err
	}
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		return r.insertAudit(tx, attempt.AuditID, attempt.Action,
			strings.TrimSpace(attempt.Scope), attempt.Actor, attempt.Reason,
			attempt.ExpectedVersion, observed, "REJECTED", attempt.ReasonCode,
			attempt.AttemptedAt)
	})
}

func (r *Repository) insertAudit(ctx context.Context, id, actionName, scope, actor,
	reason string, expectedVersion, observedVersion int64, outcome, reasonCode string,
	at time.Time) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO ordering_live_control_audits(
			audit_id, action, scope, actor_user_id, reason,
			expected_version, observed_version, outcome, reason_code, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (audit_id) DO NOTHING
	`, id, actionName, scope, actor, reason, expectedVersion, observedVersion,
		outcome, reasonCode, at)
	return err
}

func action(kill bool) string {
	if kill {
		return "KILL"
	}
	return "REACTIVATE"
}
