package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	paymentdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// InstructionGate owns Payment's local confirmation. It has no AgencyOrder
// application dependency and never writes an AgencyOrder-owned table.
type InstructionGate struct {
	database *sharedpostgres.Database
	wake     func()
}

func NewInstructionGate(db *sharedpostgres.Database, wake func()) *InstructionGate {
	return &InstructionGate{database: db, wake: wake}
}

func (g *InstructionGate) ConfirmInstruction(ctx context.Context, claim procmsg.InstructionClaim) error {
	if sharedapp.InTransaction(ctx) {
		return procmsg.ErrEffectInvalid
	}
	claim.ID = procmsg.EffectIdentity(claim.AgencyOrderID, "instruction:"+claim.Rail+":"+claim.ReferenceID)
	if err := claim.Validate(); err != nil {
		return err
	}
	var outcome string
	err := g.database.WithinTransaction(ctx, func(tx context.Context) error {
		tx, flush := procmsg.CollectEvents(tx, g.database.Queryer(tx))
		raw, err := json.Marshal(claim)
		if err != nil {
			return err
		}
		_, err = g.database.Queryer(tx).ExecContext(tx, `INSERT INTO payment_instruction_claims(id,agency_order_id,claim,state) VALUES($1,$2,$3,'PENDING') ON CONFLICT(id) DO NOTHING`, claim.ID, claim.AgencyOrderID, raw)
		if err != nil {
			return err
		}
		var stored []byte
		if err = g.database.Queryer(tx).QueryRowContext(tx, `SELECT claim,state FROM payment_instruction_claims WHERE id=$1 FOR UPDATE`, claim.ID).Scan(&stored, &outcome); err != nil {
			return err
		}
		var original procmsg.InstructionClaim
		if json.Unmarshal(stored, &original) != nil || original.AgencyOrderID != claim.AgencyOrderID || original.Rail != claim.Rail || original.ReferenceID != claim.ReferenceID || original.SnapshotHash != claim.SnapshotHash {
			return paymentdomain.ErrInstructionMismatch
		}
		// Retrying never moves the first validated timestamp past expiry.
		if outcome == "PENDING" {
			_, err = procmsg.AppendEvent(tx, g.database.Queryer(tx), procmsg.ProcessEvent{AgencyOrderID: claim.AgencyOrderID, Source: procmsg.SourcePayment, Type: procmsg.EventInstructionClaimed, Payload: original, DedupKey: "instruction-claim:" + claim.ID}, time.Now().UTC())
			if err != nil {
				return err
			}
		}
		return flush()
	})
	if err != nil {
		return err
	}
	if g.wake != nil {
		g.wake()
	}
	switch outcome {
	case "CONFIRMED":
		return nil
	case "REJECTED":
		return paymentdomain.ErrInstructionNotConsumable
	default:
		return procmsg.ErrInstructionPending
	}
}

func (g *InstructionGate) RecordInstructionConfirmation(ctx context.Context, value procmsg.InstructionConfirmation) error {
	if !sharedapp.InTransaction(ctx) || value.Claim.Validate() != nil {
		return procmsg.ErrEffectInvalid
	}
	if err := procmsg.RequireExecution(ctx, "", procmsg.EffectConfirmInstruction); err != nil {
		return err
	}
	var raw []byte
	var state string
	if err := g.database.Queryer(ctx).QueryRowContext(ctx, `SELECT claim,state FROM payment_instruction_claims WHERE id=$1 AND agency_order_id=$2 FOR UPDATE`, value.Claim.ID, value.Claim.AgencyOrderID).Scan(&raw, &state); err != nil {
		return err
	}
	var original procmsg.InstructionClaim
	if json.Unmarshal(raw, &original) != nil || original != value.Claim {
		return paymentdomain.ErrInstructionMismatch
	}
	next := "REJECTED"
	if value.Confirmed {
		next = "CONFIRMED"
	}
	if state != "PENDING" {
		if state == next {
			return nil
		}
		return errors.New("PAYMENT_INSTRUCTION_CONFIRMATION_CONFLICT")
	}
	_, err := g.database.Queryer(ctx).ExecContext(ctx, `UPDATE payment_instruction_claims SET state=$2,reason=$3,confirmed_at=clock_timestamp() WHERE id=$1`, value.Claim.ID, next, value.Reason)
	return err
}
