package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"
)

const (
	eventStreamCompensationA = "eeeeeeee-eeee-4eee-8eee-eeeeeeee5101"
	eventStreamCompensationB = "eeeeeeee-eeee-4eee-8eee-eeeeeeee5102"
)

// Compensation completion can happen independently per MO. Each transition is
// reported by identity and the reducer folds the order aggregate itself
// (ADR-0070 §4.1); concurrent appends must still receive distinct seqs.
func TestMOCompensationEventSerializesAggregateObservationAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 18, 30, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed compensation concurrency fixture: %v", err)
		}
	}
	exec(`
		INSERT INTO payment_mo_compensations(
			id,allocation_id,funding_position_id,agency_order_id,customer_payment_id,
			rail,provider_environment,action,cause,state,amount_minor,currency,
			execution_profile_hash,idempotency_key,version,approved_at,
			created_at,updated_at
		) VALUES
			($1,$3,$5,$7,$8,'PAYPAL','SANDBOX','REFUND','PROCUREMENT_FAILURE',
			 'EXECUTION_PENDING',10570,'USD',$9,'event-stream-compensation-a',1,$10,$10,$10),
			($2,$4,$6,$7,$8,'PAYPAL','SANDBOX','VOID','PROCUREMENT_FAILURE',
			 'EXECUTION_PENDING',2138,'USD',$9,'event-stream-compensation-b',1,$10,$10,$10)
	`, eventStreamCompensationA, eventStreamCompensationB,
		fundingAllocationA, fundingAllocationB, fundingPositionA, fundingPositionB,
		fundingOrderID, fundingCustomerPay, fundingSandboxHash, now)

	compensationIDs := []string{eventStreamCompensationA, eventStreamCompensationB}
	ready := make(chan struct{}, len(compensationIDs))
	startEmit := make(chan struct{})
	errs := make(chan error, len(compensationIDs))
	for index, compensationID := range compensationIDs {
		index, compensationID := index, compensationID
		go func() {
			err := database.WithinTransaction(ctx, func(tx context.Context) error {
				q := database.Queryer(tx)
				completedAt := now.Add(time.Duration(index+1) * time.Second)
				result, err := q.ExecContext(tx, `
					UPDATE payment_mo_compensations
					SET state='SUCCEEDED',completed_at=$2,version=version+1,updated_at=$2
					WHERE id=$1
				`, compensationID, completedAt)
				if err != nil {
					return err
				}
				if affected, err := result.RowsAffected(); err != nil || affected != 1 {
					return fmt.Errorf("update compensation: affected=%d err=%w", affected, err)
				}
				ready <- struct{}{}
				select {
				case <-startEmit:
				case <-tx.Done():
					return tx.Err()
				}
				return EmitMOCompensationEvent(tx, q, compensationID, completedAt)
			})
			errs <- err
		}()
	}
	for range compensationIDs {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(startEmit)
	for range compensationIDs {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent compensation event: %v", err)
		}
	}

	// Each sibling publishes only its own row (ADR-0070 §4.1); the reducer
	// derives active/succeeded counts from its identity fold. The order stream
	// lock taken at append time still assigns distinct, commit-ordered seqs.
	var succeededEvents, distinctSeq, distinctCompensations int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*), count(DISTINCT seq), count(DISTINCT payload->>'compensationId')
		FROM order_process_events
		WHERE agency_order_id=$1
		  AND type='payment.mo_compensation.state_changed.v1'
		  AND payload->>'state'='SUCCEEDED'
	`, fundingOrderID).Scan(&succeededEvents, &distinctSeq, &distinctCompensations); err != nil {
		t.Fatal(err)
	}
	if succeededEvents != 2 || distinctSeq != 2 || distinctCompensations != 2 {
		t.Fatalf("concurrent compensation events not serialized per identity: events=%d seqs=%d compensations=%d",
			succeededEvents, distinctSeq, distinctCompensations)
	}
}
