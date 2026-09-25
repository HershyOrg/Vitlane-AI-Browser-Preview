package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	agencypostgres "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

// Run the production migration against real pre-fix receipt rows. Payment and
// order evidence must remain identical; only an archived, invalid receipt may
// be repaired once after the Owner supplies finalized proof.
func TestReceiptFinalityMigrationAndOwnerRepair(t *testing.T) {
	for _, tc := range []struct {
		name, state, purpose, txState, reason string
		invalid                               bool
	}{
		{"submitted snapshot", "COMPLETION_SUBMITTED", "PAY", "FINALIZED", "COMPLETED_ALL", true},
		{"safe transaction", "COMPLETED", "COMPLETE", "SAFE", "COMPLETED_ALL", true},
		{"valid completion", "COMPLETED", "COMPLETE", "FINALIZED", "COMPLETED_ALL", false},
		{"valid partial refund", "FINALIZED", "REFUND_PARTIAL", "FINALIZED", "REFUNDED_ALL", false},
		{"valid legacy refund proof", "REFUNDED", "REFUND", "FINALIZED", "REFUNDED_ALL", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			f := seedGIWACompensationGraph(t, ctx, 1)
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := f.database.DB.ExecContext(ctx, q, args...); err != nil {
					t.Fatal(err)
				}
			}
			const receiptID = "88888888-8888-4888-8888-888888888888"
			const terminalHash = "0x1234abcd"
			payload := func(state, purpose, txState string) []byte {
				b, err := json.Marshal(map[string]any{"payment": map[string]string{"state": state}, "transactions": []agencydomain.ChainTransaction{{Purpose: purpose, TxHash: terminalHash, State: txState}}})
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			oldPayload := payload(tc.state, tc.purpose, tc.txState)
			oldHash, err := shareddomain.CanonicalJSONHashBytes(oldPayload)
			if err != nil {
				t.Fatal(err)
			}
			exec(`INSERT INTO agency_order_receipts(id,agency_order_id,settlement_payment_id,kind,legal_sale,terminal_state,terminal_tx_hash,receipt_hash,payload,created_at,payment_rail,provider_environment,asset,economic_effect,merchant_execution_mode,execution_profile_hash)
  VALUES($1,$2,$3,'TEST',false,$4,$5,$6,$7,$8,'GIWA','TESTNET','TVITUSD','NO_REAL_VALUE','SIMULATED_NO_EFFECT',$9)`, receiptID, giwaTestOrderID, giwaTestSettlementID, tc.reason, terminalHash, oldHash, oldPayload, f.now, giwaTestProfileHash)
			// The chain can now be completed while the previously frozen receipt remains
			// invalid, exactly as observed in preview.82.
			exec(`UPDATE settlement_payments SET state='COMPLETED',complete_tx_hash=$1 WHERE id=$2`, terminalHash, giwaTestSettlementID)
			snapshot := func() string {
				t.Helper()
				var s string
				if err := f.database.DB.QueryRowContext(ctx, `SELECT jsonb_build_object('order',(SELECT to_jsonb(o) FROM agency_orders o),'payment',(SELECT to_jsonb(p) FROM settlement_payments p),'funding',(SELECT jsonb_agg(f) FROM payment_mo_funding_positions f),'compensation',(SELECT jsonb_agg(c) FROM payment_mo_compensations c))::text`).Scan(&s); err != nil {
					t.Fatal(err)
				}
				return s
			}
			before := snapshot()
			repo := agencypostgres.NewRepository(f.database)
			_, err = repo.GetReceipt(ctx, giwaTestUserID, giwaTestOrderID)
			if tc.invalid && !errors.Is(err, agencydomain.ErrReceiptNotFound) {
				t.Fatalf("premature receipt remains visible: %v", err)
			}
			if !tc.invalid && err != nil {
				t.Fatal(err)
			}
			migration, err := os.ReadFile(filepath.Join(giwaMigrationDirectory(t), "000100_receipt_terminal_finality.up.sql"))
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				exec(string(migration))
			}
			var archives, events int
			var evidencePreserved bool
			if err := f.database.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM order_process_execution_history WHERE source='receipt'),(SELECT count(*) FROM order_process_events WHERE dedup_key LIKE 'migration100:%'),COALESCE((SELECT h.evidence=to_jsonb(r) FROM order_process_execution_history h JOIN agency_order_receipts r ON h.identity=r.id::text WHERE h.source='receipt'),true)`).Scan(&archives, &events, &evidencePreserved); err != nil {
				t.Fatal(err)
			}
			want := 0
			if tc.invalid {
				want = 1
			}
			if archives != want || events != want || !evidencePreserved {
				t.Fatalf("archive/event mismatch: %d %d preserved=%v", archives, events, evidencePreserved)
			}
			if tc.invalid {
				var kind string
				var raw []byte
				if err := f.database.DB.QueryRowContext(ctx, `SELECT type,payload FROM order_process_events WHERE dedup_key LIKE 'migration100:%'`).Scan(&kind, &raw); err != nil {
					t.Fatal(err)
				}
				var fact procmsg.SettlementStateChangedPayload
				if err := json.Unmarshal(raw, &fact); err != nil || kind != procmsg.EventSettlementStateChanged || fact.State != "COMPLETED" || fact.SettlementPaymentID != giwaTestSettlementID {
					t.Fatalf("invalid repair observation: %s %+v %v", kind, fact, err)
				}
			}
			candidate := agencydomain.Receipt{ID: "99999999-9999-4999-8999-999999999999", AgencyOrderID: giwaTestOrderID, SettlementPaymentID: giwaTestSettlementID, Kind: "TEST", PaymentRail: "GIWA", ProviderEnvironment: "TESTNET", Asset: "TVITUSD", EconomicEffect: "NO_REAL_VALUE", MerchantExecutionMode: "SIMULATED_NO_EFFECT", ExecutionProfileHash: giwaTestProfileHash, TerminalState: tc.reason, TerminalTxHash: terminalHash, CreatedAt: f.now.Add(time.Hour)}
			candidate.Payload = oldPayload
			candidate.ReceiptHash = oldHash
			if tc.invalid && !errors.Is(repo.CreateReceipt(ctx, candidate), agencydomain.ErrInvalid) {
				t.Fatal("repository accepted pending proof")
			}
			purpose := "COMPLETE"
			state := "COMPLETED"
			if tc.reason == "REFUNDED_ALL" {
				purpose = tc.purpose
				state = tc.state
			}
			candidate.Payload = payload(state, purpose, "FINALIZED")
			candidate.ReceiptHash, err = shareddomain.CanonicalJSONHashBytes(candidate.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.CreateReceipt(ctx, candidate); err != nil {
				t.Fatal(err)
			}
			stored, err := repo.GetReceipt(ctx, giwaTestUserID, giwaTestOrderID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.ID != receiptID || !stored.HasFinalizedTerminalTransaction() {
				t.Fatal("receipt identity or proof lost")
			}
			if tc.invalid && stored.ReceiptHash == oldHash {
				t.Fatal("invalid receipt not repaired")
			}
			if !tc.invalid && stored.ReceiptHash != oldHash {
				t.Fatal("valid receipt rewritten")
			}
			firstHash := stored.ReceiptHash
			firstTime := stored.CreatedAt
			// A newly generated timestamp/hash must not rewrite the repaired row again.
			candidate.CreatedAt = candidate.CreatedAt.Add(time.Hour)
			candidate.Payload = append(candidate.Payload[:len(candidate.Payload)-1], []byte(`,"extra":"replay"}`)...)
			candidate.ReceiptHash, err = shareddomain.CanonicalJSONHashBytes(candidate.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.CreateReceipt(ctx, candidate); err != nil {
				t.Fatal(err)
			}
			stored, err = repo.GetReceipt(ctx, giwaTestUserID, giwaTestOrderID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.ReceiptHash != firstHash || !stored.CreatedAt.Equal(firstTime) {
				t.Fatal("replay rewrote finalized receipt")
			}
			var issued int
			if err := f.database.DB.QueryRowContext(ctx, `SELECT count(*) FROM order_process_events WHERE type=$1 AND (payload->>'terminalTxFinalized')::boolean`, procmsg.EventReceiptIssued).Scan(&issued); err != nil {
				t.Fatal(err)
			}
			if issued != 1 {
				t.Fatalf("receipt event duplicated: %d", issued)
			}
			if before != snapshot() {
				t.Fatal("receipt repair changed economic or order evidence")
			}
		})
	}
}
