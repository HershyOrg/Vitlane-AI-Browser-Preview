package app

import (
	"strings"
	"testing"
)

func TestAxisRankingOmitsStructuredPriceUnlessAnAxisUsesIt(t *testing.T) {
	context := ResearchContext{ContentLocale: "en-US", Criteria: &ResearchCriteria{Subject: ResearchSubject{Label: "pen", ProductType: "pen"}, Axes: []ResearchAxis{{AxisID: "writing", Label: "Writing", Definition: "Writing comfort", Importance: 5}}}}
	observations := []ResearchCandidateObservation{{ObservationID: "product", Name: "Pen", PriceMinimum: Money{Amount: "123.45", Currency: "USD"}, PriceMaximum: Money{Amount: "234.56", Currency: "USD"}}}
	prompt := rankingPrompt(context, observations, 16)
	if strings.Contains(prompt, "123.45") || strings.Contains(prompt, "234.56") {
		t.Fatal("price leaked without an explicit axis")
	}
	context.Criteria.Axes[0].UsesPrice = true
	if !strings.Contains(rankingPrompt(context, observations, 16), "minPrice=123.45 USD") {
		t.Fatal("explicit price axis lost its observation")
	}
	if observations[0].PriceMinimum.Amount != "123.45" {
		t.Fatal("prompt construction mutated the source observation")
	}
}
