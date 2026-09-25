package app

import (
	"fmt"
	"testing"
)

func TestResearchImagesKeepAllSelectedCandidates(t *testing.T) {
	for _, count := range []int{0, 15, 50} {
		items := make([]ResearchCandidateObservation, count)
		for i := range items {
			items[i].ObservationID = fmt.Sprint(i)
			items[i].ImageURL = "https://example.com/item.jpg"
		}
		context := ResearchContext{Criteria: &ResearchCriteria{Axes: []ResearchAxis{{UsesVisualEvidence: true}}}}
		if images := researchImages(context, items); len(images) != count {
			t.Fatalf("images=%d candidates=%d", len(images), count)
		}
		context.Criteria.Axes[0].UsesVisualEvidence = false
		if len(researchImages(context, items)) != 0 {
			t.Fatal("nonvisual axes must not attach images")
		}
	}
}
