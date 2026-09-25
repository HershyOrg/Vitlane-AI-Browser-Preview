package app

import "context"

type authenticatedUserIDContextKey struct{}
type webPrincipalContextKey struct{}

type WebPrincipal struct {
	UserID        string
	AuthSessionID string
}

func WithAuthenticatedUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, authenticatedUserIDContextKey{}, userID)
}

func AuthenticatedUserIDFrom(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(authenticatedUserIDContextKey{}).(string)
	return userID, ok && userID != ""
}

func WithWebPrincipal(ctx context.Context, principal WebPrincipal) context.Context {
	ctx = WithAuthenticatedUserID(ctx, principal.UserID)
	return context.WithValue(ctx, webPrincipalContextKey{}, principal)
}

func WebPrincipalFrom(ctx context.Context) (WebPrincipal, bool) {
	principal, ok := ctx.Value(webPrincipalContextKey{}).(WebPrincipal)
	return principal, ok && principal.UserID != "" && principal.AuthSessionID != ""
}
