package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestGetAccessTokenPreservesRefreshErrorAndInvalidGrant(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token revoked"}`))
	}))
	defer tokenSrv.Close()

	tokenFile := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(tokenFile)
	if err := store.Save(&oauth2.Token{
		AccessToken:  "expired-access",
		RefreshToken: "revoked-refresh",
		Expiry:       time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(&OAuthConfig{
		ClientID:  "test-client",
		AuthURL:   tokenSrv.URL + "/authorize",
		TokenURL:  tokenSrv.URL + "/token",
		TokenFile: tokenFile,
	}, nil)

	_, err := mgr.GetAccessToken(context.Background())
	if err == nil {
		t.Fatal("expected refresh failure")
	}
	if !errors.Is(err, ErrNoValidToken) {
		t.Fatalf("error must wrap ErrNoValidToken, got: %v", err)
	}
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("error must wrap ErrTokenExpired, got: %v", err)
	}
	var retrieveErr *oauth2.RetrieveError
	if !errors.As(err, &retrieveErr) || retrieveErr.ErrorCode != "invalid_grant" {
		t.Fatalf("expected wrapped oauth2.RetrieveError(invalid_grant), got: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "invalid_grant") {
		t.Fatalf("error text should include invalid_grant, got: %v", err)
	}

	// Expired token without a refresh token must also wrap ErrNoValidToken with an explicit reason.
	noRefreshFile := filepath.Join(t.TempDir(), "no-refresh.json")
	if err := NewTokenStore(noRefreshFile).Save(&oauth2.Token{
		AccessToken: "expired-only",
		Expiry:      time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	mgr2 := NewManager(&OAuthConfig{
		ClientID:  "test-client",
		AuthURL:   tokenSrv.URL + "/authorize",
		TokenURL:  tokenSrv.URL + "/token",
		TokenFile: noRefreshFile,
	}, nil)
	_, err = mgr2.GetAccessToken(context.Background())
	if !errors.Is(err, ErrNoValidToken) || !strings.Contains(err.Error(), "did not issue a refresh token") {
		t.Fatalf("missing-refresh error = %v", err)
	}
}
