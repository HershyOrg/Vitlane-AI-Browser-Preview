package postgres

import (
	"context"
	"fmt"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

func (r *Repository) ClaimOrderSheetCleanup(
	ctx context.Context,
	now, leaseUntil time.Time,
	limit int,
) ([]agencydomain.OrderSheetSession, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		WITH expired AS (
			UPDATE agency_order_sheet_sessions
			SET state='EXPIRED',
				version=version+1,
				snapshot=jsonb_set(
					jsonb_set(snapshot, '{state}', '"EXPIRED"'::jsonb, true),
					'{version}', to_jsonb(version+1), true
				),
				cleanup_state='PENDING',
				cleanup_available_at=$1,
				cleanup_lease_until=NULL,
				updated_at=$1
			WHERE state NOT IN ('CONSUMED','EXPIRED') AND expires_at <= $1
			RETURNING id
		), candidates AS (
			SELECT id
			FROM agency_order_sheet_sessions
			WHERE (
				cleanup_state='PENDING' AND cleanup_available_at <= $1
			) OR (
				cleanup_state='RUNNING' AND cleanup_lease_until <= $1
			)
			ORDER BY cleanup_available_at ASC NULLS FIRST, created_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT $3
		), claimed AS (
			UPDATE agency_order_sheet_sessions AS session
			SET cleanup_state='RUNNING',
				cleanup_attempt_count=cleanup_attempt_count+1,
				cleanup_lease_until=$2,
				cleanup_last_error_code=NULL,
				updated_at=$1
			FROM candidates
			WHERE session.id=candidates.id
			RETURNING session.user_id, session.snapshot
		)
		SELECT user_id, snapshot FROM claimed
	`, now, leaseUntil, limit)
	if err != nil {
		return nil, fmt.Errorf("claim OrderSheet cleanup: %w", err)
	}
	defer rows.Close()
	result := make([]agencydomain.OrderSheetSession, 0)
	for rows.Next() {
		var userID string
		var payload []byte
		if err := rows.Scan(&userID, &payload); err != nil {
			return nil, err
		}
		session, err := decodeSession(payload, userID)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		if err := r.hydrateCleanupCapabilities(ctx, &result[index]); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// hydrateCleanupCapabilities covers a provider crash window where a capability
// was durably vaulted but the corresponding MerchantCheckout had not yet been
// copied into the OrderSheet snapshot. Only opaque safe references are loaded;
// raw cart, checkout and buyer values remain encrypted in the vault.
func (r *Repository) hydrateCleanupCapabilities(
	ctx context.Context,
	session *agencydomain.OrderSheetSession,
) error {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id,shop_domain,capability_kind
		FROM agency_order_provider_capabilities
		WHERE order_sheet_session_id=$1 AND user_id=$2
		ORDER BY shop_domain,capability_kind
	`, session.ID, session.UserID)
	if err != nil {
		return fmt.Errorf("load OrderSheet cleanup capabilities: %w", err)
	}
	defer rows.Close()
	byShop := make(map[string]int, len(session.MerchantCheckouts))
	for index := range session.MerchantCheckouts {
		byShop[session.MerchantCheckouts[index].ShopDomain] = index
	}
	for rows.Next() {
		var safeRef, shopDomain, kind string
		if err := rows.Scan(&safeRef, &shopDomain, &kind); err != nil {
			return err
		}
		index, found := byShop[shopDomain]
		if !found {
			session.MerchantCheckouts = append(session.MerchantCheckouts,
				agencydomain.MerchantCheckout{MerchantID: shopDomain, ShopDomain: shopDomain},
			)
			index = len(session.MerchantCheckouts) - 1
			byShop[shopDomain] = index
		}
		checkout := &session.MerchantCheckouts[index]
		switch kind {
		case "BUYER_CONTEXT":
			checkout.BuyerContextSafeRef = safeRef
		case "STOREFRONT_CART":
			checkout.StorefrontCartSafeRef = safeRef
		case "UCP_CHECKOUT":
			checkout.CheckoutSessionSafeRef = safeRef
		}
	}
	return rows.Err()
}

func (r *Repository) CompleteOrderSheetCleanup(
	ctx context.Context,
	sessionID string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			DELETE FROM agency_order_provider_capabilities
			WHERE order_sheet_session_id=$1
		`, sessionID); err != nil {
			return fmt.Errorf("delete OrderSheet provider capabilities: %w", err)
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE agency_order_sheet_sessions
			SET cleanup_state='SUCCEEDED',
				cleanup_available_at=NULL,
				cleanup_lease_until=NULL,
				cleanup_last_error_code=NULL,
				cleanup_completed_at=COALESCE(cleanup_completed_at,$2),
				updated_at=$2
			WHERE id=$1 AND cleanup_state IN ('RUNNING','SUCCEEDED')
		`, sessionID, now); err != nil {
			return fmt.Errorf("complete OrderSheet cleanup: %w", err)
		}
		return nil
	})
}

func (r *Repository) RetryOrderSheetCleanup(
	ctx context.Context,
	sessionID, reasonCode string,
	retryAt, now time.Time,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE agency_order_sheet_sessions
		SET cleanup_state='PENDING',
			cleanup_available_at=$2,
			cleanup_lease_until=NULL,
			cleanup_last_error_code=$3,
			updated_at=$4
		WHERE id=$1 AND cleanup_state='RUNNING'
	`, sessionID, retryAt, reasonCode, now)
	if err != nil {
		return fmt.Errorf("retry OrderSheet cleanup: %w", err)
	}
	return nil
}
