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
// account into the starter's connection: the code waits until the starter finishes it in the app.
func TestOAuthSignInIsFinishedOnlyByThePersonWhoStartedIt(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	api := &StreamingAPI{logger: loggerv2.NewNoop()}
	flow := registerTestOAuthFlow(t, "state-bound", "owner")

	rec := httptest.NewRecorder()
	api.handleOAuthCallback(rec, httptest.NewRequest(http.MethodGet, "/api/oauth/callback?state=state-bound&code=one-time-code", nil))
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), oauthReturnParam+"=state-bound") {
		t.Fatalf("callback did not send the browser into the app: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(flow.CodeChan) != 0 {
		t.Fatal("the callback finished the sign-in without the person who started it")
	}

	complete := func(person string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/oauth/complete", strings.NewReader(`{"state":"state-bound"}`))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: person, Username: person}))
		rec := httptest.NewRecorder()
		api.handleOAuthReturnComplete(rec, req)
		return rec.Code
	}
	if code := complete("teammate"); code != http.StatusForbidden || len(flow.CodeChan) != 0 {
		t.Fatalf("a teammate finished another person's sign-in: %d", code)
	}
	if code := complete("owner"); code != http.StatusOK {
		t.Fatalf("the person who started the sign-in could not finish it: %d", code)
	}
	if got := <-flow.CodeChan; got != "one-time-code" {
		t.Fatalf("flow received %q", got)
	}
}
