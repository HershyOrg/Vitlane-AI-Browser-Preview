package domain

import (
	"errors"
	"testing"
	"time"
)

func TestShoppingSessionResearchLifecycle(t *testing.T) {
	now := time.Date(2026, time.July, 18, 9, 0, 0, 0, time.UTC)
	session, err := NewReadySession(
		"session-1",
		"target-1",
		"user-1",
		[]byte(`{"title":"chair"}`),
		[]byte(`{"budget":{"amount":"100","currency":"USD"}}`),
		true,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := session.StartResearch("round-1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionStatusResearching ||
		session.CurrentResearchRoundID == nil ||
		*session.CurrentResearchRoundID != "round-1" {
		t.Fatalf("unexpected researching session: %#v", session)
	}
	if err := session.CancelResearch("other-round", now.Add(2*time.Minute)); !errors.Is(
		err, ErrResearchRoundMismatch,
	) {
		t.Fatalf("expected round mismatch, got %v", err)
	}
	if err := session.CancelResearch("round-1", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionStatusReady || session.CurrentResearchRoundID != nil {
		t.Fatalf("unexpected cancelled session: %#v", session)
	}

	if err := session.StartResearch("round-2", now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteResearch("round-2", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionStatusReviewing ||
		session.CurrentResearchRoundID == nil ||
		*session.CurrentResearchRoundID != "round-2" {
		t.Fatalf("unexpected reviewing session: %#v", session)
	}
}

func TestFailedInitialResearchRetainsTerminalRoundPointer(t *testing.T) {
	now := time.Date(2026, time.August, 14, 6, 0, 0, 0, time.UTC)
	session, err := NewReadySession(
		"session-1", "target-1", "user-1",
		[]byte(`{"title":"chair"}`), []byte(`{"country":"US"}`), true, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.StartResearch("round-1", now); err != nil {
		t.Fatal(err)
	}
	if err := session.FailResearch("round-1", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionStatusReady ||
		session.CurrentResearchRoundID == nil ||
		*session.CurrentResearchRoundID != "round-1" {
		t.Fatalf("failed session=%#v", session)
	}
}
