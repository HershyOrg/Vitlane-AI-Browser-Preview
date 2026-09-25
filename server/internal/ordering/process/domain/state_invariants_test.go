package domain

import (
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"testing"
)

func TestProcessStateRejectOlderEntityVersionWithoutAffectingSibling(t *testing.T) {
	w := ProcessState{}
	effects := eventEffects{}
	for _, tc := range []struct {
		mo, state string
		version   int64
	}{{"mo-a", "PLACED", 4}, {"mo-b", "PLANNED", 1}, {"mo-a", "PLANNED", 2}, {"mo-a", "FAILED", 4}} {
		e := processEvent(t, tc.version, procmsg.EventMerchantOrderStateChanged, procmsg.MerchantOrderStateChangedPayload{MerchantOrderID: tc.mo, AllocationID: tc.mo + "-allocation", State: tc.state, UnitCount: 1})
		e.EntityKey = "merchant_order:" + tc.mo
		e.SourceEntityVersion = tc.version
		if err := applyEvent(&w, &effects, e); err != nil {
			t.Fatal(err)
		}
	}
	if w.MerchantOrders["mo-a"].OwnerState != "PLACED" || w.MerchantOrders["mo-b"].OwnerState != "PLANNED" || len(effects.compensationTriggers) != 0 {
		t.Fatalf("stale event regressed facts %+v", w)
	}
}
