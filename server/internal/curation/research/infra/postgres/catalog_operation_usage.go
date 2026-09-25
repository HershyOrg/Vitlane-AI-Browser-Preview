package postgres

import (
	"context"
	a "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

func (r *Repository) catalogOperationUsage(ctx context.Context, source string) ([]a.CatalogOperationUsage, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT operation,count(*),
 count(*) FILTER(WHERE outcome NOT IN ('SUCCESS','RUNNING','CATALOG_PRODUCT_NOT_FOUND'))
 FROM research_catalog_api_calls WHERE source=$1 AND started_at>now()-interval '24 hours'
 AND (billable OR operation='USAGE') GROUP BY operation ORDER BY operation`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []a.CatalogOperationUsage{}
	seen := map[string]bool{}
	for rows.Next() {
		var x a.CatalogOperationUsage
		if err = rows.Scan(&x.Operation, &x.Requests24h, &x.Failures24h); err != nil {
			return nil, err
		}
		if source == "TELEGRAM_JIRUM" {
			_, x.DailyLimit = a.TelegramOperationLimits(x.Operation)
		}
		if source == "AMAZON" && x.Operation == "FEED" {
			x.DailyLimit = r.backgroundAmazonDailyCalls
		}
		seen[x.Operation] = true
		out = append(out, x)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if source == "TELEGRAM_JIRUM" {
		for _, op := range []string{"FEED_PAGE", "LINK_RESOLVE"} {
			if !seen[op] {
				_, cap := a.TelegramOperationLimits(op)
				out = append(out, a.CatalogOperationUsage{Operation: op, DailyLimit: cap})
			}
		}
	}
	if source == "AMAZON" && !seen["FEED"] {
		out = append(out, a.CatalogOperationUsage{Operation: "FEED", DailyLimit: r.backgroundAmazonDailyCalls})
	}
	return out, nil
}
