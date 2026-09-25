package postgres

import (
	"context"
	"fmt"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
)

// ListCurations reads one sidebar page in a single statement. It joins only
// the table that holds the title, and walks the partial index on
// (user_id, created_at DESC, id DESC) so the work stays one page long however
// many Curations the user has (ADR-0079).
func (r *Repository) ListCurations(
	ctx context.Context,
	userID string,
	query curationapp.CurationListQuery,
	pageSize int,
) (curationapp.CurationListRepositoryPage, error) {
	statement, args := curationListStatement(userID, query, pageSize)
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, statement, args...)
	if err != nil {
		return curationapp.CurationListRepositoryPage{}, fmt.Errorf("list curations: %w", err)
	}
	defer rows.Close()

	items := make([]curationapp.CurationListItem, 0, pageSize+1)
	for rows.Next() {
		var item curationapp.CurationListItem
		if err := rows.Scan(&item.CurationID, &item.IntentSummary, &item.CreatedAt); err != nil {
			return curationapp.CurationListRepositoryPage{}, fmt.Errorf("scan curation list: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return curationapp.CurationListRepositoryPage{}, fmt.Errorf("iterate curation list: %w", err)
	}
	page := curationapp.CurationListRepositoryPage{Items: items}
	if len(items) > pageSize {
		page.Items = items[:pageSize]
		page.More = true
	}
	return page, nil
}

// curationListStatement always orders newest first. Before keeps rows older
// than its cursor and After keeps newer ones; reading one row past the page
// tells whether more remain in that direction.
func curationListStatement(
	userID string,
	query curationapp.CurationListQuery,
	pageSize int,
) (string, []any) {
	args := []any{userID}
	bound := ""
	cursor, comparison := query.Before, "<"
	if query.After != nil {
		cursor, comparison = query.After, ">"
	}
	if cursor != nil {
		args = append(args, cursor.CreatedAt, cursor.CurationID)
		bound = fmt.Sprintf("\n\t\t  AND (curation.created_at, curation.id) %s ($2, $3::uuid)", comparison)
	}
	args = append(args, pageSize+1)
	return fmt.Sprintf(`
		SELECT curation.id::text, plan.original_intent, curation.created_at
		FROM curations AS curation
		JOIN shopping_plans AS plan ON plan.id = curation.shopping_plan_id
		WHERE curation.user_id = $1
		  AND curation.archived_at IS NULL%s
		ORDER BY curation.created_at DESC, curation.id DESC
		LIMIT $%d`, bound, len(args)), args
}
