// Package e2e verifies the clean-cut ordering contract across immutable
// MerchantOrder allocations, payment effects, manual procurement, logistics,
// and the production OrderProcess reducer. External PayPal and merchant sites
// are represented by production-shaped fakes; no legacy payment compatibility
// path is part of this suite.
package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	paymentdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/policy"
	processdomain "github.com/vitlane/vitlane/server/internal/ordering/process/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	procurementdomain "github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

const (
	sixMOAgencyOrderID = "agency-order-six-mo-paypal"
	sixMOCustomerID    = "customer-six-mo"
)

var sixMONow = time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)

type immutableMOAllocation struct {
	ID                   string
	MerchantOrderID      string
	CheckoutOrdinal      int
	MerchantID           string
	ShopDomain           string
	PassThroughMinor     int64
	FeeVariableMinor     int64
	FeeFixedMinor        int64
	FeeTotalMinor        int64
	CustomerGrossMinor   int64
	Currency             string
	FeePolicyVersion     string
	ExecutionProfileHash string
}

func sixMOPayPalAllocations(t *testing.T) []immutableMOAllocation {
	t.Helper()
	bases := make([]policy.MerchantFeeBase, 6)
	for index := range bases {
		bases[index] = policy.MerchantFeeBase{
			CheckoutOrdinal:  index + 1,
			PassThroughMinor: int64(index+1) * 1_000,
		}
	}
	quote, err := policy.CalculateAgencyFee("PAYPAL_SANDBOX", bases)
	if err != nil {
		t.Fatalf("calculate six-MO fee: %v", err)
	}
	if quote.PolicyVersion != policy.PayPalMOFeePolicyVersion ||
		quote.PassThroughMinor != 21_000 || quote.VariableMinor != 1_134 ||
		quote.FixedMinor != 180 || quote.TotalMinor != 1_314 ||
		quote.CustomerPayableMinor != 22_314 {
		t.Fatalf("unexpected six-MO fee quote: %+v", quote)
	}

	result := make([]immutableMOAllocation, len(quote.MerchantOrders))
	for index, fee := range quote.MerchantOrders {
		result[index] = immutableMOAllocation{
			ID:                   fmt.Sprintf("allocation-%d", index+1),
			MerchantOrderID:      fmt.Sprintf("merchant-order-%d", index+1),
			CheckoutOrdinal:      fee.CheckoutOrdinal,
			MerchantID:           fmt.Sprintf("merchant-%d", index+1),
			ShopDomain:           fmt.Sprintf("shop-%d.example", index+1),
			PassThroughMinor:     fee.PassThroughMinor,
			FeeVariableMinor:     fee.VariableMinor,
			FeeFixedMinor:        fee.FixedMinor,
			FeeTotalMinor:        fee.TotalMinor,
			CustomerGrossMinor:   fee.CustomerPayableMinor,
			Currency:             "USD",
			FeePolicyVersion:     quote.PolicyVersion,
			ExecutionProfileHash: "0x" + strings.Repeat("7a", 32),
		}
		if fee.FixedMinor != 30 ||
			fee.TotalMinor != fee.VariableMinor+fee.FixedMinor ||
			fee.CustomerPayableMinor != fee.PassThroughMinor+fee.TotalMinor {
			t.Fatalf("MO %d fee is not 5.4%% plus USD 0.30: %+v", index+1, fee)
		}
	}
	return result
}

type authorizeCall struct {
	AgencyOrderID string
	AmountMinor   int64
	Currency      string
}

type captureCall struct {
	MerchantOrderID string
	AllocationID    string
	AmountMinor     int64
	Currency        string
	Stage           string
	CaptureID       string
}

type refundCall struct {
	MerchantOrderID string
	CaptureID       string
	AmountMinor     int64
	Currency        string
	RefundID        string
}

type voidCall struct {
	MerchantOrderID string
	AmountMinor     int64
	Currency        string
	AuthorizationID string
}

type compensationRecord struct {
	MerchantOrderID string
	AllocationID    string
	Action          paymentdomain.MOCompensationAction
	Cause           paymentdomain.MOCompensationCause
	AmountMinor     int64
	Currency        string
	ProviderEffect  bool
}

// fakePayPalFundingPort has the same economic boundary as Payment: one
// full-order authorization, exact captures at MO procurement start, and one
// whole-MO VOID/refund selected from the persisted funding milestone.
type fakePayPalFundingPort struct {
	allocations     map[string]immutableMOAllocation
	funding         map[string]paymentdomain.MOFundingState
	authorizations  []authorizeCall
	captures        []captureCall
	refunds         []refundCall
	voids           []voidCall
	compensations   map[string]compensationRecord
	authorizationID string
}

func newFakePayPalFundingPort(allocations []immutableMOAllocation) *fakePayPalFundingPort {
	port := &fakePayPalFundingPort{
		allocations:     make(map[string]immutableMOAllocation, len(allocations)),
		funding:         make(map[string]paymentdomain.MOFundingState, len(allocations)),
		compensations:   make(map[string]compensationRecord, len(allocations)),
		authorizationID: "PAYPAL-AUTH-SIX-MO",
	}
	for _, allocation := range allocations {
		port.allocations[allocation.MerchantOrderID] = allocation
		port.funding[allocation.MerchantOrderID] = paymentdomain.MOFundingAvailable
	}
	return port
}

func (p *fakePayPalFundingPort) AuthorizeFullOrder(
	agencyOrderID string,
	amountMinor int64,
	currency string,
) error {
	if agencyOrderID == "" || amountMinor <= 0 || currency != "USD" {
		return errors.New("invalid full-order authorization")
	}
	if len(p.authorizations) > 0 {
		previous := p.authorizations[0]
		if previous.AgencyOrderID != agencyOrderID || previous.AmountMinor != amountMinor ||
			previous.Currency != currency {
			return errors.New("authorization replay mismatch")
		}
		return nil
	}
	p.authorizations = append(p.authorizations, authorizeCall{
		AgencyOrderID: agencyOrderID, AmountMinor: amountMinor, Currency: currency,
	})
	return nil
}

func (p *fakePayPalFundingPort) CaptureAtProcurementStart(
	merchantOrderID, allocationID string,
	amountMinor int64,
) error {
	allocation, ok := p.allocations[merchantOrderID]
	if !ok || allocation.ID != allocationID || allocation.CustomerGrossMinor != amountMinor {
		return errors.New("capture does not match immutable MO allocation")
	}
	if len(p.authorizations) != 1 ||
		p.funding[merchantOrderID] != paymentdomain.MOFundingAvailable {
		return errors.New("MO funding is not available for capture")
	}
	for _, call := range p.captures {
		if call.MerchantOrderID == merchantOrderID {
			return nil
		}
	}
	captureID := fmt.Sprintf("PAYPAL-CAPTURE-MO-%d", allocation.CheckoutOrdinal)
	p.captures = append(p.captures, captureCall{
		MerchantOrderID: merchantOrderID,
		AllocationID:    allocationID,
		AmountMinor:     amountMinor,
		Currency:        "USD",
		Stage:           "PROCUREMENT_START",
		CaptureID:       captureID,
	})
	p.funding[merchantOrderID] = paymentdomain.MOFundingActive
	return nil
}

func (p *fakePayPalFundingPort) Compensate(
	merchantOrderID, allocationID string,
	cause paymentdomain.MOCompensationCause,
) (compensationRecord, bool, error) {
	if previous, ok := p.compensations[merchantOrderID]; ok {
		if previous.AllocationID != allocationID || previous.Cause != cause {
			return compensationRecord{}, true, errors.New("compensation replay mismatch")
		}
		return previous, true, nil
	}
	allocation, ok := p.allocations[merchantOrderID]
	if !ok || allocation.ID != allocationID || !paymentdomain.ValidMOCompensationCause(cause) {
		return compensationRecord{}, false, errors.New("invalid MO compensation target")
	}
	record := compensationRecord{
		MerchantOrderID: merchantOrderID,
		AllocationID:    allocationID,
		Cause:           cause,
		AmountMinor:     allocation.CustomerGrossMinor,
		Currency:        "USD",
	}
	switch p.funding[merchantOrderID] {
	case paymentdomain.MOFundingAvailable, paymentdomain.MOFundingFailed:
		record.Action = paymentdomain.MOCompensationVoid
		// PayPal has no partial void. Earlier uncaptured MOs are released as a
		// local never-capture promise; the final residual position performs the
		// provider authorization void.
		remaining := 0
		for otherMO, state := range p.funding {
			if otherMO != merchantOrderID && state != paymentdomain.MOFundingActive &&
				state != paymentdomain.MOFundingReleased {
				remaining++
			}
		}
		if remaining == 0 {
			record.ProviderEffect = true
			p.voids = append(p.voids, voidCall{
				MerchantOrderID: merchantOrderID,
				AmountMinor:     allocation.CustomerGrossMinor,
				Currency:        "USD",
				AuthorizationID: p.authorizationID,
			})
		}
	case paymentdomain.MOFundingActive:
		record.Action = paymentdomain.MOCompensationRefund
		capture, found := p.captureFor(merchantOrderID)
		if !found {
			return compensationRecord{}, false, errors.New("active MO has no capture")
		}
		record.ProviderEffect = true
		p.refunds = append(p.refunds, refundCall{
			MerchantOrderID: merchantOrderID,
			CaptureID:       capture.CaptureID,
			AmountMinor:     allocation.CustomerGrossMinor,
			Currency:        "USD",
			RefundID:        "PAYPAL-REFUND-" + merchantOrderID,
		})
	default:
		return compensationRecord{}, false, errors.New("MO funding cannot be compensated")
	}
	p.funding[merchantOrderID] = paymentdomain.MOFundingReleased
	p.compensations[merchantOrderID] = record
	return record, false, nil
}

func (p *fakePayPalFundingPort) captureFor(merchantOrderID string) (captureCall, bool) {
	for _, call := range p.captures {
		if call.MerchantOrderID == merchantOrderID {
			return call, true
		}
	}
	return captureCall{}, false
}

type procurementEvidence struct {
	Kind                     procurementdomain.PlacementEvidenceKind
	ShopDomain               string
	ProductName              string
	Variant                  string
	Quantity                 int
	ExternalOrderID          string
	ReceiptSafeRef           string
	ActualAmountMinor        int64
	Currency                 string
	EvidenceSource           procurementdomain.EvidenceSource
	EvidenceHash             string
	OperatorID               string
	ObservedAt               time.Time
	ClaimsExternalLiveEffect bool
}

func (e *procurementEvidence) finalizeAndValidate(
	merchantOrderID string,
	authorizedAmountMinor int64,
	serverNow time.Time,
) error {
	if strings.TrimSpace(e.ShopDomain) == "" || strings.TrimSpace(e.ProductName) == "" ||
		strings.TrimSpace(e.Variant) == "" || e.Quantity <= 0 ||
		strings.TrimSpace(e.OperatorID) == "" {
		return errors.New("procurement evidence is incomplete")
	}
	record := procurementdomain.FinalizePlacementEvidence(
		merchantOrderID, e.OperatorID, serverNow,
		procurementdomain.PlacementEvidence{
			Kind: e.Kind, ExternalOrderRef: e.ExternalOrderID,
			ReceiptSafeRef: e.ReceiptSafeRef, ActualAmountMinor: e.ActualAmountMinor,
			Currency: e.Currency, EvidenceSource: e.EvidenceSource,
			ClaimsExternalLive: e.ClaimsExternalLiveEffect,
		},
	)
	e.EvidenceHash = record.EvidenceHash
	e.ObservedAt = record.ObservedAt
	return procurementdomain.ValidatePlacementEvidence(
		procurementdomain.ModeSimulatedNoEffect,
		authorizedAmountMinor,
		record,
	)
}

type customerExchange struct {
	Prompt   string
	Response string
	Answered bool
}

type fakeProcurementTask struct {
	Allocation immutableMOAllocation
	Claimed    bool
	Decisions  []procurementdomain.ManualDecision
	Requests   []customerExchange
	Started    bool
	Placed     bool
	Failed     bool
	Cancelled  bool
	Evidence   *procurementEvidence
}

// fakeProcurementPort preserves the production ordering: manual decision and
// zero open requests first, then Payment capture, then merchant evidence.
type fakeProcurementPort struct {
	payment *fakePayPalFundingPort
	tasks   map[string]*fakeProcurementTask
}

func newFakeProcurementPort(
	payment *fakePayPalFundingPort,
	allocations []immutableMOAllocation,
) *fakeProcurementPort {
	port := &fakeProcurementPort{
		payment: payment,
		tasks:   make(map[string]*fakeProcurementTask, len(allocations)),
	}
	for _, allocation := range allocations {
		copy := allocation
		port.tasks[allocation.MerchantOrderID] = &fakeProcurementTask{Allocation: copy}
	}
	return port
}

func (p *fakeProcurementPort) Claim(merchantOrderID string) error {
	task, ok := p.tasks[merchantOrderID]
	if !ok || task.Cancelled || task.Failed || task.Placed {
		return errors.New("procurement task is not claimable")
	}
	task.Claimed = true
	return nil
}

func (p *fakeProcurementPort) Decide(
	merchantOrderID string,
	decision procurementdomain.ManualDecision,
) error {
	task := p.tasks[merchantOrderID]
	if task == nil || !task.Claimed || task.Started || task.hasOpenRequest() {
		return errors.New("manual decision is not admissible")
	}
	switch decision {
	case procurementdomain.DecisionWithinAuthorization,
		procurementdomain.DecisionImmaterialVariance,
		procurementdomain.DecisionMaterialCondition,
		procurementdomain.DecisionUnableToPurchase:
	default:
		return errors.New("invalid manual decision")
	}
	task.Decisions = append(task.Decisions, decision)
	return nil
}

func (p *fakeProcurementPort) AskCustomer(merchantOrderID, prompt string) error {
	task := p.tasks[merchantOrderID]
	if task == nil || len(task.Decisions) == 0 || task.hasOpenRequest() ||
		task.Decisions[len(task.Decisions)-1] != procurementdomain.DecisionMaterialCondition ||
		strings.TrimSpace(prompt) == "" {
		return errors.New("customer request requires a material-condition decision")
	}
	task.Requests = append(task.Requests, customerExchange{Prompt: prompt})
	return nil
}

func (p *fakeProcurementPort) Respond(merchantOrderID, response string) error {
	task := p.tasks[merchantOrderID]
	if task == nil || !task.hasOpenRequest() || strings.TrimSpace(response) == "" {
		return errors.New("no customer request is awaiting response")
	}
	index := len(task.Requests) - 1
	task.Requests[index].Response = response
	task.Requests[index].Answered = true
	return nil
}

func (p *fakeProcurementPort) Begin(merchantOrderID string) error {
	task := p.tasks[merchantOrderID]
	if task == nil || !task.Claimed || task.Started || task.hasOpenRequest() ||
		len(task.Decisions) == 0 {
		return errors.New("procurement start is blocked")
	}
	latest := task.Decisions[len(task.Decisions)-1]
	if latest != procurementdomain.DecisionWithinAuthorization &&
		latest != procurementdomain.DecisionImmaterialVariance {
		return errors.New("procurement requires a fresh positive re-evaluation")
	}
	if err := p.payment.CaptureAtProcurementStart(
		merchantOrderID, task.Allocation.ID, task.Allocation.CustomerGrossMinor,
	); err != nil {
		return err
	}
	task.Started = true
	return nil
}

func (p *fakeProcurementPort) Place(
	merchantOrderID string,
	evidence procurementEvidence,
) error {
	task := p.tasks[merchantOrderID]
	if task == nil || !task.Started || task.Placed || task.Failed || task.Cancelled {
		return errors.New("merchant order cannot be placed")
	}
	if err := evidence.finalizeAndValidate(
		merchantOrderID, task.Allocation.PassThroughMinor, sixMONow,
	); err != nil {
		return err
	}
	if evidence.ShopDomain != task.Allocation.ShopDomain {
		return errors.New("evidence shop does not match immutable allocation")
	}
	task.Placed = true
	task.Evidence = &evidence
	return nil
}

func (p *fakeProcurementPort) Fail(merchantOrderID string) error {
	task := p.tasks[merchantOrderID]
	if task == nil || !task.Claimed || task.Started || len(task.Decisions) == 0 ||
		task.Decisions[len(task.Decisions)-1] != procurementdomain.DecisionUnableToPurchase {
		return errors.New("procurement failure requires UNABLE_TO_PURCHASE")
	}
	task.Failed = true
	return nil
}

func (p *fakeProcurementPort) CancelPreEffect(merchantOrderID string) error {
	task := p.tasks[merchantOrderID]
	if task == nil || task.Started || task.Placed || task.Failed || task.Cancelled {
		return errors.New("pre-effect cancellation is unavailable")
	}
	task.Cancelled = true
	return nil
}

func (task *fakeProcurementTask) hasOpenRequest() bool {
	return len(task.Requests) > 0 && !task.Requests[len(task.Requests)-1].Answered
}

type recoveryEvidence struct {
	MerchantOrderID string
	AmountMinor     int64
	Currency        string
	Source          string
	Reference       string
	EvidenceHash    string
	ObservedAt      time.Time
}

type fakeLogisticsPort struct {
	exceptions map[string]string
	recoveries []recoveryEvidence
}

func newFakeLogisticsPort() *fakeLogisticsPort {
	return &fakeLogisticsPort{exceptions: make(map[string]string)}
}

func (l *fakeLogisticsPort) JudgeDeliveryException(
	merchantOrderID, rationale string,
) error {
	if strings.TrimSpace(merchantOrderID) == "" || len(strings.TrimSpace(rationale)) < 8 {
		return errors.New("delivery exception requires a public rationale")
	}
	l.exceptions[merchantOrderID] = rationale
	return nil
}

func (l *fakeLogisticsPort) RecordRecovery(evidence recoveryEvidence) error {
	if l.exceptions[evidence.MerchantOrderID] == "" || evidence.AmountMinor < 0 ||
		evidence.Currency != "USD" || strings.TrimSpace(evidence.Source) == "" ||
		strings.TrimSpace(evidence.Reference) == "" ||
		len(strings.TrimSpace(evidence.EvidenceHash)) < 16 || evidence.ObservedAt.IsZero() {
		return errors.New("recovery evidence is incomplete")
	}
	l.recoveries = append(l.recoveries, evidence)
	return nil
}

type processProjection struct {
	process processdomain.Process
	seq     int64
	now     time.Time
}

func newProcessProjection() *processProjection {
	return &processProjection{
		process: processdomain.Process{AgencyOrderID: sixMOAgencyOrderID},
		now:     sixMONow,
	}
}

func (p *processProjection) Apply(
	t *testing.T,
	source procmsg.Source,
	eventType string,
	payload any,
) processdomain.Decision {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s: %v", eventType, err)
	}
	p.seq++
	events := []processdomain.Event{}
	expectations := []processdomain.EffectExpectation{}
	for _, e := range p.process.ProcessState.Effects {
		expectations = append(expectations, e)
	}
	for _, mo := range p.process.ProcessState.MerchantOrders {
		for _, e := range mo.Effects {
			expectations = append(expectations, e)
		}
	}
	for _, e := range expectations {
		ancillary := e.Type == procmsg.EffectPlanMerchantOrders || e.Type == procmsg.EffectRegisterExpectedUnits || e.Type == procmsg.EffectPublishSupportCard || e.Type == procmsg.EffectApplyLogisticsCancellation
		if !e.Pending() || !ancillary {
			continue
		}
		reportRaw, _ := json.Marshal(procmsg.EffectReport{EffectID: e.ID, EffectType: e.Type, MerchantOrderID: e.MerchantOrderID, RequestID: e.Request.ID, Outcome: "SUCCEEDED"})
		events = append(events, processdomain.Event{ID: p.seq, Seq: p.seq, Source: e.Target, Type: procmsg.EventEffectReported, Payload: reportRaw, OccurredAt: p.now})
		p.seq++
	}
	events = append(events, processdomain.Event{ID: p.seq, Seq: p.seq, Source: string(source), Type: eventType, Payload: raw, OccurredAt: p.now})
	if eventType == procmsg.EventMOCompensationStateChanged {
		observed := payload.(procmsg.MOCompensationStateChangedPayload)
		if observed.State == "SUCCEEDED" {
			for _, e := range expectations {
				if e.Pending() && e.MerchantOrderID == observed.MerchantOrderID && e.Type == procmsg.EffectCompensateMO {
					p.seq++
					reportRaw, _ := json.Marshal(procmsg.EffectReport{EffectID: e.ID, EffectType: e.Type, MerchantOrderID: e.MerchantOrderID, RequestID: e.Request.ID, Outcome: "SUCCEEDED"})
					events = append(events, processdomain.Event{ID: p.seq, Seq: p.seq, Source: e.Target, Type: procmsg.EventEffectReported, Payload: reportRaw, OccurredAt: p.now})
				}
			}
		}
	}
	var decision processdomain.Decision
	var effects []processdomain.EffectDraft
	for _, event := range events {
		next, err := processdomain.Reduce(p.process, event, p.now)
		if err != nil {
			t.Fatalf("process event %s: %v", eventType, err)
		}
		p.process = processdomain.Process{
			AgencyOrderID:  sixMOAgencyOrderID,
			State:          next.State,
			TerminalReason: next.TerminalReason,
			LastReasonCode: next.LastReasonCode,
			Version:        p.process.Version + 1,
			LastAppliedSeq: event.Seq,
			WakeAt:         next.WakeAt,
			ProcessState:   next.ProcessState,
			UpdatedAt:      p.now,
		}
		effects = append(effects, next.Effects...)
		decision = next
	}
	decision.Effects = effects

	return decision
}

func commandByType(
	t *testing.T,
	decision processdomain.Decision,
	commandType string,
) processdomain.EffectDraft {
	t.Helper()
	for _, command := range decision.Effects {
		if command.Type == commandType {
			return command
		}
	}
	t.Fatalf("command %s not found in %+v", commandType, decision.Effects)
	return processdomain.EffectDraft{}
}

func assertCompensationCommand(
	t *testing.T,
	decision processdomain.Decision,
	allocation immutableMOAllocation,
	cause string,
) {
	t.Helper()
	command := commandByType(t, decision, procmsg.EffectCompensateMO)
	payload, ok := command.Payload.(procmsg.ExecuteMOCompensationPayload)
	if !ok {
		t.Fatalf("unexpected compensation payload type %T", command.Payload)
	}
	if command.Target != string(procmsg.TargetPayment) ||
		payload.MerchantOrderID != allocation.MerchantOrderID ||
		payload.AllocationID != allocation.ID || payload.Cause != cause ||
		command.IdempotencyKey != procmsg.EffectIdempotencyKey(
			procmsg.EffectCompensateMO,
			sixMOAgencyOrderID,
			"merchant-order:"+allocation.MerchantOrderID,
		) {
		t.Fatalf("compensation command did not preserve exact MO target: %+v", command)
	}
}

func merchantOrderPayload(
	allocation immutableMOAllocation,
	state string,
	openCount int,
	cause string,
) procmsg.MerchantOrderStateChangedPayload {
	return procmsg.MerchantOrderStateChangedPayload{
		MerchantOrderID:   allocation.MerchantOrderID,
		AllocationID:      allocation.ID,
		ShopDomain:        allocation.ShopDomain,
		State:             state,
		CompensationCause: cause,
		TotalCount:        6,
		OpenCount:         openCount,
	}
}

func evidenceFor(allocation immutableMOAllocation, suffix string) procurementEvidence {
	return procurementEvidence{
		Kind:              procurementdomain.PlacementEvidenceSandboxTest,
		ShopDomain:        allocation.ShopDomain,
		ProductName:       "E2E product " + suffix,
		Variant:           "Variant " + suffix,
		Quantity:          1,
		ExternalOrderID:   "SHOP-ORDER-" + suffix,
		ReceiptSafeRef:    "receipt-safe-ref-" + suffix,
		ActualAmountMinor: allocation.PassThroughMinor,
		Currency:          allocation.Currency,
		EvidenceSource:    procurementdomain.EvidenceReceipt,
		OperatorID:        "operator-six-mo",
	}
}

func TestPayPalSixMerchantOrderCleanCutFlow(t *testing.T) {
	allocations := sixMOPayPalAllocations(t)
	allocationSnapshot := append([]immutableMOAllocation(nil), allocations...)
	byOrdinal := func(ordinal int) immutableMOAllocation {
		t.Helper()
		return allocations[ordinal-1]
	}

	payment := newFakePayPalFundingPort(allocations)
	procurement := newFakeProcurementPort(payment, allocations)
	logistics := newFakeLogisticsPort()
	process := newProcessProjection()

	var authorizedGross int64
	for _, allocation := range allocations {
		authorizedGross += allocation.CustomerGrossMinor
	}
	if err := payment.AuthorizeFullOrder(
		sixMOAgencyOrderID, authorizedGross, "USD",
	); err != nil {
		t.Fatal(err)
	}
	// Idempotent application progress must not create a second authorization.
	if err := payment.AuthorizeFullOrder(
		sixMOAgencyOrderID, authorizedGross, "USD",
	); err != nil {
		t.Fatal(err)
	}
	if len(payment.authorizations) != 1 {
		t.Fatalf("PayPal AUTHORIZE calls=%d, want exactly one", len(payment.authorizations))
	}

	process.Apply(t, procmsg.SourceAgencyOrder, procmsg.EventOrderIssued,
		procmsg.OrderIssuedPayload{
			UserID: sixMOCustomerID, Rail: "PAYPAL", IssuedAt: sixMONow,
		})
	fundingReady := process.Apply(t, procmsg.SourcePayment, procmsg.EventCustomerFundingReady,
		procmsg.CustomerFundingReadyPayload{
			CustomerPaymentID:   "customer-payment-six-mo",
			Rail:                "PAYPAL",
			Source:              "PAYPAL_AUTHORIZATION",
			ProviderEnvironment: "SANDBOX",
		})
	planCommand := commandByType(t, fundingReady, procmsg.EffectPlanMerchantOrders)
	if planCommand.Target != string(procmsg.TargetProcurement) ||
		fundingReady.State != processdomain.StateProcurementInProgress {
		t.Fatalf("funding-ready handoff=%+v command=%+v", fundingReady, planCommand)
	}
	for _, allocation := range allocations {
		process.Apply(t, procmsg.SourceProcurement, procmsg.EventMerchantOrderStateChanged,
			merchantOrderPayload(allocation, "PLANNED", 6, ""))
	}

	// MO 1: within authorization, capture only at Begin, then preserve complete
	// external order/receipt/operator evidence. No customer message is needed.
	mo1 := byOrdinal(1)
	if err := procurement.Claim(mo1.MerchantOrderID); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Decide(
		mo1.MerchantOrderID, procurementdomain.DecisionWithinAuthorization,
	); err != nil {
		t.Fatal(err)
	}
	if len(payment.captures) != 0 {
		t.Fatal("manual review must not capture before procurement begins")
	}
	if err := procurement.Begin(mo1.MerchantOrderID); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Place(mo1.MerchantOrderID, evidenceFor(mo1, "MO-1")); err != nil {
		t.Fatal(err)
	}
	if len(procurement.tasks[mo1.MerchantOrderID].Requests) != 0 {
		t.Fatal("ordinary procurement created an unnecessary customer request")
	}
	process.Apply(t, procmsg.SourceProcurement, procmsg.EventMerchantOrderStateChanged,
		merchantOrderPayload(mo1, "PLACED", 5, ""))
	process.Apply(t, procmsg.SourceLogistics, procmsg.EventUnitFulfillmentChanged,
		procmsg.UnitFulfillmentChangedPayload{
			ExpectedUnitID: "expected-mo-1", MerchantOrderID: mo1.MerchantOrderID,
			Fulfillment: "DELIVERED_EXPECTED", PendingPhysicalCount: 0,
			DeliveredValueExists: true,
		})

	// MO 2: request -> response -> re-evaluation -> second request -> response ->
	// final positive re-evaluation. Neither pending nor answered input itself may
	// start a capture.
	mo2 := byOrdinal(2)
	if err := procurement.Claim(mo2.MerchantOrderID); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Decide(
		mo2.MerchantOrderID, procurementdomain.DecisionMaterialCondition,
	); err != nil {
		t.Fatal(err)
	}
	if err := procurement.AskCustomer(mo2.MerchantOrderID, "첫 번째 조건에 동의합니까?"); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Begin(mo2.MerchantOrderID); err == nil {
		t.Fatal("open customer request must block procurement")
	}
	if err := procurement.Respond(mo2.MerchantOrderID, "첫 번째 조건에 동의합니다"); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Begin(mo2.MerchantOrderID); err == nil {
		t.Fatal("customer response must return to manual re-evaluation")
	}
	if err := procurement.Decide(
		mo2.MerchantOrderID, procurementdomain.DecisionMaterialCondition,
	); err != nil {
		t.Fatal(err)
	}
	if err := procurement.AskCustomer(mo2.MerchantOrderID, "추가 옵션을 확인해 주세요"); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Respond(mo2.MerchantOrderID, "옵션 B로 진행해 주세요"); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Decide(
		mo2.MerchantOrderID, procurementdomain.DecisionWithinAuthorization,
	); err != nil {
		t.Fatal(err)
	}
	if len(payment.captures) != 1 {
		t.Fatalf("MO 2 conversation captured early: captures=%d", len(payment.captures))
	}
	if err := procurement.Begin(mo2.MerchantOrderID); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Place(mo2.MerchantOrderID, evidenceFor(mo2, "MO-2")); err != nil {
		t.Fatal(err)
	}
	if task := procurement.tasks[mo2.MerchantOrderID]; len(task.Decisions) != 3 ||
		len(task.Requests) != 2 || !task.Requests[0].Answered || !task.Requests[1].Answered {
		t.Fatalf("MO 2 did not preserve repeated re-evaluation: %+v", task)
	}
	process.Apply(t, procmsg.SourceProcurement, procmsg.EventMerchantOrderStateChanged,
		merchantOrderPayload(mo2, "PLACED", 4, ""))
	process.Apply(t, procmsg.SourceLogistics, procmsg.EventUnitFulfillmentChanged,
		procmsg.UnitFulfillmentChangedPayload{
			ExpectedUnitID: "expected-mo-2", MerchantOrderID: mo2.MerchantOrderID,
			Fulfillment: "DELIVERED_EXPECTED", PendingPhysicalCount: 0,
			DeliveredValueExists: true,
		})

	// MO 3: unable to purchase before effect. Process targets the exact MO and
	// Payment selects VOID for the full immutable gross, including allocated fee.
	mo3 := byOrdinal(3)
	if err := procurement.Claim(mo3.MerchantOrderID); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Decide(
		mo3.MerchantOrderID, procurementdomain.DecisionUnableToPurchase,
	); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Fail(mo3.MerchantOrderID); err != nil {
		t.Fatal(err)
	}
	failed := process.Apply(t, procmsg.SourceProcurement, procmsg.EventMerchantOrderStateChanged,
		merchantOrderPayload(mo3, "FAILED", 3, procmsg.CompensationCauseProcurementFailure))
	assertCompensationCommand(t, failed, mo3, procmsg.CompensationCauseProcurementFailure)
	comp3, _, err := payment.Compensate(
		mo3.MerchantOrderID, mo3.ID, paymentdomain.MOCompensationProcurementFailure,
	)
	if err != nil || comp3.Action != paymentdomain.MOCompensationVoid || comp3.ProviderEffect ||
		comp3.AmountMinor != mo3.CustomerGrossMinor {
		t.Fatalf("MO 3 compensation=%+v err=%v", comp3, err)
	}
	process.Apply(t, procmsg.SourcePayment, procmsg.EventMOCompensationStateChanged,
		procmsg.MOCompensationStateChangedPayload{
			CompensationID: "compensation-mo-3", MerchantOrderID: mo3.MerchantOrderID,
			AllocationID: mo3.ID, State: "SUCCEEDED",
			Cause:          procmsg.CompensationCauseProcurementFailure,
			SucceededCount: 1,
		})

	// MO 4: capture and place first, then a delivery exception. Customer refund
	// and merchant recovery are independent facts with their own evidence.
	mo4 := byOrdinal(4)
	if err := procurement.Claim(mo4.MerchantOrderID); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Decide(
		mo4.MerchantOrderID, procurementdomain.DecisionWithinAuthorization,
	); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Begin(mo4.MerchantOrderID); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Place(mo4.MerchantOrderID, evidenceFor(mo4, "MO-4")); err != nil {
		t.Fatal(err)
	}
	process.Apply(t, procmsg.SourceProcurement, procmsg.EventMerchantOrderStateChanged,
		merchantOrderPayload(mo4, "PLACED", 2, ""))
	process.Apply(t, procmsg.SourceLogistics, procmsg.EventUnitFulfillmentChanged,
		procmsg.UnitFulfillmentChangedPayload{
			ExpectedUnitID: "expected-mo-4", MerchantOrderID: mo4.MerchantOrderID,
			Fulfillment: "WRONG_ACTUAL", PendingPhysicalCount: 1,
			DeliveredValueExists: true,
		})
	if err := logistics.JudgeDeliveryException(
		mo4.MerchantOrderID, "오배송이 확인되어 전액 환불합니다",
	); err != nil {
		t.Fatal(err)
	}
	deliveryFault := process.Apply(t, procmsg.SourceLogistics, procmsg.EventDeliveryFaultJudged,
		procmsg.DeliveryFaultJudgedPayload{
			ResolutionID: "resolution-mo-4", ExpectedUnitID: "expected-mo-4",
			MerchantOrderID: mo4.MerchantOrderID, AllocationID: mo4.ID,
			Cause: "WRONG_ACTUAL", Judgment: "REFUND",
		})
	assertCompensationCommand(t, deliveryFault, mo4, procmsg.CompensationCauseDeliveryException)
	comp4, _, err := payment.Compensate(
		mo4.MerchantOrderID, mo4.ID, paymentdomain.MOCompensationDeliveryException,
	)
	if err != nil || comp4.Action != paymentdomain.MOCompensationRefund ||
		comp4.AmountMinor != mo4.CustomerGrossMinor {
		t.Fatalf("MO 4 compensation=%+v err=%v", comp4, err)
	}
	if err := logistics.RecordRecovery(recoveryEvidence{
		MerchantOrderID: mo4.MerchantOrderID,
		AmountMinor:     mo4.PassThroughMinor,
		Currency:        "USD",
		Source:          "MERCHANT_RETURN",
		Reference:       "merchant-credit-mo-4",
		EvidenceHash:    "sha256:e2e-recovery-evidence-mo-4",
		ObservedAt:      sixMONow,
	}); err != nil {
		t.Fatal(err)
	}
	process.Apply(t, procmsg.SourceLogistics, procmsg.EventUnitFulfillmentChanged,
		procmsg.UnitFulfillmentChangedPayload{
			ExpectedUnitID: "expected-mo-4", MerchantOrderID: mo4.MerchantOrderID,
			Fulfillment: "RESOLVED", PendingPhysicalCount: 0,
			DeliveredValueExists: true,
		})
	process.Apply(t, procmsg.SourcePayment, procmsg.EventMOCompensationStateChanged,
		procmsg.MOCompensationStateChangedPayload{
			CompensationID: "compensation-mo-4", MerchantOrderID: mo4.MerchantOrderID,
			AllocationID: mo4.ID, State: "SUCCEEDED",
			Cause:          procmsg.CompensationCauseDeliveryException,
			SucceededCount: 2,
		})

	// MO 5: approved post-effect customer refund. It refunds the exact capture,
	// never a unit or a recalculated fee amount.
	mo5 := byOrdinal(5)
	if err := procurement.Claim(mo5.MerchantOrderID); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Decide(
		mo5.MerchantOrderID, procurementdomain.DecisionWithinAuthorization,
	); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Begin(mo5.MerchantOrderID); err != nil {
		t.Fatal(err)
	}
	if err := procurement.Place(mo5.MerchantOrderID, evidenceFor(mo5, "MO-5")); err != nil {
		t.Fatal(err)
	}
	process.Apply(t, procmsg.SourceProcurement, procmsg.EventMerchantOrderStateChanged,
		merchantOrderPayload(mo5, "PLACED", 1, ""))
	process.Apply(t, procmsg.SourceLogistics, procmsg.EventUnitFulfillmentChanged,
		procmsg.UnitFulfillmentChangedPayload{
			ExpectedUnitID: "expected-mo-5", MerchantOrderID: mo5.MerchantOrderID,
			Fulfillment: "DELIVERED_EXPECTED", PendingPhysicalCount: 0,
			DeliveredValueExists: true,
		})
	refundApproved := process.Apply(t, procmsg.SourceAgencyOrder,
		procmsg.EventRefundReviewDecided, procmsg.RefundReviewDecidedPayload{
			RequestID: "refund-request-mo-5", MerchantOrderID: mo5.MerchantOrderID,
			AllocationID: mo5.ID, Decision: "APPROVED", ReasonCode: "PRODUCT_DEFECT",
		})
	assertCompensationCommand(t, refundApproved, mo5,
		procmsg.CompensationCauseCustomerRefundPostEffect)
	comp5, replay, err := payment.Compensate(
		mo5.MerchantOrderID, mo5.ID, paymentdomain.MOCompensationCustomerRefundPostEffect,
	)
	if err != nil || replay || comp5.Action != paymentdomain.MOCompensationRefund ||
		comp5.AmountMinor != mo5.CustomerGrossMinor {
		t.Fatalf("MO 5 compensation=%+v replay=%v err=%v", comp5, replay, err)
	}
	if replayed, replay, err := payment.Compensate(
		mo5.MerchantOrderID, mo5.ID, paymentdomain.MOCompensationCustomerRefundPostEffect,
	); err != nil || !replay || replayed != comp5 {
		t.Fatalf("MO 5 compensation replay=%+v replay=%v err=%v", replayed, replay, err)
	}
	process.Apply(t, procmsg.SourceLogistics, procmsg.EventUnitFulfillmentChanged,
		procmsg.UnitFulfillmentChangedPayload{
			ExpectedUnitID: "expected-mo-5", MerchantOrderID: mo5.MerchantOrderID,
			Fulfillment: "RESOLVED", PendingPhysicalCount: 0,
			DeliveredValueExists: true,
		})
	process.Apply(t, procmsg.SourcePayment, procmsg.EventMOCompensationStateChanged,
		procmsg.MOCompensationStateChangedPayload{
			CompensationID: "compensation-mo-5", MerchantOrderID: mo5.MerchantOrderID,
			AllocationID: mo5.ID, State: "SUCCEEDED",
			Cause:          procmsg.CompensationCauseCustomerRefundPostEffect,
			SucceededCount: 3,
		})

	// MO 6: free pre-effect cancellation targets one MO. Process first issues
	// the exact cancellation command, then the terminal MO fact triggers VOID.
	mo6 := byOrdinal(6)
	cancelRequested := process.Apply(t, procmsg.SourceCustomer, procmsg.EventActionRequested, procmsg.ActionRequest{ID: "cancel-mo-6", Kind: procmsg.RequestCancel, CancelKind: "PRE_EFFECT", AgencyOrderID: sixMOAgencyOrderID, MerchantOrderID: mo6.MerchantOrderID, AllocationID: mo6.ID, ActorID: sixMOCustomerID, ActorRole: "CUSTOMER"})
	reservation := commandByType(t, cancelRequested, procmsg.EffectReserveCancellation)
	reservationID := procmsg.EffectIdentity(sixMOAgencyOrderID, reservation.IdempotencyKey)
	reservationResult, _ := json.Marshal(procmsg.CancellationReservation{ReservationID: reservationID, UndeliveredUnits: 1})
	reserved := process.Apply(t, procmsg.SourceLogistics, procmsg.EventEffectReported, procmsg.EffectReport{EffectID: reservationID, EffectType: reservation.Type, MerchantOrderID: mo6.MerchantOrderID, RequestID: "cancel-mo-6", Outcome: "SUCCEEDED", Result: reservationResult})
	cancelCommand := commandByType(t, reserved, procmsg.EffectCancelPrePurchase)
	cancelPayload, ok := cancelCommand.Payload.(procmsg.CancellationContext)
	if !ok || cancelCommand.Target != string(procmsg.TargetProcurement) || cancelPayload.MerchantOrderID != mo6.MerchantOrderID || cancelPayload.AllocationID != mo6.ID {
		t.Fatalf("cancellation effect=%+v", cancelCommand)
	}
	if err := procurement.CancelPreEffect(mo6.MerchantOrderID); err != nil {
		t.Fatal(err)
	}
	process.Apply(t, procmsg.SourceProcurement, procmsg.EventEffectReported, procmsg.EffectReport{EffectID: procmsg.EffectIdentity(sixMOAgencyOrderID, cancelCommand.IdempotencyKey), EffectType: cancelCommand.Type, MerchantOrderID: mo6.MerchantOrderID, RequestID: "cancel-mo-6", Outcome: "SUCCEEDED"})
	cancelled := process.Apply(t, procmsg.SourceProcurement,
		procmsg.EventMerchantOrderStateChanged,
		merchantOrderPayload(mo6, "CANCELLED", 0,
			procmsg.CompensationCauseCustomerCancelPreEffect))
	if cancelled.ProcessState.MerchantOrders[mo6.MerchantOrderID].CompensationCause != procmsg.CompensationCauseCustomerCancelPreEffect {
		t.Fatal("cancel compensation obligation lost")
	}
	if cancelled.State != processdomain.StateResolutionInProgress {
		t.Fatalf("order terminated before MO 6 compensation: %s", cancelled.State)
	}
	comp6, _, err := payment.Compensate(
		mo6.MerchantOrderID, mo6.ID, paymentdomain.MOCompensationCustomerCancelPreEffect,
	)
	if err != nil || comp6.Action != paymentdomain.MOCompensationVoid ||
		!comp6.ProviderEffect || comp6.AmountMinor != mo6.CustomerGrossMinor {
		t.Fatalf("MO 6 compensation=%+v err=%v", comp6, err)
	}
	terminal := process.Apply(t, procmsg.SourcePayment,
		procmsg.EventMOCompensationStateChanged,
		procmsg.MOCompensationStateChangedPayload{
			CompensationID: "compensation-mo-6", MerchantOrderID: mo6.MerchantOrderID,
			AllocationID: mo6.ID, State: "SUCCEEDED",
			Cause:          procmsg.CompensationCauseCustomerCancelPreEffect,
			SucceededCount: 4, AllMerchantOrdersCompensated: false,
		})
	if terminal.State != processdomain.StateTerminal ||
		terminal.TerminalReason != processdomain.TerminalReasonCompletedPartial {
		t.Fatalf("six-MO terminal projection=(%s,%s), workflow=%+v",
			terminal.State, terminal.TerminalReason, terminal.ProcessState)
	}
	commandByType(t, terminal, procmsg.EffectIssueReceipt)

	// Sandbox exercises the production evidence shape without turning a TEST
	// reference into a claim that a real merchant effect occurred.
	for _, allocation := range []immutableMOAllocation{mo1, mo2, mo4, mo5} {
		evidence := procurement.tasks[allocation.MerchantOrderID].Evidence
		if evidence == nil || evidence.Kind != procurementdomain.PlacementEvidenceSandboxTest ||
			evidence.ClaimsExternalLiveEffect ||
			evidence.ActualAmountMinor != allocation.PassThroughMinor ||
			evidence.Currency != allocation.Currency ||
			strings.TrimSpace(evidence.ExternalOrderID) == "" ||
			strings.TrimSpace(evidence.ReceiptSafeRef) == "" {
			t.Fatalf("Sandbox TEST evidence lost its explicit meaning: %+v", evidence)
		}
	}

	// Cross-boundary money assertions: captures exist only for 1/2/4/5 at the
	// procurement-start gate; 4/5 are exact capture refunds; 3/6 are whole-MO
	// VOID decisions and only the last residual position calls PayPal void.
	wantCaptured := []int{1, 2, 4, 5}
	if len(payment.captures) != len(wantCaptured) {
		t.Fatalf("capture calls=%+v", payment.captures)
	}
	for index, ordinal := range wantCaptured {
		allocation := byOrdinal(ordinal)
		call := payment.captures[index]
		if call.MerchantOrderID != allocation.MerchantOrderID ||
			call.AllocationID != allocation.ID ||
			call.AmountMinor != allocation.CustomerGrossMinor || call.Currency != "USD" ||
			call.Stage != "PROCUREMENT_START" {
			t.Fatalf("capture %d=%+v, allocation=%+v", index, call, allocation)
		}
	}
	if _, found := payment.captureFor(mo3.MerchantOrderID); found {
		t.Fatal("failed pre-effect MO 3 was captured")
	}
	if _, found := payment.captureFor(mo6.MerchantOrderID); found {
		t.Fatal("cancelled pre-effect MO 6 was captured")
	}
	if len(payment.refunds) != 2 || len(payment.voids) != 1 ||
		payment.voids[0].MerchantOrderID != mo6.MerchantOrderID {
		t.Fatalf("provider refund/void effects: refunds=%+v voids=%+v",
			payment.refunds, payment.voids)
	}
	sort.Slice(payment.refunds, func(i, j int) bool {
		return payment.refunds[i].MerchantOrderID < payment.refunds[j].MerchantOrderID
	})
	for index, allocation := range []immutableMOAllocation{mo4, mo5} {
		refund := payment.refunds[index]
		capture, found := payment.captureFor(allocation.MerchantOrderID)
		if !found || refund.CaptureID != capture.CaptureID ||
			refund.AmountMinor != allocation.CustomerGrossMinor || refund.Currency != "USD" {
			t.Fatalf("refund %d=%+v capture=%+v", index, refund, capture)
		}
	}
	for _, allocation := range []immutableMOAllocation{mo3, mo4, mo5, mo6} {
		compensation := payment.compensations[allocation.MerchantOrderID]
		if compensation.AllocationID != allocation.ID ||
			compensation.AmountMinor != allocation.CustomerGrossMinor ||
			compensation.Currency != "USD" {
			t.Fatalf("whole-MO gross was not conserved: %+v", compensation)
		}
	}
	if len(logistics.recoveries) != 1 ||
		logistics.recoveries[0].MerchantOrderID != mo4.MerchantOrderID ||
		logistics.recoveries[0].AmountMinor != mo4.PassThroughMinor {
		t.Fatalf("delivery recovery evidence=%+v", logistics.recoveries)
	}
	if !reflect.DeepEqual(allocations, allocationSnapshot) {
		t.Fatalf("immutable MO allocations changed:\nstarted=%+v\nended=%+v",
			allocationSnapshot, allocations)
	}
}
