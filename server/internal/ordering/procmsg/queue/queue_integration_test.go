package queue_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	processdomain "github.com/vitlane/vitlane/server/internal/ordering/process/domain"
	processpostgres "github.com/vitlane/vitlane/server/internal/ordering/process/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg/queue"
	"github.com/vitlane/vitlane/server/internal/ordering/testfixture"
)

func TestEffectQueueAtomicityAndRecovery(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := testfixture.Open(t, ctx)
	testfixture.SeedOrder(t, ctx, db, now)
	testfixture.Exec(t, ctx, db, `CREATE TABLE test_owner_evidence(id text PRIMARY KEY,value text NOT NULL)`)
	store := processpostgres.NewStore(db)
	var wakes atomic.Int32
	q := queue.New(db, func() { wakes.Add(1) })
	issue := func(key string) procmsg.Delivery {
		t.Helper()
		err := db.WithinTransaction(ctx, func(tx context.Context) error {
			_, err := store.InsertEffect(tx, testfixture.OrderID, processdomain.EffectDraft{Type: "test.owner.effect.v1", Target: "SUPPORT", IdempotencyKey: key, Payload: map[string]any{"z": 2, "a": map[string]any{"value": 1}}}, now)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		ds, err := q.Claim(ctx, []string{"SUPPORT"}, now, 50)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range ds {
			if d.Effect.IdempotencyKey == key {
				return d
			}
		}
		t.Fatal("effect not claimed")
		return procmsg.Delivery{}
	}
	count := func(query string) int {
		t.Helper()
		var n int
		if err := db.DB.QueryRowContext(ctx, query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("owner-result-and-ack-rollback-together", func(t *testing.T) {
		d := issue("rollback")
		failure := errors.New("injected crash before commit")
		err := q.Consume(ctx, d, func(tx context.Context, e procmsg.ProcessEffect) error {
			if _, err := db.Queryer(tx).ExecContext(tx, `INSERT INTO test_owner_evidence VALUES('rollback','verified')`); err != nil {
				return err
			}
			if err := q.Report(tx, e, "SUCCEEDED", "", nil, "verified", now); err != nil {
				return err
			}
			return failure
		})
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if count(`SELECT count(*) FROM test_owner_evidence`) != 0 || count(`SELECT count(*) FROM order_process_events`) != 0 || wakes.Load() != 0 {
			t.Fatal("rolled back owner write leaked a fact or wake")
		}
		calls := 0
		accept := func(tx context.Context, e procmsg.ProcessEffect) error {
			calls++
			if _, err := db.Queryer(tx).ExecContext(tx, `INSERT INTO test_owner_evidence VALUES('rollback','verified')`); err != nil {
				return err
			}
			return q.Report(tx, e, "SUCCEEDED", "", nil, "verified", now)
		}
		if err = q.Consume(ctx, d, accept); err != nil {
			t.Fatal(err)
		}
		if err = q.Consume(ctx, d, accept); err != nil {
			t.Fatal(err)
		}
		if calls != 1 || count(`SELECT count(*) FROM order_process_events`) != 1 || wakes.Load() != 1 {
			t.Fatal("duplicate consumed effect re-executed")
		}
	})
	t.Run("unknown-and-stale-owner-fact", func(t *testing.T) {
		d := issue("unknown")
		if err := q.WithDelivery(ctx, d, func(tx context.Context, e procmsg.ProcessEffect) (procmsg.Disposition, error) {
			return procmsg.RetryDelivery, q.Report(tx, e, "EFFECT_UNKNOWN", "PROVIDER_GET_REQUIRED", nil, "unknown", now)
		}); err != nil {
			t.Fatal(err)
		}
		ds, err := q.Claim(ctx, []string{"SUPPORT"}, now.Add(time.Minute), 50)
		if err != nil || len(ds) != 1 {
			t.Fatalf("reclaim: %v %+v", err, ds)
		}
		newDelivery := ds[0]
		if newDelivery.ClaimVersion <= d.ClaimVersion || newDelivery.Effect.ID != d.Effect.ID {
			t.Fatal("retry changed business identity")
		}
		err = q.Consume(ctx, d, func(context.Context, procmsg.ProcessEffect) error { t.Fatal("stale claim ran"); return nil })
		if !errors.Is(err, procmsg.ErrStaleDelivery) {
			t.Fatalf("stale ACK accepted: %v", err)
		}
		if err = q.Observe(ctx, d, func(tx context.Context, e procmsg.ProcessEffect) error {
			return q.Report(tx, e, "SUCCEEDED", "", nil, "verified", now)
		}); err != nil {
			t.Fatal(err)
		}
		var state string
		var version int64
		if err := db.DB.QueryRowContext(ctx, `SELECT delivery_state,claim_version FROM order_process_effects WHERE id=$1`, d.Effect.ID).Scan(&state, &version); err != nil {
			t.Fatal(err)
		}
		if state != "PENDING" || version != newDelivery.ClaimVersion {
			t.Fatal("late fact acknowledged a newer delivery")
		}
		if err = q.Consume(ctx, newDelivery, func(tx context.Context, e procmsg.ProcessEffect) error {
			return q.Report(tx, e, "SUCCEEDED", "", nil, "verified", now)
		}); err != nil {
			t.Fatal(err)
		}
		if count(`SELECT count(*) FROM order_process_events WHERE payload->>'code'='PROVIDER_GET_REQUIRED'`) != 1 {
			t.Fatal("UNKNOWN observation lost")
		}
		if count(`SELECT count(*) FROM order_process_events WHERE payload->>'outcome'='SUCCEEDED'`) != 2 {
			t.Fatal("verified result was duplicated or lost")
		}
	})
	t.Run("input-immutable-and-reconstructed-from-jsonb", func(t *testing.T) {
		d := issue("immutable")
		if _, err := db.DB.ExecContext(ctx, `UPDATE order_process_effects SET payload='{}' WHERE id=$1`, d.Effect.ID); err == nil {
			t.Fatal("effect input was mutable")
		}
		if procmsg.PayloadHash(d.Effect.Payload) != d.Effect.InputHash {
			t.Fatal("JSONB changed canonical payload hash")
		}
		forged := d
		forged.Effect.InputHash = "forged"
		if err := q.Consume(ctx, forged, func(context.Context, procmsg.ProcessEffect) error { t.Fatal("forged input executed"); return nil }); !errors.Is(err, procmsg.ErrEffectInvalid) {
			t.Fatal(err)
		}
		if err := q.Consume(ctx, d, func(context.Context, procmsg.ProcessEffect) error { return nil }); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("poll-recovers-without-wake", func(t *testing.T) {
		d := issue("no-wake")
		quiet := queue.New(db, nil)
		if err := quiet.Retry(ctx, d, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		ds, err := quiet.Claim(ctx, []string{"SUPPORT"}, now, 50)
		if err != nil || len(ds) != 1 {
			t.Fatalf("no-wake recovery: %v %+v", err, ds)
		}
		if err := quiet.Consume(ctx, ds[0], func(context.Context, procmsg.ProcessEffect) error { return nil }); err != nil {
			t.Fatal(err)
		}
	})
}

type blockingConsumer struct {
	q       *queue.Queue
	started chan string
	release chan struct{}
}

func (c blockingConsumer) Accept(ctx context.Context, d procmsg.Delivery) error {
	c.started <- d.Effect.IdempotencyKey
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.release:
	}
	return c.q.Consume(ctx, d, func(context.Context, procmsg.ProcessEffect) error { return nil })
}

func TestEffectWorkerDoesNotSerializeSameOrderIO(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Now().UTC()
	db := testfixture.Open(t, ctx)
	testfixture.SeedOrder(t, ctx, db, now)
	store := processpostgres.NewStore(db)
	q := queue.New(db, nil)
	for _, key := range []string{"first", "sibling"} {
		if err := db.WithinTransaction(ctx, func(tx context.Context) error {
			_, err := store.InsertEffect(tx, testfixture.OrderID, processdomain.EffectDraft{Type: "test.owner.effect.v1", Target: "SUPPORT", IdempotencyKey: key, Payload: map[string]any{}}, now)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	consumer := blockingConsumer{q, make(chan string, 2), make(chan struct{})}
	worker := queue.NewWorker(q)
	worker.Register("SUPPORT", consumer)
	worker.SetConcurrency(2)
	done := make(chan error, 1)
	go func() { done <- worker.Tick(ctx) }()
	defer func() {
		close(consumer.release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for range 2 {
		select {
		case <-consumer.started:
		case <-time.After(3 * time.Second):
			t.Fatal("same Order external work serialized")
		}
	}
	// Both Owner calls are outside DB transactions, so independent local work can
	// still acquire a connection while they are waiting for provider observations.
	if err := db.WithinTransaction(ctx, func(tx context.Context) error { _, err := db.Queryer(tx).ExecContext(tx, `SELECT 1`); return err }); err != nil {
		t.Fatal(err)
	}
}

func TestEffectPrivateInputIdentityAndImmutability(t *testing.T) {
	ctx := context.Background()
	db := testfixture.Open(t, ctx)
	testfixture.SeedOrder(t, ctx, db, time.Now().UTC())
	inputs := queue.NewActionInputs(db, procmsg.TargetProcurement)
	// An order-level test input avoids manufacturing an unrelated MO row.
	r := procmsg.ActionRequest{ID: "private-input", Kind: procmsg.RequestRetryEffect, AgencyOrderID: testfixture.OrderID, ActorID: testfixture.OperatorID, ActorRole: "OPERATOR", ReferenceID: "target-effect"}
	original := map[string]string{"note": "private operator evidence"}
	staged, err := inputs.Stage(ctx, r, original)
	if err != nil {
		t.Fatal(err)
	}
	again, err := inputs.Stage(ctx, r, original)
	if err != nil || again.InputRef != staged.InputRef {
		t.Fatalf("input replay=%+v %v", again, err)
	}
	if _, err := inputs.Stage(ctx, r, map[string]string{"note": "different"}); !errors.Is(err, procmsg.ErrRequestInvalid) {
		t.Fatalf("body conflict=%v", err)
	}
	if _, err := db.DB.ExecContext(ctx, `UPDATE procurement_process_inputs SET payload='{}' WHERE id=$1`, staged.InputRef); err == nil {
		t.Fatal("immutable Owner input changed")
	}
	if _, err := inputs.Load(ctx, staged); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*procmsg.ActionRequest){func(r *procmsg.ActionRequest) { r.ActorID = testfixture.UserID }, func(r *procmsg.ActionRequest) { r.ReferenceID = "another" }, func(r *procmsg.ActionRequest) { r.Kind = procmsg.RequestKind("ABANDON_EFFECT") }, func(r *procmsg.ActionRequest) { r.Decision = "different" }} {
		forged := staged
		mutate(&forged)
		if _, err := inputs.Load(ctx, forged); err == nil {
			t.Fatalf("changed input scope loaded: %+v", forged)
		}
	}
	var count int
	if err := db.DB.QueryRowContext(ctx, `SELECT count(*) FROM order_process_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("staging ran action: count=%d err=%v", count, err)
	}
}
