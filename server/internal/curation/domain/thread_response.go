package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// A Thread answers in natural language once, in its last Action (ADR-0086).
// COMMENT follows a research that added candidates; ANSWER replies to a
// question and runs no other Action. The reply never changes product state,
// and losing it never changes the Thread's outcome.
const (
	ThreadResponseSchema = "vitlane.thread-response.v1"
	ResponseKindComment  = "COMMENT"
	ResponseKindAnswer   = "ANSWER"
	// ReasonResponseUnavailable marks a RESPONSE Action that finished without a
	// reply: the model call failed, or its text did not pass validation.
	ReasonResponseUnavailable = "RESPONSE_UNAVAILABLE"

	responseCommentMaximumRunes = 600
	responseAnswerMaximumRunes  = 1200
)

// ResponseReference resolves one `[[ref]]` token of the body to a candidate the
// Server offered. Names should use these tokens; validation does not certify prose.
type ResponseReference struct {
	Ref         string `json:"ref"`
	CandidateID string `json:"candidateId"`
	TargetID    string `json:"targetId"`
	Title       string `json:"title"`
}

type ActionResponse struct {
	Combination   *CombinationResponse `json:"combination,omitempty"`
	SchemaVersion string               `json:"schemaVersion"`
	Kind          string               `json:"kind"`
	Body          string               `json:"body"`
	Locale        string               `json:"locale"`
	References    []ResponseReference  `json:"references"`
	ModelKey      string               `json:"modelKey,omitempty"`
	CreatedAt     time.Time            `json:"createdAt"`
}

// ResearchSourceFact and ResearchFacts are the Round outcome a research Job
// reports with its receipt: how much each source returned and how much of it
// was compared. Every number a response shows comes from here.
type ResearchSourceFact struct {
	Source         string `json:"source"`
	Status         string `json:"status"`
	ReasonCode     string `json:"reasonCode,omitempty"`
	CandidateCount int    `json:"candidateCount"`
}
type ResearchFacts struct {
	RoundID     string               `json:"roundId"`
	Observed    int                  `json:"observed"`
	Duplicates  int                  `json:"duplicates"`
	Rejected    int                  `json:"rejected"`
	Admitted    int                  `json:"admitted"`
	Evaluated   int                  `json:"evaluated"`
	Unevaluated int                  `json:"unevaluated"`
	Sources     []ResearchSourceFact `json:"sources"`
}

// ResponseAction returns the Thread's RESPONSE Action, if it has one.
func (t *CurationThread) ResponseAction() *CurationAction {
	for i := range t.Actions {
		if t.Actions[i].Type == CurationActionResponse {
			return &t.Actions[i]
		}
	}
	return nil
}

// NeedsResponseComment reports whether a Thread that has run every planned
// Action should still write a comment. Only a research that added candidates
// earns one: settings-only and empty researches keep their fixed sentences,
// and a failed or cancelled Thread never reaches this point.
func (t *CurationThread) NeedsResponseComment() bool {
	if !t.Active() || len(t.Actions) == 0 || t.ResponseAction() != nil {
		return false
	}
	added := false
	for _, a := range t.Actions {
		if a.Status != "SUCCEEDED" {
			return false
		}
		for _, job := range a.Jobs {
			for _, effect := range job.Effects {
				added = added || effect.Kind == "CANDIDATES_ADDED" && effect.Count > 0
			}
		}
	}
	return added
}

// AppendResponseAction registers the comment as a continuation of the last
// Action, so the worker runs it in the same pass instead of a tick later.
func (t *CurationThread) AppendResponseAction(id string) {
	last := t.Actions[len(t.Actions)-1]
	t.Actions = append(t.Actions, CurationAction{
		ID: CurationActionID(id), Type: CurationActionResponse, Instruction: ResponseKindComment,
		GeneratedByActionID: string(last.ID), Status: "PENDING", InputRevision: 1,
		Jobs: []ActionJobResult{}, Effects: []ActionEffect{}, Decisions: []ActionDecision{}, DecisionIDs: []string{}, Answers: []ThreadAnswer{},
	})
}

// SettleResponseJobs closes a RUNNING RESPONSE Action whose Job ended without
// the runner recording a reply. The Thread still succeeds: the reply is an
// addition to the result, never a condition of it.
func (a *CurationAction) SettleResponseJobs(jobs []ActionJobResult) {
	if a.Type != CurationActionResponse || a.Status != "RUNNING" || len(jobs) == 0 {
		return
	}
	for _, job := range jobs {
		if job.Status != "SUCCEEDED" && job.Status != "FAILED" && job.Status != "CANCELLED" {
			return
		}
	}
	a.Jobs = jobs
	a.Status = "SUCCEEDED"
	a.ReasonCode = ReasonResponseUnavailable
}

var (
	responseToken = regexp.MustCompile(`\[\[([a-z][a-z0-9]{0,7})\]\]`)
	responseURL   = regexp.MustCompile(`(?i)https?://|www\.`)
)

// NewActionResponse validates a model reply and resolves its references. The
// rules are the same for the stub and a live model: offered references only,
// no link and a bounded length. Prices and names are grounded by the prompt,
// not certified by text validation; the UI displays a fixed price disclosure.
func NewActionResponse(kind, body, locale, model string, offered []ResponseReference, now time.Time) (ActionResponse, error) {
	invalid := func(reason string) (ActionResponse, error) {
		return ActionResponse{}, fault.New(fault.ProviderRejected, reason, false)
	}
	if kind != ResponseKindComment && kind != ResponseKindAnswer {
		return invalid("THREAD_RESPONSE_KIND_INVALID")
	}
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r", ""))
	for strings.Contains(body, "\n\n\n") {
		body = strings.ReplaceAll(body, "\n\n\n", "\n\n")
	}
	maximum := responseCommentMaximumRunes
	if kind == ResponseKindAnswer {
		maximum = responseAnswerMaximumRunes
	}
	if body == "" || !utf8.ValidString(body) || utf8.RuneCountInString(body) > maximum {
		return invalid("THREAD_RESPONSE_LENGTH_INVALID")
	}
	if responseURL.MatchString(body) {
		return invalid("THREAD_RESPONSE_LINK_FORBIDDEN")
	}
	byRef := map[string]ResponseReference{}
	for _, reference := range offered {
		byRef[reference.Ref] = reference
	}
	used := []ResponseReference{}
	seen := map[string]bool{}
	for _, match := range responseToken.FindAllStringSubmatch(body, -1) {
		reference, ok := byRef[match[1]]
		if !ok {
			return invalid("THREAD_RESPONSE_REFERENCE_UNKNOWN")
		}
		if !seen[reference.Ref] {
			seen[reference.Ref] = true
			used = append(used, reference)
		}
	}
	if rest := responseToken.ReplaceAllString(body, ""); strings.Contains(rest, "[[") || strings.Contains(rest, "]]") {
		return invalid("THREAD_RESPONSE_REFERENCE_MALFORMED")
	}
	return ActionResponse{SchemaVersion: ThreadResponseSchema, Kind: kind, Body: body, Locale: locale, References: used, ModelKey: model, CreatedAt: now}, nil
}
