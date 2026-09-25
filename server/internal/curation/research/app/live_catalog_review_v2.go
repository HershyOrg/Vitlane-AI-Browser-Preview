package app

import (
	"context"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
	"strings"
	"sync"
	"time"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const (
	LiveCatalogReviewPolicyVersionV2 = "phase8-live-review.v1"
)

type LiveCatalogReviewConfigV2 struct {
	MaximumCallsPerWindow int
	Window                time.Duration
	MaximumConcurrent     int
}

type LiveCatalogReviewSearchInputV2 struct {
	Query        string
	Intent       string
	Country      string
	Currency     string
	MinimumMinor *int64
	MaximumMinor *int64
	Limit        int
	// PreferredTerms는 모델이 사용자 피드백에서 유도한 선호 특징이다. 랭킹
	// 가중치(soft)로만 쓴다 — 카탈로그 텍스트에 문자 그대로 없다고 후보를
	// 탈락시키면 주관적 피드백이 전 후보를 지워 재조사가 항상 실패한다.
	PreferredTerms []string
	// Semantic exclusions inform criteria evaluation; they are not literal bans.
	PreferredExclusions []string
	// HardLexicalTerms는 명시적 사용자 제약만 담는다. 각 term이 관측 텍스트에
	// 존재하지 않으면 후보를 탈락시킨다.
	HardLexicalTerms []string
	Exclusions       []string
}

type LiveCatalogReviewMetricsV2 struct {
	DiscoveryOutcome *ResearchDiscoveryOutcome
	// Saved with the pool so deterministic expansion retains its provider context.
	ProviderCountry           string
	ProviderQuery             string
	SourceCoverage            []SourceCoverage
	PolicyVersion             string
	StartedAt                 time.Time
	CompletedAt               time.Time
	Duration                  time.Duration
	AICallCount               int
	ShopifyCallCount          int
	LocalCallsUsed            int
	LocalCallsRemaining       int
	LocalRateLimit            int
	LocalRateWindow           time.Duration
	ProviderCostStatus        string
	ProviderBillingCredential bool
	ExternalEffect            string
}

type LiveCatalogReviewResultV2 struct {
	DiscoveryDuplicateCount int
	NextProgress            SourceProgressSet
	Search                  CatalogProductSearchResult
	Metrics                 LiveCatalogReviewMetricsV2
	CandidateEligibleCount  int
	DiscardedNoLocatorCount int
	// ProviderRejectedCount is the provider-side count of search results that
	// were not products. It joins DiscardedNoLocatorCount in the Round's
	// discovery outcome as rejected, never as a source failure.
	ProviderRejectedCount int
	// EvaluatedCount and UnevaluatedCount split the admitted products by
	// whether a valid axis assessment came back for them. An unevaluated
	// product is still a candidate; it sorts as 0 until a later evaluation.
	EvaluatedCount       int
	UnevaluatedCount     int
	CandidateAssessments map[string]LiveCandidateAssessmentV2
}

// LiveCandidateAssessmentV2 is the durable-safe, English assessment compiled
// from a fresh provider observation. It deliberately contains no raw catalog
// title, price, media URL, or provider message content.
type LiveCandidateAssessmentV2 struct {
	AxisAssessment *researchdomain.AxisAssessmentV1
	IntentPoint    string
	Features       []string
	Specifications []string
}

type LiveCatalogReviewServiceV2 struct {
	discoveryAdapters []DiscoveryAdapter
	responseHandoff   discoveryResponseHandoff
	budgetReader      BudgetReader
	amazon            AmazonCatalogGateway
	korean            ExternalCatalogGateway
	exchangeRate      *ExchangeRateService
	gateway           liveCatalogGatewayV2
	variantChoice     *VariantChoiceServiceV2
	workspace         CatalogWorkspaceRepositoryV2
	cartReader        CatalogCartDraftReaderV2
	foregroundWork    CatalogCurationForegroundWorkReaderV2
	targetResearch    TargetResearchStateReader
	clock             sharedapp.Clock
	config            LiveCatalogReviewConfigV2

	mu            sync.Mutex
	windowStarted time.Time
	windowCalls   int
	activeCalls   int
}

// CatalogCurationForegroundWorkReaderV2 is the read-only bridge to the
// Intelligence control plane. A synchronous Expand must never race the
// CurationAction/ResearchRound/IntelligenceJob chain that owns initial or
// natural-language research for the same Curation.
type CatalogCurationForegroundWorkReaderV2 interface {
	HasActiveCurationWork(context.Context, string, string) (bool, error)
}

// TargetResearchStateReader answers whether a Target's ShoppingSession is
// mid-research. Curation owns the session; Research only asks.
type TargetResearchStateReader interface {
	FindByTarget(context.Context, string, string) (shoppingsessiondomain.ShoppingSession, error)
}

// EnableTargetResearchGuard turns on the ADR-0083 rule that a Target whose
// Round is open accepts no card writes: pin, like, reaction or purchase check
// wait until the Round finalizes or is cancelled.
func (service *LiveCatalogReviewServiceV2) EnableTargetResearchGuard(reader TargetResearchStateReader) {
	service.targetResearch = reader
}

// ResearchInProgressV2 is the reason a card write is refused while the
// Target's Round is open.
const ResearchInProgressV2 = "RESEARCH_IN_PROGRESS"

func (service *LiveCatalogReviewServiceV2) guardTargetWriteV2(ctx context.Context, userID, targetID string) error {
	if service.targetResearch == nil || strings.TrimSpace(targetID) == "" {
		return nil
	}
	session, err := service.targetResearch.FindByTarget(ctx, userID, targetID)
	if err != nil {
		// No session means no research ever ran for the Target; nothing to guard.
		return nil
	}
	if session.Status == shoppingsessiondomain.SessionStatusResearching {
		return fault.New(fault.Conflict, ResearchInProgressV2, true)
	}
	return nil
}

func (service *LiveCatalogReviewServiceV2) EnableCurationForegroundWorkV2(
	reader CatalogCurationForegroundWorkReaderV2,
) error {
	if reader == nil {
		return fault.New(
			fault.InvalidInput, "PHASE8_FOREGROUND_WORK_READER_INVALID", false,
		)
	}
	service.foregroundWork = reader
	return nil
}

// liveCatalogProviderOperationV2 keeps one bounded logical user operation in
// the concurrency gate while charging the rate window only when an external
// Shopify attempt is about to start. Calls inside an operation are sequential;
// a split retry cannot overlap another call from the same operation.
type liveCatalogProviderOperationV2 struct {
	service       *LiveCatalogReviewServiceV2
	calls         int
	lastUsed      int
	lastRemaining int
	callActive    bool
	closed        bool
}

var _ CatalogProviderCallAdmissionV2 = (*liveCatalogProviderOperationV2)(nil)

type liveCatalogGatewayV2 interface {
	CatalogGatewayV2
	CatalogOfferLookupGatewayV2
}

func NewLiveCatalogReviewServiceV2(
	gateway liveCatalogGatewayV2,
	clock sharedapp.Clock,
	config LiveCatalogReviewConfigV2,
) (*LiveCatalogReviewServiceV2, error) {
	if gateway == nil || clock == nil || config.MaximumCallsPerWindow < 1 ||
		config.Window <= 0 || config.MaximumConcurrent < 1 {
		return nil, fault.New(
			fault.InvalidInput, "LIVE_CATALOG_REVIEW_CONFIG_INVALID", false,
		)
	}
	return &LiveCatalogReviewServiceV2{
		gateway: gateway, clock: clock, config: config,
	}, nil
}

func (service *LiveCatalogReviewServiceV2) acquire(
	now time.Time,
	providerCalls int,
) (int, int, func(), error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.refreshWindowLockedV2(now)
	if service.activeCalls >= service.config.MaximumConcurrent {
		failure := fault.New(fault.RateLimited, "LIVE_CATALOG_REVIEW_BUSY", true)
		failure.RetryAfter = time.Second
		return 0, 0, nil, failure
	}
	if providerCalls < 1 || service.windowCalls+providerCalls > service.config.MaximumCallsPerWindow {
		failure := fault.New(fault.RateLimited, "LIVE_CATALOG_REVIEW_RATE_LIMITED", true)
		failure.RetryAfter = service.config.Window - now.Sub(service.windowStarted)
		if failure.RetryAfter <= 0 {
			failure.RetryAfter = time.Second
		}
		return 0, 0, nil, failure
	}
	service.windowCalls += providerCalls
	service.activeCalls++
	used := service.windowCalls
	remaining := service.config.MaximumCallsPerWindow - used
	return used, remaining, func() {
		service.mu.Lock()
		service.activeCalls--
		service.mu.Unlock()
	}, nil
}

func (service *LiveCatalogReviewServiceV2) beginProviderOperationV2(
	now time.Time,
	minimumCalls int,
) (*liveCatalogProviderOperationV2, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.refreshWindowLockedV2(now)
	if service.activeCalls >= service.config.MaximumConcurrent {
		failure := fault.New(fault.RateLimited, "LIVE_CATALOG_REVIEW_BUSY", true)
		failure.RetryAfter = time.Second
		return nil, failure
	}
	if minimumCalls < 1 ||
		service.windowCalls+minimumCalls > service.config.MaximumCallsPerWindow {
		return nil, service.rateLimitFailureLockedV2(now)
	}
	service.activeCalls++
	return &liveCatalogProviderOperationV2{
		service: service, lastUsed: service.windowCalls,
		lastRemaining: service.config.MaximumCallsPerWindow - service.windowCalls,
	}, nil
}

func (operation *liveCatalogProviderOperationV2) AcquireCatalogProviderCall(
	_ context.Context,
) (func(), error) {
	if operation == nil || operation.service == nil {
		return nil, fault.New(
			fault.InternalFailure, "LIVE_CATALOG_OPERATION_INVALID", false,
		)
	}
	service := operation.service
	service.mu.Lock()
	now := service.clock.Now()
	service.refreshWindowLockedV2(now)
	if operation.closed || operation.callActive {
		service.mu.Unlock()
		return nil, fault.New(
			fault.InternalFailure, "LIVE_CATALOG_OPERATION_INVALID", false,
		)
	}
	if service.windowCalls+1 > service.config.MaximumCallsPerWindow {
		failure := service.rateLimitFailureLockedV2(now)
		service.mu.Unlock()
		return nil, failure
	}
	service.windowCalls++
	operation.calls++
	operation.lastUsed = service.windowCalls
	operation.lastRemaining = service.config.MaximumCallsPerWindow - service.windowCalls
	operation.callActive = true
	service.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			service.mu.Lock()
			operation.callActive = false
			service.mu.Unlock()
		})
	}, nil
}

func (operation *liveCatalogProviderOperationV2) Close() {
	if operation == nil || operation.service == nil {
		return
	}
	service := operation.service
	service.mu.Lock()
	defer service.mu.Unlock()
	if operation.closed {
		return
	}
	operation.closed = true
	service.activeCalls--
}

func (operation *liveCatalogProviderOperationV2) Snapshot() (int, int, int) {
	if operation == nil || operation.service == nil {
		return 0, 0, 0
	}
	operation.service.mu.Lock()
	defer operation.service.mu.Unlock()
	return operation.calls, operation.lastUsed, operation.lastRemaining
}

func (service *LiveCatalogReviewServiceV2) providerWindowSnapshotV2(
	now time.Time,
) (int, int) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.refreshWindowLockedV2(now)
	return service.windowCalls, service.config.MaximumCallsPerWindow - service.windowCalls
}

func (service *LiveCatalogReviewServiceV2) refreshWindowLockedV2(now time.Time) {
	if service.windowStarted.IsZero() ||
		now.Sub(service.windowStarted) >= service.config.Window {
		service.windowStarted = now
		service.windowCalls = 0
	}
}

func (service *LiveCatalogReviewServiceV2) rateLimitFailureLockedV2(
	now time.Time,
) *fault.Error {
	failure := fault.New(fault.RateLimited, "LIVE_CATALOG_REVIEW_RATE_LIMITED", true)
	failure.RetryAfter = service.config.Window - now.Sub(service.windowStarted)
	if failure.RetryAfter <= 0 {
		failure.RetryAfter = time.Second
	}
	return failure
}
