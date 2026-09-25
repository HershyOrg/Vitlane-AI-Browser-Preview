package domain

import (
	"errors"
	"time"
)

var (
	ErrTargetNotConfirmed      = errors.New("TARGET_NOT_CONFIRMED")
	ErrSessionNotFound         = errors.New("SESSION_NOT_FOUND")
	ErrSessionNotReady         = errors.New("SESSION_NOT_READY")
	ErrSessionNotResearching   = errors.New("SESSION_NOT_RESEARCHING")
	ErrResearchRoundMismatch   = errors.New("RESEARCH_ROUND_MISMATCH")
	ErrSessionVersionConflict  = errors.New("VERSION_CONFLICT")
	ErrCandidateNotPurchasable = errors.New("CANDIDATE_NOT_PURCHASABLE")
)

type ShoppingSessionID string
type PlanTargetID string
type UserID string

type SessionStatus string

const (
	SessionStatusReady       SessionStatus = "READY"
	SessionStatusResearching SessionStatus = "RESEARCHING"
	SessionStatusReviewing   SessionStatus = "REVIEWING"
)

type ShoppingSession struct {
	ID                     ShoppingSessionID `json:"id"`
	PlanTargetID           PlanTargetID      `json:"planTargetId"`
	UserID                 UserID            `json:"userId"`
	TargetSnapshot         []byte            `json:"-"`
	ResearchScopeSnapshot  []byte            `json:"-"`
	Status                 SessionStatus     `json:"status"`
	CurrentResearchRoundID *string           `json:"currentResearchRoundId,omitempty"`
	Version                int64             `json:"version"`
	CreatedAt              time.Time         `json:"createdAt"`
	UpdatedAt              time.Time         `json:"updatedAt"`
}

func (s *ShoppingSession) StartResearch(roundID string, now time.Time) error {
	if s.Status != SessionStatusReady {
		return ErrSessionNotReady
	}
	s.Status = SessionStatusResearching
	s.CurrentResearchRoundID = &roundID
	s.Version++
	s.UpdatedAt = now
	return nil
}

func (s *ShoppingSession) CompleteResearch(roundID string, now time.Time) error {
	if s.Status != SessionStatusResearching {
		return ErrSessionNotResearching
	}
	if s.CurrentResearchRoundID == nil || *s.CurrentResearchRoundID != roundID {
		return ErrResearchRoundMismatch
	}
	s.Status = SessionStatusReviewing
	s.Version++
	s.UpdatedAt = now
	return nil
}

func (s *ShoppingSession) CancelResearch(roundID string, now time.Time) error {
	if s.Status != SessionStatusResearching {
		return ErrSessionNotResearching
	}
	if s.CurrentResearchRoundID == nil || *s.CurrentResearchRoundID != roundID {
		return ErrResearchRoundMismatch
	}
	s.Status = SessionStatusReady
	s.CurrentResearchRoundID = nil
	s.Version++
	s.UpdatedAt = now
	return nil
}

// FailResearch releases initial Research work while retaining the terminal
// Round pointer for replay, diagnostics and the workspace failure projection.
// A later explicit start may replace the pointer with a new Round.
func (s *ShoppingSession) FailResearch(roundID string, now time.Time) error {
	if s.Status != SessionStatusResearching {
		return ErrSessionNotResearching
	}
	if s.CurrentResearchRoundID == nil || *s.CurrentResearchRoundID != roundID {
		return ErrResearchRoundMismatch
	}
	s.Status = SessionStatusReady
	s.Version++
	s.UpdatedAt = now
	return nil
}

func (s *ShoppingSession) RequestResearchAgain(roundID string, now time.Time) error {
	if s.Status != SessionStatusReviewing {
		return ErrSessionNotReady
	}
	s.Status = SessionStatusResearching
	s.CurrentResearchRoundID = &roundID
	s.Version++
	s.UpdatedAt = now
	return nil
}

func (s *ShoppingSession) CancelResearchAgain(
	roundID, previousRoundID string,
	now time.Time,
) error {
	if s.Status != SessionStatusResearching ||
		s.CurrentResearchRoundID == nil || *s.CurrentResearchRoundID != roundID {
		return ErrResearchRoundMismatch
	}
	s.Status = SessionStatusReviewing
	s.CurrentResearchRoundID = &previousRoundID
	s.Version++
	s.UpdatedAt = now
	return nil
}

func NewReadySession(
	id ShoppingSessionID,
	targetID PlanTargetID,
	userID UserID,
	targetSnapshot, scopeSnapshot []byte,
	confirmed bool,
	now time.Time,
) (ShoppingSession, error) {
	if !confirmed {
		return ShoppingSession{}, ErrTargetNotConfirmed
	}
	return ShoppingSession{
		ID: id, PlanTargetID: targetID, UserID: userID,
		TargetSnapshot: targetSnapshot, ResearchScopeSnapshot: scopeSnapshot,
		Status: SessionStatusReady, Version: 1, CreatedAt: now, UpdatedAt: now,
	}, nil
}
