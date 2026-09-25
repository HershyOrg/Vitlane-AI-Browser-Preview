package app

import (
	"context"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

// CancelResearchRound closes a round whose work will not run.
func (s *Service) CancelResearchRound(
	ctx context.Context,
	userID string,
	roundID string,
) error {
	round, err := s.repository.GetRound(ctx, userID, roundID, false)
	if err != nil {
		return err
	}
	// 취소의 목적은 "일이 더 실행되지 않는 것"이다. round가 이미 terminal이면
	// 그 목적은 달성돼 있으므로 성공으로 취급한다. 여기서 오류를 돌리면 함께
	// 취소 중인 Job의 트랜잭션까지 롤백되어 취소 자체가 실패한다(2026-08-16
	// production 실측: FAILED round에 붙은 좀비 job의 취소 불가).
	if round.Status != researchdomain.RoundStatusRequested {
		return nil
	}
	return s.Cancel(ctx, userID, round.ShoppingSessionID)
}
