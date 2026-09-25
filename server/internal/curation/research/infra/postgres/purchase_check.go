package postgres

import (
	"context"
	"database/sql"
	"fmt"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"time"
)

// purchaseSnapshotUpsertSet keeps the previous display snapshot when a command
// carries none, so that undo/redo never blanks what the user saw at check time.
// A command with a snapshot replaces the whole set at once (the table CHECK
// constraint requires the columns to be consistent as a set).
const purchaseSnapshotUpsertSet = `product_title=CASE WHEN EXCLUDED.snapshot_at IS NULL THEN research_external_purchase_records.product_title ELSE EXCLUDED.product_title END,` +
	`variant_title=CASE WHEN EXCLUDED.snapshot_at IS NULL THEN research_external_purchase_records.variant_title ELSE EXCLUDED.variant_title END,` +
	`merchant=CASE WHEN EXCLUDED.snapshot_at IS NULL THEN research_external_purchase_records.merchant ELSE EXCLUDED.merchant END,` +
	`price_minor=CASE WHEN EXCLUDED.snapshot_at IS NULL THEN research_external_purchase_records.price_minor ELSE EXCLUDED.price_minor END,` +
	`price_unknown=CASE WHEN EXCLUDED.snapshot_at IS NULL THEN research_external_purchase_records.price_unknown ELSE EXCLUDED.price_unknown END,` +
	`currency=CASE WHEN EXCLUDED.snapshot_at IS NULL THEN research_external_purchase_records.currency ELSE EXCLUDED.currency END,` +
	`snapshot_at=COALESCE(EXCLUDED.snapshot_at,research_external_purchase_records.snapshot_at)`

type purchaseSnapshotValues struct {
	title, variant, merchant, priceMinor, priceUnknown, currency, at any
}

// purchaseSnapshotColumns maps an optional validated snapshot onto the nullable
// column set. Unknown prices store NULL amounts, never 0.
func purchaseSnapshotColumns(snapshot *researchdomain.PurchaseCheckSnapshot, now time.Time) purchaseSnapshotValues {
	if snapshot == nil {
		return purchaseSnapshotValues{}
	}
	values := purchaseSnapshotValues{title: snapshot.ProductTitle, priceUnknown: snapshot.PriceUnknown, at: now}
	if snapshot.VariantTitle != "" {
		values.variant = snapshot.VariantTitle
	}
	if snapshot.Merchant != "" {
		values.merchant = snapshot.Merchant
	}
	if !snapshot.PriceUnknown {
		values.priceMinor = snapshot.PriceMinor
		values.currency = snapshot.Currency
	} else if snapshot.Currency != "" {
		values.currency = snapshot.Currency
	}
	return values
}

// ListPurchaseChecks is the account-scope projection of checked self-reports
// (ADR-0075), newest first. The Target title is read live from the Candidate's
// plan target; a missing Candidate row leaves it empty rather than failing.
func (r *Repository) ListPurchaseChecks(ctx context.Context, user string, limit int) ([]researchdomain.PurchaseCheck, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT r.curation_id, COALESCE(c.plan_target_id::text,''), COALESCE(t.title,''), r.candidate_id,
		       r.source, r.marketplace, COALESCE(r.asin,''), COALESCE(r.product_id,''),
		       r.checked, r.version, r.recorded_at,
		       r.product_title, r.variant_title, r.merchant, r.price_minor, r.price_unknown, r.currency, r.snapshot_at
		FROM research_external_purchase_records r
		LEFT JOIN phase8_research_candidates c
		  ON c.user_id=r.user_id AND c.curation_id=r.curation_id AND c.candidate_id=r.candidate_id
		LEFT JOIN plan_targets t
		  ON t.id=c.plan_target_id AND t.user_id=c.user_id AND t.curation_id=c.curation_id
		WHERE r.user_id=$1 AND r.checked
		ORDER BY r.recorded_at DESC, r.source, r.marketplace, r.asin, r.product_id
		LIMIT $2
	`, user, limit)
	if err != nil {
		return nil, fmt.Errorf("list purchase checks: %w", err)
	}
	defer rows.Close()
	values := []researchdomain.PurchaseCheck{}
	for rows.Next() {
		var value researchdomain.PurchaseCheck
		var source, marketplace, asin, productID string
		var title, variant, merchant, currency sql.NullString
		var priceMinor sql.NullInt64
		var priceUnknown sql.NullBool
		var snapshotAt sql.NullTime
		if err := rows.Scan(&value.CurationID, &value.TargetID, &value.TargetTitle, &value.CandidateID,
			&source, &marketplace, &asin, &productID, &value.Checked, &value.Version, &value.RecordedAt,
			&title, &variant, &merchant, &priceMinor, &priceUnknown, &currency, &snapshotAt); err != nil {
			return nil, fmt.Errorf("scan purchase check: %w", err)
		}
		value.Evidence = "SELF_REPORTED"
		if productID != "" {
			value.ProductRef = &researchdomain.SourceProductRef{Source: researchdomain.Source(source), Marketplace: marketplace, ProductID: productID}
		} else {
			value.VariantRef = &researchdomain.SourceVariantRef{Source: researchdomain.Source(source), Marketplace: marketplace, ASIN: asin}
		}
		if url, err := value.ExternalURL(); err == nil {
			value.ProductURL = url
		}
		if snapshotAt.Valid && title.Valid && priceUnknown.Valid {
			at := snapshotAt.Time
			value.SnapshotAt = &at
			value.Snapshot = &researchdomain.PurchaseCheckSnapshot{ProductTitle: title.String, VariantTitle: variant.String, Merchant: merchant.String, PriceUnknown: priceUnknown.Bool, Currency: currency.String}
			if !priceUnknown.Bool && priceMinor.Valid {
				value.Snapshot.PriceMinor = priceMinor.Int64
			}
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate purchase checks: %w", err)
	}
	return values, nil
}
