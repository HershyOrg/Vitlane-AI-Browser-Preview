package main

import (
	"context"
	"encoding/json"
	"fmt"
	researchamazonstub "github.com/vitlane/vitlane/server/internal/curation/research/infra/amazonstub"
	researchapify "github.com/vitlane/vitlane/server/internal/curation/research/infra/apifyactor"
	dealfeed "github.com/vitlane/vitlane/server/internal/curation/research/infra/dealfeed"
	researchfx "github.com/vitlane/vitlane/server/internal/curation/research/infra/frankfurter"
	researchkorean "github.com/vitlane/vitlane/server/internal/curation/research/infra/koreancatalog"
	researchamazon "github.com/vitlane/vitlane/server/internal/curation/research/infra/openwebninja"
	"github.com/vitlane/vitlane/server/internal/shared/infra/analytics"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	accounthttp "github.com/vitlane/vitlane/server/internal/account/iface/http"
	accountinfra "github.com/vitlane/vitlane/server/internal/account/infra"
	"github.com/vitlane/vitlane/server/internal/account/infra/googleoidc"
	accountmockdojang "github.com/vitlane/vitlane/server/internal/account/infra/mockdojang"
	accountpii "github.com/vitlane/vitlane/server/internal/account/infra/pii"
	accountpostgres "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	browserapp "github.com/vitlane/vitlane/server/internal/browserrun/app"
	browserhttp "github.com/vitlane/vitlane/server/internal/browserrun/iface/http"
	browserpostgres "github.com/vitlane/vitlane/server/internal/browserrun/infra/postgres"
	curation "github.com/vitlane/vitlane/server/internal/curation"
	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	autoapp "github.com/vitlane/vitlane/server/internal/curation/auto/app"
	conversationapp "github.com/vitlane/vitlane/server/internal/curation/conversation/app"
	conversationhttp "github.com/vitlane/vitlane/server/internal/curation/conversation/iface/http"
	curationhttp "github.com/vitlane/vitlane/server/internal/curation/iface/http"
	curationpostgres "github.com/vitlane/vitlane/server/internal/curation/infra/postgres"
	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	intelligencehttp "github.com/vitlane/vitlane/server/internal/curation/intelligence/iface/http"
	intelligencecuration "github.com/vitlane/vitlane/server/internal/curation/intelligence/infra/curation"
	intelligencepostgres "github.com/vitlane/vitlane/server/internal/curation/intelligence/infra/postgres"
	intelligenceresearch "github.com/vitlane/vitlane/server/internal/curation/intelligence/infra/research"
	runnerapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/app"
	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
	runnerhttp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/iface/http"
	runnerintelligence "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/infra/intelligence"
	runnermodelstub "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/infra/modelstub"
	runneropenai "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/infra/openaiapi"
	runnerpostgres "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/infra/postgres"
	planningpostgres "github.com/vitlane/vitlane/server/internal/curation/planning/infra/postgres"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchhttp "github.com/vitlane/vitlane/server/internal/curation/research/iface/http"
	researchcatalogstub "github.com/vitlane/vitlane/server/internal/curation/research/infra/catalogstub"
	researchcuration "github.com/vitlane/vitlane/server/internal/curation/research/infra/curation"
	researchpostgres "github.com/vitlane/vitlane/server/internal/curation/research/infra/postgres"
	researchstorefront "github.com/vitlane/vitlane/server/internal/curation/research/infra/shopifystorefront"
	researchshopify "github.com/vitlane/vitlane/server/internal/curation/research/infra/shopifyucp"
	shoppingsessionapp "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	shoppingsessionhttp "github.com/vitlane/vitlane/server/internal/curation/research/session/iface/http"
	shoppingsessionpostgres "github.com/vitlane/vitlane/server/internal/curation/research/session/infra/postgres"
	agencyorderapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencyorderhttp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/iface/http"
	agencyorderadapters "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/infra/adapters"
	agencyorderpostgres "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/infra/postgres"
	agencyordershopify "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/infra/shopifyucp"
	livecontrolapp "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/app"
	livecontrolhttp "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/iface/http"
	livecontrolpostgres "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/infra/postgres"
	logisticsapp "github.com/vitlane/vitlane/server/internal/ordering/logistics/app"
	logisticshttp "github.com/vitlane/vitlane/server/internal/ordering/logistics/iface/http"
	logisticspostgres "github.com/vitlane/vitlane/server/internal/ordering/logistics/infra/postgres"
	operatorapp "github.com/vitlane/vitlane/server/internal/ordering/operator/app"
	operatorhttp "github.com/vitlane/vitlane/server/internal/ordering/operator/iface/http"
	operatorpostgres "github.com/vitlane/vitlane/server/internal/ordering/operator/infra/postgres"
	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
	settlementhttp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/iface/http"
	settlementaccount "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/infra/account"
	settlementevm "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/infra/evm"
	settlementpostgres "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/infra/postgres"
	paymenthttp "github.com/vitlane/vitlane/server/internal/ordering/payment/iface/http"
	paymentpaypal "github.com/vitlane/vitlane/server/internal/ordering/payment/infra/paypal"
	paymentpostgres "github.com/vitlane/vitlane/server/internal/ordering/payment/infra/postgres"
	processapp "github.com/vitlane/vitlane/server/internal/ordering/process/app"
	processpostgres "github.com/vitlane/vitlane/server/internal/ordering/process/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	processqueue "github.com/vitlane/vitlane/server/internal/ordering/procmsg/queue"
	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	procurementhttp "github.com/vitlane/vitlane/server/internal/ordering/procurement/iface/http"
	procurementadapters "github.com/vitlane/vitlane/server/internal/ordering/procurement/infra/adapters"
	procurementpostgres "github.com/vitlane/vitlane/server/internal/ordering/procurement/infra/postgres"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
	supportapp "github.com/vitlane/vitlane/server/internal/support/app"
	supporthttp "github.com/vitlane/vitlane/server/internal/support/iface/http"
	supportpii "github.com/vitlane/vitlane/server/internal/support/infra/pii"
	supportpostgres "github.com/vitlane/vitlane/server/internal/support/infra/postgres"
)

// application holds every constructed service, handler and worker so that
// routes.go and workers.go can consume them without re-wiring. Construction
// order and behavior are identical to the previous single-file run().
type application struct {
	analytics *analytics.Sender
	config    runtimeConfig
	database  *sharedpostgres.Database
	logger    *slog.Logger
	clock     sharedapp.SystemClock
	ids       sharedapp.UUIDGenerator

	accountRepository   *accountpostgres.Repository
	intelligenceService *intelligenceapp.Service
	intelligenceHandler *intelligencehttp.Handler
	providers           intelligenceapp.ProviderRegistry
	managedRunnerBudget *runnerapp.BudgetService

	accountHandler           *accounthttp.Handler
	authHandler              *accounthttp.AuthHandler
	authMiddleware           *accounthttp.AuthMiddleware
	operatorSessionsHandler  *accounthttp.OperatorSessionsHandler
	browserRunHandler        *browserhttp.Handler
	curationHandler          *curationhttp.Handler
	backgroundService        *researchapp.BackgroundService
	backgroundHandler        *researchhttp.BackgroundHandler
	conversationService      *conversationapp.Service
	conversationHandler      *conversationhttp.Handler
	threadHandler            *curationhttp.ThreadHandler
	threadService            *curationapp.ThreadService
	curationWorkspaceHandler *curationhttp.WorkspaceHandler
	selectionHandler         *curationhttp.SelectionHandler
	catalogCartHandler       *curationhttp.CatalogCartHandlerV2
	researchHandler          *researchhttp.Handler
	liveCatalogReviewHandler *researchhttp.LiveCatalogReviewHandlerV2
	amazonUsageHandler       *researchhttp.AmazonUsageHandler
	catalogAPIHandler        *researchhttp.CatalogAPIHandler
	sessionHandler           *shoppingsessionhttp.Handler
	supportHandler           *supporthttp.Handler
	managedRunnerHandler     *runnerhttp.Handler
	settlementHandler        *settlementhttp.Handler
	agencyOrderHandler       *agencyorderhttp.Handler
	agencyOrderLifecycle     *agencyorderapp.LifecycleService
	procurementService       *procurementapp.Service
	procurementHandler       *procurementhttp.Handler
	logisticsService         *logisticsapp.Service
	logisticsHandler         *logisticshttp.Handler
	operatorWorkHandler      *operatorhttp.Handler
	liveControlService       *livecontrolapp.Service
	liveControlHandler       *livecontrolhttp.Handler
	paymentService           *paymentapp.Service
	paymentHandler           *paymenthttp.Handler
	accountingService        *paymentapp.AccountingService
	accountingHandler        *paymenthttp.AccountingHandler
	giwaIntake               *paymentapp.GIWAIntake

	settlementReconciler    *settlementapp.Reconciler
	settlementCommandWorker *settlementapp.CommandWorker
	agencyOrderWorker       *agencyorderapp.LifecycleWorker
	orderProcessor          *processapp.OrderProcessor
	orderEffects            *processqueue.Worker
	orderQueue              *processqueue.Queue
	orderProcessWatchdog    *processapp.Watchdog
	settlementGateway       *settlementevm.Gateway
	settlementHealth        *settlementRuntimeHealth
	httpMetrics             *httpMetrics
	piiKeyring              *accountpii.Keyring
	opsReporter             *opsReporter
	heartbeats              *healthchecksClient
}

type catalogTokenSourceAdapter struct {
	source *agencyordershopify.TokenSource
}

func (adapter catalogTokenSourceAdapter) AccessToken(ctx context.Context) (string, error) {
	token, err := adapter.source.Token(ctx)
	if err != nil {
		return "", err
	}
	return token.AccessToken, nil
}

func (adapter catalogTokenSourceAdapter) InvalidateAccessToken(accessToken string) {
	adapter.source.Invalidate(accessToken)
}

// Close releases resources whose lifetime previously ended with run()'s
// defer statements.
func (a *application) Close() {
	if a.settlementGateway != nil {
		a.settlementGateway.Close()
	}
}

func newApplication(
	ctx context.Context,
	config runtimeConfig,
	database *sharedpostgres.Database,
	transport http.RoundTripper,
	logger *slog.Logger,
) (*application, error) {
	clock := sharedapp.SystemClock{}
	ids := sharedapp.UUIDGenerator{}
	liveControlService := livecontrolapp.NewService(
		livecontrolpostgres.NewRepository(database), livecontrolapp.StaticPolicy{
			OrderIssue:     config.liveAgencyOrderIssueEnabled,
			PayPalMoney:    config.paypalLiveCaptureEnabled,
			MerchantEffect: config.manualMerchantEffectEnabled,
		}, clock, ids,
	)
	liveControlHandler := livecontrolhttp.NewHandler(liveControlService)
	finalizer := runtimepolicy.NewFinalizer(
		ctx, config.runtimePolicies.externalFinalize,
	)
	accountRepository := accountpostgres.NewRepository(database)
	accountRateLimiter, err := accountpostgres.NewRateLimiter(
		database, config.accountRateLimitSecret,
		config.accountRateLimitKeyVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("configure Account rate limiter: %w", err)
	}
	accountService := accountapp.NewService(accountRepository, clock, ids)
	browserRunService := browserapp.NewService(
		browserpostgres.NewRepository(database), database, clock, ids,
	)
	browserRunHandler := browserhttp.NewHandler(browserRunService)
	var identityProvider accountapp.IdentityProvider
	if config.googleClientID != "" && config.googleClientSecret != "" {
		googleClient, clientErr := sharedhttpclient.NewClient(
			transport, config.runtimePolicies.googleOIDCTimeout,
		)
		if clientErr != nil {
			return nil, fmt.Errorf("configure Google OIDC HTTP client: %w", clientErr)
		}
		provider, err := googleoidc.New(ctx, googleoidc.Config{
			ClientID: config.googleClientID, ClientSecret: config.googleClientSecret,
			RedirectURL: config.publicBaseURL + "/api/v1/auth/google/callback",
			HTTPClient:  googleClient,
		})
		if err != nil {
			return nil, err
		}
		identityProvider = provider
	}
	authenticationService := accountapp.NewAuthenticationService(
		accountRepository, identityProvider, accountinfra.CryptoSecretGenerator{},
		clock, ids, logger,
	)
	authenticationService.EnableRateLimiter(accountRateLimiter)
	planningRepository := planningpostgres.NewRepository(database)
	curationRepository := curationpostgres.NewRepository(database, planningRepository)
	sessionService := shoppingsessionapp.NewService(
		shoppingsessionpostgres.NewRepository(database), clock, ids,
	)
	curationService := curationapp.NewService(
		curationRepository, sessionService, database, clock, ids, logger,
	)
	curationService.EnableResearchSelectionRecorder(&accountapp.PreferencesService{Repository: accountRepository})
	selectionService := curationapp.NewSelectionService(
		curationRepository, clock, ids,
	)
	selectionService.EnableTransactor(database)
	selectionService.EnableSelectionMutationActions(curationService)
	curationService.EnableTargetSelectionRemover(selectionService)
	catalogCartService, err := curationapp.NewCatalogCartServiceV2(
		curationRepository, clock,
	)
	if err != nil {
		return nil, fmt.Errorf("configure Phase 8 CartView: %w", err)
	}
	researchRepository := researchpostgres.NewRepository(database)
	catalogLimits, err := researchapp.ParseCatalogLocalLimits(config.catalogAPILimitsJSON)
	if err != nil {
		return nil, err
	}
	researchRepository.ConfigureCatalogResources(catalogLimits, int64(config.apifyMonthlyCapUSD)*1000000)
	researchService := researchapp.NewService(
		researchRepository, curationService, sessionService,
		database, clock, ids, logger,
	)
	var shopifyAgentTokens *agencyordershopify.TokenSource
	if config.shopifyUCPEnabled ||
		(config.agencyOrderEnabled && config.agencyOrderCheckoutProvider == "shopify") {
		shopifyTokenClient, clientErr := sharedhttpclient.NewClient(
			transport, config.runtimePolicies.shopifyTimeout,
		)
		if clientErr != nil {
			return nil, fmt.Errorf("configure Shopify token HTTP client: %w", clientErr)
		}
		shopifyAgentTokens, err = agencyordershopify.NewTokenSource(
			shopifyTokenClient, "https://api.shopify.com/auth/access_token",
			config.shopifyDevClientID, config.shopifyDevClientSecret,
		)
		if err != nil {
			return nil, fmt.Errorf("configure Shopify agent token source: %w", err)
		}
	}
	koreanClient, err := sharedhttpclient.NewClient(transport, 35*time.Second)
	if err != nil {
		return nil, err
	}
	// Paid Actor runs are optional: without a token the registry's Actor malls
	// keep their Google Shopping offers and nothing is bought.
	var actorGateway researchkorean.ActorSearcher
	if config.apifyToken != "" {
		actorClient, actorErr := sharedhttpclient.NewClient(transport, 30*time.Second)
		if actorErr != nil {
			return nil, actorErr
		}
		gateway, actorErr := researchapify.New(researchapify.Config{
			Token: config.apifyToken, MonthlyCapMicros: int64(config.apifyMonthlyCapUSD) * 1_000_000,
			Client: actorClient, Control: researchRepository, Ledger: researchRepository,
		})
		if actorErr != nil {
			return nil, actorErr
		}
		actorGateway = gateway
	}
	browserBridgeURL, browserBridgeToken := "", ""
	if config.browserProductSearchEnabled {
		browserBridgeURL, browserBridgeToken = config.browserBridgeURL, config.browserBridgeToken
	}
	koreanGateway, err := researchkorean.New(researchkorean.Config{Enabled: config.koreanCatalogEnabled, OWNKey: config.ownAPIKey, NaverClientID: config.naverHubID, NaverClientSecret: config.naverHubSecret, SerpKey: config.serpAPIKey, BrowserBaseURL: browserBridgeURL, BrowserToken: browserBridgeToken, Client: koreanClient, Control: researchRepository, DetailLimit: config.koreanDetailLimit, MaxConcurrent: config.koreanMaxConcurrent, SearchTimeout: time.Duration(config.koreanSearchTimeoutSeconds) * time.Second, Actor: actorGateway})
	if err != nil {
		return nil, err
	}
	if config.catalogProvider == "stub" && config.koreanCatalogEnabled {
		koreanGateway, err = researchkorean.NewReviewStub(config.appEnvironment, researchRepository)
		if err != nil {
			return nil, err
		}
	}
	exchangeRate := &researchapp.ExchangeRateService{Repository: researchRepository, Gateway: researchfx.Gateway{Client: koreanClient}}
	var amazonGateway researchapp.AmazonCatalogGateway
	if config.amazonEnabled && config.amazonMode == "stub" {
		gateway, e := researchamazonstub.New(config.amazonStubFile)
		if e != nil {
			return nil, e
		}
		amazonGateway = gateway
	} else if config.amazonEnabled {
		client, e := sharedhttpclient.NewClient(transport, 10*time.Second)
		if e != nil {
			return nil, e
		}
		gateway, e := researchamazon.New(researchamazon.Config{APIKey: config.amazonAPIKey, Client: client, Usage: researchRepository})
		if e != nil {
			return nil, e
		}
		amazonGateway = gateway
	}
	if amazonGateway != nil {
		amazonGateway = &researchapp.ControlledAmazonGateway{Gateway: amazonGateway, Control: researchRepository}
	}
	var catalogWorkspaceResearch *researchapp.LiveCatalogReviewServiceV2
	var catalogShopifyGateway *researchshopify.GatewayV2
	switch {
	case config.catalogProvider == "stub":
		// Local review and E2E use the same Phase 8 filter-proof and CandidatePool
		// boundary as Shopify without depending on the external provider.
		stubCatalog, stubErr := researchcatalogstub.New(config.appEnvironment)
		if stubErr != nil {
			return nil, fmt.Errorf("configure stub catalog: %w", stubErr)
		}
		// The stub answers instead of Shopify, and its calls are counted in the
		// operator's ledger exactly like Shopify's, so local review shows the same
		// numbers the operator sees in production.
		catalogWorkspaceResearch, err = researchapp.NewLiveCatalogReviewServiceV2(
			researchapp.NewAccountedCatalogGatewayV2(stubCatalog, researchRepository, clock), clock, researchapp.LiveCatalogReviewConfigV2{
				MaximumCallsPerWindow: config.curationCatalogResearchRate,
				Window:                time.Minute, MaximumConcurrent: config.curationCatalogConcurrency,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("configure Phase 8 fixture research: %w", err)
		}
	case config.shopifyUCPEnabled:
		catalogClient, clientErr := sharedhttpclient.NewClient(
			transport, config.runtimePolicies.shopifyTimeout,
		)
		if clientErr != nil {
			return nil, fmt.Errorf("configure Shopify HTTP client: %w", clientErr)
		}
		catalogShopifyGateway, err = researchshopify.NewGatewayV2(
			researchshopify.GatewayV2Config{
				Endpoint:   config.shopifyUCPCatalogURL,
				ProfileURL: config.shopifyUCPAgentProfileURL,
				HTTPClient: catalogClient, ExpectedVersion: "2026-04-08",
				LookupBatchSize: researchapp.CatalogLookupDefaultBatchSize,
				Tokens:          catalogTokenSourceAdapter{source: shopifyAgentTokens},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("configure Phase 8 Shopify gateway: %w", err)
		}
		// Every Shopify catalog call — a research search, a page's price lookup, an
		// order preparation lookup — is recorded in the operator's API ledger.
		catalogWorkspaceResearch, err = researchapp.NewLiveCatalogReviewServiceV2(
			researchapp.NewAccountedCatalogGatewayV2(catalogShopifyGateway, researchRepository, clock), clock, researchapp.LiveCatalogReviewConfigV2{
				MaximumCallsPerWindow: config.curationCatalogResearchRate,
				Window:                time.Minute, MaximumConcurrent: config.curationCatalogConcurrency,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("configure Phase 8 automated research: %w", err)
		}
	}
	if config.amazonEnabled && (catalogWorkspaceResearch == nil || !config.curationCatalogResearchEnabled) {
		return nil, fmt.Errorf("Amazon requires Catalog Research")
	}
	if catalogWorkspaceResearch != nil {
		catalogWorkspaceResearch.EnableAmazon(amazonGateway)
		catalogWorkspaceResearch.EnableKoreanCatalog(koreanGateway, exchangeRate)
		catalogWorkspaceResearch.EnableTargetResearchGuard(sessionService)
		if enableErr := catalogWorkspaceResearch.EnableWorkspaceRepositoryV2(researchRepository); enableErr != nil {
			return nil, fmt.Errorf("enable Phase 8 research workspace: %w", enableErr)
		}
		if enableErr := catalogWorkspaceResearch.EnableCartDraftReaderV2(
			researchcuration.NewCatalogCartAdapterV2(catalogCartService),
		); enableErr != nil {
			return nil, fmt.Errorf("enable Phase 8 CartView preparation: %w", enableErr)
		}
		if enableErr := researchService.EnableLiveCatalogReviewV2(catalogWorkspaceResearch); enableErr != nil {
			return nil, fmt.Errorf("enable automated Phase 8 research: %w", enableErr)
		}
	}
	var intelligenceService *intelligenceapp.Service
	var providers intelligenceapp.ProviderRegistry
	if !config.managedRunnerLimits.Valid() {
		return nil, fmt.Errorf("managed runner daily limits are invalid")
	}
	managedRunnerBudget := runnerapp.NewBudgetService(
		runnerpostgres.NewLedger(database), config.managedRunnerLimits,
		database, clock, ids,
	)
	var managedRunnerRegistry runnerdomain.Registry
	var autoModelProvider intelligenceapp.Provider
	if config.managedRunnerEnabled {
		registry, registryErr := runnerdomain.NewRegistry(
			runnerdomain.DefaultModels(), runnerdomain.DefaultModelKey,
		)
		if registryErr != nil {
			return nil, fmt.Errorf("configure managed runner registry: %w", registryErr)
		}
		managedRunnerRegistry = registry
		var modelPort runnerapp.ModelPort
		switch config.managedRunnerProvider {
		case "stub":
			// modelstub refuses to build in production, so a misconfigured
			// deploy fails at startup instead of serving fixture candidates.
			stub, stubErr := runnermodelstub.New(config.appEnvironment)
			if stubErr != nil {
				return nil, fmt.Errorf("configure managed runner stub: %w", stubErr)
			}
			modelPort = stub
		default:
			if config.managedRunnerAPIKey == "" {
				return nil, fmt.Errorf(
					"MANAGED_RUNNER_ENABLED requires MANAGED_OPENAI_API_SECRET",
				)
			}
			modelClient, clientErr := managedModelHTTPClient(transport, config.runtimePolicies.managedModelTimeout)
			if clientErr != nil {
				return nil, fmt.Errorf("configure managed model HTTP client: %w", clientErr)
			}
			modelPort = runneropenai.NewClient(
				config.managedRunnerBaseURL, config.managedRunnerAPIKey, modelClient,
			)
		}
		// Research owns whether a catalog is configured at all; the pipeline
		// just needs to know one exists. Without it it submits NO_RESULTS
		// rather than inventing products.
		catalogEnabled := catalogWorkspaceResearch != nil
		managedProvider, providerErr := runnerintelligence.NewProvider(
			managedRunnerBudget, modelPort, managedRunnerRegistry, finalizer,
		)
		if providerErr != nil {
			return nil, fmt.Errorf("configure managed provider: %w", providerErr)
		}
		autoModelProvider = managedProvider
		providers, providerErr = intelligenceapp.NewProviderRegistry(
			intelligenceapp.RegisteredProvider{
				Provider:    managedProvider,
				Concurrency: config.managedRunnerConcurrency,
			},
		)
		if providerErr != nil {
			return nil, fmt.Errorf("configure intelligence providers: %w", providerErr)
		}
		researchAdapter := intelligenceresearch.NewAdapter(researchService, ids)
		intelligenceRepository := intelligencepostgres.NewRepository(database, ids)
		intelligenceRepository.EnableThreads()
		intelligenceService, err = intelligenceapp.NewService(
			intelligenceRepository,
			intelligencecuration.NewProductAdapter(
				curationService, researchAdapter,
			),
			providers, database,
			clock, ids, logger, finalizer,
		)
		if err != nil {
			return nil, fmt.Errorf("configure intelligence service: %w", err)
		}
		if err := intelligenceService.ConfigureAdmission(intelligenceapp.ClaimPolicy{
			OtherLane:           config.intelligenceOtherLane,
			ResearchPerUser:     config.intelligenceResearchPerUser,
			ResearchPerCuration: config.intelligenceResearchPerCuration,
			ResearchGlobal:      config.intelligenceResearchGlobal,
		}, time.Duration(config.managedModelSlotWaitSeconds)*time.Second); err != nil {
			return nil, fmt.Errorf("configure intelligence admission: %w", err)
		}
		if err := intelligenceService.ConfigureEvaluation(intelligenceapp.EvaluationPolicy{
			BatchLimit:    config.researchEvaluationBatchLimit,
			Parallel:      config.researchEvaluationParallel,
			ExtraSlotWait: time.Duration(config.researchEvaluationExtraSlotWaitSecond) * time.Second,
			BatchTimeout:  time.Duration(config.researchEvaluationBatchTimeoutSeconds) * time.Second,
		}); err != nil {
			return nil, fmt.Errorf("configure research evaluation: %w", err)
		}
		intelligenceService.EnableThreadContinuations()
		curationIntelligence := intelligencecuration.NewJobCreator(intelligenceService)
		curationService.EnableIntelligenceWork(curationIntelligence)
		curationService.EnableCurationForegroundWork(curationIntelligence)
		if catalogWorkspaceResearch != nil {
			if enableErr := catalogWorkspaceResearch.EnableCurationForegroundWorkV2(
				curationIntelligence,
			); enableErr != nil {
				return nil, fmt.Errorf(
					"enable Phase 8 foreground-work admission: %w", enableErr,
				)
			}
		}
		researchService.EnableIntelligenceWork(
			intelligenceresearch.NewJobCreator(intelligenceService),
		)
		// Whether a catalog is wired decides between real Candidates and an
		// honest NO_RESULTS, so it belongs in the boot record rather than
		// being inferred from an empty result later.
		logger.Info("intelligence configured",
			"event", "intelligence.configured",
			"model_provider", config.managedRunnerProvider,
			"catalog_provider", config.catalogProvider,
			"catalog_enabled", catalogEnabled,
			"shopify_ucp_enabled", config.shopifyUCPEnabled)
	}
	accountHandler := accounthttp.NewHandler(accountService)
	accountHandler.EnablePreferences(&accountapp.PreferencesService{Repository: accountRepository})
	accountHandler.EnableTrustedProxies(config.trustedProxyPrefixes)
	var settlementHandler *settlementhttp.Handler
	var settlementService *settlementapp.Service
	var settlementReconciler *settlementapp.Reconciler
	var settlementCommandWorker *settlementapp.CommandWorker
	var settlementGateway *settlementevm.Gateway
	var piiKeyring *accountpii.Keyring
	var shippingService *accountapp.ShippingService
	var settlementHealth *settlementRuntimeHealth
	if config.settlementEnabled {
		// ADR-0040 §8: rotation keeps every version that may still decrypt
		// stored ciphertext; PII_KEY_VERSION names the active write key.
		var cipherErr error
		if config.piiEncryptionKeys != "" {
			piiKeyring, cipherErr = accountpii.NewKeyring(
				config.piiEncryptionKeys, config.piiKeyVersion,
			)
		} else {
			piiKeyring, cipherErr = accountpii.NewKeyringFromSingle(
				config.piiEncryptionKey, config.piiKeyVersion,
			)
		}
		if cipherErr != nil {
			return nil, cipherErr
		}
		piiCipher := piiKeyring
		shippingService = accountapp.NewShippingService(
			accountRepository, piiCipher, clock, ids,
		)
		accountHandler.EnableShipping(shippingService)

		kycProvider := accountmockdojang.Provider{}
		walletVerification := accountapp.NewWalletVerificationService(
			accountRepository, accountinfra.CryptoSecretGenerator{}, clock, ids,
			config.publicBaseURL, config.settlementConfig.ChainCAIP2,
		)
		walletVerification.EnableRateLimiter(accountRateLimiter)
		accountHandler.EnableWalletVerification(walletVerification)
		kycService := accountapp.NewKYCService(
			accountRepository, kycProvider,
			accountdomain.KYCProviderMockDojang,
			accountdomain.KYCEffectSimulated, clock, ids,
		)
		kycService.EnableFinalizer(finalizer)
		accountHandler.EnableKYC(kycService)

		quoteSigner, err := settlementevm.NewPrivateKeySignerFromFile(config.quoteSignerKeyFile)
		if err != nil {
			return nil, err
		}
		finalizerSigner, err := settlementevm.NewPrivateKeySignerFromFile(config.finalizerKeyFile)
		if err != nil {
			return nil, err
		}
		refunderSigner, err := settlementevm.NewPrivateKeySignerFromFile(config.refunderKeyFile)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(refunderSigner.Address(), config.refunderAddress) {
			return nil, fmt.Errorf(
				"REFUNDER_KEY_FILE address %s does not match REFUNDER_ADDRESS",
				refunderSigner.Address(),
			)
		}
		settlementRepository := settlementpostgres.NewRepository(database)
		settlementService = settlementapp.NewService(
			settlementRepository, quoteSigner, clock, ids, config.settlementConfig,
		)
		settlementService.EnableAgencyOrders(
			settlementRepository,
			settlementaccount.NewAgencyOrderIdentityAdapter(walletVerification),
		)
		if err := settlementRepository.EnsureMerchants(ctx, settlementMerchants(config), clock.Now()); err != nil {
			return nil, err
		}

		settlementHandler = settlementhttp.NewHandler(settlementService)

		evmHTTPClient, clientErr := sharedhttpclient.NewClient(
			transport, config.runtimePolicies.evmHTTPClientTimeout,
		)
		if clientErr != nil {
			return nil, fmt.Errorf("configure EVM HTTP client: %w", clientErr)
		}
		settlementGateway, err = settlementevm.DialGateway(
			ctx, config.privateRPCURL, config.settlementConfig.SettlementAddress,
			config.settlementConfig.ChainID, finalizerSigner, refunderSigner,
			evmHTTPClient,
		)
		if err != nil {
			return nil, err
		}
		if err := settlementGateway.ValidateConfig(
			ctx, config.settlementConfig.TokenAddress, config.settlementConfig.FaucetAddress,
			config.settlementConfig.FeeRecipient, quoteSigner.Address(), finalizerSigner.Address(),
			refunderSigner.Address(), config.pauserAddress, config.settlementConfig.ClaimAmount,
			config.settlementConfig.FeeBps,
			settlementMerchantExpectations(config),
		); err != nil {
			return nil, err
		}
		if err := validateOnchainManifest(ctx, config, settlementGateway); err != nil {
			return nil, err
		}
		settlementReconciler = settlementapp.NewReconciler(
			settlementRepository, settlementGateway, clock,
			config.settlementConfig.ChainID, config.settlementConfig.SettlementAddress,
			config.chainStartBlock, logger, config.settlementReconcilePolicy,
		)
		settlementCommandWorker = settlementapp.NewCommandWorker(
			settlementRepository, settlementGateway, clock, logger,
			config.settlementReconcilePolicy,
		)
		settlementHealth = newSettlementRuntimeHealth()
	}
	var agencyOrderHandler *agencyorderhttp.Handler
	var agencyOrderLifecycle *agencyorderapp.LifecycleService
	var agencyOrderWorker *agencyorderapp.LifecycleWorker
	var orderProcessor *processapp.OrderProcessor
	var orderEffects *processqueue.Worker
	var orderQueue *processqueue.Queue
	var orderProcessWatchdog *processapp.Watchdog
	var giwaIntake *paymentapp.GIWAIntake
	var procurementService *procurementapp.Service
	var procurementHandler *procurementhttp.Handler
	var logisticsService *logisticsapp.Service
	var logisticsHandler *logisticshttp.Handler
	var operatorWorkHandler *operatorhttp.Handler
	var operatorWorkService *operatorapp.Service
	var operatorReadRepository *operatorpostgres.Repository
	var accountingService *paymentapp.AccountingService
	var accountingHandler *paymenthttp.AccountingHandler
	var agencyOrderRepository *agencyorderpostgres.Repository
	if config.agencyOrderEnabled {
		if catalogWorkspaceResearch == nil || shippingService == nil || piiKeyring == nil {
			return nil, fmt.Errorf("AgencyOrder requires Catalog Research and encrypted shipping snapshots")
		}
		profileHash, hashErr := agencyorderhttp.AgentProfileHash()
		if hashErr != nil {
			return nil, fmt.Errorf("hash AgencyOrder agent profile: %w", hashErr)
		}
		var checkout agencyorderapp.MerchantCheckoutPreflightPort
		switch config.agencyOrderCheckoutProvider {
		case "stub":
			checkout, err = agencyorderadapters.NewCheckoutStub(
				config.appEnvironment, config.agencyOrderAgentProfileURL, profileHash,
			)
		case "shopify":
			checkoutClient, clientErr := sharedhttpclient.NewClient(
				transport, config.runtimePolicies.shopifyTimeout,
			)
			if clientErr != nil {
				return nil, fmt.Errorf("configure Shopify checkout client: %w", clientErr)
			}
			checkout, err = agencyordershopify.NewGateway(agencyordershopify.GatewayConfig{
				HTTPClient: checkoutClient, Tokens: shopifyAgentTokens,
				Vault:               agencyorderpostgres.NewCapabilityVault(database, piiKeyring, clock, ids),
				AgentProfileURL:     config.agencyOrderAgentProfileURL,
				AgentProfileVersion: "2026-04-08", AgentProfileHash: profileHash,
				StorefrontAPIVersion: "2026-07",
				BuyerContactEmail:    config.agencyOrderBuyerContactEmail,
			})
		}
		if err != nil {
			return nil, fmt.Errorf("configure AgencyOrder checkout: %w", err)
		}
		agencyOrderRepository = agencyorderpostgres.NewRepository(database)
		instructionGate := paymentpostgres.NewInstructionGate(database, func() {
			if orderQueue != nil {
				orderQueue.Wake()
			}
		})
		if settlementService != nil {
			settlementService.EnableInstructionGate(instructionGate, database)
		}
		agencyService, serviceErr := agencyorderapp.NewService(
			agencyOrderRepository,
			agencyorderadapters.NewCartReader(catalogCartService),
			agencyorderadapters.NewExactLineResolver(
				curation.NewCatalogOrderPreparer(catalogWorkspaceResearch),
			),
			agencyorderadapters.NewShipping(shippingService), checkout, database, clock, ids,
		)
		if serviceErr != nil {
			return nil, serviceErr
		}
		agencyOrderHandler = agencyorderhttp.NewHandler(agencyService, config.trustedProxyPrefixes)
		agencyService.EnableLiveIssueGate(liveControlService)
		agencyOrderLifecycle = agencyorderapp.NewLifecycleService(
			agencyOrderRepository,
			agencyorderadapters.NewOperatorShipping(shippingService),
			clock, ids,
		)
		agencyOrderHandler.EnableLifecycle(agencyOrderLifecycle)
		agencyOrderWorker = agencyorderapp.NewLifecycleWorker(logger)
		// GIWA 수납 합류는 payment가 자기 테이블에 쓴다(ADR-0055 §4). PayPal
		// 구성과 무관하게 GIWA rail이 있는 한 항상 돈다.
		giwaIntake = paymentapp.NewGIWAIntake(paymentpostgres.NewRepository(database), clock)
		accountingService = paymentapp.NewAccountingService(
			paymentpostgres.NewRepository(database), clock,
		)
		accountingHandler = paymenthttp.NewAccountingHandler(accountingService)
		agencyOrderWorker.EnableOrderSheetCleanup(
			agencyorderapp.NewOrderSheetCleanupService(
				agencyOrderRepository, checkout, clock,
			),
		)
		// Procurement Context(Step 4B): accepted receipt 소비와 운영자 Task 창구.
		procurementService = procurementapp.NewService(
			procurementpostgres.NewRepository(database),
			procurementadapters.NewOperatorShipping(shippingService),
			agencyorderpostgres.NewCapabilityVault(database, piiKeyring, clock, ids),
			clock,
			// LIVE_MERCHANT_EFFECT는 Step 6 activation 사건에서만 열린다.
			// FEE_RETAINED disclosure 판정은 ordering/policy가 소유한다.
			config.manualMerchantEffectEnabled,
		)
		procurementService.EnableLiveMerchantGate(liveControlService)
		procurementHandler = procurementhttp.NewHandler(procurementService)
		// Logistics Context(Step 5A): 기대 unit 등록과 운영자 shipment 창구.
		logisticsService = logisticsapp.NewService(
			logisticspostgres.NewRepository(database), clock,
		)
		logisticsHandler = logisticshttp.NewHandler(logisticsService)
		// 운영자 work surface — 큐들의 read-only 합성(ADR-0055 §5·ADR-0056 §3:
		// 소진 커맨드 개입 큐 포함).
		operatorWorkService = operatorapp.NewService(
			procurementService, agencyOrderLifecycle, logisticsService, clock,
		)
		operatorWorkService.EnableAccounting(accountingService)
		operatorReadRepository = operatorpostgres.NewRepository(database)
		operatorWorkService.EnableOrderLookup(
			operatorReadRepository, agencyOrderLifecycle,
		)
		operatorWorkService.EnableLivePayPalOrderCount(operatorReadRepository)
		operatorWorkHandler = operatorhttp.NewHandler(operatorWorkService)
		processStore := processpostgres.NewStore(database)
		orderProcessor = processapp.NewOrderProcessor(processStore, clock)
		orderQueue = processqueue.New(database, nil)
		orderEffects = processqueue.NewWorker(orderQueue)
		procurementService.EnableProcessInputs(processqueue.NewActionInputs(database, procmsg.TargetProcurement))
		agencyOrderLifecycle.EnableProcessInputs(processqueue.NewActionInputs(database, procmsg.TargetAgencyOrder))
		logisticsService.EnableProcessInputs(processqueue.NewActionInputs(database, procmsg.TargetLogistics))
		logisticsHandler.EnableProcessor(orderProcessor)
		procurementHandler.EnableProcessor(orderProcessor)
		agencyOrderHandler.EnableProcessor(orderProcessor)
		operatorWorkService.EnableProcessIntervention(orderProcessor)
		operatorWorkHandler.EnableProcessIntervention(orderProcessor)
		orderProcessWatchdog = processapp.NewWatchdog(processStore, clock, logger)
		orderEffects.Register(string(procmsg.TargetAgencyOrder), agencyorderapp.NewEffectConsumer(agencyOrderLifecycle, orderQueue))
		orderEffects.Register(string(procmsg.TargetProcurement), procurementapp.NewEffectConsumer(procurementService, orderQueue))
		orderEffects.Register(string(procmsg.TargetLogistics), logisticsapp.NewEffectConsumer(logisticsService, orderQueue))
	}
	var paymentService *paymentapp.Service
	var paymentHandler *paymenthttp.Handler
	livePayPalConfigured := config.paypalLiveClientID != "" &&
		config.paypalLiveClientSecret != "" && config.paypalLiveWebhookID != "" &&
		config.paypalLiveMerchantID != ""
	if agencyOrderHandler != nil {
		paymentRepository := paymentpostgres.NewRepository(database)
		type providerSpec struct {
			environment, baseURL, clientID, clientSecret, webhookID string
			issueEnabled, captureEnabled                            bool
		}
		providerSpecs := make([]providerSpec, 0, 2)
		registrations := make([]paymentapp.ProviderRegistration, 0, len(providerSpecs))
		paypalConfigured := config.paypalSandboxEnabled || livePayPalConfigured
		if paypalConfigured {
			paypalHTTPClient, httpErr := sharedhttpclient.NewClient(
				transport, config.runtimePolicies.paypalTimeout,
			)
			if httpErr != nil {
				return nil, fmt.Errorf("configure PayPal HTTP client: %w", httpErr)
			}
			if config.paypalSandboxEnabled {
				providerSpecs = append(providerSpecs, providerSpec{
					environment: "SANDBOX", baseURL: paymentpaypal.SandboxBaseURL,
					clientID: config.paypalSandboxClientID, clientSecret: config.paypalSandboxClientSecret,
					webhookID: config.paypalSandboxWebhookID,
					// Sandbox remains available for orders already issued across a
					// deployment switch. New-order selection is gated on the handler.
					issueEnabled: true, captureEnabled: true,
				})
			}
			if livePayPalConfigured {
				providerSpecs = append(providerSpecs, providerSpec{
					environment: "LIVE", baseURL: paymentpaypal.LiveBaseURL,
					clientID: config.paypalLiveClientID, clientSecret: config.paypalLiveClientSecret,
					webhookID:      config.paypalLiveWebhookID,
					issueEnabled:   config.liveAgencyOrderIssueEnabled,
					captureEnabled: config.paypalLiveCaptureEnabled,
				})
			}
			registrations = make([]paymentapp.ProviderRegistration, 0, len(providerSpecs))
			for _, spec := range providerSpecs {
				paypalClient, clientErr := paymentpaypal.NewClient(paymentpaypal.Config{
					BaseURL: spec.baseURL, ClientID: spec.clientID,
					ClientSecret: spec.clientSecret, HTTPClient: paypalHTTPClient,
				})
				if clientErr != nil {
					return nil, fmt.Errorf("configure PayPal %s client: %w", spec.environment, clientErr)
				}
				// 환경별 사전 검증 merchant binding 없이는 해당 adapter를 boot하지
				// 않는다. credential 원문은 저장하거나 로그에 쓰지 않는다.
				binding, found, bindingErr := paymentRepository.GetBinding(ctx, spec.environment)
				if bindingErr != nil {
					return nil, fmt.Errorf("read PayPal %s account binding: %w", spec.environment, bindingErr)
				}
				if !found {
					return nil, fmt.Errorf(
						"PayPal %s requires a registered account binding — run `vitlane paypal-binding register --environment %s` first",
						spec.environment, spec.environment,
					)
				}
				if binding.WebhookID != spec.webhookID {
					return nil, fmt.Errorf("PayPal %s webhook ID does not match the registered binding", spec.environment)
				}
				if binding.ClientIDFingerprint != paypalClientFingerprint(spec.clientID) {
					return nil, fmt.Errorf("PayPal %s client fingerprint does not match the registered binding", spec.environment)
				}
				if spec.environment == "LIVE" && binding.MerchantID != config.paypalLiveMerchantID {
					return nil, fmt.Errorf("PayPal LIVE merchant ID does not match the registered binding")
				}
				registrations = append(registrations, paymentapp.ProviderRegistration{
					Environment: spec.environment, Client: paypalClient, WebhookID: spec.webhookID,
					IssueEnabled: spec.issueEnabled, CaptureEnabled: spec.captureEnabled,
				})
			}
		}
		providerRegistry, registryErr := paymentapp.NewProviderRegistry(registrations...)
		if registryErr != nil {
			return nil, fmt.Errorf("configure PayPal provider registry: %w", registryErr)
		}
		paymentService = paymentapp.NewServiceWithProviderRegistry(
			paymentRepository, providerRegistry,
			paymentpostgres.NewInstructionGate(database, func() {
				if orderQueue != nil {
					orderQueue.Wake()
				}
			}), database,
			paymentapp.Config{
				PublicBaseURL: config.publicBaseURL,
			}, clock, ids)
		paymentService.EnableLiveMoneyGate(liveControlService)
		if orderEffects != nil {
			orderEffects.Register(string(procmsg.TargetPayment), paymentapp.NewEffectConsumer(paymentService, orderQueue))
		}

		if operatorWorkService != nil {
			operatorWorkService.EnablePaymentReconciliation(paymentService)
		}
		// GIWA의 local ACTIVE 전이와 compensation은 PayPal credential이 전혀
		// 없어도 위 rail-neutral Payment core를 사용한다. PayPal HTTP/routes와
		// provider resource adoption만 실제 provider registry가 있을 때 연다.
		if paypalConfigured {
			paymentHandler = paymenthttp.NewHandler(paymentService)
			if operatorWorkService != nil {
				operatorWorkService.EnablePayPalResourceAdoption(operatorReadRepository)
			}
			if config.paypalSandboxEnabled {
				agencyOrderHandler.EnablePayPalSandbox()
			}
			if config.liveAgencyOrderIssueEnabled {
				agencyOrderHandler.EnablePayPalLive()
			}
		}
	}
	// Support 대화(ADR-0059) — 제품 무관하게 항상 배선된다. 주문 첨부 검증
	// 게이트웨이는 agencyOrder 런타임이 있을 때만 연다(typed-nil 함정 회피:
	// nil일 수 있는 구체 포인터를 인터페이스 변수에 직접 담지 않는다).
	supportService := supportapp.NewService(
		supportpostgres.NewRepository(database), accountService, clock, ids, logger,
	)
	if agencyOrderLifecycle != nil {
		supportService.EnableOrderingGateway(agencyOrderLifecycle)
	}
	if piiKeyring != nil {
		supportService.EnableImageCipher(supportpii.NewAdapter(piiKeyring))
	}
	// 고객 통신 카드는 process 커맨드 레일의 SUPPORT executor가 만든다(ADR-0070
	// §4.5) — owner 서비스는 대화 어댑터를 더 이상 알지 않는다.
	if orderEffects != nil && agencyOrderLifecycle != nil {
		handler := newSupportCardHandler(supportService, agencyOrderLifecycle, procurementService, logisticsService, paymentService)
		handler.inbox, handler.clock = orderQueue, clock
		orderEffects.Register(string(procmsg.TargetSupport), handler)
	}
	supportHandler := supporthttp.NewHandler(supportService)
	marketingAdminEmails := accounthttp.NewEmailAllowlist(config.marketingAdminEmails)
	settlementOperatorEmails := accounthttp.NewEmailAllowlist(config.settlementOperatorEmails)
	// ADR-0040 §10: allowlisted operators get the shorter session TTL and the
	// ops screen gains an audited force-revoke command.
	authenticationService.EnableOperatorSessionPolicy(func(email string) bool {
		return marketingAdminEmails.Allows(email) || settlementOperatorEmails.Allows(email)
	})
	operatorSessionsHandler := accounthttp.NewOperatorSessionsHandler(
		accountapp.NewOperatorSessionService(
			accountRepository, database, clock, ids,
		),
		marketingAdminEmails, settlementOperatorEmails,
	)
	authHandler := accounthttp.NewAuthHandler(
		authenticationService, accounthttp.CookiePolicy{Secure: config.production},
		marketingAdminEmails, settlementOperatorEmails,
		accounthttp.AuthFeatures{
			GoogleEnabled:             identityProvider != nil,
			MobileRedirectURIs:        config.mobileAuthRedirectURIs,
			DevelopmentSessionEnabled: config.allowDevAuth,
			DevelopmentUserID:         config.devAuthDefaultUserID,
			MerchantEffectLive:        config.manualMerchantEffectEnabled,
			DevelopmentProfiles: []accounthttp.DevelopmentProfile{
				{
					Key: "empty-user", Label: "빈 일반 사용자",
					Description: "지갑·배송지·KYC·구매 내역 없이 시작합니다.",
					UserID:      "e5000000-0000-4000-8000-000000000101",
					StartPath:   "/",
					Resettable:  true,
				},
				{
					Key: "empty-operator", Label: "빈 운영자 사용자",
					Description: "개인 데이터는 비어 있고 운영자 메뉴에 접근할 수 있습니다.",
					UserID:      "e5000000-0000-4000-8000-000000000102",
					Operator:    true,
					// 운영자 홈은 주문 처리다(운영정합 3차 #1 — operatorHomePath와 동일).
					StartPath:  "/admin/agencyOrder",
					Resettable: true,
				},
				{
					Key: "multi-product", Label: "다중 상품 구매 시나리오",
					Description: "완료된 Target 3개에서 Shopify CandidatePool·CartView·구매 준비 흐름을 검수합니다.",
					UserID:      "e5000000-0000-4000-8000-000000000103",
					StartPath:   "/curations/e5100000-0000-4000-8000-000000000002",
				},
			},
		},
	)
	authHandler.EnableTrustedProxies(config.trustedProxyPrefixes)
	authMiddleware := accounthttp.NewAuthMiddleware(
		authenticationService, marketingAdminEmails, settlementOperatorEmails,
	)
	if catalogWorkspaceResearch != nil {
		catalogWorkspaceResearch.EnableBudgetReader(curationService)
	}
	curationHandler := curationhttp.NewHandler(curationService, logger)
	researchHandler := researchhttp.NewHandler(researchService)
	threadService := curationapp.NewThreadService(curationService, curationRepository, autoapp.ThreadInterpreter{Provider: autoModelProvider, DefaultModel: runnerdomain.DefaultModelKey}, threadPrimitives{curation: curationService, research: researchService, intelligence: intelligenceService, products: intelligencecuration.NewProductAdapter(curationService, intelligenceresearch.NewAdapter(researchService, ids))})
	if intelligenceService != nil {
		intelligenceService.SetActionInterpreter(threadService)
		// The reply runs as the Thread's last model Job, so it needs the same
		// worker and cost ledger as interpretation (ADR-0086).
		if autoModelProvider != nil {
			threadService.SetResponder(autoapp.ThreadResponder{Provider: autoModelProvider, DefaultModel: runnerdomain.DefaultModelKey}, threadResponseSource{research: catalogWorkspaceResearch, fx: exchangeRate})
		}
	}
	researchHandler.SetThreads(threadService)
	threadService.SetCombinationCart(catalogCartService)
	threadHandler := curationhttp.NewThreadHandler(threadService)
	// The legacy action-cancel route settles the owning Thread too.
	intelligenceHandler := intelligencehttp.NewHandler(intelligenceService)
	intelligenceHandler.SetThreadCanceller(threadService)

	conversationService := &conversationapp.Service{Threads: threadService, Repository: curationRepository, Tx: database, Plans: curationService, Research: researchService, Retry: intelligenceService, Provider: autoModelProvider, Model: runnerdomain.DefaultModelKey}
	researchRepository.ConfigureBackgroundAmazon(config.backgroundAmazonDailyCalls, int64(config.backgroundAmazonReserve))
	backgroundService := &researchapp.BackgroundService{Repository: researchRepository, Provider: autoModelProvider, Model: runnerdomain.DefaultModelKey, Enabled: config.backgroundResearchEnabled}
	if config.backgroundResearchEnabled {
		backgroundClient, clientErr := sharedhttpclient.NewClient(transport, 12*time.Second)
		if clientErr != nil {
			return nil, fmt.Errorf("configure background feed HTTP client: %w", clientErr)
		}
		if config.backgroundTelegramEnabled {
			backgroundService.Feeds = append(backgroundService.Feeds, &dealfeed.Telegram{Client: backgroundClient, Control: researchRepository})
			backgroundService.Countries = append(backgroundService.Countries, "KR")
		}
		if config.backgroundAmazonEnabled && config.amazonAPIKey != "" {
			if config.backgroundAmazonDailyCalls <= 0 {
				return nil, fmt.Errorf("BACKGROUND_AMAZON_DAILY_CALLS must be positive before enabling the Amazon feed")
			}
			feed, e := researchamazon.New(researchamazon.Config{APIKey: config.amazonAPIKey, Client: backgroundClient, Usage: researchRepository})
			if e != nil {
				return nil, e
			}
			backgroundService.Feeds = append(backgroundService.Feeds, feed)
			backgroundService.Countries = append(backgroundService.Countries, "US")
		}
	}
	conversationService.Background = backgroundService
	conversationHandler := &conversationhttp.Handler{Service: conversationService}
	var liveCatalogReviewHandler *researchhttp.LiveCatalogReviewHandlerV2
	if config.curationCatalogResearchEnabled {
		if catalogWorkspaceResearch == nil {
			return nil, fmt.Errorf("Phase 8 review requires automated research")
		}
		// The deterministic catalog stub deliberately exposes the same
		// CandidatePool read surface in local/test E2E, while leaving Variant
		// browsing unavailable. Only a real Shopify gateway may install the
		// Storefront authority used by the modal.
		if catalogShopifyGateway != nil {
			liveClient, clientErr := sharedhttpclient.NewClient(
				transport, config.runtimePolicies.shopifyTimeout,
			)
			if clientErr != nil {
				return nil, fmt.Errorf("configure Phase 8 live Shopify HTTP client: %w", clientErr)
			}
			storefrontClient, storefrontErr := researchstorefront.NewClientV2(
				researchstorefront.ClientV2Config{HTTPClient: liveClient},
			)
			if storefrontErr != nil {
				return nil, fmt.Errorf("configure Phase 8 Shopify Storefront client: %w", storefrontErr)
			}
			// The option modal's catalog lookups are Shopify calls like any other and are
			// counted in the same ledger.
			variantResolver, resolverErr := researchstorefront.NewLiveVariantSourceResolverV2(
				researchapp.NewAccountedCatalogGatewayV2(catalogShopifyGateway, researchRepository, clock), storefrontClient, clock, 5*time.Minute,
			)
			if resolverErr != nil {
				return nil, fmt.Errorf("configure Phase 8 Storefront source resolver: %w", resolverErr)
			}
			variantCursorAuthority, cursorErr :=
				researchstorefront.NewMemoryVariantPageCursorAuthorityV2(clock)
			if cursorErr != nil {
				return nil, fmt.Errorf("configure Phase 8 variant cursor authority: %w", cursorErr)
			}
			variantChoice, variantErr := researchapp.NewVariantChoiceServiceV2(
				variantResolver, storefrontClient, variantCursorAuthority,
				clock, 5*time.Minute,
			)
			if variantErr != nil {
				return nil, fmt.Errorf("configure Phase 8 variant choice service: %w", variantErr)
			}
			if enableErr := catalogWorkspaceResearch.EnableVariantChoiceV2(variantChoice); enableErr != nil {
				return nil, fmt.Errorf("enable Phase 8 Storefront variant choice: %w", enableErr)
			}
		}
		liveCatalogReviewHandler = researchhttp.NewLiveCatalogReviewHandlerV2(catalogWorkspaceResearch)
	}
	curationWorkspaceHandler := curationhttp.NewWorkspaceHandler(
		curationService,
		researchService,
		selectionService,
	)
	if agencyOrderRepository != nil {
		curationWorkspaceHandler = curationhttp.NewWorkspaceHandler(
			curationService,
			researchService,
			selectionService,
			agencyOrderRepository,
		)
	}
	curationWorkspaceHandler.EnableConversation(conversationService)
	if intelligenceService != nil {
		curationWorkspaceHandler.EnableIntelligence(intelligenceService)
	}
	if catalogWorkspaceResearch != nil {
		curationWorkspaceHandler.EnableCatalogResearch(catalogWorkspaceResearch)
	}
	sessionHandler := shoppingsessionhttp.NewHandler(sessionService)
	selectionHandler := curationhttp.NewSelectionHandler(selectionService)
	managedRunnerHandler := runnerhttp.NewHandler(
		managedRunnerBudget, managedRunnerRegistry,
		intelligenceService != nil,
	)
	// The reporter is the single assembler of the ops health document; the
	// operator API, the loopback listener and the sampler all read it so the
	// dashboard, the host watch and the time series can never diverge.
	var opsHealthChain chainHeadsReader
	if settlementGateway != nil {
		opsHealthChain = settlementGateway
	}
	reporter := &opsReporter{
		database:         database,
		metrics:          newHTTPMetrics(),
		settlementHealth: settlementHealth,
		chain:            opsHealthChain,
		chainID:          config.settlementConfig.ChainID,
		contractAddress:  config.settlementConfig.SettlementAddress,
		activeKeyVersion: config.piiKeyVersion,
		hostFactsPath:    config.opsHostFactsPath,
	}
	analyticsClient, err := sharedhttpclient.NewClient(transport, 2*time.Second)
	if err != nil {
		return nil, err
	}
	authHandler.EnableAnalyticsIdentity(config.analytics.UserID)
	return &application{
		analytics: analytics.New(config.analytics, analyticsClient),
		config:    config,
		database:  database,
		logger:    logger,
		clock:     clock,
		ids:       ids,

		accountRepository:   accountRepository,
		intelligenceService: intelligenceService,
		intelligenceHandler: intelligenceHandler,
		providers:           providers,
		managedRunnerBudget: managedRunnerBudget,

		accountHandler:          accountHandler,
		authHandler:             authHandler,
		authMiddleware:          authMiddleware,
		operatorSessionsHandler: operatorSessionsHandler,
		browserRunHandler:       browserRunHandler,
		curationHandler:         curationHandler,
		backgroundService:       backgroundService, backgroundHandler: &researchhttp.BackgroundHandler{Service: backgroundService},
		conversationService: conversationService, conversationHandler: conversationHandler,
		threadHandler: threadHandler, threadService: threadService,
		curationWorkspaceHandler: curationWorkspaceHandler,
		selectionHandler:         selectionHandler,
		catalogCartHandler:       curationhttp.NewCatalogCartHandlerV2(catalogCartService),
		researchHandler:          researchHandler,
		liveCatalogReviewHandler: liveCatalogReviewHandler,
		catalogAPIHandler:        &researchhttp.CatalogAPIHandler{Gateway: koreanGateway, BackgroundConfigured: config.backgroundResearchEnabled && config.backgroundTelegramEnabled, ShopifyConfigured: catalogWorkspaceResearch != nil, ShopifyStub: config.catalogProvider == "stub", Control: researchRepository, FX: exchangeRate, Curations: curationService, Summary: researchRepository},
		amazonUsageHandler:       &researchhttp.AmazonUsageHandler{Repo: researchRepository, Control: researchRepository, Gateway: amazonGateway, Mode: strings.ToUpper(config.amazonMode)},
		sessionHandler:           sessionHandler,
		supportHandler:           supportHandler,
		managedRunnerHandler:     managedRunnerHandler,
		settlementHandler:        settlementHandler,
		agencyOrderHandler:       agencyOrderHandler,
		paymentService:           paymentService,
		paymentHandler:           paymentHandler,
		accountingService:        accountingService,
		accountingHandler:        accountingHandler,
		giwaIntake:               giwaIntake,
		agencyOrderLifecycle:     agencyOrderLifecycle,
		procurementService:       procurementService,
		procurementHandler:       procurementHandler,
		logisticsService:         logisticsService,
		logisticsHandler:         logisticsHandler,
		operatorWorkHandler:      operatorWorkHandler,
		liveControlService:       liveControlService,
		liveControlHandler:       liveControlHandler,

		settlementReconciler:    settlementReconciler,
		settlementCommandWorker: settlementCommandWorker,
		agencyOrderWorker:       agencyOrderWorker,
		orderProcessor:          orderProcessor,
		orderEffects:            orderEffects,
		orderQueue:              orderQueue,
		orderProcessWatchdog:    orderProcessWatchdog,
		settlementGateway:       settlementGateway,
		settlementHealth:        settlementHealth,
		httpMetrics:             reporter.metrics,
		piiKeyring:              piiKeyring,
		opsReporter:             reporter,
		heartbeats: newHealthchecksClient(
			config.healthchecksAPIKey, config.healthchecksAPIURL,
			transport, clock,
		),
	}, nil
}

func settlementMerchants(config runtimeConfig) []settlementdomain.MerchantRegistryEntry {
	return []settlementdomain.MerchantRegistryEntry{
		{
			MerchantID:     settlementdomain.GenericWebUSDSettlementPathID,
			DisplayName:    "Generic Web USD manual path",
			DomainSuffixes: []string{}, Country: "ZZ", Currency: "USD",
			FulfillmentMode: "MANUAL_MERCHANT_ORDER", PaymentEnabled: true,
			PrincipalRecipient: config.merchantPrincipals[settlementdomain.GenericWebUSDSettlementPathID],
			RegistryVersion:    config.merchantRegistryVersions[settlementdomain.GenericWebUSDSettlementPathID],
			Active:             true,
		},
		{
			MerchantID: "AMAZON_US", DisplayName: "Amazon US",
			DomainSuffixes: []string{"amazon.com"}, Country: "US", Currency: "USD",
			FulfillmentMode: "MANUAL_MERCHANT_ORDER", PaymentEnabled: true,
			PrincipalRecipient: config.merchantPrincipals["AMAZON_US"],
			RegistryVersion:    config.merchantRegistryVersions["AMAZON_US"], Active: true,
		},
		{
			MerchantID: "WALMART_US", DisplayName: "Walmart US",
			DomainSuffixes: []string{"walmart.com"}, Country: "US", Currency: "USD",
			FulfillmentMode: "MANUAL_MERCHANT_ORDER", PaymentEnabled: true,
			PrincipalRecipient: config.merchantPrincipals["WALMART_US"],
			RegistryVersion:    config.merchantRegistryVersions["WALMART_US"], Active: true,
		},
		{
			MerchantID: "SHOPIFY_UCP", DisplayName: "Shopify UCP reference",
			DomainSuffixes: []string{"myshopify.com", "shopify.com"}, Country: "US", Currency: "USD",
			FulfillmentMode: "MANUAL_MERCHANT_ORDER", PaymentEnabled: true,
			PrincipalRecipient: config.merchantPrincipals["SHOPIFY_UCP"],
			RegistryVersion:    config.merchantRegistryVersions["SHOPIFY_UCP"], Active: true,
		},
		{
			MerchantID: "COUPANG_KR", DisplayName: "Coupang KR",
			DomainSuffixes: []string{"coupang.com"}, Country: "KR", Currency: "KRW",
			FulfillmentMode: "MANUAL_MERCHANT_ORDER", PaymentEnabled: false,
			PrincipalRecipient: "0x0000000000000000000000000000000000000000",
			RegistryVersion:    1, Active: true,
		},
	}
}

func settlementMerchantExpectations(config runtimeConfig) []settlementevm.MerchantExpectation {
	entries := settlementMerchants(config)
	expectations := make([]settlementevm.MerchantExpectation, 0, len(entries))
	for _, entry := range entries {
		if !entry.PaymentEnabled {
			continue
		}
		expectations = append(expectations, settlementevm.MerchantExpectation{
			MerchantID: entry.MerchantID, PrincipalRecipient: entry.PrincipalRecipient,
			RegistryVersion: entry.RegistryVersion,
		})
	}
	return expectations
}

type onchainManifest struct {
	SchemaVersion string `json:"schemaVersion"`
	Version       string `json:"version"`
	Environment   string `json:"environment"`
	ChainID       uint64 `json:"chainId"`
	Status        string `json:"status"`
	Contracts     map[string]struct {
		Address             string `json:"address"`
		RuntimeBytecodeHash string `json:"runtimeBytecodeHash"`
	} `json:"contracts"`
}

func validateOnchainManifest(
	ctx context.Context,
	config runtimeConfig,
	gateway *settlementevm.Gateway,
) error {
	payload, err := os.ReadFile(config.manifestPath)
	if err != nil {
		return fmt.Errorf("read onchain manifest: %w", err)
	}
	var manifest onchainManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return fmt.Errorf("decode onchain manifest: %w", err)
	}
	if manifest.SchemaVersion != "vitlane.onchain-deployment.v1" ||
		manifest.Status != "ACTIVE" || manifest.Environment != "TEST" ||
		manifest.ChainID != config.settlementConfig.ChainID {
		return fmt.Errorf("onchain manifest is not an active matching TEST deployment")
	}
	expected := map[string]string{
		"VitlaneTestUSD":    config.settlementConfig.TokenAddress,
		"VitlaneFaucet":     config.settlementConfig.FaucetAddress,
		"VitlaneSettlement": config.settlementConfig.SettlementAddress,
	}
	for name, expectedAddress := range expected {
		contract, found := manifest.Contracts[name]
		if !found || !strings.EqualFold(contract.Address, expectedAddress) {
			return fmt.Errorf("manifest %s address mismatch", name)
		}
		actualHash, err := gateway.RuntimeCodeHash(ctx, expectedAddress)
		if err != nil {
			return err
		}
		if !strings.EqualFold(actualHash, contract.RuntimeBytecodeHash) {
			return fmt.Errorf("manifest %s runtime bytecode hash mismatch", name)
		}
	}
	return nil
}

// Non-streaming completions send headers only when generation finishes.
// Keep the shared transport unchanged and use the model deadline for this client.
func managedModelHTTPClient(transport http.RoundTripper, timeout time.Duration) (*http.Client, error) {
	if base, ok := transport.(*http.Transport); ok {
		modelTransport := base.Clone()
		modelTransport.ResponseHeaderTimeout = timeout
		return sharedhttpclient.NewClient(modelTransport, timeout)
	}
	return sharedhttpclient.NewClient(transport, timeout)
}
