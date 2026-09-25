package postgres

import (
	"context"
	"testing"
	"time"

	processapp "github.com/vitlane/vitlane/server/internal/ordering/process/app"
)

func TestWatchdogHotLimitAndDurableColdCursor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolated(t, ctx)
	db := database.DB

	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	const (
		userID     = "11111111-1111-1111-1111-111111111111"
		snapshotID = "12121212-1212-1212-1212-121212121212"
	)
	exec(`INSERT INTO users(id,status,created_at,updated_at)
		VALUES($1,'ACTIVE',$2,$2)`, userID, now)
	exec(`INSERT INTO shipping_snapshots(
		id,user_id,profile_version,country,masked_summary,encrypted_payload,
		payload_nonce,key_version,snapshot_hmac,created_at,source_kind
	) VALUES($1,$2,1,'US','J*** D**, US',decode('00','hex'),decode('00','hex'),1,'hmac',$3,'ORDER_SHEET_INPUT')`,
		snapshotID, userID, now)

	orders := []struct {
		sessionID string
		orderID   string
		updatedAt time.Time
	}{
		{
			sessionID: "21111111-1111-1111-1111-111111111111",
			orderID:   "31111111-1111-1111-1111-111111111111",
			updatedAt: now.Add(-48 * time.Hour),
		},
		{
			sessionID: "22222222-2222-2222-2222-222222222222",
			orderID:   "32222222-2222-2222-2222-222222222222",
			updatedAt: now.Add(-2 * time.Hour),
		},
		{
			sessionID: "23333333-3333-3333-3333-333333333333",
			orderID:   "33333333-3333-3333-3333-333333333333",
			updatedAt: now.Add(-time.Hour),
		},
	}
	for index, order := range orders {
		exec(`INSERT INTO agency_order_sheet_sessions(
			id,user_id,source_cart_id,source_cart_version,source_cart_snapshot_hash,
			state,version,creation_key_hash,creation_request_hash,snapshot,
			created_at,expires_at,updated_at
		) VALUES($1,$2,gen_random_uuid(),1,$3,'CONSUMED',1,$4,$5,'{}',$6,$6,$6)`,
			order.sessionID, userID, "cart-hash-"+order.orderID,
			"creation-key-"+order.orderID, "creation-request-"+order.orderID, now)
		exec(`INSERT INTO agency_orders(
			id,user_id,order_sheet_session_id,source_cart_id,source_cart_version,
			source_cart_snapshot_hash,shipping_snapshot_id,snapshot_hash,
			idempotency_key_hash,status,customer_payable_minor,currency,snapshot,
			payment_rail,provider_environment,asset,economic_effect,
			merchant_execution_mode,execution_profile_hash,issued_at,expires_at
		) VALUES($1,$2,$3,gen_random_uuid(),1,$4,$5,$6,$7,'ISSUED',3030,'USD','{}',
		        'GIWA','TESTNET','TVITUSD','NO_REAL_VALUE','SIMULATED_NO_EFFECT',
		        '0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732',
		        $8::timestamptz,$8::timestamptz + interval '20 minutes')`,
			order.orderID, userID, order.sessionID, "cart-hash-"+order.orderID,
			snapshotID, "snapshot-hash-"+order.orderID, "idempotency-"+order.orderID, now)
		exec(`INSERT INTO agency_order_processes(
			agency_order_id,state,version,created_at,updated_at
		) VALUES($1,'WAITING_CUSTOMER_PAYMENT',$2,$3,$3)`,
			order.orderID, index+1, order.updatedAt)
	}

	repository := NewStore(database)
	hot, err := repository.ListHotWatchdogSnapshots(ctx, now.Add(-24*time.Hour), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hot) != 1 || hot[0].AgencyOrderID != orders[2].orderID {
		t.Fatalf("hot page=%v", snapshotIDs(hot))
	}

	cold, err := repository.ListColdWatchdogSnapshots(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshotIDs(cold); len(got) != 2 || got[0] != orders[0].orderID || got[1] != orders[1].orderID {
		t.Fatalf("first cold page=%v", got)
	}
	if err := repository.AdvanceColdWatchdogCursor(ctx, cold[len(cold)-1].AgencyOrderID, now); err != nil {
		t.Fatal(err)
	}

	// 새 repository 인스턴스도 PostgreSQL cursor를 이어받는다.
	restarted := NewStore(database)
	cold, err = restarted.ListColdWatchdogSnapshots(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshotIDs(cold); len(got) != 1 || got[0] != orders[2].orderID {
		t.Fatalf("resumed cold page=%v", got)
	}
	if err := restarted.AdvanceColdWatchdogCursor(ctx, cold[0].AgencyOrderID, now); err != nil {
		t.Fatal(err)
	}

	// 마지막 UUID 뒤에서는 corpus 처음으로 돌아간다.
	cold, err = restarted.ListColdWatchdogSnapshots(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshotIDs(cold); len(got) != 2 || got[0] != orders[0].orderID || got[1] != orders[1].orderID {
		t.Fatalf("wrapped cold page=%v", got)
	}
}

func snapshotIDs(snapshots []processapp.WatchdogSnapshot) []string {
	ids := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		ids = append(ids, snapshot.AgencyOrderID)
	}
	return ids
}
