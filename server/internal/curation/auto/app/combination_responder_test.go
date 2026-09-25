package app

import (
	c "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	"strings"
	"testing"
	"time"
)

func TestCombinationModelCannotSupplyItemsOrMoney(t *testing.T) {
	amount := int64(159700)
	input := c.ResponseContext{Kind: "COMMENT", Locale: "ko-KR", References: []d.ResponseReference{{Ref: "c1", CandidateID: "real", TargetID: "t", Title: "Real"}}, Combinations: []d.CombinationResponse{{ID: "pick", MinimumMinor: &amount, Items: []d.CombinationItem{{Ref: "c1", CandidateID: "real"}}}}}
	out := combinationOutput{Body: "[[c1]]를 추천해요.", CombinationID: "pick", Compatibility: "UNVERIFIED", Reasons: []string{"함께 쓰기 좋은 구성이에요."}, Tips: []d.CombinationTip{{Label: "활용", Body: "가지고 있다면 함께 활용하세요."}}}
	reply, err := validateCombinationOutput(input, out, "test", time.Now())
	if err != nil || reply.SchemaVersion != "vitlane.thread-response.v2" || *reply.Combination.MinimumMinor != amount || reply.Combination.Items[0].CandidateID != "real" {
		t.Fatal(reply, err)
	}
	cases := []combinationOutput{out, out, out, out, out}
	cases[0].CombinationID = "invented"
	cases[1].Tips = []d.CombinationTip{{Label: "x", Body: "[[missing]]"}}
	cases[2].BudgetAdvice = "https://untrusted.invalid"
	cases[3].Reasons = []string{strings.Repeat("가", 321)}
	cases[4].Compatibility = "CONFLICT"
	for _, bad := range cases {
		if _, err := validateCombinationOutput(input, bad, "test", time.Now()); err == nil {
			t.Fatal("accepted invalid output", bad)
		}
	}
}

func TestCombinationBodyNamesOnlyItsChosenRepresentatives(t *testing.T) {
	input := c.ResponseContext{Kind: "COMMENT", Locale: "ko-KR", References: []d.ResponseReference{{Ref: "a", CandidateID: "a"}, {Ref: "b", CandidateID: "b"}, {Ref: "c", CandidateID: "c"}}, Combinations: []d.CombinationResponse{{ID: "match", Items: []d.CombinationItem{{Ref: "a"}, {Ref: "b"}}}}}
	out := combinationOutput{Body: "[[a]]와 [[b]] 조합을 추천해요.", CombinationID: "match", Compatibility: "UNVERIFIED"}
	if _, err := validateCombinationOutput(input, out, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"[[a]]를 추천해요.", "[[a]]와 [[b]] 대신 [[c]]를 대표로 추천해요."} {
		out.Body = body
		if _, err := validateCombinationOutput(input, out, "test", time.Now()); err == nil {
			t.Fatal("body disagrees with representative selection", body)
		}
	}
}
