package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
)

// EnsureMerchants owns the AgencyOrder settlement-principal registry.
func (r *Repository) EnsureMerchants(
	ctx context.Context,
	entries []settlementdomain.MerchantRegistryEntry,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		for _, entry := range entries {
			_, err := r.database.Queryer(tx).ExecContext(tx, `
				INSERT INTO merchant_registry_entries(
					merchant_id,display_name,domain_suffixes,country,currency,
					fulfillment_mode,payment_enabled,principal_recipient,
					registry_version,active,updated_at
				) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
				ON CONFLICT(merchant_id) DO UPDATE SET
					display_name=EXCLUDED.display_name,
					domain_suffixes=EXCLUDED.domain_suffixes,
					country=EXCLUDED.country,
					currency=EXCLUDED.currency,
					fulfillment_mode=EXCLUDED.fulfillment_mode,
					payment_enabled=EXCLUDED.payment_enabled,
					principal_recipient=EXCLUDED.principal_recipient,
					registry_version=EXCLUDED.registry_version,
					active=EXCLUDED.active,
					updated_at=EXCLUDED.updated_at
			`, entry.MerchantID, entry.DisplayName, entry.DomainSuffixes,
				entry.Country, entry.Currency, entry.FulfillmentMode,
				entry.PaymentEnabled, strings.ToLower(entry.PrincipalRecipient),
				entry.RegistryVersion, entry.Active, now,
			)
			if err != nil {
				return fmt.Errorf("upsert settlement merchant %s: %w", entry.MerchantID, err)
			}
		}
		return nil
	})
}
