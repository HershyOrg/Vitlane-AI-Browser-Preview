package domain

// Physical units remain logistics identities only. Money and refund
// eligibility live at the MerchantOrder allocation boundary.

type UnitStage string

const (
	UnitOrdered           UnitStage = "ORDERED"
	UnitProcuring         UnitStage = "PROCURING"
	UnitProcurementFailed UnitStage = "PROCUREMENT_FAILED"
	UnitCancelled         UnitStage = "CANCELLED"
	UnitAwaitingShipment  UnitStage = "AWAITING_SHIPMENT"
	UnitInTransit         UnitStage = "IN_TRANSIT"
	UnitDelivered         UnitStage = "DELIVERED"
	UnitException         UnitStage = "EXCEPTION"
	UnitReturnInProgress  UnitStage = "RETURN_IN_PROGRESS"
	UnitRefundRequested   UnitStage = "REFUND_REQUESTED"
	UnitRefundPending     UnitStage = "REFUND_PENDING"
	UnitRefunded          UnitStage = "REFUNDED"
)

type UnitShipmentRef struct {
	ID          string `json:"id"`
	Carrier     string `json:"carrier"`
	TrackingRef string `json:"trackingRef"`
	State       string `json:"state"`
}

type UnitView struct {
	MerchantOrderUnitID string           `json:"merchantOrderUnitId"`
	MerchantOrderID     string           `json:"merchantOrderId"`
	AllocationID        string           `json:"allocationId"`
	LineID              string           `json:"lineId"`
	UnitIndex           int              `json:"unitIndex"`
	ShopDomain          string           `json:"shopDomain"`
	Stage               UnitStage        `json:"stage"`
	RefundStatus        string           `json:"refundStatus"`
	Shipment            *UnitShipmentRef `json:"shipment,omitempty"`
	ReturnState         string           `json:"returnState,omitempty"`
	ResolutionCause     string           `json:"resolutionCause,omitempty"`
	ResolutionDecision  string           `json:"resolutionDecision,omitempty"`
}

type UnitFacts struct {
	MerchantOrderUnitID string
	MerchantOrderID     string
	AllocationID        string
	LineID              string
	UnitIndex           int
	ShopDomain          string
	RefundStatus        string
	MerchantOrderState  string
	Fulfillment         string
	ReturnState         string
	ResolutionCause     string
	ResolutionDecision  string
	Shipment            *UnitShipmentRef
}

var openReturnStates = map[string]bool{
	"REQUESTED": true, "RETURN_IN_TRANSIT": true,
	"RECEIVED": true, "MERCHANT_RETURNED": true,
}

func DeriveUnitStage(refundStatus string, facts UnitFacts) UnitStage {
	switch refundStatus {
	case "REQUESTED":
		return UnitRefundRequested
	case "REFUND_PENDING":
		return UnitRefundPending
	case "REFUNDED":
		return UnitRefunded
	}
	// Delivery REFUND is already a committed whole-MO obligation even during
	// the short handoff before Payment creates its compensation row.
	if facts.ResolutionDecision == "REFUND" {
		return UnitRefundPending
	}
	if openReturnStates[facts.ReturnState] {
		return UnitReturnInProgress
	}
	if facts.Fulfillment != "" {
		switch facts.Fulfillment {
		case "AWAITING_EFFECT":
			return UnitAwaitingShipment
		case "IN_TRANSIT_EXPECTED":
			return UnitInTransit
		case "DELIVERED_EXPECTED", "RESOLVED", "NONCONFORMING_RESOLVED", "SIMULATED_NO_EFFECT":
			return UnitDelivered
		case "RETURNED":
			return UnitReturnInProgress
		case "SUPERSEDED_BY_CANCELLATION":
			return UnitCancelled
		case "NO_PLACEMENT":
			return UnitProcurementFailed
		default:
			return UnitException
		}
	}
	switch facts.MerchantOrderState {
	case "", "PLANNED":
		return UnitOrdered
	case "FAILED":
		return UnitProcurementFailed
	case "CANCELLED":
		return UnitCancelled
	default:
		return UnitProcuring
	}
}

func ComposeUnitViews(facts []UnitFacts) []UnitView {
	units := make([]UnitView, 0, len(facts))
	for _, fact := range facts {
		view := UnitView{
			MerchantOrderUnitID: fact.MerchantOrderUnitID,
			MerchantOrderID:     fact.MerchantOrderID, AllocationID: fact.AllocationID,
			LineID: fact.LineID, UnitIndex: fact.UnitIndex, ShopDomain: fact.ShopDomain,
			Stage: DeriveUnitStage(fact.RefundStatus, fact), RefundStatus: fact.RefundStatus,
			Shipment: fact.Shipment, ReturnState: fact.ReturnState,
			ResolutionCause: fact.ResolutionCause, ResolutionDecision: fact.ResolutionDecision,
		}
		units = append(units, view)
	}
	return units
}
