package postgres

import (
	"context"
	"fmt"

	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
)

func (r *Repository) ListCurationAgencyOrderTrace(
	ctx context.Context,
	userID, curationID string,
	limit int,
) ([]agencyapp.CurationTrace, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT orders.id, orders.source_cart_id,
		       line->>'planTargetId', line->>'candidateId',
		       process.state, orders.issued_at, process.updated_at
		FROM agency_orders orders
		JOIN agency_order_processes process ON process.agency_order_id=orders.id
		CROSS JOIN LATERAL jsonb_array_elements(orders.snapshot->'lines') line
		WHERE orders.user_id=$1 AND orders.source_cart_id=$2
		  AND COALESCE(line->>'planTargetId','') <> ''
		ORDER BY orders.issued_at, orders.id, line->>'planTargetId'
		LIMIT $3
	`, userID, curationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list curation AgencyOrder trace: %w", err)
	}
	defer rows.Close()
	result := make([]agencyapp.CurationTrace, 0)
	for rows.Next() {
		var item agencyapp.CurationTrace
		if err := rows.Scan(
			&item.AgencyOrderID, &item.CurationID, &item.TargetID,
			&item.CandidateID, &item.State, &item.IssuedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan curation AgencyOrder trace: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) ListCurationAgencyOrderedTargetIDs(
	ctx context.Context,
	userID, curationID string,
) ([]string, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT DISTINCT line->>'planTargetId' AS target_id
		FROM agency_orders orders
		CROSS JOIN LATERAL jsonb_array_elements(orders.snapshot->'lines') line
		WHERE orders.user_id=$1 AND orders.source_cart_id=$2
		  AND COALESCE(line->>'planTargetId','') <> ''
		ORDER BY target_id
	`, userID, curationID)
	if err != nil {
		return nil, fmt.Errorf("list curation AgencyOrder target ids: %w", err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var targetID string
		if err := rows.Scan(&targetID); err != nil {
			return nil, fmt.Errorf("scan curation AgencyOrder target id: %w", err)
		}
		result = append(result, targetID)
	}
	return result, rows.Err()
}
