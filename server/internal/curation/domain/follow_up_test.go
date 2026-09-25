package domain

import (
	"strings"
	"testing"
	"time"
)

func TestFollowUpRespondCapability(t *testing.T) {
	for _, kind := range []string{"PROPOSAL", "ERROR", "NOTICE", "CLARIFICATION"} {
		for _, action := range []string{"ACCEPT", "DISMISS", "ACKNOWLEDGE", "OTHER"} {
			m := FollowUp{Kind: kind, Status: "PENDING", Version: 1, Payload: &FollowUpAction{Kind: "RESEARCH_AGAIN", TargetID: "target", SessionID: "session", SessionVersion: 1, CriteriaVersion: 1}}
			_, err := m.Respond(action, 1)
			valid := kind == "PROPOSAL" && (action == "ACCEPT" || action == "DISMISS") || kind != "PROPOSAL" && action == "ACKNOWLEDGE"
			if (err == nil) != valid {
				t.Fatalf("%s/%s: %v", kind, action, err)
			}
		}
	}
	for _, state := range []string{"ACCEPTED", "DISMISSED", "ACKNOWLEDGED", "SUPERSEDED"} {
		m := FollowUp{Kind: "PROPOSAL", Status: state, Version: 2, Payload: &FollowUpAction{}}
		if _, err := m.Respond("ACCEPT", 2); err == nil {
			t.Fatal("reactivated", state)
		}
	}
	m := FollowUp{Kind: "PROPOSAL", Status: "PENDING", Version: 2, Payload: &FollowUpAction{}}
	if _, err := m.Respond("ACCEPT", 1); err == nil {
		t.Fatal("stale version accepted")
	}
}

var proposalAction = FollowUpAction{Kind: "RESEARCH_AGAIN", TargetID: "target", SessionID: "session", SessionVersion: 1, CriteriaVersion: 1}

func proposalCodes(ms []FollowUp) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.Content.Code)
	}
	return out
}

// Accepting any proposal keeps its exact target, session and criteria version;
// only the stored search feedback differs by proposal.
func assertProposalAction(t *testing.T, m FollowUp) {
	t.Helper()
	p := m.Payload
	if m.Kind != "PROPOSAL" || p == nil || p.Kind != "RESEARCH_AGAIN" || p.TargetID != "target" || p.SessionID != "session" || p.SessionVersion != 1 || p.CriteriaVersion != 1 || m.Fingerprint != "context" {
		t.Fatalf("proposal changed the action: %+v %+v", m, p)
	}
}

func TestLowAxisFitProposalRelaxesToFewGoodCandidates(t *testing.T) {
	c := TargetCriteriaSetV1{Subject: ResearchSubject{Label: "pen"}, Axes: []ResearchAxis{{AxisID: "portable", Label: "portability", Importance: 4}}}
	now := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                      string
		compared, low, good, pool int
		want                      bool
	}{
		{"majority below 50 (original rule)", 3, 2, 1, 49, true},
		{"majority below 50 with several good", 5, 3, 2, 10, true},
		{"at most one scored 60 or more", 5, 1, 1, 10, true},
		{"none scored 60 or more", 4, 0, 0, 10, true},
		{"enough good and no low majority", 5, 2, 2, 10, false},
		{"fewer than three compared", 2, 2, 0, 10, false},
		{"pool at capacity", 3, 3, 0, 50, false},
	} {
		ms := ResearchAgainProposals(ProposalFacts{Criteria: c, Compared: tc.compared, LowByAxis: map[string]int{"portable": tc.low}, GoodByAxis: map[string]int{"portable": tc.good}, PoolSize: tc.pool}, "en-US", proposalAction, "context", now)
		if (len(ms) == 1) != tc.want || len(ms) > 1 {
			t.Fatalf("%s: %v", tc.name, proposalCodes(ms))
		}
		if len(ms) == 1 {
			assertProposalAction(t, ms[0])
			if ms[0].Content.Code != ProposalLowAxisFit || ms[0].Content.AvailableAt != nil || !strings.Contains(ms[0].Payload.Feedback, "portability") || ms[0].Content.TargetTitle != "pen" {
				t.Fatalf("%s: %+v %+v", tc.name, ms[0].Content, ms[0].Payload)
			}
		}
	}
	c.Axes[0].Importance = 2
	if ms := ResearchAgainProposals(ProposalFacts{Criteria: c, Compared: 3, LowByAxis: map[string]int{"portable": 3}, PoolSize: 4}, "ko-KR", proposalAction, "context", now); len(ms) != 0 {
		t.Fatal("unimportant axis proposed")
	}
	// The first qualifying axis in criteria order names the proposal.
	c.Axes = []ResearchAxis{{AxisID: "weight", Label: "무게", Importance: 3}, {AxisID: "portable", Label: "휴대성", Importance: 5}}
	c.Subject.Label = "만년필"
	ms := ResearchAgainProposals(ProposalFacts{Criteria: c, Compared: 4, LowByAxis: map[string]int{"weight": 3, "portable": 4}, GoodByAxis: map[string]int{"weight": 0, "portable": 0}, PoolSize: 4}, "ko-KR", proposalAction, "context", now)
	if len(ms) != 1 || ms[0].Content.Body != "새 후보 4개 중 3개가 ‘무게’ 평가 50점 미만이었어요. ‘무게’를 더 잘 충족하는 후보를 우선해 만년필을 다시 찾아볼까요?" || ms[0].Payload.Feedback != "‘무게’ 기준을 더 잘 충족하는 후보를 우선해서 찾아줘." {
		t.Fatalf("korean weak axis: %+v", ms)
	}
	ms = ResearchAgainProposals(ProposalFacts{Criteria: c, Compared: 5, LowByAxis: map[string]int{"weight": 0, "portable": 2}, GoodByAxis: map[string]int{"weight": 5, "portable": 1}, PoolSize: 5}, "ko-KR", proposalAction, "context", now)
	if len(ms) != 1 || ms[0].Content.Body != "새 후보 5개 중 ‘휴대성’ 평가 60점 이상은 1개뿐이에요. ‘휴대성’을 더 잘 충족하는 후보를 우선해 만년필을 다시 찾아볼까요?" {
		t.Fatalf("korean few good: %+v", ms)
	}
}

func TestFewNewCandidatesProposalSearchesDifferently(t *testing.T) {
	c := TargetCriteriaSetV1{Subject: ResearchSubject{Label: "무선 키보드"}, Axes: []ResearchAxis{{AxisID: "quiet", Label: "소음", Importance: 4}}}
	now := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                                 string
		researched                           bool
		observed, duplicates, admitted, pool int
		wantBody                             string
	}{
		{"mostly duplicates", true, 20, 18, 2, 12, "검색 결과 20개 중 18개가 이미 있던 후보라 새 후보는 2개였어요. 다른 표현과 브랜드로 무선 키보드를 넓혀 다시 찾아볼까요?"},
		{"few results", true, 3, 1, 1, 12, "검색 결과가 3개라 새 후보는 1개였어요. 다른 표현과 브랜드로 무선 키보드를 넓혀 다시 찾아볼까요?"},
		{"no results", true, 0, 0, 0, 12, "이번 검색에서는 결과를 찾지 못했어요. 다른 표현과 브랜드로 무선 키보드를 넓혀 다시 찾아볼까요?"},
		{"three new is enough", true, 10, 0, 3, 12, ""},
		{"not researched in this response", false, 0, 0, 0, 12, ""},
		{"pool at capacity", true, 20, 19, 1, 50, ""},
	} {
		ms := ResearchAgainProposals(ProposalFacts{Criteria: c, PoolSize: tc.pool, Researched: tc.researched, Observed: tc.observed, Duplicates: tc.duplicates, Admitted: tc.admitted, PreviousQuery: "무선 저소음 키보드"}, "ko-KR", proposalAction, "context", now)
		if tc.wantBody == "" {
			if len(ms) != 0 {
				t.Fatalf("%s: %v", tc.name, proposalCodes(ms))
			}
			continue
		}
		if len(ms) != 1 || ms[0].Content.Code != ProposalFewNewCandidates || ms[0].Content.Body != tc.wantBody || ms[0].Content.AvailableAt != nil {
			t.Fatalf("%s: %+v", tc.name, ms)
		}
		assertProposalAction(t, ms[0])
		if ms[0].Payload.Feedback != "이전 검색어와 다른 표현·브랜드·하위 종류로 넓혀서 찾아줘. 이전 검색어: 무선 저소음 키보드" {
			t.Fatalf("%s feedback: %q", tc.name, ms[0].Payload.Feedback)
		}
	}
	ms := ResearchAgainProposals(ProposalFacts{Criteria: c, PoolSize: 1, Researched: true, Observed: 4, Duplicates: 1, Admitted: 1, PreviousQuery: strings.Repeat("가", 300)}, "en-US", proposalAction, "context", now)
	if len(ms) != 1 || ms[0].Content.Body != "The search returned 4 results, so 1 new candidate was added. Shall I search for 무선 키보드 again with different wording and brands?" || ms[0].Payload.Feedback != "Search with different wording, brands or subtypes than the previous query. Previous query: "+strings.Repeat("가", 200) || len(ms[0].Payload.Feedback) > 2000 {
		t.Fatalf("english few new: %+v", ms)
	}
	ms = ResearchAgainProposals(ProposalFacts{Criteria: c, PoolSize: 1, Researched: true, Observed: 0, Admitted: 0}, "en-US", proposalAction, "context", now)
	if len(ms) != 1 || ms[0].Payload.Feedback != "Search with different wording, brands or subtypes than the previous query." {
		t.Fatalf("no previous query: %+v", ms)
	}
	ms = ResearchAgainProposals(ProposalFacts{Criteria: c, PoolSize: 1, Researched: true, Observed: 12, Duplicates: 12, Admitted: 0}, "en-US", proposalAction, "context", now)
	if len(ms) != 1 || ms[0].Content.Body != "Most search results were candidates you already have (12 of 12), so 0 new candidates were added. Shall I search for 무선 키보드 again with different wording and brands?" {
		t.Fatalf("english duplicates: %+v", ms)
	}
}

func TestRateLimitedSourcesProposalWaitsForTheLimit(t *testing.T) {
	c := TargetCriteriaSetV1{Subject: ResearchSubject{Label: "만년필"}, Axes: []ResearchAxis{{AxisID: "ink", Label: "잉크", Importance: 4}}}
	now := time.Date(2026, 9, 18, 1, 0, 0, 0, time.FixedZone("KST", 9*3600))
	malls := []MallName{{Korean: "쿠팡", English: "Coupang"}, {Korean: "11번가", English: "11st"}}
	facts := ProposalFacts{Criteria: c, Compared: 5, LowByAxis: map[string]int{"ink": 0}, GoodByAxis: map[string]int{"ink": 5}, PoolSize: 5, Researched: true, Observed: 9, Admitted: 5, RateLimitedMalls: malls}
	ms := ResearchAgainProposals(facts, "ko-KR", proposalAction, "context", now)
	if len(ms) != 1 || ms[0].Content.Code != ProposalSourcesRateLimited || ms[0].Content.Body != "쿠팡·11번가는 호출 한도로 이번에 확인하지 못했어요. 잠시 뒤 만년필을 다시 찾아볼까요?" || ms[0].Payload.Feedback != "" {
		t.Fatalf("korean rate limited: %+v", ms)
	}
	assertProposalAction(t, ms[0])
	if at := ms[0].Content.AvailableAt; at == nil || !at.Equal(now.Add(time.Minute)) || at.Location() != time.UTC {
		t.Fatalf("availableAt=%v", ms[0].Content.AvailableAt)
	}
	ms = ResearchAgainProposals(facts, "en-US", proposalAction, "context", now)
	if len(ms) != 1 || ms[0].Content.Body != "Coupang, 11st could not be checked this time because of call limits. Shall I search for 만년필 again in a moment?" {
		t.Fatalf("english rate limited: %+v", ms)
	}
	// Proposals for one Target come in a fixed order and each keeps its own feedback.
	facts.Admitted, facts.Compared, facts.GoodByAxis = 1, 1, map[string]int{}
	ms = ResearchAgainProposals(facts, "ko-KR", proposalAction, "context", now)
	if codes := proposalCodes(ms); strings.Join(codes, ",") != ProposalFewNewCandidates+","+ProposalSourcesRateLimited || ms[0].Payload == ms[1].Payload {
		t.Fatalf("combined proposals: %v", codes)
	}
	facts.Researched = false
	if ms = ResearchAgainProposals(facts, "ko-KR", proposalAction, "context", now); len(ms) != 0 {
		t.Fatalf("rate limit without a Round in this response: %v", proposalCodes(ms))
	}
}

func TestKoreanParticlesFollowTheFinalSyllable(t *testing.T) {
	for word, want := range map[string]string{"휴대성": "휴대성을", "무게": "무게를", "11번가": "11번가를", "Lamy": "Lamy을(를)", "": "을(를)"} {
		if got := withObjectParticle(word); got != want {
			t.Fatalf("%q: %q want %q", word, got, want)
		}
	}
	if got := withTopicParticle("쿠팡·무신사"); got != "쿠팡·무신사는" {
		t.Fatal(got)
	}
	if got := withTopicParticle("쿠팡"); got != "쿠팡은" {
		t.Fatal(got)
	}
}
