package main

import (
	"os"
	"strings"
	"testing"
)

func clearPayPalEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PAYPAL_SANDBOX_ENABLED",
		"LIVE_AGENCY_ORDER_ISSUE_ENABLED", "PAYPAL_LIVE_CAPTURE_ENABLED",
		"PAYPAL_LIVE_ENABLED", "PAYPAL_SANDBOX_CLIENT_ID",
		"PAYPAL_SANDBOX_CLIENT_SECRET", "PAYPAL_SANDBOX_WEBHOOK_ID",
		"PAYPAL_LIVE_CLIENT_ID", "PAYPAL_LIVE_CLIENT_SECRET",
		"PAYPAL_LIVE_WEBHOOK_ID", "PAYPAL_LIVE_MERCHANT_ID", "PAYPAL_LIVE_EMAIL",
		"MANUAL_MERCHANT_EFFECT_ENABLED",
	} {
		t.Setenv(key, "")
	}
}

func TestPayPalLiveSwitchDefaultsRemainFailClosed(t *testing.T) {
	clearPayPalEnvironment(t)
	t.Setenv("APP_ENV", "development")
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.liveAgencyOrderIssueEnabled || config.paypalLiveCaptureEnabled ||
		config.manualMerchantEffectEnabled {
		t.Fatalf("unexpected effect-capable defaults: %+v", config)
	}
}

func TestPayPalLiveSwitchRejectsBroadOrOutOfOrderActivation(t *testing.T) {
	t.Run("retired broad switch", func(t *testing.T) {
		clearPayPalEnvironment(t)
		t.Setenv("APP_ENV", "development")
		t.Setenv("PAYPAL_LIVE_ENABLED", "true")
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "retired") {
			t.Fatalf("expected retired switch error, got %v", err)
		}
	})

	t.Run("capture before issue", func(t *testing.T) {
		clearPayPalEnvironment(t)
		t.Setenv("APP_ENV", "development")
		t.Setenv("PAYPAL_LIVE_CLIENT_ID", "live-client")
		t.Setenv("PAYPAL_LIVE_CLIENT_SECRET", "live-secret")
		t.Setenv("PAYPAL_LIVE_WEBHOOK_ID", "WH-LIVE")
		t.Setenv("PAYPAL_LIVE_MERCHANT_ID", "MERCHANT-LIVE")
		t.Setenv("PAYPAL_LIVE_CAPTURE_ENABLED", "true")
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "issue gate") {
			t.Fatalf("expected ordered activation error, got %v", err)
		}
	})

	t.Run("merchant effect before capture", func(t *testing.T) {
		clearPayPalEnvironment(t)
		t.Setenv("APP_ENV", "development")
		t.Setenv("MANUAL_MERCHANT_EFFECT_ENABLED", "true")
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "capture gate") {
			t.Fatalf("expected ordered merchant effect activation error, got %v", err)
		}
	})

	t.Run("partial Live adapter", func(t *testing.T) {
		clearPayPalEnvironment(t)
		t.Setenv("APP_ENV", "development")
		t.Setenv("PAYPAL_LIVE_CLIENT_ID", "live-client")
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "together") {
			t.Fatalf("expected all-or-none credentials error, got %v", err)
		}
	})
}

func TestPayPalLiveAdapterCanBePreparedWithoutIssuance(t *testing.T) {
	clearPayPalEnvironment(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("PAYPAL_LIVE_CLIENT_ID", "live-client")
	t.Setenv("PAYPAL_LIVE_CLIENT_SECRET", "live-secret")
	t.Setenv("PAYPAL_LIVE_WEBHOOK_ID", "WH-LIVE")
	t.Setenv("PAYPAL_LIVE_MERCHANT_ID", "MERCHANT-LIVE")
	t.Setenv("PAYPAL_LIVE_EMAIL", "merchant@example.test")
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.liveAgencyOrderIssueEnabled || config.paypalLiveCaptureEnabled {
		t.Fatalf("staged adapter unexpectedly enabled effects: %+v", config)
	}
}

func TestComposeForwardsPayPalLiveSwitchesWithSafeDefaults(t *testing.T) {
	paths := []string{"../../../compose.yaml"}
	if _, err := os.Stat("../../../deploy/vultr/compose.yaml"); err == nil {
		paths = append(paths, "../../../deploy/vultr/compose.yaml")
	}
	for _, path := range paths {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		compose := string(payload)
		for _, required := range []string{
			"LIVE_AGENCY_ORDER_ISSUE_ENABLED: ${LIVE_AGENCY_ORDER_ISSUE_ENABLED:-false}",
			"PAYPAL_LIVE_CAPTURE_ENABLED: ${PAYPAL_LIVE_CAPTURE_ENABLED:-false}",
			"MANUAL_MERCHANT_EFFECT_ENABLED: ${MANUAL_MERCHANT_EFFECT_ENABLED:-false}",
			"PAYPAL_LIVE_CLIENT_ID: ${PAYPAL_LIVE_CLIENT_ID:-}",
			"PAYPAL_LIVE_CLIENT_SECRET: ${PAYPAL_LIVE_CLIENT_SECRET:-}",
			"PAYPAL_LIVE_WEBHOOK_ID: ${PAYPAL_LIVE_WEBHOOK_ID:-}",
			"PAYPAL_LIVE_MERCHANT_ID: ${PAYPAL_LIVE_MERCHANT_ID:-}",
			"PAYPAL_LIVE_EMAIL: ${PAYPAL_LIVE_EMAIL:-}",
		} {
			if !strings.Contains(compose, required) {
				t.Fatalf("%s PayPal Live boundary missing %q", path, required)
			}
		}
	}
}

func TestSandboxAndLiveCanBeConfiguredTogether(t *testing.T) {
	config := runtimeConfig{paypalSandboxEnabled: true, liveAgencyOrderIssueEnabled: true,
		paypalLiveCaptureEnabled: true, manualMerchantEffectEnabled: true}
	if !config.paypalSandboxEnabled || !config.liveAgencyOrderIssueEnabled ||
		!config.paypalLiveCaptureEnabled || !config.manualMerchantEffectEnabled {
		t.Fatalf("independent payment rails collapsed: %+v", config)
	}
}
