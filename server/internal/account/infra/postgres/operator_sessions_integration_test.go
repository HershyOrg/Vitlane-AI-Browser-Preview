package postgres_test

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountpostgres "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// ADR-0040 §10: the ops screen lists active sessions and cuts one user's
// access with an audited decision inside a single transaction.
func TestOperatorSessionsListRevokeAndAudit(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	// Full-schema migration is intentionally bounded but must tolerate a cold
	// CI runner after the shared integration-test package queue.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	schema := "operator_sessions_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := baseDatabase.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := baseDatabase.DB.ExecContext(
			context.Background(), "DROP SCHEMA "+schema+" CASCADE",
		); err != nil {
			t.Errorf("drop isolated operator sessions schema: %v", err)
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
	if err := database.Migrate(ctx, "../../../../migrations"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	operatorID := "aa111111-1111-4111-8111-111111111111"
	subjectID := "aa222222-2222-4222-8222-222222222222"
	for _, user := range []string{operatorID, subjectID} {
		if _, err := database.DB.ExecContext(ctx, `
			INSERT INTO users(id, status, created_at, updated_at)
			VALUES ($1, 'ACTIVE', $2, $2)
		`, user, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO external_identities(
			id, user_id, provider, provider_subject, email_snapshot,
			email_verified, created_at, updated_at
		) VALUES (
			'ab222222-2222-4222-8222-222222222222', $1, 'GOOGLE',
			'subject-ops-test', 'subject@example.com', true, $2, $2
		)
	`, subjectID, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	sessions := []struct {
		id      string
		user    string
		expires time.Time
		revoked bool
	}{
		{"ac111111-1111-4111-8111-111111111111", subjectID, now.Add(24 * time.Hour), false},
		{"ac222222-2222-4222-8222-222222222222", subjectID, now.Add(48 * time.Hour), false},
		{"ac333333-3333-4333-8333-333333333333", subjectID, now.Add(-time.Minute), false},
		{"ac444444-4444-4444-8444-444444444444", operatorID, now.Add(24 * time.Hour), false},
	}
	for index, session := range sessions {
		var revokedAt any
		if session.revoked {
			revokedAt = now
		}
		if _, err := database.DB.ExecContext(ctx, `
			INSERT INTO auth_sessions(
				id, user_id, token_hash, expires_at, revoked_at, created_at,
				authenticated_at
			) VALUES ($1, $2, decode(lpad($3, 64, '0'), 'hex'), $4, $5, $6, $6)
		`, session.id, session.user, strconv.Itoa(index+40),
			session.expires, revokedAt, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	service := accountapp.NewOperatorSessionService(
		accountpostgres.NewRepository(database), database,
		sharedapp.SystemClock{}, sharedapp.UUIDGenerator{},
	)

	active, err := service.ListActiveSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 3 {
		t.Fatalf("active sessions=%d, want 3 (expired excluded)", len(active))
	}
	subjectEmail := ""
	for _, session := range active {
		if session.UserID == subjectID {
			subjectEmail = session.Email
		}
	}
	if subjectEmail != "subject@example.com" {
		t.Fatalf("subject email=%q", subjectEmail)
	}

	revoked, err := service.RevokeUserSessions(
		ctx, operatorID, subjectID, "operator offboarding rehearsal entry",
	)
	if err != nil || revoked != 2 {
		t.Fatalf("revoked=%d err=%v (expired session must not count)", revoked, err)
	}

	remaining, err := service.ListActiveSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].UserID != operatorID {
		t.Fatalf("remaining sessions=%#v", remaining)
	}

	var auditCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM operator_action_audits
		WHERE operator_user_id=$1 AND subject_user_id=$2
		  AND action='SESSION_REVOKE_ALL'
	`, operatorID, subjectID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("audit rows=%d", auditCount)
	}
}
