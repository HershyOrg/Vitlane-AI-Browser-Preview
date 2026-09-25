package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"
)

const (
	eventStreamSecondAllocationID = "80808080-8080-4080-8080-808080808002"
	eventStreamSecondMerchantID   = "81818181-8181-4181-8181-818181818002"
)

// Two sibling MerchantOrder transactions of one order commit concurrently.
// Each publishes only its own row (ADR-0070 §4.1 — the reducer derives the
// order aggregate from the identity fold), and the order stream lock taken at
// append time still gives every event a distinct, commit-ordered seq.
func TestMerchantOrderEventSerializesAggregateObservationAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openFailedFundingDatabase(t, ctx)
	now := time.Date(2026, 8, 27, 18, 0, 0, 0, time.UTC)
	seedFailedFundingOrder(t, ctx, database, now)
	repository := NewRepository(database)
	if err := repository.PlanFromFunding(ctx, failedFundingOrderID, fundingProof(), now); err != nil {
		t.Fatalf("plan first merchant order: %v", err)
	}

	var firstMerchantOrderID string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT id::text FROM merchant_orders WHERE allocation_id=$1
	`, failedFundingAllocation).Scan(&firstMerchantOrderID); err != nil {
		t.Fatal(err)
	}
	execFailedFunding(t, ctx, database, `
		INSERT INTO agency_order_mo_allocations(
			id,agency_order_id,checkout_ordinal,merchant_id,shop_domain,
			pass_through_minor,fee_variable_minor,fee_fixed_minor,fee_total_minor,
			customer_gross_minor,currency,fee_policy_version,allocation_hash,
			execution_profile_hash,created_at
		) VALUES($1,$2,2,'merchant-concurrent','concurrent.example',1000,54,30,84,
			1084,'USD','PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1',
			'0x'||repeat('ef',32),$3,$4)
	`, eventStreamSecondAllocationID, failedFundingOrderID, failedFundingProfileHash, now)
	execFailedFunding(t, ctx, database, `
		INSERT INTO merchant_orders(
			id,agency_order_id,manifest_id,allocation_id,merchant_id,shop_domain,
			checkout_ordinal,checkout_snapshot,execution_mode,state,version,
			created_at,updated_at
		)
		SELECT $1,$2,manifest.id,$3,'merchant-concurrent','concurrent.example',2,
			'{}','SIMULATED_NO_EFFECT','PLANNED',1,$4,$4
		FROM procurement_manifests manifest WHERE manifest.agency_order_id=$2
	`, eventStreamSecondMerchantID, failedFundingOrderID,
		eventStreamSecondAllocationID, now)

	merchantOrderIDs := []string{firstMerchantOrderID, eventStreamSecondMerchantID}
	ready := make(chan struct{}, len(merchantOrderIDs))
	startEmit := make(chan struct{})
	errs := make(chan error, len(merchantOrderIDs))
	for index, merchantOrderID := range merchantOrderIDs {
		index, merchantOrderID := index, merchantOrderID
		go func() {
			err := database.WithinTransaction(ctx, func(tx context.Context) error {
				q := database.Queryer(tx)
				result, err := q.ExecContext(tx, `
					UPDATE merchant_orders
					SET state='FAILED',failure_code=$2,version=version+1,updated_at=$3
					WHERE id=$1
				`, merchantOrderID, fmt.Sprintf("CONCURRENT_FAILURE_%d", index+1),
					now.Add(time.Duration(index+1)*time.Second))
				if err != nil {
					return err
				}
				if affected, err := result.RowsAffected(); err != nil || affected != 1 {
					return fmt.Errorf("update merchant order: affected=%d err=%w", affected, err)
				}
				ready <- struct{}{}
				select {
				case <-startEmit:
				case <-tx.Done():
					return tx.Err()
				}
				return emitMerchantOrderEvent(
					tx, q, merchantOrderID,
					now.Add(time.Duration(index+1)*time.Second),
				)
			})
			errs <- err
		}()
	}
	for range merchantOrderIDs {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(startEmit)
	for range merchantOrderIDs {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent merchant-order event: %v", err)
		}
	}

	var failedEvents, distinctSeq, distinctMerchantOrders int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*), count(DISTINCT seq), count(DISTINCT payload->>'merchantOrderId')
		FROM order_process_events
		WHERE agency_order_id=$1
		  AND type='procurement.merchant_order.state_changed.v1'
		  AND payload->>'state'='FAILED'
	`, failedFundingOrderID).Scan(&failedEvents, &distinctSeq, &distinctMerchantOrders); err != nil {
		t.Fatal(err)
	}
	if failedEvents != 2 || distinctSeq != 2 || distinctMerchantOrders != 2 {
		t.Fatalf("concurrent MO events not serialized per identity: events=%d seqs=%d merchantOrders=%d",
			failedEvents, distinctSeq, distinctMerchantOrders)
	}
}
