package app

import (
	"context"
	"encoding/json"
	c "github.com/vitlane/vitlane/server/internal/curation/app"
	a "github.com/vitlane/vitlane/server/internal/curation/auto/app"
	ad "github.com/vitlane/vitlane/server/internal/curation/auto/domain"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	i "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	r "github.com/vitlane/vitlane/server/internal/curation/research/app"
	shared "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"regexp"
	"strings"
	"time"
)

type Request struct {
	ID        string    `json:"id"`
	Body      string    `json:"body"`
	Mode      string    `json:"mode"`
	Status    string    `json:"status"`
	ActionID  string    `json:"actionId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}
type Conversation struct {
	SchemaVersion string       `json:"schemaVersion"`
	Version       int64        `json:"version"`
	Unfinished    bool         `json:"unfinished"`
	Requests      []Request    `json:"requests"`
	Messages      []d.FollowUp `json:"messages"`
}
type ResponseInput struct {
	UserID          string `json:"-"`
	AuthSessionID   string `json:"-"`
	CurationID      string `json:"-"`
	MessageID       string `json:"-"`
	Response        string `json:"response"`
	ClientRequestID string `json:"clientRequestId"`
	ExpectedVersion int64  `json:"expectedVersion"`
}
type RequestInput struct {
	ExpectedConversationVersion *int64 `json:"expectedConversationVersion,omitempty"`
	SchemaVersion               string `json:"schemaVersion"`
	UserID                      string `json:"-"`
	AuthSessionID               string `json:"-"`
	CurationID                  string `json:"-"`
	ClientRequestID             string `json:"clientRequestId"`
	ExpectedCurationVersion     int64  `json:"expectedCurationVersion"`
	Mode                        string `json:"mode"`
	Request                     string `json:"request"`
	TargetID                    string `json:"targetId,omitempty"`
	JobID                       string `json:"jobId,omitempty"`
}
type Generation struct {
	BackgroundEligible bool
	UserID             string
	CurationID         string
	ResponseID         string
	Locale             string
	Choices            []d.FollowUp
}
type Repository interface {
	Read(context.Context, string, string) (Conversation, error)
	LockMessage(context.Context, ResponseInput) (d.FollowUp, int64, error)
	SaveResponse(context.Context, ResponseInput, string) error
	AdmitManual(context.Context, RequestInput) (bool, error)
	DispatchContext(context.Context, string) error
	ClaimResponse(context.Context) (*Generation, error)
	FinishGeneration(context.Context, *Generation, *d.FollowUp) error
}
type Plans interface {
	GetByCuration(context.Context, string, string) (c.PlanResult, error)
	TargetCriteria(context.Context, string, string, string) (*d.TargetCriteriaSetV1, error)
	ExecuteExpansionAction(context.Context, c.ExecuteExpansionActionInput) (c.ExpansionResult, error)
}
type Research interface {
	ResearchAgain(context.Context, r.ResearchAgainInput) (r.ResearchAgainResult, error)
}
type Retry interface {
	RetryJob(context.Context, string, string) (i.JobRef, error)
}
type Auto interface {
	Execute(context.Context, a.Input) (a.Result, error)
}
type SubscriptionService interface {
	AcceptSubscription(context.Context, string, string, string, d.FollowUpAction) error
	SubscriptionChoices(context.Context, string, string, string) ([]d.FollowUp, error)
}
type Service struct {
	Background SubscriptionService
	Threads    *c.ThreadService

	Repository Repository
	Tx         shared.Transactor
	Plans      Plans
	Research   Research
	Retry      Retry
	Auto       Auto
	Provider   i.Provider
	Model      string
}

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

func invalid() error { return fault.New(fault.InvalidInput, "CONVERSATION_REQUEST_INVALID", false) }
func (s *Service) Read(ctx context.Context, user, cid string) (Conversation, error) {
	return s.Repository.Read(ctx, user, cid)
}
func (s *Service) Respond(ctx context.Context, in ResponseInput) error {
	if !uuid.MatchString(in.ClientRequestID) || !uuid.MatchString(in.MessageID) || in.ExpectedVersion < 1 {
		return invalid()
	}
	return s.Tx.WithinTransaction(ctx, func(tx context.Context) error {
		m, cv, err := s.Repository.LockMessage(tx, in)
		if err != nil {
			return err
		}
		// Exact replay precedes status/version checks and never closes newer messages.
		if m.ResponseRequestID == in.ClientRequestID {
			expected := map[string]string{"ACCEPT": "ACCEPTED", "DISMISS": "DISMISSED", "ACKNOWLEDGE": "ACKNOWLEDGED"}[in.Response]
			if m.Status == expected && m.Version == in.ExpectedVersion+1 {
				return nil
			}
			return fault.New(fault.Conflict, "CONVERSATION_IDEMPOTENCY_CONFLICT", false)
		}
		next, err := m.Respond(in.Response, in.ExpectedVersion)
		if err != nil {
			return err
		}
		if next == "ACCEPTED" {
			// Mark accepted before the common intake supersedes every other pending row.
			if err = s.Repository.SaveResponse(tx, in, next); err != nil {
				return err
			}
			if m.Payload.Kind == "SUBSCRIBE_DEALS" {
				if s.Background == nil {
					return fault.New(fault.ProviderUnavailable, "BACKGROUND_RESEARCH_DISABLED", false)
				}
				return s.Background.AcceptSubscription(tx, in.UserID, in.CurationID, in.MessageID, *m.Payload)
			}
			if s.Threads != nil {
				// The stored feedback changes only the search; criteria stay as the proposal saw them.
				_, err = s.Threads.Submit(tx, in.UserID, in.AuthSessionID, in.CurationID, c.SubmitThreadInput{SessionID: m.Payload.SessionID, ExpectedSessionVersion: &m.Payload.SessionVersion, ExpectedCriteriaVersion: &m.Payload.CriteriaVersion, ID: in.ClientRequestID, Request: m.Payload.Feedback, ExpectedCurationVersion: cv, Kind: "RESEARCH_AGAIN", TargetID: m.Payload.TargetID, KeepCriteria: true})
				return err
			}
			req := RequestInput{UserID: in.UserID, AuthSessionID: in.AuthSessionID, CurationID: in.CurationID, ClientRequestID: in.ClientRequestID, ExpectedCurationVersion: cv, Mode: "FOLLOW_UP_ACCEPT", TargetID: m.Payload.TargetID, Request: m.Payload.Feedback}
			if _, err = s.Repository.AdmitManual(tx, req); err != nil {
				return err
			}
			if err = s.Repository.DispatchContext(tx, req.ClientRequestID); err != nil {
				return err
			}
			_, err = s.Research.ResearchAgain(tx, r.ResearchAgainInput{UserID: in.UserID, AuthSessionID: in.AuthSessionID, CurationID: in.CurationID, TargetID: m.Payload.TargetID, SessionID: m.Payload.SessionID, CurationActionID: in.ClientRequestID, ClientRequestID: in.ClientRequestID, Feedback: m.Payload.Feedback, ExpectedCurationVersion: cv, ExpectedSessionVersion: m.Payload.SessionVersion, ExpectedCriteriaVersion: &m.Payload.CriteriaVersion})
			return err
		}
		return s.Repository.SaveResponse(tx, in, next)
	})
}
func (s *Service) Execute(ctx context.Context, in RequestInput) (a.Result, error) {
	in.Request = strings.TrimSpace(in.Request)
	if in.SchemaVersion != "vitlane.curation-conversation-request.v1" || !uuid.MatchString(in.ClientRequestID) || in.ExpectedCurationVersion < 1 || len(in.Request) > 2000 {
		return a.Result{}, invalid()
	}
	if s.Threads != nil && (in.Mode == "AUTO" || in.Mode == "ADD_TARGET" || in.Mode == "RESEARCH_AGAIN") {
		kind := in.Mode
		if kind == "AUTO" {
			kind = ""
		}
		_, err := s.Threads.Submit(ctx, in.UserID, in.AuthSessionID, in.CurationID, c.SubmitThreadInput{ExpectedConversationVersion: in.ExpectedConversationVersion, ID: in.ClientRequestID, Request: in.Request, ExpectedCurationVersion: in.ExpectedCurationVersion, Kind: kind, TargetID: in.TargetID})
		return a.Result{Status: "EXECUTED", Decision: ad.Decision(in.Mode), Source: ad.SourceDeterministic, TargetID: in.TargetID, ReasonCode: "THREAD_ACCEPTED"}, err
	}
	if in.Mode == "AUTO" {
		if s.Auto == nil {
			return a.Result{}, fault.New(fault.InvalidInput, "CURATION_AUTO_THREAD_REQUIRED", false)
		}
		return s.Auto.Execute(ctx, a.Input{UserID: in.UserID, AuthSessionID: in.AuthSessionID, CurationID: in.CurationID, ExpectedConversationVersion: in.ExpectedConversationVersion, Request: in.Request, ExpectedCurationVersion: in.ExpectedCurationVersion, ClientRequestID: in.ClientRequestID})
	}
	if in.Mode != "ADD_TARGET" && in.Mode != "RESEARCH_AGAIN" && in.Mode != "RETRY" {
		return a.Result{}, invalid()
	}
	result := a.Result{Status: "EXECUTED", Decision: ad.Decision(in.Mode), Source: ad.SourceDeterministic, TargetID: in.TargetID, ReasonCode: "MANUAL_REQUEST"}
	err := s.Tx.WithinTransaction(ctx, func(tx context.Context) error {
		replay, err := s.Repository.AdmitManual(tx, in)
		if err != nil {
			return err
		}
		if replay {
			result.Replay = true
			return nil
		}
		if err = s.Repository.DispatchContext(tx, in.ClientRequestID); err != nil {
			return err
		}
		p, err := s.Plans.GetByCuration(tx, in.UserID, in.CurationID)
		if err != nil {
			return err
		}
		if p.Curation.Version != in.ExpectedCurationVersion {
			return fault.New(fault.Conflict, "VERSION_CONFLICT", false)
		}
		if in.Mode == "RETRY" {
			_, err = s.Retry.RetryJob(tx, in.UserID, in.JobID)
			return err
		}
		if in.Mode == "ADD_TARGET" {
			if in.Request == "" {
				return invalid()
			}
			kind := d.CurationActionCurationAddTargets
			if p.Curation.Phase == d.CurationPhasePlanning {
				kind = d.CurationActionPlanningAddTargets
			}
			_, err = s.Plans.ExecuteExpansionAction(tx, c.ExecuteExpansionActionInput{ActionID: in.ClientRequestID, UserID: in.UserID, AuthSessionID: in.AuthSessionID, CurationID: in.CurationID, PlanID: string(p.Plan.ID), Type: kind, Instruction: in.Request, ExpectedCurationVersion: in.ExpectedCurationVersion})
			return err
		}
		for _, session := range p.Sessions {
			if string(session.PlanTargetID) == in.TargetID {
				criteria, err := s.Plans.TargetCriteria(tx, in.UserID, in.CurationID, in.TargetID)
				if err != nil {
					return err
				}
				var version int64
				if criteria != nil {
					version = criteria.Version
				}
				_, err = s.Research.ResearchAgain(tx, r.ResearchAgainInput{UserID: in.UserID, AuthSessionID: in.AuthSessionID, CurationID: in.CurationID, TargetID: in.TargetID, SessionID: string(session.ID), CurationActionID: in.ClientRequestID, ClientRequestID: in.ClientRequestID, Feedback: in.Request, ExpectedCurationVersion: in.ExpectedCurationVersion, ExpectedSessionVersion: session.Version, ExpectedCriteriaVersion: &version})
				return err
			}
		}
		return invalid()
	})
	return result, err
}

// The checkpoint is committed before the optional model call. A crash cannot
// repeat spend: the worker abandons this optional proposal on restart.
func (s *Service) Tick(ctx context.Context) error {
	g, err := s.Repository.ClaimResponse(ctx)
	if err != nil || g == nil {
		return err
	}
	if g.BackgroundEligible && s.Background != nil {
		choices, e := s.Background.SubscriptionChoices(ctx, g.UserID, g.CurationID, g.Locale)
		if e != nil {
			return e
		}
		g.Choices = append(g.Choices, choices...)
	}
	var chosen *d.FollowUp
	if len(g.Choices) > 0 && s.Provider != nil && s.Provider.Available(ctx, g.UserID) == nil {
		descriptions := make([]d.FollowUpContent, len(g.Choices))
		for n, m := range g.Choices {
			descriptions[n] = m.Content
		}
		payload, _ := json.Marshal(descriptions)
		ids := []int{-1}
		for n := range descriptions {
			ids = append(ids, n)
		}
		out, e := s.Provider.Complete(ctx, i.CompletionRequest{UserID: g.UserID, RequestKey: "curation-follow-up:" + g.ResponseID, JobID: g.ResponseID, AttemptID: g.ResponseID, ModelKey: s.Model, SystemPrompt: "Select at most one useful follow-up from the supplied foreground research and passive deal-subscription choices. Consider a passive subscription when waiting for a future deal is more useful than repeating research. Return -1 if none is useful. Never invent an action or a target.", UserPrompt: string(payload), SchemaName: "vitlane_curation_follow_up", Schema: map[string]any{"type": "object", "properties": map[string]any{"choice": map[string]any{"type": "integer", "enum": ids}}, "required": []string{"choice"}, "additionalProperties": false}, Deadline: time.Now().Add(15 * time.Second)})
		if e == nil {
			var decision struct {
				Choice *int `json:"choice"`
			}
			if json.Unmarshal([]byte(out.Content), &decision) == nil && decision.Choice != nil && *decision.Choice >= 0 && *decision.Choice < len(g.Choices) {
				chosen = &g.Choices[*decision.Choice]
			}
		}
	}
	return s.Repository.FinishGeneration(ctx, g, chosen)
}
