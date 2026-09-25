package app

import (
	"context"
	"encoding/json"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"slices"
	"strings"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
)

type ResearchAgainInput struct {
	ExpectedCriteriaVersion *int64
	UserID                  string
	AuthSessionID           string
	CurationID              string
	TargetID                string
	SessionID               string
	CurationActionID        string
	Feedback                string
	ExpectedCurationVersion int64
	ExpectedSessionVersion  int64
	ClientRequestID         string
}

type ResearchAgainResult struct {
	Session  shoppingsessiondomain.ShoppingSession `json:"session"`
	Round    researchdomain.ResearchRound          `json:"round"`
	Feedback researchdomain.ResearchFeedback       `json:"feedback"`
	Replay   bool                                  `json:"replay"`
}

func (s *Service) ResearchAgain(
	ctx context.Context,
	input ResearchAgainInput,
) (ResearchAgainResult, error) {
	if !uuidPattern.MatchString(input.ClientRequestID) ||
		!uuidPattern.MatchString(input.CurationActionID) ||
		input.CurationActionID != input.ClientRequestID ||
		strings.TrimSpace(input.CurationID) == "" ||
		strings.TrimSpace(input.TargetID) == "" ||
		input.ExpectedCurationVersion < 1 ||
		len(input.Feedback) > 2000 {
		return ResearchAgainResult{}, researchdomain.ErrFeedbackInvalid
	}
	requestHash, _, err := researchdomain.HashJSON(struct {
		ExpectedCriteriaVersion *int64 `json:"expectedCriteriaVersion,omitempty"`
		CurationID              string `json:"curationId"`
		TargetID                string `json:"targetId"`
		SessionID               string `json:"sessionId"`
		CurationActionID        string `json:"curationActionId"`
		Feedback                string `json:"feedback"`
		ExpectedCurationVersion int64  `json:"expectedCurationVersion"`
		ExpectedSessionVersion  int64  `json:"expectedSessionVersion"`
	}{
		ExpectedCriteriaVersion: input.ExpectedCriteriaVersion, CurationID: input.CurationID,
		TargetID:                input.TargetID,
		SessionID:               input.SessionID,
		CurationActionID:        input.CurationActionID,
		Feedback:                strings.TrimSpace(input.Feedback),
		ExpectedCurationVersion: input.ExpectedCurationVersion,
		ExpectedSessionVersion:  input.ExpectedSessionVersion,
	})
	if err != nil {
		return ResearchAgainResult{}, err
	}

	var result ResearchAgainResult
	err = s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		subjectID := input.TargetID
		if _, actionErr := s.plans.RecordCurationAction(
			txContext,
			curationapp.RecordCurationActionInput{
				ActionID:                input.CurationActionID,
				UserID:                  input.UserID,
				CurationID:              input.CurationID,
				Type:                    curationdomain.CurationActionTargetResearchAgain,
				SubjectType:             curationdomain.CurationActionSubjectTarget,
				SubjectID:               &subjectID,
				Body:                    strings.TrimSpace(input.Feedback),
				ExpectedCurationVersion: input.ExpectedCurationVersion,
				SourceRefType:           curationdomain.CurationActionSourceResearchAgainRequest,
				SourceRefID:             input.ClientRequestID,
			},
		); actionErr != nil {
			return actionErr
		}
		session, lockErr := s.sessions.GetForUpdate(
			txContext, input.UserID, input.SessionID,
		)
		if lockErr != nil {
			return lockErr
		}
		var target struct {
			ID         string `json:"id"`
			PlanID     string `json:"planId"`
			CurationID string `json:"curationId"`
		}
		if unmarshalErr := json.Unmarshal(
			session.TargetSnapshot,
			&target,
		); unmarshalErr != nil {
			return unmarshalErr
		}
		if string(session.PlanTargetID) != input.TargetID ||
			target.ID != input.TargetID ||
			target.CurationID != input.CurationID {
			return shoppingsessiondomain.ErrSessionNotFound
		}
		if replayed, found, replayErr := s.researchAgainReplay(
			txContext, input, requestHash,
		); replayErr != nil {
			return replayErr
		} else if found {
			result = replayed
			return s.attachResearchWork(txContext, input, &result, true)
		}
		if p, ok := s.plans.(criteriaPort); ok && input.ExpectedCriteriaVersion != nil {
			criteria, err := p.TargetCriteria(txContext, input.UserID, input.CurationID, input.TargetID)
			if err != nil {
				return err
			}
			version := int64(0)
			if criteria != nil {
				version = criteria.Version
			}
			if version != *input.ExpectedCriteriaVersion {
				return fault.New(fault.Conflict, "RESEARCH_CRITERIA_CHANGED", false)
			}
		}
		// Checked after the replay branch: a re-sent command must converge on
		// its stored result even while its own work is still running.
		if err := s.guardActionConcurrency(
			txContext, input.UserID, target.PlanID,
		); err != nil {
			return err
		}
		if session.Version != input.ExpectedSessionVersion {
			return shoppingsessiondomain.ErrSessionVersionConflict
		}
		if session.Status != shoppingsessiondomain.SessionStatusReviewing ||
			session.CurrentResearchRoundID == nil {
			return shoppingsessiondomain.ErrSessionNotReady
		}
		plan, readErr := s.activeSessionPlan(
			txContext, input.UserID, session,
			curationdomain.ErrPlanNotConfirmed,
		)
		if readErr != nil {
			return readErr
		}
		previous, readErr := s.repository.GetRound(
			txContext, input.UserID, *session.CurrentResearchRoundID, true,
		)
		if readErr != nil {
			return readErr
		}
		snapshot, snapshotErr := s.interactionSnapshot(
			txContext, input.UserID, input.CurationID, input.TargetID,
		)
		if snapshotErr != nil {
			return snapshotErr
		}
		roundNumber, readErr := s.repository.NextRoundNumber(
			txContext, input.SessionID,
		)
		if readErr != nil {
			return readErr
		}
		nextRoundID := s.ids.NewID()
		contextSnapshot, buildErr := buildContextSnapshot(
			nextRoundID, target.PlanID, roundNumber,
			plan.Plan.ExecutionMode, session,
		)
		if buildErr == nil {
			contextSnapshot, buildErr = s.attachPurchaseFeedback(txContext, input.UserID, target.PlanID, contextSnapshot)
		}
		if buildErr != nil {
			return buildErr
		}
		now := s.clock.Now()
		next, buildErr := researchdomain.NewRound(
			nextRoundID, input.SessionID, input.UserID,
			roundNumber, contextSnapshot, now,
		)
		if buildErr != nil {
			return buildErr
		}
		previousStatus, buildErr := previous.Supersede(now)
		if buildErr != nil {
			return buildErr
		}
		feedback, buildErr := researchdomain.NewFeedback(
			s.ids.NewID(), input.SessionID, previous.ID, next.ID,
			input.UserID, input.Feedback, input.ClientRequestID,
			requestHash, previousStatus, snapshot, now,
		)
		if buildErr != nil {
			return buildErr
		}
		if updateErr := s.repository.UpdateRound(txContext, previous); updateErr != nil {
			return updateErr
		}
		if createErr := s.repository.CreateRound(txContext, next); createErr != nil {
			return createErr
		}
		if createErr := s.repository.CreateFeedback(txContext, feedback); createErr != nil {
			return createErr
		}
		session, buildErr = s.sessions.RequestResearchAgain(
			txContext, input.UserID, input.SessionID, next.ID,
		)
		if buildErr != nil {
			return buildErr
		}
		result = ResearchAgainResult{
			Session: session, Round: next, Feedback: feedback,
		}
		return s.attachResearchWork(txContext, input, &result, false)
	})
	if err == nil {
		s.logger.InfoContext(ctx, "research requested again",
			"event", "research.requested_again", "result", "success",
			"user_id", input.UserID, "session_id", input.SessionID,
			"round_id", result.Round.ID)
	}
	return result, err
}

// attachResearchWork opens the intelligence job that will run the new round.
// The plan owns the provider, so a re-research is executed by the same owner
// the user chose at submission.
func (s *Service) attachResearchWork(
	ctx context.Context,
	input ResearchAgainInput,
	result *ResearchAgainResult,
	_ bool,
) error {
	// 이미 종결된 round에는 실행 job을 붙이지 않는다. terminal round의 job은
	// 실행 즉시 RESEARCH_ROUND_CLOSED로 거부되고 재시도만 반복하다 deadline으로
	// 소진되는 좀비가 된다(2026-08-16 production 실측: 같은 피드백 replay가
	// FAILED round를 돌려준 경우). 종결 round의 결과는 이미 존재하므로 job 없이
	// 그대로 반환한다.
	if result.Round.Status != researchdomain.RoundStatusRequested {
		return nil
	}
	var target struct {
		PlanID string `json:"planId"`
	}
	if err := json.Unmarshal(result.Session.TargetSnapshot, &target); err != nil {
		return err
	}
	plan, err := s.plans.Get(ctx, input.UserID, target.PlanID)
	if err != nil {
		return err
	}
	return s.attachResearchJobs(
		ctx,
		CreateResearchJobInput{
			UserID: input.UserID, CurationID: input.CurationID,
			CurationActionID: input.CurationActionID,
			PlanID:           target.PlanID,
			Provider:         string(plan.Plan.AgentMode),
			ModelKey:         plan.Plan.ModelKey,
		},
		[]string{result.Round.ID},
	)
}

func (s *Service) researchAgainReplay(
	ctx context.Context,
	input ResearchAgainInput,
	requestHash string,
) (ResearchAgainResult, bool, error) {
	replay, found, err := s.feedbackReplay(
		ctx, input.UserID, input.ClientRequestID, requestHash,
	)
	if err != nil || !found {
		return ResearchAgainResult{}, found, err
	}
	session, err := s.sessions.Get(ctx, input.UserID, input.SessionID)
	if err != nil {
		return ResearchAgainResult{}, false, err
	}
	round, err := s.repository.GetRound(ctx, input.UserID, replay.NextRoundID, false)
	if err != nil {
		return ResearchAgainResult{}, false, err
	}
	return ResearchAgainResult{
		Session: session, Round: round, Feedback: replay, Replay: true,
	}, true, nil
}

type FeedbackResult struct {
	SchemaVersion   string                          `json:"schemaVersion"`
	FeedbackVersion int64                           `json:"feedbackVersion"`
	FeedbackHash    string                          `json:"feedbackHash"`
	Feedback        researchdomain.FeedbackEnvelope `json:"feedback"`
}

func (s *Service) feedbackReplay(
	ctx context.Context,
	userID, requestID, requestHash string,
) (researchdomain.ResearchFeedback, bool, error) {
	value, found, err := s.repository.FindFeedbackByRequest(ctx, userID, requestID)
	if err != nil || !found {
		return value, found, err
	}
	if value.RequestHash != requestHash {
		return researchdomain.ResearchFeedback{}, false,
			researchdomain.ErrIdempotencyConflict
	}
	return value, true, nil
}

func (s *Service) interactionSnapshot(
	ctx context.Context,
	userID, curationID, targetID string,
) (researchdomain.InteractionSnapshot, error) {
	snapshot := researchdomain.InteractionSnapshot{
		Variants: []researchdomain.CatalogVariantInteractionSnapshot{},
	}
	if s.liveCatalog == nil || s.liveCatalog.workspace == nil {
		return snapshot, nil
	}
	stored, err := s.liveCatalog.workspace.LoadCatalogWorkspaceStateV2(
		ctx, userID, curationID,
	)
	if err != nil {
		return researchdomain.InteractionSnapshot{}, err
	}
	for _, interaction := range stored.Interactions {
		if interaction.TargetID != targetID {
			continue
		}
		if !interaction.Pinned &&
			interaction.Sentiment == string(researchdomain.CatalogVariantSentimentNone) {
			continue
		}
		sentiment := researchdomain.CatalogVariantSentiment(interaction.Sentiment)
		switch sentiment {
		case researchdomain.CatalogVariantSentimentNone,
			researchdomain.CatalogVariantSentimentLike,
			researchdomain.CatalogVariantSentimentDislike:
		default:
			return researchdomain.InteractionSnapshot{},
				researchdomain.ErrFeedbackInvalid
		}
		snapshot.Variants = append(snapshot.Variants,
			researchdomain.CatalogVariantInteractionSnapshot{
				CandidateID: interaction.CandidateID,
				VariantID:   interaction.VariantID, Pinned: interaction.Pinned,
				Sentiment: sentiment,
			})
	}
	slices.SortFunc(snapshot.Variants, func(left, right researchdomain.CatalogVariantInteractionSnapshot) int {
		if compared := strings.Compare(left.CandidateID, right.CandidateID); compared != 0 {
			return compared
		}
		return strings.Compare(left.VariantID, right.VariantID)
	})
	for _, interaction := range stored.ProductInteractions {
		if interaction.TargetID != targetID || (!interaction.Pinned && interaction.Sentiment == "NONE") {
			continue
		}
		if interaction.ProductRef.Validate() != nil || !interaction.ProductRef.Source.KoreanExternal() || (interaction.Sentiment != "NONE" && interaction.Sentiment != "LIKE" && interaction.Sentiment != "DISLIKE") {
			return researchdomain.InteractionSnapshot{}, researchdomain.ErrFeedbackInvalid
		}
		snapshot.Products = append(snapshot.Products, researchdomain.ProductInteractionSnapshot{CandidateID: interaction.CandidateID, ProductRef: interaction.ProductRef, Pinned: interaction.Pinned, Sentiment: interaction.Sentiment})
	}
	slices.SortFunc(snapshot.Products, func(left, right researchdomain.ProductInteractionSnapshot) int {
		return strings.Compare(left.CandidateID, right.CandidateID)
	})
	return snapshot, nil
}
