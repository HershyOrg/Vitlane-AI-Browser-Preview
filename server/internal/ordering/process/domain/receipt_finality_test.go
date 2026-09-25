package domain

import (
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"testing"
)

func TestReceiptEffectWaitsForGIWASettlementFinality(t *testing.T) {
	for _, existing := range []bool{false, true} {
		w := withUnit(withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "PLACED", UnitCount: 1}), "unit-1", "mo-1", "DELIVERED_EXPECTED")
		w.Rail = "GIWA"
		w.ReceiptIssued = existing
		if existing {
			w.ReceiptState = string(TerminalReasonCompletedAll)
		}
		p, d := reduceForTest(t, runningProcess(w), reducerEvent(1, procmsg.EventSettlementStateChanged, procmsg.SettlementStateChangedPayload{State: "COMPLETION_SUBMITTED"}, string(procmsg.SourceSettlement)))
		if d.State != StateTerminal || d.TerminalReason != TerminalReasonCompletedAll || hasEffect(d, procmsg.EffectIssueReceipt) {
			t.Fatalf("physical completion issued premature receipt: %+v", d)
		}
		p, d = reduceForTest(t, p, reducerEvent(2, procmsg.EventSettlementStateChanged, procmsg.SettlementStateChangedPayload{State: "COMPLETED"}, string(procmsg.SourceSettlement)))
		effect := onlyEffect(t, d, procmsg.EffectIssueReceipt)
		oldKey := procmsg.EffectIdempotencyKey(procmsg.EffectIssueReceipt, p.AgencyOrderID, string(TerminalReasonCompletedAll))
		if effect.IdempotencyKey == oldKey {
			t.Fatal("premature receipt repair reuses completed effect key")
		}
		_, d = reduceForTest(t, p, reducerEvent(3, procmsg.EventReceiptIssued, procmsg.ReceiptIssuedPayload{ReceiptID: "receipt", TerminalState: string(TerminalReasonCompletedAll), TerminalTxFinalized: true}, string(procmsg.SourceAgencyOrder)))
		if !d.ProcessState.ReceiptTerminalTxFinalized || hasEffect(d, procmsg.EffectIssueReceipt) {
			t.Fatal("finalized receipt did not converge")
		}
	}
}

func TestPayPalAndCompensatedGIWAReceiptEligibility(t *testing.T) {
	for _, rail := range []string{"GIWA", "PAYPAL"} {
		w := withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "CANCELLED", Compensation: MOCompensationFold{ID: "compensation", State: "SUCCEEDED", Action: "REFUND"}})
		w.Rail = rail
		d := projectDecisionForTest(runningProcess(w), nil, reducerNow)
		if d.TerminalReason != TerminalReasonRefundedAll {
			t.Fatalf("refund terminal changed: %+v", d)
		}
		onlyEffect(t, d, procmsg.EffectIssueReceipt)
	}
	w := withUnit(withMO(fundedProcessState(), "mo-1", MOState{OwnerState: "PLACED", UnitCount: 1}), "unit-1", "mo-1", "DELIVERED_EXPECTED")
	w.Rail = "PAYPAL"
	onlyEffect(t, projectDecisionForTest(runningProcess(w), nil, reducerNow), procmsg.EffectIssueReceipt)
}
