package procmsg

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExecutionRequiresExactImmutableEffectScope(t *testing.T) {
	for _, scope := range []ExecutionScope{{}, {AgencyOrderID: "order", MerchantOrderID: "mo", FlowID: "flow", Action: EffectEnsureMOFunding}, {AgencyOrderID: "order", MerchantOrderID: "other", FlowID: "flow", EffectID: "effect", Action: EffectEnsureMOFunding}, {AgencyOrderID: "order", MerchantOrderID: "mo", FlowID: "flow", EffectID: "effect", Action: EffectReleasePurchase}} {
		if !errors.Is(RequireExecution(WithExecutionScope(context.Background(), scope), "mo", EffectEnsureMOFunding), ErrExecutionRequired) {
			t.Fatalf("invalid scope accepted: %+v", scope)
		}
	}
}
func TestOnlyAuthoritativeOwnerMayPublishBusinessFact(t *testing.T) {
	for _, source := range []Source{SourceCustomer, SourceOperator, SourceProcurement, SourceSystem} {
		_, err := AppendEvent(context.Background(), nil, ProcessEvent{AgencyOrderID: "order", Source: source, Type: EventMOCompensationStateChanged, DedupKey: "forged", Payload: map[string]any{"state": "SUCCEEDED"}}, time.Now())
		if !errors.Is(err, ErrEventInvalid) {
			t.Fatalf("source %s invented payment fact", source)
		}
	}
}
