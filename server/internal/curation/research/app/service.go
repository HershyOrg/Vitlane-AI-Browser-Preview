package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shoppingsessionapp "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// Repository owns the durable Research control plane. Candidate production is
// deliberately absent here: CandidatePool commands are the sole writer for
// current Research results.
type Repository interface {
	NextRoundNumber(context.Context, string) (int, error)
	CreateRound(context.Context, researchdomain.ResearchRound) error
	GetRound(context.Context, string, string, bool) (researchdomain.ResearchRound, error)
	GetCurrentRound(context.Context, string, string) (researchdomain.ResearchRound, error)
	UpdateRound(context.Context, researchdomain.ResearchRound) error
	GetCandidate(context.Context, string, string, string) (researchdomain.Candidate, error)
	InsertConfiguration(context.Context, researchdomain.CandidateConfiguration) (researchdomain.CandidateConfiguration, error)
	GetLatestConfiguration(
		context.Context, string, string, string,
	) (researchdomain.CandidateConfiguration, error)
	ListRounds(context.Context, string, string) ([]researchdomain.ResearchRound, error)
	CreateFeedback(context.Context, researchdomain.ResearchFeedback) error
	GetFeedbackForRound(context.Context, string, string, bool) (researchdomain.ResearchFeedback, error)
	FindFeedbackByRequest(context.Context, string, string) (researchdomain.ResearchFeedback, bool, error)
	UpdateFeedback(context.Context, researchdomain.ResearchFeedback) error
}

type PreparedCandidateRepository interface {
	GetConfiguration(
		context.Context, string, string, string, string,
	) (researchdomain.CandidateConfiguration, error)
}

type PlanningService interface {
	Get(context.Context, string, string) (curationapp.PlanResult, error)
	RecordCurationAction(
		context.Context,
		curationapp.RecordCurationActionInput,
	) (curationapp.RecordCurationActionResult, error)
	RecordManagedContinuationCurationAction(
		context.Context,
		curationapp.RecordCurationActionInput,
	) (curationapp.RecordCurationActionResult, error)
	RecordOwnedPatchCurationAction(
		context.Context,
		curationapp.RecordCurationActionInput,
	) (curationapp.RecordCurationActionResult, error)
	StartCurating(
		context.Context, string, string, int64,
	) (curationapp.StartCuratingResult, error)
}

type Service struct {
	repository       Repository
	plans            PlanningService
	sessions         *shoppingsessionapp.Service
	intelligenceWork IntelligenceJobCreator
	liveCatalog      *LiveCatalogReviewServiceV2
	transactor       sharedapp.Transactor
	clock            sharedapp.Clock
	ids              sharedapp.IDGenerator
	logger           *slog.Logger
	// observations lets an automatic retry of a Round skip source collection.
	observations *observationCheckpoints
}

// EnableLiveCatalogReviewV2 installs the sole Candidate-producing pipeline.
// ResearchRound/ShoppingSession continue to own lifecycle and cancellation;
// product observations and Candidate state are written through CandidatePool.
func (s *Service) EnableLiveCatalogReviewV2(service *LiveCatalogReviewServiceV2) error {
	if service == nil {
		return errors.New("catalog live catalog service is required")
	}
	s.liveCatalog = service
	return nil
}

func NewService(
	repository Repository,
	plans PlanningService,
	sessions *shoppingsessionapp.Service,
	transactor sharedapp.Transactor,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
	logger *slog.Logger,
) *Service {
	return &Service{
		repository: repository, plans: plans, sessions: sessions,
		transactor: transactor, clock: clock, ids: ids, logger: logger,
		observations: newObservationCheckpoints(),
	}
}

type ResearchContext struct {
	DiscoveryRequirements   []DiscoveryDescriptor                    `json:"-"`
	CachedQuery             *CatalogIntelligenceCatalogQuery         `json:"-"`
	ExecutionCheckpoint     bool                                     `json:"-"`
	ContentLocale           string                                   `json:"contentLocale"`
	Criteria                *curationdomain.TargetCriteriaSetV1      `json:"criteria,omitempty"`
	Budget                  *ResearchBudgetSnapshot                  `json:"budget,omitempty"`
	ResearchSettings        *curationdomain.ResearchSettings         `json:"researchSettings,omitempty"`
	PurchaseFeedbackVersion int64                                    `json:"purchaseFeedbackVersion"`
	AlreadyPurchased        []ExternalPurchaseRecord                 `json:"alreadyPurchased"`
	RoundID                 string                                   `json:"roundId"`
	SessionID               string                                   `json:"sessionId"`
	PlanID                  string                                   `json:"planId"`
	RoundNumber             int                                      `json:"roundNumber"`
	ExecutionMode           curationdomain.ExecutionMode             `json:"executionMode"`
	Target                  shoppingsessionapp.TargetSnapshot        `json:"target"`
	ResearchScope           shoppingsessionapp.ResearchScopeSnapshot `json:"researchScope"`
	CandidateMinimum        int                                      `json:"candidateMinimum"`
	CandidateMaximum        int                                      `json:"candidateMaximum"`
}

type ResearchTask struct {
	RoundID          string                     `json:"roundId"`
	SessionID        string                     `json:"sessionId"`
	TargetTitle      string                     `json:"targetTitle"`
	RoundNumber      int                        `json:"roundNumber"`
	ContextSchema    string                     `json:"contextSchema"`
	ContextVersion   int64                      `json:"contextVersion"`
	ContextHash      string                     `json:"contextHash"`
	Status           researchdomain.RoundStatus `json:"status"`
	FeedbackRequired bool                       `json:"feedbackRequired"`
	ActivationRef    string                     `json:"activationRef,omitempty"`
}

type StartResearchGrantInput struct {
	UserID         string
	PlanID         string
	ConnectionID   string
	SessionIDs     []string
	IdempotencyKey string
}

type ContextResult struct {
	ContextSchema    string          `json:"contextSchema"`
	ContextVersion   int64           `json:"contextVersion"`
	ContextHash      string          `json:"contextHash"`
	Context          ResearchContext `json:"context"`
	FeedbackRequired bool            `json:"feedbackRequired"`
	FeedbackSummary  string          `json:"feedbackSummary,omitempty"`
	FeedbackVersion  int64           `json:"feedbackVersion,omitempty"`
	FeedbackHash     string          `json:"feedbackHash,omitempty"`
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type ResearchGroup struct {
	Session shoppingsessiondomain.ShoppingSession `json:"session"`
	Round   *researchdomain.ResearchRound         `json:"round,omitempty"`
	Rounds  []researchdomain.ResearchRound        `json:"rounds"`
}

type PlanResearchResult struct {
	PlanID string          `json:"planId"`
	Groups []ResearchGroup `json:"groups"`
}

// GetPlanResearch projects lifecycle only. Candidate cards are loaded from the
// Phase 8 workspace, so this endpoint can never revive legacy Candidate rows.
func (s *Service) GetPlanResearch(
	ctx context.Context,
	userID, planID string,
) (PlanResearchResult, error) {
	plan, err := s.plans.Get(ctx, userID, planID)
	if err != nil {
		return PlanResearchResult{}, err
	}
	groups := make([]ResearchGroup, 0, len(plan.Sessions))
	for _, session := range plan.Sessions {
		group := ResearchGroup{
			Session: session, Rounds: []researchdomain.ResearchRound{},
		}
		rounds, err := s.repository.ListRounds(ctx, userID, string(session.ID))
		if err != nil {
			return PlanResearchResult{}, err
		}
		group.Rounds = append(group.Rounds, rounds...)
		round, roundErr := s.repository.GetCurrentRound(ctx, userID, string(session.ID))
		if roundErr == nil {
			group.Round = &round
		} else if !errors.Is(roundErr, researchdomain.ErrRoundNotFound) {
			return PlanResearchResult{}, roundErr
		}
		groups = append(groups, group)
	}
	return PlanResearchResult{PlanID: planID, Groups: groups}, nil
}

func (s *Service) Cancel(
	ctx context.Context,
	userID, sessionID string,
) error {
	err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		session, err := s.sessions.Get(txContext, userID, sessionID)
		if err != nil {
			return err
		}
		if session.CurrentResearchRoundID == nil {
			return researchdomain.ErrRoundNotFound
		}
		round, err := s.repository.GetRound(
			txContext, userID, *session.CurrentResearchRoundID, true,
		)
		if err != nil {
			return err
		}
		feedback, feedbackErr := s.repository.GetFeedbackForRound(
			txContext, userID, round.ID, true,
		)
		if err := round.Cancel(s.clock.Now()); err != nil {
			return err
		}
		if err := s.repository.UpdateRound(txContext, round); err != nil {
			return err
		}
		if feedbackErr == nil {
			previous, err := s.repository.GetRound(
				txContext, userID, feedback.PreviousRoundID, true,
			)
			if err != nil {
				return err
			}
			if err := previous.Restore(feedback.PreviousRoundStatus, s.clock.Now()); err != nil {
				return err
			}
			if err := s.repository.UpdateRound(txContext, previous); err != nil {
				return err
			}
			if err := feedback.Cancel(s.clock.Now()); err != nil {
				return err
			}
			if err := s.repository.UpdateFeedback(txContext, feedback); err != nil {
				return err
			}
			if _, err = s.sessions.CancelResearchAgain(
				txContext, userID, sessionID, round.ID, previous.ID,
			); err != nil {
				return err
			}
		} else {
			if !errors.Is(feedbackErr, researchdomain.ErrFeedbackNotFound) {
				return feedbackErr
			}
			if _, err = s.sessions.CancelResearch(
				txContext, userID, sessionID, round.ID,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.logger.InfoContext(ctx, "research cancelled",
			"event", "research.cancelled", "result", "success",
			"user_id", userID, "session_id", sessionID)
	}
	return err
}

func buildContextSnapshot(
	roundID, planID string,
	roundNumber int,
	executionMode curationdomain.ExecutionMode,
	session shoppingsessiondomain.ShoppingSession,
) (json.RawMessage, error) {
	var target shoppingsessionapp.TargetSnapshot
	var scope shoppingsessionapp.ResearchScopeSnapshot
	if err := json.Unmarshal(session.TargetSnapshot, &target); err != nil {
		return nil, fmt.Errorf("decode target snapshot: %w", err)
	}
	if err := json.Unmarshal(session.ResearchScopeSnapshot, &scope); err != nil {
		return nil, fmt.Errorf("decode research scope snapshot: %w", err)
	}
	return json.Marshal(ResearchContext{
		RoundID: roundID, SessionID: string(session.ID), PlanID: planID,
		RoundNumber: roundNumber, ExecutionMode: executionMode,
		Target: target, ResearchScope: scope,
		CandidateMinimum: 1, CandidateMaximum: researchdomain.MaxCandidatesPerResearch,
	})
}

func taskFromRound(round researchdomain.ResearchRound) (ResearchTask, error) {
	var context ResearchContext
	if err := json.Unmarshal(round.ContextSnapshot, &context); err != nil {
		return ResearchTask{}, err
	}
	return ResearchTask{
		RoundID: round.ID, SessionID: round.ShoppingSessionID,
		TargetTitle: context.Target.Title, RoundNumber: round.RoundNumber,
		ContextSchema: round.ContextSchema, ContextVersion: round.ContextVersion,
		ContextHash: round.ContextHash, Status: round.Status,
		FeedbackRequired: false,
	}, nil
}

func (s *Service) feedbackForRound(
	ctx context.Context,
	userID, roundID string,
) (researchdomain.ResearchFeedback, bool, error) {
	feedback, err := s.repository.GetFeedbackForRound(ctx, userID, roundID, false)
	if errors.Is(err, researchdomain.ErrFeedbackNotFound) {
		return researchdomain.ResearchFeedback{}, false, nil
	}
	if err != nil {
		return researchdomain.ResearchFeedback{}, false, err
	}
	return feedback, true, nil
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}
