package postgres_test

import (
	"context"
	"errors"
	paymentdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	paymentpg "github.com/vitlane/vitlane/server/internal/ordering/payment/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"github.com/vitlane/vitlane/server/internal/ordering/testfixture"
	"testing"
	"time"
)

func seedInstruction(t *testing.T, h *reducerHarness) {
	testfixture.Exec(t, h.ctx, h.db, `INSERT INTO agency_order_payment_instructions(id,agency_order_id,user_id,agency_order_snapshot_hash,amount_minor,currency,rail,asset,state,payload,expires_at,created_at,provider_environment,economic_effect,merchant_execution_mode,execution_profile_hash)
 SELECT '11111111-1111-4111-8111-111111111111',id,user_id,snapshot_hash,customer_payable_minor,currency,payment_rail,asset,'PENDING','{}',expires_at,issued_at,provider_environment,economic_effect,merchant_execution_mode,execution_profile_hash FROM agency_orders WHERE id=$1`, testfixture.OrderID)
}
func instructionClaim(h *reducerHarness) procmsg.InstructionClaim {
	return procmsg.InstructionClaim{AgencyOrderID: testfixture.OrderID, Rail: "PAYPAL", ReferenceID: testfixture.PaymentID, SnapshotHash: "failed-funding-order-hash", ValidAt: h.clock.Time}
}
func TestReducerInstructionConfirmationSurvivesRestartAndDuplicateDelivery(t *testing.T) {
	h := newReducerHarness(t)
	seedInstruction(t, h)
	wakeCount := 0
	gate := paymentpg.NewInstructionGate(h.db, func() { wakeCount++ })
	claim := instructionClaim(h)
	if err := gate.ConfirmInstruction(h.ctx, claim); !errors.Is(err, procmsg.ErrInstructionPending) {
		t.Fatalf("confirmation before Owner: %v", err)
	}
	if wakeCount != 1 {
		t.Fatal("missing postcommit wake")
	}
	if _, err := h.db.DB.ExecContext(h.ctx, `UPDATE payment_instruction_claims SET claim=jsonb_set(claim,'{snapshotHash}','"changed"')`); err == nil {
		t.Fatal("claim payload could be changed after publication")
	}

	h.reduce(t)
	// Queue processing and transport completion alone do not authorize Payment.
	h.deliver(t, "AGENCYORDER")
	restarted := paymentpg.NewInstructionGate(h.db, nil)
	if err := restarted.ConfirmInstruction(h.ctx, claim); !errors.Is(err, procmsg.ErrInstructionPending) {
		t.Fatalf("AgencyOrder ACK bypassed Payment confirmation: %v", err)
	}
	h.reduce(t)
	h.deliver(t, "PAYMENT")
	h.reduce(t)
	claim.ValidAt = claim.ValidAt.Add(time.Second)
	if err := restarted.ConfirmInstruction(h.ctx, claim); err != nil {
		t.Fatal(err)
	}
	var events, consumptions int
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT COUNT(*) FROM order_process_events WHERE type=$1`, procmsg.EventInstructionClaimed).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT COUNT(*) FROM agency_order_instruction_claims`).Scan(&consumptions); err != nil {
		t.Fatal(err)
	}
	if events != 1 || consumptions != 1 {
		t.Fatalf("replay emitted another claim: events=%d consumes=%d", events, consumptions)
	}
	claim.ReferenceID = "different-payment"
	if err := gate.ConfirmInstruction(h.ctx, claim); !errors.Is(err, procmsg.ErrInstructionPending) {
		t.Fatal(err)
	}
	h.reduce(t)
	h.deliver(t, "AGENCYORDER")
	h.reduce(t)
	h.deliver(t, "PAYMENT")
	h.reduce(t)
	if err := gate.ConfirmInstruction(h.ctx, claim); !errors.Is(err, paymentdomain.ErrInstructionNotConsumable) {
		t.Fatalf("second payment consumed one-time instruction: %v", err)
	}
}
func TestReducerInstructionRejectsExpiredOrMismatchedClaims(t *testing.T) {
	for _, kind := range []string{"expired", "snapshot", "rail"} {
		t.Run(kind, func(t *testing.T) {
			h := newReducerHarness(t)
			seedInstruction(t, h)
			claim := instructionClaim(h)
			switch kind {
			case "expired":
				claim.ValidAt = claim.ValidAt.Add(time.Hour)
			case "snapshot":
				claim.SnapshotHash = "wrong"
			case "rail":
				claim.Rail = "GIWA"
			}
			gate := paymentpg.NewInstructionGate(h.db, nil)
			if err := gate.ConfirmInstruction(h.ctx, claim); !errors.Is(err, procmsg.ErrInstructionPending) {
				t.Fatal(err)
			}
			h.reduce(t)
			h.deliver(t, "AGENCYORDER")
			h.reduce(t)
			h.deliver(t, "PAYMENT")
			h.reduce(t)
			if err := gate.ConfirmInstruction(h.ctx, claim); !errors.Is(err, paymentdomain.ErrInstructionNotConsumable) {
				t.Fatalf("invalid claim admitted: %v", err)
			}
			var state string
			if err := h.db.DB.QueryRowContext(h.ctx, `SELECT state FROM agency_order_payment_instructions`).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != "PENDING" {
				t.Fatalf("invalid claim consumed instruction: %s", state)
			}
		})
	}
}
func TestReducerInstructionCannotStartInsideOwnerTransaction(t *testing.T) {
	h := newReducerHarness(t)
	gate := paymentpg.NewInstructionGate(h.db, nil)
	err := h.db.WithinTransaction(h.ctx, func(tx context.Context) error { return gate.ConfirmInstruction(tx, instructionClaim(h)) })
	if !errors.Is(err, procmsg.ErrEffectInvalid) {
		t.Fatalf("ambient Owner TX accepted: %v", err)
	}
}
