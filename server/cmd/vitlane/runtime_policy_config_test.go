package main

import (
	"strings"
	"testing"
	"time"
)

func TestRuntimePolicyDefaults(t *testing.T) {
	clearRuntimePolicyEnvironment(t)
	policies, err := loadRuntimePolicies()
	if err != nil {
		t.Fatal(err)
	}
	if policies.database.MaxOpenConnections != 40 ||
		policies.database.MaxIdleConnections != 10 ||
		policies.database.MigrationLockTimeout != 2*time.Minute ||
		policies.httpTransport.ResponseHeaderTimeout != 20*time.Second ||
		policies.maxConcurrentRequests != 500 ||
		policies.requestTimeout != 10*time.Second ||
		policies.managedModelTimeout != 180*time.Second {
		t.Fatalf("runtime policy defaults = %#v", policies)
	}
}

func TestRuntimePolicyRejectsInvalidEnvironment(t *testing.T) {
	clearRuntimePolicyEnvironment(t)
	t.Setenv("DB_MAX_OPEN_CONNECTIONS", "4")
	t.Setenv("DB_MAX_IDLE_CONNECTIONS", "5")
	_, err := loadRuntimePolicies()
	if err == nil || !strings.Contains(err.Error(), "max idle") {
		t.Fatalf("invalid pool error = %v", err)
	}

	t.Setenv("DB_MAX_IDLE_CONNECTIONS", "2")
	t.Setenv("HTTP_CONNECT_TIMEOUT_SECONDS", "not-a-number")
	_, err = loadRuntimePolicies()
	if err == nil || !strings.Contains(err.Error(), "HTTP_CONNECT_TIMEOUT_SECONDS") {
		t.Fatalf("invalid timeout error = %v", err)
	}

	t.Setenv("HTTP_CONNECT_TIMEOUT_SECONDS", "5")
	t.Setenv("MANAGED_MODEL_TIMEOUT_SECONDS", "601")
	_, err = loadRuntimePolicies()
	if err == nil || !strings.Contains(err.Error(), "operational upper bound") {
		t.Fatalf("unsafe provider timeout error = %v", err)
	}

	t.Setenv("MANAGED_MODEL_TIMEOUT_SECONDS", "90")
	t.Setenv("HTTP_REQUEST_TIMEOUT_SECONDS", "16")
	_, err = loadRuntimePolicies()
	if err == nil || !strings.Contains(err.Error(), "HTTP_REQUEST_TIMEOUT_SECONDS") {
		t.Fatalf("unsafe request timeout error = %v", err)
	}

	t.Setenv("HTTP_REQUEST_TIMEOUT_SECONDS", "10")
	t.Setenv("HTTP_MAX_CONCURRENT_REQUESTS", "501")
	_, err = loadRuntimePolicies()
	if err == nil || !strings.Contains(err.Error(), "HTTP_MAX_CONCURRENT_REQUESTS") {
		t.Fatalf("unsafe request concurrency error = %v", err)
	}

	t.Setenv("HTTP_MAX_CONCURRENT_REQUESTS", "500")
	t.Setenv("DB_MIGRATION_LOCK_TIMEOUT_SECONDS", "601")
	_, err = loadRuntimePolicies()
	if err == nil || !strings.Contains(err.Error(), "operational upper bound") {
		t.Fatalf("unsafe migration lock timeout error = %v", err)
	}
}

func clearRuntimePolicyEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"DB_MAX_OPEN_CONNECTIONS", "DB_MAX_IDLE_CONNECTIONS",
		"DB_CONNECTION_MAX_IDLE_SECONDS", "DB_CONNECTION_MAX_LIFETIME_SECONDS",
		"DB_ACQUIRE_TIMEOUT_SECONDS", "DB_MIGRATION_LOCK_TIMEOUT_SECONDS",
		"DB_INTERACTIVE_QUERY_TIMEOUT_SECONDS",
		"DB_INTERACTIVE_TRANSACTION_TIMEOUT_SECONDS", "DB_INTERACTIVE_LOCK_TIMEOUT_SECONDS",
		"DB_WORKER_QUERY_TIMEOUT_SECONDS", "DB_WORKER_TRANSACTION_TIMEOUT_SECONDS",
		"DB_WORKER_LOCK_TIMEOUT_SECONDS", "DB_ADMIN_QUERY_TIMEOUT_SECONDS",
		"DB_ADMIN_TRANSACTION_TIMEOUT_SECONDS", "DB_ADMIN_LOCK_TIMEOUT_SECONDS",
		"HTTP_CONNECT_TIMEOUT_SECONDS", "HTTP_KEEP_ALIVE_SECONDS",
		"HTTP_TLS_HANDSHAKE_TIMEOUT_SECONDS", "HTTP_RESPONSE_HEADER_TIMEOUT_SECONDS",
		"HTTP_EXPECT_CONTINUE_TIMEOUT_SECONDS",
		"HTTP_IDLE_CONNECTION_TIMEOUT_SECONDS", "HTTP_MAX_IDLE_CONNECTIONS",
		"HTTP_MAX_IDLE_CONNECTIONS_PER_HOST", "HTTP_MAX_CONNECTIONS_PER_HOST",
		"HTTP_MAX_CONCURRENT_REQUESTS",
		"HTTP_REQUEST_TIMEOUT_SECONDS", "EXTERNAL_FINALIZATION_TIMEOUT_SECONDS",
		"GOOGLE_OIDC_TIMEOUT_SECONDS", "SHOPIFY_UCP_TIMEOUT_SECONDS",
		"MANAGED_MODEL_TIMEOUT_SECONDS", "EVM_RPC_HTTP_TIMEOUT_SECONDS",
	} {
		t.Setenv(key, "")
	}
}
