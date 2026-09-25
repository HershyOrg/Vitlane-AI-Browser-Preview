package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/livecontrol/domain"
)

type testRepository struct {
	state       domain.State
	rejected    []Attempt
	rejectedErr error
}

func (r *testRepository) Get(context.Context) (domain.State, error) { return r.state, nil }
func (r *testRepository) LockForAdmission(context.Context) (domain.State, error) {
	return r.state, nil
}
func (r *testRepository) Apply(_ context.Context, change Change) (domain.State, error) {
	if change.ExpectedVersion != r.state.Version {
		return domain.State{}, domain.ErrVersionConflict
	}
	switch change.Scope {
	case domain.ScopeAll:
		r.state.OrderIssueKilled, r.state.PayPalMoneyKilled,
			r.state.MerchantEffectKilled = change.Kill, change.Kill, change.Kill
	case domain.ScopeOrderIssue:
		r.state.OrderIssueKilled = change.Kill
	case domain.ScopePayPalMoney:
		r.state.PayPalMoneyKilled = change.Kill
	case domain.ScopeMerchantEffect:
		r.state.MerchantEffectKilled = change.Kill
	}
	r.state.Version++
	r.state.ChangedAt, r.state.ChangedBy, r.state.Reason = change.ChangedAt, change.Actor, change.Reason
	return r.state, nil
}
func (r *testRepository) RecordRejected(_ context.Context, attempt Attempt) error {
	r.rejected = append(r.rejected, attempt)
	return r.rejectedErr
}

func TestRejectedCommandFailsClosedWhenAuditCannotBePersisted(t *testing.T) {
	auditErr := errors.New("audit storage unavailable")
	repository := &testRepository{
		state: domain.State{Version: 1, OrderIssueKilled: true, PayPalMoneyKilled: true,
			MerchantEffectKilled: true, ChangedAt: time.Now().UTC()},
		rejectedErr: auditErr,
	}
	service := NewService(repository, StaticPolicy{OrderIssue: true},
		testClock{now: time.Now().UTC()}, testIDs{})
	_, err := service.Kill(context.Background(), ChangeRequest{
		Scope: string(domain.ScopeOrderIssue), Confirmation: "wrong phrase",
		Reason: "provider incident", ExpectedVersion: 1, Actor: "operator-1",
	})
	if !errors.Is(err, auditErr) || repository.state.Version != 1 {
		t.Fatalf("err=%v state=%+v", err, repository.state)
	}
}

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

type testIDs struct{}

func (testIDs) NewID() string { return "00000000-0000-4000-8000-000000000001" }

func TestRuntimeControlDefaultsFailClosedAndRequiresExactKillPhrase(t *testing.T) {
	repository := &testRepository{state: domain.State{
		Version: 1, OrderIssueKilled: true, PayPalMoneyKilled: true,
		MerchantEffectKilled: true, ChangedAt: time.Date(2026, 8, 29, 1, 0, 0, 0, time.UTC),
	}}
	service := NewService(repository, StaticPolicy{OrderIssue: true, PayPalMoney: true,
		MerchantEffect: true}, testClock{now: time.Date(2026, 8, 29, 2, 0, 0, 0, time.UTC)}, testIDs{})
	if allowed, revision, err := service.AllowLiveOrderIssue(context.Background()); err != nil || allowed || revision != 1 {
		t.Fatalf("default admission allowed=%v revision=%d err=%v", allowed, revision, err)
	}
	_, err := service.Kill(context.Background(), ChangeRequest{
		Scope: string(domain.ScopeAll), Confirmation: "kill paypal", Reason: "provider incident",
		ExpectedVersion: 1, Actor: "operator-1",
	})
	if !errors.Is(err, domain.ErrInvalid) || len(repository.rejected) != 1 {
		t.Fatalf("invalid command err=%v audits=%d", err, len(repository.rejected))
	}
}

func TestReactivationIsVersionedAndCanOpenIndependentScopes(t *testing.T) {
	repository := &testRepository{state: domain.State{
		Version: 4, OrderIssueKilled: true, PayPalMoneyKilled: true,
		MerchantEffectKilled: true, ChangedAt: time.Now().UTC(),
	}}
	service := NewService(repository, StaticPolicy{OrderIssue: true, PayPalMoney: true,
		MerchantEffect: true}, testClock{now: time.Now().UTC()}, testIDs{})
	snapshot, err := service.Reactivate(context.Background(), ChangeRequest{
		Scope: string(domain.ScopeOrderIssue), Confirmation: "REACTIVATE PAYPAL LIVE ORDERS",
		Reason: "approved pilot dogfood", ExpectedVersion: 4, Actor: "operator-1",
	})
	if err != nil || !snapshot.OrderIssue.Effective || snapshot.PayPalMoney.Effective ||
		snapshot.Version != 5 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if _, err := service.Reactivate(context.Background(), ChangeRequest{
		Scope: string(domain.ScopePayPalMoney), Confirmation: "REACTIVATE PAYPAL LIVE MONEY",
		Reason: "approved pilot dogfood", ExpectedVersion: 4, Actor: "operator-1",
	}); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale version err=%v", err)
	}
}

func TestReactivationCannotExceedStaticPolicy(t *testing.T) {
	repository := &testRepository{state: domain.State{
		Version: 1, OrderIssueKilled: true, PayPalMoneyKilled: true,
		MerchantEffectKilled: true, ChangedAt: time.Now().UTC(),
	}}
	service := NewService(repository, StaticPolicy{}, testClock{now: time.Now().UTC()}, testIDs{})
	_, err := service.Reactivate(context.Background(), ChangeRequest{
		Scope: string(domain.ScopeAll), Confirmation: "REACTIVATE PAYPAL LIVE ALL",
		Reason: "attempt while static flags are closed", ExpectedVersion: 1, Actor: "operator-1",
	})
	if !errors.Is(err, domain.ErrInvalid) || len(repository.rejected) != 1 || repository.state.Version != 1 {
		t.Fatalf("err=%v audits=%d state=%+v", err, len(repository.rejected), repository.state)
	}
}
