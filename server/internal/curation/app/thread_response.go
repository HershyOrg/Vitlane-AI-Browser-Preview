package app

import (
	"context"
	"sort"
	"strconv"
	"strings"

	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// The Thread's reply (ADR-0086). The responder is a model call with no tools
// and no authority: it reads saved assessments and observed listing facts, then returns text. Curation
// decides what it may see, validates what it wrote and stores it on the
// RESPONSE Action. Nothing here changes a Target, a Candidate or a budget.

const (
	responseCommentCandidates = 3
	responseAnswerCandidates  = 8
	// responseHistoryBytes bounds the conversation an ANSWER call receives. The
	// newest turns are kept; older turns are dropped whole, oldest first.
	responseHistoryBytes = 6000
	responseTextLimit    = 320
)

type ResponseAxis struct {
	Label       string `json:"label"`
	Score       int    `json:"score"`
	Basis       string `json:"basis"`
	Explanation string `json:"explanation,omitempty"`
}

// ResponsePrice is a listing observation, not a checkout quote. Selected option
// prices take precedence; otherwise the catalog minimum and maximum are kept.
type ResponseOption struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type ResponsePrice struct {
	MinimumMinor          int64            `json:"minimumMinor"`
	MaximumMinor          int64            `json:"maximumMinor"`
	Currency              string           `json:"currency"`
	Basis                 string           `json:"basis"`
	ObservedAt            string           `json:"observedAt,omitempty"`
	SelectedOptions       []ResponseOption `json:"selectedOptions,omitempty"`
	ConvertedMaximumMinor map[string]int64 `json:"estimatedMaximumMinorByCurrency,omitempty"`
	ConvertedMinor        map[string]int64 `json:"estimatedMinorByCurrency,omitempty"`
	ExchangeRateAsOf      string           `json:"exchangeRateAsOf,omitempty"`
}

// Both COMMENT and ANSWER receive the same explicitly scoped listing facts.
type ResponseCandidate struct {
	Description          string         `json:"description,omitempty"`
	AssessmentFacts      []string       `json:"assessmentFacts,omitempty"`
	VariantID            string         `json:"-"`
	ConfigurationVersion int64          `json:"-"`
	CartItemID           string         `json:"-"`
	Quantity             int            `json:"quantity"`
	KeepCart             bool           `json:"keepCart"`
	Ref                  string         `json:"ref"`
	CandidateID          string         `json:"-"`
	TargetID             string         `json:"-"`
	Title                string         `json:"title,omitempty"`
	Seller               string         `json:"seller,omitempty"`
	Source               string         `json:"source,omitempty"`
	Price                *ResponsePrice `json:"price,omitempty"`
	PriceMinor           *int64         `json:"-"`
	Currency             string         `json:"-"`
	TotalScore           int            `json:"totalScore"`
	IntentPoint          string         `json:"intentPoint,omitempty"`
	Axes                 []ResponseAxis `json:"axes"`
	RoundID              string         `json:"-"`
	Budget               string         `json:"budget"`
	Rank                 int            `json:"rank"`
	NewThisRound         bool           `json:"newInThisRequest"`
}

type ResponseTarget struct {
	ID         string              `json:"-"`
	Title      string              `json:"title"`
	Axes       []string            `json:"axes"`
	Exclusions []string            `json:"exclusions,omitempty"`
	UnitBudget string              `json:"unitBudget,omitempty"`
	Researched bool                `json:"researchedInThisRequest"`
	Added      int                 `json:"addedInThisRequest"`
	Facts      *d.ResearchFacts    `json:"facts,omitempty"`
	PoolSize   int                 `json:"savedCandidates"`
	Candidates []ResponseCandidate `json:"candidates"`
}

type ResponseTurn struct {
	Request string `json:"request"`
	Reply   string `json:"reply"`
}

type ResponseContext struct {
	OriginalIntent string                  `json:"originalIntent"`
	Combinations   []d.CombinationResponse `json:"combinations,omitempty"`
	Kind           string                  `json:"kind"`
	Locale         string                  `json:"locale"`
	Request        string                  `json:"request"`
	Changes        []string                `json:"changesInThisRequest"`
	Budget         string                  `json:"budget"`
	Targets        []ResponseTarget        `json:"products"`
	History        []ResponseTurn          `json:"conversation"`
	References     []d.ResponseReference   `json:"-"`

	UserID        string `json:"-"`
	ModelKey      string `json:"-"`
	ActionID      string `json:"-"`
	JobID         string `json:"-"`
	AttemptID     string `json:"-"`
	InputRevision int64  `json:"-"`
}

// ThreadResponder writes the reply text. It returns a validated response or an
// error; it never returns unvalidated model text.
type ThreadResponder interface {
	Respond(context.Context, ResponseContext) (d.ActionResponse, error)
}

// SavedCandidate is what the Research side reports for one Target's pool.
type SavedCandidate struct {
	Excluded             bool
	Description          string
	AssessmentFacts      []string
	VariantID            string
	ConfigurationVersion int64
	SourceState          string
	CandidateID          string
	Title                string
	Seller               string
	Source               string
	PriceMinor           *int64
	Price                *ResponsePrice
	Currency             string
	Assessed             bool
	TotalScore           int
	IntentPoint          string
	Axes                 []ResponseAxis
	RoundID              string
}

// ThreadResponseSource reads saved candidates for the given Targets, in pool
// order, hidden ones excluded. It may hydrate listing facts from the provider,
// so callers must not hold a database transaction or curation lock.
type ThreadResponseSource interface {
	SavedCandidates(ctx context.Context, userID, curationID string, targetIDs []string) (map[string][]SavedCandidate, error)
}

// SetResponder enables the RESPONSE Action. Without it no comment is appended
// and a planned answer finishes as unavailable.
func (s *ThreadService) SetResponder(responder ThreadResponder, source ThreadResponseSource) {
	s.responder = responder
	s.responseSource = source
}

func clip(value string, limit int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func formatMinor(minor int64, currency string) string {
	if currency == "USD" {
		return "$" + strconv.FormatFloat(float64(minor)/100, 'f', 2, 64)
	}
	digits := strconv.FormatInt(minor, 10)
	out := []byte{}
	for i, c := range []byte(digits) {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return "₩" + string(out)
}

func moneyMinor(amount *string, currency string) (int64, bool) {
	if amount == nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(*amount), 64)
	if err != nil || value < 0 {
		return 0, false
	}
	if currency == "USD" {
		return int64(value*100 + 0.5), true
	}
	return int64(value + 0.5), true
}

func candidateMinor(c SavedCandidate, currency string) *int64 {
	if c.PriceMinor == nil {
		return nil
	}
	if c.Currency == currency {
		return c.PriceMinor
	}
	if c.Price != nil {
		if value, ok := c.Price.ConvertedMinor[currency]; ok {
			return &value
		}
	}
	return nil
}

// RankSavedCandidates orders a Target's assessed candidates the way the Web
// shows them: Vitlane Pick first — the best score whose price fits the
// Target's unit budget, or the best score when none fits or the budget cannot
// be compared — then the rest by score. Pool order breaks ties.
func RankSavedCandidates(candidates []SavedCandidate, unitBudgetMinor int64, budgetCurrency string, hasBudget bool) []SavedCandidate {
	ranked := []SavedCandidate{}
	for _, c := range candidates {
		if c.Assessed {
			ranked = append(ranked, c)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].TotalScore > ranked[j].TotalScore })
	if !hasBudget {
		return ranked
	}
	// Every tied best fitting candidate leads, matching Web's pickLeaders.
	best := -1
	leaders, rest := []SavedCandidate{}, []SavedCandidate{}
	for _, c := range ranked {
		price := candidateMinor(c, budgetCurrency)
		if price != nil && *price <= unitBudgetMinor && (best < 0 || c.TotalScore == best) {
			best = c.TotalScore
			leaders = append(leaders, c)
		} else {
			rest = append(rest, c)
		}
	}
	if len(leaders) > 0 {
		return append(leaders, rest...)
	}
	return ranked
}

// responseHistory keeps the newest turns that fit the byte budget and returns
// them oldest first, the order a reader expects.
func responseHistory(threads []d.CurationThread, currentID string, limit int) []ResponseTurn {
	sorted := append([]d.CurationThread{}, threads...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.After(sorted[j].CreatedAt) })
	kept := []ResponseTurn{}
	used := 0
	for _, t := range sorted {
		if t.ID == currentID || t.Active() {
			continue
		}
		turn := ResponseTurn{Request: clip(t.Request, 600), Reply: threadReplySummary(t)}
		if turn.Request == "" {
			// A button press has no words; the model still needs to know it happened.
			turn.Request = manualRequestSummary(t)
		}
		size := len(turn.Request) + len(turn.Reply)
		if used+size > limit {
			break
		}
		used += size
		kept = append(kept, turn)
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return kept
}

func manualRequestSummary(t d.CurationThread) string {
	for _, a := range t.Actions {
		if a.Type == d.CurationActionResponse {
			continue
		}
		label := a.TargetLabel
		if label == "" {
			label = t.TargetLabels[a.TargetID]
		}
		return strings.TrimSpace("[button] " + string(a.Type) + " " + label)
	}
	return "[button]"
}

// threadReplySummary is what an earlier Thread said, for the model's eyes only:
// its saved reply when it has one, otherwise a terse record of what happened.
func threadReplySummary(t d.CurationThread) string {
	if a := t.ResponseAction(); a != nil && a.Response != nil {
		body := a.Response.Body
		for _, reference := range a.Response.References {
			title := reference.Title
			if title == "" {
				// A Shopify candidate has no saved name; the turn still has to read as a sentence.
				title = strings.TrimSpace("a " + t.TargetLabels[reference.TargetID] + " candidate")
			}
			body = strings.ReplaceAll(body, "[["+reference.Ref+"]]", title)
		}
		return clip(body, 900)
	}
	parts := []string{"status=" + t.Status}
	for _, a := range t.Actions {
		for _, job := range a.Jobs {
			for _, effect := range job.Effects {
				if effect.Kind == "CANDIDATES_ADDED" {
					parts = append(parts, "added "+strconv.Itoa(effect.Count)+" candidates for "+t.TargetLabels[effect.TargetID])
				}
				if effect.Kind == "NO_RESULTS" {
					parts = append(parts, "no new candidates for "+t.TargetLabels[effect.TargetID])
				}
			}
		}
		for _, effect := range a.Effects {
			parts = append(parts, strings.ToLower(effect.Kind))
		}
	}
	return clip(strings.Join(parts, "; "), 300)
}

func threadChanges(t d.CurationThread) []string {
	out := []string{}
	for _, a := range t.Actions {
		for _, effect := range a.Effects {
			label := effect.TargetLabel
			if label == "" {
				label = t.TargetLabels[effect.TargetID]
			}
			out = append(out, strings.TrimSpace(effect.Kind+" "+label))
		}
		for _, job := range a.Jobs {
			for _, effect := range job.Effects {
				label := t.TargetLabels[effect.TargetID]
				if effect.Kind == "CANDIDATES_ADDED" {
					out = append(out, "CANDIDATES_ADDED "+label+" count="+strconv.Itoa(effect.Count))
				} else {
					out = append(out, strings.TrimSpace(effect.Kind+" "+label))
				}
			}
		}
	}
	return out
}

// buildResponseContext assembles everything the responder may see. It runs
// outside the transaction: listing hydration and FX may call providers.
func (s *ThreadService) buildResponseContext(ctx context.Context, t d.CurationThread, a d.CurationAction, snapshot ThreadContext) (ResponseContext, error) {
	out := ResponseContext{OriginalIntent: clip(snapshot.OriginalIntent, 2000), Kind: a.Instruction, Locale: snapshot.Locale, Request: clip(t.Request, 2000), Changes: threadChanges(t), Targets: []ResponseTarget{}, History: []ResponseTurn{}, UserID: t.UserID, ModelKey: snapshot.ModelKey}
	answer := a.Instruction == d.ResponseKindAnswer || (a.Instruction == "COMBINATION" && len(snapshot.Targets) < 2)
	combination := s.combinationCart != nil && len(snapshot.Targets) > 1 && (!answer || a.Instruction == "COMBINATION")
	if a.Instruction == "COMBINATION" {
		out.Kind = d.ResponseKindComment
		if answer {
			out.Kind = d.ResponseKindAnswer
		}
	}
	researched := map[string]*d.ActionJobResult{}
	for i := range t.Actions {
		for j := range t.Actions[i].Jobs {
			job := &t.Actions[i].Jobs[j]
			if job.Kind == "RESEARCH_ROUND" && job.Status == "SUCCEEDED" && job.TargetID != "" {
				researched[job.TargetID] = job
			}
		}
	}
	targetIDs := []string{}
	for _, target := range snapshot.Targets {
		if answer || combination || researched[target.ID] != nil {
			targetIDs = append(targetIDs, target.ID)
		}
	}
	saved := map[string][]SavedCandidate{}
	if s.responseSource != nil && len(targetIDs) > 0 {
		var err error
		saved, err = s.responseSource.SavedCandidates(ctx, t.UserID, t.CurationID, targetIDs)
		if err != nil {
			return out, err
		}
	}
	if combination {
		for targetID, candidates := range saved {
			usable := []SavedCandidate{}
			for _, candidate := range candidates {
				if !candidate.Excluded {
					usable = append(usable, candidate)
				}
			}
			saved[targetID] = usable
		}
	}
	out.Budget = "NO_LIMIT"
	if snapshot.Budget.Enabled && snapshot.Budget.TotalAmount != nil {
		if minor, ok := moneyMinor(snapshot.Budget.TotalAmount, snapshot.Budget.Currency); ok {
			out.Budget = formatMinor(minor, snapshot.Budget.Currency)
		}
	}
	limit := responseCommentCandidates
	if answer {
		limit = responseAnswerCandidates
	}
	next := 1
	for _, target := range snapshot.Targets {
		if !answer && !combination && researched[target.ID] == nil {
			continue
		}
		view := ResponseTarget{ID: target.ID, Title: target.Title, Axes: []string{}, Candidates: []ResponseCandidate{}, PoolSize: len(saved[target.ID])}
		if target.Criteria != nil {
			view.Title = target.Criteria.Subject.Label
			for _, axis := range target.Criteria.Axes {
				view.Axes = append(view.Axes, axis.Label+" (importance "+strconv.Itoa(axis.Importance)+")")
			}
			view.Exclusions = target.Criteria.Exclusions
		}
		if job := researched[target.ID]; job != nil {
			view.Researched = true
			view.Facts = job.Facts
			for _, effect := range job.Effects {
				if effect.Kind == "CANDIDATES_ADDED" {
					view.Added += effect.Count
				}
			}
		}
		unit, hasBudget := int64(0), false
		if snapshot.Budget.Enabled {
			for _, allocation := range snapshot.Budget.Allocations {
				if allocation.TargetID != target.ID {
					continue
				}
				if minor, ok := moneyMinor(allocation.Amount, snapshot.Budget.Currency); ok && allocation.Quantity > 0 {
					unit, hasBudget = minor/int64(allocation.Quantity), true
					view.UnitBudget = formatMinor(unit, snapshot.Budget.Currency)
				}
			}
		}
		for rank, c := range RankSavedCandidates(saved[target.ID], unit, snapshot.Budget.Currency, hasBudget) {
			if rank >= limit {
				break
			}
			item := ResponseCandidate{Description: clip(c.Description, 800), AssessmentFacts: c.AssessmentFacts, VariantID: c.VariantID, ConfigurationVersion: c.ConfigurationVersion, Quantity: 1, Ref: "c" + strconv.Itoa(next), CandidateID: c.CandidateID, TargetID: target.ID, Title: c.Title, Seller: c.Seller, Source: c.Source,
				Price: c.Price, PriceMinor: c.PriceMinor, Currency: c.Currency, TotalScore: c.TotalScore, IntentPoint: clip(c.IntentPoint, responseTextLimit), Axes: []ResponseAxis{}, RoundID: c.RoundID, Rank: rank + 1, Budget: "UNKNOWN"}
			next++
			for _, axis := range c.Axes {
				axis.Explanation = clip(axis.Explanation, responseTextLimit)
				item.Axes = append(item.Axes, axis)
			}
			if job := researched[target.ID]; job != nil && job.Facts != nil && job.Facts.RoundID != "" {
				item.NewThisRound = c.RoundID == job.Facts.RoundID
			}
			if price := candidateMinor(c, snapshot.Budget.Currency); hasBudget && price != nil {
				item.Budget = "WITHIN"
				if *price > unit {
					item.Budget = "OVER"
				}
			}

			view.Candidates = append(view.Candidates, item)
			out.References = append(out.References, d.ResponseReference{Ref: item.Ref, CandidateID: c.CandidateID, TargetID: target.ID, Title: c.Title})
		}
		out.Targets = append(out.Targets, view)
	}
	if combination {
		if err := s.prepareCombinations(ctx, &out, snapshot, saved); err != nil {
			return out, err
		}
	}
	if answer || combination {
		_, threads, err := s.repo.ListThreads(ctx, t.UserID, t.CurationID)
		if err != nil {
			return out, err
		}
		out.History = responseHistory(threads, t.ID, responseHistoryBytes)
	}
	return out, nil
}

// runResponse is the RESPONSE branch of the Action's model Job. A failure is
// returned as a non-retryable fault: the Job closes as FAILED for operators,
// and the Thread worker then settles the Action as unavailable, not failed.
func (s *ThreadService) runResponse(ctx context.Context, user, curation, action, job, attempt string, revision int64) error {
	scope := ThreadExecutionFrom(ctx)
	unavailable := func(reason string) error { return fault.New(fault.ProviderRejected, reason, false) }
	if s.responder == nil {
		return unavailable("THREAD_RESPONDER_UNAVAILABLE")
	}
	var prepared d.CurationThread
	var snapshot ThreadContext
	err := s.curation.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		if e := s.repo.LockThreadCuration(tx, user, curation); e != nil {
			return e
		}
		t, e := s.repo.ReadThread(tx, user, scope.ThreadID)
		if e != nil {
			return e
		}
		a := t.CurrentAction()
		if !t.Active() || a == nil || string(a.ID) != action || a.Type != d.CurationActionResponse || a.Status != "RUNNING" || a.InputRevision != revision {
			return fault.New(fault.Conflict, "CURATION_ACTION_CHANGED", false)
		}
		snapshot, e = s.snapshot(tx, t)
		if e != nil {
			return e
		}
		prepared = t
		return nil
	})
	if err != nil {
		return err
	}
	input, err := s.buildResponseContext(ctx, prepared, *prepared.CurrentAction(), snapshot)
	if err != nil {
		return err
	}
	// A cancellation during lookup must not start a new model request.
	fresh, err := s.repo.ReadThread(ctx, user, scope.ThreadID)
	if err != nil {
		return err
	}
	current := fresh.CurrentAction()
	if !fresh.Active() || current == nil || string(current.ID) != action || current.Status != "RUNNING" || current.InputRevision != revision {
		return fault.New(fault.Conflict, "CURATION_ACTION_CHANGED", false)
	}
	input.ActionID, input.JobID, input.AttemptID, input.InputRevision = action, job, attempt, revision
	response, err := s.responder.Respond(ctx, input)
	if err != nil {
		reason := "THREAD_RESPONSE_FAILED"
		if f, ok := fault.As(err); ok && f.Reason != "" {
			reason = f.Reason
		}
		s.curation.logger.WarnContext(ctx, "thread response unavailable", "event", "curation.thread_response", "result", "unavailable", "thread_id", scope.ThreadID, "action_id", action, "kind", input.Kind, "reason_code", reason)
		return unavailable(reason)
	}
	return s.curation.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		if e := s.repo.LockThreadCuration(tx, user, curation); e != nil {
			return e
		}
		fresh, e := s.repo.ReadThread(tx, user, scope.ThreadID)
		if e != nil {
			return e
		}
		a := fresh.CurrentAction()
		if !fresh.Active() || a == nil || string(a.ID) != action || a.Status != "RUNNING" || a.InputRevision != revision {
			return fault.New(fault.Conflict, "CURATION_ACTION_CHANGED", false)
		}
		a.Response = &response
		a.Jobs = []d.ActionJobResult{{JobID: job, AttemptID: attempt, ActionID: action, Kind: "ACTION_INTERPRETATION", Status: "SUCCEEDED", Effects: []d.ActionEffect{}}}
		a.Status = "SUCCEEDED"
		fresh.Revision++
		fresh.RefreshStatus()
		s.curation.logger.InfoContext(ctx, "thread response written", "event", "curation.thread_response", "result", "success", "thread_id", fresh.ID, "action_id", action, "kind", response.Kind, "references", len(response.References))
		return s.repo.SaveThread(tx, fresh)
	})
}
