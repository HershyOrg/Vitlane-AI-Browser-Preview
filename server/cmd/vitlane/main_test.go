package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	accountdojang "github.com/vitlane/vitlane/server/internal/account/infra/dojang"
	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
)

func TestProductionConfigurationGuards(t *testing.T) {
	t.Run("AgencyOrder hard cutover is required", func(t *testing.T) {
		setProductionEnvironment(t)
		t.Setenv("AGENCY_ORDER_ENABLED", "false")
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "AGENCY_ORDER_ENABLED") {
			t.Fatalf("expected AgencyOrder hard-cutover guard, got %v", err)
		}
	})

	t.Run("development auth is forbidden", func(t *testing.T) {
		setProductionEnvironment(t)
		t.Setenv("ALLOW_DEV_AUTH", "true")
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "ALLOW_DEV_AUTH") {
			t.Fatalf("expected development auth guard, got %v", err)
		}
	})

	t.Run("development review user is forbidden", func(t *testing.T) {
		setProductionEnvironment(t)
		t.Setenv("DEV_AUTH_DEFAULT_USER_ID", "e5000000-0000-4000-8000-000000000001")
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "DEV_AUTH_DEFAULT_USER_ID") {
			t.Fatalf("expected development review user guard, got %v", err)
		}
	})

	t.Run("Catalog Research is required in production", func(t *testing.T) {
		setProductionEnvironment(t)
		t.Setenv("CURATION_CATALOG_RESEARCH_ENABLED", "false")
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "CURATION_CATALOG_RESEARCH_ENABLED") {
			t.Fatalf("expected Catalog Research requirement, got %v", err)
		}
	})

	t.Run("Catalog Research stub is forbidden in production", func(t *testing.T) {
		setProductionEnvironment(t)
		t.Setenv("RESEARCH_CATALOG_PROVIDER", "stub")
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "catalog stub") {
			t.Fatalf("expected production catalog stub guard, got %v", err)
		}
	})

	t.Run("HTTPS is required", func(t *testing.T) {
		setProductionEnvironment(t)
		t.Setenv("PUBLIC_BASE_URL", "http://vitlane.example")
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "HTTPS") {
			t.Fatalf("expected HTTPS guard, got %v", err)
		}
	})

	t.Run("Google credentials are required", func(t *testing.T) {
		setProductionEnvironment(t)
		t.Setenv("GOOGLE_OIDC_CLIENT_ID", "")
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "Google") {
			t.Fatalf("expected Google credential guard, got %v", err)
		}
	})

	t.Run("Account rate limit secret is required", func(t *testing.T) {
		setProductionEnvironment(t)
		t.Setenv("ACCOUNT_RATE_LIMIT_SECRET", "local-account-rate-limit-secret")
		if _, err := loadConfig(); err == nil ||
			!strings.Contains(err.Error(), "ACCOUNT_RATE_LIMIT_SECRET") {
			t.Fatalf("expected Account rate limit secret guard, got %v", err)
		}
	})

	t.Run("valid production configuration passes", func(t *testing.T) {
		setProductionEnvironment(t)
		if _, err := loadConfig(); err != nil {
			t.Fatalf("valid production config: %v", err)
		}
	})
}

func TestPublicBaseURLCannotContainPath(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("PUBLIC_BASE_URL", "http://localhost:8080/not-an-origin")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("expected origin-only validation, got %v", err)
	}
}

func TestDevelopmentReviewUserRequiresDevelopmentAuth(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("PUBLIC_BASE_URL", "http://localhost:8080")
	t.Setenv("ALLOW_DEV_AUTH", "false")
	t.Setenv("DEV_AUTH_DEFAULT_USER_ID", "e5000000-0000-4000-8000-000000000001")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "requires ALLOW_DEV_AUTH") {
		t.Fatalf("expected development auth dependency, got %v", err)
	}
}

func TestBrowserProductSearchRequiresKoreanCatalogAndPairedSafeBridge(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("PUBLIC_BASE_URL", "http://localhost:8080")
	t.Setenv("BROWSER_PRODUCT_SEARCH_ENABLED", "true")
	t.Setenv("KOREAN_PRODUCT_SEARCH_ENABLED", "false")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "requires KOREAN_PRODUCT_SEARCH_ENABLED") {
		t.Fatalf("expected Korean catalog dependency, got %v", err)
	}
	t.Setenv("KOREAN_PRODUCT_SEARCH_ENABLED", "true")
	t.Setenv("BRIDGE_TOKEN", "short")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "BRIDGE_TOKEN") {
		t.Fatalf("expected bridge pairing guard, got %v", err)
	}
	t.Setenv("BRIDGE_TOKEN", "0123456789abcdef0123456789abcdef")
	t.Setenv("BROWSER_BRIDGE_URL", "http://merchant.example")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "BROWSER_BRIDGE_URL") {
		t.Fatalf("expected non-loopback HTTP guard, got %v", err)
	}
	t.Setenv("BROWSER_BRIDGE_URL", "http://127.0.0.1:8787")
	config, err := loadConfig()
	if err != nil || !config.browserProductSearchEnabled {
		t.Fatalf("valid local browser discovery config: %+v err=%v", config, err)
	}
}

func TestCatalogResearchRequiresProviderAndBoundedRate(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("CURATION_CATALOG_RESEARCH_ENABLED", "true")
	t.Setenv("SHOPIFY_UCP_ENABLED", "false")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "requires Shopify") {
		t.Fatalf("expected research provider dependency, got %v", err)
	}
	t.Setenv("RESEARCH_CATALOG_PROVIDER", "stub")
	if _, err := loadConfig(); err != nil {
		t.Fatalf("the deterministic test catalog must expose the Phase 8 read surface: %v", err)
	}
	t.Setenv("RESEARCH_CATALOG_PROVIDER", "")
	t.Setenv("SHOPIFY_UCP_ENABLED", "true")
	t.Setenv("CURATION_CATALOG_RESEARCH_CALLS_PER_MINUTE", "61")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "CALLS_PER_MINUTE") {
		t.Fatalf("expected bounded live rate, got %v", err)
	}
	t.Setenv("CURATION_CATALOG_RESEARCH_CALLS_PER_MINUTE", "30")
	t.Setenv("CURATION_CATALOG_RESEARCH_MAX_CONCURRENCY", "17")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "MAX_CONCURRENCY") {
		t.Fatalf("expected measured concurrency ceiling, got %v", err)
	}
	t.Setenv("CURATION_CATALOG_RESEARCH_MAX_CONCURRENCY", "16")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "SHOPIFY_DEV_CLIENT_ID") {
		t.Fatalf("expected token-tier Catalog credentials, got %v", err)
	}
}

// ADR-0032. The runner used to default on, so any environment without a model
// secret refused to boot — CI included. The credential decides when nobody
// says otherwise, but an operator who explicitly asks for a runner they cannot
// credential must still get a startup failure instead of a silent no-op.
func TestManagedRunnerFollowsItsCredentialUnlessToldOtherwise(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		setting  string
		provider string
		apiKey   string
		want     bool
	}{
		{"unset without a secret stays off", "", "openai", "", false},
		{"unset with a secret comes up", "", "openai", "sk-test", true},
		{"unset with the stub needs no secret", "", "stub", "", true},
		{"an explicit request is honoured", "true", "openai", "sk-test", true},
		{"an explicit refusal wins over a secret", "false", "openai", "sk-test", false},
		// Reaching this combination is the point: loadConfig turns it into
		// "MANAGED_RUNNER_ENABLED requires MANAGED_OPENAI_API_SECRET".
		{"an impossible request still reaches the error", "true", "openai", "", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := managedRunnerEnabled(
				testCase.setting, testCase.provider, testCase.apiKey,
			)
			if got != testCase.want {
				t.Fatalf(
					"managedRunnerEnabled(%q, %q, secret=%t) = %t, want %t",
					testCase.setting, testCase.provider,
					testCase.apiKey != "", got, testCase.want,
				)
			}
		})
	}
}

func TestSettlementSettlementConfigurationFailsClosed(t *testing.T) {
	base := runtimeConfig{
		settlementEnabled: true, settlementEnvironment: "GIWA_TESTNET",
		settlementReconcilePolicy: settlementapp.DefaultReconcilePolicy(),
		settlementOperatorEmails:  []string{"operator@example.com"},
		privateRPCURL:             "https://private-rpc.example", quoteSignerKeyFile: "/run/quote.key",
		finalizerKeyFile: "/run/finalizer.key", refunderKeyFile: "/run/refunder.key",
		manifestPath:     "/run/manifest.json",
		kycAssuranceMode: "MOCK_DOJANG_VERIFIED",
		piiEncryptionKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		piiKeyVersion:    "test-v1",
		dojangConfig: accountdojang.Config{
			Level:           accountdomain.AssuranceDojangVerifiedAddress,
			ScrollAddress:   "0xd5077b67dcb56caC8b270C7788FC3E6ee03F17B9",
			EASAddress:      "0x4200000000000000000000000000000000000021",
			AttesterID:      "0xd99b42e778498aa3c9c1f6a012359130252780511687a35982e8e52735453034",
			AttesterAddress: "0x4097bF3Cb731AEB3E501b910B33B2aF9Fa68E388",
			SchemaUID:       "0x072d75e18b2be4f89a13a7147240477481c4b526d5795802acba59046b426e08",
		},
		refunderAddress: "0x1111111111111111111111111111111111111111",
		pauserAddress:   "0x2222222222222222222222222222222222222222",
		merchantPrincipals: map[string]string{
			"AMAZON_US":       "0x3333333333333333333333333333333333333333",
			"WALMART_US":      "0x3333333333333333333333333333333333333333",
			"SHOPIFY_UCP":     "0x3333333333333333333333333333333333333333",
			"GENERIC_WEB_USD": "0x3333333333333333333333333333333333333333",
		},
		merchantRegistryVersions: map[string]uint64{
			"AMAZON_US":       2,
			"WALMART_US":      2,
			"SHOPIFY_UCP":     2,
			"GENERIC_WEB_USD": 1,
		},
		settlementConfig: settlementdomain.SettlementConfig{
			RPCURL:            "https://public-rpc.example",
			ClaimAmount:       "1000000000",
			TokenAddress:      "0x6666666666666666666666666666666666666666",
			FaucetAddress:     "0x7777777777777777777777777777777777777777",
			SettlementAddress: "0x8888888888888888888888888888888888888888",
			FeeRecipient:      "0x9999999999999999999999999999999999999999",
		},
	}

	invalidReconcilePolicy := base
	invalidReconcilePolicy.settlementReconcilePolicy.EventOverlapBlocks =
		invalidReconcilePolicy.settlementReconcilePolicy.EventChunkSize
	if err := invalidReconcilePolicy.validateSettlement("production"); err == nil ||
		!strings.Contains(err.Error(), "event overlap") {
		t.Fatalf("invalid reconcile window must fail closed, got %v", err)
	}
	if err := base.validateSettlement("production"); err != nil {
		t.Fatalf("reviewed GIWA test configuration should pass static validation: %v", err)
	}

	divergentPrincipal := base
	divergentPrincipal.merchantPrincipals = map[string]string{
		"AMAZON_US":       "0x4444444444444444444444444444444444444444",
		"WALMART_US":      "0x3333333333333333333333333333333333333333",
		"SHOPIFY_UCP":     "0x3333333333333333333333333333333333333333",
		"GENERIC_WEB_USD": "0x3333333333333333333333333333333333333333",
	}
	if err := divergentPrincipal.validateSettlement("production"); err == nil ||
		!strings.Contains(err.Error(), "must match") {
		t.Fatalf("TestPhase merchant principals must be unified, got %v", err)
	}

	zeroRegistryVersion := base
	zeroRegistryVersion.merchantRegistryVersions = map[string]uint64{
		"AMAZON_US":       0,
		"WALMART_US":      2,
		"SHOPIFY_UCP":     2,
		"GENERIC_WEB_USD": 1,
	}
	if err := zeroRegistryVersion.validateSettlement("production"); err == nil ||
		!strings.Contains(err.Error(), "registry version") {
		t.Fatalf("zero merchant registry version must fail closed, got %v", err)
	}

	if err := base.validateSettlement("production"); err != nil {
		t.Fatalf("deployed TestPhase may use explicit MockDojang with the production KYC lifecycle: %v", err)
	}

	faucetAsKYC := base
	faucetAsKYC.kycAssuranceMode = "DOJANG_VERIFIED_ADDRESS"
	faucetAsKYC.dojangConfig.AttesterID =
		"0xaa92f8c143657dde575de430aecaea6ca91f2e6072339b16932d426895d8d678"
	if err := faucetAsKYC.validateSettlement("production"); err == nil ||
		!strings.Contains(err.Error(), "FAUCET") {
		t.Fatalf("Faucet attester must not be accepted as KYC, got %v", err)
	}

	liveDojang := base
	liveDojang.kycAssuranceMode = "DOJANG_VERIFIED_ADDRESS"
	if err := liveDojang.validateSettlement("production"); err == nil ||
		!strings.Contains(err.Error(), "later replacement") {
		t.Fatalf("live Dojang must remain a later adapter replacement, got %v", err)
	}

	real := base
	real.settlementEnvironment = "REAL"
	if err := real.validateSettlement("production"); err == nil || !strings.Contains(err.Error(), "forbids") {
		t.Fatalf("REAL mode with test assets must fail closed, got %v", err)
	}

	missingRole := base
	missingRole.refunderAddress = ""
	if err := missingRole.validateSettlement("production"); err == nil || !strings.Contains(err.Error(), "REFUNDER_ADDRESS") {
		t.Fatalf("missing public role address must fail closed, got %v", err)
	}
}

func TestTrustedProxyCIDRs(t *testing.T) {
	t.Run("valid prefixes are normalized", func(t *testing.T) {
		t.Setenv("APP_ENV", "development")
		t.Setenv("TRUSTED_PROXY_CIDRS", "172.30.0.9/24, 2001:db8::1/64")
		config, err := loadConfig()
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if len(config.trustedProxyPrefixes) != 2 {
			t.Fatalf("trusted proxy prefixes=%v", config.trustedProxyPrefixes)
		}
		if got := config.trustedProxyPrefixes[0].String(); got != "172.30.0.0/24" {
			t.Fatalf("first trusted proxy prefix=%q", got)
		}
		if got := config.trustedProxyPrefixes[1].String(); got != "2001:db8::/64" {
			t.Fatalf("second trusted proxy prefix=%q", got)
		}
	})

	t.Run("invalid prefix is rejected", func(t *testing.T) {
		t.Setenv("APP_ENV", "development")
		t.Setenv("TRUSTED_PROXY_CIDRS", "not-a-cidr")
		if _, err := loadConfig(); err == nil ||
			!strings.Contains(err.Error(), "TRUSTED_PROXY_CIDRS") {
			t.Fatalf("expected trusted proxy CIDR guard, got %v", err)
		}
	})
}

func TestMarketingAdminConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("MARKETING_ADMIN_EMAILS", " first@example.com,second@example.com ")
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.marketingAdminEmails) != 2 || config.marketingAdminEmails[0] != "first@example.com" {
		t.Fatalf("marketing admin emails=%v", config.marketingAdminEmails)
	}
}

func TestMarketingFrontendConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("MARKETING_WEB_DIR", "/app/marketing")
	t.Setenv("MARKETING_BASE_URL", "https://vitlane.com/")
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.marketingHost != "vitlane.com" || config.marketingWebDir != "/app/marketing" {
		t.Fatalf("marketing frontend config=%+v", config)
	}
}

func TestFrontendHandlerRoutesByHost(t *testing.T) {
	productDir := t.TempDir()
	marketingDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(productDir, "index.html"), []byte("product"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(marketingDir, "index.html"), []byte("marketing"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := frontendHandler(spaHandler(productDir), staticSiteHandler(marketingDir), "vitlane.com")

	for _, test := range []struct{ host, want string }{
		{host: "vitlane.com", want: "marketing"},
		{host: "app.vitlane.com", want: "product"},
	} {
		request := httptest.NewRequest("GET", "https://"+test.host+"/", nil)
		request.Host = test.host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 || !strings.Contains(response.Body.String(), test.want) {
			t.Fatalf("host=%s status=%d body=%q", test.host, response.Code, response.Body.String())
		}
	}
}

func TestMarketingStaticSiteServesPolicyIndexAndRejectsMissingFiles(t *testing.T) {
	siteDir := t.TempDir()
	privacyDir := filepath.Join(siteDir, "privacy")
	if err := os.MkdirAll(privacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privacyDir, "index.html"), []byte("privacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := staticSiteHandler(siteDir)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/privacy/", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "privacy") {
		t.Fatalf("policy status=%d body=%q", response.Code, response.Body.String())
	}
	if policy := response.Header().Get("Content-Security-Policy"); policy !=
		"default-src 'self'; script-src 'self'; connect-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' https: data:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'" {
		t.Fatalf("content security policy=%q", policy)
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest("GET", "/missing.js", nil))
	if missing.Code != 404 {
		t.Fatalf("missing status=%d", missing.Code)
	}
}

func TestMarketingEnglishPagesMoveKoreanVisitorsOnArrivalOnly(t *testing.T) {
	siteDir := t.TempDir()
	for page, body := range map[string]string{
		"index.html":            "english home",
		"ko/index.html":         "korean home",
		"privacy/index.html":    "english privacy",
		"ko/privacy/index.html": "korean privacy",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(siteDir, page)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(siteDir, page), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	handler := staticSiteHandler(siteDir)
	korean := "ko-KR,ko;q=0.9,en-US;q=0.8"
	for _, test := range []struct {
		name, method, path, language, fetchSite string
		cookies                                 map[string]string
		wantLocation, wantBody                  string
	}{
		{name: "Korean browser from search", path: "/", language: korean, fetchSite: "cross-site", wantLocation: "/ko/"},
		{name: "Korean browser typing the address", path: "/", language: korean, fetchSite: "none", wantLocation: "/ko/"},
		{name: "query survives", path: "/privacy/?from=consent", language: korean, wantLocation: "/ko/privacy/?from=consent"},
		{name: "HEAD moves too", method: http.MethodHead, path: "/", language: korean, wantLocation: "/ko/"},
		{name: "English page last seen in Korean", path: "/", language: "en-US", cookies: map[string]string{"vt_locale_seen": "ko-KR"}, wantLocation: "/ko/"},
		{name: "crawler without language", path: "/", wantBody: "english home"},
		{name: "English browser", path: "/", language: "en-US,en;q=0.9,ko;q=0.5", wantBody: "english home"},
		{name: "language link inside the site", path: "/", language: korean, fetchSite: "same-origin", wantBody: "english home"},
		{name: "App link to a policy", path: "/privacy/", language: korean, fetchSite: "same-site", wantBody: "english privacy"},
		{name: "explicit English choice", path: "/", language: korean, cookies: map[string]string{"vt_locale_choice": "en-US", "vt_locale_seen": "ko-KR"}, wantBody: "english home"},
		{name: "legacy cookie is ignored", path: "/", language: "en-US", cookies: map[string]string{"vt_locale": "ko-KR"}, wantBody: "english home"},
		{name: "Korean page never moves", path: "/ko/", language: "en-US", cookies: map[string]string{"vt_locale_choice": "en-US"}, wantBody: "korean home"},
	} {
		t.Run(test.name, func(t *testing.T) {
			method := test.method
			if method == "" {
				method = http.MethodGet
			}
			request := httptest.NewRequest(method, "https://vitlane.com"+test.path, nil)
			if test.language != "" {
				request.Header.Set("Accept-Language", test.language)
			}
			if test.fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			}
			for name, value := range test.cookies {
				request.AddCookie(&http.Cookie{Name: name, Value: value})
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			adaptive := !strings.HasPrefix(test.path, "/ko/")
			if vary := response.Header().Get("Vary"); adaptive != (vary == "Accept-Language, Cookie, Sec-Fetch-Site") {
				t.Fatalf("vary=%q adaptive=%v", vary, adaptive)
			}
			if test.wantLocation != "" {
				if response.Code != http.StatusFound || response.Header().Get("Location") != test.wantLocation || response.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("status=%d location=%q cache=%q", response.Code, response.Header().Get("Location"), response.Header().Get("Cache-Control"))
				}
				return
			}
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("status=%d location=%q body=%q", response.Code, response.Header().Get("Location"), response.Body.String())
			}
			if adaptive && response.Header().Get("Cache-Control") != "no-cache" {
				t.Fatalf("adaptive English page cache=%q", response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestAppResponsesAreNotIndexedButMarketingIs(t *testing.T) {
	productDir := t.TempDir()
	marketingDir := t.TempDir()
	for dir, files := range map[string]map[string]string{
		productDir:   {"index.html": "product", "robots.txt": "User-agent: *\nAllow: /\n"},
		marketingDir: {"index.html": "marketing"},
	} {
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	handler := frontendHandler(spaHandler(productDir), staticSiteHandler(marketingDir), "vitlane.com")
	for _, test := range []struct {
		host, path string
		noindex    bool
	}{
		{host: "app.vitlane.com", path: "/login", noindex: true},
		{host: "app.vitlane.com", path: "/robots.txt", noindex: true},
		{host: "vitlane.com", path: "/", noindex: false},
	} {
		request := httptest.NewRequest(http.MethodGet, "https://"+test.host+test.path, nil)
		request.Host = test.host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || (response.Header().Get("X-Robots-Tag") == "noindex") != test.noindex {
			t.Fatalf("%s%s status=%d X-Robots-Tag=%q", test.host, test.path, response.Code, response.Header().Get("X-Robots-Tag"))
		}
	}
}

func TestSPAContentSecurityPolicyAllowsSelfHTTPSAndDataImages(t *testing.T) {
	webDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(webDir, "index.html"),
		[]byte("<!doctype html><title>Vitlane</title>"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/plans/plan-1/research", nil)
	response := httptest.NewRecorder()

	spaHandler(webDir).ServeHTTP(response, request)

	if response.Code != 200 {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if policy := response.Header().Get("Content-Security-Policy"); policy !=
		"img-src 'self' https: data:; object-src 'none'; base-uri 'self'" {
		t.Fatalf("content security policy=%q", policy)
	}
}

func TestSPAHandlerReturnsNotFoundForUnknownAPIPath(t *testing.T) {
	webDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(webDir, "index.html"),
		[]byte("<!doctype html><title>Vitlane</title>"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	spaHandler(webDir).ServeHTTP(
		response,
		httptest.NewRequest("GET", "/api/v1/admin/marketing-inquiries", nil),
	)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestSPAHandlerDoesNotExposeRemovedBridgeInstallerOrDirectoryListings(
	t *testing.T,
) {
	webDir := t.TempDir()
	assetDir := filepath.Join(webDir, "assets")
	if err := os.MkdirAll(assetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(webDir, "index.html"),
		[]byte("<!doctype html><title>Product SPA</title>"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(assetDir, "private-name.js"),
		[]byte("asset"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	handler := spaHandler(webDir)
	for _, test := range []struct {
		path        string
		wantContent string
	}{
		{
			path:        "/codex-bridge/v1/",
			wantContent: "Product SPA",
		},
		{
			path:        "/plans/plan-1/research",
			wantContent: "Product SPA",
		},
		{
			path:        "/assets/",
			wantContent: "Product SPA",
		},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequest("GET", test.path, nil),
		)
		if response.Code != 200 ||
			!strings.Contains(response.Body.String(), test.wantContent) {
			t.Fatalf(
				"path=%s status=%d body=%q",
				test.path,
				response.Code,
				response.Body.String(),
			)
		}
		if strings.Contains(response.Body.String(), "private-name.js") {
			t.Fatalf("path=%s exposed a directory listing", test.path)
		}
	}

	removedInstallerResponse := httptest.NewRecorder()
	handler.ServeHTTP(
		removedInstallerResponse,
		httptest.NewRequest("GET", "/codex-bridge/v1/install.mjs", nil),
	)
	if removedInstallerResponse.Code != http.StatusOK ||
		!strings.Contains(
			removedInstallerResponse.Body.String(),
			"Product SPA",
		) {
		t.Fatalf(
			"removed installer status=%d body=%q",
			removedInstallerResponse.Code,
			removedInstallerResponse.Body.String(),
		)
	}
}

func setProductionEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "production")
	t.Setenv("PUBLIC_BASE_URL", "https://vitlane.example")
	t.Setenv("ALLOW_DEV_AUTH", "false")
	t.Setenv("GOOGLE_OIDC_CLIENT_ID", "client")
	t.Setenv("GOOGLE_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("MARKETING_ADMIN_EMAILS", "operator@example.com")
	t.Setenv("ACCOUNT_RATE_LIMIT_SECRET", "test-production-account-rate-limit-secret")
	t.Setenv("CURATION_CATALOG_RESEARCH_ENABLED", "true")
	t.Setenv("SHOPIFY_UCP_ENABLED", "true")
	t.Setenv("SHOPIFY_UCP_AGENT_PROFILE_URL", "https://vitlane.example/.well-known/ucp-agent.json")
	t.Setenv("AGENCY_ORDER_ENABLED", "true")
	t.Setenv("AGENCY_ORDER_CHECKOUT_PROVIDER", "shopify")
	t.Setenv("SHOPIFY_DEV_CLIENT_ID", "reviewed-client")
	t.Setenv("SHOPIFY_DEV_CLIENT_SECRET", "reviewed-secret")
	t.Setenv("PHASE5_SETTLEMENT_ENABLED", "true")
	t.Setenv("PHASE5_OPERATOR_EMAILS", "operator@example.com")
	t.Setenv("SETTLEMENT_ENV", "GIWA_TESTNET")
	t.Setenv("GIWA_RPC_URL", "https://rpc.internal.example")
	t.Setenv("GIWA_PUBLIC_RPC_URL", "https://rpc.example")
	t.Setenv("ONCHAIN_MANIFEST_PATH", "/run/vitlane/phase5/manifest.json")
	t.Setenv("KYC_ASSURANCE_MODE", "MOCK_DOJANG_VERIFIED")
	t.Setenv("PII_ENCRYPTION_KEY", "test-encryption-key")
	t.Setenv("PII_KEY_VERSION", "v1")
	t.Setenv("QUOTE_SIGNER_KEY_FILE", "/run/vitlane/phase5/quote.key")
	t.Setenv("FINALIZER_KEY_FILE", "/run/vitlane/phase5/finalizer.key")
	t.Setenv("REFUNDER_KEY_FILE", "/run/vitlane/phase5/refunder.key")
	address := "0x1000000000000000000000000000000000000001"
	for _, key := range []string{
		"TVITUSD_ADDRESS", "FAUCET_ADDRESS", "SETTLEMENT_ADDRESS", "TEST_FEE",
		"AMAZON_TEST_PRINCIPAL", "WALMART_TEST_PRINCIPAL", "SHOPIFY_TEST_PRINCIPAL",
		"GENERIC_WEB_TEST_PRINCIPAL", "REFUNDER_ADDRESS", "PAUSER_ADDRESS",
	} {
		t.Setenv(key, address)
	}
}
