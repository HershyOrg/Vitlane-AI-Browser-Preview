package research

import (
	"context"
	"testing"

	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

type stubResearchService struct {
	contextResult     researchapp.ContextResult
	contextJobID      string
	contextRound      string
	catalogInputQuery researchapp.CatalogIntelligenceCatalogQuery
	failureReason     string
	failureRetryable  bool
}

func (s *stubResearchService) FailResearchRound(
	_ context.Context, _, _, reasonCode string, retryable bool,
) error {
	s.failureReason = reasonCode
	s.failureRetryable = retryable
	return nil
}

func (s *stubResearchService) RunCatalogResearchForIntelligence(
	_ context.Context,
	_ string,
	_ string,
	_ string,
	_ string,
	query researchapp.CatalogIntelligenceCatalogQuery,
	_ researchapp.CatalogIntelligenceCandidateRanker,
) (researchapp.CatalogIntelligenceResearchResult, error) {
	s.catalogInputQuery = query
	return researchapp.CatalogIntelligenceResearchResult{}, nil
}

func (s *stubResearchService) GetContextForIntelligence(
	_ context.Context, _ string, jobID string, roundID string,
) (researchapp.ContextResult, error) {
	s.contextJobID = jobID
	s.contextRound = roundID
	return s.contextResult, nil
}

func (s *stubResearchService) StartReadySessionResearch(
	context.Context, researchapp.StartReadySessionResearchInput,
) (researchapp.StartReadySessionResearchResult, error) {
	return researchapp.StartReadySessionResearchResult{}, nil
}

func (s *stubResearchService) StartResearch(
	context.Context, researchapp.StartResearchInput,
) (researchapp.StartResearchResult, error) {
	return researchapp.StartResearchResult{}, nil
}

func (s *stubResearchService) CancelResearchRound(
	context.Context, string, string,
) error {
	return nil
}

func TestReadResearchContextCarriesFeedbackAndJobProvenance(t *testing.T) {
	service := &stubResearchService{contextResult: researchapp.ContextResult{
		ContextVersion:   2,
		ContextHash:      "context-hash",
		FeedbackRequired: true,
		FeedbackSummary:  "더 가벼운 상품을 찾아 주세요.",
		FeedbackVersion:  1,
		FeedbackHash:     "feedback-hash",
	}}
	adapter := NewAdapter(service, nil)

	result, err := adapter.ReadResearchContext(
		context.Background(), "user-1",
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
	)
	if err != nil {
		t.Fatal(err)
	}
	if service.contextJobID != "11111111-1111-4111-8111-111111111111" ||
		service.contextRound != "22222222-2222-4222-8222-222222222222" ||
		!result.FeedbackRequired || result.FeedbackVersion != 1 ||
		result.FeedbackHash != "feedback-hash" ||
		result.FeedbackSummary != "더 가벼운 상품을 찾아 주세요." {
		t.Fatalf("feedback context was not preserved: %#v", result)
	}
}

func TestRunCatalogResearchPassesManagedQueryToResearch(t *testing.T) {
	service := &stubResearchService{}
	adapter := NewAdapter(service, nil)

	_, err := adapter.RunCatalogResearch(
		context.Background(), "user-1",
		"11111111-1111-4111-8111-111111111111",
		"33333333-3333-4333-8333-333333333333",
		"22222222-2222-4222-8222-222222222222",
		intelligenceapp.CatalogQueryPayload{
			Query: "trail running vest", MustInclude: []string{"hydration"}, ProductVertical: "FASHION",
			MustExclude: []string{"kids"},
		},
		func(
			context.Context,
			[]intelligenceapp.ResearchCandidateObservation,
			int,
		) ([]intelligenceapp.ResearchRankedCandidate, error) {
			return nil, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if service.catalogInputQuery.Query != "trail running vest" ||
		len(service.catalogInputQuery.MustInclude) != 1 ||
		service.catalogInputQuery.MustInclude[0] != "hydration" ||
		service.catalogInputQuery.ProductVertical != "FASHION" ||
		len(service.catalogInputQuery.MustExclude) != 1 ||
		service.catalogInputQuery.MustExclude[0] != "kids" {
		t.Fatalf("catalog query was not preserved: %#v", service.catalogInputQuery)
	}
}

func TestFailResearchTargetPreservesSafeTerminalReason(t *testing.T) {
	service := &stubResearchService{}
	adapter := NewAdapter(service, nil)
	if err := adapter.FailResearchTarget(
		context.Background(), "user-1", "round-1",
		"CANDIDATE_RANKING_EMPTY", false,
	); err != nil {
		t.Fatal(err)
	}
	if service.failureReason != "CANDIDATE_RANKING_EMPTY" ||
		service.failureRetryable {
		t.Fatalf("failure=%q retryable=%t", service.failureReason, service.failureRetryable)
	}
}
