package app

import (
	"context"
	"encoding/json"
	"fmt"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// GetContextForIntelligence reads a round the Server dispatched work for. The
// IntelligenceJob remains the control-plane authority for that exact round;
// product observations and Candidate materialization are exclusively handled
// by RunCatalogResearchForIntelligence.
func (s *Service) GetContextForIntelligence(
	ctx context.Context,
	userID string,
	jobID string,
	roundID string,
) (ContextResult, error) {
	if !uuidPattern.MatchString(jobID) || !uuidPattern.MatchString(roundID) {
		return ContextResult{}, researchdomain.ErrRoundNotFound
	}
	round, err := s.repository.GetRound(ctx, userID, roundID, false)
	if err != nil {
		return ContextResult{}, err
	}
	if round.Status != researchdomain.RoundStatusRequested {
		// 종결된 round 읽기는 재시도로 달라지지 않는 사실 충돌이다. fault로
		// 분류하지 않으면 워커가 INTERNAL_FAILURE(retryable)로 오분류해 시도만
		// 반복하고, 사용자에게는 원인 불명 반복 실패로 보인다(2026-08-16
		// production 좀비 진단 지연).
		return ContextResult{}, fault.Wrap(
			researchdomain.ErrRoundClosed, fault.Conflict,
			"RESEARCH_ROUND_CLOSED", false,
		)
	}
	var researchContext ResearchContext
	if err := json.Unmarshal(round.ContextSnapshot, &researchContext); err != nil {
		return ContextResult{}, fmt.Errorf("decode research context: %w", err)
	}
	if err := s.attachExecutionQuery(ctx, userID, roundID, &researchContext); err != nil {
		return ContextResult{}, err
	}
	if s.liveCatalog != nil && s.liveCatalog.workspace != nil {
		state, err := s.liveCatalog.workspace.LoadCatalogWorkspaceStateV2(ctx, userID, researchContext.Target.CurationID)
		if err != nil {
			return ContextResult{}, err
		}
		n, pending := 0, 0
		for _, c := range state.Candidates {
			if c.PlanTargetID == researchContext.Target.ID {
				n++
				if c.EvaluationRoundID == roundID && c.Assessment.AxisAssessment == nil {
					pending++
				}
			}
		}
		if n >= 50 && pending == 0 {
			return ContextResult{}, fault.New(fault.Conflict, "RESEARCH_CANDIDATE_CAPACITY_REACHED", false)
		}
	}
	feedback, feedbackRequired, err := s.feedbackForRound(ctx, userID, round.ID)
	if err != nil {
		return ContextResult{}, err
	}
	if feedbackRequired && feedback.Status != researchdomain.FeedbackStatusActive {
		return ContextResult{}, researchdomain.ErrFeedbackInvalid
	}
	s.logger.InfoContext(ctx, "research context read",
		"event", "research.context_read", "result", "success",
		"user_id", userID, "agent_provenance", "intelligence-job:"+jobID,
		"round_id", round.ID, "session_id", round.ShoppingSessionID)
	result := ContextResult{
		ContextSchema: round.ContextSchema, ContextVersion: round.ContextVersion,
		ContextHash: round.ContextHash, Context: researchContext,
		FeedbackRequired: feedbackRequired,
	}
	if feedbackRequired {
		result.FeedbackSummary = feedback.Feedback
		result.FeedbackVersion = feedback.FeedbackVersion
		result.FeedbackHash = feedback.FeedbackHash
	}
	return result, nil
}
