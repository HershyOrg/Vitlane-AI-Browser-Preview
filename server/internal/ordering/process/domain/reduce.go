package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

var (
	ErrEventInvalid = errors.New("ORDER_PROCESS_EVENT_INVALID")
)

// EffectExpectation records a decision and its observed business outcome. It has
// no delivery claim, worker, attempt, lease, provider payload or execution step.
// Keeping resolved identities makes repeated facts unable to reissue an effect.
type EffectExpectation struct {
	ID               string                    `json:"effectId"`
	Type             string                    `json:"type"`
	Target           string                    `json:"target"`
	MerchantOrderID  string                    `json:"merchantOrderId,omitempty"`
	Request          procmsg.ActionRequest     `json:"request"`
	Outcome          string                    `json:"outcome,omitempty"`
	Reason           string                    `json:"reason,omitempty"`
	IssuedAt         time.Time                 `json:"issuedAt"`
	InstructionClaim *procmsg.InstructionClaim `json:"instructionClaim,omitempty"`
}

func (e EffectExpectation) Pending() bool {
	return e.Outcome != "SUCCEEDED" && e.Outcome != "REJECTED"
}

type PurchaseIntent struct {
	Released              bool                  `json:"released,omitempty"`
	Request               procmsg.ActionRequest `json:"request"`
	ReservationID         string                `json:"reservationId,omitempty"`
	RegistrationConfirmed bool                  `json:"registrationConfirmed,omitempty"`
	FundingConfirmed      bool                  `json:"fundingConfirmed,omitempty"`
	MerchantGrantID       string                `json:"merchantGrantId,omitempty"`
}

type CancellationIntent struct {
	Request            procmsg.ActionRequest `json:"request"`
	ReservationID      string                `json:"reservationId,omitempty"`
	UndeliveredUnits   int                   `json:"undeliveredUnits"`
	LogisticsConfirmed bool                  `json:"logisticsConfirmed"`
}

func receipt(r procmsg.ActionRequest, outcome, reason, waiting string) procmsg.RequestReceipt {
	g := procmsg.Guidance{ReasonCode: reason, WaitingFor: waiting, CustomerAction: "WAIT", OperatorAction: "WAIT"}
	switch {
	case reason == "CANCELLED_DELIVERY_REQUIRES_REVIEW":
		g.CustomerAction, g.OperatorAction = "CONTACT_SUPPORT", "REVIEW_RETURN"
	case outcome == "COMPLETED":
		g.CustomerAction, g.OperatorAction = "VIEW_RESULT", "VIEW_RESULT"
	case reason == "PAYMENT_OUTCOME_UNKNOWN":
		g.OperatorAction = "RECONCILE_PAYMENT"
	case reason == "MONEY_GATE_CLOSED":
		g.OperatorAction = "CHECK_PAYMENT_GATE"
	case reason == "MERCHANT_RESULT_REQUIRED":
		g.OperatorAction = "RECORD_MERCHANT_RESULT"
	case reason == "ASSIGNMENT_REQUIRED" || reason == "PROCUREMENT_ASSIGNMENT_REQUIRED" || reason == "PROCUREMENT_LEASE_EXPIRED":
		g.OperatorAction = "CLAIM_TASK"
	case reason == "PROCUREMENT_MANUAL_DECISION_REQUIRED":
		g.OperatorAction = "RECORD_PURCHASE_CONDITIONS"
	case reason == "CUSTOMER_DECISION_REQUIRED" || reason == "PROCUREMENT_CUSTOMER_REQUEST_OPEN":
		g.CustomerAction, g.OperatorAction = "RESPOND_TO_REQUEST", "WAIT_CUSTOMER_RESPONSE"
	case reason == "PROCUREMENT_PII_ACCESS_DENIED":
		g.OperatorAction = "CHECK_ASSIGNMENT_AND_REVEAL"
	case outcome == "REJECTED":
		g.CustomerAction, g.OperatorAction = "REVIEW_STATE", "REVIEW_STATE"
	}
	return procmsg.RequestReceipt{SchemaVersion: "vitlane.order-process-receipt.v1", AgencyOrderID: r.AgencyOrderID, RequestID: r.ID, FlowID: procmsg.RequestFlowID(r.AgencyOrderID, r.ID), MerchantOrderID: r.MerchantOrderID, Kind: r.Kind, Outcome: outcome, Guidance: g}
}

// Reduce consumes one durable event and returns one decision without I/O.
// Its effects become pending expectations before a later event can be reduced.
func Reduce(p Process, e Event, now time.Time) (Decision, error) {
	if e.Type == "" {
		return Decision{}, ErrEventInvalid
	}
	d := Decision{State: p.State, TerminalReason: p.TerminalReason, LastReasonCode: p.LastReasonCode, ProcessState: cloneProcessState(p.ProcessState)}
	if p.Version == 0 {
		d.State = StateWaitingCustomerPayment
	}
	var drafts []EffectDraft
	switch e.Type {
	case procmsg.EventInstructionClaimed:
		claim, err := procmsg.ParsePayload[procmsg.InstructionClaim](e.Payload)
		if err != nil || claim.Validate() != nil || claim.AgencyOrderID != p.AgencyOrderID || e.Source != string(procmsg.SourcePayment) {
			return Decision{}, ErrEventInvalid
		}
		drafts = []EffectDraft{{Type: procmsg.EffectConsumeInstruction, Target: string(procmsg.TargetAgencyOrder), Payload: claim, IdempotencyKey: "instruction:" + claim.ID}}
	case procmsg.EventActionRequested:
		r, err := procmsg.ParsePayload[procmsg.ActionRequest](e.Payload)
		if err != nil || r.Validate() != nil || r.AgencyOrderID != p.AgencyOrderID || r.ActorRole != e.Source {
			return Decision{}, ErrEventInvalid
		}
		drafts = reduceRequest(&d, r, e, now)
	case procmsg.EventEffectReported:
		r, err := procmsg.ParsePayload[procmsg.EffectReport](e.Payload)
		if err != nil {
			return Decision{}, ErrEventInvalid
		}
		var errReport error
		drafts, errReport = reduceEffectReport(&d, p.AgencyOrderID, r, e, now)
		if errReport != nil {
			return Decision{}, errReport
		}
	default:
		part, err := reduceObservedFact(p, e, pendingEffects(d.ProcessState), now)
		if err != nil {
			return Decision{}, err
		}
		d.State, d.TerminalReason, d.LastReasonCode, d.WakeAt = part.State, part.TerminalReason, part.LastReasonCode, part.WakeAt
		d.ProcessState = part.ProcessState
		drafts = part.Effects
	}
	for _, draft := range drafts {
		registerEffect(&d, p.AgencyOrderID, draft, now)
	}
	resumeCompensations(&d, p.AgencyOrderID, e.ID, now)
	finishObservedPurchases(&d)
	finishObservedCancellations(&d)
	updateLateDeliveryGuidance(&d, p.AgencyOrderID, e)
	resumeRequests(&d, p.AgencyOrderID, now)

	// Project after registering expectations and resuming deferred requests.
	// Queue ACK state never participates in this business projection.
	projection := p
	projection.State, projection.TerminalReason, projection.LastReasonCode = d.State, d.TerminalReason, d.LastReasonCode
	projection.ProcessState = d.ProcessState
	part := deriveDecision(projection, cloneProcessState(d.ProcessState), eventEffects{}, pendingEffects(d.ProcessState), now)
	d.State, d.TerminalReason, d.LastReasonCode, d.WakeAt = part.State, part.TerminalReason, part.LastReasonCode, part.WakeAt
	d.ProcessState = part.ProcessState
	for _, draft := range part.Effects {
		registerEffect(&d, p.AgencyOrderID, draft, now)
	}
	// Every newly issued effect, including resumed work and derived notices,
	// belongs to this event's decision. Replayed identities stay deduplicated.
	for i := range d.Effects {
		d.Effects[i].CausedByEventID = e.ID
	}
	d.MerchantOrders = d.ProcessState.merchantOrderDecisions(p.ProcessState)
	d.StageChanged = p.State != d.State || p.TerminalReason != d.TerminalReason || p.LastReasonCode != d.LastReasonCode
	return d, nil
}

func requestOwner(kind procmsg.RequestKind) procmsg.Target {
	switch kind {
	case procmsg.RequestCreateShipment, procmsg.RequestShipmentEvent, procmsg.RequestConfirmDelivery, procmsg.RequestResolveDelivery, procmsg.RequestCreateReturn, procmsg.RequestUpdateReturn:
		return procmsg.TargetLogistics
	case procmsg.RequestRefund, procmsg.RequestRefundDecision:
		return procmsg.TargetAgencyOrder
	default:
		return procmsg.TargetProcurement
	}
}

func reduceRequest(d *Decision, r procmsg.ActionRequest, e Event, now time.Time) []EffectDraft {
	refuse := func(reason string) []EffectDraft {
		d.Receipts = append(d.Receipts, receipt(r, "REJECTED", reason, ""))
		return nil
	}
	if r.Kind == procmsg.RequestRetryEffect {
		if r.ActorRole != "OPERATOR" {
			return refuse("OPERATOR_REQUIRED")
		}
		_, expected, found := findExpectation(&d.ProcessState, r.ReferenceID, r.MerchantOrderID)
		if !found || !expected.Pending() {
			return refuse("EFFECT_ALREADY_RESOLVED")
		}
		d.Receipts = append(d.Receipts, receipt(r, "ACCEPTED", "OWNER_RETRY_PENDING", expected.Type))
		return []EffectDraft{{Type: procmsg.EffectRetryDelivery, Target: expected.Target, Payload: r, IdempotencyKey: "retry:" + r.ID, CausedByEventID: e.ID}}
	}
	f := d.ProcessState.MerchantOrders[r.MerchantOrderID]
	if f == nil || f.OwnerState == "" {
		return refuse("MERCHANT_ORDER_UNKNOWN")
	}
	if r.ActorRole == "CUSTOMER" && r.ActorID != d.ProcessState.UserID {
		return refuse("ORDER_ACCESS_DENIED")
	}
	requiredRole := "OPERATOR"
	switch r.Kind {
	case procmsg.RequestCancel, procmsg.RequestRefund, procmsg.RequestCustomerResponse:
		requiredRole = "CUSTOMER"
	}
	if r.ActorRole != requiredRole {
		return refuse(requiredRole + "_REQUIRED")
	}
	if r.AllocationID != "" && r.AllocationID != f.AllocationID {
		return refuse("ORDER_SCOPE_MISMATCH")
	}
	if r.TaskID != "" && f.TaskID != "" && r.TaskID != f.TaskID {
		return refuse("ORDER_SCOPE_MISMATCH")
	}
	r.AllocationID = f.AllocationID
	if r.TaskID == "" {
		r.TaskID = f.TaskID
	}
	busy := f.Purchase != nil || f.Cancellation != nil || hasBlockingEffect(f)
	switch r.Kind {
	case procmsg.RequestPurchase:
		if r.ActorRole != "OPERATOR" {
			return refuse("OPERATOR_REQUIRED")
		}
		if f.Purchase != nil {
			waiting, reason := purchaseWaitingFor(f), "PROCESS_PENDING"
			if waiting == "MERCHANT_RESULT" {
				reason = "MERCHANT_RESULT_REQUIRED"
			}
			if f.Purchase.Released {
				reason = "COMPENSATION_PENDING"
			}
			for _, expected := range f.Effects {
				if expected.Pending() && expected.Type == waiting && expected.Request.ID == f.Purchase.Request.ID {
					if expected.Outcome == "EFFECT_UNKNOWN" {
						reason = "PAYMENT_OUTCOME_UNKNOWN"
					} else if expected.Reason != "" {
						reason = expected.Reason
					}
				}
			}
			result := receipt(f.Purchase.Request, "WAITING", reason, waiting)
			result.RequestID = r.ID
			d.Receipts = append(d.Receipts, result)
			return nil
		}
		if busy {
			return refuse("EFFECT_IN_PROGRESS")
		}
		if !preEffectCancellable(f) {
			return refuse("PURCHASE_NOT_AVAILABLE")
		}
		if f.RefundRequestState == "REQUESTED" || f.RefundDecision == "APPROVED" || f.OpenRequests() > 0 {
			return refuse("CUSTOMER_DECISION_REQUIRED")
		}
		if d.ProcessState.Authorization.Hash == "" || r.TaskID == "" || f.FundingPositionID == "" {
			return refuse("ORDER_CONTEXT_NOT_READY")
		}
		f.Purchase = &PurchaseIntent{Request: r}
		d.Receipts = append(d.Receipts, receipt(r, "ACCEPTED", "PURCHASE_PREPARATION_PENDING", procmsg.EffectReservePurchase))
		return []EffectDraft{purchaseEffect(d.ProcessState, f, procmsg.EffectReservePurchase, string(procmsg.TargetProcurement), e.ID)}
	case procmsg.RequestCancel:
		if r.ActorRole != "CUSTOMER" {
			return refuse("CUSTOMER_REQUIRED")
		}
		if r.CancelKind == "DELAY_RULE" && (d.ProcessState.IssuedAt == nil || now.Before(d.ProcessState.IssuedAt.Add(time.Duration(DelayRuleWindowDays)*24*time.Hour))) {
			return refuse("DELAY_RULE_NOT_DUE")
		}
		if busy {
			if r.CancelKind != "DELAY_RULE" {
				return refuse("EFFECT_IN_PROGRESS")
			}
			for _, pending := range f.PendingRequests {
				if pending.Kind == r.Kind && pending.CancelKind == r.CancelKind {
					x := receipt(pending, "DEFERRED", "EFFECT_IN_PROGRESS", purchaseWaitingFor(f))
					x.RequestID = r.ID
					d.Receipts = append(d.Receipts, x)
					return nil
				}
			}
			f.PendingRequests = append(f.PendingRequests, r)
			d.Receipts = append(d.Receipts, receipt(r, "DEFERRED", "EFFECT_IN_PROGRESS", purchaseWaitingFor(f)))
			return nil
		}
		if r.CancelKind == "PRE_EFFECT" && !preEffectCancellable(f) {
			return refuse("CANCEL_NOT_AVAILABLE")
		}
		if f.CompensationCause != "" || f.Compensation.State != "" || f.CancellationKind != "" {
			return refuse("CANCEL_ALREADY_RESOLVING")
		}
		f.Cancellation = &CancellationIntent{Request: r}
		f.Intent = &MOIntent{RequestID: r.ID, Kind: r.CancelKind, Seq: e.Seq, Outcome: IntentOutcomeEffectIssued, UserID: r.ActorID}
		d.Receipts = append(d.Receipts, receipt(r, "ACCEPTED", "CANCELLATION_ELIGIBILITY_PENDING", procmsg.EffectReserveCancellation))
		return []EffectDraft{cancellationEffect(d.ProcessState, f, procmsg.EffectReserveCancellation, string(procmsg.TargetLogistics), e.ID)}
	case procmsg.RequestShipmentEvent, procmsg.RequestConfirmDelivery, procmsg.RequestCreateReturn, procmsg.RequestUpdateReturn:
		// Physical observations remain recordable while cancellation/refund is pending.
	case procmsg.RequestCreateShipment:
		if busy {
			return refuse("EFFECT_IN_PROGRESS")
		}
		if f.OwnerState != "PLACED" || f.CancellationKind != "" {
			return refuse("SHIPMENT_NOT_AVAILABLE")
		}
	case procmsg.RequestResolveDelivery:
		if busy {
			return refuse("EFFECT_IN_PROGRESS")
		}
	case procmsg.RequestClaimTask, procmsg.RequestPlacementEvidence, procmsg.RequestFailureEvidence, procmsg.RequestRevealShipping, procmsg.RequestRevealCheckout:
		// These inputs can finish or take responsibility for existing work. Owner
		// assignment, grant and evidence validation still apply at execution time.
		if (r.Kind == procmsg.RequestPlacementEvidence || r.Kind == procmsg.RequestFailureEvidence) && f.LockState != "STARTED" && (f.Purchase == nil || f.Purchase.MerchantGrantID == "") {
			return refuse("MERCHANT_PERMISSION_REQUIRED")
		}
	case procmsg.RequestManualDecision:
		if busy && !(r.Decision == "UNABLE_TO_PURCHASE" && f.Purchase != nil && f.Purchase.MerchantGrantID != "") {
			return refuse("EFFECT_IN_PROGRESS")
		}
		if !busy && f.OwnerState != "PLANNED" {
			return refuse("PURCHASE_NOT_AVAILABLE")
		}
	case procmsg.RequestRefund, procmsg.RequestRefundDecision:
		if f.CancellationKind != "" {
			return refuse("CANCEL_ALREADY_RESOLVING")
		}
		if busy {
			return refuse("EFFECT_IN_PROGRESS")
		}
	default:
		if busy {
			return refuse("EFFECT_IN_PROGRESS")
		}
		if f.OwnerState != "PLANNED" {
			return refuse("PURCHASE_NOT_AVAILABLE")
		}
	}
	d.Receipts = append(d.Receipts, receipt(r, "ACCEPTED", "OWNER_RESULT_PENDING", string(r.Kind)))
	return []EffectDraft{{Type: procmsg.EffectApplyOwnerAction, Target: string(requestOwner(r.Kind)), Payload: procmsg.OwnerActionContext{ActionRequest: r, MerchantState: f.OwnerState, FundingState: f.FundingState, HasCompensation: f.CompensationCause != "" || f.Compensation.State != "", UnitIDs: append([]string(nil), f.UnitIDs...)}, IdempotencyKey: "request:" + r.ID + ":" + string(r.Kind), CausedByEventID: e.ID}}
}

func purchaseEffect(s ProcessState, f *MOState, kind, target string, cause int64) EffectDraft {
	r := f.Purchase.Request
	a := s.Authorization
	p := procmsg.PurchaseContext{RequestID: r.ID, ActorID: r.ActorID, TaskID: r.TaskID, MerchantOrderID: r.MerchantOrderID, AllocationID: f.AllocationID, AuthorizationKind: a.Kind, AuthorizationHash: a.Hash, ExecutionProfileHash: a.ExecutionProfileHash, ExecutionMode: a.ExecutionMode, FundingPositionID: f.FundingPositionID, FundingState: f.FundingState, UnitIDs: append([]string(nil), f.UnitIDs...)}
	return EffectDraft{Type: kind, Target: target, Payload: p, IdempotencyKey: "purchase:" + r.ID + ":" + kind, CausedByEventID: cause}
}

func cancellationEffect(s ProcessState, f *MOState, kind, target string, cause int64) EffectDraft {
	c := f.Cancellation
	p := procmsg.CancellationContext{ActionRequest: c.Request, UserID: s.UserID, FundingState: f.FundingState, OwnerState: f.OwnerState, UnitIDs: append([]string(nil), f.UnitIDs...), ReservationID: c.ReservationID, UndeliveredUnits: c.UndeliveredUnits}
	if s.IssuedAt != nil {
		p.IssuedAt = *s.IssuedAt
	}
	return EffectDraft{Type: kind, Target: target, Payload: p, IdempotencyKey: "cancel:" + c.Request.ID + ":" + kind, CausedByEventID: cause}
}

func registerEffect(d *Decision, order string, draft EffectDraft, now time.Time) {
	id := procmsg.EffectIdentity(order, draft.IdempotencyKey)
	raw, _ := json.Marshal(draft.Payload)
	var scope struct {
		MerchantOrderID string `json:"merchantOrderId"`
		RequestID       string `json:"requestId"`
	}
	_ = json.Unmarshal(raw, &scope)
	expect := EffectExpectation{ID: id, Type: draft.Type, Target: draft.Target, MerchantOrderID: scope.MerchantOrderID, IssuedAt: now}
	if draft.Type == procmsg.EffectConsumeInstruction {
		var claim procmsg.InstructionClaim
		_ = json.Unmarshal(raw, &claim)
		expect.InstructionClaim = &claim
	}
	collection := &d.ProcessState.Effects
	if scope.MerchantOrderID != "" {
		f := d.ProcessState.mo(scope.MerchantOrderID)
		if draft.Type == procmsg.EffectCompensateMO && hasUnresolvedPurchasePhase(f) {
			return
		}
		collection = &f.Effects
		if f.Purchase != nil && f.Purchase.Request.ID == scope.RequestID {
			expect.Request = f.Purchase.Request
		}
		if f.Cancellation != nil && f.Cancellation.Request.ID == scope.RequestID {
			expect.Request = f.Cancellation.Request
		}
	}
	if expect.Request.ID == "" && scope.RequestID != "" {
		_ = json.Unmarshal(raw, &expect.Request)
	}
	if *collection == nil {
		*collection = map[string]EffectExpectation{}
	}
	if _, exists := (*collection)[id]; exists {
		return
	}
	(*collection)[id] = expect
	d.Effects = append(d.Effects, draft)
}

func findExpectation(s *ProcessState, id, mo string) (*map[string]EffectExpectation, EffectExpectation, bool) {
	if mo == "" {
		e, ok := s.Effects[id]
		return &s.Effects, e, ok
	}
	f := s.MerchantOrders[mo]
	if f == nil {
		return nil, EffectExpectation{}, false
	}
	e, ok := f.Effects[id]
	return &f.Effects, e, ok
}

func reduceEffectReport(d *Decision, order string, report procmsg.EffectReport, event Event, now time.Time) ([]EffectDraft, error) {
	collection, expect, ok := findExpectation(&d.ProcessState, report.EffectID, report.MerchantOrderID)
	if !ok || expect.Type != report.EffectType || expect.Request.ID != report.RequestID || event.Source != expect.Target {
		return nil, ErrEventInvalid
	}
	if !expect.Pending() {
		return nil, nil
	}
	switch report.Outcome {
	case "SUCCEEDED", "REJECTED", "WAITING", "EFFECT_UNKNOWN", "ATTENTION_REQUIRED":
	default:
		return nil, ErrEventInvalid
	}
	expect.Outcome, expect.Reason = report.Outcome, report.Code
	(*collection)[expect.ID] = expect
	if expect.Type == procmsg.EffectConsumeInstruction && report.Outcome == "SUCCEEDED" {
		value, err := procmsg.ParsePayload[procmsg.InstructionConfirmation](report.Result)
		if err != nil || expect.InstructionClaim == nil || value.Claim != *expect.InstructionClaim {
			return nil, ErrEventInvalid
		}
		return []EffectDraft{{Type: procmsg.EffectConfirmInstruction, Target: string(procmsg.TargetPayment), Payload: value, IdempotencyKey: "instruction-confirm:" + value.Claim.ID, CausedByEventID: event.ID}}, nil
	}
	if expect.Target == string(procmsg.TargetSupport) {
		if d.ProcessState.CommunicationAttention == nil {
			d.ProcessState.CommunicationAttention = map[string]string{}
		}
		if report.Outcome == "ATTENTION_REQUIRED" {
			d.ProcessState.CommunicationAttention[expect.ID] = report.Code
		} else if report.Outcome == "SUCCEEDED" || report.Outcome == "REJECTED" {
			delete(d.ProcessState.CommunicationAttention, expect.ID)
		}
		if expect.Request.ID != "" {
			outcome := "WAITING"
			if report.Outcome == "SUCCEEDED" {
				outcome = "COMPLETED"
			}
			if report.Outcome == "REJECTED" {
				outcome = "REJECTED"
			}
			d.Receipts = append(d.Receipts, receipt(expect.Request, outcome, report.Code, expect.Type))
		}
		return nil, nil
	}
	if f := d.ProcessState.MerchantOrders[report.MerchantOrderID]; f != nil {
		if report.Outcome == "ATTENTION_REQUIRED" || report.Outcome == "EFFECT_UNKNOWN" {
			f.Attention = &MOAttention{Code: report.Code, EffectID: expect.ID, EffectType: expect.Type}
		} else if f.Attention != nil && f.Attention.EffectID == expect.ID {
			f.Attention = nil
		}
	}
	if report.MerchantOrderID == "" {
		if expect.Request.ID != "" {
			outcome := "WAITING"
			if report.Outcome == "SUCCEEDED" {
				outcome = "COMPLETED"
			}
			if report.Outcome == "REJECTED" {
				outcome = "REJECTED"
			}
			d.Receipts = append(d.Receipts, receipt(expect.Request, outcome, report.Code, expect.Type))
		}
		return nil, nil
	}
	f := d.ProcessState.MerchantOrders[report.MerchantOrderID]
	if report.Outcome == "EFFECT_UNKNOWN" {
		d.Receipts = append(d.Receipts, receipt(expect.Request, "WAITING", "PAYMENT_OUTCOME_UNKNOWN", expect.Type))
		return nil, nil
	}
	if report.Outcome == "WAITING" || report.Outcome == "ATTENTION_REQUIRED" {
		d.Receipts = append(d.Receipts, receipt(expect.Request, "WAITING", report.Code, expect.Type))
		return nil, nil
	}
	if report.Outcome == "REJECTED" {
		d.Receipts = append(d.Receipts, receipt(expect.Request, "REJECTED", report.Code, ""))
		if f.Purchase != nil && expect.Request.ID == f.Purchase.Request.ID {
			if f.Purchase.MerchantGrantID != "" {
				return nil, ErrEventInvalid
			}
			if f.Purchase.ReservationID == "" {
				f.Purchase = nil
				return nil, nil
			}
			return []EffectDraft{purchaseEffect(d.ProcessState, f, procmsg.EffectReleasePurchase, string(procmsg.TargetProcurement), event.ID)}, nil
		}
		if f.Cancellation != nil && expect.Request.ID == f.Cancellation.Request.ID {
			if expect.Type == procmsg.EffectReserveCancellation {
				f.Cancellation = nil
				if f.Intent != nil {
					f.Intent.Outcome, f.Intent.Code = IntentOutcomeRejected, report.Code
				}
				return nil, nil
			}
			return []EffectDraft{cancellationEffect(d.ProcessState, f, procmsg.EffectReleaseCancellation, string(procmsg.TargetLogistics), event.ID)}, nil
		}
		return nil, nil
	}
	switch expect.Type {
	case procmsg.EffectReserveCancellation:
		result, err := procmsg.ParsePayload[procmsg.CancellationReservation](report.Result)
		if err != nil || f.Cancellation == nil || result.ReservationID != expect.ID || result.UndeliveredUnits < 0 {
			return nil, ErrEventInvalid
		}
		f.Cancellation.ReservationID, f.Cancellation.UndeliveredUnits = result.ReservationID, result.UndeliveredUnits
		kind := procmsg.EffectCancelPrePurchase
		if expect.Request.CancelKind == "DELAY_RULE" {
			kind = procmsg.EffectCancelByDelayRule
		}
		return []EffectDraft{cancellationEffect(d.ProcessState, f, kind, string(procmsg.TargetProcurement), event.ID)}, nil
	case procmsg.EffectReleaseCancellation:
		f.Cancellation = nil
		if f.Intent != nil {
			f.Intent.Outcome = IntentOutcomeRejected
		}
	case procmsg.EffectApplyLogisticsCancellation:
		if f.Cancellation != nil {
			f.Cancellation.LogisticsConfirmed = true
		}
	case procmsg.EffectReservePurchase:
		if f.Purchase == nil {
			return nil, ErrEventInvalid
		}
		f.Purchase.ReservationID = expect.ID
		if purchaseStopped(f) {
			return releasePurchase(d, f, event.ID), nil
		}
		return []EffectDraft{purchaseEffect(d.ProcessState, f, procmsg.EffectConfirmPurchaseRegistration, string(procmsg.TargetLogistics), event.ID)}, nil
	case procmsg.EffectConfirmPurchaseRegistration:
		if f.Purchase == nil {
			return nil, ErrEventInvalid
		}
		f.Purchase.RegistrationConfirmed = true
		if purchaseStopped(f) {
			return releasePurchase(d, f, event.ID), nil
		}
		return []EffectDraft{purchaseEffect(d.ProcessState, f, procmsg.EffectEnsureMOFunding, string(procmsg.TargetPayment), event.ID)}, nil
	case procmsg.EffectEnsureMOFunding:
		if f.Purchase == nil {
			return nil, ErrEventInvalid
		}
		result, err := procmsg.ParsePayload[procmsg.FundingResult](report.Result)
		if err != nil || result.State != "ACTIVE" || result.PositionID == "" || result.PositionID != f.FundingPositionID {
			return nil, ErrEventInvalid
		}
		f.FundingPositionID, f.FundingState = result.PositionID, result.State
		f.Purchase.FundingConfirmed = true
		if purchaseStopped(f) {
			return releasePurchase(d, f, event.ID), nil
		}
		return []EffectDraft{purchaseEffect(d.ProcessState, f, procmsg.EffectGrantMerchantPurchase, string(procmsg.TargetProcurement), event.ID)}, nil
	case procmsg.EffectGrantMerchantPurchase:
		if f.Purchase == nil {
			return nil, ErrEventInvalid
		}
		f.Purchase.MerchantGrantID = expect.ID
		d.Receipts = append(d.Receipts, receipt(f.Purchase.Request, "WAITING", "MERCHANT_RESULT_REQUIRED", "MERCHANT_RESULT"))
	case procmsg.EffectReleasePurchase:
		f.CompensationCause = procmsg.CompensationCauseProcurementFailure
		request := f.Purchase.Request
		f.Purchase.Released = true
		d.Receipts = append(d.Receipts, receipt(request, "WAITING", "COMPENSATION_PENDING", procmsg.EffectCompensateMO))
		return []EffectDraft{{Type: procmsg.EffectCompensateMO, Target: string(procmsg.TargetPayment), Payload: procmsg.ExecuteMOCompensationPayload{MerchantOrderID: report.MerchantOrderID, AllocationID: f.AllocationID, Cause: procmsg.CompensationCauseProcurementFailure}, IdempotencyKey: procmsg.EffectIdempotencyKey(procmsg.EffectCompensateMO, order, "merchant-order:"+report.MerchantOrderID), CausedByEventID: event.ID}}, nil
	case procmsg.EffectCancelPrePurchase, procmsg.EffectCancelByDelayRule:
		f.CancellationKind = expect.Request.CancelKind
		if f.Intent != nil {
			f.Intent.Outcome = IntentOutcomeSucceeded
		}
		d.Receipts = append(d.Receipts, receipt(expect.Request, "WAITING", "CANCELLATION_CONFIRMED_REFUND_PENDING", procmsg.EffectCompensateMO))
		f.CompensationCause = cancelCause(expect.Request.CancelKind)
		return []EffectDraft{cancellationEffect(d.ProcessState, f, procmsg.EffectApplyLogisticsCancellation, string(procmsg.TargetLogistics), event.ID)}, nil
	default:
		if expect.Request.ID != "" && expect.Request.Kind != "" {
			d.Receipts = append(d.Receipts, receipt(expect.Request, "COMPLETED", "", ""))
		}
	}
	return nil, nil
}

func finishObservedCancellations(d *Decision) {
	for _, f := range d.ProcessState.MerchantOrders {
		if f.Cancellation != nil && f.Cancellation.LogisticsConfirmed && f.Compensation.State == "SUCCEEDED" {
			reason := "CANCELLED_AND_REFUNDED"
			if cancelledMOHasDelivery(d.ProcessState, f.Cancellation.Request.MerchantOrderID) {
				reason = "CANCELLED_DELIVERY_REQUIRES_REVIEW"
			}
			d.Receipts = append(d.Receipts, receipt(f.Cancellation.Request, "COMPLETED", reason, ""))
			f.Cancellation = nil
		}
	}
}

func purchaseStopped(f *MOState) bool {
	return f.OwnerState == "FAILED" || f.OwnerState == "CANCELLED" || f.CancellationKind != "" || f.CompensationCause != "" || f.RefundRequestState == "REQUESTED" || f.RefundDecision == "APPROVED" || f.OpenRequests() > 0
}
func releasePurchase(d *Decision, f *MOState, cause int64) []EffectDraft {
	d.Receipts = append(d.Receipts, receipt(f.Purchase.Request, "WAITING", "PURCHASE_STOPPED", procmsg.EffectReleasePurchase))
	return []EffectDraft{purchaseEffect(d.ProcessState, f, procmsg.EffectReleasePurchase, string(procmsg.TargetProcurement), cause)}
}

func cancelCause(kind string) string {
	if kind == "DELAY_RULE" {
		return procmsg.CompensationCauseDelayRule
	}
	return procmsg.CompensationCauseCustomerCancelPreEffect
}

func hasBlockingEffect(f *MOState) bool {
	for _, e := range f.Effects {
		if e.Pending() && e.Type != procmsg.EffectPublishSupportCard {
			return true
		}
	}
	return false
}
func purchaseWaitingFor(f *MOState) string {
	if f.Purchase != nil && f.Purchase.MerchantGrantID != "" {
		return "MERCHANT_RESULT"
	}
	ids := make([]string, 0, len(f.Effects))
	for id := range f.Effects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if e := f.Effects[id]; e.Pending() {
			return e.Type
		}
	}
	return "OWNER_RESULT"
}
func pendingEffects(s ProcessState) []PendingEffect {
	var open []PendingEffect
	appendEffects := func(m map[string]EffectExpectation) {
		for _, e := range m {
			if e.Pending() {
				open = append(open, PendingEffect{ID: e.ID, Type: e.Type, NeedsAttention: e.Outcome == "ATTENTION_REQUIRED", MerchantOrderID: e.MerchantOrderID})
			}
		}
	}
	appendEffects(s.Effects)
	for _, id := range s.sortedMerchantOrderIDs() {
		appendEffects(s.MerchantOrders[id].Effects)
	}
	sort.Slice(open, func(i, j int) bool { return open[i].ID < open[j].ID })
	return open
}
func finishObservedPurchases(d *Decision) {
	for _, f := range d.ProcessState.MerchantOrders {
		if f.Purchase == nil {
			continue
		}
		if f.OwnerState == "PLACED" && f.Purchase.MerchantGrantID != "" {
			d.Receipts = append(d.Receipts, receipt(f.Purchase.Request, "COMPLETED", "MERCHANT_PLACED", ""))
			f.Purchase = nil
		} else if f.OwnerState == "FAILED" && f.Compensation.State == "SUCCEEDED" && !hasUnresolvedPurchasePhase(f) {
			d.Receipts = append(d.Receipts, receipt(f.Purchase.Request, "COMPLETED", "COMPENSATED", ""))
			f.Purchase = nil
		}
	}
}

func hasUnresolvedPurchasePhase(f *MOState) bool {
	if f.Purchase == nil {
		return false
	}
	for _, e := range f.Effects {
		if e.Request.ID == f.Purchase.Request.ID && e.Pending() {
			switch e.Type {
			case procmsg.EffectReservePurchase, procmsg.EffectConfirmPurchaseRegistration, procmsg.EffectEnsureMOFunding, procmsg.EffectGrantMerchantPurchase, procmsg.EffectReleasePurchase:
				return true
			}
		}
	}
	return false
}

func resumeCompensations(d *Decision, order string, cause int64, now time.Time) {
	for _, id := range d.ProcessState.sortedMerchantOrderIDs() {
		f := d.ProcessState.MerchantOrders[id]
		if f.CompensationCause == "" || f.Compensation.State == "SUCCEEDED" || hasUnresolvedPurchasePhase(f) {
			continue
		}
		registerEffect(d, order, EffectDraft{Type: procmsg.EffectCompensateMO, Target: string(procmsg.TargetPayment), Payload: procmsg.ExecuteMOCompensationPayload{MerchantOrderID: id, AllocationID: f.AllocationID, Cause: f.CompensationCause}, IdempotencyKey: procmsg.EffectIdempotencyKey(procmsg.EffectCompensateMO, order, "merchant-order:"+id), CausedByEventID: cause}, now)
	}
}
func resumeRequests(d *Decision, order string, now time.Time) {
	for _, id := range d.ProcessState.sortedMerchantOrderIDs() {
		f := d.ProcessState.MerchantOrders[id]
		if f.Purchase != nil || hasBlockingEffect(f) || len(f.PendingRequests) == 0 {
			continue
		}
		pending := f.PendingRequests
		f.PendingRequests = nil
		for _, r := range pending {
			for _, draft := range reduceRequest(d, r, Event{}, now) {
				registerEffect(d, order, draft, now)
			}
		}
	}
}

func (e EffectExpectation) String() string { return fmt.Sprintf("%s/%s", e.Target, e.Type) }

func cancelledMOHasDelivery(s ProcessState, mo string) bool {
	for _, u := range s.Units {
		if u.MerchantOrderID == mo && (u.Fulfillment == "DELIVERED_EXPECTED" || u.Fulfillment == "WRONG_ACTUAL") {
			return true
		}
	}
	return false
}
func updateLateDeliveryGuidance(d *Decision, order string, event Event) {
	if event.Type != procmsg.EventUnitFulfillmentChanged {
		return
	}
	for mo, f := range d.ProcessState.MerchantOrders {
		if f.CancellationKind == "" || f.Compensation.State != "SUCCEEDED" || f.Intent == nil || f.Intent.RequestID == "" || !cancelledMOHasDelivery(d.ProcessState, mo) {
			continue
		}
		r := procmsg.ActionRequest{ID: f.Intent.RequestID, Kind: procmsg.RequestCancel, AgencyOrderID: order, MerchantOrderID: mo, ActorID: f.Intent.UserID, ActorRole: "CUSTOMER", CancelKind: f.Intent.Kind}
		d.Receipts = append(d.Receipts, receipt(r, "COMPLETED", "CANCELLED_DELIVERY_REQUIRES_REVIEW", ""))
	}
}
