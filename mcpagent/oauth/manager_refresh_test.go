package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

func TestGetAccessTokenSerializesConcurrentRefreshes(t *testing.T) {
	var hits atomic.Int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(40 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh-access-token","token_type":"Bearer","expires_in":3600,"refresh_token":"fresh-refresh-token"}`))
	}))
	defer tokenSrv.Close()

	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token.json")
	if err := NewTokenStore(tokenFile).Save(&oauth2.Token{
		AccessToken:  "expired-access",
		RefreshToken: "initial-refresh",
		Expiry:       time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	tokens := make(chan string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr := NewManager(&OAuthConfig{
				ClientID:  "test-client",
				AuthURL:   tokenSrv.URL + "/authorize",
				TokenURL:  tokenSrv.URL + "/token",
				TokenFile: tokenFile,
			}, nil)
			tok, err := mgr.GetAccessToken(context.Background())
			if err != nil {
				errs <- err
				return
			}
			tokens <- tok
		}()
	}
	wg.Wait()
	close(errs)
	close(tokens)

	for err := range errs {
		t.Fatalf("concurrent refresh error: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("expected exactly 1 upstream refresh call, got %d", got)
	}
	for tok := range tokens {
		if tok != "fresh-access-token" {
			t.Fatalf("unexpected token %q", tok)
		}
	}
	if info, err := os.Stat(tokenFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %v, err = %v", info.Mode().Perm(), err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".*.tmp")); len(leftovers) > 0 {
		t.Fatalf("leftover temp files: %v", leftovers)
	}
}

func TestCallbackServerEscapesErrorAndDoesNotBlockDuplicateHits(t *testing.T) {
	cs := NewCallbackServer(0, "expected-state")

	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/callback?error=server_error&error_description=%3Cscript%3Ealert(1)%3C/script%3E", nil)
	cs.handleCallback(rec1, req1)
	if ct := rec1.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if strings.Contains(rec1.Body.String(), "<script>alert(1)</script>") {
		t.Fatalf("unescaped HTML rendered in error page: %s", rec1.Body.String())
	}
	if !strings.Contains(rec1.Body.String(), "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("expected escaped error description in body: %s", rec1.Body.String())
	}

	// Second callback hit must not block even when nobody drains errChan/codeChan.
	done := make(chan struct{})
	go func() {
		rec2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state=expected-state", nil)
		cs.handleCallback(rec2, req2)
		rec3 := httptest.NewRecorder()
		cs.handleCallback(rec3, req2)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("duplicate callback hit blocked")
	}
}
