package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
)

func (r *Repository) CreateAgencyOrderConsent(ctx context.Context, userID, orderID string,
	identity settlementapp.AgencyOrderIdentity, config settlementdomain.SettlementConfig, now time.Time) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		// A sealed authorization is an existing result, including after its pay
		// deadline or runtime configuration changes. Match the original consent
		// before bypassing first-issuance checks; never create or replace it here.
		var sameConsent bool
		err := r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT COALESCE(c.user_id=$2 AND c.wallet_id=$3
				AND c.wallet_ownership_proof_id=$4, FALSE)
			FROM settlement_authorizations a
			JOIN agency_orders o ON o.id=a.agency_order_id
			LEFT JOIN agency_order_payment_consents c ON c.agency_order_id=a.agency_order_id
			WHERE a.agency_order_id=$1 AND o.user_id=$2
		`, orderID, userID, identity.WalletID, identity.OwnershipProofID).Scan(&sameConsent)
		if err == nil {
			if !sameConsent {
				return settlementdomain.ErrAuthorizationInvalid
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		var amountMinor int64
		var expiresAt time.Time
		err = r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT i.amount_minor,i.expires_at
			FROM agency_orders o
			JOIN agency_order_payment_instructions i ON i.agency_order_id=o.id
			JOIN user_policy_acceptances p ON p.user_id=o.user_id
			WHERE o.id=$1 AND o.user_id=$2 AND o.status='ISSUED'
			  AND i.state IN ('PENDING','CONSUMED') AND i.expires_at>$3
			  AND p.policy_id='PHASE5_TEST_SETTLEMENT' AND p.policy_version='2026-07-24'
		`, orderID, userID, now).Scan(&amountMinor, &expiresAt)
		if err != nil {
			return settlementdomain.ErrAuthorizationInvalid
		}
		if !now.Before(identity.OwnershipValidUntil) || identity.OwnershipValidUntil.Before(expiresAt) ||
			identity.PayerChainID != config.ChainCAIP2 || strings.TrimSpace(identity.PayerAddress) == "" {
			return settlementdomain.ErrAuthorizationInvalid
		}
		var registryVersion uint64
		var principal string
		err = r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT registry_version,principal_recipient
			FROM merchant_registry_entries
			WHERE merchant_id=$1 AND active=TRUE AND payment_enabled=TRUE
		`, settlementdomain.GenericWebUSDSettlementPathID).Scan(&registryVersion, &principal)
		if err != nil {
			return settlementdomain.ErrAuthorizationInvalid
		}
		var storedWalletID, storedProofID string
		err = r.database.Queryer(tx).QueryRowContext(tx, `
			INSERT INTO agency_order_payment_consents(
				agency_order_id,user_id,wallet_id,wallet_ownership_proof_id,amount_base_units,
				token_address,settlement_address,chain_id,merchant_id,merchant_registry_version,
				fee_bps,fee_recipient,principal_recipient,created_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			ON CONFLICT(agency_order_id) DO UPDATE SET
				agency_order_id=EXCLUDED.agency_order_id
			WHERE agency_order_payment_consents.user_id=EXCLUDED.user_id
			  AND agency_order_payment_consents.wallet_id=EXCLUDED.wallet_id
			  AND agency_order_payment_consents.wallet_ownership_proof_id=EXCLUDED.wallet_ownership_proof_id
			  AND agency_order_payment_consents.amount_base_units=EXCLUDED.amount_base_units
			  AND lower(agency_order_payment_consents.token_address)=lower(EXCLUDED.token_address)
			  AND lower(agency_order_payment_consents.settlement_address)=lower(EXCLUDED.settlement_address)
			  AND agency_order_payment_consents.chain_id=EXCLUDED.chain_id
			RETURNING wallet_id,wallet_ownership_proof_id
		`, orderID, userID, identity.WalletID, identity.OwnershipProofID,
			fmt.Sprintf("%d0000", amountMinor), strings.ToLower(config.TokenAddress), strings.ToLower(config.SettlementAddress),
			config.ChainID, settlementdomain.GenericWebUSDSettlementPathID, registryVersion,
			config.FeeBps, strings.ToLower(config.FeeRecipient), strings.ToLower(principal), now,
		).Scan(&storedWalletID, &storedProofID)
		if err != nil || storedWalletID != identity.WalletID || storedProofID != identity.OwnershipProofID {
			return settlementdomain.ErrAuthorizationInvalid
		}
		return nil
	})
}
