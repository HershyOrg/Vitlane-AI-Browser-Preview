package app

import (
	"context"
	"encoding/json"
	"errors"

	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

type Repository interface {
	Create(ctx context.Context, session shoppingsessiondomain.ShoppingSession) error
	FindByTarget(ctx context.Context, userID, targetID string) (shoppingsessiondomain.ShoppingSession, error)
	Get(ctx context.Context, userID, sessionID string) (shoppingsessiondomain.ShoppingSession, error)
	GetForUpdate(ctx context.Context, userID, sessionID string) (shoppingsessiondomain.ShoppingSession, error)
	Save(ctx context.Context, previousVersion int64, session shoppingsessiondomain.ShoppingSession) error
}

type Service struct {
	repository Repository
	clock      sharedapp.Clock
	ids        sharedapp.IDGenerator
}

func NewService(repository Repository, clock sharedapp.Clock, ids sharedapp.IDGenerator) *Service {
	return &Service{repository: repository, clock: clock, ids: ids}
}

type TargetSnapshot struct {
	ProductVertical  string             `json:"productVertical,omitempty"`
	ID               string             `json:"id"`
	CurationID       string             `json:"curationId"`
	PlanID           string             `json:"planId"`
	Title            string             `json:"title"`
	NormalizedIntent string             `json:"normalizedIntent"`
	Category         string             `json:"category"`
	AllocatedBudget  shareddomain.Money `json:"allocatedBudget"`
	TargetHash       string             `json:"targetHash"`
	TargetHashSchema string             `json:"targetHashSchema"`
	ConfirmedAt      string             `json:"confirmedAt"`
}

type ResearchScopeSnapshot struct {
	Category     string              `json:"category"`
	Country      string              `json:"country"`
	City         string              `json:"city,omitempty"`
	AllowedItems []string            `json:"allowedItems"`
	BlockedItems []string            `json:"blockedItems"`
	MinPrice     *shareddomain.Money `json:"minPrice,omitempty"`
	MaxPrice     *shareddomain.Money `json:"maxPrice,omitempty"`
	ReferenceURL string              `json:"referenceUrl,omitempty"`
	URLMode      string              `json:"urlMode"`
}

type CreateReadyInput struct {
	UserID         string
	TargetID       string
	TargetSnapshot TargetSnapshot
	ScopeSnapshot  ResearchScopeSnapshot
}

func (s *Service) CreateReady(ctx context.Context, input CreateReadyInput) (shoppingsessiondomain.ShoppingSession, error) {
	existing, err := s.repository.FindByTarget(ctx, input.UserID, input.TargetID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, shoppingsessiondomain.ErrSessionNotFound) {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	targetJSON, err := json.Marshal(input.TargetSnapshot)
	if err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	scopeJSON, err := json.Marshal(input.ScopeSnapshot)
	if err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	session, err := shoppingsessiondomain.NewReadySession(
		shoppingsessiondomain.ShoppingSessionID(s.ids.NewID()),
		shoppingsessiondomain.PlanTargetID(input.TargetID),
		shoppingsessiondomain.UserID(input.UserID),
		targetJSON,
		scopeJSON,
		input.TargetSnapshot.TargetHash != "",
		s.clock.Now(),
	)
	if err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	if err := s.repository.Create(ctx, session); err != nil {
		// A concurrent retry may have won the unique constraint.
		existing, findErr := s.repository.FindByTarget(ctx, input.UserID, input.TargetID)
		if findErr == nil {
			return existing, nil
		}
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	return session, nil
}

func (s *Service) Get(ctx context.Context, userID, sessionID string) (shoppingsessiondomain.ShoppingSession, error) {
	return s.repository.Get(ctx, userID, sessionID)
}

func (s *Service) GetForUpdate(
	ctx context.Context,
	userID, sessionID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	return s.repository.GetForUpdate(ctx, userID, sessionID)
}

func (s *Service) FindByTarget(
	ctx context.Context,
	userID, targetID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	return s.repository.FindByTarget(ctx, userID, targetID)
}

func (s *Service) StartResearch(
	ctx context.Context,
	userID, sessionID, roundID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	session, err := s.repository.GetForUpdate(ctx, userID, sessionID)
	if err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	previousVersion := session.Version
	if err := session.StartResearch(roundID, s.clock.Now()); err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	if err := s.repository.Save(ctx, previousVersion, session); err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	return session, nil
}

func (s *Service) CompleteResearch(
	ctx context.Context,
	userID, sessionID, roundID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	session, err := s.repository.GetForUpdate(ctx, userID, sessionID)
	if err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	previousVersion := session.Version
	if err := session.CompleteResearch(roundID, s.clock.Now()); err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	if err := s.repository.Save(ctx, previousVersion, session); err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	return session, nil
}

func (s *Service) CancelResearch(
	ctx context.Context,
	userID, sessionID, roundID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	session, err := s.repository.GetForUpdate(ctx, userID, sessionID)
	if err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	previousVersion := session.Version
	if err := session.CancelResearch(roundID, s.clock.Now()); err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	if err := s.repository.Save(ctx, previousVersion, session); err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	return session, nil
}

func (s *Service) FailResearch(
	ctx context.Context,
	userID string,
	sessionID string,
	roundID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	session, err := s.repository.GetForUpdate(ctx, userID, sessionID)
	if err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	previousVersion := session.Version
	if err := session.FailResearch(roundID, s.clock.Now()); err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	if err := s.repository.Save(ctx, previousVersion, session); err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	return session, nil
}

func (s *Service) RequestResearchAgain(
	ctx context.Context,
	userID, sessionID, roundID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	session, err := s.repository.GetForUpdate(ctx, userID, sessionID)
	if err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	previousVersion := session.Version
	if err := session.RequestResearchAgain(roundID, s.clock.Now()); err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	if err := s.repository.Save(ctx, previousVersion, session); err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	return session, nil
}

func (s *Service) CancelResearchAgain(
	ctx context.Context,
	userID, sessionID, roundID, previousRoundID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	session, err := s.repository.GetForUpdate(ctx, userID, sessionID)
	if err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	previousVersion := session.Version
	if err := session.CancelResearchAgain(roundID, previousRoundID, s.clock.Now()); err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	if err := s.repository.Save(ctx, previousVersion, session); err != nil {
		return shoppingsessiondomain.ShoppingSession{}, err
	}
	return session, nil
}
