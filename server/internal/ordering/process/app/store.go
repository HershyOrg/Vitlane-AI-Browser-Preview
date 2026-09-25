package app

import (
	"github.com/vitlane/vitlane/server/internal/ordering/process/domain"
	"time"
)

type EffectWrite struct {
	AgencyOrderID string
	domain.EffectDraft
}
type DecisionInput struct {
	Process domain.Process
	Event   *domain.Event
}
type DueTimer struct {
	AgencyOrderID string
	At            time.Time
}
type DecisionOutcome struct {
	Applied                              bool
	EventSeq                             int64
	StageChanged                         bool
	State                                domain.State
	TerminalReason                       domain.TerminalReason
	LastReasonCode                       string
	EffectsIssued, MerchantOrdersChanged int
}
