package googleoidc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"golang.org/x/oauth2"
)

const googleIssuer = "https://accounts.google.com"

type Provider struct {
	oauthConfig *oauth2.Config
	verifier    *oidc.IDTokenVerifier
	httpClient  *http.Client
}

type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
}

func New(ctx context.Context, config Config) (*Provider, error) {
	if strings.TrimSpace(config.ClientID) == "" ||
		strings.TrimSpace(config.ClientSecret) == "" ||
		strings.TrimSpace(config.RedirectURL) == "" || config.HTTPClient == nil {
		return nil, errors.New("Google OIDC configuration is incomplete")
	}
	discoveryContext := context.WithValue(ctx, oauth2.HTTPClient, config.HTTPClient)
	discovered, err := oidc.NewProvider(discoveryContext, googleIssuer)
	if err != nil {
		return nil, fmt.Errorf("discover Google OIDC: %w", err)
	}
	return NewWithEndpoints(config, discovered.Endpoint(), discovered.Verifier(&oidc.Config{
		ClientID: config.ClientID,
	})), nil
}

func NewWithEndpoints(
	config Config,
	endpoint oauth2.Endpoint,
	verifier *oidc.IDTokenVerifier,
) *Provider {
	return &Provider{
		oauthConfig: &oauth2.Config{
			ClientID: config.ClientID, ClientSecret: config.ClientSecret,
			RedirectURL: config.RedirectURL, Endpoint: endpoint,
			Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
		},
		verifier:   verifier,
		httpClient: config.HTTPClient,
	}
}

func (p *Provider) AuthorizationURL(input accountapp.AuthorizationRequest) (string, error) {
	if strings.TrimSpace(input.State) == "" ||
		strings.TrimSpace(input.Nonce) == "" ||
		strings.TrimSpace(input.PKCEChallenge) == "" {
		return "", errors.New("OIDC authorization parameters are incomplete")
	}
	options := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("nonce", input.Nonce),
		oauth2.SetAuthURLParam("code_challenge", input.PKCEChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}
	if input.ForceFresh {
		// max_age=0 obliges Google to re-authenticate and to return the
		// auth_time claim; prompt=login keeps the account picker honest.
		options = append(options,
			oauth2.SetAuthURLParam("max_age", "0"),
			oauth2.SetAuthURLParam("prompt", "login"),
		)
	}
	return p.oauthConfig.AuthCodeURL(input.State, options...), nil
}

func (p *Provider) ExchangeAndVerifyCode(
	ctx context.Context,
	input accountapp.CodeExchangeRequest,
) (accountdomain.VerifiedIdentity, error) {
	if p.httpClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, p.httpClient)
	}
	token, err := p.oauthConfig.Exchange(
		ctx, input.Code, oauth2.VerifierOption(input.PKCEVerifier),
	)
	if err != nil {
		return accountdomain.VerifiedIdentity{}, classifyCodeExchangeError(err, ctx)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return accountdomain.VerifiedIdentity{}, errors.New("Google response did not contain an ID token")
	}
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return accountdomain.VerifiedIdentity{}, fault.Wrap(
			err, fault.ProviderRejected, "GOOGLE_ID_TOKEN_REJECTED", false,
		)
	}
	var claims struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Nonce         string `json:"nonce"`
		AuthTime      int64  `json:"auth_time"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return accountdomain.VerifiedIdentity{}, fmt.Errorf("decode Google ID token claims: %w", err)
	}
	return accountdomain.VerifiedIdentity{
		Provider: accountdomain.IdentityProviderGoogle,
		Subject:  claims.Subject, Email: claims.Email,
		EmailVerified: claims.EmailVerified, DisplayName: claims.Name, Nonce: claims.Nonce,
		AuthTime: authTimeFromClaim(claims.AuthTime),
	}, nil
}

func authTimeFromClaim(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

func classifyCodeExchangeError(err error, ctx context.Context) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(ctx.Err(), context.Canceled):
		// Exchange has already begun, so cancellation does not prove that the
		// single-use authorization code remained unconsumed.
		return fault.Wrap(err, fault.ExternalEffectUnknown, "GOOGLE_CODE_EXCHANGE_UNKNOWN", false)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
		// The authorization code can be consumed even when the response is lost.
		// The login attempt is also single-use, so the browser must start over.
		return fault.Wrap(err, fault.ExternalEffectUnknown, "GOOGLE_CODE_EXCHANGE_UNKNOWN", false)
	default:
		var responseError *oauth2.RetrieveError
		if errors.As(err, &responseError) {
			return fault.Wrap(
				err, fault.ProviderRejected, "GOOGLE_CODE_EXCHANGE_REJECTED", false,
			)
		}
		// oauth2 does not expose whether a non-response transport error happened
		// before or after the code reached Google. The local attempt is already
		// consumed, so fail closed and require a new login attempt.
		return fault.Wrap(err, fault.ExternalEffectUnknown, "GOOGLE_CODE_EXCHANGE_UNKNOWN", false)
	}
}
