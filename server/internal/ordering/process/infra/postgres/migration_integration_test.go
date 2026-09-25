package postgres_test

import (
	"context"
	"github.com/vitlane/vitlane/server/internal/ordering/testfixture"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestReducerCutoverPreservesEvidenceAndRefusesUnresolvedWork(t *testing.T) {
	for _, tc := range []struct {
		name, merchant, funding string
		blocked                 bool
	}{
		{"planned", "PLANNED", "AVAILABLE", false},
		{"placed", "PLACED", "ACTIVE", false},
		{"failed", "FAILED", "AVAILABLE", false},
		{"cancelled", "CANCELLED", "AVAILABLE", false},
		{"merchant-in-flight", "PLACEMENT_PENDING", "AVAILABLE", true},
		{"merchant-unknown", "PLACEMENT_UNKNOWN", "AVAILABLE", true},
		{"capture-pending", "READY_TO_PLACE", "ACTIVATION_PENDING", true},
		{"capture-unknown", "READY_TO_PLACE", "ACTIVATION_UNKNOWN", true},
		{"captured-without-merchant-result", "READY_TO_PLACE", "ACTIVE", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			db := testfixture.Open(t, ctx, true)
			now := time.Now().UTC()
			testfixture.SeedOrder(t, ctx, db, now)
			const mo = "11111111-2222-4333-8444-555555555555"
			testfixture.Exec(t, ctx, db, `INSERT INTO procurement_manifests(id,agency_order_id,customer_payment_id,snapshot_hash,execution_profile_hash,created_at) VALUES($1,$2,$3,'cutover-evidence',$4,$5)`, mo, testfixture.OrderID, testfixture.PaymentID, testfixture.ProfileHash, now)
			testfixture.Exec(t, ctx, db, `INSERT INTO merchant_orders(id,agency_order_id,manifest_id,allocation_id,merchant_id,shop_domain,checkout_ordinal,checkout_snapshot,execution_mode,state,version,created_at,updated_at) VALUES($1,$2,$1,$3,'merchant-sibling','sibling.example',1,'{}','SIMULATED_NO_EFFECT',$4,1,$5,$5)`, mo, testfixture.OrderID, testfixture.Allocation, tc.merchant, now)
			testfixture.Exec(t, ctx, db, `UPDATE payment_mo_funding_positions SET merchant_order_id=$1,state=$2 WHERE id=$3`, mo, tc.funding, testfixture.PositionID)
			testfixture.Exec(t, ctx, db, `INSERT INTO agency_order_processes(agency_order_id,state,version,workflow,created_at,updated_at) VALUES($1,'PROCUREMENT_IN_PROGRESS',1,'{"foldVersion":2}',$2,$2)`, testfixture.OrderID, now)
			testfixture.Exec(t, ctx, db, `INSERT INTO order_process_events(agency_order_id,seq,source,type,payload,dedup_key,occurred_at,recorded_at,applied_at,applied_version) VALUES($1,1,'AGENCYORDER','agencyorder.order.issued.v1','{}','cutover-event',$2,$2,$2,1)`, testfixture.OrderID, now)
			testfixture.Exec(t, ctx, db, `INSERT INTO order_process_commands(id,agency_order_id,target,type,payload,idempotency_key,caused_by_event_id,state,next_attempt_at,created_at,updated_at,completed_at) SELECT $1,$2,'LOGISTICS','logistics.register_expected_units.v1','{}','cutover-completed',id,'SUCCEEDED',$3,$3,$3,$3 FROM order_process_events WHERE dedup_key='cutover-event'`, mo, testfixture.OrderID, now)
			snapshot := func() string {
				var value string
				if err := db.DB.QueryRowContext(ctx, `SELECT jsonb_build_object('order',(SELECT to_jsonb(a) FROM agency_orders a),'mo',(SELECT to_jsonb(a) FROM merchant_orders a),'funding',(SELECT to_jsonb(a)-ARRAY['workflow_operation_id','workflow_command_id','process_flow_id','process_effect_id'] FROM payment_mo_funding_positions a),'authorization',(SELECT to_jsonb(a) FROM payment_paypal_authorizations a),'allocation',(SELECT to_jsonb(a) FROM agency_order_mo_allocations a))::text`).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			before := snapshot()
			_, file, _, _ := runtime.Caller(0)
			migrations := filepath.Join(filepath.Dir(file), "../../../../../migrations")
			err := db.Migrate(ctx, migrations)
			if tc.blocked {
				if err == nil || !strings.Contains(err.Error(), "ORDER_REDUCER_CUTOVER_REQUIRES_RECONCILIATION") {
					t.Fatalf("unresolved evidence migrated: %v", err)
				}
				var rolledBack bool
				if err := db.DB.QueryRowContext(ctx, `SELECT to_regclass('order_process_commands') IS NOT NULL AND to_regclass('order_process_effects') IS NULL`).Scan(&rolledBack); err != nil || !rolledBack {
					t.Fatalf("partial cutover: %v %v", rolledBack, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var model, archived, effects int
				if err := db.DB.QueryRowContext(ctx, `SELECT (process_state->>'modelVersion')::int,(SELECT COUNT(*) FROM order_process_execution_history WHERE source='effect'),(SELECT COUNT(*) FROM order_process_effects) FROM agency_order_processes`).Scan(&model, &archived, &effects); err != nil {
					t.Fatal(err)
				}
				if model != 3 || archived != 1 || effects != 0 {
					t.Fatalf("cutover fabricated execution or lost history: model=%d archive=%d effects=%d", model, archived, effects)
				}
				if err := db.Migrate(ctx, migrations); err != nil {
					t.Fatalf("migration not repeatable: %v", err)
				}
			}
			if after := snapshot(); after != before {
				t.Fatalf("economic evidence changed during cutover\nbefore=%s\nafter=%s", before, after)
			}
		})
	}
}
