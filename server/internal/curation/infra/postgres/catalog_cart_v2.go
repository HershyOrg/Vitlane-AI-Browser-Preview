package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func (r *Repository) GetCatalogCartV2(
	ctx context.Context,
	userID, curationID string,
) (curationapp.CatalogCartStateV2, error) {
	state := curationapp.CatalogCartStateV2{
		UserID: userID, CurationID: curationID,
		Items: []curationapp.CatalogCartItemV2{},
	}
	queryer := r.database.Queryer(ctx)
	var archivedAt sql.NullTime
	err := queryer.QueryRowContext(ctx, `
		-- Cart remains the Shopify US/USD checkout projection. Research/view currency is separate.
		SELECT 'US', 'USD', curation.archived_at
		FROM curations curation
		JOIN shopping_plans plan ON plan.id=curation.shopping_plan_id
		WHERE curation.user_id=$1 AND curation.id=$2
	`, userID, curationID).Scan(&state.Country, &state.Currency, &archivedAt)
	if errors.Is(err, sql.ErrNoRows) || archivedAt.Valid {
		return state, fault.New(fault.InvalidInput, "PHASE8_CART_CURATION_NOT_AVAILABLE", false)
	}
	if err != nil {
		return state, fmt.Errorf("read catalog cart owner: %w", err)
	}
	err = queryer.QueryRowContext(ctx, `
		SELECT version, updated_at
		FROM phase8_cart_views
		WHERE user_id=$1 AND curation_id=$2
	`, userID, curationID).Scan(&state.Version, &state.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		state.Version = 0
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("read catalog cart view: %w", err)
	}
	rows, err := queryer.QueryContext(ctx, `
		SELECT cart_item_id, plan_target_id, candidate_id,
		       product_title_snapshot, product_url,
		       merchant_name_snapshot, seller_domain, intent_point_snapshot,
		       variant_id, variant_title_snapshot, selected_options,
		       preview_price_minor, preview_currency, quantity,
		       observed_at, added_at
		FROM phase8_cart_items
		WHERE user_id=$1 AND curation_id=$2
		ORDER BY added_at, cart_item_id
	`, userID, curationID)
	if err != nil {
		return state, fmt.Errorf("list catalog cart items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var value curationapp.CatalogCartItemV2
		var productURL, sellerDomain sql.NullString
		var selectedOptions []byte
		if err := rows.Scan(
			&value.Item.ID, &value.TargetID, &value.Item.CandidateID,
			&value.Item.ProductTitleSnapshot, &productURL,
			&value.Merchant, &sellerDomain, &value.IntentPoint,
			&value.Item.VariantID, &value.Item.VariantTitleSnapshot, &selectedOptions,
			&value.Item.PreviewPriceMinor, &value.Item.PreviewCurrency,
			&value.Item.Quantity, &value.Item.ObservedAt, &value.Item.AddedAt,
		); err != nil {
			return state, fmt.Errorf("scan catalog cart item: %w", err)
		}
		value.Item.ProductURL = productURL.String
		value.SellerDomain = sellerDomain.String
		if err := json.Unmarshal(selectedOptions, &value.Item.SelectedOptions); err != nil {
			return state, fmt.Errorf("decode catalog cart options: %w", err)
		}
		state.Items = append(state.Items, value)
	}
	if err := rows.Err(); err != nil {
		return state, fmt.Errorf("iterate catalog cart items: %w", err)
	}
	return state, nil
}

func (r *Repository) ReplaceCatalogCartV2(
	ctx context.Context,
	userID, curationID string,
	expectedVersion int64,
	items []curationapp.CatalogCartItemV2,
	now time.Time,
) (curationapp.CatalogCartStateV2, error) {
	var result curationapp.CatalogCartStateV2
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		var archivedAt sql.NullTime
		if err := queryer.QueryRowContext(txContext, `
			SELECT archived_at
			FROM curations
			WHERE user_id=$1 AND id=$2
			FOR UPDATE
		`, userID, curationID).Scan(&archivedAt); errors.Is(err, sql.ErrNoRows) || archivedAt.Valid {
			return fault.New(fault.InvalidInput, "PHASE8_CART_CURATION_NOT_AVAILABLE", false)
		} else if err != nil {
			return fmt.Errorf("lock catalog cart curation: %w", err)
		}
		var currentVersion int64
		err := queryer.QueryRowContext(txContext, `
			SELECT version
			FROM phase8_cart_views
			WHERE user_id=$1 AND curation_id=$2
			FOR UPDATE
		`, userID, curationID).Scan(&currentVersion)
		if errors.Is(err, sql.ErrNoRows) {
			if expectedVersion != 0 {
				return curationapp.ErrCatalogCartVersionConflict
			}
			currentVersion = 0
			if _, err := queryer.ExecContext(txContext, `
				INSERT INTO phase8_cart_views(
					user_id, curation_id, version, created_at, updated_at
				) VALUES ($1,$2,1,$3,$3)
			`, userID, curationID, now); err != nil {
				return fmt.Errorf("create catalog cart view: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("lock catalog cart view: %w", err)
		} else if currentVersion != expectedVersion {
			return curationapp.ErrCatalogCartVersionConflict
		}
		if _, err := queryer.ExecContext(txContext, `
			DELETE FROM phase8_cart_items
			WHERE user_id=$1 AND curation_id=$2
		`, userID, curationID); err != nil {
			return fmt.Errorf("replace catalog cart items: %w", err)
		}
		for _, value := range items {
			var checkoutEligible bool
			if err := queryer.QueryRowContext(txContext, `SELECT EXISTS(SELECT 1 FROM phase8_research_candidates WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3 AND candidate_id=$4 AND source_kind='SHOPIFY_LIVE')`, userID, curationID, value.TargetID, value.Item.CandidateID).Scan(&checkoutEligible); err != nil {
				return err
			}
			if !checkoutEligible {
				return fault.New(fault.InvalidInput, "EXTERNAL_PRODUCT_CART_FORBIDDEN", false)
			}
			options, err := json.Marshal(value.Item.SelectedOptions)
			if err != nil {
				return err
			}
			if _, err := queryer.ExecContext(txContext, `
				INSERT INTO phase8_cart_items(
					user_id, curation_id, cart_item_id, plan_target_id,
					candidate_id, product_title_snapshot, product_url,
					merchant_name_snapshot, seller_domain, intent_point_snapshot,
					variant_id, variant_title_snapshot, selected_options,
					preview_price_minor, preview_currency, quantity,
					observed_at, added_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
			`, userID, curationID, value.Item.ID, value.TargetID,
				value.Item.CandidateID, value.Item.ProductTitleSnapshot,
				nullableCatalogCartString(value.Item.ProductURL), value.Merchant,
				nullableCatalogCartString(value.SellerDomain), value.IntentPoint,
				value.Item.VariantID, value.Item.VariantTitleSnapshot, options,
				value.Item.PreviewPriceMinor, value.Item.PreviewCurrency,
				value.Item.Quantity, value.Item.ObservedAt, value.Item.AddedAt,
			); err != nil {
				return fmt.Errorf("insert catalog cart item: %w", err)
			}
		}
		nextVersion := currentVersion + 1
		if currentVersion > 0 {
			if _, err := queryer.ExecContext(txContext, `
				UPDATE phase8_cart_views
				SET version=$3, updated_at=$4
				WHERE user_id=$1 AND curation_id=$2
			`, userID, curationID, nextVersion, now); err != nil {
				return fmt.Errorf("update catalog cart view: %w", err)
			}
		}
		result, err = r.GetCatalogCartV2(txContext, userID, curationID)
		return err
	})
	return result, err
}

func nullableCatalogCartString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
