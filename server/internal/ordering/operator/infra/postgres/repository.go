package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	operatorapp "github.com/vitlane/vitlane/server/internal/ordering/operator/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct {
	database *sharedpostgres.Database
}

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

// CountLivePayPalOrders counts LIVE AgencyOrders that crossed the customer
// PayPal authorization boundary. Local issuance and payer-action checkout
// resources are excluded because they cannot fund merchant order processing.
func (r *Repository) CountLivePayPalOrders(ctx context.Context) (int, error) {
	var count int
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT paypal_authorization.agency_order_id)
		FROM payment_paypal_authorizations paypal_authorization
		JOIN payment_customer_payments payment
		  ON payment.id=paypal_authorization.customer_payment_id
		 AND payment.agency_order_id=paypal_authorization.agency_order_id
		WHERE paypal_authorization.rail='PAYPAL'
		  AND paypal_authorization.provider_environment='LIVE'
		  AND payment.rail='PAYPAL'
		  AND payment.provider_environment='LIVE'
		  AND payment.asset='USD'
		  AND payment.economic_effect='REAL_MONEY'
		  AND payment.merchant_execution_mode='LIVE_MERCHANT_EFFECT'
	`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count LIVE PayPal orders: %w", err)
	}
	return count, nil
}

type lookupCandidate struct {
	agencyOrderID string
	matchedBy     operatorapp.LookupIdentifierType
	environment   string
	paymentRail   string
}

func (r *Repository) ResolveAndAudit(
	ctx context.Context,
	input operatorapp.OrderLookupInput,
	operatorUserID string,
	now time.Time,
) (operatorapp.OrderLookupMatch, error) {
	var match operatorapp.OrderLookupMatch
	var resolutionErr error
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		rows, err := r.database.Queryer(tx).QueryContext(tx, lookupCandidatesSQL,
			input.IdentifierType, input.Value, input.Environment,
			input.ShopDomain, input.Carrier,
		)
		if err != nil {
			return fmt.Errorf("resolve operator order lookup: %w", err)
		}
		defer rows.Close()
		candidates := make([]lookupCandidate, 0, 3)
		for rows.Next() {
			var candidate lookupCandidate
			if err := rows.Scan(
				&candidate.agencyOrderID, &candidate.matchedBy,
				&candidate.environment, &candidate.paymentRail,
			); err != nil {
				return err
			}
			candidates = append(candidates, candidate)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		outcome := "NOT_FOUND"
		matchedBy := ""
		agencyOrderID := ""
		uniqueOrders := make(map[string]lookupCandidate)
		for _, candidate := range candidates {
			if _, exists := uniqueOrders[candidate.agencyOrderID]; !exists {
				uniqueOrders[candidate.agencyOrderID] = candidate
			}
		}
		switch len(uniqueOrders) {
		case 0:
			resolutionErr = operatorapp.ErrOrderLookupNotFound
		case 1:
			outcome = "MATCHED"
			for _, candidate := range uniqueOrders {
				match = operatorapp.OrderLookupMatch{
					AgencyOrderID: candidate.agencyOrderID,
					MatchedBy:     candidate.matchedBy,
					Environment:   candidate.environment,
					PaymentRail:   candidate.paymentRail,
				}
				matchedBy = string(candidate.matchedBy)
				agencyOrderID = candidate.agencyOrderID
			}
		default:
			outcome = "AMBIGUOUS"
			resolutionErr = operatorapp.ErrOrderLookupAmbiguous
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO ordering_operator_order_lookup_audits(
				id, action, operator_user_id, identifier_type, environment,
				query_hash, qualifier_hash, outcome, matched_by,
				agency_order_id, created_at
			) VALUES(
				gen_random_uuid(),'LOOKUP',$1,$2,$3,$4,NULLIF($5,''),$6,
				NULLIF($7,''),NULLIF($8,'')::uuid,$9
			)
		`, operatorUserID, input.IdentifierType, input.Environment,
			lookupHash(input), qualifierHash(input), outcome, matchedBy,
			agencyOrderID, now,
		); err != nil {
			return fmt.Errorf("audit operator order lookup: %w", err)
		}
		return nil
	})
	if err != nil {
		return operatorapp.OrderLookupMatch{}, err
	}
	return match, resolutionErr
}

func (r *Repository) AuditDetailView(
	ctx context.Context,
	agencyOrderID, operatorUserID string,
	now time.Time,
) error {
	sum := sha256.Sum256([]byte("DETAIL_VIEW\x00" + strings.ToLower(agencyOrderID)))
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO ordering_operator_order_lookup_audits(
			id, action, operator_user_id, identifier_type, environment,
			query_hash, outcome, matched_by, agency_order_id, created_at
		)
		SELECT gen_random_uuid(),'DETAIL_VIEW',$2,'AGENCY_ORDER_ID',
		       orders.provider_environment,$3,'MATCHED','AGENCY_ORDER_ID',orders.id,$4
		FROM agency_orders orders WHERE orders.id::text=$1
	`, agencyOrderID, operatorUserID, hex.EncodeToString(sum[:]), now)
	if err != nil {
		return fmt.Errorf("audit operator order detail: %w", err)
	}
	return nil
}

func (r *Repository) ListIdentifiers(
	ctx context.Context,
	agencyOrderID string,
) ([]operatorapp.OrderIdentifier, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, orderIdentifiersSQL, agencyOrderID)
	if err != nil {
		return nil, fmt.Errorf("list operator order identifiers: %w", err)
	}
	defer rows.Close()
	identifiers := make([]operatorapp.OrderIdentifier, 0)
	for rows.Next() {
		var identifier operatorapp.OrderIdentifier
		if err := rows.Scan(
			&identifier.Kind, &identifier.Value, &identifier.Qualifier,
			&identifier.RelatedResourceID,
		); err != nil {
			return nil, err
		}
		identifiers = append(identifiers, identifier)
	}
	return identifiers, rows.Err()
}

func lookupHash(input operatorapp.OrderLookupInput) string {
	payload := strings.Join([]string{
		string(input.IdentifierType), string(input.Environment), input.Value,
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func qualifierHash(input operatorapp.OrderLookupInput) string {
	if input.ShopDomain == "" && input.Carrier == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(input.ShopDomain + "\x00" + input.Carrier))
	return hex.EncodeToString(sum[:])
}

const lookupCandidatesSQL = `
	WITH candidates AS (
		SELECT orders.id::text AS agency_order_id, 'AGENCY_ORDER_ID' AS matched_by,
		       orders.provider_environment, orders.payment_rail
		FROM agency_orders orders
		WHERE ($1 IN ('AUTO','AGENCY_ORDER_ID')) AND orders.id::text=lower($2)
		UNION ALL
		SELECT orders.id::text, 'PAYMENT_ID', orders.provider_environment, orders.payment_rail
		FROM agency_orders orders
		JOIN payment_customer_payments payment ON payment.agency_order_id=orders.id
		WHERE ($1 IN ('AUTO','PAYMENT_ID')) AND payment.id::text=lower($2)
		UNION ALL
		SELECT orders.id::text, 'PAYMENT_ID', orders.provider_environment, orders.payment_rail
		FROM agency_orders orders
		JOIN settlement_payments payment ON payment.agency_order_id=orders.id
		WHERE ($1 IN ('AUTO','PAYMENT_ID')) AND payment.id::text=lower($2)
		UNION ALL
		SELECT orders.id::text, 'PAYPAL_ORDER_ID', orders.provider_environment, orders.payment_rail
		FROM agency_orders orders
		JOIN payment_customer_payments payment ON payment.agency_order_id=orders.id
		JOIN payment_paypal_attempts attempt ON attempt.customer_payment_id=payment.id
		WHERE ($1 IN ('AUTO','PAYPAL_ORDER_ID')) AND attempt.paypal_order_id=$2
		UNION ALL
		SELECT orders.id::text, 'PAYPAL_CAPTURE_ID', orders.provider_environment, orders.payment_rail
		FROM agency_orders orders
		JOIN payment_mo_cash_receipts receipt ON receipt.agency_order_id=orders.id
		WHERE ($1 IN ('AUTO','PAYPAL_CAPTURE_ID')) AND receipt.provider_capture_id=$2
		UNION ALL
		SELECT orders.id::text, 'PAYPAL_REFUND_ID', orders.provider_environment, orders.payment_rail
		FROM agency_orders orders
		JOIN payment_mo_compensations compensation ON compensation.agency_order_id=orders.id
		WHERE ($1 IN ('AUTO','PAYPAL_REFUND_ID'))
		  AND compensation.action='REFUND' AND compensation.provider_resource_id=$2
		UNION ALL
		SELECT orders.id::text, 'MERCHANT_ORDER_ID', orders.provider_environment, orders.payment_rail
		FROM agency_orders orders
		JOIN merchant_orders merchant_order ON merchant_order.agency_order_id=orders.id
		WHERE ($1 IN ('AUTO','MERCHANT_ORDER_ID')) AND merchant_order.id::text=lower($2)
		UNION ALL
		SELECT orders.id::text, 'MERCHANT_ORDER_REF', orders.provider_environment, orders.payment_rail
		FROM agency_orders orders
		JOIN merchant_orders merchant_order ON merchant_order.agency_order_id=orders.id
		WHERE ($1 IN ('AUTO','MERCHANT_ORDER_REF')) AND merchant_order.external_order_ref=$2
		  AND ($4='' OR merchant_order.shop_domain=$4)
		UNION ALL
		SELECT orders.id::text, 'SHIPMENT_ID', orders.provider_environment, orders.payment_rail
		FROM agency_orders orders
		JOIN logistics_shipments shipment ON shipment.agency_order_id=orders.id
		WHERE ($1 IN ('AUTO','SHIPMENT_ID')) AND shipment.id::text=lower($2)
		UNION ALL
		SELECT orders.id::text, 'TRACKING_REF', orders.provider_environment, orders.payment_rail
		FROM agency_orders orders
		JOIN logistics_shipments shipment ON shipment.agency_order_id=orders.id
		WHERE ($1 IN ('AUTO','TRACKING_REF')) AND shipment.tracking_ref=$2
		  AND ($5='' OR shipment.carrier=$5)
		UNION ALL
		SELECT orders.id::text, 'GIWA_TX_HASH', orders.provider_environment, orders.payment_rail
		FROM agency_orders orders
		JOIN settlement_payments payment ON payment.agency_order_id=orders.id
		JOIN chain_transactions transaction ON transaction.settlement_payment_id=payment.id
		WHERE ($1 IN ('AUTO','GIWA_TX_HASH')) AND lower(transaction.tx_hash)=lower($2)
	)
	SELECT DISTINCT agency_order_id, matched_by, provider_environment, payment_rail
	FROM candidates
	WHERE ($3='ANY' OR provider_environment=$3)
	ORDER BY agency_order_id, matched_by
`

const orderIdentifiersSQL = `
	WITH identifiers(kind,value,qualifier,related_resource_id,priority) AS (
		SELECT 'AGENCY_ORDER_ID', orders.id::text, '', orders.id::text, 10
		FROM agency_orders orders WHERE orders.id::text=$1
		UNION ALL
		SELECT 'PAYMENT_ID', payment.id::text, payment.rail, payment.id::text, 20
		FROM payment_customer_payments payment WHERE payment.agency_order_id::text=$1
		UNION ALL
		SELECT 'PAYMENT_ID', payment.id::text, 'GIWA', payment.id::text, 20
		FROM settlement_payments payment WHERE payment.agency_order_id::text=$1
		UNION ALL
		SELECT 'PAYPAL_ORDER_ID', attempt.paypal_order_id, '', payment.id::text, 30
		FROM payment_customer_payments payment
		JOIN payment_paypal_attempts attempt ON attempt.customer_payment_id=payment.id
		WHERE payment.agency_order_id::text=$1 AND attempt.paypal_order_id IS NOT NULL
		UNION ALL
		SELECT 'PAYPAL_CAPTURE_ID', receipt.provider_capture_id, '',
		       receipt.id::text, 40
		FROM payment_mo_cash_receipts receipt
		WHERE receipt.agency_order_id::text=$1
		UNION ALL
		SELECT 'PAYPAL_REFUND_ID', compensation.provider_resource_id, '',
		       compensation.id::text, 50
		FROM payment_mo_compensations compensation
		WHERE compensation.agency_order_id::text=$1
		  AND compensation.action='REFUND'
		  AND compensation.provider_resource_id IS NOT NULL
		UNION ALL
		SELECT 'MERCHANT_ORDER_ID', merchant_order.id::text, merchant_order.shop_domain,
		       merchant_order.id::text, 60
		FROM merchant_orders merchant_order WHERE merchant_order.agency_order_id::text=$1
		UNION ALL
		SELECT 'MERCHANT_ORDER_REF', merchant_order.external_order_ref,
		       merchant_order.shop_domain, merchant_order.id::text, 70
		FROM merchant_orders merchant_order
		WHERE merchant_order.agency_order_id::text=$1 AND merchant_order.external_order_ref IS NOT NULL
		UNION ALL
		SELECT 'SHIPMENT_ID', shipment.id::text, shipment.carrier, shipment.id::text, 80
		FROM logistics_shipments shipment WHERE shipment.agency_order_id::text=$1
		UNION ALL
		SELECT 'TRACKING_REF', shipment.tracking_ref, shipment.carrier, shipment.id::text, 90
		FROM logistics_shipments shipment WHERE shipment.agency_order_id::text=$1
		UNION ALL
		SELECT 'GIWA_TX_HASH', transaction.tx_hash, transaction.purpose,
		       transaction.tx_hash, 100
		FROM settlement_payments payment
		JOIN chain_transactions transaction ON transaction.settlement_payment_id=payment.id
		WHERE payment.agency_order_id::text=$1
	)
	SELECT kind,value,qualifier,related_resource_id
	FROM identifiers
	WHERE value IS NOT NULL AND value<>''
	GROUP BY kind,value,qualifier,related_resource_id
	ORDER BY min(priority), kind, value
`
