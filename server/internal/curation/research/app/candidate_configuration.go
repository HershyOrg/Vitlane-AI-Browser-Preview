package app

import (
	"context"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
)

type PrepareCandidateConfigurationInput struct {
	UserID        string
	SessionID     string
	CandidateID   string
	Configuration researchdomain.CandidateConfigurationInput
}

// PrepareCandidateConfiguration is the user-facing transaction boundary for
// Selection and AgencyOrder preparation. The session ownership/status lock
// and immutable configuration insert therefore commit or roll back together.
func (s *Service) PrepareCandidateConfiguration(
	ctx context.Context,
	input PrepareCandidateConfigurationInput,
) (researchdomain.CandidateConfiguration, error) {
	var configuration researchdomain.CandidateConfiguration
	err := s.transactor.WithinTransaction(
		ctx,
		func(txContext context.Context) error {
			session, prepareErr := s.sessions.GetForUpdate(
				txContext, input.UserID, input.SessionID,
			)
			if prepareErr != nil {
				return prepareErr
			}
			if session.Status != shoppingsessiondomain.SessionStatusReviewing {
				return shoppingsessiondomain.ErrCandidateNotPurchasable
			}
			if prepareErr = s.ensurePurchasableSessionPlan(
				txContext, input.UserID, session,
			); prepareErr != nil {
				return prepareErr
			}
			candidate, prepareErr := s.repository.GetCandidate(
				txContext, input.UserID, input.SessionID, input.CandidateID,
			)
			if prepareErr != nil {
				return prepareErr
			}
			if candidate.Orderability.Status == "POLICY_BLOCKED" ||
				candidate.Orderability.Status == "SETTLEMENT_CURRENCY_UNSUPPORTED" {
				return shoppingsessiondomain.ErrCandidateNotPurchasable
			}
			configuration, prepareErr = researchdomain.NewCandidateConfiguration(
				s.ids.NewID(), input.SessionID, candidate.ID,
				candidate.CandidateHash, input.UserID, candidate.VariantDiscovery,
				input.Configuration, s.clock.Now(),
			)
			if prepareErr != nil {
				return prepareErr
			}
			configuration, prepareErr = s.repository.InsertConfiguration(
				txContext, configuration,
			)
			return prepareErr
		},
	)
	return configuration, err
}

func (s *Service) ensurePurchasableSessionPlan(
	ctx context.Context,
	userID string,
	session shoppingsessiondomain.ShoppingSession,
) error {
	_, err := s.activeSessionPlan(
		ctx, userID, session, shoppingsessiondomain.ErrCandidateNotPurchasable,
	)
	return err
}
