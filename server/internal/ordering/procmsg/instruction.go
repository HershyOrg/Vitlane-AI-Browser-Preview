package procmsg

import (
	"errors"
	"time"
)

const (
	EventInstructionClaimed  = "payment.instruction.claimed.v1"
	EffectConsumeInstruction = "agency-order.consume_instruction.v1"
	EffectConfirmInstruction = "payment.confirm_instruction.v1"
)

var ErrInstructionPending = errors.New("PAYMENT_INSTRUCTION_CONFIRMATION_PENDING")

// InstructionClaim identifies one immutable payment authorization. ValidAt is
// the verified provider authorization time, or the first GIWA signing request.
// Neither a queue ACK nor a second payment identity may consume the instruction.
type InstructionClaim struct {
	ID            string    `json:"claimId"`
	AgencyOrderID string    `json:"agencyOrderId"`
	Rail          string    `json:"rail"`
	ReferenceID   string    `json:"referenceId"`
	SnapshotHash  string    `json:"snapshotHash"`
	ValidAt       time.Time `json:"validAt"`
}

func (c InstructionClaim) Validate() error {
	if c.ID == "" || c.AgencyOrderID == "" || c.ReferenceID == "" || c.SnapshotHash == "" || c.ValidAt.IsZero() || (c.Rail != "PAYPAL" && c.Rail != "GIWA") {
		return ErrEventInvalid
	}
	return nil
}

type InstructionConfirmation struct {
	Claim     InstructionClaim `json:"claim"`
	Confirmed bool             `json:"confirmed"`
	Reason    string           `json:"reason,omitempty"`
}
