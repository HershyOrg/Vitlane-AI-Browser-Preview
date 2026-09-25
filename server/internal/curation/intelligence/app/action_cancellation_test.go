package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
)

type cancelTransactionKey struct{}

type cancelTestTransactor struct {
	rolledBack bool
}

func (t *cancelTestTransactor) WithinTransaction(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	err := fn(context.WithValue(ctx, cancelTransactionKey{}, true))
	t.rolledBack = err != nil
	return err
}

type cancelTestRepository struct {
	Repository
	sawTransaction bool
	cancelled      []CancelledTarget
}

func (r *cancelTestRepository) CancelAction(
	ctx context.Context,
	_, _ string,
	_ time.Time,
) ([]CancelledTarget, error) {
	r.sawTransaction, _ = ctx.Value(cancelTransactionKey{}).(bool)
	return r.cancelled, nil
}

type cancelTestProducts struct {
	ProductPort
	sawTransaction bool
	err            error
}

func (p *cancelTestProducts) CancelTarget(
	ctx context.Context,
	_ string,
	_ intelligencedomain.JobTarget,
) error {
	p.sawTransaction, _ = ctx.Value(cancelTransactionKey{}).(bool)
	return p.err
}

type cancelTestClock struct{}

func (cancelTestClock) Now() time.Time { return time.Unix(1, 0).UTC() }

func cancelTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCancelActionUsesOneTransactionForJobAndProductTarget(t *testing.T) {
	repository := &cancelTestRepository{cancelled: []CancelledTarget{{
		JobID: "job-1",
		Target: intelligencedomain.JobTarget{
			Kind: intelligencedomain.TargetPlanningTask,
			ID:   "task-1",
		},
	}}}
	products := &cancelTestProducts{}
	transactor := &cancelTestTransactor{}
	service := &Service{
		repository: repository, products: products,
		transactor: transactor, clock: cancelTestClock{}, logger: cancelTestLogger(),
	}

	cancelled, err := service.CancelAction(
		context.Background(), "user-1", "action-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 || !repository.sawTransaction || !products.sawTransaction {
		t.Fatalf(
			"cancelled=%d repository_tx=%t product_tx=%t",
			cancelled, repository.sawTransaction, products.sawTransaction,
		)
	}
}

func TestCancelActionRollsBackWhenProductTargetCannotClose(t *testing.T) {
	repository := &cancelTestRepository{cancelled: []CancelledTarget{{
		JobID: "job-1",
		Target: intelligencedomain.JobTarget{
			Kind: intelligencedomain.TargetResearchRound,
			ID:   "round-1",
		},
	}}}
	products := &cancelTestProducts{err: errors.New("target close failed")}
	transactor := &cancelTestTransactor{}
	service := &Service{
		repository: repository, products: products,
		transactor: transactor, clock: cancelTestClock{}, logger: cancelTestLogger(),
	}

	if _, err := service.CancelAction(
		context.Background(), "user-1", "action-1",
	); err == nil || !transactor.rolledBack {
		t.Fatalf("err=%v rolled_back=%t", err, transactor.rolledBack)
	}
}
