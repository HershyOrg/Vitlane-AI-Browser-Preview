package main

import (
	"fmt"
	"github.com/vitlane/vitlane/server/internal/shared/infra/analytics"
	"math/big"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	accountdojang "github.com/vitlane/vitlane/server/internal/account/infra/dojang"
	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

const maxHTTPConcurrentRequests = 500

type runtimePolicies struct {
	database              sharedpostgres.Config
	httpTransport         sharedhttpclient.Config
	maxConcurrentRequests int
	requestTimeout        time.Duration
	externalFinalize      time.Duration
	googleOIDCTimeout     time.Duration
	shopifyTimeout        time.Duration
	paypalTimeout         time.Duration
	managedModelTimeout   time.Duration
	evmHTTPClientTimeout  time.Duration
}

type runtimeConfig struct {
	analytics                      analytics.Config
	koreanCatalogEnabled           bool
	koreanDetailLimit              int
	koreanMaxConcurrent            int
	koreanSearchTimeoutSeconds     int
	catalogAPILimitsJSON           string
	ownAPIKey                      string
	naverHubID                     string
	naverHubSecret                 string
	serpAPIKey                     string
	browserProductSearchEnabled    bool
	browserBridgeURL               string
	browserBridgeToken             string
	apifyToken                     string
	apifyMonthlyCapUSD             int
	amazonAPIKey                   string
	backgroundResearchEnabled      bool
	backgroundTelegramEnabled      bool
	backgroundAmazonEnabled        bool
	backgroundAmazonDailyCalls     int
	backgroundAmazonReserve        int
	amazonEnabled                  bool
	amazonMode                     string
	amazonStubFile                 string
	runtimePolicies                runtimePolicies
	production                     bool
	appEnvironment                 string
	allowDevAuth                   bool
	devAuthDefaultUserID           string
	publicBaseURL                  string
	googleClientID                 string
	googleClientSecret             string
	mobileAuthRedirectURIs         []string
	trustedOrigins                 []string
	trustedProxyPrefixes           []netip.Prefix
	marketingAdminEmails           []string
	accountRateLimitSecret         string
	accountRateLimitKeyVersion     int
	marketingWebDir                string
	marketingHost                  string
	settlementOperatorEmails       []string
	settlementEnabled              bool
	settlementEnvironment          string
	settlementConfig               settlementdomain.SettlementConfig
	privateRPCURL                  string
	kycAssuranceMode               string
	quoteSignerKeyFile             string
	finalizerKeyFile               string
	refunderKeyFile                string
	merchantPrincipals             map[string]string
	merchantRegistryVersions       map[string]uint64
	chainStartBlock                uint64
	settlementReconcilePolicy      settlementapp.ReconcilePolicy
	manifestPath                   string
	refunderAddress                string
	pauserAddress                  string
	dojangConfig                   accountdojang.Config
	piiEncryptionKey               string
	piiEncryptionKeys              string
	piiDeletionGraceHours          int
	piiSnapshotRetentionDays       int
	operatorFreshAuthMinutes       int
	piiKeyVersion                  string
	shopifyUCPEnabled              bool
	curationCatalogResearchEnabled bool
	curationCatalogResearchRate    int
	curationCatalogConcurrency     int
	shopifyUCPCatalogURL           string
	shopifyUCPAgentProfileURL      string
	agencyOrderEnabled             bool
	agencyOrderCheckoutProvider    string
	agencyOrderBuyerContactEmail   string
	agencyOrderAgentProfileURL     string
	shopifyDevClientID             string
	shopifyDevClientSecret         string
	paypalSandboxEnabled           bool
	liveAgencyOrderIssueEnabled    bool
	paypalLiveCaptureEnabled       bool
	manualMerchantEffectEnabled    bool
	paypalSandboxClientID          string
	paypalSandboxClientSecret      string
	paypalSandboxWebhookID         string
	paypalLiveClientID             string
	paypalLiveClientSecret         string
	paypalLiveWebhookID            string
	paypalLiveMerchantID           string
	paypalLiveEmail                string
	catalogProvider                string
	managedRunnerEnabled           bool
	managedRunnerProvider          string
	managedRunnerAPIKey            string
	managedRunnerBaseURL           string
	managedRunnerConcurrency       int
	managedRunnerLimits            runnerdomain.Limits
	// Execution admission for intelligence jobs (2026-09-17 research
	// performance track): research rounds are capped per user, per curation
	// and globally; other job kinds keep a short lane; a model call waits a
	// bounded time for a provider slot before the job is deferred.
	intelligenceResearchPerUser     int
	intelligenceResearchPerCuration int
	intelligenceResearchGlobal      int
	intelligenceOtherLane           int
	managedModelSlotWaitSeconds     int
	// Candidate evaluation batching (ADR-0083 PR 4): batches at or under the
	// limit, run in parallel when the round can hold that many model slots.
	researchEvaluationBatchLimit          int
	researchEvaluationParallel            int
	researchEvaluationExtraSlotWaitSecond int
	researchEvaluationBatchTimeoutSeconds int
	opsHostFactsPath                      string
	healthchecksAPIKey                    string
	healthchecksAPIURL                    string
}

func loadConfig() (runtimeConfig, error) {
	runtimePolicies, err := loadRuntimePolicies()
	if err != nil {
		return runtimeConfig{}, err
	}
	appEnvironment := env("APP_ENV", "development")
	if appEnvironment != "development" && appEnvironment != "test" && appEnvironment != "production" {
		return runtimeConfig{}, fmt.Errorf("APP_ENV must be development, test, or production")
	}
	amazonMode := env("AMAZON_PRODUCT_SEARCH_MODE", "live")
	if amazonMode != "live" && amazonMode != "stub" {
		return runtimeConfig{}, fmt.Errorf("AMAZON_PRODUCT_SEARCH_MODE must be live or stub")
	}
	if amazonMode == "stub" && appEnvironment == "production" {
		return runtimeConfig{}, fmt.Errorf("Amazon stub is forbidden in production")
	}
	if amazonMode == "stub" && strings.TrimSpace(os.Getenv("AMAZON_PRODUCT_SEARCH_STUB_FILE")) == "" {
		return runtimeConfig{}, fmt.Errorf("Amazon stub requires AMAZON_PRODUCT_SEARCH_STUB_FILE")
	}
	managedRunnerProvider := env("MANAGED_RUNNER_MODEL_PROVIDER", "openai")
	managedRunnerAPIKey := strings.TrimSpace(
		os.Getenv("MANAGED_OPENAI_API_SECRET"),
	)
	publicBaseURL := strings.TrimRight(env("PUBLIC_BASE_URL", "http://localhost:8080"), "/")
	config := runtimeConfig{
		analytics:                   analytics.Config{Mode: env("ANALYTICS_MODE", "disabled"), MeasurementID: strings.TrimSpace(os.Getenv("GA4_MEASUREMENT_ID")), APISecret: strings.TrimSpace(os.Getenv("GA4_API_SECRET")), IdentityKey: os.Getenv("ANALYTICS_IDENTITY_KEY"), Release: sourceRevision},
		runtimePolicies:             runtimePolicies,
		production:                  appEnvironment == "production",
		appEnvironment:              appEnvironment,
		allowDevAuth:                env("ALLOW_DEV_AUTH", "false") == "true",
		devAuthDefaultUserID:        strings.TrimSpace(os.Getenv("DEV_AUTH_DEFAULT_USER_ID")),
		publicBaseURL:               publicBaseURL,
		googleClientID:              strings.TrimSpace(os.Getenv("GOOGLE_OIDC_CLIENT_ID")),
		googleClientSecret:          strings.TrimSpace(os.Getenv("GOOGLE_OIDC_CLIENT_SECRET")),
		marketingAdminEmails:        splitNonEmpty(os.Getenv("MARKETING_ADMIN_EMAILS")),
		accountRateLimitSecret:      env("ACCOUNT_RATE_LIMIT_SECRET", "local-account-rate-limit-secret"),
		accountRateLimitKeyVersion:  envInt("ACCOUNT_RATE_LIMIT_KEY_VERSION", 1),
		marketingWebDir:             strings.TrimSpace(os.Getenv("MARKETING_WEB_DIR")),
		settlementOperatorEmails:    splitNonEmpty(os.Getenv("PHASE5_OPERATOR_EMAILS")),
		settlementEnabled:           env("PHASE5_SETTLEMENT_ENABLED", "false") == "true",
		shopifyUCPEnabled:           env("SHOPIFY_UCP_ENABLED", "false") == "true",
		agencyOrderEnabled:          env("AGENCY_ORDER_ENABLED", "false") == "true",
		agencyOrderCheckoutProvider: strings.ToLower(env("AGENCY_ORDER_CHECKOUT_PROVIDER", "stub")),
		// UCP buyer_identity 연락 이메일 — 운영(대행) 소유. 고객 연락처를 여기
		// 넣지 않는다(견적 단계 PII 유출·Shop발 예상외 연락 방지, 운영정합 3차).
		agencyOrderBuyerContactEmail: strings.TrimSpace(env("AGENCY_ORDER_BUYER_CONTACT_EMAIL", "")),
		agencyOrderAgentProfileURL:   publicBaseURL + "/.well-known/ucp-agent.json",
		shopifyDevClientID:           strings.TrimSpace(os.Getenv("SHOPIFY_DEV_CLIENT_ID")),
		shopifyDevClientSecret:       strings.TrimSpace(os.Getenv("SHOPIFY_DEV_CLIENT_SECRET")),
		paypalSandboxEnabled:         env("PAYPAL_SANDBOX_ENABLED", "false") == "true",
		liveAgencyOrderIssueEnabled:  env("LIVE_AGENCY_ORDER_ISSUE_ENABLED", "false") == "true",
		paypalLiveCaptureEnabled:     env("PAYPAL_LIVE_CAPTURE_ENABLED", "false") == "true",
		// LIVE merchant effect는 Step 6 activation 사건에서만 켠다(ADR-0052).
		manualMerchantEffectEnabled:    env("MANUAL_MERCHANT_EFFECT_ENABLED", "false") == "true",
		paypalSandboxClientID:          strings.TrimSpace(os.Getenv("PAYPAL_SANDBOX_CLIENT_ID")),
		paypalSandboxClientSecret:      strings.TrimSpace(os.Getenv("PAYPAL_SANDBOX_CLIENT_SECRET")),
		paypalSandboxWebhookID:         strings.TrimSpace(os.Getenv("PAYPAL_SANDBOX_WEBHOOK_ID")),
		paypalLiveClientID:             strings.TrimSpace(os.Getenv("PAYPAL_LIVE_CLIENT_ID")),
		paypalLiveClientSecret:         strings.TrimSpace(os.Getenv("PAYPAL_LIVE_CLIENT_SECRET")),
		paypalLiveWebhookID:            strings.TrimSpace(os.Getenv("PAYPAL_LIVE_WEBHOOK_ID")),
		paypalLiveMerchantID:           strings.TrimSpace(os.Getenv("PAYPAL_LIVE_MERCHANT_ID")),
		paypalLiveEmail:                strings.TrimSpace(os.Getenv("PAYPAL_LIVE_EMAIL")),
		koreanCatalogEnabled:           env("KOREAN_PRODUCT_SEARCH_ENABLED", "false") == "true",
		koreanDetailLimit:              envInt("KOREAN_CATALOG_DETAIL_LIMIT", 4),
		koreanMaxConcurrent:            envInt("KOREAN_CATALOG_MAX_CONCURRENT", 4),
		koreanSearchTimeoutSeconds:     envInt("KOREAN_CATALOG_TIMEOUT_SECONDS", 60),
		catalogAPILimitsJSON:           os.Getenv("CATALOG_API_LIMITS_JSON"),
		ownAPIKey:                      strings.TrimSpace(os.Getenv("OPEN_WEB_NINJA_API_KEY")),
		naverHubID:                     strings.TrimSpace(os.Getenv("NAVER_API_HUB_CLIENT_ID")),
		naverHubSecret:                 strings.TrimSpace(os.Getenv("NAVER_API_HUB_CLIENT_SECRET")),
		serpAPIKey:                     strings.TrimSpace(os.Getenv("SERP_API_KEY")),
		browserProductSearchEnabled:    env("BROWSER_PRODUCT_SEARCH_ENABLED", "false") == "true",
		browserBridgeURL:               strings.TrimRight(env("BROWSER_BRIDGE_URL", "http://127.0.0.1:8787"), "/"),
		browserBridgeToken:             strings.TrimSpace(os.Getenv("BRIDGE_TOKEN")),
		apifyToken:                     strings.TrimSpace(os.Getenv("APIFY_API_TOKEN")),
		apifyMonthlyCapUSD:             envInt("APIFY_MONTHLY_CAP_USD", 5),
		amazonAPIKey:                   strings.TrimSpace(os.Getenv("AMAZON_PRODUCT_SEARCH_API")),
		backgroundResearchEnabled:      env("BACKGROUND_RESEARCH_ENABLED", "false") == "true",
		backgroundTelegramEnabled:      env("BACKGROUND_TELEGRAM_ENABLED", "true") == "true",
		backgroundAmazonEnabled:        env("BACKGROUND_AMAZON_ENABLED", "false") == "true",
		backgroundAmazonDailyCalls:     envInt("BACKGROUND_AMAZON_DAILY_CALLS", 0),
		backgroundAmazonReserve:        envInt("BACKGROUND_AMAZON_FOREGROUND_RESERVE", 10),
		amazonEnabled:                  env("AMAZON_PRODUCT_SEARCH_ENABLED", "false") == "true",
		amazonMode:                     amazonMode,
		amazonStubFile:                 strings.TrimSpace(os.Getenv("AMAZON_PRODUCT_SEARCH_STUB_FILE")),
		curationCatalogResearchEnabled: env("CURATION_CATALOG_RESEARCH_ENABLED", "false") == "true",
		curationCatalogResearchRate:    envInt("CURATION_CATALOG_RESEARCH_CALLS_PER_MINUTE", 10),
		curationCatalogConcurrency:     envInt("CURATION_CATALOG_RESEARCH_MAX_CONCURRENCY", 16),
		catalogProvider:                env("RESEARCH_CATALOG_PROVIDER", ""),
		managedRunnerEnabled: managedRunnerEnabled(
			os.Getenv("MANAGED_RUNNER_ENABLED"),
			managedRunnerProvider, managedRunnerAPIKey,
		),
		managedRunnerProvider: managedRunnerProvider,
		managedRunnerAPIKey:   managedRunnerAPIKey,
		managedRunnerBaseURL:  env("MANAGED_RUNNER_BASE_URL", ""),
		// OpenAI-compatible APIs throttle around ten concurrent requests, so
		// that is the provider's own ceiling rather than a guess. A user
		// cannot monopolise it: each is capped at three concurrent actions.
		managedRunnerConcurrency:              envInt("MANAGED_RUNNER_CONCURRENCY", 10),
		intelligenceResearchPerUser:           envInt("INTELLIGENCE_RESEARCH_MAX_PER_USER", 3),
		intelligenceResearchPerCuration:       envInt("INTELLIGENCE_RESEARCH_MAX_PER_CURATION", 3),
		intelligenceResearchGlobal:            envInt("INTELLIGENCE_RESEARCH_MAX_GLOBAL", 6),
		intelligenceOtherLane:                 envInt("INTELLIGENCE_OTHER_JOB_LANE", 4),
		managedModelSlotWaitSeconds:           envInt("MANAGED_MODEL_SLOT_WAIT_SECONDS", 30),
		researchEvaluationBatchLimit:          envInt("RESEARCH_EVALUATION_BATCH_LIMIT", 25),
		researchEvaluationParallel:            envInt("RESEARCH_EVALUATION_PARALLEL", 2),
		researchEvaluationExtraSlotWaitSecond: envInt("RESEARCH_EVALUATION_EXTRA_SLOT_WAIT_SECONDS", 10),
		researchEvaluationBatchTimeoutSeconds: envInt("RESEARCH_EVALUATION_BATCH_TIMEOUT_SECONDS", 120),
		managedRunnerLimits: runnerdomain.Limits{
			ServerHard:      int64(envInt("MANAGED_RUNNER_SERVER_DAILY_MICROS", 10_000_000)),
			ServerAdmission: int64(envInt("MANAGED_RUNNER_SERVER_ADMISSION_MICROS", 8_000_000)),
			UserDaily:       int64(envInt("MANAGED_RUNNER_USER_DAILY_MICROS", 100_000)),
		},
		shopifyUCPCatalogURL: env(
			"SHOPIFY_UCP_CATALOG_URL", "https://catalog.shopify.com/api/ucp/mcp",
		),
		shopifyUCPAgentProfileURL: env(
			"SHOPIFY_UCP_AGENT_PROFILE_URL",
			"https://shopify.dev/ucp/agent-profiles/2026-04-08/valid-with-capabilities.json",
		),
		settlementEnvironment: strings.ToUpper(env("SETTLEMENT_ENV", "DISABLED")),
		privateRPCURL:         strings.TrimSpace(os.Getenv("GIWA_RPC_URL")),
		settlementReconcilePolicy: settlementapp.ReconcilePolicy{
			BatchSize: envInt("SETTLEMENT_RECONCILE_BATCH_SIZE", 50),
			TickTimeout: time.Duration(envInt(
				"SETTLEMENT_RECONCILE_TICK_TIMEOUT_SECONDS", 30,
			)) * time.Second,
			RPCTimeout: time.Duration(envInt(
				"SETTLEMENT_RECONCILE_RPC_TIMEOUT_SECONDS", 5,
			)) * time.Second,
			EventChunkSize: uint64(envInt(
				"SETTLEMENT_RECONCILE_EVENT_CHUNK_BLOCKS", 1_000,
			)),
			EventOverlapBlocks: uint64(envInt(
				"SETTLEMENT_RECONCILE_EVENT_OVERLAP_BLOCKS", 32,
			)),
			ObservationTimeout: time.Duration(envInt(
				"SETTLEMENT_RECONCILE_OBSERVATION_TIMEOUT_SECONDS", 21_600,
			)) * time.Second,
			ObservationBackoffMin: time.Duration(envInt(
				"SETTLEMENT_RECONCILE_BACKOFF_MIN_SECONDS", 15,
			)) * time.Second,
			ObservationBackoffMax: time.Duration(envInt(
				"SETTLEMENT_RECONCILE_BACKOFF_MAX_SECONDS", 900,
			)) * time.Second,
		},
		kycAssuranceMode:         strings.ToUpper(strings.TrimSpace(os.Getenv("KYC_ASSURANCE_MODE"))),
		piiEncryptionKey:         strings.TrimSpace(os.Getenv("PII_ENCRYPTION_KEY")),
		piiEncryptionKeys:        strings.TrimSpace(os.Getenv("PII_ENCRYPTION_KEYS")),
		piiDeletionGraceHours:    envInt("PII_DELETION_GRACE_HOURS", 24),
		piiSnapshotRetentionDays: envInt("PII_SNAPSHOT_RETENTION_DAYS", 1825),
		operatorFreshAuthMinutes: envInt("OPERATOR_FRESH_AUTH_MINUTES", 15),
		piiKeyVersion:            strings.TrimSpace(env("PII_KEY_VERSION", "v1")),
		// L3 facts file the host watch writes; empty (dev, E2E) omits the
		// host section from the ops health report.
		opsHostFactsPath: strings.TrimSpace(os.Getenv("OPS_HOST_FACTS_PATH")),
		// Read-only Healthchecks project key for the dashboard's L1 card.
		// Empty keeps the endpoint in "not configured" mode.
		healthchecksAPIKey: strings.TrimSpace(os.Getenv("HEALTHCHECKS_API_KEY")),
		healthchecksAPIURL: strings.TrimRight(strings.TrimSpace(env(
			"HEALTHCHECKS_API_URL", "https://healthchecks.io/api/v3/checks/",
		)), "/") + "/",
		quoteSignerKeyFile: strings.TrimSpace(os.Getenv("QUOTE_SIGNER_KEY_FILE")),
		finalizerKeyFile:   strings.TrimSpace(os.Getenv("FINALIZER_KEY_FILE")),
		refunderKeyFile:    strings.TrimSpace(os.Getenv("REFUNDER_KEY_FILE")),
		manifestPath:       strings.TrimSpace(os.Getenv("ONCHAIN_MANIFEST_PATH")),
		refunderAddress:    strings.TrimSpace(os.Getenv("REFUNDER_ADDRESS")),
		pauserAddress:      strings.TrimSpace(os.Getenv("PAUSER_ADDRESS")),
		dojangConfig: accountdojang.Config{
			ScrollAddress: strings.TrimSpace(env(
				"DOJANG_SCROLL_ADDRESS", "0xd5077b67dcb56caC8b270C7788FC3E6ee03F17B9",
			)),
			EASAddress: strings.TrimSpace(env(
				"DOJANG_EAS_ADDRESS", "0x4200000000000000000000000000000000000021",
			)),
			AttesterID:      strings.TrimSpace(os.Getenv("DOJANG_ATTESTER_ID")),
			AttesterAddress: strings.TrimSpace(os.Getenv("DOJANG_ATTESTER_ADDRESS")),
			SchemaUID: strings.TrimSpace(env(
				"DOJANG_SCHEMA_UID", "0x072d75e18b2be4f89a13a7147240477481c4b526d5795802acba59046b426e08",
			)),
		},
		merchantPrincipals: map[string]string{
			"GENERIC_WEB_USD": strings.TrimSpace(os.Getenv("GENERIC_WEB_TEST_PRINCIPAL")),
			"AMAZON_US":       strings.TrimSpace(os.Getenv("AMAZON_TEST_PRINCIPAL")),
			"WALMART_US":      strings.TrimSpace(os.Getenv("WALMART_TEST_PRINCIPAL")),
			"SHOPIFY_UCP":     strings.TrimSpace(os.Getenv("SHOPIFY_TEST_PRINCIPAL")),
		},
		merchantRegistryVersions: make(map[string]uint64, 4),
	}
	chainID, err := parseUintEnv("GIWA_CHAIN_ID", 91342)
	if err != nil {
		return runtimeConfig{}, err
	}
	for _, merchant := range []struct {
		id     string
		envKey string
	}{
		{id: "GENERIC_WEB_USD", envKey: "GENERIC_WEB_TEST_REGISTRY_VERSION"},
		{id: "AMAZON_US", envKey: "AMAZON_TEST_REGISTRY_VERSION"},
		{id: "WALMART_US", envKey: "WALMART_TEST_REGISTRY_VERSION"},
		{id: "SHOPIFY_UCP", envKey: "SHOPIFY_TEST_REGISTRY_VERSION"},
	} {
		version, versionErr := parseUintEnv(merchant.envKey, 1)
		if versionErr != nil {
			return runtimeConfig{}, versionErr
		}
		config.merchantRegistryVersions[merchant.id] = version
	}
	chainStartBlock, err := parseUintEnv("CHAIN_START_BLOCK", 0)
	if err != nil {
		return runtimeConfig{}, err
	}
	config.chainStartBlock = chainStartBlock
	config.settlementConfig = settlementdomain.SettlementConfig{
		Environment: config.settlementEnvironment,
		ChainID:     chainID, ChainCAIP2: fmt.Sprintf("eip155:%d", chainID),
		RPCURL:            strings.TrimSpace(os.Getenv("GIWA_PUBLIC_RPC_URL")),
		ExplorerURL:       strings.TrimSpace(os.Getenv("GIWA_EXPLORER_URL")),
		TokenAddress:      strings.TrimSpace(os.Getenv("TVITUSD_ADDRESS")),
		FaucetAddress:     strings.TrimSpace(os.Getenv("FAUCET_ADDRESS")),
		SettlementAddress: strings.TrimSpace(os.Getenv("SETTLEMENT_ADDRESS")),
		TokenSymbol:       "tVITUSD", TokenDecimals: 6, FeeBps: 100,
		FeeRecipient: strings.TrimSpace(os.Getenv("TEST_FEE")),
		ClaimAmount:  env("FAUCET_CLAIM_AMOUNT", "1000000000"),
	}
	switch config.kycAssuranceMode {
	case "DOJANG_VERIFIED_ADDRESS":
		config.dojangConfig.Level = accountdomain.AssuranceDojangVerifiedAddress
		if config.dojangConfig.AttesterID == "" {
			config.dojangConfig.AttesterID = "0xd99b42e778498aa3c9c1f6a012359130252780511687a35982e8e52735453034"
		}
		if config.dojangConfig.AttesterAddress == "" {
			config.dojangConfig.AttesterAddress = "0x4097bF3Cb731AEB3E501b910B33B2aF9Fa68E388"
		}
	}
	parsed, err := url.Parse(config.publicBaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" {
		return runtimeConfig{}, fmt.Errorf("PUBLIC_BASE_URL must be an origin without a path")
	}
	if marketingBaseURL := strings.TrimSpace(os.Getenv("MARKETING_BASE_URL")); marketingBaseURL != "" {
		marketingURL, err := url.Parse(strings.TrimRight(marketingBaseURL, "/"))
		if err != nil || marketingURL.Scheme == "" || marketingURL.Host == "" || marketingURL.Path != "" {
			return runtimeConfig{}, fmt.Errorf("MARKETING_BASE_URL must be an origin without a path")
		}
		config.marketingHost = marketingURL.Hostname()
	}
	if (config.marketingWebDir == "") != (config.marketingHost == "") {
		return runtimeConfig{}, fmt.Errorf("MARKETING_WEB_DIR and MARKETING_BASE_URL must be configured together")
	}
	if config.production {
		if !config.agencyOrderEnabled {
			return runtimeConfig{}, fmt.Errorf("production requires AGENCY_ORDER_ENABLED=true")
		}
		if !config.curationCatalogResearchEnabled {
			return runtimeConfig{}, fmt.Errorf("production requires CURATION_CATALOG_RESEARCH_ENABLED=true")
		}
		if !config.shopifyUCPEnabled {
			return runtimeConfig{}, fmt.Errorf("production Catalog Research requires SHOPIFY_UCP_ENABLED=true")
		}
		if config.catalogProvider == "stub" {
			return runtimeConfig{}, fmt.Errorf("production Catalog Research cannot use the catalog stub")
		}
		if config.allowDevAuth {
			return runtimeConfig{}, fmt.Errorf("ALLOW_DEV_AUTH cannot be true in production")
		}
		if config.devAuthDefaultUserID != "" {
			return runtimeConfig{}, fmt.Errorf("DEV_AUTH_DEFAULT_USER_ID cannot be set in production")
		}
		if parsed.Scheme != "https" {
			return runtimeConfig{}, fmt.Errorf("production PUBLIC_BASE_URL must use HTTPS")
		}
		if config.googleClientID == "" || config.googleClientSecret == "" {
			return runtimeConfig{}, fmt.Errorf("production requires Google OIDC credentials")
		}
		if len(config.marketingAdminEmails) == 0 {
			return runtimeConfig{}, fmt.Errorf("production requires MARKETING_ADMIN_EMAILS")
		}
		if config.accountRateLimitSecret == "local-account-rate-limit-secret" {
			return runtimeConfig{}, fmt.Errorf("production requires ACCOUNT_RATE_LIMIT_SECRET")
		}
		if config.settlementEnabled && !config.shopifyUCPEnabled {
			return runtimeConfig{}, fmt.Errorf("production Phase 5 requires SHOPIFY_UCP_ENABLED")
		}
	}
	if len(config.accountRateLimitSecret) < 16 ||
		config.accountRateLimitKeyVersion <= 0 {
		return runtimeConfig{}, fmt.Errorf(
			"ACCOUNT_RATE_LIMIT_SECRET must be at least 16 characters and " +
				"ACCOUNT_RATE_LIMIT_KEY_VERSION must be positive",
		)
	}
	if config.devAuthDefaultUserID != "" && !config.allowDevAuth {
		return runtimeConfig{}, fmt.Errorf("DEV_AUTH_DEFAULT_USER_ID requires ALLOW_DEV_AUTH=true")
	}
	if config.curationCatalogResearchEnabled &&
		!config.shopifyUCPEnabled && config.catalogProvider != "stub" {
		return runtimeConfig{}, fmt.Errorf(
			"CURATION_CATALOG_RESEARCH_ENABLED requires Shopify or the test catalog stub",
		)
	}
	if config.curationCatalogResearchRate < 1 || config.curationCatalogResearchRate > 60 {
		return runtimeConfig{}, fmt.Errorf("CURATION_CATALOG_RESEARCH_CALLS_PER_MINUTE must be between 1 and 60")
	}
	if config.curationCatalogConcurrency < 1 || config.curationCatalogConcurrency > 16 {
		return runtimeConfig{}, fmt.Errorf("CURATION_CATALOG_RESEARCH_MAX_CONCURRENCY must be between 1 and 16")
	}
	if config.intelligenceResearchPerUser < 1 || config.intelligenceResearchPerUser > 20 ||
		config.intelligenceResearchPerCuration < 1 || config.intelligenceResearchPerCuration > config.intelligenceResearchPerUser ||
		config.intelligenceResearchGlobal < 1 || config.intelligenceResearchGlobal > 50 ||
		config.intelligenceOtherLane < 1 || config.intelligenceOtherLane > 20 {
		return runtimeConfig{}, fmt.Errorf("INTELLIGENCE_RESEARCH_MAX_PER_USER (1-20), INTELLIGENCE_RESEARCH_MAX_PER_CURATION (1..per-user), INTELLIGENCE_RESEARCH_MAX_GLOBAL (1-50) and INTELLIGENCE_OTHER_JOB_LANE (1-20) must be within range")
	}
	if config.managedModelSlotWaitSeconds < 1 || config.managedModelSlotWaitSeconds > 120 {
		return runtimeConfig{}, fmt.Errorf("MANAGED_MODEL_SLOT_WAIT_SECONDS must be between 1 and 120")
	}
	if config.researchEvaluationBatchLimit < 5 || config.researchEvaluationBatchLimit > 50 ||
		config.researchEvaluationParallel < 1 || config.researchEvaluationParallel > 4 ||
		config.researchEvaluationExtraSlotWaitSecond < 1 || config.researchEvaluationExtraSlotWaitSecond > 60 ||
		config.researchEvaluationBatchTimeoutSeconds < 30 || config.researchEvaluationBatchTimeoutSeconds > 600 {
		return runtimeConfig{}, fmt.Errorf("RESEARCH_EVALUATION_BATCH_LIMIT (5-50), RESEARCH_EVALUATION_PARALLEL (1-4), RESEARCH_EVALUATION_EXTRA_SLOT_WAIT_SECONDS (1-60) and RESEARCH_EVALUATION_BATCH_TIMEOUT_SECONDS (30-600) must be within range")
	}
	if config.koreanMaxConcurrent < 1 || config.koreanMaxConcurrent > 8 || config.koreanSearchTimeoutSeconds < 10 || config.koreanSearchTimeoutSeconds > 120 {
		return runtimeConfig{}, fmt.Errorf("invalid Korean catalog concurrency or timeout")
	}
	if config.koreanDetailLimit < 1 || config.koreanDetailLimit > 10 {
		return runtimeConfig{}, fmt.Errorf("KOREAN_CATALOG_DETAIL_LIMIT must be between 1 and 10")
	}
	if config.browserProductSearchEnabled {
		browserURL, browserErr := url.Parse(config.browserBridgeURL)
		if browserErr != nil || browserURL == nil {
			return runtimeConfig{}, fmt.Errorf("BROWSER_BRIDGE_URL must be an HTTPS origin or a loopback HTTP origin")
		}
		browserHost := strings.TrimSuffix(strings.ToLower(browserURL.Hostname()), ".")
		browserIP, _ := netip.ParseAddr(browserHost)
		if !config.koreanCatalogEnabled {
			return runtimeConfig{}, fmt.Errorf("BROWSER_PRODUCT_SEARCH_ENABLED requires KOREAN_PRODUCT_SEARCH_ENABLED=true")
		}
		if browserURL.User != nil || browserURL.Host == "" ||
			(browserURL.Path != "" && browserURL.Path != "/") || browserURL.RawQuery != "" || browserURL.Fragment != "" ||
			(browserURL.Scheme != "https" && !(browserURL.Scheme == "http" && (browserHost == "localhost" || browserIP.IsValid() && browserIP.IsLoopback()))) {
			return runtimeConfig{}, fmt.Errorf("BROWSER_BRIDGE_URL must be an HTTPS origin or a loopback HTTP origin")
		}
		if config.production && browserURL.Scheme != "https" {
			return runtimeConfig{}, fmt.Errorf("production BROWSER_BRIDGE_URL must use HTTPS")
		}
		if len([]byte(config.browserBridgeToken)) < 32 {
			return runtimeConfig{}, fmt.Errorf("BRIDGE_TOKEN must have at least 32 bytes when browser product search is enabled")
		}
	}
	// The Actor runs are billed by the third party, so the cap is a hard
	// number this process refuses to start without.
	if config.apifyMonthlyCapUSD < 1 || config.apifyMonthlyCapUSD > 100 {
		return runtimeConfig{}, fmt.Errorf("APIFY_MONTHLY_CAP_USD must be between 1 and 100")
	}
	if config.shopifyUCPEnabled {
		if config.shopifyDevClientID == "" || config.shopifyDevClientSecret == "" {
			return runtimeConfig{}, fmt.Errorf("Shopify Catalog Research requires SHOPIFY_DEV_CLIENT_ID and SHOPIFY_DEV_CLIENT_SECRET")
		}
		catalogURL, catalogErr := url.Parse(config.shopifyUCPCatalogURL)
		profileURL, profileErr := url.Parse(config.shopifyUCPAgentProfileURL)
		if catalogErr != nil || catalogURL.Scheme == "" || catalogURL.Host == "" ||
			profileErr != nil || profileURL.Scheme == "" || profileURL.Host == "" {
			return runtimeConfig{}, fmt.Errorf("Shopify UCP catalog and agent profile URLs must be absolute")
		}
		if config.production &&
			(catalogURL.Scheme != "https" ||
				!strings.EqualFold(catalogURL.Hostname(), "catalog.shopify.com") ||
				catalogURL.Path != "/api/ucp/mcp" ||
				profileURL.Scheme != "https" ||
				(!strings.EqualFold(profileURL.Hostname(), "shopify.dev") &&
					(!config.agencyOrderEnabled || config.shopifyUCPAgentProfileURL != config.agencyOrderAgentProfileURL))) {
			return runtimeConfig{}, fmt.Errorf("production Shopify UCP URLs must use the reviewed official hosts")
		}
	}
	// The retired broad switch remains invalid. Live readiness uses separate
	// issue/capture/effect gates so one environment variable cannot move money.
	if env("PAYPAL_LIVE_ENABLED", "false") == "true" {
		return runtimeConfig{}, fmt.Errorf("PAYPAL_LIVE_ENABLED is retired; use the staged Live gates")
	}
	if config.paypalSandboxEnabled {
		if !config.agencyOrderEnabled {
			return runtimeConfig{}, fmt.Errorf("PAYPAL_SANDBOX_ENABLED requires AGENCY_ORDER_ENABLED=true")
		}
		if config.paypalSandboxClientID == "" || config.paypalSandboxClientSecret == "" ||
			config.paypalSandboxWebhookID == "" {
			return runtimeConfig{}, fmt.Errorf(
				"PAYPAL_SANDBOX_ENABLED requires PAYPAL_SANDBOX_CLIENT_ID, " +
					"PAYPAL_SANDBOX_CLIENT_SECRET and PAYPAL_SANDBOX_WEBHOOK_ID",
			)
		}
	}
	liveCredentials := 0
	for _, value := range []string{
		config.paypalLiveClientID, config.paypalLiveClientSecret,
		config.paypalLiveWebhookID, config.paypalLiveMerchantID,
	} {
		if value != "" {
			liveCredentials++
		}
	}
	if liveCredentials != 0 && liveCredentials != 4 {
		return runtimeConfig{}, fmt.Errorf(
			"PayPal Live adapter requires PAYPAL_LIVE_CLIENT_ID, PAYPAL_LIVE_CLIENT_SECRET, PAYPAL_LIVE_WEBHOOK_ID and PAYPAL_LIVE_MERCHANT_ID together",
		)
	}
	if config.liveAgencyOrderIssueEnabled {
		if !config.agencyOrderEnabled || liveCredentials != 4 {
			return runtimeConfig{}, fmt.Errorf(
				"LIVE_AGENCY_ORDER_ISSUE_ENABLED requires AgencyOrder and the complete Live adapter configuration",
			)
		}
	}
	if config.paypalLiveCaptureEnabled &&
		(!config.liveAgencyOrderIssueEnabled || liveCredentials != 4) {
		return runtimeConfig{}, fmt.Errorf(
			"PAYPAL_LIVE_CAPTURE_ENABLED requires the Live issue gate and complete Live adapter configuration",
		)
	}
	if config.manualMerchantEffectEnabled && !config.paypalLiveCaptureEnabled {
		return runtimeConfig{}, fmt.Errorf(
			"MANUAL_MERCHANT_EFFECT_ENABLED requires the PayPal Live capture gate",
		)
	}
	if config.agencyOrderEnabled {
		if !config.settlementEnabled || !config.curationCatalogResearchEnabled {
			return runtimeConfig{}, fmt.Errorf("AGENCY_ORDER_ENABLED requires Phase 5 settlement and Catalog Research")
		}
		if config.agencyOrderCheckoutProvider != "stub" && config.agencyOrderCheckoutProvider != "shopify" {
			return runtimeConfig{}, fmt.Errorf("AGENCY_ORDER_CHECKOUT_PROVIDER must be stub or shopify")
		}
		if config.production && config.agencyOrderCheckoutProvider != "shopify" {
			return runtimeConfig{}, fmt.Errorf("production AgencyOrder requires Shopify checkout provider")
		}
		if config.agencyOrderCheckoutProvider == "shopify" &&
			(config.shopifyDevClientID == "" || config.shopifyDevClientSecret == "") {
			return runtimeConfig{}, fmt.Errorf("Shopify AgencyOrder checkout requires SHOPIFY_DEV_CLIENT_ID and SHOPIFY_DEV_CLIENT_SECRET")
		}
		if config.production && config.shopifyUCPAgentProfileURL != config.agencyOrderAgentProfileURL {
			return runtimeConfig{}, fmt.Errorf("production AgencyOrder requires the Vitlane agent profile")
		}
	}
	if err := config.validateSettlement(appEnvironment); err != nil {
		return runtimeConfig{}, err
	}
	if origin := strings.TrimSpace(os.Getenv("TRUSTED_BROWSER_ORIGIN")); origin != "" {
		config.trustedOrigins = append(config.trustedOrigins, origin)
	}
	for _, rawURI := range strings.Split(env("MOBILE_AUTH_REDIRECT_URIS", "vitlane://auth/callback"), ",") {
		rawURI = strings.TrimSpace(rawURI)
		parsedURI, err := url.Parse(rawURI)
		if err != nil || parsedURI.Scheme == "" || parsedURI.Host == "" ||
			parsedURI.RawQuery != "" || parsedURI.Fragment != "" || parsedURI.User != nil {
			return runtimeConfig{}, fmt.Errorf("MOBILE_AUTH_REDIRECT_URIS contains invalid redirect URI %q", rawURI)
		}
		config.mobileAuthRedirectURIs = append(config.mobileAuthRedirectURIs, rawURI)
	}
	if len(config.mobileAuthRedirectURIs) == 0 {
		return runtimeConfig{}, fmt.Errorf("MOBILE_AUTH_REDIRECT_URIS requires at least one redirect URI")
	}
	for _, rawPrefix := range strings.Split(os.Getenv("TRUSTED_PROXY_CIDRS"), ",") {
		rawPrefix = strings.TrimSpace(rawPrefix)
		if rawPrefix == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(rawPrefix)
		if err != nil {
			return runtimeConfig{}, fmt.Errorf(
				"TRUSTED_PROXY_CIDRS contains invalid CIDR %q", rawPrefix,
			)
		}
		config.trustedProxyPrefixes = append(
			config.trustedProxyPrefixes,
			prefix.Masked(),
		)
	}
	if err := config.analytics.Validate(config.production); err != nil {
		return runtimeConfig{}, err
	}
	return config, nil
}

func loadRuntimePolicies() (runtimePolicies, error) {
	database := sharedpostgres.DefaultConfig()
	transport := sharedhttpclient.DefaultConfig()
	var err error
	if database.MaxOpenConnections, err = positiveIntEnv(
		"DB_MAX_OPEN_CONNECTIONS", database.MaxOpenConnections,
	); err != nil {
		return runtimePolicies{}, err
	}
	if database.MaxIdleConnections, err = nonNegativeIntEnv(
		"DB_MAX_IDLE_CONNECTIONS", database.MaxIdleConnections,
	); err != nil {
		return runtimePolicies{}, err
	}
	if database.ConnMaxIdleTime, err = secondsEnv(
		"DB_CONNECTION_MAX_IDLE_SECONDS", database.ConnMaxIdleTime,
	); err != nil {
		return runtimePolicies{}, err
	}
	if database.ConnMaxLifetime, err = secondsEnv(
		"DB_CONNECTION_MAX_LIFETIME_SECONDS", database.ConnMaxLifetime,
	); err != nil {
		return runtimePolicies{}, err
	}
	if database.AcquireTimeout, err = secondsEnv(
		"DB_ACQUIRE_TIMEOUT_SECONDS", database.AcquireTimeout,
	); err != nil {
		return runtimePolicies{}, err
	}
	if database.MigrationLockTimeout, err = secondsEnv(
		"DB_MIGRATION_LOCK_TIMEOUT_SECONDS", database.MigrationLockTimeout,
	); err != nil {
		return runtimePolicies{}, err
	}
	classes := []struct {
		prefix string
		value  *sharedpostgres.TimeoutClass
	}{
		{"DB_INTERACTIVE", &database.Interactive},
		{"DB_WORKER", &database.Worker},
		{"DB_ADMIN", &database.Admin},
	}
	for _, class := range classes {
		if class.value.Query, err = secondsEnv(
			class.prefix+"_QUERY_TIMEOUT_SECONDS", class.value.Query,
		); err != nil {
			return runtimePolicies{}, err
		}
		if class.value.Transaction, err = secondsEnv(
			class.prefix+"_TRANSACTION_TIMEOUT_SECONDS", class.value.Transaction,
		); err != nil {
			return runtimePolicies{}, err
		}
		if class.value.Lock, err = secondsEnv(
			class.prefix+"_LOCK_TIMEOUT_SECONDS", class.value.Lock,
		); err != nil {
			return runtimePolicies{}, err
		}
	}
	if err := database.Validate(); err != nil {
		return runtimePolicies{}, fmt.Errorf("invalid PostgreSQL runtime policy: %w", err)
	}
	for key, target := range map[string]*time.Duration{
		"HTTP_CONNECT_TIMEOUT_SECONDS":         &transport.ConnectTimeout,
		"HTTP_KEEP_ALIVE_SECONDS":              &transport.KeepAlive,
		"HTTP_TLS_HANDSHAKE_TIMEOUT_SECONDS":   &transport.TLSHandshakeTimeout,
		"HTTP_RESPONSE_HEADER_TIMEOUT_SECONDS": &transport.ResponseHeaderTimeout,
		"HTTP_EXPECT_CONTINUE_TIMEOUT_SECONDS": &transport.ExpectContinueTimeout,
		"HTTP_IDLE_CONNECTION_TIMEOUT_SECONDS": &transport.IdleConnectionTimeout,
	} {
		if *target, err = secondsEnv(key, *target); err != nil {
			return runtimePolicies{}, err
		}
	}
	if transport.MaxIdleConnections, err = positiveIntEnv(
		"HTTP_MAX_IDLE_CONNECTIONS", transport.MaxIdleConnections,
	); err != nil {
		return runtimePolicies{}, err
	}
	if transport.MaxIdleConnectionsHost, err = positiveIntEnv(
		"HTTP_MAX_IDLE_CONNECTIONS_PER_HOST", transport.MaxIdleConnectionsHost,
	); err != nil {
		return runtimePolicies{}, err
	}
	if transport.MaxConnectionsHost, err = positiveIntEnv(
		"HTTP_MAX_CONNECTIONS_PER_HOST", transport.MaxConnectionsHost,
	); err != nil {
		return runtimePolicies{}, err
	}
	if err := transport.Validate(); err != nil {
		return runtimePolicies{}, fmt.Errorf("invalid HTTP transport policy: %w", err)
	}
	maxConcurrentRequests, err := positiveIntEnv(
		"HTTP_MAX_CONCURRENT_REQUESTS", maxHTTPConcurrentRequests,
	)
	if err != nil {
		return runtimePolicies{}, err
	}
	if maxConcurrentRequests > maxHTTPConcurrentRequests {
		return runtimePolicies{}, fmt.Errorf(
			"HTTP_MAX_CONCURRENT_REQUESTS exceeds its operational upper bound",
		)
	}
	policies := runtimePolicies{
		database:              database,
		httpTransport:         transport,
		maxConcurrentRequests: maxConcurrentRequests,
	}
	for _, entry := range []struct {
		key      string
		target   *time.Duration
		fallback time.Duration
		maximum  time.Duration
	}{
		{"HTTP_REQUEST_TIMEOUT_SECONDS", &policies.requestTimeout, 10 * time.Second, 15 * time.Second},
		{"EXTERNAL_FINALIZATION_TIMEOUT_SECONDS", &policies.externalFinalize, 3 * time.Second, 30 * time.Second},
		{"GOOGLE_OIDC_TIMEOUT_SECONDS", &policies.googleOIDCTimeout, 15 * time.Second, time.Minute},
		{"SHOPIFY_UCP_TIMEOUT_SECONDS", &policies.shopifyTimeout, 15 * time.Second, time.Minute},
		{"PAYPAL_TIMEOUT_SECONDS", &policies.paypalTimeout, 30 * time.Second, time.Minute},
		{"MANAGED_MODEL_TIMEOUT_SECONDS", &policies.managedModelTimeout, 180 * time.Second, 10 * time.Minute},
		{"EVM_RPC_HTTP_TIMEOUT_SECONDS", &policies.evmHTTPClientTimeout, 15 * time.Second, time.Minute},
	} {
		if *entry.target, err = secondsEnv(entry.key, entry.fallback); err != nil {
			return runtimePolicies{}, err
		}
		if *entry.target > entry.maximum {
			return runtimePolicies{}, fmt.Errorf("%s exceeds its operational upper bound", entry.key)
		}
	}
	return policies, nil
}
func (c runtimeConfig) validateSettlement(appEnvironment string) error {
	validEnvironment := map[string]bool{
		"DISABLED": true, "LOCAL": true, "GIWA_TESTNET": true, "REAL": true,
	}
	if !validEnvironment[c.settlementEnvironment] {
		return fmt.Errorf("SETTLEMENT_ENV must be DISABLED, LOCAL, GIWA_TESTNET, or REAL")
	}
	if !c.settlementEnabled {
		return nil
	}
	if err := c.settlementReconcilePolicy.Validate(); err != nil {
		return fmt.Errorf("invalid settlement reconcile policy: %w", err)
	}
	if c.settlementEnvironment == "DISABLED" {
		return fmt.Errorf("PHASE5_SETTLEMENT_ENABLED requires SETTLEMENT_ENV")
	}
	if appEnvironment == "production" && len(c.settlementOperatorEmails) == 0 {
		return fmt.Errorf("production Phase 5 requires PHASE5_OPERATOR_EMAILS")
	}
	if appEnvironment == "production" && c.settlementEnvironment == "LOCAL" {
		return fmt.Errorf("production cannot use SETTLEMENT_ENV=LOCAL")
	}
	if c.settlementEnvironment == "REAL" {
		if c.kycAssuranceMode == "MOCK_DOJANG_VERIFIED" ||
			c.settlementConfig.FaucetAddress != "" {
			return fmt.Errorf("REAL settlement forbids MockDojang and Faucet")
		}
		return fmt.Errorf("REAL settlement is gated until After MVP approval")
	}
	if (c.piiEncryptionKey == "" && c.piiEncryptionKeys == "") ||
		c.piiKeyVersion == "" {
		return fmt.Errorf(
			"Phase 5 shipping profiles require PII_ENCRYPTION_KEY or " +
				"PII_ENCRYPTION_KEYS plus PII_KEY_VERSION",
		)
	}
	if c.kycAssuranceMode == "DOJANG_VERIFIED_ADDRESS" &&
		(strings.EqualFold(
			c.dojangConfig.AttesterID,
			"0xaa92f8c143657dde575de430aecaea6ca91f2e6072339b16932d426895d8d678",
		) || strings.EqualFold(
			c.dojangConfig.AttesterAddress,
			"0x63CCe2b569A7bC35895ee24306c1512fefc06121",
		)) {
		return fmt.Errorf("DOJANG_TEST_FAUCET attester cannot satisfy KYC")
	}
	if c.kycAssuranceMode != "MOCK_DOJANG_VERIFIED" {
		return fmt.Errorf(
			"the current Wallet/KYC redesign requires KYC_ASSURANCE_MODE=MOCK_DOJANG_VERIFIED; the live Dojang adapter is a later replacement",
		)
	}
	// MVP runs MockDojang in the deployed TestPhase process. The case,
	// operation, credential, evidence and audit lifecycle is production-shaped;
	// externalEffect remains SIMULATED and the UI labels it explicitly.
	if c.privateRPCURL == "" || c.settlementConfig.RPCURL == "" ||
		c.quoteSignerKeyFile == "" || c.finalizerKeyFile == "" ||
		c.refunderKeyFile == "" || c.manifestPath == "" {
		return fmt.Errorf("test settlement requires RPC URLs and signer key files")
	}
	claimAmount, ok := new(big.Int).SetString(c.settlementConfig.ClaimAmount, 10)
	if !ok || claimAmount.Sign() <= 0 {
		return fmt.Errorf("FAUCET_CLAIM_AMOUNT must be a positive base-unit integer")
	}
	for name, value := range map[string]string{
		"TVITUSD_ADDRESS":            c.settlementConfig.TokenAddress,
		"FAUCET_ADDRESS":             c.settlementConfig.FaucetAddress,
		"SETTLEMENT_ADDRESS":         c.settlementConfig.SettlementAddress,
		"TEST_FEE":                   c.settlementConfig.FeeRecipient,
		"AMAZON_TEST_PRINCIPAL":      c.merchantPrincipals["AMAZON_US"],
		"WALMART_TEST_PRINCIPAL":     c.merchantPrincipals["WALMART_US"],
		"SHOPIFY_TEST_PRINCIPAL":     c.merchantPrincipals["SHOPIFY_UCP"],
		"GENERIC_WEB_TEST_PRINCIPAL": c.merchantPrincipals["GENERIC_WEB_USD"],
		"REFUNDER_ADDRESS":           c.refunderAddress,
		"PAUSER_ADDRESS":             c.pauserAddress,
	} {
		if !common.IsHexAddress(value) {
			return fmt.Errorf("%s must be an EVM address", name)
		}
	}
	unifiedPrincipal := c.merchantPrincipals[settlementdomain.GenericWebUSDSettlementPathID]
	for merchantID, principal := range c.merchantPrincipals {
		if !strings.EqualFold(principal, unifiedPrincipal) {
			return fmt.Errorf(
				"%s principal must match GENERIC_WEB_TEST_PRINCIPAL in TestPhase",
				merchantID,
			)
		}
		if c.merchantRegistryVersions[merchantID] == 0 {
			return fmt.Errorf("%s registry version must be positive", merchantID)
		}
	}
	return nil
}

func parseUintEnv(key string, fallback uint64) (uint64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an unsigned integer", key)
	}
	return value, nil
}

func splitNonEmpty(value string) []string {
	values := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		if normalized := strings.TrimSpace(item); normalized != "" {
			values = append(values, normalized)
		}
	}
	return values
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func positiveIntEnv(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func nonNegativeIntEnv(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return value, nil
}

func secondsEnv(key string, fallback time.Duration) (time.Duration, error) {
	seconds, err := positiveIntEnv(key, int(fallback/time.Second))
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds) * time.Second, nil
}

// managedRunnerEnabled resolves ADR-0032's runner switch, where the model
// credential is the real capability. An environment that was never given one
// cannot run the managed pipeline no matter what it asks for, and refusing to
// boot over a variable the operator never set would take down every deployment
// that only wants the external agent — which is exactly how CI broke.
//
// So an unset switch follows the secret. An explicit value is still obeyed
// verbatim, and that is what keeps the missing-secret startup error reachable:
// "MANAGED_RUNNER_ENABLED=true" with no secret is an operator asserting
// something the process cannot honour, and it must fail loudly rather than
// serve a runner that silently never runs.
func managedRunnerEnabled(setting, provider, apiKey string) bool {
	switch strings.TrimSpace(setting) {
	case "true":
		return true
	case "false":
		return false
	}
	// The stub provider carries its own credential-free capability, and refuses
	// to build outside development, so following it here cannot reach production.
	return provider == "stub" || apiKey != ""
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
