package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	liveapp "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/app"
	livedomain "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type handlerRepository struct {
	state    livedomain.State
	rejected []liveapp.Attempt
}

func (r *handlerRepository) Get(context.Context) (livedomain.State, error) { return r.state, nil }
func (r *handlerRepository) LockForAdmission(context.Context) (livedomain.State, error) {
	return r.state, nil
}
func (r *handlerRepository) Apply(_ context.Context, change liveapp.Change) (livedomain.State, error) {
	if change.ExpectedVersion != r.state.Version {
		return livedomain.State{}, livedomain.ErrVersionConflict
	}
	switch change.Scope {
	case livedomain.ScopeAll:
		r.state.OrderIssueKilled, r.state.PayPalMoneyKilled, r.state.MerchantEffectKilled = true, true, true
	case livedomain.ScopeOrderIssue:
		r.state.OrderIssueKilled = true
	case livedomain.ScopePayPalMoney:
		r.state.PayPalMoneyKilled = true
	case livedomain.ScopeMerchantEffect:
		r.state.MerchantEffectKilled = true
	}
	r.state.Version++
	r.state.ChangedAt, r.state.ChangedBy, r.state.Reason = change.ChangedAt, change.Actor, change.Reason
	return r.state, nil
}
func (r *handlerRepository) RecordRejected(_ context.Context, attempt liveapp.Attempt) error {
	r.rejected = append(r.rejected, attempt)
	return nil
}

type handlerClock struct{ now time.Time }

func (c handlerClock) Now() time.Time { return c.now }

type handlerIDs struct{ next int }

func (i *handlerIDs) NewID() string {
	i.next++
	return "00000000-0000-4000-8000-00000000090" + string(rune('0'+i.next))
}

func newTestHandler() (*Handler, *handlerRepository) {
	repository := &handlerRepository{state: livedomain.State{
		Version: 5, ChangedAt: time.Date(2026, 8, 29, 1, 0, 0, 0, time.UTC),
		ChangedBy: "operator-previous", Reason: "previous activation",
	}}
	service := liveapp.NewService(repository, liveapp.StaticPolicy{
		OrderIssue: true, PayPalMoney: true, MerchantEffect: true,
	}, handlerClock{now: time.Date(2026, 8, 29, 2, 0, 0, 0, time.UTC)}, &handlerIDs{})
	return NewHandler(service), repository
}

func TestGetReturnsCredentialFreeControlProjection(t *testing.T) {
	handler, _ := newTestHandler()
	recorder := httptest.NewRecorder()
	handler.Get(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/liveControl", nil))
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"schemaVersion":"vitlane.live-control.v1"`) ||
		strings.Contains(strings.ToLower(recorder.Body.String()), "secret") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestKillRequiresActorAndExactConfirmation(t *testing.T) {
	handler, repository := newTestHandler()
	unauthenticated := httptest.NewRecorder()
	handler.Kill(unauthenticated, httptest.NewRequest(http.MethodPost,
		"/api/v1/admin/liveControl/kill", bytes.NewBufferString(`{}`)))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}

	invalidRequest := httptest.NewRequest(http.MethodPost, "/api/v1/admin/liveControl/kill",
		bytes.NewBufferString(`{"scope":"ALL_NEW_LIVE_EFFECTS","confirmation":"kill live","reason":"provider incident","expectedVersion":5}`))
	invalidRequest = invalidRequest.WithContext(sharedapp.WithAuthenticatedUserID(invalidRequest.Context(), "operator-1"))
	invalid := httptest.NewRecorder()
	handler.Kill(invalid, invalidRequest)
	if invalid.Code != http.StatusUnprocessableEntity || len(repository.rejected) != 1 {
		t.Fatalf("invalid status=%d body=%s audits=%d", invalid.Code, invalid.Body.String(), len(repository.rejected))
	}

	validRequest := httptest.NewRequest(http.MethodPost, "/api/v1/admin/liveControl/kill",
		bytes.NewBufferString(`{"scope":"ALL_NEW_LIVE_EFFECTS","confirmation":"KILL PAYPAL LIVE ALL","reason":"provider incident","expectedVersion":5}`))
	validRequest = validRequest.WithContext(sharedapp.WithAuthenticatedUserID(validRequest.Context(), "operator-1"))
	valid := httptest.NewRecorder()
	handler.Kill(valid, validRequest)
	if valid.Code != http.StatusOK || repository.state.Version != 6 ||
		repository.state.ChangedBy != "operator-1" || !repository.state.OrderIssueKilled ||
		!repository.state.PayPalMoneyKilled || !repository.state.MerchantEffectKilled {
		t.Fatalf("valid status=%d body=%s state=%+v", valid.Code, valid.Body.String(), repository.state)
	}
}
