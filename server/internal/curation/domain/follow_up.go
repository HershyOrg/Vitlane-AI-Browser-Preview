package domain

import (
	"fmt"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"strings"
	"time"
)

// FollowUp is a message capability, not an executable job. All terminal
// states remain readable, but can never transition back to PENDING.
type FollowUp struct {
	ID                string          `json:"id"`
	ResponseID        string          `json:"responseId"`
	Kind              string          `json:"kind"`
	Status            string          `json:"status"`
	Version           int64           `json:"version"`
	Content           FollowUpContent `json:"content"`
	Payload           *FollowUpAction `json:"-"`
	Fingerprint       string          `json:"-"`
	ResponseRequestID string          `json:"-"`
	CreatedAt         time.Time       `json:"createdAt"`
}
type FollowUpContent struct {
	Code        string `json:"code"`
	Body        string `json:"body,omitempty"`
	Locale      string `json:"locale,omitempty"`
	TargetTitle string `json:"targetTitle,omitempty"`
	Added       int    `json:"added,omitempty"`
	JobID       string `json:"jobId,omitempty"`
	ReasonCode  string `json:"reasonCode,omitempty"`
	Retryable   bool   `json:"retryable,omitempty"`
	// AvailableAt is when accepting starts to be useful; the Web waits for it.
	AvailableAt *time.Time `json:"availableAt,omitempty"`
}
type FollowUpAction struct {
	Subscription    *SubscriptionTerms `json:"subscription,omitempty"`
	Kind            string             `json:"kind"`
	TargetID        string             `json:"targetId"`
	SessionID       string             `json:"sessionId"`
	SessionVersion  int64              `json:"sessionVersion"`
	CriteriaVersion int64              `json:"criteriaVersion"`
	Feedback        string             `json:"feedback"`
}

func (m FollowUp) Respond(response string, version int64) (string, error) {
	if m.Status != "PENDING" {
		return "", fault.New(fault.Conflict, "FOLLOW_UP_SUPERSEDED", false)
	}
	if m.Version != version {
		return "", fault.New(fault.Conflict, "FOLLOW_UP_VERSION_CONFLICT", false)
	}
	if m.Kind == "PROPOSAL" {
		if response == "ACCEPT" && m.Payload != nil && m.Payload.Kind == "SUBSCRIBE_DEALS" && m.Payload.TargetID != "" && m.Payload.Subscription != nil {
			return "ACCEPTED", nil
		}
		if response == "ACCEPT" && m.Payload != nil && m.Payload.Kind == "RESEARCH_AGAIN" && m.Payload.TargetID != "" && m.Payload.SessionID != "" && m.Payload.SessionVersion > 0 && m.Payload.CriteriaVersion > 0 {
			return "ACCEPTED", nil
		}
		if response == "DISMISS" {
			return "DISMISSED", nil
		}
	} else if response == "ACKNOWLEDGE" {
		return "ACKNOWLEDGED", nil
	}
	return "", fault.New(fault.InvalidInput, "FOLLOW_UP_RESPONSE_INVALID", false)
}

// Research-again proposal codes. Each proposal stores the exact search feedback
// its acceptance runs with, so accepting it never repeats the previous search.
const (
	ProposalLowAxisFit          = "LOW_AXIS_FIT"
	ProposalFewNewCandidates    = "FEW_NEW_CANDIDATES"
	ProposalSourcesRateLimited  = "SOURCES_RATE_LIMITED"
	proposalFewNewLimit         = 2
	proposalRateLimitedCooldown = time.Minute
)

// MallName is a source's display name in both content locales.
type MallName struct{ Korean, English string }

// ProposalFacts are the verified facts of one Target in one response. Every
// number a proposal states comes from them.
type ProposalFacts struct {
	Criteria TargetCriteriaSetV1
	// Compared counts this response's new candidates assessed under the
	// current criteria; LowByAxis (<50) and GoodByAxis (>=60) count within them.
	Compared   int
	LowByAxis  map[string]int
	GoodByAxis map[string]int
	PoolSize   int
	// Researched is true when this response completed a research Round for the
	// Target. Observed, Duplicates and Admitted sum those Rounds' outcomes.
	Researched                     bool
	Observed, Duplicates, Admitted int
	// RateLimitedMalls were skipped or failed on a call limit without a candidate.
	RateLimitedMalls []MallName
	PreviousQuery    string
}

// ResearchAgainProposals returns every research-again proposal the facts
// support, in a fixed order. Research does not change criteria; the stored
// feedback changes only the search. The caller still gates the response and
// shows at most one proposal.
func ResearchAgainProposals(f ProposalFacts, locale string, action FollowUpAction, fingerprint string, now time.Time) []FollowUp {
	if f.PoolSize >= 50 {
		return nil
	}
	english := locale == "en-US"
	subject := f.Criteria.Subject.Label
	proposal := func(code, body, feedback string, availableAt *time.Time) FollowUp {
		payload := action
		payload.Feedback = feedback
		return FollowUp{Kind: "PROPOSAL", Content: FollowUpContent{Code: code, Body: body, Locale: locale, TargetTitle: subject, AvailableAt: availableAt}, Payload: &payload, Fingerprint: fingerprint}
	}
	out := []FollowUp{}
	if f.Compared >= 3 {
		for _, axis := range f.Criteria.Axes {
			low, good := f.LowByAxis[axis.AxisID], f.GoodByAxis[axis.AxisID]
			if axis.Importance < 3 || (low*2 <= f.Compared && good > 1) {
				continue
			}
			var body string
			switch {
			case english && low*2 > f.Compared:
				body = fmt.Sprintf("%d of %d new candidates scored below 50 on %s. Shall I search for %s again, prioritizing %s?", low, f.Compared, axis.Label, subject, axis.Label)
			case english && good == 0:
				body = fmt.Sprintf("None of the %d new candidates scored 60 or more on %s. Shall I search for %s again, prioritizing %s?", f.Compared, axis.Label, subject, axis.Label)
			case english:
				body = fmt.Sprintf("Only %d of %d new candidates scored 60 or more on %s. Shall I search for %s again, prioritizing %s?", good, f.Compared, axis.Label, subject, axis.Label)
			case low*2 > f.Compared:
				body = fmt.Sprintf("새 후보 %d개 중 %d개가 ‘%s’ 평가 50점 미만이었어요. ‘%s’%s 더 잘 충족하는 후보를 우선해 %s 다시 찾아볼까요?", f.Compared, low, axis.Label, axis.Label, objectParticle(axis.Label), withObjectParticle(subject))
			case good == 0:
				body = fmt.Sprintf("새 후보 %d개 모두 ‘%s’ 평가가 60점 미만이었어요. ‘%s’%s 더 잘 충족하는 후보를 우선해 %s 다시 찾아볼까요?", f.Compared, axis.Label, axis.Label, objectParticle(axis.Label), withObjectParticle(subject))
			default:
				body = fmt.Sprintf("새 후보 %d개 중 ‘%s’ 평가 60점 이상은 %d개뿐이에요. ‘%s’%s 더 잘 충족하는 후보를 우선해 %s 다시 찾아볼까요?", f.Compared, axis.Label, good, axis.Label, objectParticle(axis.Label), withObjectParticle(subject))
			}
			feedback := fmt.Sprintf("‘%s’ 기준을 더 잘 충족하는 후보를 우선해서 찾아줘.", axis.Label)
			if english {
				feedback = fmt.Sprintf("Prioritize candidates that better meet the %s criterion.", axis.Label)
			}
			out = append(out, proposal(ProposalLowAxisFit, body, feedback, nil))
			break
		}
	}
	if f.Researched && f.Admitted <= proposalFewNewLimit {
		var body string
		switch {
		case english && f.Observed == 0:
			body = fmt.Sprintf("This search returned no results. Shall I search for %s again with different wording and brands?", subject)
		case english && f.Duplicates*2 >= f.Observed:
			body = fmt.Sprintf("Most search results were candidates you already have (%d of %d), so %s added. Shall I search for %s again with different wording and brands?", f.Duplicates, f.Observed, englishCount(f.Admitted, "new candidate was", "new candidates were"), subject)
		case english:
			body = fmt.Sprintf("The search returned %s, so %s added. Shall I search for %s again with different wording and brands?", englishCount(f.Observed, "result", "results"), englishCount(f.Admitted, "new candidate was", "new candidates were"), subject)
		case f.Observed == 0:
			body = fmt.Sprintf("이번 검색에서는 결과를 찾지 못했어요. 다른 표현과 브랜드로 %s 넓혀 다시 찾아볼까요?", withObjectParticle(subject))
		case f.Duplicates*2 >= f.Observed:
			body = fmt.Sprintf("검색 결과 %d개 중 %d개가 이미 있던 후보라 새 후보는 %d개였어요. 다른 표현과 브랜드로 %s 넓혀 다시 찾아볼까요?", f.Observed, f.Duplicates, f.Admitted, withObjectParticle(subject))
		default:
			body = fmt.Sprintf("검색 결과가 %d개라 새 후보는 %d개였어요. 다른 표현과 브랜드로 %s 넓혀 다시 찾아볼까요?", f.Observed, f.Admitted, withObjectParticle(subject))
		}
		// Naming the previous query steers the new query call away from it.
		feedback := "이전 검색어와 다른 표현·브랜드·하위 종류로 넓혀서 찾아줘."
		if english {
			feedback = "Search with different wording, brands or subtypes than the previous query."
		}
		// Research feedback is capped at 2000 bytes; a query is far shorter.
		if query := []rune(strings.TrimSpace(f.PreviousQuery)); len(query) > 0 {
			if len(query) > 200 {
				query = query[:200]
			}
			if english {
				feedback += " Previous query: " + string(query)
			} else {
				feedback += " 이전 검색어: " + string(query)
			}
		}
		out = append(out, proposal(ProposalFewNewCandidates, body, feedback, nil))
	}
	if f.Researched && len(f.RateLimitedMalls) > 0 {
		names := make([]string, len(f.RateLimitedMalls))
		for n, mall := range f.RateLimitedMalls {
			names[n] = mall.Korean
			if english {
				names[n] = mall.English
			}
		}
		body := fmt.Sprintf("%s 호출 한도로 이번에 확인하지 못했어요. 잠시 뒤 %s 다시 찾아볼까요?", withTopicParticle(strings.Join(names, "·")), withObjectParticle(subject))
		if english {
			body = fmt.Sprintf("%s could not be checked this time because of call limits. Shall I search for %s again in a moment?", strings.Join(names, ", "), subject)
		}
		// A per-minute call limit clears within a minute; accepting sooner would
		// meet the same limit. The same search then reaches the skipped malls.
		availableAt := now.UTC().Add(proposalRateLimitedCooldown)
		out = append(out, proposal(ProposalSourcesRateLimited, body, "", &availableAt))
	}
	return out
}

func englishCount(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// Korean particles follow the final syllable: a final consonant takes 을/은.
// A word that does not end in Hangul shows both forms.
func objectParticle(word string) string { return hangulParticle(word, "을", "를", "을(를)") }
func withObjectParticle(word string) string {
	return word + objectParticle(word)
}
func withTopicParticle(word string) string {
	return word + hangulParticle(word, "은", "는", "은(는)")
}
func hangulParticle(word, consonant, vowel, unknown string) string {
	runes := []rune(strings.TrimSpace(word))
	if len(runes) == 0 {
		return unknown
	}
	last := runes[len(runes)-1]
	if last < 0xAC00 || last > 0xD7A3 {
		return unknown
	}
	if (last-0xAC00)%28 == 0 {
		return vowel
	}
	return consonant
}
