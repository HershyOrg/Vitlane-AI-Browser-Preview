package domain

import "testing"

func TestMerchantOrderOperationalProjectionKeepsSiblingOutcomesIndependent(t *testing.T) {
	refunded := DeriveMerchantOrderOperationalProjection(MerchantOrderOperationalFacts{
		MerchantOrderState: "PLACED", TaskState: "SUCCEEDED", FundingState: "RELEASED",
		ResolutionCause: "MISSING", ResolutionDecision: "REFUND",
		CompensationAction: "REFUND", CompensationState: "SUCCEEDED",
		Units: MerchantOrderUnitCounts{Total: 1, Refunded: 1},
	})
	shipping := DeriveMerchantOrderOperationalProjection(MerchantOrderOperationalFacts{
		MerchantOrderState: "PLACED", TaskState: "SUCCEEDED", FundingState: "ACTIVE",
		Units: MerchantOrderUnitCounts{Total: 1, AwaitingShipment: 1},
	})

	if refunded.Stage != MOStageRefunded || refunded.WorkStage != MOWorkDone ||
		refunded.TerminalReason != "REFUNDED" {
		t.Fatalf("refunded projection=%+v", refunded)
	}
	if shipping.Stage != MOStageAwaitingShipment || shipping.WorkStage != MOWorkLogistics {
		t.Fatalf("shipping projection=%+v", shipping)
	}
}

func TestMerchantOrderOperationalProjectionUsesExactMOException(t *testing.T) {
	issue := DeriveMerchantOrderOperationalProjection(MerchantOrderOperationalFacts{
		MerchantOrderState: "PLACED", TaskState: "SUCCEEDED", FundingState: "ACTIVE",
		Units: MerchantOrderUnitCounts{Total: 2, Delivered: 1, Exception: 1},
	})
	if issue.Stage != MOStageDeliveryException || issue.WorkStage != MOWorkIssue {
		t.Fatalf("issue projection=%+v", issue)
	}
}

func TestMerchantOrderOperationalProjectionTreatsRefundDecisionAsPendingBeforeCompensation(t *testing.T) {
	pending := DeriveMerchantOrderOperationalProjection(MerchantOrderOperationalFacts{
		MerchantOrderState: "PLACED", TaskState: "SUCCEEDED", FundingState: "ACTIVE",
		ResolutionCause: "WRONG_ACTUAL", ResolutionDecision: "REFUND",
		Units: MerchantOrderUnitCounts{Total: 1, RefundPending: 1},
	})
	if pending.Stage != MOStageCompensationPending || pending.WorkStage != MOWorkIssue {
		t.Fatalf("pending projection=%+v", pending)
	}
}

func TestMerchantOrderOperationalProjectionDoesNotTreatRegisteredPrePurchaseUnitsAsShipping(t *testing.T) {
	pending := DeriveMerchantOrderOperationalProjection(MerchantOrderOperationalFacts{
		MerchantOrderState: "PLANNED", TaskState: "QUEUED", FundingState: "AVAILABLE",
		Units: MerchantOrderUnitCounts{Total: 1, AwaitingShipment: 1},
	})
	if pending.Stage != MOStageProcurementPending || pending.WorkStage != MOWorkProcurement {
		t.Fatalf("pre-purchase projection=%+v", pending)
	}
}

func TestMerchantOrderOperationalProjectionScopesPayPalDisputeToExactMO(t *testing.T) {
	disputed := DeriveMerchantOrderOperationalProjection(MerchantOrderOperationalFacts{
		MerchantOrderState: "PLACED", FundingState: "ACTIVE", DisputeState: "OPEN",
		Units: MerchantOrderUnitCounts{Total: 1, Delivered: 1},
	})
	sibling := DeriveMerchantOrderOperationalProjection(MerchantOrderOperationalFacts{
		MerchantOrderState: "PLACED", FundingState: "ACTIVE",
		Units: MerchantOrderUnitCounts{Total: 1, InTransit: 1},
	})
	if disputed.Stage != MOStagePayPalDispute || disputed.WorkStage != MOWorkIssue {
		t.Fatalf("disputed projection=%+v", disputed)
	}
	if sibling.Stage != MOStageInTransit || sibling.WorkStage != MOWorkLogistics {
		t.Fatalf("sibling projection=%+v", sibling)
	}
}

func TestUnitRefundDecisionDoesNotFlashDeliveredBeforeCompensation(t *testing.T) {
	facts := UnitFacts{
		MerchantOrderState: "PLACED", Fulfillment: "RESOLVED",
		ResolutionCause: "MISSING", ResolutionDecision: "REFUND",
	}
	if stage := DeriveUnitStage("AVAILABLE", facts); stage != UnitRefundPending {
		t.Fatalf("stage=%s", stage)
	}
}
