package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("GATEWAY_REQUEST_LOG", "false")
	os.Exit(m.Run())
}

func TestGatewayRequestLogCapturesPublicTimingAndCorrelationWithoutQuerySecrets(t *testing.T) {
	t.Setenv("GATEWAY_REQUEST_LOG", "true")
	var upstreamRequestID string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamRequestID = r.Header.Get(requestIDHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousOutput)

	gw := &gateway{agent: proxyFor(upstream.URL)}
	req := httptest.NewRequest(http.MethodGet, "/api/health?token=must-not-appear", nil)
	req.Header.Set(requestIDHeader, "edge-request-123")
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 127.0.0.1")
	rec := httptest.NewRecorder()

	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if upstreamRequestID != "edge-request-123" {
		t.Fatalf("upstream request id = %q", upstreamRequestID)
	}
	if got := rec.Header().Get(requestIDHeader); got != "edge-request-123" {
		t.Fatalf("response request id = %q", got)
	}
	output := logs.String()
	for _, want := range []string{
		"[GATEWAY] --> request_id=edge-request-123",
		"[GATEWAY] <-- request_id=edge-request-123",
		`path="/api/health"`,
		"route=agent",
		"status=204",
		"ttfb_ms=",
		"duration_ms=",
		"slow=false",
		`client_ip="203.0.113.7"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("request log missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "must-not-appear") || strings.Contains(output, "token=") {
		t.Fatalf("request log exposed query credentials:\n%s", output)
	}
}

func TestGatewayRequestIDRejectsUnsafeClientValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(requestIDHeader, "unsafe request id")

	requestID := gatewayRequestID(req)
	if requestID == "unsafe request id" || !safeRequestID(requestID) {
		t.Fatalf("generated request id = %q", requestID)
	}
}

func TestAgentTokenIsShortLivedAndSignedWithGatewaySecret(t *testing.T) {
	gateway := &gateway{secret: []byte("test-secret-that-is-long-enough")}
	raw, err := gateway.agentToken()
	if err != nil {
		t.Fatalf("agentToken: %v", err)
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("token parts = %d, want 3", len(parts))
	}
	mac := hmac.New(sha256.New, gateway.secret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	wantSignature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[2]), []byte(wantSignature)) {
		t.Fatal("token signature does not verify")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	claims := &gatewayClaims{}
	if err := json.Unmarshal(payload, claims); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	if claims.UserID != "video-studio" || claims.Username != "video-studio" || claims.Provider != "gateway" {
		t.Fatalf("claims = %#v", claims)
	}
	untilExpiry := time.Until(time.Unix(claims.ExpiresAt, 0))
	if untilExpiry <= 0 || untilExpiry > 16*time.Minute {
		t.Fatalf("unexpected expiration: %d", claims.ExpiresAt)
	}
}

func TestServeAgentForwardsQueryTokenAsBearerCredential(t *testing.T) {
	var gotAuthorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	gateway := &gateway{
		secret:  []byte("test-secret-that-is-long-enough"),
		agent:   httputil.NewSingleHostReverseProxy(target),
		ssoOnly: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/public/file?token=workspace-user-token", nil)
	response := httptest.NewRecorder()
	gateway.serveAgent(response, req)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if gotAuthorization != "Bearer workspace-user-token" {
		t.Fatalf("authorization = %q", gotAuthorization)
	}
}

func TestUnauthenticatedAPIRequestReturnsExplicitLoginSignal(t *testing.T) {
	gateway := &gateway{secret: []byte("test-secret-that-is-long-enough"), ssoOnly: true}
	req := httptest.NewRequest(http.MethodGet, "/api/health?full=1", nil)
	req.Header.Set("Referer", "https://example.com/projects/123?tab=chat")
	response := httptest.NewRecorder()

	gateway.ServeHTTP(response, req)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if got := response.Header().Get(authRequiredHeader); got != "/login?next=%2Fprojects%2F123%3Ftab%3Dchat" {
		t.Fatalf("login header = %q", got)
	}
	if got := strings.TrimSpace(response.Body.String()); got != `{"error":"authentication_required"}` {
		t.Fatalf("body = %q", got)
	}
}

func TestGmailOAuthCallbackIsPublicAtGateway(t *testing.T) {
	if !agentPublicPath("/api/human-feedback/gmail/auth/callback") {
		t.Fatal("Gmail OAuth callback must bypass the gateway JWT check: Google cannot send an AgentWorks bearer token")
	}
	if agentPublicPath("/api/human-feedback/gmail/connections/gmail_001/auth/start") {
		t.Fatal("only the OAuth callback may be public; Gmail connection management must require authentication")
	}
}

func TestUnauthenticatedAPIRequestIgnoresExternalReferer(t *testing.T) {
	gateway := &gateway{secret: []byte("test-secret-that-is-long-enough"), ssoOnly: true}
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Referer", "https://attacker.example/steal")
	response := httptest.NewRecorder()

	gateway.ServeHTTP(response, req)

	if got := response.Header().Get(authRequiredHeader); got != "/login?next=%2F" {
		t.Fatalf("login header = %q", got)
	}
}

func TestUnauthenticatedAPIRequestDoesNotNestLoginReferer(t *testing.T) {
	gateway := &gateway{secret: []byte("test-secret-that-is-long-enough"), ssoOnly: true}
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Referer", "https://video.example/login?next=%2Flogin%3Fnext%3D%252F")
	response := httptest.NewRecorder()

	gateway.ServeHTTP(response, req)

	if got := response.Header().Get(authRequiredHeader); got != "/login?next=%2F" {
		t.Fatalf("login header = %q, want a clean login target", got)
	}
}

func TestUnauthenticatedFrontendRequestStillRedirectsToLogin(t *testing.T) {
	gateway := &gateway{secret: []byte("test-secret-that-is-long-enough"), ssoOnly: true}
	req := httptest.NewRequest(http.MethodGet, "/projects/123", nil)
	response := httptest.NewRecorder()

	gateway.ServeHTTP(response, req)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if got := response.Header().Get("Location"); got != "/login?next=%2Fprojects%2F123" {
		t.Fatalf("location = %q", got)
	}
}

func TestGatewaySSOOnlyRemovesPasswordLogin(t *testing.T) {
	g := &gateway{ssoOnly: true, appName: "SparkQuill", sessionCookie: sessionCookieName("sparkquill")}
	page := httptest.NewRecorder()
	g.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/login", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "href='/auth/google/start?next=%2F'") || strings.Contains(page.Body.String(), "type=password") {
		t.Fatalf("SSO login page: status=%d body=%q", page.Code, page.Body.String())
	}
	post := httptest.NewRecorder()
	g.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("password=old-password")))
	if post.Code != http.StatusMethodNotAllowed || len(post.Result().Cookies()) != 0 {
		t.Fatalf("password login must be removed: status=%d cookies=%v", post.Code, post.Result().Cookies())
	}
}

func TestNewGatewaySSOOnlyReadsApprovedEmails(t *testing.T) {
	t.Setenv("AUTH_SECRET", "test-secret-that-is-long-enough-x")
	t.Setenv("GATEWAY_SSO_ONLY", "true")
	t.Setenv("GATEWAY_SSO_SUPABASE_URL", "https://example.supabase.co")
	t.Setenv("GATEWAY_SSO_SUPABASE_ANON_KEY", "public-key")
	t.Setenv("GATEWAY_SSO_REDIRECT_URL", "https://sparkquill.example/auth/google/callback")
	t.Setenv("GATEWAY_SSO_ALLOWED_EMAILS", " Member@example.com, owner@example.com ")
	g := newGateway()
	if !g.ssoOnly || !g.ssoAllowedEmail["member@example.com"] || !g.ssoAllowedEmail["owner@example.com"] {
		t.Fatal("SSO gateway did not enable the two approved emails")
	}
}

func TestGoogleSSOGatewayAllowsOnlyApprovedEmailAndSurvivesRestart(t *testing.T) {
	var tokenRequests int
	supabase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/v1/token" || r.URL.Query().Get("grant_type") != "pkce" || r.Header.Get("apikey") != "public-key" {
			t.Errorf("unexpected token request: %s %s", r.Method, r.URL.String())
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["auth_code"] != "test-code" || body["code_verifier"] == "" {
			t.Errorf("invalid PKCE exchange: %v %v", body, err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		tokenRequests++
		w.Header().Set("Content-Type", "application/json")
		if tokenRequests == 1 {
			_, _ = io.WriteString(w, `{"user":{"email":"Member@example.com","email_confirmed_at":"2026-09-24T00:00:00Z"}}`)
		} else {
			_, _ = io.WriteString(w, `{"user":{"email":"other@gmail.com","email_confirmed_at":"2026-09-24T00:00:00Z"}}`)
		}
	}))
	defer supabase.Close()
	frontendDir := t.TempDir()
	if err := os.WriteFile(frontendDir+"/index.html", []byte("SparkQuill family"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &gateway{
		secret: []byte("test-secret-that-is-long-enough"), sessionCookie: sessionCookieName("sparkquill"), frontendDir: frontendDir,
		ssoOnly: true, ssoURL: supabase.URL, ssoAnonKey: "public-key", ssoRedirectURL: "https://sparkquill.example/auth/google/callback",
		ssoAllowedEmail: map[string]bool{"member@example.com": true, "owner@example.com": true}, ssoClient: supabase.Client(),
	}
	start := httptest.NewRecorder()
	g.ServeHTTP(start, httptest.NewRequest(http.MethodGet, "/auth/google/start?next=%2Factivity", nil))
	if start.Code != http.StatusFound {
		t.Fatalf("SSO start = %d", start.Code)
	}
	authURL, err := url.Parse(start.Header().Get("Location"))
	if err != nil || authURL.Query().Get("provider") != "google" || authURL.Query().Get("redirect_to") != g.ssoRedirectURL || authURL.Query().Get("code_challenge_method") != "s256" {
		t.Fatalf("Supabase redirect = %q, error = %v", start.Header().Get("Location"), err)
	}
	var flowCookie *http.Cookie
	for _, cookie := range start.Result().Cookies() {
		if cookie.Name == g.ssoFlowCookieName() {
			flowCookie = cookie
		}
	}
	if flowCookie == nil || !flowCookie.HttpOnly || !flowCookie.Secure {
		t.Fatalf("PKCE flow cookie = %#v", flowCookie)
	}
	callback := httptest.NewRequest(http.MethodGet, "/auth/google/callback?code=test-code", nil)
	callback.AddCookie(flowCookie)
	finished := httptest.NewRecorder()
	g.ServeHTTP(finished, callback)
	if finished.Code != http.StatusSeeOther || finished.Header().Get("Location") != "/activity" || tokenRequests != 1 {
		t.Fatalf("SSO callback: status=%d location=%q exchanges=%d body=%q", finished.Code, finished.Header().Get("Location"), tokenRequests, finished.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range finished.Result().Cookies() {
		if cookie.Name == g.sessionCookie {
			session = cookie
		}
	}
	if session == nil || !strings.HasPrefix(session.Value, "s.") {
		t.Fatalf("SSO session cookie = %#v", session)
	}
	if remaining := time.Until(session.Expires); remaining < 29*24*time.Hour || remaining > 31*24*time.Hour {
		t.Fatalf("SSO session lifetime = %s", remaining)
	}

	// A deployment creates a new gateway process but keeps AUTH_SECRET.
	restarted := &gateway{secret: g.secret, sessionCookie: g.sessionCookie, frontendDir: frontendDir, ssoOnly: true, ssoAllowedEmail: g.ssoAllowedEmail}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(session)
	page := httptest.NewRecorder()
	restarted.ServeHTTP(page, request)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "SparkQuill family") {
		t.Fatalf("session after restart: status=%d body=%q", page.Code, page.Body.String())
	}

	// Removing an address blocks its already-issued cookie at the next request.
	restarted.ssoAllowedEmail = map[string]bool{"owner@example.com": true}
	denied := httptest.NewRecorder()
	restarted.ServeHTTP(denied, request)
	if denied.Code != http.StatusSeeOther || !strings.HasPrefix(denied.Header().Get("Location"), "/login") {
		t.Fatalf("removed email still has access: status=%d location=%q", denied.Code, denied.Header().Get("Location"))
	}

	secondStart := httptest.NewRecorder()
	g.ServeHTTP(secondStart, httptest.NewRequest(http.MethodGet, "/auth/google/start", nil))
	secondCallback := httptest.NewRequest(http.MethodGet, "/auth/google/callback?code=test-code", nil)
	secondCallback.AddCookie(secondStart.Result().Cookies()[0])
	unapproved := httptest.NewRecorder()
	g.ServeHTTP(unapproved, secondCallback)
	if unapproved.Code != http.StatusForbidden {
		t.Fatalf("unapproved Google email status = %d", unapproved.Code)
	}
	for _, cookie := range unapproved.Result().Cookies() {
		if cookie.Name == g.sessionCookie {
			t.Fatal("unapproved Google email received a session cookie")
		}
	}
}

func TestSessionCookieNameNamespacesByGatewayIdentity(t *testing.T) {
	if got := sessionCookieName("video-studio"); got != "video_studio_session" {
		t.Errorf("sessionCookieName(%q) = %q, want video_studio_session", "video-studio", got)
	}
	if got := sessionCookieName("dominion"); got != "dominion_session" {
		t.Errorf("sessionCookieName(%q) = %q, want dominion_session", "dominion", got)
	}
}

func TestNewGatewayDerivesSessionCookieFromGatewayUserID(t *testing.T) {
	t.Setenv("AUTH_SECRET", "test-secret-that-is-long-enough-x")
	t.Setenv("GATEWAY_USER_ID", "dominion")

	gw := newGateway()

	if gw.sessionCookie != "dominion_session" {
		t.Errorf("sessionCookie = %q, want dominion_session", gw.sessionCookie)
	}
}

// The shared-password gate is gone: without GATEWAY_SSO_ONLY the app's own
// sign-in is the only gate, and no password setting is needed.
func TestNewGatewayHasNoGateByDefault(t *testing.T) {
	t.Setenv("AUTH_SECRET", "test-secret-that-is-long-enough-x")

	if gw := newGateway(); gw.ssoOnly {
		t.Fatal("a gateway without GATEWAY_SSO_ONLY must leave sign-in to the app")
	}
}

func TestNoGateRoutesWithoutAnySessionCookie(t *testing.T) {
	frontendDir := t.TempDir()
	if err := os.WriteFile(frontendDir+"/index.html", []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	gw := &gateway{
		secret:        []byte("test-secret-that-is-long-enough"),
		frontendDir:   frontendDir,
		sessionCookie: sessionCookieName("dominion"),
	}

	req := httptest.NewRequest(http.MethodGet, "/projects/123", nil)
	response := httptest.NewRecorder()

	gw.ServeHTTP(response, req)

	// No session cookie and no gate to reject it: this must reach
	// serveFrontend's SPA fallback (200, index.html), never a /login redirect.
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (SPA fallback, no login redirect)", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Location"); got != "" {
		t.Fatalf("unexpected redirect to %q", got)
	}
}

// Without Google sign-in in front, the gateway must NOT lend its service identity
// to anonymous callers (the pre-2026-09-02 behaviour, which let anyone act
// as the fixed product user). Public app routes pass through untouched;
// everything else needs the caller's own app JWT.
func TestNoGateNeverMintsAFallbackToken(t *testing.T) {
	var gotAuthorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}

	gw := &gateway{
		secret: []byte("test-secret-that-is-long-enough"),
		agent:  httputil.NewSingleHostReverseProxy(target),
	}

	response := httptest.NewRecorder()
	gw.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("public route status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if gotAuthorization != "" {
		t.Fatalf("authorization = %q, want none: the gateway must not mint a token for anonymous callers", gotAuthorization)
	}

	response = httptest.NewRecorder()
	gw.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("protected route without a token: status = %d, want 401", response.Code)
	}
}

// Without Google sign-in in front, the gateway must never lend its service
// identity: an unauthenticated API request is refused, a valid app JWT is
// forwarded with X-User-ID stamped from the token (never from the client),
// and the login/mode routes the app needs beforehand still pass.
func TestNoGateRequiresUserTokenAndStampsUser(t *testing.T) {
	var seenAuth, seenUser string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth, seenUser = r.Header.Get("Authorization"), r.Header.Get("X-User-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef"), userID: "video-studio"}
	g.agent = proxyFor(upstream.URL)

	for _, path := range []string{"/api/agent-profiles/video-studio/query", "/api/wp/api/documents/x"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-User-ID", "spoofed")
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without a token: got %d, want 401", path, rec.Code)
		}
	}

	token, err := g.agentToken()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agent-profiles/video-studio/query?token="+token, nil)
	req.Header.Set("X-User-ID", "spoofed")
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || seenAuth != "Bearer "+token || seenUser != "video-studio" {
		t.Fatalf("valid token: code=%d auth=%q user=%q", rec.Code, seenAuth, seenUser)
	}

	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/mode", nil))
	if rec.Code != http.StatusOK || seenAuth != "" {
		t.Fatalf("public auth route: code=%d auth=%q (must reach the app without a minted token)", rec.Code, seenAuth)
	}

	if _, ok := g.verifyAgentToken(token[:len(token)-2] + "xx"); ok {
		t.Fatal("tampered signature accepted")
	}
}

func TestWorkflowWebhookPreservesCredentialsAndBodyWithoutAppLogin(t *testing.T) {
	for _, disableGate := range []bool{false, true} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer trigger-secret" || r.Header.Get("X-Hub-Signature-256") != "sha256=original" || r.Header.Get("X-User-ID") != "" {
				t.Errorf("gateway changed webhook authentication")
			}
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["event"] != "test" {
				t.Errorf("body was not forwarded: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
		}))
		target, _ := url.Parse(upstream.URL)
		g := &gateway{ssoOnly: !disableGate, agent: httputil.NewSingleHostReverseProxy(target)}
		req := httptest.NewRequest(http.MethodPost, "/api/hooks/workflow/trigger-id", strings.NewReader(`{"event":"test"}`))
		req.Header.Set("Authorization", "Bearer trigger-secret")
		req.Header.Set("X-Hub-Signature-256", "sha256=original")
		req.Header.Set("X-User-ID", "spoofed-user")
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		upstream.Close()
		if rec.Code != http.StatusAccepted {
			t.Fatalf("webhook response = %d", rec.Code)
		}
	}
}

func TestWebhookExceptionDoesNotExposeManagementOrOtherMethods(t *testing.T) {
	for _, path := range []string{"/api/workflow-webhooks", "/api/workflow-webhooks/id", "/api/hooks/workflow/", "/api/hooks/workflow/id/extra", "/api/hooks/workflow/..", "/api/wp/execute"} {
		if isWebhookRequest(httptest.NewRequest(http.MethodPost, path, nil)) {
			t.Errorf("unexpected webhook exception: %s", path)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		if isWebhookRequest(httptest.NewRequest(method, "/api/hooks/workflow/id", nil)) {
			t.Errorf("unexpected webhook method: %s", method)
		}
	}
}

func TestWebhookReadRoutesPreserveCredentials(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hook-secret" {
			t.Error("credential changed")
		}
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	g := &gateway{agent: httputil.NewSingleHostReverseProxy(target)}
	for _, p := range []string{"/api/hooks/workflow/id/runs/run", "/api/hooks/workflow/id/runs/run/artifact?path=file&token=signed"} {
		r := httptest.NewRequest("GET", p, nil)
		r.Header.Set("Authorization", "Bearer hook-secret")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatalf("read route: %d", w.Code)
		}
	}
	if isWebhookRequest(httptest.NewRequest("DELETE", "/api/hooks/workflow/id/runs/run", nil)) {
		t.Fatal("mutation bypass")
	}
}

func TestProductWebhookBypassesGatewayButManagementStaysPrivate(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer product-trigger-secret" {
			t.Fatalf("credential changed: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-User-ID") != "" {
			t.Fatalf("spoofed user header reached product webhook: %q", r.Header.Get("X-User-ID"))
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	g := &gateway{agent: httputil.NewSingleHostReverseProxy(target)}
	req := httptest.NewRequest(http.MethodPost, "/api/hooks/product/trigger-id", strings.NewReader(`{"marker":"test"}`))
	req.Header.Set("Authorization", "Bearer product-trigger-secret")
	req.Header.Set("X-User-ID", "spoofed-user")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("product webhook response = %d", rec.Code)
	}
	for _, candidate := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/hooks/product/trigger-id", nil),
		httptest.NewRequest(http.MethodPost, "/api/hooks/product/trigger-id/extra", nil),
		httptest.NewRequest(http.MethodPost, "/api/product-webhooks/trigger-id", nil),
	} {
		if isWebhookRequest(candidate) {
			t.Fatalf("unexpected product webhook exception: %s %s", candidate.Method, candidate.URL.Path)
		}
	}
}

func TestAccessTokenReachesAppWithoutGatewaySession(t *testing.T) {
	for _, disableGate := range []bool{false, true} {
		var seenAuth, seenUser string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenAuth, seenUser = r.Header.Get("Authorization"), r.Header.Get("X-User-ID")
			w.WriteHeader(http.StatusOK)
		}))
		defer upstream.Close()
		g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef"), ssoOnly: !disableGate}
		g.agent = proxyFor(upstream.URL)

		req := httptest.NewRequest(http.MethodGet, "/api/external/v1/tools", nil)
		req.Header.Set("Authorization", "Bearer aw_pat_test-token")
		req.Header.Set("X-User-ID", "spoofed")
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("disableGate=%v: PAT request got %d, want proxied", disableGate, rec.Code)
		}
		if seenAuth != "Bearer aw_pat_test-token" {
			t.Fatalf("disableGate=%v: upstream auth = %q", disableGate, seenAuth)
		}
		if seenUser != "" {
			t.Fatalf("disableGate=%v: spoofed user id reached upstream: %q", disableGate, seenUser)
		}

		rec = httptest.NewRecorder()
		g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/external/v1/tools?token=aw_pat_query-token", nil))
		if rec.Code != http.StatusOK || seenAuth != "Bearer aw_pat_query-token" {
			t.Fatalf("disableGate=%v: ?token= PAT: code=%d auth=%q", disableGate, rec.Code, seenAuth)
		}
	}
}

func TestMCPOAuthDiscoveryAndChallengeReachAgentWithoutGatewaySession(t *testing.T) {
	for _, disableGate := range []bool{false, true} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-User-ID") != "" {
				t.Fatalf("spoofed user id reached OAuth route: %s", r.URL.Path)
			}
			if (r.URL.Path == hostedMCPPath || r.URL.Path == "/api/vault/mcp") && r.Header.Get("Authorization") == "" {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource/api/external/v1/mcp"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef"), ssoOnly: !disableGate}
		g.agent = proxyFor(upstream.URL)
		for _, path := range []string{
			"/.well-known/oauth-protected-resource/api/external/v1/mcp",
			"/.well-known/oauth-authorization-server",
			"/.well-known/oauth-authorization-server/vault", "/.well-known/oauth-protected-resource/api/vault/mcp",
			"/api/oauth/mcp/register", "/api/oauth/mcp/authorize", "/api/oauth/mcp/token",
			"/api/oauth/vault/register", "/api/oauth/vault/authorize", "/api/oauth/vault/token",
			"/api/oauth/cli/device", "/api/oauth/cli/token", "/api/oauth/cli/revoke",
		} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("X-User-ID", "spoofed")
			w := httptest.NewRecorder()
			g.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("disableGate=%v path=%s got %d", disableGate, path, w.Code)
			}
		}
		req := httptest.NewRequest(http.MethodGet, hostedMCPPath, nil)
		req.Header.Set("X-User-ID", "spoofed")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" || w.Header().Get(authRequiredHeader) != "" {
			t.Fatalf("disableGate=%v MCP challenge: code=%d headers=%v", disableGate, w.Code, w.Header())
		}
		req = httptest.NewRequest(http.MethodPost, hostedMCPPath, nil)
		req.Header.Set("Authorization", "Bearer aw_mcp_test")
		w = httptest.NewRecorder()
		g.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("disableGate=%v OAuth bearer got %d", disableGate, w.Code)
		}
		for _, token := range []string{"", "aw_vault_test"} {
			req := httptest.NewRequest(http.MethodPost, "/api/vault/mcp", nil)
			req.Header.Set("X-User-ID", "spoofed")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			g.ServeHTTP(w, req)
			if (token == "" && (w.Code != 401 || w.Header().Get(authRequiredHeader) != "" || w.Header().Get("WWW-Authenticate") == "")) || (token != "" && w.Code != 200) {
				t.Fatal("Vault MCP intercepted by browser gate", w.Code)
			}
		}
		req = httptest.NewRequest(http.MethodGet, "/api/external/v1/tools", nil)
		req.Header.Set("Authorization", "Bearer aw_cli_test")
		w = httptest.NewRecorder()
		g.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("disableGate=%v CLI bearer got %d", disableGate, w.Code)
		}
		upstream.Close()
	}
}

func TestAccessTokenStaysOutOfWorkspaceRoutes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	for _, disableGate := range []bool{false, true} {
		g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef"), ssoOnly: !disableGate}
		g.agent = proxyFor(upstream.URL)
		req := httptest.NewRequest(http.MethodGet, "/api/wp/api/documents/x", nil)
		req.Header.Set("Authorization", "Bearer aw_pat_test-token")
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("disableGate=%v: PAT on workspace route got %d, want 401", disableGate, rec.Code)
		}
	}
}

func TestNonTokenBearerStillRejectedWithoutSession(t *testing.T) {
	g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef")}
	for _, bearer := range []string{"Bearer garbage", "Bearer aw_pat", "Bearer x.aw_pat_y.z"} {
		req := httptest.NewRequest(http.MethodGet, "/api/external/v1/tools", nil)
		req.Header.Set("Authorization", bearer)
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: got %d, want 401", bearer, rec.Code)
		}
	}
}

func TestServeFrontendCachesHashedAssetsImmutably(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"index.html": "<html></html>", "runtime-config.js": "x", "assets/index-abc123.js": "js"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g := &gateway{frontendDir: dir}
	for path, want := range map[string]string{
		"/assets/index-abc123.js": "public, max-age=31536000, immutable",
		"/index.html":             "no-cache",
		"/runtime-config.js":      "no-cache",
		"/some/spa/route":         "no-cache",
	} {
		rec := httptest.NewRecorder()
		g.serveFrontend(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Header().Get("Cache-Control"); got != want {
			t.Fatalf("%s Cache-Control = %q, want %q", path, got, want)
		}
	}
}

// A hashed asset from a previous release must 404, not fall back to
// index.html: a browser (or Cloudflare in front) would otherwise treat the
// HTML as the script and show a blank page after every deploy.
func TestMissingHashedAssetIsNotFoundNotSPAFallback(t *testing.T) {
	frontendDir := t.TempDir()
	if err := os.WriteFile(frontendDir+"/index.html", []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	gw := &gateway{frontendDir: frontendDir}

	response := httptest.NewRecorder()
	gw.serveFrontend(response, httptest.NewRequest(http.MethodGet, "/assets/index-oldhash.js", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("missing asset Cache-Control = %q, want no-store", got)
	}

	response = httptest.NewRecorder()
	gw.serveFrontend(response, httptest.NewRequest(http.MethodGet, "/projects/123", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("client route status = %d, want %d (SPA fallback)", response.Code, http.StatusOK)
	}
}

// /api/wp reaches the agent API with its prefix intact (its proxy enforces
// per-user and per-workflow access and holds the workspace token); the
// gateway never talks to the workspace service itself.
func TestWorkspaceRoutesGoThroughAgent(t *testing.T) {
	var seenPath, seenUser string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath, seenUser = r.URL.Path, r.Header.Get("X-User-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer agent.Close()
	g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef"), userID: "video-studio"}
	g.agent = proxyFor(agent.URL)
	token, err := g.agentToken()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/wp/api/documents/notes.md", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-User-ID", "spoofed")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || seenPath != "/api/wp/api/documents/notes.md" || seenUser == "spoofed" {
		t.Fatalf("status=%d path=%q user=%q", rec.Code, seenPath, seenUser)
	}
}

func TestSandboxedLessonMediaUsesJWTWithoutLoginCookie(t *testing.T) {
	var seenPath, seenAuth, seenRange, seenUser string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath, seenAuth, seenRange, seenUser = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Range"), r.Header.Get("X-User-ID")
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-3/100")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("MP4!"))
	}))
	defer agent.Close()
	g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef"), userID: "family-user", ssoOnly: true}
	g.agent = proxyFor(agent.URL)
	token, err := g.agentToken()
	if err != nil {
		t.Fatal(err)
	}
	mediaPath := "/api/wp/api/documents/Chats/SparkQuill/activities/lesson/lesson.mp4/raw"
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req := httptest.NewRequest(method, mediaPath+"?token="+token, nil)
		req.Header.Set("Range", "bytes=0-3")
		req.Header.Set("X-User-ID", "spoofed")
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusPartialContent || seenPath != mediaPath || seenAuth != "Bearer "+token || seenRange != "bytes=0-3" || seenUser != "family-user" {
			t.Fatalf("%s: status=%d path=%q range=%q user=%q", method, rec.Code, seenPath, seenRange, seenUser)
		}
		if rec.Header().Get("Content-Type") != "video/mp4" || rec.Header().Get("Content-Range") != "bytes 0-3/100" {
			t.Fatal("media type and byte range must reach the browser")
		}
	}
	for _, test := range []struct{ method, path, token string }{
		{http.MethodGet, mediaPath, ""},
		{http.MethodGet, mediaPath, "invalid"},
		{http.MethodGet, mediaPath, "aw_pat_test-token"},
		{http.MethodGet, mediaPath, token[:len(token)-2] + "xx"},
		{http.MethodPut, mediaPath, token},
		{http.MethodGet, strings.TrimSuffix(mediaPath, "/raw"), token},
	} {
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, httptest.NewRequest(test.method, test.path+"?token="+test.token, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without required credential/session got %d", test.method, test.path, rec.Code)
		}
	}
}

func TestChromeExtensionGatewayExemptionIsExact(t *testing.T) {
	for _, item := range []struct {
		method, path string
		allowed      bool
	}{
		{"GET", "/api/browser/extension/connect", true},
		{"POST", "/api/browser/extension/connect", false},
		{"GET", "/api/browser/extension", false},
		{"GET", "/api/browser/extension/connect/other", false},
		{"GET", "/api/browser/extension/setup", true},
		{"POST", "/api/browser/extension/setup", false},
		{"POST", "/api/browser/extension/pairing/peek", true},
		{"POST", "/api/browser/extension/pairing/redeem", true},
		{"GET", "/api/browser/extension/pairing/peek", false},
		{"GET", "/api/browser/extension/pairing/redeem", false},
		{"POST", "/api/browser/extension/pairing/redeem/other", false},
		{"HEAD", "/api/browser/extension/setup", false},
		{"OPTIONS", "/api/browser/extension/pairing/redeem", false},
		{"GET", "/api/browser/extension/setup/", false},
		{"GET", "/api/browser/extension//setup", false},
		{"GET", "/api/browser/extension/%73etup", false},
		{"POST", "/api/browser/extension/pairing%2Fredeem", false},
		{"GET", "/api/browser/extension/setup/../extension", false},
		{"GET", "/api/account/browsers", false},
	} {
		if got := isChromeExtensionBrowserRequest(httptest.NewRequest(item.method, item.path, nil)); got != item.allowed {
			t.Fatalf("%s %s: %v", item.method, item.path, got)
		}
		// PLAT-746-283: check the actual proxy with and without the SSO front gate.
		for _, sso := range []bool{false, true} {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("X-User-ID") != "" {
					t.Error("untrusted identity reached public pairing handler")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			g := &gateway{agent: proxyFor(upstream.URL), ssoOnly: sso}
			req := httptest.NewRequest(item.method, item.path, nil)
			req.Header.Set("X-User-ID", "untrusted")
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, req)
			upstream.Close()
			if item.allowed && (rec.Code != http.StatusNoContent || calls != 1) {
				t.Fatalf("%s %s sso=%v did not reach handoff handler: %d", item.method, item.path, sso, rec.Code)
			}
			if !item.allowed && (rec.Code != http.StatusUnauthorized || calls != 0) {
				t.Fatalf("%s %s sso=%v escaped gateway: %d", item.method, item.path, sso, rec.Code)
			}
		}
	}
}

func TestGatewayEventStreamsRejectQueryTokens(t *testing.T) {
	for _, disableGate := range []bool{false, true} {
		calls := 0
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(http.StatusNoContent)
		}))
		defer upstream.Close()
		g := &gateway{agent: proxyFor(upstream.URL), ssoOnly: !disableGate}
		for _, path := range []string{"/api/live", "/api/sessions/chat/events/stream"} {
			for _, header := range []string{"", "Bearer aw_pat_fixture"} {
				req := httptest.NewRequest(http.MethodGet, path+"?token=fixture-sign-in", nil)
				req.Header.Set("Authorization", header)
				rec := httptest.NewRecorder()
				g.ServeHTTP(rec, req)
				if rec.Code != http.StatusBadRequest || calls != 0 || strings.Contains(rec.Body.String(), "fixture-sign-in") {
					t.Fatalf("gate=%v path=%s status=%d calls=%d", disableGate, path, rec.Code, calls)
				}
			}
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer aw_pat_fixture")
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("header-only gate=%v path=%s status=%d", disableGate, path, rec.Code)
			}
			calls = 0
		}
	}
}

func TestMCPAppsFrameBypassesOnlyExactAnonymousGET(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		calls := 0
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Header.Get("Authorization") != "" || r.Header.Get("X-User-ID") != "" {
				t.Error("bootstrap received an injected identity")
			}
			w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'")
			_, _ = w.Write([]byte("inert frame"))
		}))
		g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef"), ssoOnly: !disabled, agent: proxyFor(upstream.URL)}
		for _, item := range []struct {
			method, path string
			want         int
		}{
			{http.MethodGet, "/api/mcp-apps/frame", http.StatusOK},
			{http.MethodPost, "/api/mcp-apps/frame", http.StatusUnauthorized},
			{http.MethodHead, "/api/mcp-apps/frame", http.StatusUnauthorized},
			{http.MethodGet, "/api/mcp-apps/frame/x", http.StatusUnauthorized},
		} {
			before := calls
			res := httptest.NewRecorder()
			req := httptest.NewRequest(item.method, item.path, nil)
			req.Header.Set("X-User-ID", "spoofed")
			g.ServeHTTP(res, req)
			if res.Code != item.want || (calls > before) != (item.want == http.StatusOK) {
				t.Fatalf("disabled=%t %s %s: status=%d forwarded=%t", disabled, item.method, item.path, res.Code, calls > before)
			}
		}
		upstream.Close()
	}
}
