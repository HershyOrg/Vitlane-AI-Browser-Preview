package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	autodomain "github.com/vitlane/vitlane/server/internal/curation/auto/domain"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
)

const autoResolutionSchemaName = "vitlane_curation_auto_resolution"

// Budget wording must go through the semantic guard before deterministic routing.
var budgetWording = regexp.MustCompile(`(?i)예산|가격|한도|금액|배분|상한|하한|제한 ?없|budget|price|spend|afford|limit|allocation|[$₩]|[0-9][0-9,. ]*(만원|천원|원|달러|dollars|USD|KRW)`)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type PlanReader interface {
	GetByCuration(context.Context, string, string) (curationapp.PlanResult, error)
}

type AddTargetExecutor interface {
	ExecuteExpansionAction(context.Context, curationapp.ExecuteExpansionActionInput) (curationapp.ExpansionResult, error)
}

type ResearchAgainExecutor interface {
	ResearchAgain(context.Context, researchapp.ResearchAgainInput) (researchapp.ResearchAgainResult, error)
}

type Repository interface {
	Get(context.Context, string, string) (Record, bool, error)
	Begin(context.Context, Record) (Record, bool, error)
	Resolve(context.Context, string, string, ResolutionRecord) error
	MarkNeedsSelection(context.Context, string, string, string, autodomain.Source) error
	MarkExecuted(context.Context, string, string) error
}

type Record struct {
	ExpectedConversationVersion *int64
	ID                          string
	RequestText                 string
	UserID                      string
	CurationID                  string
	PlanID                      string
	RequestHash                 string
	TargetSnapshotHash          string
	ExpectedCurationVersion     int64
	Status                      string
	Decision                    autodomain.Decision
	Source                      autodomain.Source
	TargetID                    string
	SessionID                   string
	SessionVersion              int64
	ReasonCode                  string
	CreatedAt                   time.Time
}

type ResolutionRecord struct {
	Decision       autodomain.Decision
	Source         autodomain.Source
	TargetID       string
	SessionID      string
	SessionVersion int64
	ReasonCode     string
}

type Input struct {
	ExpectedConversationVersion *int64
	UserID                      string
	AuthSessionID               string
	CurationID                  string
	Request                     string
	ExpectedCurationVersion     int64
	ClientRequestID             string
}

type Result struct {
	Status     string              `json:"status"`
	Decision   autodomain.Decision `json:"decision"`
	Source     autodomain.Source   `json:"source"`
	TargetID   string              `json:"targetId,omitempty"`
	ReasonCode string              `json:"reasonCode"`
	Replay     bool                `json:"replay"`
}

type Service struct {
	plans        PlanReader
	addTargets   AddTargetExecutor
	research     ResearchAgainExecutor
	repository   Repository
	provider     intelligenceapp.Provider
	defaultModel string
}

func NewService(
	plans PlanReader,
	addTargets AddTargetExecutor,
	research ResearchAgainExecutor,
	repository Repository,
	provider intelligenceapp.Provider,
	defaultModel string,
) *Service {
	return &Service{
		plans: plans, addTargets: addTargets, research: research,
		repository: repository, provider: provider, defaultModel: defaultModel,
	}
}

func (s *Service) Execute(ctx context.Context, input Input) (Result, error) {
	input.CurationID = strings.TrimSpace(input.CurationID)
	input.Request = strings.TrimSpace(input.Request)
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	if input.UserID == "" || input.AuthSessionID == "" || input.CurationID == "" ||
		input.Request == "" || len(input.Request) > 2000 ||
		input.ExpectedCurationVersion < 1 || !uuidPattern.MatchString(input.ClientRequestID) {
		return Result{}, autodomain.ErrInvalid
	}
	requestHash := hashValue(struct {
		CurationID                  string `json:"curationId"`
		Request                     string `json:"request"`
		ExpectedCurationVersion     int64  `json:"expectedCurationVersion"`
		ExpectedConversationVersion *int64 `json:"expectedConversationVersion,omitempty"`
	}{input.CurationID, input.Request, input.ExpectedCurationVersion, input.ExpectedConversationVersion})

	if existing, found, err := s.repository.Get(ctx, input.UserID, input.ClientRequestID); err != nil {
		return Result{}, err
	} else if found {
		if existing.CurationID != input.CurationID || existing.RequestHash != requestHash ||
			existing.ExpectedCurationVersion != input.ExpectedCurationVersion {
			return Result{}, autodomain.ErrIdempotencyConflict
		}
		return s.resume(ctx, input, existing, true)
	}

	plan, targets, snapshotHash, err := s.readSnapshot(ctx, input.UserID, input.CurationID)
	if err != nil {
		return Result{}, err
	}
	if plan.Curation.Version != input.ExpectedCurationVersion {
		return Result{}, autodomain.ErrVersionConflict
	}
	record, created, err := s.repository.Begin(ctx, Record{
		ID: input.ClientRequestID, UserID: input.UserID, CurationID: input.CurationID,
		PlanID: string(plan.Plan.ID), RequestHash: requestHash,
		TargetSnapshotHash:          snapshotHash,
		ExpectedCurationVersion:     input.ExpectedCurationVersion,
		ExpectedConversationVersion: input.ExpectedConversationVersion, RequestText: input.Request, Status: "PENDING", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return Result{}, err
	}
	if !created {
		if record.CurationID != input.CurationID || record.RequestHash != requestHash {
			return Result{}, autodomain.ErrIdempotencyConflict
		}
		return s.resume(ctx, input, record, true)
	}

	resolution := autodomain.Resolution{}
	resolved := false
	if regexp.MustCompile(`(?i)^(고마워요?|감사합니다|감사|네|응|해줘|좋아|thanks?[!. ]*|thank you[!. ]*|yes[!. ]*|ok[!. ]*)$`).MatchString(input.Request) {
		resolution = autodomain.Resolution{Decision: autodomain.DecisionNeedsSelection, Source: autodomain.SourceDeterministic, ReasonCode: "NO_ACTION"}
		resolved = true
	} else if !regexp.MustCompile(`(?i)(\?|^(what|why|how|when|where|is|are|does)\b)`).MatchString(input.Request) && !budgetWording.MatchString(input.Request) && !autodomain.NeedsEnglishNormalization(input.Request) {
		resolution, resolved = autodomain.ResolveDeterministically(
			autodomain.ParseEnglish(input.Request), targets,
		)
	}
	if !resolved {
		resolution = s.resolveManaged(ctx, input, plan, targets)
	}
	if resolution.Decision == autodomain.DecisionNeedsSelection {
		if err := s.repository.MarkNeedsSelection(
			ctx, input.UserID, input.ClientRequestID,
			resolution.ReasonCode, resolution.Source,
		); err != nil {
			return Result{}, err
		}
		return resultOf(resolution, false), nil
	}

	freshPlan, freshTargets, freshHash, err := s.readSnapshot(ctx, input.UserID, input.CurationID)
	if err != nil {
		return Result{}, err
	}
	if freshPlan.Curation.Version != input.ExpectedCurationVersion || freshHash != snapshotHash {
		if markErr := s.repository.MarkNeedsSelection(
			ctx, input.UserID, input.ClientRequestID,
			"CURATION_SNAPSHOT_CHANGED", resolution.Source,
		); markErr != nil {
			return Result{}, markErr
		}
		return Result{
			Status: "NEEDS_SELECTION", Decision: autodomain.DecisionNeedsSelection,
			Source: resolution.Source, ReasonCode: "CURATION_SNAPSHOT_CHANGED",
		}, nil
	}
	target, ok := targetByID(freshTargets, resolution.TargetID)
	stored := ResolutionRecord{
		Decision: resolution.Decision, Source: resolution.Source,
		TargetID: resolution.TargetID, ReasonCode: resolution.ReasonCode,
	}
	if resolution.Decision == autodomain.DecisionResearchAgain {
		if !ok || !target.Researchable {
			if err := s.repository.MarkNeedsSelection(
				ctx, input.UserID, input.ClientRequestID,
				"TARGET_NOT_RESEARCHABLE", resolution.Source,
			); err != nil {
				return Result{}, err
			}
			return Result{
				Status: "NEEDS_SELECTION", Decision: autodomain.DecisionNeedsSelection,
				Source: resolution.Source, ReasonCode: "TARGET_NOT_RESEARCHABLE",
			}, nil
		}
		stored.SessionID = target.SessionID
		stored.SessionVersion = target.SessionVersion
	}
	if err := s.repository.Resolve(ctx, input.UserID, input.ClientRequestID, stored); err != nil {
		return Result{}, err
	}
	record.Status = "RESOLVED"
	record.Decision = stored.Decision
	record.Source = stored.Source
	record.TargetID = stored.TargetID
	record.SessionID = stored.SessionID
	record.SessionVersion = stored.SessionVersion
	record.ReasonCode = stored.ReasonCode
	return s.resume(ctx, input, record, false)
}

func staleExecutionError(err error) bool {
	return errors.Is(err, curationdomain.ErrVersionConflict) ||
		errors.Is(err, curationdomain.ErrTargetNotFound) ||
		errors.Is(err, shoppingsessiondomain.ErrSessionVersionConflict) ||
		errors.Is(err, shoppingsessiondomain.ErrSessionNotFound) ||
		errors.Is(err, shoppingsessiondomain.ErrSessionNotReady)
}

func (s *Service) resume(
	ctx context.Context,
	input Input,
	record Record,
	replay bool,
) (Result, error) {
	switch record.Status {
	case "EXECUTED":
		return resultOf(recordResolution(record), true), nil
	case "NEEDS_SELECTION":
		resolution := recordResolution(record)
		resolution.Decision = autodomain.DecisionNeedsSelection
		return resultOf(resolution, true), nil
	case "PENDING":
		if !record.CreatedAt.IsZero() && time.Since(record.CreatedAt) > 2*time.Minute {
			if err := s.repository.MarkNeedsSelection(
				ctx, input.UserID, record.ID,
				"AUTO_RESOLUTION_INTERRUPTED", autodomain.SourceManaged,
			); err != nil {
				return Result{}, err
			}
			return Result{
				Status: "NEEDS_SELECTION", Decision: autodomain.DecisionNeedsSelection,
				Source: autodomain.SourceManaged, ReasonCode: "AUTO_RESOLUTION_INTERRUPTED",
				Replay: true,
			}, nil
		}
		return Result{}, autodomain.ErrInProgress
	case "RESOLVED":
		// Continue below. If a process stopped after the exact action committed
		// but before MarkExecuted, the existing action idempotency boundary
		// returns the same effect without creating duplicate work.
	default:
		return Result{}, autodomain.ErrInvalid
	}

	var err error
	switch record.Decision {
	case autodomain.DecisionAddTarget:
		actionType := curationdomain.CurationActionCurationAddTargets
		if plan, readErr := s.plans.GetByCuration(ctx, input.UserID, input.CurationID); readErr == nil &&
			plan.Curation.Phase == curationdomain.CurationPhasePlanning {
			actionType = curationdomain.CurationActionPlanningAddTargets
		}
		_, err = s.addTargets.ExecuteExpansionAction(ctx, curationapp.ExecuteExpansionActionInput{
			ActionID: record.ID, UserID: input.UserID, AuthSessionID: input.AuthSessionID,
			CurationID: record.CurationID, PlanID: record.PlanID, Type: actionType,
			Instruction: input.Request, ExpectedCurationVersion: record.ExpectedCurationVersion,
		})
	case autodomain.DecisionResearchAgain:
		_, err = s.research.ResearchAgain(ctx, researchapp.ResearchAgainInput{
			UserID: input.UserID, AuthSessionID: input.AuthSessionID,
			CurationID: record.CurationID, TargetID: record.TargetID, SessionID: record.SessionID,
			CurationActionID: record.ID, Feedback: input.Request,
			ExpectedCurationVersion: record.ExpectedCurationVersion,
			ExpectedSessionVersion:  record.SessionVersion, ClientRequestID: record.ID,
		})
	default:
		return Result{}, autodomain.ErrInvalid
	}
	if err != nil {
		if staleExecutionError(err) {
			if markErr := s.repository.MarkNeedsSelection(
				ctx, input.UserID, record.ID,
				"AUTO_EXECUTION_STATE_CHANGED", record.Source,
			); markErr != nil {
				return Result{}, markErr
			}
			return Result{
				Status: "NEEDS_SELECTION", Decision: autodomain.DecisionNeedsSelection,
				Source: record.Source, ReasonCode: "AUTO_EXECUTION_STATE_CHANGED",
				Replay: replay,
			}, nil
		}
		return Result{}, err
	}
	if err := s.repository.MarkExecuted(ctx, input.UserID, record.ID); err != nil {
		return Result{}, err
	}
	resolution := recordResolution(record)
	result := resultOf(resolution, replay)
	result.Status = "EXECUTED"
	return result, nil
}

func (s *Service) readSnapshot(
	ctx context.Context,
	userID, curationID string,
) (curationapp.PlanResult, []autodomain.Target, string, error) {
	plan, err := s.plans.GetByCuration(ctx, userID, curationID)
	if err != nil {
		return curationapp.PlanResult{}, nil, "", err
	}
	sessions := make(map[string]shoppingsessiondomain.ShoppingSession, len(plan.Sessions))
	for _, session := range plan.Sessions {
		sessions[string(session.PlanTargetID)] = session
	}
	targets := make([]autodomain.Target, 0, len(plan.Targets))
	for _, target := range plan.Targets {
		if target.RemovedAt != nil {
			continue
		}
		item := autodomain.Target{
			ID: string(target.ID), Title: target.Title,
			NormalizedIntent: target.NormalizedIntent, OrderIndex: target.OrderIndex,
		}
		if session, ok := sessions[string(target.ID)]; ok {
			item.SessionID = string(session.ID)
			item.SessionVersion = session.Version
			item.Researchable = session.Status == shoppingsessiondomain.SessionStatusReviewing
		}
		if reader, ok := s.plans.(interface {
			TargetCriteria(context.Context, string, string, string) (*curationdomain.TargetCriteriaSetV1, error)
		}); ok {
			criteria, e := reader.TargetCriteria(ctx, userID, curationID, item.ID)
			if e != nil {
				return curationapp.PlanResult{}, nil, "", e
			}
			if criteria != nil {
				item.Title = criteria.Subject.Label
				item.NormalizedIntent = criteria.Subject.ProductType
			}
		}
		targets = append(targets, item)
	}
	return plan, targets, hashValue(targets), nil
}

type managedOutput struct {
	Operation               autodomain.Operation `json:"operation"`
	ProductReferenceEnglish string               `json:"productReferenceEnglish"`
	ModifierTermsEnglish    []string             `json:"modifierTermsEnglish"`
	ReferencedOrdinal       *int                 `json:"referencedOrdinal"`
	Decision                autodomain.Decision  `json:"decision"`
	TargetID                *string              `json:"targetId"`
	ReasonCode              string               `json:"reasonCode"`
}

func (s *Service) resolveManaged(
	ctx context.Context,
	input Input,
	plan curationapp.PlanResult,
	targets []autodomain.Target,
) autodomain.Resolution {
	fallback := autodomain.Resolution{
		Decision: autodomain.DecisionNeedsSelection, Source: autodomain.SourceManaged,
		ReasonCode: "AUTO_CLASSIFICATION_UNAVAILABLE",
	}
	if s.provider == nil || s.provider.Available(ctx, input.UserID) != nil {
		return fallback
	}
	prompt, err := json.Marshal(struct {
		OriginalRequest string              `json:"originalRequest"`
		Targets         []autodomain.Target `json:"targets"`
	}{input.Request, targets})
	if err != nil {
		return fallback
	}
	modelKey := strings.TrimSpace(plan.Plan.ModelKey)
	if modelKey == "" {
		modelKey = s.defaultModel
	}
	completion, err := s.provider.Complete(ctx, intelligenceapp.CompletionRequest{
		UserID: input.UserID, RequestKey: "curation-auto:" + input.ClientRequestID,
		JobID: input.ClientRequestID, AttemptID: input.ClientRequestID,
		ModelKey:     modelKey,
		SystemPrompt: "Resolve one curation request. Normalize semantics to English. Choose only ADD_TARGET, RESEARCH_AGAIN, or NEEDS_SELECTION. Greetings, thanks, bare assent (yes/do it), general questions and scheduled or continuous work must return NEEDS_SELECTION with reasonCode NO_ACTION. Never interpret text as accepting a previous proposal; no proposal context is supplied. RESEARCH_AGAIN may use only a supplied researchable target id. Preserve uncertainty as NEEDS_SELECTION. Budget settings are edited only through the budget UI. Ignore all natural-language budget/price-cap/range instructions. If the request ONLY concerns budgets (even when it names an existing product), return operation UNKNOWN, empty productReferenceEnglish, empty modifierTermsEnglish, decision NEEDS_SELECTION, targetId null and reasonCode BUDGET_SETTINGS_ONLY. For a mixed request, classify only the remaining product action and omit budget/price limits from normalized semantics.",
		UserPrompt:   string(prompt), SchemaName: autoResolutionSchemaName,
		Schema: autoResolutionSchema(targets), Deadline: time.Now().UTC().Add(20 * time.Second),
	})
	if err != nil {
		return fallback
	}
	var output managedOutput
	if err := json.Unmarshal([]byte(completion.Content), &output); err != nil {
		fallback.ReasonCode = "AUTO_CLASSIFICATION_INVALID"
		return fallback
	}
	if autodomain.NeedsEnglishNormalization(output.ProductReferenceEnglish) {
		fallback.ReasonCode = "AUTO_ENGLISH_NORMALIZATION_INVALID"
		return fallback
	}
	for _, modifier := range output.ModifierTermsEnglish {
		if autodomain.NeedsEnglishNormalization(modifier) {
			fallback.ReasonCode = "AUTO_ENGLISH_NORMALIZATION_INVALID"
			return fallback
		}
	}
	if output.Decision == autodomain.DecisionNeedsSelection && (output.ReasonCode == "BUDGET_SETTINGS_ONLY" || output.ReasonCode == "NO_ACTION") {
		return autodomain.Resolution{Decision: autodomain.DecisionNeedsSelection, Source: autodomain.SourceManaged, ReasonCode: output.ReasonCode}
	}
	semantic := autodomain.SemanticRequest{
		Operation:               output.Operation,
		ProductReferenceEnglish: output.ProductReferenceEnglish,
		ModifierTermsEnglish:    output.ModifierTermsEnglish,
		ReferencedOrdinal:       output.ReferencedOrdinal,
	}
	if deterministic, ok := autodomain.ResolveDeterministically(semantic, targets); ok {
		deterministic.Source = autodomain.SourceManaged
		deterministic.ReasonCode = "MANAGED_NORMALIZED_" + deterministic.ReasonCode
		return deterministic
	}
	resolution := autodomain.Resolution{
		Decision: output.Decision, Source: autodomain.SourceManaged,
		ReasonCode: safeReason(output.ReasonCode),
	}
	if output.TargetID != nil {
		resolution.TargetID = strings.TrimSpace(*output.TargetID)
	}
	switch resolution.Decision {
	case autodomain.DecisionAddTarget:
		resolution.TargetID = ""
	case autodomain.DecisionResearchAgain:
		target, ok := targetByID(targets, resolution.TargetID)
		if !ok || !target.Researchable {
			fallback.ReasonCode = "AUTO_TARGET_INVALID"
			return fallback
		}
	case autodomain.DecisionNeedsSelection:
		resolution.TargetID = ""
	default:
		fallback.ReasonCode = "AUTO_CLASSIFICATION_INVALID"
		return fallback
	}
	return resolution
}

func autoResolutionSchema(targets []autodomain.Target) map[string]any {
	targetIDs := make([]any, 0, len(targets))
	for _, target := range targets {
		if target.Researchable {
			targetIDs = append(targetIDs, target.ID)
		}
	}
	targetIDSchema := map[string]any{"type": "null"}
	if len(targetIDs) > 0 {
		targetIDSchema = map[string]any{
			"anyOf": []any{map[string]any{"type": "string", "enum": targetIDs}, map[string]any{"type": "null"}},
		}
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"operation":               map[string]any{"type": "string", "enum": []string{"ADD", "REFINE", "UNKNOWN"}},
			"productReferenceEnglish": map[string]any{"type": "string"},
			"modifierTermsEnglish":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"referencedOrdinal":       map[string]any{"anyOf": []any{map[string]any{"type": "integer", "minimum": 1}, map[string]any{"type": "null"}}},
			"decision":                map[string]any{"type": "string", "enum": []string{"ADD_TARGET", "RESEARCH_AGAIN", "NEEDS_SELECTION"}},
			"targetId":                targetIDSchema,
			"reasonCode":              map[string]any{"type": "string", "pattern": "^[A-Z0-9_]{1,64}$"},
		},
		"required": []string{"operation", "productReferenceEnglish", "modifierTermsEnglish", "referencedOrdinal", "decision", "targetId", "reasonCode"},
	}
}

func targetByID(targets []autodomain.Target, id string) (autodomain.Target, bool) {
	for _, target := range targets {
		if target.ID == id {
			return target, true
		}
	}
	return autodomain.Target{}, false
}

func recordResolution(record Record) autodomain.Resolution {
	return autodomain.Resolution{
		Decision: record.Decision, Source: record.Source,
		TargetID: record.TargetID, ReasonCode: record.ReasonCode,
	}
}

func resultOf(resolution autodomain.Resolution, replay bool) Result {
	status := "EXECUTED"
	if resolution.Decision == autodomain.DecisionNeedsSelection {
		status = "NEEDS_SELECTION"
	}
	if resolution.ReasonCode == "NO_ACTION" {
		status = "NO_ACTION"
		resolution.Decision = autodomain.DecisionNoAction
	}
	return Result{
		Status: status, Decision: resolution.Decision, Source: resolution.Source,
		TargetID: resolution.TargetID, ReasonCode: resolution.ReasonCode, Replay: replay,
	}
}

func hashValue(value any) string {
	payload, _ := json.Marshal(value)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func safeReason(value string) string {
	value = strings.TrimSpace(value)
	matched, _ := regexp.MatchString(`^[A-Z0-9_]{1,64}$`, value)
	if !matched {
		return "AUTO_MANAGED_FALLBACK"
	}
	return value
}

func IsConflict(err error) bool {
	return errors.Is(err, autodomain.ErrVersionConflict) ||
		errors.Is(err, autodomain.ErrIdempotencyConflict) ||
		errors.Is(err, autodomain.ErrInProgress) ||
		errors.Is(err, autodomain.ErrSnapshotChanged)
}

func ErrorCode(err error) string {
	for _, candidate := range []error{
		autodomain.ErrInvalid, autodomain.ErrVersionConflict,
		autodomain.ErrIdempotencyConflict, autodomain.ErrInProgress,
		autodomain.ErrSnapshotChanged,
	} {
		if errors.Is(err, candidate) {
			return candidate.Error()
		}
	}
	return "AUTO_RESEARCH_FAILED"
}
