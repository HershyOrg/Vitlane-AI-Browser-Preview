package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accounthttp "github.com/vitlane/vitlane/server/internal/account/iface/http"
	accountinfra "github.com/vitlane/vitlane/server/internal/account/infra"
	accountpostgres "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// ADR-0040 §10: a valid operator session older than the fresh-auth window is
// refused with FRESH_AUTH_REQUIRED on sensitive routes, while list-style
// operator routes keep working.
func TestFreshOperatorGateRejectsStaleSessions(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	baseDatabase, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer baseDatabase.Close()
	lockConnection, err := baseDatabase.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockConnection.Close()
	if _, err := lockConnection.ExecContext(ctx,
		`SELECT pg_advisory_lock(hashtextextended('vitlane.integration_tests', 0))`,
	); err != nil {
		t.Fatal(err)
	}
	defer lockConnection.ExecContext(context.Background(),
		`SELECT pg_advisory_unlock(hashtextextended('vitlane.integration_tests', 0))`)
	schema := "fresh_auth_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := baseDatabase.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := baseDatabase.DB.ExecContext(
			context.Background(), "DROP SCHEMA "+schema+" CASCADE",
		); err != nil {
			t.Errorf("drop isolated fresh auth schema: %v", err)
		}
	}()

	isolatedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := isolatedURL.Query()
	query.Set("search_path", schema)
	isolatedURL.RawQuery = query.Encode()
	database, err := sharedpostgres.Open(ctx, isolatedURL.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, "../../migrations"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	defer database.Close()

	repository := accountpostgres.NewRepository(database)
	service := accountapp.NewAuthenticationService(
		repository, nil, accountinfra.CryptoSecretGenerator{},
		sharedapp.SystemClock{}, sharedapp.UUIDGenerator{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	login, err := service.CreateDevelopmentSession(
		ctx, accountapp.DevelopmentSessionInput{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE users SET email='operator@example.com' WHERE id=$1
	`, login.User.ID); err != nil {
		t.Fatal(err)
	}

	middleware := accounthttp.NewAuthMiddleware(
		service,
		accounthttp.NewEmailAllowlist(nil),
		accounthttp.NewEmailAllowlist([]string{"operator@example.com"}),
	)
	sensitive := middleware.RequireFreshOperator(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	), 15*time.Minute)

	call := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "/sensitive", nil)
		request.AddCookie(&http.Cookie{
			Name: "vitlane_session", Value: login.SessionToken,
		})
		recorder := httptest.NewRecorder()
		sensitive.ServeHTTP(recorder, request)
		return recorder
	}

	if fresh := call(); fresh.Code != http.StatusNoContent {
		t.Fatalf("fresh operator refused: %d %s", fresh.Code, fresh.Body.String())
	}

	// Session ages past the window: the allowlist alone is no longer enough.
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE auth_sessions SET authenticated_at = now() - interval '16 minutes'
		WHERE user_id=$1
	`, login.User.ID); err != nil {
		t.Fatal(err)
	}
	stale := call()
	if stale.Code != http.StatusForbidden ||
		!strings.Contains(stale.Body.String(), "FRESH_AUTH_REQUIRED") {
		t.Fatalf("stale operator accepted: %d %s", stale.Code, stale.Body.String())
	}

	// A non-operator with a fresh session is still refused outright.
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE users SET email='person@example.com' WHERE id=$1
	`, login.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE auth_sessions SET authenticated_at = now() WHERE user_id=$1
	`, login.User.ID); err != nil {
		t.Fatal(err)
	}
	outsider := call()
	if outsider.Code != http.StatusForbidden ||
		!strings.Contains(outsider.Body.String(), "OPERATOR_REQUIRED") {
		t.Fatalf("non-operator accepted: %d %s", outsider.Code, outsider.Body.String())
	}
}
