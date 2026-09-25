package app

import (
	"context"
	"errors"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

// FailResearchRound consumes a terminal IntelligenceJob failure into the
// Research-owned lifecycle. CandidatePool is deliberately untouched. An
// initial failure releases the Session to READY; a re-research failure restores
// the previous completed Round and REVIEWING projection.
func (s *Service) FailResearchRound(
	ctx context.Context,
	userID string,
	roundID string,
	reasonCode string,
	retryable bool,
) error {
	err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		round, err := s.repository.GetRound(txContext, userID, roundID, true)
		if err != nil {
			return err
		}
		// Delivery is at-least-once. 이미 종결된 Round의 terminal fact가
		// 우선하며 늦은 실패 보고는 이를 덮지 않는다. 다만 여기서 오류를
		// 돌리면 같은 트랜잭션의 Job 종결까지 롤백되어, 실행 불가능한 Job이
		// 재시도만 반복하다 deadline으로 소진되는 좀비가 된다(2026-08-16
		// production 실측). 종결 Round에 대한 실패 보고는 no-op으로 흡수한다.
		if round.Status != researchdomain.RoundStatusRequested {
			return nil
		}
		if err := round.Fail(reasonCode, retryable, s.clock.Now()); err != nil {
			return err
		}
		if err := s.repository.UpdateRound(txContext, round); err != nil {
			return err
		}

		feedback, feedbackErr := s.repository.GetFeedbackForRound(
			txContext, userID, round.ID, true,
		)
		if feedbackErr == nil {
			previous, readErr := s.repository.GetRound(
				txContext, userID, feedback.PreviousRoundID, true,
			)
			if readErr != nil {
				return readErr
			}
			if restoreErr := previous.Restore(
				feedback.PreviousRoundStatus, s.clock.Now(),
			); restoreErr != nil {
				return restoreErr
			}
			if updateErr := s.repository.UpdateRound(txContext, previous); updateErr != nil {
				return updateErr
			}
			if cancelErr := feedback.Cancel(s.clock.Now()); cancelErr != nil {
				return cancelErr
			}
			if updateErr := s.repository.UpdateFeedback(txContext, feedback); updateErr != nil {
				return updateErr
			}
			_, sessionErr := s.sessions.CancelResearchAgain(
				txContext, userID, round.ShoppingSessionID,
				round.ID, previous.ID,
			)
			return sessionErr
		}
		if !errors.Is(feedbackErr, researchdomain.ErrFeedbackNotFound) {
			return feedbackErr
		}
		_, err = s.sessions.FailResearch(
			txContext, userID, round.ShoppingSessionID, round.ID,
		)
		return err
	})
	if err == nil {
		s.logger.WarnContext(ctx, "research round failed",
			"event", "research.failed", "round_id", roundID,
			"reason", reasonCode, "retryable", retryable)
	}
	return err
}
