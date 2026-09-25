package googleoidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"golang.org/x/oauth2"
)

func TestCodeExchangeTimeoutIsAmbiguousAndNotRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	provider := NewWithEndpoints(Config{
		ClientID: "client", ClientSecret: "secret", RedirectURL: "https://example.test/callback",
		HTTPClient: server.Client(),
	}, oauth2.Endpoint{TokenURL: server.URL}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := provider.ExchangeAndVerifyCode(ctx, accountapp.CodeExchangeRequest{
		Code: "single-use-code", PKCEVerifier: "verifier",
	})
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.ExternalEffectUnknown || failure.Retryable {
		t.Fatalf("classification=%#v err=%v", failure, err)
	}
}

func TestCodeExchangeCallerCancellationIsAmbiguousAndNotRetryable(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-time.After(50 * time.Millisecond)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	provider := NewWithEndpoints(Config{
		ClientID: "client", ClientSecret: "secret", RedirectURL: "https://example.test/callback",
		HTTPClient: server.Client(),
	}, oauth2.Endpoint{TokenURL: server.URL}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err := provider.ExchangeAndVerifyCode(ctx, accountapp.CodeExchangeRequest{
		Code: "single-use-code", PKCEVerifier: "verifier",
	})
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.ExternalEffectUnknown || failure.Retryable {
		t.Fatalf("classification=%#v err=%v", failure, err)
	}
}

func TestCodeExchangeDistinguishesProviderRejectionFromLostResponse(t *testing.T) {
	t.Run("provider rejection", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		}))
		defer server.Close()
		provider := NewWithEndpoints(Config{
			ClientID: "client", ClientSecret: "secret", RedirectURL: "https://example.test/callback",
			HTTPClient: server.Client(),
		}, oauth2.Endpoint{TokenURL: server.URL}, nil)
		_, err := provider.ExchangeAndVerifyCode(context.Background(), accountapp.CodeExchangeRequest{
			Code: "rejected-code", PKCEVerifier: "verifier",
		})
		failure, ok := fault.As(err)
		if !ok || failure.Code != fault.ProviderRejected || failure.Retryable {
			t.Fatalf("classification=%#v err=%v", failure, err)
		}
	})

	t.Run("lost response", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection reset after write")
		})}
		provider := NewWithEndpoints(Config{
			ClientID: "client", ClientSecret: "secret", RedirectURL: "https://example.test/callback",
			HTTPClient: client,
		}, oauth2.Endpoint{TokenURL: "https://google.example/token"}, nil)
		_, err := provider.ExchangeAndVerifyCode(context.Background(), accountapp.CodeExchangeRequest{
			Code: "single-use-code", PKCEVerifier: "verifier",
		})
		failure, ok := fault.As(err)
		if !ok || failure.Code != fault.ExternalEffectUnknown || failure.Retryable {
			t.Fatalf("classification=%#v err=%v", failure, err)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestAuthorizationCodePKCEAndIDTokenVerification(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const (
		clientID = "vitlane-test-client"
		nonce    = "expected-nonce"
		verifier = "expected-pkce-verifier"
	)

	var issuer string
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	issuer = server.URL

	publicKey := jose.JSONWebKey{
		Key: &privateKey.PublicKey, KeyID: "test-key", Algorithm: string(jose.RS256), Use: "sig",
	}
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{publicKey}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token request: %v", err)
		}
		if r.Form.Get("code") != "valid-code" || r.Form.Get("code_verifier") != verifier {
			t.Errorf("unexpected token request: %v", r.Form)
		}
		idToken := signedIDToken(t, privateKey, map[string]any{
			"iss": issuer, "sub": "google-subject", "aud": clientID,
			"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
			"nonce": nonce, "email": "person@example.com",
			"email_verified": true, "name": "Person",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "discarded-access-token",
			"token_type":   "Bearer", "expires_in": 3600, "id_token": idToken,
		})
	})

	remoteKeys := oidc.NewRemoteKeySet(context.Background(), server.URL+"/keys")
	oidcVerifier := oidc.NewVerifier(issuer, remoteKeys, &oidc.Config{ClientID: clientID})
	provider := NewWithEndpoints(Config{
		ClientID: clientID, ClientSecret: "secret",
		RedirectURL: "https://vitlane.example/api/v1/auth/google/callback",
	}, oauth2.Endpoint{
		AuthURL: server.URL + "/authorize", TokenURL: server.URL + "/token",
	}, oidcVerifier)

	authorizationURL, err := provider.AuthorizationURL(accountapp.AuthorizationRequest{
		State: "state", Nonce: nonce, PKCEChallenge: "challenge",
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("code_challenge_method") != "S256" ||
		query.Get("code_challenge") != "challenge" ||
		query.Get("nonce") != nonce {
		t.Fatalf("authorization URL lacks PKCE/nonce: %s", authorizationURL)
	}
	for _, scope := range []string{"openid", "email", "profile"} {
		if !strings.Contains(query.Get("scope"), scope) {
			t.Fatalf("authorization scope %q missing from %q", scope, query.Get("scope"))
		}
	}
	if query.Get("max_age") != "" || query.Get("prompt") != "" {
		t.Fatalf("plain login must not force re-authentication: %s", authorizationURL)
	}
	freshURL, err := provider.AuthorizationURL(accountapp.AuthorizationRequest{
		State: "state", Nonce: nonce, PKCEChallenge: "challenge",
		ForceFresh: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	freshParsed, err := url.Parse(freshURL)
	if err != nil {
		t.Fatal(err)
	}
	freshQuery := freshParsed.Query()
	if freshQuery.Get("max_age") != "0" || freshQuery.Get("prompt") != "login" {
		t.Fatalf("fresh login must force re-authentication: %s", freshURL)
	}

	identity, err := provider.ExchangeAndVerifyCode(
		context.Background(),
		accountapp.CodeExchangeRequest{Code: "valid-code", PKCEVerifier: verifier},
	)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Subject != "google-subject" || identity.Nonce != nonce ||
		identity.Email != "person@example.com" || !identity.EmailVerified {
		t.Fatalf("unexpected verified identity: %#v", identity)
	}
}

func signedIDToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	options := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key")
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key}, options,
	)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	object, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := object.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return serialized
}
