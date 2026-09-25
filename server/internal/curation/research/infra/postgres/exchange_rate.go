package postgres

import (
	"context"
	"database/sql"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"time"
)

func (r *Repository) ReadExchangeRate(ctx context.Context) (researchdomain.DailyExchangeRate, error) {
	result := researchdomain.DailyExchangeRate{Base: "USD", Quote: "KRW", Source: "https://frankfurter.dev/"}
	var rate sql.NullString
	var date, observed sql.NullTime
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT rate,as_of,observed_at FROM research_daily_exchange_rate WHERE pair='USD/KRW'`).Scan(&rate, &date, &observed)
	if rate.Valid && date.Valid && observed.Valid {
		result.Rate = rate.String
		result.AsOf = date.Time.Format("2006-01-02")
		result.ObservedAt = observed.Time
	}
	return result, err
}
func (r *Repository) ClaimExchangeRateRefresh(ctx context.Context, now time.Time) (bool, error) {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE research_daily_exchange_rate SET next_attempt_at=$1::timestamptz+interval '10 minutes' WHERE pair='USD/KRW' AND next_attempt_at<=$1 AND (observed_at IS NULL OR observed_at < date_trunc('day',$1::timestamptz AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')`, now)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
func (r *Repository) SaveExchangeRate(ctx context.Context, rate researchdomain.DailyExchangeRate) error {
	if !rate.Valid(time.Now().UTC()) {
		return researchdomain.ErrExchangeRate
	}
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE research_daily_exchange_rate SET rate=$1,as_of=$2,observed_at=$3,next_attempt_at=$3 WHERE pair='USD/KRW'`, rate.Rate, rate.AsOf, rate.ObservedAt)
	return err
}
