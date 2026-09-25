package app

import (
	"context"
	"testing"
)

func TestWebPrincipalAlsoProvidesAuthenticatedUser(t *testing.T) {
	ctx := WithWebPrincipal(context.Background(), WebPrincipal{
		UserID:        "user-1",
		AuthSessionID: "session-1",
	})
	principal, ok := WebPrincipalFrom(ctx)
	if !ok || principal.UserID != "user-1" ||
		principal.AuthSessionID != "session-1" {
		t.Fatalf("web principal=%#v ok=%v", principal, ok)
	}
	userID, ok := AuthenticatedUserIDFrom(ctx)
	if !ok || userID != "user-1" {
		t.Fatalf("authenticated user=%q ok=%v", userID, ok)
	}
}
