package postgres_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	accountinfra "github.com/vitlane/vitlane/server/internal/account/infra"
	"github.com/vitlane/vitlane/server/internal/account/infra/mockdojang"
	accountpostgres "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

func TestPostgresAuthenticationInvariants(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	// This public-schema invariant suite includes the full migration set and a
	// global integration lock. Keep it bounded without making normal CI queueing
	// consume the entire parent deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	database, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx, "../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	lockConnection, err := database.DB.Conn(ctx)
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
	if _, err := database.DB.ExecContext(ctx, `
		TRUNCATE plan_creation_requests, auth_sessions, oauth_login_attempts,
		         external_identities, shopping_sessions, plan_targets,
		         shopping_plans, wallets, users CASCADE
	`); err != nil {
		t.Fatal(err)
	}

	repository := accountpostgres.NewRepository(database)
	now := time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)
	attempt, err := accountdomain.NewOAuthLoginAttempt(
		accountdomain.OAuthLoginAttemptID(sharedapp.UUIDGenerator{}.NewID()),
		accountdomain.IdentityProviderGoogle,
		bytesOf(1), bytesOf(2), bytesOf(3), "verifier", "/plans/example",
		now, 10*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateLoginAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, consumeErr := repository.ConsumeLoginAttempt(
				ctx, accountdomain.IdentityProviderGoogle,
				attempt.StateHash, attempt.BrowserBindingHash, now,
			)
			results <- consumeErr
		}()
	}
	wait.Wait()
	close(results)
	successes := 0
	consumed := 0
	for result := range results {
		if result == nil {
			successes++
		}
		if errors.Is(result, accountdomain.ErrLoginAttemptConsumed) {
			consumed++
		}
	}
	if successes != 1 || consumed != 1 {
		t.Fatalf("attempt consumption success=%d consumed=%d", successes, consumed)
	}
	var storedPKCE sql.NullString
	var loginSecretCleanedAt sql.NullTime
	if err := database.DB.QueryRowContext(ctx, `
		SELECT pkce_verifier, secret_cleaned_at
		FROM oauth_login_attempts
		WHERE id=$1
	`, attempt.ID).Scan(&storedPKCE, &loginSecretCleanedAt); err != nil {
		t.Fatal(err)
	}
	if storedPKCE.Valid || !loginSecretCleanedAt.Valid {
		t.Fatalf(
			"consumed OAuth secret was retained: pkce=%#v cleaned=%#v",
			storedPKCE, loginSecretCleanedAt,
		)
	}
	expiredAttempt, err := accountdomain.NewOAuthLoginAttempt(
		accountdomain.OAuthLoginAttemptID(sharedapp.UUIDGenerator{}.NewID()),
		accountdomain.IdentityProviderGoogle,
		bytesOf(11), bytesOf(12), bytesOf(13), "expired-verifier", "/",
		now.Add(-20*time.Minute), 10*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateLoginAttempt(ctx, expiredAttempt); err != nil {
		t.Fatal(err)
	}
	cleanup, err := repository.CleanupTerminalSecurityArtifacts(ctx, now, 100)
	if err != nil || cleanup.LoginAttempts != 1 {
		t.Fatalf("expired OAuth cleanup=%#v err=%v", cleanup, err)
	}
	cleanupReplay, err := repository.CleanupTerminalSecurityArtifacts(ctx, now, 100)
	if err != nil || cleanupReplay.LoginAttempts != 0 {
		t.Fatalf("OAuth cleanup replay=%#v err=%v", cleanupReplay, err)
	}

	ids := sharedapp.UUIDGenerator{}
	user := accountdomain.NewExternalUser(
		accountdomain.UserID(ids.NewID()), "first@example.com", "First", now,
	)
	verified := accountdomain.VerifiedIdentity{
		Provider: accountdomain.IdentityProviderGoogle,
		Subject:  "stable-subject", Email: user.Email,
		EmailVerified: true, DisplayName: user.DisplayName,
	}
	identity, err := accountdomain.NewExternalIdentity(
		accountdomain.ExternalIdentityID(ids.NewID()), user.ID, verified, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := accountdomain.NewAuthSession(
		accountdomain.AuthSessionID(ids.NewID()), user.ID, bytesOf(4),
		now, 7*24*time.Hour,
	)
	first, err := repository.CompleteExternalLogin(ctx, user, identity, session, nil)
	if err != nil {
		t.Fatal(err)
	}

	proposedAgain := accountdomain.NewExternalUser(
		accountdomain.UserID(ids.NewID()), "changed@example.com", "Changed", now.Add(time.Minute),
	)
	verified.Email = proposedAgain.Email
	verified.DisplayName = proposedAgain.DisplayName
	identityAgain, _ := accountdomain.NewExternalIdentity(
		accountdomain.ExternalIdentityID(ids.NewID()), proposedAgain.ID,
		verified, now.Add(time.Minute),
	)
	sessionAgain, _ := accountdomain.NewAuthSession(
		accountdomain.AuthSessionID(ids.NewID()), proposedAgain.ID, bytesOf(5),
		now.Add(time.Minute), 7*24*time.Hour,
	)
	second, err := repository.CompleteExternalLogin(
		ctx, proposedAgain, identityAgain, sessionAgain, session.TokenHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.User.ID != second.User.ID {
		t.Fatalf("same subject produced two users: %s != %s", first.User.ID, second.User.ID)
	}
	if second.User.Email != "changed@example.com" {
		t.Fatalf("profile snapshot was not updated: %#v", second.User)
	}
	if _, err := repository.FindActiveAuthSession(
		ctx, session.TokenHash, now.Add(2*time.Minute),
	); !errors.Is(err, accountdomain.ErrSessionMissing) {
		t.Fatalf("rotated browser session should be revoked: %v", err)
	}
	active, err := repository.FindActiveAuthSession(
		ctx, sessionAgain.TokenHash, now.Add(2*time.Minute),
	)
	if err != nil || active.User.ID != first.User.ID {
		t.Fatalf("new session lookup=%#v err=%v", active, err)
	}
	handoff, err := accountdomain.NewMobileAuthHandoff(
		ids.NewID(), active.User.ID, bytesOf(21), bytesOf(22),
		active.Session.ExpiresAt, active.Session.AuthenticatedAt,
		now.Add(2*time.Minute), 2*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateMobileAuthHandoff(
		ctx, handoff, sessionAgain.TokenHash,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FindActiveAuthSession(
		ctx, sessionAgain.TokenHash, now.Add(3*time.Minute),
	); !errors.Is(err, accountdomain.ErrSessionMissing) {
		t.Fatalf("mobile source session should be rotated: %v", err)
	}
	mobile, err := repository.ExchangeMobileAuthHandoff(
		ctx, handoff.CodeHash, handoff.VerifierChallenge,
		accountapp.MobileSessionGrant{
			SessionID: accountdomain.AuthSessionID(ids.NewID()),
			TokenHash: bytesOf(23), CreatedAt: now.Add(3 * time.Minute),
		},
	)
	if err != nil || mobile.User.ID != active.User.ID {
		t.Fatalf("mobile handoff exchange=%#v err=%v", mobile, err)
	}
	if _, err := repository.ExchangeMobileAuthHandoff(
		ctx, handoff.CodeHash, handoff.VerifierChallenge,
		accountapp.MobileSessionGrant{
			SessionID: accountdomain.AuthSessionID(ids.NewID()),
			TokenHash: bytesOf(24), CreatedAt: now.Add(3 * time.Minute),
		},
	); !errors.Is(err, accountdomain.ErrMobileAuthConsumed) {
		t.Fatalf("mobile handoff replay should fail: %v", err)
	}
	if err := repository.RevokeAuthSession(ctx, sessionAgain.TokenHash, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FindActiveAuthSession(
		ctx, sessionAgain.TokenHash, now.Add(4*time.Minute),
	); !errors.Is(err, accountdomain.ErrSessionMissing) {
		t.Fatalf("revoked session should be rejected: %v", err)
	}
}

type integrationClock struct {
	now time.Time
}

func (clock integrationClock) Now() time.Time {
	return clock.now
}

func TestPostgresWalletRegistrationAndMockKYCLifecycle(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	database, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx, "../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	lockConnection, err := database.DB.Conn(ctx)
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
	if _, err := database.DB.ExecContext(ctx, `TRUNCATE users CASCADE`); err != nil {
		t.Fatal(err)
	}

	repository := accountpostgres.NewRepository(database)
	ids := sharedapp.UUIDGenerator{}
	now := time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC)
	users := []accountdomain.User{
		accountdomain.NewExternalUser(
			accountdomain.UserID(ids.NewID()), "same-1@example.com", "Same One", now,
		),
		accountdomain.NewExternalUser(
			accountdomain.UserID(ids.NewID()), "same-2@example.com", "Same Two", now,
		),
	}
	for _, user := range users {
		if err := repository.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}

	sameAddressKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	sameAddress := crypto.PubkeyToAddress(sameAddressKey.PublicKey).Hex()
	const (
		registerSameOperation = "register-same-address"
		completeSameOperation = "complete-same-address"
	)
	completions := make([]accountapp.WalletRegistrationCompletionResult, 0, len(users))
	for index, user := range users {
		completion := registerWallet(
			t,
			ctx,
			repository,
			user.ID,
			sameAddressKey,
			now,
			registerSameOperation,
			completeSameOperation,
		)
		if completion.Wallet.UserID != user.ID ||
			completion.Wallet.Address != sameAddress ||
			!completion.Wallet.IsRegistered() ||
			completion.OwnershipProof.UserID != user.ID ||
			completion.OwnershipProof.WalletID != completion.Wallet.ID {
			t.Fatalf("account %d registration scope mismatch: %#v", index, completion)
		}
		completions = append(completions, completion)
		var storedNonce, storedMessage sql.NullString
		var walletSecretCleanedAt sql.NullTime
		if err := database.DB.QueryRowContext(ctx, `
			SELECT nonce, message, secret_cleaned_at
			FROM wallet_registration_attempts
			WHERE id=$1
		`, completion.OwnershipProof.RegistrationAttemptID).Scan(
			&storedNonce, &storedMessage, &walletSecretCleanedAt,
		); err != nil {
			t.Fatal(err)
		}
		if storedNonce.Valid || storedMessage.Valid ||
			!walletSecretCleanedAt.Valid {
			t.Fatalf(
				"terminal Wallet secret was retained: nonce=%#v message=%#v cleaned=%#v",
				storedNonce, storedMessage, walletSecretCleanedAt,
			)
		}
	}
	if completions[0].Wallet.ID == completions[1].Wallet.ID {
		t.Fatalf("same address across users collapsed into one Wallet: %#v", completions)
	}
	if _, err := repository.FindWallet(
		ctx, users[1].ID, completions[0].Wallet.ID,
	); !errors.Is(err, accountdomain.ErrWalletNotRegistered) {
		t.Fatalf("cross-user Wallet lookup should fail: %v", err)
	}
	if _, err := repository.FindWalletOwnershipProof(
		ctx,
		users[1].ID,
		completions[0].Wallet.ID,
		completions[0].OwnershipProof.ID,
	); !errors.Is(err, accountdomain.ErrWalletOwnershipProofMissing) {
		t.Fatalf("cross-user ownership proof lookup should fail: %v", err)
	}

	otherKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	operationReuseService := newWalletRegistrationIntegrationService(
		repository, now,
	)
	_, err = operationReuseService.CreateRegistrationAttempt(
		ctx,
		accountapp.CreateRegistrationAttemptInput{
			UserID:            string(users[0].ID),
			Address:           crypto.PubkeyToAddress(otherKey.PublicKey).Hex(),
			ChainID:           "eip155:91342",
			ClientOperationID: registerSameOperation,
		},
	)
	if !errors.Is(err, accountdomain.ErrWalletRegistrationOperationReused) {
		t.Fatalf("registration operation accepted different request: %v", err)
	}
	expiringService := newWalletRegistrationIntegrationService(
		repository, now,
	)
	expiringAttempt, err := expiringService.CreateRegistrationAttempt(
		ctx,
		accountapp.CreateRegistrationAttemptInput{
			UserID:            string(users[0].ID),
			Address:           crypto.PubkeyToAddress(sameAddressKey.PublicKey).Hex(),
			ChainID:           "eip155:91342",
			ClientOperationID: "registration-attempt-expiry-replay",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	expiredReplayService := newWalletRegistrationIntegrationService(
		repository, expiringAttempt.Attempt.ExpiresAt,
	)
	_, err = expiredReplayService.CreateRegistrationAttempt(
		ctx,
		accountapp.CreateRegistrationAttemptInput{
			UserID:            string(users[0].ID),
			Address:           crypto.PubkeyToAddress(sameAddressKey.PublicKey).Hex(),
			ChainID:           "eip155:91342",
			ClientOperationID: "registration-attempt-expiry-replay",
		},
	)
	if !errors.Is(err, accountdomain.ErrWalletRegistrationAttemptExpired) {
		t.Fatalf("expired create replay error=%v", err)
	}
	storedExpiredAttempt, err := repository.FindWalletRegistrationAttempt(
		ctx, users[0].ID, expiringAttempt.Attempt.ID,
	)
	if err != nil ||
		storedExpiredAttempt.Status !=
			accountdomain.WalletRegistrationAttemptExpired {
		t.Fatalf(
			"expired create replay was not persisted: %#v err=%v",
			storedExpiredAttempt, err,
		)
	}
	if storedExpiredAttempt.Message != "" ||
		storedExpiredAttempt.SecretCleanedAt == nil {
		t.Fatalf(
			"expired Wallet attempt retained terminal secret: %#v",
			storedExpiredAttempt,
		)
	}
	cleanupService := newWalletRegistrationIntegrationService(
		repository, now.Add(30*time.Second),
	)
	cleanupPending, err := cleanupService.CreateRegistrationAttempt(
		ctx,
		accountapp.CreateRegistrationAttemptInput{
			UserID:            string(users[0].ID),
			Address:           crypto.PubkeyToAddress(sameAddressKey.PublicKey).Hex(),
			ChainID:           "eip155:91342",
			ClientOperationID: "registration-attempt-worker-cleanup",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanupResult, err := repository.CleanupTerminalSecurityArtifacts(
		ctx, cleanupPending.Attempt.ExpiresAt, 100,
	)
	if err != nil || cleanupResult.WalletAttempts < 1 {
		t.Fatalf("Wallet cleanup=%#v err=%v", cleanupResult, err)
	}
	cleanedPending, err := repository.FindWalletRegistrationAttempt(
		ctx, users[0].ID, cleanupPending.Attempt.ID,
	)
	if err != nil ||
		cleanedPending.Status != accountdomain.WalletRegistrationAttemptExpired ||
		cleanedPending.Message != "" || cleanedPending.SecretCleanedAt == nil {
		t.Fatalf("cleaned Wallet attempt=%#v err=%v", cleanedPending, err)
	}
	cleanupReplay, err := repository.CleanupTerminalSecurityArtifacts(
		ctx, cleanupPending.Attempt.ExpiresAt, 100,
	)
	if err != nil || cleanupReplay.WalletAttempts != 0 {
		t.Fatalf("Wallet cleanup replay=%#v err=%v", cleanupReplay, err)
	}

	singleWalletService := newWalletRegistrationIntegrationService(
		repository, now.Add(time.Minute),
	)
	_, err = singleWalletService.CreateRegistrationAttempt(
		ctx,
		accountapp.CreateRegistrationAttemptInput{
			UserID:            string(users[0].ID),
			Address:           crypto.PubkeyToAddress(otherKey.PublicKey).Hex(),
			ChainID:           "eip155:91342",
			ClientOperationID: "reject-second-wallet",
		},
	)
	if !errors.Is(err, accountdomain.ErrWalletAlreadyRegistered) {
		t.Fatalf("second current Wallet was not rejected: %v", err)
	}
	firstRegisteredAt := completions[0].Wallet.RegisteredAt
	firstProofID := completions[0].OwnershipProof.ID
	refreshedFirst := registerWallet(
		t,
		ctx,
		repository,
		users[0].ID,
		sameAddressKey,
		now.Add(90*time.Second),
		"refresh-first-wallet-proof",
		"complete-first-wallet-proof-refresh",
	)
	if refreshedFirst.Wallet.ID != completions[0].Wallet.ID ||
		!refreshedFirst.Wallet.RegisteredAt.Equal(firstRegisteredAt) {
		t.Fatalf(
			"proof refresh changed Wallet identity or registration order: before=%#v after=%#v",
			completions[0].Wallet, refreshedFirst.Wallet,
		)
	}
	supersededFirstProof, err := repository.FindWalletOwnershipProof(
		ctx, users[0].ID, refreshedFirst.Wallet.ID, firstProofID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if supersededFirstProof.RevokedAt == nil {
		t.Fatalf(
			"proof refresh did not revoke the superseded proof: %#v",
			supersededFirstProof,
		)
	}
	completions[0] = refreshedFirst
	secondWallet := refreshedFirst
	wallets, err := repository.ListWallets(ctx, users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	assertExactlyOneDefaultWallet(
		t, wallets, completions[0].Wallet.ID, "single current Wallet",
	)

	cancelledKYC, err := accountdomain.NewKYCVerificationCase(
		ids.NewID(),
		users[0].ID,
		secondWallet.Wallet.ID,
		secondWallet.OwnershipProof.ID,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		now.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	cancelledOperation, err := accountdomain.NewKYCProviderOperation(
		accountdomain.KYCProviderOperationID(ids.NewID()),
		cancelledKYC,
		accountdomain.KYCOperationStart,
		"kyc-start-unavailable-before-deregister",
		"0xkyc-start-unavailable-before-deregister",
		"kyc:start:unavailable-before-deregister",
		now.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.ReserveKYCStart(
		ctx, cancelledKYC, cancelledOperation,
	); err != nil {
		t.Fatal(err)
	}
	cancelledOperation, err = cancelledOperation.ProviderUnavailable(
		"PROVIDER_UNAVAILABLE", now.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.CompleteKYCProviderFailure(
		ctx, cancelledOperation, now.Add(2*time.Minute),
	); err != nil {
		t.Fatal(err)
	}

	if err := repository.DeregisterWallet(
		ctx, users[0].ID, secondWallet.Wallet.ID, now.Add(3*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	deregistered, err := repository.FindWallet(
		ctx, users[0].ID, secondWallet.Wallet.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if deregistered.RegistrationStatus != accountdomain.WalletDeregistered ||
		deregistered.CurrentOwnershipProofID != nil ||
		deregistered.DeregisteredAt == nil {
		t.Fatalf("Wallet was not deregistered atomically: %#v", deregistered)
	}
	firstDeregisteredAt := *deregistered.DeregisteredAt
	if err := repository.DeregisterWallet(
		ctx, users[0].ID, secondWallet.Wallet.ID, now.Add(3*time.Minute+time.Second),
	); err != nil {
		t.Fatalf("deregister response-loss retry was not idempotent: %v", err)
	}
	deregistered, err = repository.FindWallet(
		ctx, users[0].ID, secondWallet.Wallet.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if deregistered.DeregisteredAt == nil ||
		!deregistered.DeregisteredAt.Equal(firstDeregisteredAt) {
		t.Fatalf(
			"deregister retry changed the terminal timestamp: first=%s retry=%#v",
			firstDeregisteredAt, deregistered,
		)
	}
	revokedProof, err := repository.FindWalletOwnershipProof(
		ctx,
		users[0].ID,
		secondWallet.Wallet.ID,
		secondWallet.OwnershipProof.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if revokedProof.RevokedAt == nil {
		t.Fatalf("deregister did not revoke current ownership proof: %#v", revokedProof)
	}
	cancelledRecord, found, err := repository.FindKYCProviderOperation(
		ctx,
		users[0].ID,
		accountdomain.KYCOperationStart,
		cancelledOperation.ClientOperationID,
		cancelledOperation.RequestHash,
	)
	if err != nil || !found ||
		cancelledRecord.Operation.State != accountdomain.KYCOperationFailed ||
		cancelledRecord.Operation.Retryable ||
		cancelledRecord.Operation.FailureCode != "WALLET_DEREGISTERED" ||
		cancelledRecord.Case.State != accountdomain.KYCStateCancelled {
		t.Fatalf(
			"deregister did not terminalize unavailable KYC work: %#v found=%v err=%v",
			cancelledRecord, found, err,
		)
	}
	wallets, err = repository.ListWallets(ctx, users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(wallets) != 0 {
		t.Fatalf("deregistered current Wallet remained in overview: %#v", wallets)
	}

	reregistered := registerWallet(
		t,
		ctx,
		repository,
		users[0].ID,
		sameAddressKey,
		now.Add(4*time.Minute),
		"reregister-second-wallet",
		"complete-reregistered-wallet",
	)
	if reregistered.Wallet.ID != secondWallet.Wallet.ID ||
		reregistered.OwnershipProof.WalletID != secondWallet.Wallet.ID ||
		reregistered.OwnershipProof.ID == secondWallet.OwnershipProof.ID ||
		!reregistered.Wallet.CreatedAt.Equal(secondWallet.Wallet.CreatedAt) {
		t.Fatalf(
			"re-registration did not preserve canonical Wallet identity: before=%#v after=%#v",
			secondWallet, reregistered,
		)
	}
	wallets, err = repository.ListWallets(ctx, users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	assertExactlyOneDefaultWallet(
		t, wallets, reregistered.Wallet.ID, "re-registration",
	)
	completions[0] = reregistered
	completions[0] = assertConcurrentRegistrationCompletion(
		t, ctx, repository, users[0].ID, sameAddressKey,
		now.Add(270*time.Second),
	)
	defaultRaceUser := accountdomain.NewExternalUser(
		accountdomain.UserID(ids.NewID()),
		"default-race@example.com",
		"Default Race",
		now,
	)
	if err := repository.CreateUser(ctx, defaultRaceUser); err != nil {
		t.Fatal(err)
	}
	firstDefaultKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	secondDefaultKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	assertConcurrentFirstWalletDefaults(
		t, ctx, repository, defaultRaceUser.ID,
		[]*ecdsa.PrivateKey{firstDefaultKey, secondDefaultKey},
		now.Add(280*time.Second),
	)

	kycNow := now.Add(5 * time.Minute)
	startedCases := make([]accountdomain.KYCVerificationCase, 0, len(users))
	for index, user := range users {
		service := accountapp.NewKYCService(
			repository,
			mockdojang.Provider{},
			accountdomain.KYCProviderMockDojang,
			accountdomain.KYCEffectSimulated,
			integrationClock{now: kycNow},
			ids,
		)
		startInput := accountapp.StartKYCInput{
			UserID:            string(user.ID),
			WalletID:          string(completions[index].Wallet.ID),
			OwnershipProofID:  string(completions[index].OwnershipProof.ID),
			ClientOperationID: "kyc-start-same-address",
		}
		started, err := service.Start(ctx, startInput)
		if err != nil || started.Replay {
			t.Fatalf("start account %d: result=%#v err=%v", index, started, err)
		}
		startReplay, err := service.Start(ctx, startInput)
		if err != nil || !startReplay.Replay ||
			startReplay.Case.ID != started.Case.ID {
			t.Fatalf("start replay account %d: result=%#v err=%v", index, startReplay, err)
		}
		if started.Case.UserID != user.ID ||
			started.Case.WalletID != completions[index].Wallet.ID ||
			started.Case.StartedWithOwnershipProofID !=
				completions[index].OwnershipProof.ID ||
			started.Case.State != accountdomain.KYCStatePendingProvider ||
			started.Case.ProviderKind != accountdomain.KYCProviderMockDojang ||
			started.Case.ProviderVersion != accountdomain.MockDojangProviderVersion ||
			started.Case.ExternalEffect != accountdomain.KYCEffectSimulated {
			t.Fatalf("KYC start scope mismatch for account %d: %#v", index, started)
		}
		startedCases = append(startedCases, started.Case)

		checkInput := accountapp.CheckKYCInput{
			UserID:            string(user.ID),
			CaseID:            started.Case.ID,
			ClientOperationID: "kyc-check-same-address",
		}
		if index == 0 {
			orphanedOperationID :=
				accountdomain.KYCProviderOperationID(ids.NewID())
			orphanedOperation, err :=
				accountdomain.NewKYCProviderOperation(
					orphanedOperationID,
					started.Case,
					accountdomain.KYCOperationCheck,
					"kyc-check-orphaned-browser-operation",
					"orphaned-request-hash",
					"kyc:check:"+string(orphanedOperationID),
					kycNow,
				)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := repository.ReserveKYCCheck(
				ctx, started.Case, orphanedOperation,
			); err != nil {
				t.Fatal(err)
			}
			checkInput.ClientOperationID =
				"kyc-check-replacement-browser-operation"
		}
		checked, err := service.Check(ctx, checkInput)
		expectedReplay := index == 0
		if err != nil || checked.Replay != expectedReplay {
			t.Fatalf("check account %d: result=%#v err=%v", index, checked, err)
		}
		checkReplay, err := service.Check(ctx, checkInput)
		if err != nil || !checkReplay.Replay ||
			checkReplay.Credential == nil ||
			checked.Credential == nil ||
			checkReplay.Credential.ID != checked.Credential.ID ||
			checkReplay.Observation == nil ||
			checked.Observation == nil ||
			checkReplay.Observation.ID != checked.Observation.ID {
			t.Fatalf("check replay account %d: result=%#v err=%v", index, checkReplay, err)
		}
		if checked.Case.State != accountdomain.KYCStateVerified ||
			checked.Credential.UserID != user.ID ||
			checked.Credential.WalletID != completions[index].Wallet.ID ||
			checked.Credential.ProviderKind != accountdomain.KYCProviderMockDojang ||
			checked.Credential.ExternalEffect != accountdomain.KYCEffectSimulated ||
			checked.Observation.UserID != user.ID ||
			checked.Observation.WalletID != completions[index].Wallet.ID ||
			checked.Observation.Status != accountdomain.KYCEvidenceValid {
			t.Fatalf("KYC result scope mismatch for account %d: %#v", index, checked)
		}
	}
	if _, err := repository.FindKYCVerificationCase(
		ctx, users[1].ID, startedCases[0].ID,
	); !errors.Is(err, accountdomain.ErrKYCVerificationMissing) {
		t.Fatalf("cross-user KYC case lookup should fail: %v", err)
	}

	operationReuseKYC := accountapp.NewKYCService(
		repository,
		mockdojang.Provider{},
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		integrationClock{now: kycNow},
		ids,
	)
	_, err = operationReuseKYC.Start(
		ctx,
		accountapp.StartKYCInput{
			UserID:            string(users[0].ID),
			WalletID:          string(reregistered.Wallet.ID),
			OwnershipProofID:  string(reregistered.OwnershipProof.ID),
			ClientOperationID: "kyc-start-same-address",
		},
	)
	if !errors.Is(err, accountdomain.ErrKYCIdempotencyReused) {
		t.Fatalf("KYC operation accepted a different request: %v", err)
	}
	assertConcurrentKYCOperations(
		t,
		ctx,
		repository,
		completions[0].Wallet,
		completions[0].OwnershipProof,
		now.Add(6*time.Minute),
	)
	invalidStartService := accountapp.NewKYCService(
		repository,
		invalidKYCStartProvider{},
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		integrationClock{now: now.Add(7 * time.Minute)},
		ids,
	)
	_, err = invalidStartService.Start(ctx, accountapp.StartKYCInput{
		UserID:            string(users[0].ID),
		WalletID:          string(completions[0].Wallet.ID),
		OwnershipProofID:  string(completions[0].OwnershipProof.ID),
		ClientOperationID: "kyc-start-invalid-provider-result",
	})
	if !errors.Is(err, accountdomain.ErrKYCVerificationInvalid) {
		t.Fatalf("invalid provider Start result was accepted: %v", err)
	}
	invalidCases, err := repository.ListKYCVerificationCases(
		ctx, users[0].ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(invalidCases) == 0 ||
		invalidCases[0].State != accountdomain.KYCStateCancelled ||
		invalidCases[0].FailureCode != "INVALID_PROVIDER_RESULT" {
		t.Fatalf(
			"invalid provider Start did not terminalize its case: %#v",
			invalidCases,
		)
	}
	invalidOperations, err := repository.ListLatestKYCProviderOperations(
		ctx, users[0].ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	var invalidOperation *accountdomain.KYCProviderOperation
	for index := range invalidOperations {
		if invalidOperations[index].CaseID == invalidCases[0].ID {
			invalidOperation = &invalidOperations[index]
			break
		}
	}
	if invalidOperation == nil ||
		invalidOperation.State != accountdomain.KYCOperationFailed ||
		invalidOperation.FailureCode != "INVALID_PROVIDER_RESULT" ||
		invalidOperation.Retryable {
		t.Fatalf(
			"latest provider failure projection source mismatch: %#v",
			invalidOperations,
		)
	}
	recoveryService := accountapp.NewKYCService(
		repository,
		mockdojang.Provider{},
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		integrationClock{now: now.Add(7 * time.Minute)},
		ids,
	)
	recoveredStart, err := recoveryService.Start(
		ctx,
		accountapp.StartKYCInput{
			UserID:            string(users[0].ID),
			WalletID:          string(completions[0].Wallet.ID),
			OwnershipProofID:  string(completions[0].OwnershipProof.ID),
			ClientOperationID: "kyc-start-after-invalid-provider-result",
		},
	)
	if err != nil ||
		recoveredStart.Case.State != accountdomain.KYCStatePendingProvider {
		t.Fatalf(
			"fresh KYC Start could not recover after invalid provider result: %#v err=%v",
			recoveredStart, err,
		)
	}

	assertScopedKYCRowCount(
		t, ctx, database, "kyc_provider_operations", users, 9,
	)
	assertScopedKYCRowCount(
		t, ctx, database, "kyc_provider_operation_aliases", users, 3,
	)
	assertScopedKYCRowCount(t, ctx, database, "kyc_credentials", users, 3)
	assertScopedKYCRowCount(
		t, ctx, database, "kyc_evidence_observations", users, 3,
	)
}

func TestPostgresAccountRateLimitIsDurableConcurrentAndAtomic(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx, "../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	lockConnection, err := database.DB.Conn(ctx)
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
	if _, err := database.DB.ExecContext(ctx,
		`TRUNCATE account_rate_limit_buckets`,
	); err != nil {
		t.Fatal(err)
	}

	const secret = "integration-account-rate-limit-secret"
	limiter, err := accountpostgres.NewRateLimiter(database, secret, 7)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	rule := accountapp.RateLimitRule{
		Policy: "integration_restart", Dimension: "source",
		Subject: "203.0.113.7", Limit: 2, Window: 10 * time.Minute,
	}
	for index := 0; index < 2; index++ {
		decision, err := limiter.Acquire(ctx, []accountapp.RateLimitRule{rule}, now)
		if err != nil || !decision.Allowed {
			t.Fatalf("allowed hit %d decision=%#v err=%v", index, decision, err)
		}
	}
	restarted, err := accountpostgres.NewRateLimiter(database, secret, 7)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := restarted.Acquire(ctx, []accountapp.RateLimitRule{rule}, now)
	if err != nil || decision.Allowed || decision.RetryAfter != 10*time.Minute {
		t.Fatalf("restart decision=%#v err=%v", decision, err)
	}
	var hashLength int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT octet_length(subject_hash)
		FROM account_rate_limit_buckets
		WHERE policy_id=$1
	`, rule.Policy).Scan(&hashLength); err != nil || hashLength != sha256.Size {
		t.Fatalf("stored subject hash length=%d err=%v", hashLength, err)
	}

	if _, err := database.DB.ExecContext(ctx,
		`TRUNCATE account_rate_limit_buckets`,
	); err != nil {
		t.Fatal(err)
	}
	concurrentRule := accountapp.RateLimitRule{
		Policy: "integration_concurrent", Dimension: "source",
		Subject: "198.51.100.8", Limit: 5, Window: time.Hour,
	}
	var wait sync.WaitGroup
	decisions := make(chan accountapp.RateLimitDecision, 12)
	errorsFound := make(chan error, 12)
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			decision, err := limiter.Acquire(
				ctx, []accountapp.RateLimitRule{concurrentRule}, now,
			)
			decisions <- decision
			errorsFound <- err
		}()
	}
	wait.Wait()
	close(decisions)
	close(errorsFound)
	allowed := 0
	for decision := range decisions {
		if decision.Allowed {
			allowed++
		}
	}
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	if allowed != concurrentRule.Limit {
		t.Fatalf("concurrent allowed=%d want=%d", allowed, concurrentRule.Limit)
	}

	if _, err := database.DB.ExecContext(ctx,
		`TRUNCATE account_rate_limit_buckets`,
	); err != nil {
		t.Fatal(err)
	}
	blockedRule := accountapp.RateLimitRule{
		Policy: "integration_atomic_b", Dimension: "source",
		Subject: "blocked", Limit: 1, Window: time.Hour,
	}
	if decision, err := limiter.Acquire(
		ctx, []accountapp.RateLimitRule{blockedRule}, now,
	); err != nil || !decision.Allowed {
		t.Fatalf("prime blocking bucket decision=%#v err=%v", decision, err)
	}
	newRule := accountapp.RateLimitRule{
		Policy: "integration_atomic_a", Dimension: "user",
		Subject: "new-subject", Limit: 10, Window: time.Hour,
	}
	decision, err = limiter.Acquire(
		ctx, []accountapp.RateLimitRule{newRule, blockedRule}, now,
	)
	if err != nil || decision.Allowed {
		t.Fatalf("atomic denial decision=%#v err=%v", decision, err)
	}
	var newBucketCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM account_rate_limit_buckets WHERE policy_id=$1
	`, newRule.Policy).Scan(&newBucketCount); err != nil || newBucketCount != 0 {
		t.Fatalf("partially committed bucket count=%d err=%v", newBucketCount, err)
	}
}

type invalidKYCStartProvider struct{}

func (invalidKYCStartProvider) Start(
	context.Context,
	accountapp.KYCProviderStartRequest,
) (accountapp.KYCProviderStartResult, error) {
	return accountapp.KYCProviderStartResult{
		State: accountdomain.KYCStateVerified,
	}, nil
}

func (invalidKYCStartProvider) Check(
	context.Context,
	accountapp.KYCProviderCheckRequest,
) (accountapp.KYCProviderCheckResult, error) {
	return accountapp.KYCProviderCheckResult{}, errors.New("unexpected KYC check")
}

type blockingKYCProvider struct {
	delegate     mockdojang.Provider
	mutex        sync.Mutex
	startKeys    []string
	checkKeys    []string
	startReached chan struct{}
	startRelease chan struct{}
	checkReached chan struct{}
	checkRelease chan struct{}
}

func newBlockingKYCProvider() *blockingKYCProvider {
	return &blockingKYCProvider{
		startReached: make(chan struct{}, 2),
		startRelease: make(chan struct{}),
		checkReached: make(chan struct{}, 2),
		checkRelease: make(chan struct{}),
	}
}

func (p *blockingKYCProvider) Start(
	ctx context.Context,
	request accountapp.KYCProviderStartRequest,
) (accountapp.KYCProviderStartResult, error) {
	p.mutex.Lock()
	p.startKeys = append(p.startKeys, request.ProviderRequestKey)
	p.mutex.Unlock()
	p.startReached <- struct{}{}
	select {
	case <-p.startRelease:
	case <-ctx.Done():
		return accountapp.KYCProviderStartResult{}, ctx.Err()
	}
	return p.delegate.Start(ctx, request)
}

func (p *blockingKYCProvider) Check(
	ctx context.Context,
	request accountapp.KYCProviderCheckRequest,
) (accountapp.KYCProviderCheckResult, error) {
	p.mutex.Lock()
	p.checkKeys = append(p.checkKeys, request.ProviderRequestKey)
	p.mutex.Unlock()
	p.checkReached <- struct{}{}
	select {
	case <-p.checkRelease:
	case <-ctx.Done():
		return accountapp.KYCProviderCheckResult{}, ctx.Err()
	}
	return p.delegate.Check(ctx, request)
}

func assertConcurrentKYCOperations(
	t *testing.T,
	ctx context.Context,
	repository *accountpostgres.Repository,
	wallet accountdomain.Wallet,
	proof accountdomain.WalletOwnershipProof,
	now time.Time,
) {
	t.Helper()
	provider := newBlockingKYCProvider()
	service := accountapp.NewKYCService(
		repository,
		provider,
		accountdomain.KYCProviderMockDojang,
		accountdomain.KYCEffectSimulated,
		integrationClock{now: now},
		sharedapp.UUIDGenerator{},
	)
	startInputs := []accountapp.StartKYCInput{
		{
			UserID: string(wallet.UserID), WalletID: string(wallet.ID),
			OwnershipProofID:  string(proof.ID),
			ClientOperationID: "concurrent-kyc-start-a",
		},
		{
			UserID: string(wallet.UserID), WalletID: string(wallet.ID),
			OwnershipProofID:  string(proof.ID),
			ClientOperationID: "concurrent-kyc-start-b",
		},
	}
	startResults := make([]accountapp.KYCStartResult, len(startInputs))
	startErrors := make([]error, len(startInputs))
	var wait sync.WaitGroup
	for index := range startInputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			startResults[index], startErrors[index] =
				service.Start(ctx, startInputs[index])
		}(index)
	}
	waitForProviderCalls(t, provider.startReached, len(startInputs))
	close(provider.startRelease)
	wait.Wait()
	for index, err := range startErrors {
		if err != nil {
			t.Fatalf("concurrent KYC start %d failed: %v", index, err)
		}
	}
	if startResults[0].Case.ID != startResults[1].Case.ID ||
		startResults[0].Case.State != accountdomain.KYCStatePendingProvider ||
		len(provider.startKeys) != 2 ||
		provider.startKeys[0] != provider.startKeys[1] {
		t.Fatalf(
			"concurrent KYC starts did not converge: results=%#v keys=%v",
			startResults, provider.startKeys,
		)
	}

	checkInputs := []accountapp.CheckKYCInput{
		{
			UserID:            string(wallet.UserID),
			CaseID:            startResults[0].Case.ID,
			ClientOperationID: "concurrent-kyc-check-a",
		},
		{
			UserID:            string(wallet.UserID),
			CaseID:            startResults[0].Case.ID,
			ClientOperationID: "concurrent-kyc-check-b",
		},
	}
	checkResults := make([]accountapp.KYCCheckResult, len(checkInputs))
	checkErrors := make([]error, len(checkInputs))
	for index := range checkInputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			checkResults[index], checkErrors[index] =
				service.Check(ctx, checkInputs[index])
		}(index)
	}
	waitForProviderCalls(t, provider.checkReached, len(checkInputs))
	close(provider.checkRelease)
	wait.Wait()
	for index, err := range checkErrors {
		if err != nil {
			t.Fatalf("concurrent KYC check %d failed: %v", index, err)
		}
	}
	if checkResults[0].Case.ID != checkResults[1].Case.ID ||
		checkResults[0].Case.State != accountdomain.KYCStateVerified ||
		checkResults[0].Credential == nil ||
		checkResults[1].Credential == nil ||
		checkResults[0].Credential.ID != checkResults[1].Credential.ID ||
		len(provider.checkKeys) != 2 ||
		provider.checkKeys[0] != provider.checkKeys[1] {
		t.Fatalf(
			"concurrent KYC checks did not converge: results=%#v keys=%v",
			checkResults, provider.checkKeys,
		)
	}
	replayed, err := service.Check(ctx, checkInputs[1])
	if err != nil || !replayed.Replay ||
		replayed.Credential == nil ||
		replayed.Credential.ID != checkResults[0].Credential.ID {
		t.Fatalf(
			"concurrent KYC alias did not exact replay: %#v err=%v",
			replayed, err,
		)
	}
}

func waitForProviderCalls(
	t *testing.T,
	reached <-chan struct{},
	count int,
) {
	t.Helper()
	for index := 0; index < count; index++ {
		select {
		case <-reached:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for provider call %d/%d", index+1, count)
		}
	}
}

func newWalletRegistrationIntegrationService(
	repository *accountpostgres.Repository,
	now time.Time,
) *accountapp.WalletVerificationService {
	return accountapp.NewWalletVerificationService(
		repository,
		accountinfra.CryptoSecretGenerator{},
		integrationClock{now: now},
		sharedapp.UUIDGenerator{},
		"https://test.vitlane.example",
		"eip155:91342",
	)
}

func registerWallet(
	t *testing.T,
	ctx context.Context,
	repository *accountpostgres.Repository,
	userID accountdomain.UserID,
	privateKey *ecdsa.PrivateKey,
	now time.Time,
	registerOperationID string,
	completeOperationID string,
) accountapp.WalletRegistrationCompletionResult {
	t.Helper()
	service := newWalletRegistrationIntegrationService(repository, now)
	input := accountapp.CreateRegistrationAttemptInput{
		UserID:            string(userID),
		Address:           crypto.PubkeyToAddress(privateKey.PublicKey).Hex(),
		ChainID:           "eip155:91342",
		ClientOperationID: registerOperationID,
	}
	attempt, err := service.CreateRegistrationAttempt(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Replay {
		t.Fatalf("first registration attempt unexpectedly replayed: %#v", attempt)
	}
	attemptReplay, err := service.CreateRegistrationAttempt(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !attemptReplay.Replay ||
		attemptReplay.Attempt.ID != attempt.Attempt.ID ||
		attemptReplay.Attempt.Nonce != attempt.Attempt.Nonce ||
		attemptReplay.Attempt.Message != attempt.Attempt.Message {
		t.Fatalf(
			"registration attempt exact replay mismatch: first=%#v replay=%#v",
			attempt, attemptReplay,
		)
	}
	signature, err := crypto.Sign(
		accounts.TextHash([]byte(attempt.Attempt.Message)),
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	completeInput := accountapp.CompleteRegistrationAttemptInput{
		UserID:            string(userID),
		AttemptID:         string(attempt.Attempt.ID),
		Nonce:             attempt.Attempt.Nonce,
		Signature:         hexutil.Encode(signature),
		ClientOperationID: completeOperationID,
	}
	completed, err := service.CompleteRegistrationAttempt(ctx, completeInput)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Replay {
		t.Fatalf("first registration completion unexpectedly replayed: %#v", completed)
	}
	completionReplay, err := service.CompleteRegistrationAttempt(ctx, completeInput)
	if err != nil {
		t.Fatal(err)
	}
	if !completionReplay.Replay ||
		completionReplay.Wallet.ID != completed.Wallet.ID ||
		completionReplay.OwnershipProof.ID != completed.OwnershipProof.ID {
		t.Fatalf(
			"registration completion exact replay mismatch: first=%#v replay=%#v",
			completed, completionReplay,
		)
	}
	if completed.Wallet.CurrentOwnershipProofID == nil ||
		*completed.Wallet.CurrentOwnershipProofID != completed.OwnershipProof.ID ||
		completed.OwnershipProof.ValidUntil.Sub(
			completed.OwnershipProof.VerifiedAt,
		) != accountdomain.WalletOwnershipProofLifetime {
		t.Fatalf("registered Wallet/ownership proof mismatch: %#v", completed)
	}
	return completed
}

func assertConcurrentRegistrationCompletion(
	t *testing.T,
	ctx context.Context,
	repository *accountpostgres.Repository,
	userID accountdomain.UserID,
	privateKey *ecdsa.PrivateKey,
	now time.Time,
) accountapp.WalletRegistrationCompletionResult {
	t.Helper()
	service := newWalletRegistrationIntegrationService(repository, now)
	attempt, err := service.CreateRegistrationAttempt(
		ctx,
		accountapp.CreateRegistrationAttemptInput{
			UserID:            string(userID),
			Address:           crypto.PubkeyToAddress(privateKey.PublicKey).Hex(),
			ChainID:           "eip155:91342",
			ClientOperationID: "concurrent-registration-attempt",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := crypto.Sign(
		accounts.TextHash([]byte(attempt.Attempt.Message)),
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	input := accountapp.CompleteRegistrationAttemptInput{
		UserID:            string(userID),
		AttemptID:         string(attempt.Attempt.ID),
		Nonce:             attempt.Attempt.Nonce,
		Signature:         hexutil.Encode(signature),
		ClientOperationID: "concurrent-registration-completion",
	}
	results := make([]accountapp.WalletRegistrationCompletionResult, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errs[index] =
				service.CompleteRegistrationAttempt(ctx, input)
		}(index)
	}
	close(start)
	wait.Wait()
	replays := 0
	for index := range results {
		if errs[index] != nil {
			t.Fatalf(
				"concurrent completion %d failed: %v", index, errs[index],
			)
		}
		if results[index].Replay {
			replays++
		}
	}
	if replays != 1 ||
		results[0].Wallet.ID != results[1].Wallet.ID ||
		results[0].OwnershipProof.ID != results[1].OwnershipProof.ID {
		t.Fatalf("concurrent completion did not converge: %#v", results)
	}
	return results[0]
}

func assertConcurrentFirstWalletDefaults(
	t *testing.T,
	ctx context.Context,
	repository *accountpostgres.Repository,
	userID accountdomain.UserID,
	privateKeys []*ecdsa.PrivateKey,
	now time.Time,
) {
	t.Helper()
	service := newWalletRegistrationIntegrationService(repository, now)
	inputs := make([]accountapp.CompleteRegistrationAttemptInput, len(privateKeys))
	for index, privateKey := range privateKeys {
		attempt, err := service.CreateRegistrationAttempt(
			ctx,
			accountapp.CreateRegistrationAttemptInput{
				UserID:  string(userID),
				Address: crypto.PubkeyToAddress(privateKey.PublicKey).Hex(),
				ChainID: "eip155:91342",
				ClientOperationID: fmt.Sprintf(
					"first-wallet-attempt-%d", index,
				),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		signature, err := crypto.Sign(
			accounts.TextHash([]byte(attempt.Attempt.Message)),
			privateKey,
		)
		if err != nil {
			t.Fatal(err)
		}
		inputs[index] = accountapp.CompleteRegistrationAttemptInput{
			UserID:    string(userID),
			AttemptID: string(attempt.Attempt.ID),
			Nonce:     attempt.Attempt.Nonce,
			Signature: hexutil.Encode(signature),
			ClientOperationID: fmt.Sprintf(
				"first-wallet-completion-%d", index,
			),
		}
	}
	errs := make([]error, len(inputs))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range inputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, errs[index] = service.CompleteRegistrationAttempt(
				ctx, inputs[index],
			)
		}(index)
	}
	close(start)
	wait.Wait()
	successes := 0
	conflicts := 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, accountdomain.ErrWalletAlreadyRegistered):
			conflicts++
		default:
			t.Fatalf("concurrent first Wallet failed unexpectedly: %v", err)
		}
	}
	wallets, err := repository.ListWallets(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if successes != 1 || conflicts != 1 ||
		len(wallets) != 1 || !wallets[0].IsDefault {
		t.Fatalf(
			"concurrent first Wallet invariant failed: wallets=%#v errors=%v",
			wallets, errs,
		)
	}
}

func assertExactlyOneDefaultWallet(
	t *testing.T,
	wallets []accountdomain.Wallet,
	expected accountdomain.WalletID,
	stage string,
) {
	t.Helper()
	var defaults []accountdomain.WalletID
	for _, wallet := range wallets {
		if wallet.IsDefault {
			defaults = append(defaults, wallet.ID)
		}
	}
	if len(defaults) != 1 || defaults[0] != expected {
		t.Fatalf("%s default Wallets=%v, want only %s", stage, defaults, expected)
	}
}

func assertScopedKYCRowCount(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	table string,
	users []accountdomain.User,
	expected int,
) {
	t.Helper()
	var count int
	query := "SELECT COUNT(*) FROM " + table + " WHERE user_id IN ($1,$2)"
	if err := database.DB.QueryRowContext(
		ctx, query, users[0].ID, users[1].ID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("%s scoped row count=%d, want %d", table, count, expected)
	}
}

func bytesOf(value byte) []byte {
	result := make([]byte, 32)
	for index := range result {
		result[index] = value
	}
	return result
}

var _ accountapp.AuthRepository = (*accountpostgres.Repository)(nil)
var _ accountapp.WalletVerificationRepository = (*accountpostgres.Repository)(nil)
var _ accountapp.KYCRepository = (*accountpostgres.Repository)(nil)

// The development profile reset is a hand-written dependency-ordered purge of
// the live schema, and nothing else executes it: the app tests use a memory
// double. This fixture includes immutable Live-readiness evidence and its
// RESTRICT graph so schema additions fail here instead of late in browser E2E.
func TestPostgresResetDevelopmentUserMatchesSchema(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	database, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx, "../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	lockConnection, err := database.DB.Conn(ctx)
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

	repository := accountpostgres.NewRepository(database)
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	userID := "e7000000-0000-4000-8000-000000000001"
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO users(id, status, email, display_name, created_at, updated_at)
		VALUES ($1,'ACTIVE','reset@example.com','Reset',$2,$2)
		ON CONFLICT (id) DO NOTHING
	`, userID, now); err != nil {
		t.Fatal(err)
	}
	// A prior failed run can leave the deterministic fixture behind in the
	// shared integration database. Exercise the same dependency-ordered reset
	// before seeding so reruns remain deterministic and new RESTRICT edges still
	// fail through the product path rather than as duplicate fixture keys.
	if err := repository.ResetDevelopmentUser(
		ctx, accountdomain.UserID(userID), now,
	); err != nil {
		t.Fatalf("clean stale reset fixture: %v", err)
	}
	// The daily ledger is keyed by (scope, scope_id). Only the USER row is the
	// profile's; the SERVER row is the deployment's own spend.
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO managed_runner_usage_daily(
			usage_date, scope, scope_id, reserved_micros, settled_micros,
			request_count, updated_at
		) VALUES
			($1,'USER',$2,10,10,1,$3),
			($1,'SERVER','SERVER',10,10,1,$3)
		ON CONFLICT (usage_date, scope, scope_id) DO NOTHING
	`, now, userID, now); err != nil {
		t.Fatal(err)
	}
	for index, statement := range strings.Split(`
		INSERT INTO shipping_profiles(
			id,user_id,label,country,masked_summary,encrypted_payload,payload_nonce,
			key_version,payload_hmac,profile_version,is_default,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000010',$1,'reset order','US','US ***01',
			'\xdeadbeef','\x0102030405060708090a0b0c','test','reset-hmac',1,true,$2,$2
		);
		INSERT INTO shipping_snapshots(
			id,user_id,source_profile_id,profile_version,country,masked_summary,
			encrypted_payload,payload_nonce,key_version,snapshot_hmac,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000011',$1,
			'e7000000-0000-4000-8000-000000000010',1,'US','US ***01',
			'\xdeadbeef','\x0102030405060708090a0b0c','test','reset-snapshot-hmac',$2
		);
		INSERT INTO agency_order_sheet_sessions(
			id,user_id,source_cart_id,source_cart_version,source_cart_snapshot_hash,
			state,version,creation_key_hash,creation_request_hash,snapshot,
			created_at,expires_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000012',$1,
			'e7000000-0000-4000-8000-000000000013',1,'reset-cart-hash',
			'CONSUMED',2,'reset-creation-key','reset-request-hash','{}'::jsonb,
			$2,$2::timestamptz + interval '20 minutes',$2
		);
		INSERT INTO agency_order_provider_capabilities(
			id,user_id,order_sheet_session_id,shop_domain,capability_kind,
			encrypted_payload,payload_nonce,key_version,payload_hmac,created_at,expires_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000014',$1,
			'e7000000-0000-4000-8000-000000000012','reset.example','STOREFRONT_CART',
			'\xdeadbeef','\x0102030405060708090a0b0c','test','reset-capability-hmac',
			$2,$2::timestamptz + interval '20 minutes'
		);
		INSERT INTO agency_orders(
			id,user_id,order_sheet_session_id,source_cart_id,source_cart_version,
			source_cart_snapshot_hash,shipping_snapshot_id,snapshot_hash,
			idempotency_key_hash,status,customer_payable_minor,currency,snapshot,
			payment_rail,provider_environment,asset,economic_effect,
			merchant_execution_mode,execution_profile_hash,issued_at,expires_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000015',$1,
			'e7000000-0000-4000-8000-000000000012',
			'e7000000-0000-4000-8000-000000000013',1,'reset-cart-hash',
			'e7000000-0000-4000-8000-000000000011','reset-order-hash',
			'reset-order-key','ISSUED',1010,'USD','{}'::jsonb,
			'GIWA','TESTNET','TVITUSD','NO_REAL_VALUE','SIMULATED_NO_EFFECT',
			'0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732',
			$2,$2::timestamptz + interval '20 minutes'
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO agency_order_mo_allocations(
			id,agency_order_id,checkout_ordinal,merchant_id,shop_domain,
			pass_through_minor,fee_variable_minor,fee_fixed_minor,fee_total_minor,
			customer_gross_minor,currency,fee_policy_version,allocation_hash,
			execution_profile_hash,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000061',
			'e7000000-0000-4000-8000-000000000015',1,'reset.example',
			'reset.example',900,110,0,110,1010,'USD','TVIT_ORDER_1_V1',
			'0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee',
			'0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732',$2
		);
		INSERT INTO ordering_operator_order_lookup_audits(
			id,action,operator_user_id,identifier_type,environment,query_hash,
			qualifier_hash,outcome,matched_by,agency_order_id,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000057','LOOKUP',$1,
			'AGENCY_ORDER_ID','TESTNET',repeat('a',64),NULL,'MATCHED',
			'AGENCY_ORDER_ID','e7000000-0000-4000-8000-000000000015',$2
		);
		INSERT INTO agency_order_procurement_authorizations(
			agency_order_id,user_id,order_sheet_session_id,authorization_kind,
			authorization_hash,execution_profile_hash,source_cart_snapshot_hash,
			displayed_snapshot_hash,order_snapshot_hash,locale,copy_version,
			accepted_at,payload,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000015',$1,
			'e7000000-0000-4000-8000-000000000012','MANUAL_OPERATOR_PURCHASE',
			'reset-authorization-hash-0000000000000001',
			'0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732',
			'reset-cart-hash','reset-display-hash','reset-order-hash',
			'en-US','procurement-authorization.v1',$2,
			jsonb_build_object(
				'kind','MANUAL_OPERATOR_PURCHASE',
				'agencyOrderId','e7000000-0000-4000-8000-000000000015',
				'shops',jsonb_build_array(jsonb_build_object(
					'shopDomain','reset.example')),
				'approvedPassThrough',jsonb_build_object(
					'amountMinor',900,'currency','USD'),
				'approvedAgencyFee',jsonb_build_object(
					'amountMinor',110,'currency','USD'),
				'approvedCustomerPayable',jsonb_build_object(
					'amountMinor',1010,'currency','USD'),
				'customerApproval',jsonb_build_object(
					'agencyConsent',true,'privacyConsent',true,
					'locale','en-US','copyVersion','procurement-authorization.v1'),
				'orderSheetSessionId','e7000000-0000-4000-8000-000000000012',
				'sourceCartSnapshotHash','reset-cart-hash',
				'displayedSnapshotHash','reset-display-hash',
				'orderSnapshotHash','reset-order-hash',
				'executionProfileHash',
					'0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732',
				'acceptedAt',to_jsonb($2::timestamptz),
				'authorizationHash','reset-authorization-hash-0000000000000001'
			),$2
		);
		INSERT INTO agency_order_payment_instructions(
			id,agency_order_id,user_id,agency_order_snapshot_hash,amount_minor,currency,
			rail,asset,provider_environment,economic_effect,merchant_execution_mode,
			execution_profile_hash,state,payload,expires_at,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000016',
			'e7000000-0000-4000-8000-000000000015',$1,'reset-order-hash',1010,
			'USD','GIWA','TVITUSD','TESTNET','NO_REAL_VALUE','SIMULATED_NO_EFFECT',
			'0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732',
			'PENDING','{}'::jsonb,$2::timestamptz + interval '20 minutes',$2
		);
		INSERT INTO agency_order_audits(
			agency_order_id,user_id,action,order_snapshot_hash,displayed_snapshot_hash,
			disclosure_version,idempotency_key_hash,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000015',$1,'ISSUED','reset-order-hash',
			'reset-display-hash','reset-disclosure','reset-order-key',$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO settlement_authorizations(
			id,agency_order_id,order_hash,payer,nonce,pay_deadline,
			refund_after,signer_address,typed_data_hash,authorization_payload,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000018',
			'e7000000-0000-4000-8000-000000000015','reset-settlement-order',
			'0x1111111111111111111111111111111111111111',1,$2::timestamptz + interval '10 minutes',
			$2::timestamptz + interval '70 minutes','0x2222222222222222222222222222222222222222',
			'reset-typed-data','{}'::jsonb,$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO settlement_payments(
			id,agency_order_id,order_hash,chain_id,settlement_address,payer,
			amount_base_units,state,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000019',
			'e7000000-0000-4000-8000-000000000015','reset-settlement-order',91342,
			'0x3333333333333333333333333333333333333333',
			'0x1111111111111111111111111111111111111111',10100000,'AUTHORIZED',$2,$2
		);
		INSERT INTO payment_customer_payments(
			id,agency_order_id,user_id,rail,provider_environment,asset,
			economic_effect,amount_minor,currency,state,merchant_execution_mode,
			execution_profile_hash,version,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000024',
			'e7000000-0000-4000-8000-000000000015',$1,
			'GIWA','TESTNET','TVITUSD','NO_REAL_VALUE',1010,'USD','CAPTURED',
			'SIMULATED_NO_EFFECT',
			'0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732',
			1,$2,$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO payment_funds_receipts(
			id,customer_payment_id,agency_order_id,kind,provider_environment,
			capture_id,paypal_order_id,amount_minor,currency,accepted,occurred_at,
			created_at,order_hash,pay_tx_hash,execution_profile_hash
		) VALUES(
			'e7000000-0000-4000-8000-000000000025',
			'e7000000-0000-4000-8000-000000000024',
			'e7000000-0000-4000-8000-000000000015','GIWA_FINALIZED_PAY','TESTNET',
			NULL,NULL,1010,'USD',true,$2,$2,
			'0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
			'0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
			'0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732'
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO payment_mo_funding_positions(
			id,allocation_id,agency_order_id,customer_payment_id,rail,source,
			provider_environment,amount_minor,currency,execution_profile_hash,state,
			version,available_at,activated_at,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000062',
			'e7000000-0000-4000-8000-000000000061',
			'e7000000-0000-4000-8000-000000000015',
			'e7000000-0000-4000-8000-000000000024','GIWA','GIWA_PREPAID',
			'TESTNET',1010,'USD',
			'0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732',
			'ACTIVE',1,$2,$2,$2,$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO procurement_manifests(
			id,agency_order_id,funds_receipt_id,snapshot_hash,
			execution_profile_hash,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000026',
			'e7000000-0000-4000-8000-000000000015',
			'e7000000-0000-4000-8000-000000000025','reset-order-hash',
			'0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732',$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO merchant_orders(
			id,agency_order_id,manifest_id,allocation_id,merchant_id,shop_domain,checkout_ordinal,
			checkout_snapshot,execution_mode,state,version,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000027',
			'e7000000-0000-4000-8000-000000000015',
			'e7000000-0000-4000-8000-000000000026',
			'e7000000-0000-4000-8000-000000000061','reset.example','reset.example',1,
			'{}'::jsonb,'SIMULATED_NO_EFFECT','PLANNED',1,$2,$2
		);
		INSERT INTO merchant_order_execution_tasks(
			id,merchant_order_id,agency_order_id,state,assigned_operator_user_id,
			assigned_at,lease_until,version,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000028',
			'e7000000-0000-4000-8000-000000000027',
			'e7000000-0000-4000-8000-000000000015','CLAIMED',$1,$2,
			$2::timestamptz + interval '30 days',1,$2,$2
		);
		INSERT INTO procurement_decision_records(
			id,merchant_order_id,agency_order_id,task_id,decision,public_rationale,
			internal_note,observed_condition,evidence_source,evidence_hash,
			observed_at,decided_by_user_id,task_version,authorization_hash,
			execution_profile_hash,idempotency_key,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000029',
			'e7000000-0000-4000-8000-000000000027',
			'e7000000-0000-4000-8000-000000000015',
			'e7000000-0000-4000-8000-000000000028','WITHIN_AUTHORIZATION',
			'The approved product conditions still match.',NULL,
			'The merchant page was manually reviewed.','MERCHANT_PAGE',
			'reset-procurement-evidence-hash',$2,$1,1,
			'reset-authorization-hash-0000000000000001',
			'0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732',
			'reset-procurement-decision-key',$2
		);
		INSERT INTO procurement_customer_requests(
			id,merchant_order_id,agency_order_id,user_id,kind,prompt,response_type,
			response_options,public_context,state,response,requested_by_user_id,
			requested_at,due_at,resolved_at,resolved_by_user_id,resolution_reason,
			source_decision_id,idempotency_key,resolution_idempotency_key,
			resolution_request_hash,version,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000030',
			'e7000000-0000-4000-8000-000000000027',
			'e7000000-0000-4000-8000-000000000015',$1,'CONSENT',
			'Please confirm the newly observed condition.','BOOLEAN_CONSENT',
			'[]'::jsonb,'A material condition needs confirmation.','ANSWERED',
			'{"accepted":true}'::jsonb,$1,$2,$2::timestamptz + interval '7 days',
			$2::timestamptz + interval '1 hour',$1,'Customer accepted the condition.',
			'e7000000-0000-4000-8000-000000000029',
			'reset-procurement-request-key','reset-procurement-resolution-key',
			'reset-procurement-resolution-hash',1,$2,$2::timestamptz + interval '1 hour'
		);
		INSERT INTO procurement_effect_locks(
			merchant_order_id,task_id,decision_record_id,authorization_hash,
			execution_profile_hash,operator_user_id,state,idempotency_key,started_at,
			funding_position_id,funding_state,funding_requested_at,funding_resolved_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000027',
			'e7000000-0000-4000-8000-000000000028',
			'e7000000-0000-4000-8000-000000000029',
			'reset-authorization-hash-0000000000000001',
			'0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732',
			$1,'STARTED','reset-merchant-effect-key',$2,
			'e7000000-0000-4000-8000-000000000062','FUNDED',$2,$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO agency_order_processes(
			agency_order_id,state,version,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000015','WAITING_CUSTOMER_PAYMENT',1,$2,$2
		);
		INSERT INTO agency_order_execution_units(
			id,agency_order_id,settlement_payment_id,merchant_id,shop_domain,
			checkout_ordinal,checkout_snapshot,state,assigned_operator_user_id,
			assigned_at,external_effect,merchant_order_created,
			merchant_order_flow_completed,version,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000020',
			'e7000000-0000-4000-8000-000000000015',
			'e7000000-0000-4000-8000-000000000019','reset.example',
			'reset.example',1,'{}'::jsonb,'RUNNING',$1,$2,'SIMULATED',false,false,1,$2,$2
		);
		INSERT INTO agency_order_execution_audits(
			id,agency_order_id,execution_unit_id,actor_user_id,action,
			idempotency_key,details,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000021',
			'e7000000-0000-4000-8000-000000000015',
			'e7000000-0000-4000-8000-000000000020',$1,'OPERATOR_ASSIGNED',
			'reset-assignment-key','{}'::jsonb,$2
		);
		INSERT INTO agency_order_pii_access_audits(
			id,agency_order_id,execution_unit_id,shipping_snapshot_id,actor_user_id,
			action,reason_code,reason_detail,outcome,correlation_id,idempotency_key,
			event_hash,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000022',
			'e7000000-0000-4000-8000-000000000015',
			'e7000000-0000-4000-8000-000000000020',
			'e7000000-0000-4000-8000-000000000011',$1,
			'SHIPPING_ADDRESS_REVEAL','PLACE_MERCHANT_ORDER',
			'reset integration evidence','GRANTED','reset-correlation',
			'reset-pii-key','reset-event-hash',$2
		);
		INSERT INTO support_messages(
			id,user_id,author,body,agency_order_id,created_by_user_id,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000023',$1,'OPERATOR',
			'reset support reply','e7000000-0000-4000-8000-000000000015',$1,$2
		);
		INSERT INTO support_image_attachments(
			id,message_id,user_id,ordinal,media_type,width,height,byte_size,
			content_sha256,ciphertext,nonce,key_version,ciphertext_fingerprint,
			created_at,retention_until
		) VALUES(
			'e7000000-0000-4000-8000-000000000040',
			'e7000000-0000-4000-8000-000000000023',$1,1,'image/png',1,1,4,
			'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
			'\xdeadbeef','\x01020304','test','reset-image-fingerprint',$2,
			$2::timestamptz + interval '30 days'
		);
		INSERT INTO support_messages(
			id,user_id,author,body,agency_order_id,created_at,content_kind,
			business_card_type,business_reference_type,business_reference_id,
			action_required,public_payload,idempotency_namespace,idempotency_key
		) VALUES(
			'e7000000-0000-4000-8000-000000000041',$1,'SYSTEM',
			'Procurement input is required.',
			'e7000000-0000-4000-8000-000000000015',$2,'BUSINESS_CARD',
			'PROCUREMENT_REQUEST','PROCUREMENT','reset-open-action',true,
			'{}'::jsonb,'BUSINESS_CARD','reset-open-card-key'
		);
		INSERT INTO support_open_business_actions(
			user_id,reference_type,reference_id,card_message_id,opened_at
		) VALUES(
			$1,'PROCUREMENT','reset-open-action',
			'e7000000-0000-4000-8000-000000000041',$2
		);
		INSERT INTO support_messages(
			id,user_id,author,body,agency_order_id,created_at,content_kind,
			business_card_type,business_reference_type,business_reference_id,
			action_required,public_payload,idempotency_namespace,idempotency_key
		) VALUES(
			'e7000000-0000-4000-8000-000000000042',$1,'SYSTEM',
			'Refund review is required.',
			'e7000000-0000-4000-8000-000000000015',$2,'BUSINESS_CARD',
			'REFUND_REQUEST','REFUND','reset-closed-action',true,
			'{}'::jsonb,'BUSINESS_CARD','reset-closed-card-key'
		);
		INSERT INTO support_messages(
			id,user_id,author,body,agency_order_id,created_at,content_kind,
			business_card_type,business_reference_type,business_reference_id,
			action_required,resolves_card_id,public_payload,idempotency_namespace,
			idempotency_key
		) VALUES(
			'e7000000-0000-4000-8000-000000000043',$1,'SYSTEM',
			'Refund review was completed.',
			'e7000000-0000-4000-8000-000000000015',$2,'BUSINESS_CARD',
			'REFUND_DECISION','REFUND','reset-closed-action',false,
			'e7000000-0000-4000-8000-000000000042','{}'::jsonb,
			'BUSINESS_CARD','reset-resolution-card-key'
		);
		INSERT INTO agency_order_sheet_sessions(
			id,user_id,source_cart_id,source_cart_version,source_cart_snapshot_hash,
			state,version,creation_key_hash,creation_request_hash,snapshot,
			created_at,expires_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000050',$1,
			'e7000000-0000-4000-8000-000000000051',1,'reset-paypal-cart-hash',
			'CONSUMED',2,'reset-paypal-creation-key','reset-paypal-request-hash',
			'{}'::jsonb,$2,$2::timestamptz + interval '20 minutes',$2
		);
		INSERT INTO agency_orders(
			id,user_id,order_sheet_session_id,source_cart_id,source_cart_version,
			source_cart_snapshot_hash,shipping_snapshot_id,snapshot_hash,
			idempotency_key_hash,status,customer_payable_minor,currency,snapshot,
			payment_rail,provider_environment,asset,economic_effect,
			merchant_execution_mode,execution_profile_hash,issued_at,expires_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000052',$1,
			'e7000000-0000-4000-8000-000000000050',
			'e7000000-0000-4000-8000-000000000051',1,'reset-paypal-cart-hash',
			'e7000000-0000-4000-8000-000000000011','reset-paypal-order-hash',
			'reset-paypal-order-key','ISSUED',1010,'USD','{}'::jsonb,
			'PAYPAL','SANDBOX','USD','NO_REAL_VALUE','SIMULATED_NO_EFFECT',
			'0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665',
			$2,$2::timestamptz + interval '20 minutes'
		);
		INSERT INTO payment_customer_payments(
			id,agency_order_id,user_id,rail,provider_environment,asset,
			economic_effect,amount_minor,currency,state,merchant_execution_mode,
			execution_profile_hash,version,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000053',
			'e7000000-0000-4000-8000-000000000052',$1,
			'PAYPAL','SANDBOX','USD','NO_REAL_VALUE',1010,'USD','CAPTURED',
			'SIMULATED_NO_EFFECT',
			'0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665',
			1,$2,$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO payment_paypal_attempts(
			id,customer_payment_id,sequence,state,paypal_order_id,return_nonce,
			version,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000057',
			'e7000000-0000-4000-8000-000000000053',1,'AUTHORIZE_COMPLETED',
			'PAYPAL-ORDER-RESET-1','reset-paypal-return-nonce',1,$2,$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO agency_order_mo_allocations(
			id,agency_order_id,checkout_ordinal,merchant_id,shop_domain,
			pass_through_minor,fee_variable_minor,fee_fixed_minor,fee_total_minor,
			customer_gross_minor,currency,fee_policy_version,allocation_hash,
			execution_profile_hash,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000058',
			'e7000000-0000-4000-8000-000000000052',1,'reset-merchant',
			'reset.example',950,30,30,60,1010,'USD','PAYPAL_MO_5_4_FIXED_30_V1',
			'0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd',
			'0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665',$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO payment_paypal_authorizations(
			id,customer_payment_id,agency_order_id,paypal_attempt_id,rail,
			provider_environment,paypal_order_id,payee_merchant_id,
			paypal_authorization_id,amount_minor,currency,execution_profile_hash,
			state,version,authorized_at,honor_refreshed_at,reauthorization_count,
			terminal_at,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000059',
			'e7000000-0000-4000-8000-000000000053',
			'e7000000-0000-4000-8000-000000000052',
			'e7000000-0000-4000-8000-000000000057','PAYPAL','SANDBOX',
			'PAYPAL-ORDER-RESET-1','PAYEE-RESET-1','AUTH-RESET-1',1010,'USD',
			'0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665',
			'CAPTURED',1,$2::timestamptz - interval '4 days',
			$2::timestamptz - interval '4 days',0,$2,$2,$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO payment_mo_funding_positions(
			id,allocation_id,agency_order_id,customer_payment_id,
			paypal_authorization_id,rail,source,provider_environment,amount_minor,
			currency,execution_profile_hash,state,version,available_at,activated_at,
			created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000060',
			'e7000000-0000-4000-8000-000000000058',
			'e7000000-0000-4000-8000-000000000052',
			'e7000000-0000-4000-8000-000000000053',
			'e7000000-0000-4000-8000-000000000059','PAYPAL',
			'PAYPAL_AUTHORIZATION','SANDBOX',1010,'USD',
			'0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665',
			'ACTIVE',1,$2,$2,$2,$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO payment_mo_cash_receipts(
			id,funding_position_id,allocation_id,agency_order_id,
			customer_payment_id,paypal_authorization_id,provider_environment,kind,
			provider_capture_id,gross_minor,economics_reconciled,currency,
			execution_profile_hash,occurred_at,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000054',
			'e7000000-0000-4000-8000-000000000060',
			'e7000000-0000-4000-8000-000000000058',
			'e7000000-0000-4000-8000-000000000052',
			'e7000000-0000-4000-8000-000000000053',
			'e7000000-0000-4000-8000-000000000059','SANDBOX','PAYPAL_CAPTURE',
			'CAPTURE-RESET-1',1010,false,'USD',
			'0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665',$2,$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO procurement_manifests(
			id,agency_order_id,funds_receipt_id,customer_payment_id,snapshot_hash,
			execution_profile_hash,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000063',
			'e7000000-0000-4000-8000-000000000052',NULL,
			'e7000000-0000-4000-8000-000000000053','reset-paypal-manifest-hash',
			'0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665',$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO merchant_orders(
			id,agency_order_id,manifest_id,allocation_id,merchant_id,shop_domain,
			checkout_ordinal,checkout_snapshot,execution_mode,state,version,
			created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000064',
			'e7000000-0000-4000-8000-000000000052',
			'e7000000-0000-4000-8000-000000000063',
			'e7000000-0000-4000-8000-000000000058','reset-merchant',
			'reset.example',1,'{}'::jsonb,'SIMULATED_NO_EFFECT','PLANNED',1,$2,$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO payment_external_operations(
			id,purpose,owner_kind,owner_id,idempotency_key,request_hash,state,
			provider_resource_id,first_sent_at,idempotency_deadline,resolved_at,
			created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000065','PAYPAL_REAUTHORIZE',
			'PAYPAL_AUTHORIZATION','e7000000-0000-4000-8000-000000000059',
			'reset-paypal-reauthorize-key','reset-paypal-reauthorize-request',
			'SUCCEEDED','AUTH-RESET-ADOPTED-1',$2::timestamptz - interval '2 hours',
			$2::timestamptz - interval '1 hour',$2,$2::timestamptz - interval '2 hours',$2
		);
		INSERT INTO payment_paypal_reauthorization_adoptions(
			id,merchant_order_id,target_funding_position_id,paypal_authorization_id,
			operation_id,operation_owner_kind,provider_environment,
			previous_provider_authorization_id,provider_authorization_id,
			provider_status,operation_state,amount_minor,currency,paypal_order_id,
			payee_merchant_id,provider_created_at,actor_user_id,evidence_source,
			evidence_hash,internal_note,observed_at,operation_first_sent_at,
			operation_idempotency_deadline,original_authorized_at,
			idempotency_key_hash,request_hash,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000066',
			'e7000000-0000-4000-8000-000000000064',
			'e7000000-0000-4000-8000-000000000060',
			'e7000000-0000-4000-8000-000000000059',
			'e7000000-0000-4000-8000-000000000065','PAYPAL_AUTHORIZATION',
			'SANDBOX','AUTH-RESET-1','AUTH-RESET-ADOPTED-1','CREATED','SUCCEEDED',
			1010,'USD','PAYPAL-ORDER-RESET-1','PAYEE-RESET-1',
			$2::timestamptz - interval '90 minutes',$1,'PAYPAL_DASHBOARD',
			'0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd',
			'reset adoption',$2,$2::timestamptz - interval '2 hours',
			$2::timestamptz - interval '1 hour',$2::timestamptz - interval '4 days',
			'0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee',
			'0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff',$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO payment_mo_compensations(
			id,allocation_id,funding_position_id,agency_order_id,customer_payment_id,
			rail,provider_environment,action,cause,state,amount_minor,currency,
			execution_profile_hash,provider_resource_id,idempotency_key,version,
			approved_at,completed_at,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000067',
			'e7000000-0000-4000-8000-000000000058',
			'e7000000-0000-4000-8000-000000000060',
			'e7000000-0000-4000-8000-000000000052',
			'e7000000-0000-4000-8000-000000000053','PAYPAL','SANDBOX','REFUND',
			'DELIVERY_EXCEPTION','SUCCEEDED',1010,'USD',
			'0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665',
			'REFUND-RESET-ADOPTED-1','reset-paypal-refund-compensation',1,$2,$2,$2,$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO payment_external_operations(
			id,purpose,owner_kind,owner_id,idempotency_key,request_hash,state,
			provider_resource_id,first_sent_at,idempotency_deadline,resolved_at,
			created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000068','PAYPAL_MO_REFUND',
			'MO_COMPENSATION','e7000000-0000-4000-8000-000000000067',
			'reset-paypal-refund-key','reset-paypal-refund-request','SUCCEEDED',
			'REFUND-RESET-ADOPTED-1',$2::timestamptz - interval '2 hours',
			$2::timestamptz - interval '1 hour',$2,$2::timestamptz - interval '2 hours',$2
		);
		INSERT INTO payment_paypal_refund_adoptions(
			id,compensation_id,operation_id,operation_owner_kind,
			provider_environment,provider_refund_id,provider_status,outcome_state,
			amount_minor,currency,parent_capture_id,invoice_id,
			operation_first_sent_at,operation_idempotency_deadline,operator_user_id,
			public_rationale,evidence_source,evidence_hash,observed_at,request_hash,
			created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000069',
			'e7000000-0000-4000-8000-000000000067',
			'e7000000-0000-4000-8000-000000000068','MO_COMPENSATION','SANDBOX',
			'REFUND-RESET-ADOPTED-1','COMPLETED','SUCCEEDED',1010,'USD',
			'CAPTURE-RESET-1','reset-paypal-refund-key',
			$2::timestamptz - interval '2 hours',$2::timestamptz - interval '1 hour',$1,
			'PayPal activity identifies the original refund.','PAYPAL_DASHBOARD',
			'0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
			$2,'0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',$2
		);
		WITH owner AS (SELECT $1::uuid AS user_id)
		INSERT INTO payment_paypal_dispute_cases(
			id,environment,dispute_id,agency_order_id,customer_payment_id,
			mo_cash_receipt_id,capture_id,state,provider_status,outcome,reason,
			lifecycle_stage,latest_event_id,latest_event_type,opened_at,
			last_observed_at,version,created_at,updated_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000055','SANDBOX','PP-D-RESET-1',
			'e7000000-0000-4000-8000-000000000052',
			'e7000000-0000-4000-8000-000000000053',
			'e7000000-0000-4000-8000-000000000054','CAPTURE-RESET-1',
			'OPEN','WAITING_FOR_SELLER_RESPONSE','NONE','OTHER','INQUIRY',
			'WH-RESET-DISPUTE-1','CUSTOMER.DISPUTE.CREATED',$2,$2,1,$2,$2
		);
		INSERT INTO payment_paypal_dispute_manual_actions(
			id,dispute_case_id,action_kind,external_reference,public_rationale,
			internal_note,actor_user_id,observed_provider_status,evidence_source,
			evidence_hash,observed_at,idempotency_key_hash,request_hash,created_at
		) VALUES(
			'e7000000-0000-4000-8000-000000000056',
			'e7000000-0000-4000-8000-000000000055','CASE_OBSERVED',
			'PP-RC-RESET-1','The operator reviewed the PayPal case.','',$1,
			'WAITING_FOR_SELLER_RESPONSE','PAYPAL_RESOLUTION_CENTER',
			'0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
			$2,'0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
			'0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc',$2
		);
	`, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err := database.DB.ExecContext(ctx, statement, userID, now); err != nil {
			t.Fatalf("seed AgencyOrder reset fixture statement %d: %v", index, err)
		}
	}
	if _, err := database.DB.ExecContext(ctx, `
		DELETE FROM agency_order_procurement_authorizations
		WHERE agency_order_id='e7000000-0000-4000-8000-000000000015'
	`); err == nil {
		t.Fatal("ordinary SQL must not delete immutable procurement authorization")
	}
	if _, err := database.DB.ExecContext(ctx, `
		DELETE FROM payment_paypal_dispute_manual_actions
		WHERE id='e7000000-0000-4000-8000-000000000056'
	`); err == nil {
		t.Fatal("ordinary SQL must not delete append-only PayPal dispute evidence")
	}
	if _, err := database.DB.ExecContext(ctx, `
		DELETE FROM payment_paypal_refund_adoptions
		WHERE id='e7000000-0000-4000-8000-000000000069'
	`); err == nil {
		t.Fatal("ordinary SQL must not delete append-only PayPal refund adoption")
	}
	if _, err := database.DB.ExecContext(ctx, `
		DELETE FROM payment_paypal_reauthorization_adoptions
		WHERE id='e7000000-0000-4000-8000-000000000066'
	`); err == nil {
		t.Fatal("ordinary SQL must not delete append-only PayPal reauthorization adoption")
	}

	if err := repository.ResetDevelopmentUser(
		ctx, accountdomain.UserID(userID), now,
	); err != nil {
		t.Fatalf("reset development user: %v", err)
	}

	var userScoped, serverScoped int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT
			count(*) FILTER (WHERE scope='USER' AND scope_id=$1),
			count(*) FILTER (WHERE scope='SERVER')
		FROM managed_runner_usage_daily
	`, userID).Scan(&userScoped, &serverScoped); err != nil {
		t.Fatal(err)
	}
	if userScoped != 0 || serverScoped == 0 {
		t.Fatalf(
			"reset must clear the user's spend and keep the server's: user=%d server=%d",
			userScoped, serverScoped,
		)
	}
	var agencyRows int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM agency_order_sheet_sessions WHERE user_id=$1) +
			(SELECT count(*) FROM agency_orders WHERE user_id=$1) +
			(SELECT count(*) FROM agency_order_procurement_authorizations WHERE user_id=$1) +
			(SELECT count(*) FROM agency_order_payment_instructions WHERE user_id=$1) +
			(SELECT count(*) FROM agency_order_provider_capabilities WHERE user_id=$1) +
			(SELECT count(*) FROM settlement_authorizations
			 WHERE id='e7000000-0000-4000-8000-000000000018') +
			(SELECT count(*) FROM settlement_payments
			 WHERE id='e7000000-0000-4000-8000-000000000019') +
			(SELECT count(*) FROM agency_order_processes
			 WHERE agency_order_id='e7000000-0000-4000-8000-000000000015') +
			(SELECT count(*) FROM agency_order_execution_units
			 WHERE agency_order_id='e7000000-0000-4000-8000-000000000015') +
			(SELECT count(*) FROM agency_order_execution_audits
			 WHERE agency_order_id='e7000000-0000-4000-8000-000000000015') +
			(SELECT count(*) FROM agency_order_pii_access_audits
			 WHERE agency_order_id='e7000000-0000-4000-8000-000000000015') +
			(SELECT count(*) FROM ordering_operator_order_lookup_audits
			 WHERE operator_user_id=$1
			    OR agency_order_id='e7000000-0000-4000-8000-000000000015') +
			(SELECT count(*) FROM procurement_decision_records
			 WHERE agency_order_id='e7000000-0000-4000-8000-000000000015') +
			(SELECT count(*) FROM procurement_customer_requests WHERE user_id=$1) +
			(SELECT count(*) FROM procurement_effect_locks
			 WHERE merchant_order_id='e7000000-0000-4000-8000-000000000027') +
			(SELECT count(*) FROM payment_paypal_dispute_cases
			 WHERE agency_order_id='e7000000-0000-4000-8000-000000000052') +
			(SELECT count(*) FROM payment_paypal_dispute_manual_actions
			 WHERE dispute_case_id='e7000000-0000-4000-8000-000000000055') +
			(SELECT count(*) FROM payment_paypal_refund_adoptions
			 WHERE id='e7000000-0000-4000-8000-000000000069') +
			(SELECT count(*) FROM payment_paypal_reauthorization_adoptions
			 WHERE id='e7000000-0000-4000-8000-000000000066') +
			(SELECT count(*) FROM support_open_business_actions WHERE user_id=$1) +
			(SELECT count(*) FROM support_image_attachments WHERE user_id=$1) +
			(SELECT count(*) FROM support_messages
			 WHERE user_id=$1 OR created_by_user_id=$1)
	`, userID).Scan(&agencyRows); err != nil {
		t.Fatal(err)
	}
	if agencyRows != 0 {
		t.Fatalf("reset left AgencyOrder graph rows=%d", agencyRows)
	}
	var resetSetting sql.NullString
	if err := database.DB.QueryRowContext(ctx,
		`SELECT current_setting('vitlane.account_reset', true)`,
	).Scan(&resetSetting); err != nil {
		t.Fatal(err)
	}
	if resetSetting.Valid && resetSetting.String == "on" {
		t.Fatal("development account reset guard leaked outside its transaction")
	}
	var status string
	if err := database.DB.QueryRowContext(ctx,
		`SELECT status FROM users WHERE id=$1`, userID,
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "ACTIVE" {
		t.Fatalf("reset must leave the user ACTIVE, got %q", status)
	}
}
