package domain

import (
	"slices"
	"testing"
	"time"
)

func actionProjection() Projection {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	return Projection{
		AgencyOrder: AgencyOrder{ID: "order-1", IssuedAt: now.Add(-time.Hour)},
		PaymentInstruction: PaymentInstruction{
			State: "PENDING", ExpiresAt: now.Add(10 * time.Minute),
			PaymentSelection: PaymentSelection{Rail: "PAYPAL"},
		},
		Process: Process{State: ProcessWaitingCustomerPayment},
	}
}

func kinds(actions []CustomerAction) []CustomerActionKind {
	out := make([]CustomerActionKind, 0, len(actions))
	for _, action := range actions {
		out = append(out, action.Kind)
	}
	return out
}

func TestCustomerActionsPayWindowClosesAtAuthorization(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	projection := actionProjection()
	actions := AvailableCustomerActions(projection, now)
	if !slices.Equal(kinds(actions), []CustomerActionKind{ActionPay}) || actions[0].Rail != "PAYPAL" {
		t.Fatalf("actions=%+v", actions)
	}
	projection.Payment = &PaymentProjection{Rail: "PAYPAL", State: "AUTHORIZED"}
	if actions := AvailableCustomerActions(projection, now); len(actions) != 0 {
		t.Fatalf("authorized payment must close PAY, got %+v", actions)
	}
}

func TestCustomerActionsCancelOnlyEligibleMerchantOrder(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	projection := actionProjection()
	projection.Payment = &PaymentProjection{Rail: "PAYPAL", State: "PARTIALLY_CAPTURED"}
	projection.Process.State = ProcessProcurementInProgress
	projection.MerchantOrders = []MerchantOrderSummary{
		{ID: "mo-1", State: "PLACED", FundingState: "ACTIVE"},
		{ID: "mo-2", State: "PLANNED", FundingState: "AVAILABLE"},
		{ID: "mo-3", State: "PLANNED", CancellationState: "REQUESTED"},
	}
	actions := AvailableCustomerActions(projection, now)
	if !slices.Equal(kinds(actions), []CustomerActionKind{ActionCancelPreEffect, ActionRequestRefund}) {
		t.Fatalf("actions=%+v", actions)
	}
	if !slices.Equal(actions[0].EligibleMerchantOrderIDs, []string{"mo-2"}) {
		t.Fatalf("cancel eligibility=%v", actions[0].EligibleMerchantOrderIDs)
	}
	if !slices.Equal(actions[1].EligibleMerchantOrderIDs, []string{"mo-1"}) {
		t.Fatalf("refund eligibility=%v", actions[1].EligibleMerchantOrderIDs)
	}
}

func TestCustomerActionsRequestRefundUsesWholeMO(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	projection := actionProjection()
	projection.Payment = &PaymentProjection{State: "COMPLETED"}
	projection.Process.State = ProcessTerminal
	projection.Process.TerminalReason = TerminalReasonCompletedAll
	projection.MerchantOrders = []MerchantOrderSummary{
		{ID: "mo-1", AllocationID: "allocation-1", State: "PLACED"},
		{ID: "mo-2", AllocationID: "allocation-2", State: "FAILED", RefundRequestState: "REQUESTED"},
		{ID: "mo-3", AllocationID: "allocation-3", State: "PLACED", CompensationState: "SUCCEEDED"},
		{ID: "mo-4", AllocationID: "allocation-4", State: "FAILED"},
	}
	actions := AvailableCustomerActions(projection, now)
	if !slices.Equal(kinds(actions), []CustomerActionKind{ActionRequestRefund}) ||
		!slices.Equal(actions[0].EligibleMerchantOrderIDs, []string{"mo-1"}) {
		t.Fatalf("whole-MO refund action=%+v", actions)
	}
	if slices.Contains(actions[0].ReasonCodes, "CHANGE_OF_MIND") || len(actions[0].ReasonCodes) == 0 {
		t.Fatalf("reason codes=%v", actions[0].ReasonCodes)
	}
}

func TestCustomerActionsDelayRuleNamesMerchantOrders(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	projection := actionProjection()
	projection.Payment = &PaymentProjection{Rail: "PAYPAL", State: "CAPTURED"}
	projection.Process.State = ProcessLogisticsInProgress
	projection.AgencyOrder.IssuedAt = now.Add(-31 * 24 * time.Hour)
	projection.MerchantOrders = []MerchantOrderSummary{
		{ID: "mo-1", State: "PLACED"}, {ID: "mo-2", State: "CANCELLED"},
	}
	actions := AvailableCustomerActions(projection, now)
	if !slices.Equal(kinds(actions), []CustomerActionKind{ActionCancelDelayRule, ActionRequestRefund}) ||
		!slices.Equal(actions[0].EligibleMerchantOrderIDs, []string{"mo-1"}) {
		t.Fatalf("delay action=%+v", actions)
	}
}

func TestCustomerActionsResumeConsumedGIWAInstructionWithoutReopeningSubmittedPayment(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	for _, state := range []string{"", "AUTHORIZED", "AWAITING_ALLOWANCE", "FAILED", "PAYMENT_SUBMITTED", "SUBMISSION_UNKNOWN", "SAFE", "FINALIZED", "REFUNDED"} {
		t.Run(state, func(t *testing.T) {
			projection := actionProjection()
			projection.PaymentInstruction.State = "CONSUMED"
			projection.PaymentInstruction.PaymentSelection.Rail = "GIWA"
			if state != "" {
				projection.Payment = &PaymentProjection{Rail: "GIWA", State: state}
			}
			actions := AvailableCustomerActions(projection, now)
			want := state == "" || state == "AUTHORIZED" || state == "AWAITING_ALLOWANCE" || state == "FAILED"
			if slices.Contains(kinds(actions), ActionPay) != want {
				t.Fatalf("state=%s actions=%+v", state, actions)
			}
			projection.PaymentInstruction.ExpiresAt = now
			if slices.Contains(kinds(AvailableCustomerActions(projection, now)), ActionPay) {
				t.Fatal("expired instruction resumed")
			}
		})
	}
}
