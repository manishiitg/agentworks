package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func registerTestOAuthFlow(t *testing.T, state, person string) *OAuthFlowState {
	t.Helper()
	flow := &OAuthFlowState{Person: person, State: state, ServerName: "app", Done: make(chan struct{}), CodeChan: make(chan string, 1), ErrChan: make(chan error, 1)}
	oauthFlowsMu.Lock()
	oauthFlows[state] = flow
	oauthFlowsMu.Unlock()
	t.Cleanup(func() {
		oauthFlowsMu.Lock()
		delete(oauthFlows, state)
		oauthFlowsMu.Unlock()
		oauthReturns.Lock()
		delete(oauthReturns.m, state)
		oauthReturns.Unlock()
	})
	return flow
}

// The provider's error text comes from the URL: anyone can send a link with a live state and their own text, so it
// must never run as script on the app's address.
func TestOAuthCallbackShowsProviderErrorAsText(t *testing.T) {
	api := &StreamingAPI{logger: loggerv2.NewNoop()}
	registerTestOAuthFlow(t, "state-xss", "")
	rec := httptest.NewRecorder()
	api.handleOAuthCallback(rec, httptest.NewRequest(http.MethodGet, "/api/oauth/callback?state=state-xss&error=x&error_description="+url.QueryEscape("<script>alert(1)</script>"), nil))
	if body := rec.Body.String(); strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("provider error text was not escaped: %s", body)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("callback page has no restrictive CSP: %q", csp)
	}
}

// On a server with several people, a sign-in link finished in a teammate's browser must not put the teammate's
// account into the starter's connection: the callback issues a fresh completion nonce (never reusing state),
// and a cross-user mismatch immediately destroys the held authorization code.
func TestOAuthSignInIsFinishedOnlyByThePersonWhoStartedIt(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	api := &StreamingAPI{logger: loggerv2.NewNoop()}

	complete := func(person, token string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/oauth/complete", strings.NewReader(`{"state":"`+token+`"}`))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: person, Username: person}))
		rec := httptest.NewRecorder()
		api.handleOAuthReturnComplete(rec, req)
		return rec.Code
	}

	// Flow 1: teammate completes callback; raw state is rejected, teammate mismatch destroys the held code
	// so the starter cannot subsequently redeem the teammate's authorization code.
	flow1 := registerTestOAuthFlow(t, "state-bound-1", "owner")
	rec := httptest.NewRecorder()
	api.handleOAuthCallback(rec, httptest.NewRequest(http.MethodGet, "/api/oauth/callback?state=state-bound-1&code=teammate-code", nil))
	loc, err := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || err != nil {
		t.Fatalf("callback did not send the browser into the app: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	nonce1 := loc.Query().Get(oauthReturnParam)
	if nonce1 == "" || nonce1 == "state-bound-1" {
		t.Fatalf("expected fresh completion nonce distinct from state, got %q", nonce1)
	}
	if len(flow1.CodeChan) != 0 {
		t.Fatal("the callback finished the sign-in without the person who started it")
	}
	if code := complete("owner", "state-bound-1"); code != http.StatusForbidden {
		t.Fatalf("starter redeemed raw state instead of completion nonce: %d", code)
	}
	if code := complete("teammate", nonce1); code != http.StatusForbidden || len(flow1.CodeChan) != 0 {
		t.Fatalf("a teammate finished another person's sign-in: %d", code)
	}
	if code := complete("owner", nonce1); code != http.StatusForbidden || len(flow1.CodeChan) != 0 {
		t.Fatalf("held victim code survived cross-user mismatch and was redeemed by starter: %d", code)
	}

	// Flow 2: owner completes their own callback cleanly with the completion nonce.
	flow2 := registerTestOAuthFlow(t, "state-bound-2", "owner")
	rec2 := httptest.NewRecorder()
	api.handleOAuthCallback(rec2, httptest.NewRequest(http.MethodGet, "/api/oauth/callback?state=state-bound-2&code=one-time-code", nil))
	loc2, _ := url.Parse(rec2.Header().Get("Location"))
	nonce2 := loc2.Query().Get(oauthReturnParam)
	if nonce2 == "" || nonce2 == "state-bound-2" {
		t.Fatalf("expected fresh completion nonce, got %q", nonce2)
	}
	if code := complete("owner", nonce2); code != http.StatusOK {
		t.Fatalf("the person who started the sign-in could not finish it: %d", code)
	}
	if got := <-flow2.CodeChan; got != "one-time-code" {
		t.Fatalf("flow received %q", got)
	}
}
