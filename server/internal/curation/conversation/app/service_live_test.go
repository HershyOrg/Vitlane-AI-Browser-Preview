package app

import (
	"context"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	i "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	ra "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/app"
	rd "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
	ri "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/infra/intelligence"
	ro "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/infra/openaiapi"
	rp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/infra/postgres"
	shared "github.com/vitlane/vitlane/server/internal/shared/app"
	dbpg "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type liveGenerationRepo struct {
	repoStub
	generation *Generation
}

func (r *liveGenerationRepo) ClaimResponse(context.Context) (*Generation, error) {
	return r.generation, nil
}

type observedLiveProvider struct {
	i.Provider
	calls int
	err   error
}

func (p *observedLiveProvider) Complete(ctx context.Context, in i.CompletionRequest) (i.CompletionResult, error) {
	p.calls++
	out, err := p.Provider.Complete(ctx, in)
	p.err = err
	return out, err
}

// Explicit opt-in. Only synthetic observation text is sent through the real
// Managed adapter and budget ledger; it does not read user or catalog content.
func TestLiveManagedFollowUpSelection(t *testing.T) {
	key := os.Getenv("VITLANE_FOLLOWUP_TEST_API_KEY")
	if key == "" {
		t.Skip("explicit live synthetic test is disabled")
	}
	url := os.Getenv("TEST_DATABASE_URL")
	if !strings.Contains(url, "/vitlane_curation_step5_live?") {
		t.Fatal("requires dedicated Step 5 live fixture database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := dbpg.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ids := shared.UUIDGenerator{}
	registry, err := rd.NewRegistry(rd.DefaultModels(), rd.DefaultModelKey)
	if err != nil {
		t.Fatal(err)
	}
	budget := ra.NewBudgetService(rp.NewLedger(db), rd.DefaultLimits(), db, shared.SystemClock{}, ids)
	provider, err := ri.NewProvider(budget, ro.NewClient("", key, &http.Client{Timeout: 20 * time.Second}), registry, runtimepolicy.NewFinalizer(ctx, 3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	proposals := d.ResearchAgainProposals(d.ProposalFacts{Criteria: d.TargetCriteriaSetV1{Version: 1, Subject: d.ResearchSubject{Label: "Synthetic travel pen"}, Axes: []d.ResearchAxis{{AxisID: "portable", Label: "portability", Importance: 5}}}, Compared: 5, LowByAxis: map[string]int{"portable": 4}, PoolSize: 5}, "en-US", d.FollowUpAction{Kind: "RESEARCH_AGAIN", TargetID: ids.NewID(), SessionID: ids.NewID(), SessionVersion: 1, CriteriaVersion: 1}, "synthetic-context", time.Now())
	if len(proposals) != 1 {
		t.Fatalf("synthetic proposals=%d", len(proposals))
	}
	m := &proposals[0]
	repo := &liveGenerationRepo{generation: &Generation{UserID: "e5000000-0000-4000-8000-000000000101", ResponseID: ids.NewID(), Choices: []d.FollowUp{*m}}}
	observed := &observedLiveProvider{Provider: provider}
	service := &Service{Repository: repo, Provider: observed, Model: rd.DefaultModelKey}
	if err = service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if observed.calls != 1 || observed.err != nil || repo.finished != 1 {
		t.Fatalf("calls=%d error=%v finished=%d", observed.calls, observed.err, repo.finished)
	}
	if repo.chosen != nil && repo.chosen.Payload.TargetID != m.Payload.TargetID {
		t.Fatal("model changed exact action")
	}
	t.Logf("Live Managed selection succeeded; proposal selected=%t; synthetic facts only", repo.chosen != nil)
}
