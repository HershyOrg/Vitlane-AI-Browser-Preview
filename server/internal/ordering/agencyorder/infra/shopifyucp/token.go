package shopifyucp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
	"golang.org/x/sync/singleflight"
)

type Token struct {
	AccessToken string
	Scope       string
	ExpiresAt   time.Time
}

type TokenSource struct {
	client       *http.Client
	authURL      string
	clientID     string
	clientSecret string
	now          func() time.Time
	mu           sync.RWMutex
	cached       Token
	group        singleflight.Group
}

func NewTokenSource(client *http.Client, authURL, clientID, clientSecret string) (*TokenSource, error) {
	if client == nil || strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientSecret) == "" ||
		!strings.HasPrefix(strings.TrimSpace(authURL), "https://") {
		return nil, fmt.Errorf("Shopify token source config invalid")
	}
	return &TokenSource{client: client, authURL: strings.TrimSpace(authURL),
		clientID: strings.TrimSpace(clientID), clientSecret: strings.TrimSpace(clientSecret), now: time.Now}, nil
}

func (s *TokenSource) Token(ctx context.Context) (Token, error) {
	if cached, ok := s.current(); ok {
		return cached, nil
	}
	resultChannel := s.group.DoChan("refresh", func() (any, error) {
		if cached, ok := s.current(); ok {
			return cached, nil
		}
		return s.exchange(ctx)
	})
	select {
	case <-ctx.Done():
		return Token{}, ctx.Err()
	case result := <-resultChannel:
		if result.Err != nil {
			return Token{}, result.Err
		}
		return result.Val.(Token), nil
	}
}

func (s *TokenSource) CurrentScope() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cached.Scope
}

func (s *TokenSource) Invalidate(accessToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached.AccessToken == accessToken {
		s.cached = Token{}
	}
}

func (s *TokenSource) current() (Token, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cached, s.cached.AccessToken != "" && s.now().Add(5*time.Minute).Before(s.cached.ExpiresAt)
}

func (s *TokenSource) exchange(ctx context.Context) (Token, error) {
	payload, _ := json.Marshal(map[string]string{
		"client_id": s.clientID, "client_secret": s.clientSecret, "grant_type": "client_credentials",
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.authURL, bytes.NewReader(payload))
	if err != nil {
		return Token{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := sharedhttpclient.Do(ctx, s.client, request, sharedhttpclient.ExternalEffect)
	if err != nil {
		return Token{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Token{}, sharedhttpclient.StatusFault(response.StatusCode, response.Header.Get("Retry-After"), true)
	}
	body, err := sharedhttpclient.ReadBody(response.Body, 1<<20)
	if err != nil {
		return Token{}, fault.Wrap(err, fault.ProviderUnavailable, "SHOPIFY_TOKEN_RESPONSE_INVALID", true)
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &result); err != nil || strings.TrimSpace(result.AccessToken) == "" {
		return Token{}, fault.New(fault.ProviderRejected, "SHOPIFY_TOKEN_RESPONSE_INVALID", false)
	}
	scope, expiresAt, err := shopifyTokenClaims(result.AccessToken)
	if err != nil || !s.now().Add(5*time.Minute).Before(expiresAt) {
		return Token{}, fault.New(fault.ProviderRejected, "SHOPIFY_TOKEN_RESPONSE_INVALID", false)
	}
	token := Token{AccessToken: result.AccessToken, Scope: scope, ExpiresAt: expiresAt}
	s.mu.Lock()
	s.cached = token
	s.mu.Unlock()
	return token, nil
}

// Shopify's client-credentials response contains only access_token. The token
// is a JWT whose scopes and one-hour expiry are the documented cache metadata.
// The token came from the authenticated Shopify endpoint; these claims are not
// used as an identity assertion or as a substitute for provider authorization.
func shopifyTokenClaims(accessToken string) (string, time.Time, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", time.Time{}, fmt.Errorf("Shopify access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", time.Time{}, fmt.Errorf("decode Shopify access token claims: %w", err)
	}
	var claims struct {
		Scopes string `json:"scopes"`
		Expiry int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Expiry <= 0 {
		return "", time.Time{}, fmt.Errorf("Shopify access token claims invalid")
	}
	return strings.TrimSpace(claims.Scopes), time.Unix(claims.Expiry, 0).UTC(), nil
}
