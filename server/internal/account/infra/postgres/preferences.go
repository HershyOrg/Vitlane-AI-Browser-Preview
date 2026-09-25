package postgres

import (
	"context"
	"database/sql"
	"errors"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func (r *Repository) ReadPreferences(ctx context.Context, user string) (accountdomain.UserPreferences, error) {
	p := accountdomain.UserPreferences{SchemaVersion: "vitlane.user-preferences.v1"}
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT version, COALESCE(ui_locale,''), COALESCE(preferred_currency,''), COALESCE(research_country,'') FROM account_user_preferences WHERE user_id=$1`, user).Scan(&p.Version, &p.UILocale, &p.PreferredCurrency, &p.ResearchCountry)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return p, err
}

func (r *Repository) PatchPreferences(ctx context.Context, user string, patch accountdomain.PreferencesPatch, expected *int64) (accountdomain.UserPreferences, error) {
	var result accountdomain.UserPreferences
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		if _, err := q.ExecContext(tx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "preferences:"+user); err != nil {
			return err
		}
		stored, err := r.ReadPreferences(tx, user)
		if err != nil {
			return err
		}
		if expected != nil && stored.Version != *expected {
			return fault.New(fault.Conflict, "USER_PREFERENCES_VERSION_CONFLICT", true)
		}
		result, err = stored.Apply(patch)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(tx, `INSERT INTO account_user_preferences(user_id,version,ui_locale,preferred_currency,research_country) VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,'')) ON CONFLICT(user_id) DO UPDATE SET version=EXCLUDED.version,ui_locale=EXCLUDED.ui_locale,preferred_currency=EXCLUDED.preferred_currency,research_country=EXCLUDED.research_country,updated_at=now()`, user, result.Version, result.UILocale, result.PreferredCurrency, result.ResearchCountry)
		return err
	})
	return result, err
}
