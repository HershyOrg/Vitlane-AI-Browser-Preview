package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	processapp "github.com/vitlane/vitlane/server/internal/ordering/process/app"
	processdomain "github.com/vitlane/vitlane/server/internal/ordering/process/domain"
	processpg "github.com/vitlane/vitlane/server/internal/ordering/process/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"github.com/vitlane/vitlane/server/internal/ordering/testfixture"
)

func stageReducerRequests(t *testing.T, h *reducerHarness, requests ...procmsg.ActionRequest) {
	t.Helper()
	if err := h.db.WithinTransaction(h.ctx, func(tx context.Context) error {
		if err := h.store.LockOrder(tx, testfixture.OrderID); err != nil {
			return err
		}
		for _, request := range requests {
			if _, err := h.store.AppendRequest(tx, request, h.clock.Time); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func processCursor(t *testing.T, h *reducerHarness) (version, seq int64) {
	t.Helper()
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT version,last_applied_seq FROM agency_order_processes WHERE agency_order_id=$1`, testfixture.OrderID).Scan(&version, &seq); err != nil {
		t.Fatal(err)
	}
	return
}

func assertRequestOutcome(t *testing.T, h *reducerHarness, request, want string) {
	t.Helper()
	r, err := h.processor.Receipt(h.ctx, testfixture.OrderID, request)
	if err != nil || r.Outcome != want {
		t.Fatalf("request %s: outcome=%s want=%s err=%v", request, r.Outcome, want, err)
	}
}

func TestReducerProcessorCommitsExactlyOneEvent(t *testing.T) {
	for _, purchaseFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("purchase-first=%t", purchaseFirst), func(t *testing.T) {
			h := newReducerHarness(t)
			version, seq := processCursor(t, h)
			first := h.request("purchase", procmsg.RequestPurchase)
			second := h.request("cancel", procmsg.RequestCancel)
			if !purchaseFirst {
				first, second = second, first
			}
			stageReducerRequests(t, h, first, second)
			outcome, err := h.processor.ReduceWithLock(h.ctx, testfixture.OrderID)
			if err != nil || !outcome.Applied || outcome.EventSeq != seq+1 {
				t.Fatalf("first decision: %+v err=%v", outcome, err)
			}
			if v, s := processCursor(t, h); v != version+1 || s != seq+1 {
				t.Fatalf("consumed more than one event: version=%d seq=%d", v, s)
			}
			assertRequestOutcome(t, h, first.ID, "ACCEPTED")
			assertRequestOutcome(t, h, second.ID, "RECEIVED")
			var effects int
			if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM order_process_effects f JOIN order_process_events e ON e.id=f.caused_by_event_id WHERE e.agency_order_id=$1 AND e.seq=$2`, testfixture.OrderID, seq+1).Scan(&effects); err != nil || effects != 1 {
				t.Fatalf("first event's committed effect: count=%d err=%v", effects, err)
			}
			outcome, err = h.processor.ReduceWithLock(h.ctx, testfixture.OrderID)
			if err != nil || !outcome.Applied || outcome.EventSeq != seq+2 || outcome.EffectsIssued != 0 {
				t.Fatalf("conflicting second decision: %+v err=%v", outcome, err)
			}
			assertRequestOutcome(t, h, second.ID, "REJECTED")
			outcome, err = h.processor.ReduceWithLock(h.ctx, testfixture.OrderID)
			if err != nil || outcome.Applied {
				t.Fatalf("empty queue made a decision: %+v err=%v", outcome, err)
			}
			if v, s := processCursor(t, h); v != version+2 || s != seq+2 {
				t.Fatalf("incorrect final cursor: version=%d seq=%d", v, s)
			}
		})
	}
}

func TestReducerProcessorTickDrainsWithoutAnotherPoll(t *testing.T) {
	h := newReducerHarness(t)
	version, seq := processCursor(t, h)
	requests := []procmsg.ActionRequest{h.request("purchase", procmsg.RequestPurchase)}
	for i := 0; i < 7; i++ {
		requests = append(requests, h.request(fmt.Sprintf("change-%d", i), procmsg.RequestManualDecision))
	}
	stageReducerRequests(t, h, requests...)
	// This callback runs while Tick is still draining. Read the committed state
	// through another connection and verify that the Order lock is already free.
	var wakeErr error
	wakes := 0
	h.processor.SetWakes(func() {
		wakes++
		wakeErr = h.db.WithinTransaction(h.ctx, func(tx context.Context) error {
			var unlocked bool
			if err := h.db.Queryer(tx).QueryRowContext(tx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "order_processor:"+testfixture.OrderID).Scan(&unlocked); err != nil {
				return err
			}
			var current int64
			if err := h.db.Queryer(tx).QueryRowContext(tx, `SELECT version FROM agency_order_processes WHERE agency_order_id=$1`, testfixture.OrderID).Scan(&current); err != nil {
				return err
			}
			if !unlocked || current != version+1 {
				return fmt.Errorf("wake before individual commit/unlock: unlocked=%t version=%d", unlocked, current)
			}
			return nil
		})
	}, nil)
	if err := h.processor.Tick(h.ctx); err != nil {
		t.Fatal(err)
	}
	if wakes != 1 || wakeErr != nil {
		t.Fatalf("per-event commit wake: count=%d err=%v", wakes, wakeErr)
	}
	if v, s := processCursor(t, h); v != version+int64(len(requests)) || s != seq+int64(len(requests)) {
		t.Fatalf("one poll did not drain separate decisions: version=%d seq=%d", v, s)
	}
	var count int
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM order_process_decisions d JOIN order_process_events e ON e.agency_order_id=d.agency_order_id AND e.applied_version=d.version WHERE d.agency_order_id=$1 AND d.version>$2 AND d.seq_from=d.seq_to AND d.seq_from=e.seq`, testfixture.OrderID, version).Scan(&count); err != nil || count != len(requests) {
		t.Fatalf("one event per decision audit: count=%d err=%v", count, err)
	}
	assertRequestOutcome(t, h, "purchase", "ACCEPTED")
	for _, r := range requests[1:] {
		assertRequestOutcome(t, h, r.ID, "REJECTED")
	}
}

func TestReducerProcessorSubmitKeepsReceiptBehindEarlierEvent(t *testing.T) {
	h := newReducerHarness(t)
	stageReducerRequests(t, h, h.request("purchase", procmsg.RequestPurchase))
	r, err := h.processor.Submit(h.ctx, h.request("cancel", procmsg.RequestCancel))
	if err != nil || r.Outcome != "RECEIVED" || r.Guidance.CustomerAction != "WAIT" {
		t.Fatalf("request behind an earlier event lost its durable receipt: %+v err=%v", r, err)
	}
	assertRequestOutcome(t, h, "purchase", "ACCEPTED")
	if err := h.processor.Tick(h.ctx); err != nil {
		t.Fatal(err)
	}
	resolved, err := h.processor.Receipt(h.ctx, testfixture.OrderID, r.RequestID)
	if err != nil || resolved.Outcome != "REJECTED" || resolved.Guidance.ReasonCode != "EFFECT_IN_PROGRESS" || resolved.FlowID != r.FlowID {
		t.Fatalf("queued conflict was not resolved in the original flow: %+v err=%v", resolved, err)
	}
}

type failOneRequestStore struct {
	*processpg.Store
	requestID string
	failure   error
}

func (s failOneRequestStore) SaveDecision(ctx context.Context, input processapp.DecisionInput, decision processdomain.Decision, effects []processapp.EffectWrite, now time.Time) (processapp.DecisionOutcome, error) {
	var request procmsg.ActionRequest
	if input.Event.Type == procmsg.EventActionRequested && json.Unmarshal(input.Event.Payload, &request) == nil && request.ID == s.requestID {
		return processapp.DecisionOutcome{}, s.failure
	}
	return s.Store.SaveDecision(ctx, input, decision, effects, now)
}

func TestReducerProcessorLaterFailurePreservesEarlierCommit(t *testing.T) {
	h := newReducerHarness(t)
	version, seq := processCursor(t, h)
	stageReducerRequests(t, h, h.request("purchase", procmsg.RequestPurchase), h.request("claim", procmsg.RequestClaimTask))
	failure := errors.New("fail after second event's effects and receipt were written")
	broken := processapp.NewOrderProcessor(failOneRequestStore{Store: h.store, requestID: "claim", failure: failure}, h.clock)
	if err := broken.Tick(h.ctx); !errors.Is(err, failure) {
		t.Fatalf("lost failed event: %v", err)
	}
	if v, s := processCursor(t, h); v != version+1 || s != seq+1 {
		t.Fatalf("earlier commit rolled back or failed event consumed: version=%d seq=%d", v, s)
	}
	assertRequestOutcome(t, h, "purchase", "ACCEPTED")
	assertRequestOutcome(t, h, "claim", "RECEIVED")
	var effects int
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM order_process_effects WHERE request_id='claim'`).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("failed event leaked effects: count=%d err=%v", effects, err)
	}
	// A fresh processor resumes the failed event without replaying the first one.
	restarted := processapp.NewOrderProcessor(h.store, h.clock)
	if err := restarted.Tick(h.ctx); err != nil {
		t.Fatal(err)
	}
	assertRequestOutcome(t, h, "claim", "ACCEPTED")
	if v, s := processCursor(t, h); v != version+2 || s != seq+2 {
		t.Fatalf("restart skipped or repeated a decision: version=%d seq=%d", v, s)
	}
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM order_process_effects WHERE request_id IN ('purchase','claim')`).Scan(&effects); err != nil || effects != 2 {
		t.Fatalf("restart duplicated effects: count=%d err=%v", effects, err)
	}
}

func TestReducerProcessorFundingFactAloneCannotGrantPurchase(t *testing.T) {
	h := newReducerHarness(t)
	h.submit(t, h.request("purchase", procmsg.RequestPurchase))
	h.deliver(t, "PROCUREMENT")
	h.reduce(t)
	h.deliver(t, "LOGISTICS")
	h.reduce(t)
	h.deliver(t, "PAYMENT")
	// Payment commits its funding fact and its effect report in one Owner TX.
	// The processor must remain safe after committing only the first observation.
	var reportSeq int64
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT seq FROM order_process_events WHERE agency_order_id=$1 AND type=$2 AND payload->>'effectType'=$3 AND payload->>'outcome'='SUCCEEDED'`, testfixture.OrderID, procmsg.EventEffectReported, procmsg.EffectEnsureMOFunding).Scan(&reportSeq); err != nil {
		t.Fatal(err)
	}
	for {
		_, seq := processCursor(t, h)
		if seq >= reportSeq-1 {
			break
		}
		if outcome, err := h.processor.ReduceWithLock(h.ctx, testfixture.OrderID); err != nil || !outcome.Applied {
			t.Fatalf("funding observations did not advance: %+v err=%v", outcome, err)
		}
	}
	var state, rawOutcome string
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT process_state->'merchantOrders'->$2->>'fundingState', (SELECT receipt->>'outcome' FROM order_process_requests WHERE agency_order_id=$1 AND request_id='purchase') FROM agency_order_processes WHERE agency_order_id=$1`, testfixture.OrderID, h.mo).Scan(&state, &rawOutcome); err != nil || state != "ACTIVE" || rawOutcome == "COMPLETED" {
		t.Fatalf("intermediate funding fact: state=%s outcome=%s err=%v", state, rawOutcome, err)
	}
	var grants int
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM order_process_effects WHERE type=$1`, procmsg.EffectGrantMerchantPurchase).Scan(&grants); err != nil || grants != 0 {
		t.Fatalf("funding fact granted execution before matching report: grants=%d err=%v", grants, err)
	}
	if outcome, err := h.processor.ReduceWithLock(h.ctx, testfixture.OrderID); err != nil || outcome.EventSeq != reportSeq {
		t.Fatalf("matching funding result: %+v err=%v", outcome, err)
	}
	if err := h.db.DB.QueryRowContext(h.ctx, `SELECT count(*) FROM order_process_effects WHERE type=$1`, procmsg.EffectGrantMerchantPurchase).Scan(&grants); err != nil || grants != 1 {
		t.Fatalf("verified funding did not grant exactly once: grants=%d err=%v", grants, err)
	}
}
