package domain

// MerchantOrderOperationalStage is the canonical customer/operator read-model
// state for one shop checkout. It is derived from owner facts and is never
// persisted as another source of truth.
type MerchantOrderOperationalStage string

const (
	MOStageProcurementPending  MerchantOrderOperationalStage = "PROCUREMENT_PENDING"
	MOStageProcurementActive   MerchantOrderOperationalStage = "PROCUREMENT_ACTIVE"
	MOStageAwaitingShipment    MerchantOrderOperationalStage = "AWAITING_SHIPMENT"
	MOStageInTransit           MerchantOrderOperationalStage = "IN_TRANSIT"
	MOStageDeliveryException   MerchantOrderOperationalStage = "DELIVERY_EXCEPTION"
	MOStageRefundReview        MerchantOrderOperationalStage = "REFUND_REVIEW"
	MOStageReturnInProgress    MerchantOrderOperationalStage = "RETURN_IN_PROGRESS"
	MOStageCompensationPending MerchantOrderOperationalStage = "COMPENSATION_PENDING"
	MOStagePayPalDispute       MerchantOrderOperationalStage = "PAYPAL_DISPUTE"
	MOStageAttentionRequired   MerchantOrderOperationalStage = "ATTENTION_REQUIRED"
	MOStageDelivered           MerchantOrderOperationalStage = "DELIVERED"
	MOStageRefunded            MerchantOrderOperationalStage = "REFUNDED"
	MOStageProcurementFailed   MerchantOrderOperationalStage = "PROCUREMENT_FAILED"
	MOStageCancelled           MerchantOrderOperationalStage = "CANCELLED"
)

// MerchantOrderWorkStage is the server-owned partition used by the operator
// filters. Web must not reconstruct this value from AgencyOrderProcess or the
// individual owner states.
type MerchantOrderWorkStage string

const (
	MOWorkProcurement MerchantOrderWorkStage = "PROCUREMENT"
	MOWorkLogistics   MerchantOrderWorkStage = "LOGISTICS"
	MOWorkIssue       MerchantOrderWorkStage = "ISSUE"
	MOWorkDone        MerchantOrderWorkStage = "DONE"
)

type MerchantOrderProgressState string

const (
	MOProgressWaiting MerchantOrderProgressState = "WAITING"
	MOProgressCurrent MerchantOrderProgressState = "CURRENT"
	MOProgressDone    MerchantOrderProgressState = "DONE"
	MOProgressIssue   MerchantOrderProgressState = "ISSUE"
)

type MerchantOrderProgress struct {
	Funding     MerchantOrderProgressState `json:"funding"`
	Procurement MerchantOrderProgressState `json:"procurement"`
	Delivery    MerchantOrderProgressState `json:"delivery"`
	Resolution  MerchantOrderProgressState `json:"resolution"`
}

type MerchantOrderUnitCounts struct {
	Total             int `json:"total"`
	Ordered           int `json:"ordered"`
	Procuring         int `json:"procuring"`
	AwaitingShipment  int `json:"awaitingShipment"`
	InTransit         int `json:"inTransit"`
	Delivered         int `json:"delivered"`
	Exception         int `json:"exception"`
	ReturnInProgress  int `json:"returnInProgress"`
	RefundRequested   int `json:"refundRequested"`
	RefundPending     int `json:"refundPending"`
	Refunded          int `json:"refunded"`
	ProcurementFailed int `json:"procurementFailed"`
	Cancelled         int `json:"cancelled"`
}

type MerchantOrderOperationalProjection struct {
	Stage              MerchantOrderOperationalStage `json:"stage"`
	WorkStage          MerchantOrderWorkStage        `json:"workStage"`
	TerminalReason     string                        `json:"terminalReason,omitempty"`
	Progress           MerchantOrderProgress         `json:"progress"`
	Units              MerchantOrderUnitCounts       `json:"units"`
	ResolutionCause    string                        `json:"resolutionCause,omitempty"`
	ResolutionDecision string                        `json:"resolutionDecision,omitempty"`
	ReturnState        string                        `json:"returnState,omitempty"`
	CompensationAction string                        `json:"compensationAction,omitempty"`
	CompensationState  string                        `json:"compensationState,omitempty"`
	DisputeState       string                        `json:"disputeState,omitempty"`
	DisputeOutcome     string                        `json:"disputeOutcome,omitempty"`
}

type MerchantOrderOperationalFacts struct {
	MerchantOrderState string
	TaskState          string
	FundingState       string
	RefundRequestState string
	CancellationState  string
	ResolutionCause    string
	ResolutionDecision string
	ReturnState        string
	CompensationAction string
	CompensationState  string
	DisputeState       string
	DisputeOutcome     string
	Units              MerchantOrderUnitCounts
}

func CountMerchantOrderUnitStages(units []UnitView, merchantOrderID string) MerchantOrderUnitCounts {
	counts := MerchantOrderUnitCounts{}
	for _, unit := range units {
		if unit.MerchantOrderID != merchantOrderID {
			continue
		}
		counts.Total++
		switch unit.Stage {
		case UnitOrdered:
			counts.Ordered++
		case UnitProcuring:
			counts.Procuring++
		case UnitAwaitingShipment:
			counts.AwaitingShipment++
		case UnitInTransit:
			counts.InTransit++
		case UnitDelivered:
			counts.Delivered++
		case UnitException:
			counts.Exception++
		case UnitReturnInProgress:
			counts.ReturnInProgress++
		case UnitRefundRequested:
			counts.RefundRequested++
		case UnitRefundPending:
			counts.RefundPending++
		case UnitRefunded:
			counts.Refunded++
		case UnitProcurementFailed:
			counts.ProcurementFailed++
		case UnitCancelled:
			counts.Cancelled++
		}
	}
	return counts
}

func DeriveMerchantOrderOperationalProjection(
	facts MerchantOrderOperationalFacts,
) MerchantOrderOperationalProjection {
	result := MerchantOrderOperationalProjection{
		Units:              facts.Units,
		ResolutionCause:    facts.ResolutionCause,
		ResolutionDecision: facts.ResolutionDecision,
		ReturnState:        facts.ReturnState,
		CompensationAction: facts.CompensationAction,
		CompensationState:  facts.CompensationState,
		DisputeState:       facts.DisputeState,
		DisputeOutcome:     facts.DisputeOutcome,
		Progress: MerchantOrderProgress{
			Funding:     fundingProgress(facts.FundingState),
			Procurement: MOProgressWaiting,
			Delivery:    MOProgressWaiting,
			Resolution:  MOProgressWaiting,
		},
	}

	if facts.DisputeState == "OPEN" ||
		(facts.DisputeState == "RESOLVED" && blockingDisputeOutcomes[facts.DisputeOutcome]) {
		result.Stage, result.WorkStage = MOStagePayPalDispute, MOWorkIssue
		result.Progress.Procurement = completedProcurementProgress(facts.MerchantOrderState)
		result.Progress.Resolution = MOProgressIssue
		return result
	}

	// Money terminal facts outrank physical package state. A refunded MO never
	// re-enters Logistics merely because its historical Shipment is DELIVERED.
	if (facts.CompensationState == "SUCCEEDED" &&
		(facts.CompensationAction == "REFUND" || facts.CompensationAction == "TVIT_REFUND")) ||
		(facts.Units.Total > 0 && facts.Units.Refunded == facts.Units.Total) {
		result.Stage, result.WorkStage, result.TerminalReason = MOStageRefunded, MOWorkDone, "REFUNDED"
		result.Progress.Procurement = completedProcurementProgress(facts.MerchantOrderState)
		result.Progress.Delivery, result.Progress.Resolution = MOProgressDone, MOProgressDone
		return result
	}

	if facts.CompensationState == "OUTCOME_UNKNOWN" || facts.CompensationState == "FAILED" ||
		facts.FundingState == "ACTIVATION_UNKNOWN" || facts.FundingState == "RELEASE_UNKNOWN" ||
		facts.FundingState == "FAILED" || facts.MerchantOrderState == "PLACEMENT_UNKNOWN" ||
		facts.TaskState == "OUTCOME_UNKNOWN" {
		result.Stage, result.WorkStage = MOStageAttentionRequired, MOWorkIssue
		result.Progress.Procurement, result.Progress.Resolution = MOProgressIssue, MOProgressIssue
		return result
	}

	if facts.CompensationState == "APPROVED" || facts.CompensationState == "EXECUTION_PENDING" ||
		facts.ResolutionDecision == "REFUND" || facts.Units.RefundPending > 0 {
		result.Stage, result.WorkStage = MOStageCompensationPending, MOWorkIssue
		result.Progress.Procurement = completedProcurementProgress(facts.MerchantOrderState)
		result.Progress.Delivery, result.Progress.Resolution = MOProgressDone, MOProgressCurrent
		return result
	}

	if facts.RefundRequestState == "REQUESTED" || facts.RefundRequestState == "REVIEWING" ||
		facts.Units.RefundRequested > 0 {
		result.Stage, result.WorkStage = MOStageRefundReview, MOWorkIssue
		result.Progress.Procurement = completedProcurementProgress(facts.MerchantOrderState)
		result.Progress.Resolution = MOProgressCurrent
		return result
	}

	if facts.Units.ReturnInProgress > 0 || openReturnStates[facts.ReturnState] {
		result.Stage, result.WorkStage = MOStageReturnInProgress, MOWorkIssue
		result.Progress.Procurement, result.Progress.Delivery = MOProgressDone, MOProgressDone
		result.Progress.Resolution = MOProgressCurrent
		return result
	}

	if facts.Units.Exception > 0 {
		result.Stage, result.WorkStage = MOStageDeliveryException, MOWorkIssue
		result.Progress.Procurement, result.Progress.Delivery = MOProgressDone, MOProgressIssue
		result.Progress.Resolution = MOProgressCurrent
		return result
	}

	if facts.MerchantOrderState == "CANCELLED" || facts.TaskState == "CANCELLED" ||
		facts.Units.Total > 0 && facts.Units.Cancelled == facts.Units.Total {
		result.Stage, result.WorkStage, result.TerminalReason = MOStageCancelled, MOWorkDone, "CANCELLED"
		result.Progress.Procurement, result.Progress.Delivery, result.Progress.Resolution = MOProgressDone, MOProgressDone, MOProgressDone
		return result
	}

	if facts.MerchantOrderState == "FAILED" || facts.TaskState == "FAILED" ||
		facts.Units.Total > 0 && facts.Units.ProcurementFailed == facts.Units.Total {
		result.Stage, result.WorkStage, result.TerminalReason = MOStageProcurementFailed, MOWorkDone, "PROCUREMENT_FAILED"
		result.Progress.Procurement, result.Progress.Delivery, result.Progress.Resolution = MOProgressIssue, MOProgressDone, MOProgressDone
		return result
	}

	if facts.Units.Total > 0 && facts.Units.Delivered == facts.Units.Total {
		result.Stage, result.WorkStage, result.TerminalReason = MOStageDelivered, MOWorkDone, "DELIVERED"
		result.Progress.Procurement, result.Progress.Delivery, result.Progress.Resolution = MOProgressDone, MOProgressDone, MOProgressDone
		return result
	}

	if facts.Units.InTransit > 0 {
		result.Stage, result.WorkStage = MOStageInTransit, MOWorkLogistics
		result.Progress.Procurement, result.Progress.Delivery = MOProgressDone, MOProgressCurrent
		return result
	}

	if facts.MerchantOrderState == "PLACED" || facts.TaskState == "SUCCEEDED" {
		result.Stage, result.WorkStage = MOStageAwaitingShipment, MOWorkLogistics
		result.Progress.Procurement, result.Progress.Delivery = MOProgressDone, MOProgressCurrent
		return result
	}

	if facts.TaskState == "CLAIMED" || facts.TaskState == "IN_PROGRESS" ||
		facts.MerchantOrderState == "READY_TO_PLACE" || facts.MerchantOrderState == "PLACEMENT_PENDING" {
		result.Stage, result.WorkStage = MOStageProcurementActive, MOWorkProcurement
		result.Progress.Procurement = MOProgressCurrent
		return result
	}

	result.Stage, result.WorkStage = MOStageProcurementPending, MOWorkProcurement
	return result
}

var blockingDisputeOutcomes = map[string]bool{
	"RESOLVED_BUYER_FAVOUR": true,
	"RESOLVED_WITH_PAYOUT":  true,
	"ACCEPTED":              true,
}

func fundingProgress(state string) MerchantOrderProgressState {
	switch state {
	case "ACTIVE", "RELEASED":
		return MOProgressDone
	case "AVAILABLE", "ACTIVATION_PENDING", "RELEASE_PENDING":
		return MOProgressCurrent
	case "ACTIVATION_UNKNOWN", "RELEASE_UNKNOWN", "FAILED":
		return MOProgressIssue
	default:
		return MOProgressWaiting
	}
}

func completedProcurementProgress(state string) MerchantOrderProgressState {
	if state == "PLACED" {
		return MOProgressDone
	}
	if state == "FAILED" || state == "CANCELLED" || state == "PLACEMENT_UNKNOWN" {
		return MOProgressIssue
	}
	return MOProgressCurrent
}
