package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// ConsumeInstructionClaim runs only under AgencyOrder's inbox transaction. The
// unique order claim makes duplicate delivery converge and rejects a different
// payment identity even when it carries the same order and approval snapshot.
func (r *Repository) ConsumeInstructionClaim(ctx context.Context, claim procmsg.InstructionClaim) (bool, error) {
	if !sharedapp.InTransaction(ctx) || claim.Validate() != nil {
		return false, procmsg.ErrEffectInvalid
	}
	if err := procmsg.RequireExecution(ctx, "", procmsg.EffectConsumeInstruction); err != nil {
		return false, err
	}
	var state, rail, snapshot string
	var valid bool
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT state,rail,agency_order_snapshot_hash,expires_at>$2 FROM agency_order_payment_instructions WHERE agency_order_id=$1 FOR UPDATE`, claim.AgencyOrderID, claim.ValidAt).Scan(&state, &rail, &snapshot, &valid)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var raw []byte
	err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT claim FROM agency_order_instruction_claims WHERE agency_order_id=$1`, claim.AgencyOrderID).Scan(&raw)
	if err == nil {
		var original procmsg.InstructionClaim
		if json.Unmarshal(raw, &original) != nil {
			return false, procmsg.ErrEffectInvalid
		}
		return original == claim, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if state != "PENDING" || !valid || rail != claim.Rail || snapshot != claim.SnapshotHash {
		return false, nil
	}
	raw, err = json.Marshal(claim)
	if err != nil {
		return false, err
	}
	if _, err = r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO agency_order_instruction_claims(agency_order_id,claim_id,claim) VALUES($1,$2,$3)`, claim.AgencyOrderID, claim.ID, raw); err != nil {
		return false, err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `UPDATE agency_order_payment_instructions SET state='CONSUMED' WHERE agency_order_id=$1`, claim.AgencyOrderID)
	return err == nil, err
}
