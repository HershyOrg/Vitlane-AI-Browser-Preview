package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

// fundingPlanSnapshot is a deliberately local projection of the immutable
// AgencyOrder snapshot. Procurement consumes the contract without importing
// the AgencyOrder product's domain types.
type fundingPlanSnapshot struct {
	Lines []struct {
		LineID   string `json:"lineId"`
		Quantity int    `json:"quantity"`
	} `json:"lines"`
	MerchantCheckouts []json.RawMessage `json:"merchantCheckouts"`
}

type fundingPlanCheckout struct {
	MerchantID         string   `json:"merchantId"`
	ShopDomain         string   `json:"shopDomain"`
	LineRefs           []string `json:"lineRefs"`
	AuthoritativeTotal struct {
		AmountMinor int64  `json:"amountMinor"`
		Currency    string `json:"currency"`
	} `json:"authoritativeTotal"`
}

type fundingPlanAllocation struct {
	ID               string
	CheckoutOrdinal  int
	MerchantID       string
	ShopDomain       string
	PassThroughMinor int64
	CustomerGross    int64
}

// PlanFromFunding creates the complete Procurement graph from a verified
// funding-ready CustomerPayment. PayPal reaches this point after AUTHORIZE,
// before any capture. GIWA reaches it after finalized prepayment. Every MO has
// a pre-created AVAILABLE funding position, but no merchant effect is funded
// or started by this transaction.
func (r *Repository) PlanFromFunding(
	ctx context.Context,
	agencyOrderID string,
	input procmsg.PlanFromFundingPayload,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)

		customerPaymentID := input.CustomerPaymentID
		if customerPaymentID == "" || (input.Rail != "PAYPAL" && input.Rail != "GIWA") || (input.Rail == "GIWA" && input.FundsReceiptID == "") {
			return domain.ErrPlanNotEligible
		}
		var existing bool
		if err := q.QueryRowContext(tx, `SELECT EXISTS(SELECT 1 FROM procurement_manifests WHERE agency_order_id=$1)`, agencyOrderID).Scan(&existing); err != nil {
			return err
		}
		if existing {
			return nil
		}
		var snapshotHash, executionMode, profileHash string
		rail, fundsReceiptID, paymentAmount := input.Rail, input.FundsReceiptID, input.AmountMinor
		var rawSnapshot []byte
		// AgencyOrder snapshots and allocations are immutable shared references.
		// No current Payment row is read or locked here: readiness is a verified fact.
		err := q.QueryRowContext(tx, `SELECT snapshot,snapshot_hash,merchant_execution_mode,execution_profile_hash FROM agency_orders WHERE id=$1`, agencyOrderID).Scan(&rawSnapshot, &snapshotHash, &executionMode, &profileHash)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrPlanNotEligible
		}
		if err != nil {
			return fmt.Errorf("read immutable order snapshot: %w", err)
		}

		var snapshot fundingPlanSnapshot
		if err := json.Unmarshal(rawSnapshot, &snapshot); err != nil ||
			len(snapshot.MerchantCheckouts) == 0 || len(snapshot.Lines) == 0 {
			return domain.ErrPlanNotEligible
		}
		checkouts := make([]fundingPlanCheckout, len(snapshot.MerchantCheckouts))
		for index, rawCheckout := range snapshot.MerchantCheckouts {
			if err := json.Unmarshal(rawCheckout, &checkouts[index]); err != nil ||
				checkouts[index].MerchantID == "" || checkouts[index].ShopDomain == "" ||
				checkouts[index].AuthoritativeTotal.AmountMinor <= 0 ||
				checkouts[index].AuthoritativeTotal.Currency != "USD" {
				return domain.ErrPlanNotEligible
			}
		}
		lineQuantities := make(map[string]int, len(snapshot.Lines))
		for _, line := range snapshot.Lines {
			if line.LineID == "" || line.Quantity <= 0 || lineQuantities[line.LineID] != 0 {
				return domain.ErrPlanNotEligible
			}
			lineQuantities[line.LineID] = line.Quantity
		}

		rows, err := q.QueryContext(tx, `
			SELECT allocation.id::text, allocation.checkout_ordinal,
			       allocation.merchant_id, allocation.shop_domain,
			       allocation.pass_through_minor, allocation.customer_gross_minor
			FROM agency_order_mo_allocations allocation
			WHERE allocation.agency_order_id=$1
			ORDER BY allocation.checkout_ordinal
		`, agencyOrderID)
		if err != nil {
			return err
		}
		allocations := make([]fundingPlanAllocation, 0, len(snapshot.MerchantCheckouts))
		for rows.Next() {
			var allocation fundingPlanAllocation
			if err := rows.Scan(
				&allocation.ID, &allocation.CheckoutOrdinal, &allocation.MerchantID,
				&allocation.ShopDomain, &allocation.PassThroughMinor,
				&allocation.CustomerGross,
			); err != nil {
				rows.Close()
				return err
			}
			allocations = append(allocations, allocation)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(allocations) != len(snapshot.MerchantCheckouts) {
			return domain.ErrPlanNotEligible
		}
		var grossSum int64
		for index, allocation := range allocations {
			checkout := checkouts[index]
			if allocation.CheckoutOrdinal != index+1 ||
				allocation.MerchantID != checkout.MerchantID ||
				allocation.ShopDomain != checkout.ShopDomain ||
				allocation.PassThroughMinor != checkout.AuthoritativeTotal.AmountMinor ||
				allocation.CustomerGross < allocation.PassThroughMinor {
				return domain.ErrPlanNotEligible
			}
			grossSum += allocation.CustomerGross
		}
		if grossSum != paymentAmount {
			return domain.ErrPlanNotEligible
		}

		positions := map[string]procmsg.FundingPositionSnapshot{}
		for _, position := range input.Positions {
			if position.PositionID == "" || positions[position.AllocationID].PositionID != "" || position.State != "AVAILABLE" {
				return domain.ErrPlanNotEligible
			}
			positions[position.AllocationID] = position
		}
		if len(positions) != len(allocations) {
			return domain.ErrPlanNotEligible
		}
		for _, allocation := range allocations {
			if positions[allocation.ID].AmountMinor != allocation.CustomerGross {
				return domain.ErrPlanNotEligible
			}
		}

		lineCoverage := make(map[string]int, len(lineQuantities))
		for _, checkout := range checkouts {
			for _, lineID := range checkout.LineRefs {
				if lineQuantities[lineID] == 0 {
					return domain.ErrPlanNotEligible
				}
				lineCoverage[lineID]++
			}
		}
		for lineID := range lineQuantities {
			if lineCoverage[lineID] != 1 {
				return domain.ErrPlanNotEligible
			}
		}

		var manifestID string
		if err := q.QueryRowContext(tx, `
			INSERT INTO procurement_manifests(
				id,agency_order_id,funds_receipt_id,customer_payment_id,snapshot_hash,
				execution_profile_hash,created_at
			) VALUES(
				md5($1||':manifest')::uuid,$2,
				CASE WHEN $6='GIWA' THEN NULLIF($7,'')::uuid ELSE NULL END,
				CASE WHEN $6='PAYPAL' THEN $1::uuid ELSE NULL END,
				$3,$4,$5
			)
			RETURNING id::text
		`, customerPaymentID, agencyOrderID, snapshotHash, profileHash, now,
			rail, fundsReceiptID).Scan(&manifestID); err != nil {
			return err
		}

		merchantOrderIDs := make([]string, 0, len(allocations))
		for index, allocation := range allocations {
			checkout := checkouts[index]
			checkoutJSON := snapshot.MerchantCheckouts[index]
			var merchantOrderID string
			if err := q.QueryRowContext(tx, `
				INSERT INTO merchant_orders(
					id,agency_order_id,manifest_id,allocation_id,merchant_id,
					shop_domain,checkout_ordinal,checkout_snapshot,execution_mode,
					state,version,created_at,updated_at
				) VALUES(
					md5($1||':mo:'||($2::integer)::text)::uuid,$3,$4,$5,$6,$7,$2::integer,$8,
					$9,'PLANNED',1,$10,$10
				) RETURNING id::text
			`, customerPaymentID, allocation.CheckoutOrdinal, agencyOrderID,
				manifestID, allocation.ID, allocation.MerchantID, allocation.ShopDomain,
				checkoutJSON, executionMode, now).Scan(&merchantOrderID); err != nil {
				return err
			}
			merchantOrderIDs = append(merchantOrderIDs, merchantOrderID)

			if _, err := q.ExecContext(tx, `
				INSERT INTO merchant_payments(
					id,merchant_order_id,agency_order_id,amount_minor,currency,
					state,version,created_at,updated_at
				) VALUES(md5($1||':merchant-payment')::uuid,$1::uuid,$2,$3,'USD','PLANNED',1,$4,$4)
			`, merchantOrderID, agencyOrderID, allocation.PassThroughMinor, now); err != nil {
				return err
			}
			for _, lineID := range checkout.LineRefs {
				quantity := lineQuantities[lineID]
				if quantity <= 0 {
					return domain.ErrPlanNotEligible
				}
				for unitIndex := 1; unitIndex <= quantity; unitIndex++ {
					if _, err := q.ExecContext(tx, `
						INSERT INTO merchant_order_units(
							id,merchant_order_id,agency_order_id,line_id,unit_index,
							disposition,version,created_at,updated_at
						) VALUES(
							md5($1||':unit:'||$2||':'||($3::integer)::text)::uuid,$1::uuid,$4,$2,$3::integer,
							'PENDING',1,$5,$5
						)
					`, merchantOrderID, lineID, unitIndex, agencyOrderID, now); err != nil {
						return err
					}
				}
			}
			if _, err := q.ExecContext(tx, `
				INSERT INTO merchant_order_execution_tasks(
					id,merchant_order_id,agency_order_id,state,version,created_at,updated_at
				) VALUES(md5($1||':task')::uuid,$1::uuid,$2,'QUEUED',1,$3,$3)
			`, merchantOrderID, agencyOrderID, now); err != nil {
				return err
			}
			if _, err := q.ExecContext(tx, `
				INSERT INTO agency_order_execution_audits(
					id,agency_order_id,merchant_order_id,action,details,created_at
				) VALUES(gen_random_uuid(),$1,$2,'PROCUREMENT_PLANNED',
					jsonb_build_object('source','process.plan_from_funding','rail',$3::text),$4)
			`, agencyOrderID, merchantOrderID, rail, now); err != nil {
				return err
			}
		}
		for _, merchantOrderID := range merchantOrderIDs {
			if err := emitMerchantOrderEvent(tx, q, merchantOrderID, now); err != nil {
				return err
			}
		}
		return nil
	})
}
