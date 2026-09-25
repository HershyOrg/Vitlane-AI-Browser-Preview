package postgres_test

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	accountpii "github.com/vitlane/vitlane/server/internal/account/infra/pii"
	accountpostgres "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// GAP-020 / ADR-0040 §8: after the grace window the pipeline erases profile
// ciphertext and identities, keeps purchase-linked snapshots on a retention
// clock, closes the ledger, and stays idempotent for the restore replay.
func TestPIILifecyclePurgesAfterGraceAndKeepsRetainedSnapshots(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
	schema := "pii_lifecycle_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := baseDatabase.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := baseDatabase.DB.ExecContext(
			context.Background(), "DROP SCHEMA "+schema+" CASCADE",
		); err != nil {
			t.Errorf("drop isolated pii lifecycle schema: %v", err)
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
	subject := "cc111111-1111-4111-8111-111111111111"
	bystander := "cc222222-2222-4222-8222-222222222222"
	for _, user := range []string{subject, bystander} {
		if _, err := database.DB.ExecContext(ctx, `
			INSERT INTO users(id, status, email, display_name, created_at, updated_at)
			VALUES ($1, 'ACTIVE', 'person-'||$3||'@example.com', 'Person', $2, $2)
		`, user, now.Add(-48*time.Hour), user); err != nil {
			t.Fatal(err)
		}
		if _, err := database.DB.ExecContext(ctx, `
			INSERT INTO external_identities(
				id, user_id, provider, provider_subject, email_snapshot,
				email_verified, created_at, updated_at
			) VALUES (
				gen_random_uuid(), $1, 'GOOGLE', 'subject-'||$3,
				'person-'||$3||'@example.com', true, $2, $2
			)
		`, user, now.Add(-48*time.Hour), user); err != nil {
			t.Fatal(err)
		}
	}
	profileID := "cd111111-1111-4111-8111-111111111111"
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO shipping_profiles(
			id, user_id, label, country, masked_summary, encrypted_payload,
			payload_nonce, key_version, payload_hmac, profile_version,
			is_default, created_at, updated_at
		) VALUES (
			$1, $2, 'home', 'KR', 'KR ***42', '\xdeadbeef', '\x0102030405060708090a0b0c',
			'v1', 'hmac-sha256:test', 1, true, $3, $3
		)
	`, profileID, subject, now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO shipping_snapshots(
			id, user_id, source_profile_id, profile_version, country,
			masked_summary, encrypted_payload, payload_nonce, key_version,
			snapshot_hmac, created_at
		) VALUES (
			'ce111111-1111-4111-8111-111111111111', $1, $2, 1, 'KR',
			'KR ***42', '\xdeadbeef', '\x0102030405060708090a0b0c', 'v1',
			'hmac-sha256:test', $3
		)
	`, subject, profileID, now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}

	repository := accountpostgres.NewRepository(database)
	if err := repository.RequestAccountDeletion(
		ctx, accountdomain.UserID(subject), now.Add(-25*time.Hour),
	); err != nil {
		t.Fatal(err)
	}

	worker := accountapp.NewPIILifecycleWorker(
		repository, sharedapp.SystemClock{}, time.Minute,
		24*time.Hour, 5*365*24*time.Hour, 50,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err := worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}

	var status string
	var identities, purgedProfiles, retainedSnapshots int
	var ledgerPurged, snapshotsRetained bool
	if err := database.DB.QueryRowContext(ctx, `
		SELECT u.status || ':' || u.email || u.display_name,
		       (SELECT count(*) FROM external_identities WHERE user_id=u.id),
		       (SELECT count(*) FROM shipping_profiles
		        WHERE user_id=u.id AND purged_at IS NOT NULL
		          AND encrypted_payload IS NULL),
		       (SELECT count(*) FROM shipping_snapshots
		        WHERE user_id=u.id AND purged_at IS NULL
		          AND purge_after IS NOT NULL),
		       (SELECT purged_at IS NOT NULL FROM user_deletions WHERE user_id=u.id),
		       (SELECT snapshots_retained FROM user_deletions WHERE user_id=u.id)
		FROM users u WHERE u.id=$1
	`, subject).Scan(
		&status, &identities, &purgedProfiles, &retainedSnapshots,
		&ledgerPurged, &snapshotsRetained,
	); err != nil {
		t.Fatal(err)
	}
	if status != "DELETED:" || identities != 0 || purgedProfiles != 1 ||
		retainedSnapshots != 1 || !ledgerPurged || !snapshotsRetained {
		t.Fatalf(
			"purge state status=%s identities=%d profiles=%d snapshots=%d ledger=%v retained=%v",
			status, identities, purgedProfiles, retainedSnapshots,
			ledgerPurged, snapshotsRetained,
		)
	}
	var bystanderIdentities int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM external_identities WHERE user_id=$1
	`, bystander).Scan(&bystanderIdentities); err != nil {
		t.Fatal(err)
	}
	if bystanderIdentities != 1 {
		t.Fatalf("bystander identities=%d", bystanderIdentities)
	}

	// Retention deadline reached: the snapshot ciphertext is erased too.
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE shipping_snapshots SET purge_after=$1 WHERE user_id=$2
	`, now.Add(-time.Minute), subject); err != nil {
		t.Fatal(err)
	}
	if err := worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var purgedSnapshots int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM shipping_snapshots
		WHERE user_id=$1 AND purged_at IS NOT NULL AND encrypted_payload IS NULL
	`, subject).Scan(&purgedSnapshots); err != nil {
		t.Fatal(err)
	}
	if purgedSnapshots != 1 {
		t.Fatalf("purged snapshots=%d", purgedSnapshots)
	}

	// Grace window: a fresh request is not due yet.
	if err := repository.RequestAccountDeletion(
		ctx, accountdomain.UserID(bystander), now,
	); err != nil {
		t.Fatal(err)
	}
	if err := worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var bystanderStatus string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT status FROM users WHERE id=$1
	`, bystander).Scan(&bystanderStatus); err != nil {
		t.Fatal(err)
	}
	if bystanderStatus != "DELETION_REQUESTED" {
		t.Fatalf("grace ignored: status=%s", bystanderStatus)
	}

	// Restore replay: re-marking the already-purged user runs cleanly again.
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE users SET status='DELETION_REQUESTED' WHERE id=$1
	`, subject); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE user_deletions SET purged_at=NULL, snapshots_retained=NULL
		WHERE user_id=$1
	`, subject); err != nil {
		t.Fatal(err)
	}
	if err := worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.DB.QueryRowContext(ctx, `
		SELECT status FROM users WHERE id=$1
	`, subject).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "DELETED" {
		t.Fatalf("replay status=%s", status)
	}
}

// ADR-0040 §8: rotation reseals stale rows under the active key while purged
// rows are left alone, and the rotated ciphertext stays decryptable.
func TestPIIKeyRotationResealsStaleRows(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
	schema := "pii_rotation_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := baseDatabase.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := baseDatabase.DB.ExecContext(
			context.Background(), "DROP SCHEMA "+schema+" CASCADE",
		); err != nil {
			t.Errorf("drop isolated pii rotation schema: %v", err)
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
	owner := "dd111111-1111-4111-8111-111111111111"
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO users(id, status, created_at, updated_at)
		VALUES ($1, 'ACTIVE', $2, $2)
	`, owner, now); err != nil {
		t.Fatal(err)
	}

	keyOne := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	keyTwo := "u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7s="
	originalRing, err := accountpii.NewKeyringFromSingle(keyOne, "v1")
	if err != nil {
		t.Fatal(err)
	}
	address := []byte(`{"line1":"secret street 42"}`)
	sealed, err := originalRing.Encrypt(ctx, address, owner)
	if err != nil {
		t.Fatal(err)
	}
	profileID := "de111111-1111-4111-8111-111111111111"
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO shipping_profiles(
			id, user_id, label, country, masked_summary, encrypted_payload,
			payload_nonce, key_version, payload_hmac, profile_version,
			is_default, created_at, updated_at
		) VALUES ($1, $2, 'home', 'KR', 'KR ***42', $3, $4, $5, $6, 1, true, $7, $7)
	`, profileID, owner, sealed.Ciphertext, sealed.Nonce,
		sealed.KeyVersion, sealed.Fingerprint, now); err != nil {
		t.Fatal(err)
	}
	// A purged snapshot must be skipped by rotation.
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO shipping_snapshots(
			id, user_id, source_profile_id, profile_version, country,
			masked_summary, encrypted_payload, payload_nonce, key_version,
			snapshot_hmac, purged_at, created_at
		) VALUES (
			'df111111-1111-4111-8111-111111111111', $1, $2, 1, 'KR',
			'KR ***42', NULL, NULL, 'v1', 'hmac-sha256:gone', $3, $3
		)
	`, owner, profileID, now); err != nil {
		t.Fatal(err)
	}

	rotatedRing, err := accountpii.NewKeyring(
		"v2:"+keyTwo+",v1:"+keyOne, "v2",
	)
	if err != nil {
		t.Fatal(err)
	}
	repository := accountpostgres.NewRepository(database)
	worker := accountapp.NewPIIKeyRotationWorker(
		repository, rotatedRing, rotatedRing.ActiveVersion,
		sharedapp.SystemClock{}, time.Minute, 20,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	rotated, err := worker.Tick(ctx)
	if err != nil || rotated != 1 {
		t.Fatalf("rotated=%d err=%v", rotated, err)
	}

	var keyVersion, fingerprint string
	var ciphertext, nonce []byte
	if err := database.DB.QueryRowContext(ctx, `
		SELECT key_version, payload_hmac, encrypted_payload, payload_nonce
		FROM shipping_profiles WHERE id=$1
	`, profileID).Scan(&keyVersion, &fingerprint, &ciphertext, &nonce); err != nil {
		t.Fatal(err)
	}
	if keyVersion != "v2" {
		t.Fatalf("key version=%s", keyVersion)
	}
	plaintext, err := rotatedRing.Decrypt(ctx, accountapp.EncryptedPII{
		Ciphertext: ciphertext, Nonce: nonce,
		KeyVersion: keyVersion, Fingerprint: fingerprint,
	}, owner)
	if err != nil || string(plaintext) != string(address) {
		t.Fatalf("rotated decrypt=%q err=%v", plaintext, err)
	}

	// Second tick has nothing left to do.
	rotated, err = worker.Tick(ctx)
	if err != nil || rotated != 0 {
		t.Fatalf("idle tick rotated=%d err=%v", rotated, err)
	}
}
