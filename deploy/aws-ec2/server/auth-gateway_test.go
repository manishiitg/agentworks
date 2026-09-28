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

	gw := &gateway{
		agent:               proxyFor(upstream.URL),
		disablePasswordGate: true,
	}
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
		secret: []byte("test-secret-that-is-long-enough"),
		agent:  httputil.NewSingleHostReverseProxy(target),
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
	gateway := &gateway{secret: []byte("test-secret-that-is-long-enough")}
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
	gateway := &gateway{secret: []byte("test-secret-that-is-long-enough")}
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Referer", "https://attacker.example/steal")
	response := httptest.NewRecorder()

	gateway.ServeHTTP(response, req)

	if got := response.Header().Get(authRequiredHeader); got != "/login?next=%2F" {
		t.Fatalf("login header = %q", got)
	}
}

func TestUnauthenticatedAPIRequestDoesNotNestLoginReferer(t *testing.T) {
	gateway := &gateway{secret: []byte("test-secret-that-is-long-enough")}
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Referer", "https://video.example/login?next=%2Flogin%3Fnext%3D%252F")
	response := httptest.NewRecorder()

	gateway.ServeHTTP(response, req)

	if got := response.Header().Get(authRequiredHeader); got != "/login?next=%2F" {
		t.Fatalf("login header = %q, want a clean login target", got)
	}
}

func TestUnauthenticatedFrontendRequestStillRedirectsToLogin(t *testing.T) {
	gateway := &gateway{secret: []byte("test-secret-that-is-long-enough")}
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

func TestAuthenticatedRequestRefreshesSessionNearExpiry(t *testing.T) {
	gateway := &gateway{
		secret:        []byte("test-secret-that-is-long-enough"),
		frontendDir:   t.TempDir(),
		sessionCookie: sessionCookieName("video-studio"),
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: gateway.sessionCookie, Value: gateway.signedSession(time.Now().Add(time.Hour))})
	response := httptest.NewRecorder()

	gateway.ServeHTTP(response, req)

	result := response.Result()
	defer result.Body.Close()
	var refreshed *http.Cookie
	for _, cookie := range result.Cookies() {
		if cookie.Name == gateway.sessionCookie {
			refreshed = cookie
			break
		}
	}
	if refreshed == nil {
		t.Fatal("expected a refreshed session cookie")
	}
	if remaining := time.Until(refreshed.Expires); remaining < 11*time.Hour || remaining > 13*time.Hour {
		t.Fatalf("refreshed session lifetime = %s", remaining)
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

func TestNewGatewaySSOOnlyNeedsNoSharedPassword(t *testing.T) {
	t.Setenv("AUTH_SECRET", "test-secret-that-is-long-enough-x")
	t.Setenv("ACCESS_PASSWORD", "")
	t.Setenv("GATEWAY_SSO_ONLY", "true")
	t.Setenv("GATEWAY_SSO_SUPABASE_URL", "https://example.supabase.co")
	t.Setenv("GATEWAY_SSO_SUPABASE_ANON_KEY", "public-key")
	t.Setenv("GATEWAY_SSO_REDIRECT_URL", "https://sparkquill.example/auth/google/callback")
	t.Setenv("GATEWAY_SSO_ALLOWED_EMAILS", " MahimaKh@gmail.com, manisharies.iitg@gmail.com ")
	g := newGateway()
	if !g.ssoOnly || len(g.password) != 0 || !g.ssoAllowedEmail["mahimakh@gmail.com"] || !g.ssoAllowedEmail["manisharies.iitg@gmail.com"] {
		t.Fatal("SSO gateway did not enable the two approved emails without a shared password")
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
			_, _ = io.WriteString(w, `{"user":{"email":"MahimaKh@gmail.com","email_confirmed_at":"2026-09-24T00:00:00Z"}}`)
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
		ssoAllowedEmail: map[string]bool{"mahimakh@gmail.com": true, "manisharies.iitg@gmail.com": true}, ssoClient: supabase.Client(),
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
	restarted.ssoAllowedEmail = map[string]bool{"manisharies.iitg@gmail.com": true}
	denied := httptest.NewRecorder()
	restarted.ServeHTTP(denied, request)
	if denied.Code != http.StatusSeeOther || !strings.HasPrefix(denied.Header().Get("Location"), "/login") {
		t.Fatalf("removed email still has access: status=%d location=%q", denied.Code, denied.Header().Get("Location"))
	}

	// A previously issued shared-password cookie must not bypass SSO.
	legacy := httptest.NewRequest(http.MethodGet, "/", nil)
	legacy.AddCookie(&http.Cookie{Name: g.sessionCookie, Value: g.signedSession(time.Now().Add(time.Hour))})
	legacyResult := httptest.NewRecorder()
	g.ServeHTTP(legacyResult, legacy)
	if legacyResult.Code != http.StatusSeeOther {
		t.Fatalf("old password cookie status = %d", legacyResult.Code)
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
		t.Errorf("sessionCookieName(%q) = %q, want the original literal so an already-running Video Studio deployment's browser sessions survive a redeploy of this now-parameterized binary", "video-studio", got)
	}
	if got := sessionCookieName("dominion"); got != "dominion_session" {
		t.Errorf("sessionCookieName(%q) = %q, want dominion_session", "dominion", got)
	}
}

func TestNewGatewayDerivesSessionCookieFromGatewayUserID(t *testing.T) {
	t.Setenv("AUTH_SECRET", "test-secret-that-is-long-enough-x")
	t.Setenv("ACCESS_PASSWORD", "pw")
	t.Setenv("GATEWAY_USER_ID", "dominion")

	gw := newGateway()

	if gw.sessionCookie != "dominion_session" {
		t.Errorf("sessionCookie = %q, want dominion_session", gw.sessionCookie)
	}
}

func TestNewGatewayKeepsThePasswordGateByDefault(t *testing.T) {
	t.Setenv("AUTH_SECRET", "test-secret-that-is-long-enough-x")
	t.Setenv("ACCESS_PASSWORD", "pw")

	gw := newGateway()

	if gw.disablePasswordGate {
		t.Fatal("disablePasswordGate should default to false so every existing deployment keeps its current behavior")
	}
}

func TestNewGatewayCanDisableThePasswordGateExplicitly(t *testing.T) {
	t.Setenv("AUTH_SECRET", "test-secret-that-is-long-enough-x")
	t.Setenv("ACCESS_PASSWORD", "pw")
	t.Setenv("GATEWAY_DISABLE_PASSWORD_GATE", "true")

	gw := newGateway()

	if !gw.disablePasswordGate {
		t.Fatal("GATEWAY_DISABLE_PASSWORD_GATE=true should disable the password gate")
	}
}

func TestDisabledPasswordGateRoutesWithoutAnySessionCookie(t *testing.T) {
	frontendDir := t.TempDir()
	if err := os.WriteFile(frontendDir+"/index.html", []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	gw := &gateway{
		secret:              []byte("test-secret-that-is-long-enough"),
		frontendDir:         frontendDir,
		sessionCookie:       sessionCookieName("dominion"),
		disablePasswordGate: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/projects/123", nil)
	response := httptest.NewRecorder()

	gw.ServeHTTP(response, req)

	// No session cookie, and no password gate to reject it: this must reach
	// serveFrontend's SPA fallback (200, index.html), never the /login
	// redirect a password-gated deployment would produce here.
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (SPA fallback, no login redirect)", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Location"); got != "" {
		t.Fatalf("unexpected redirect to %q -- the password gate should be fully bypassed", got)
	}
}

// With the password gate off the gateway must NOT lend its service identity
// to anonymous callers (the pre-2026-09-02 behaviour, which let anyone act
// as the fixed product user). Public app routes pass through untouched;
// everything else needs the caller's own app JWT.
func TestDisabledPasswordGateNeverMintsAFallbackToken(t *testing.T) {
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
		secret:              []byte("test-secret-that-is-long-enough"),
		agent:               httputil.NewSingleHostReverseProxy(target),
		disablePasswordGate: true,
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

// With the password gate off, the gateway must never lend its service
// identity: an unauthenticated API request is refused, a valid app JWT is
// forwarded with X-User-ID stamped from the token (never from the client),
// and the login/mode routes the app needs beforehand still pass.
func TestDisabledGateRequiresUserTokenAndStampsUser(t *testing.T) {
	var seenAuth, seenUser string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth, seenUser = r.Header.Get("Authorization"), r.Header.Get("X-User-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef"), userID: "video-studio", disablePasswordGate: true}
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
		g := &gateway{disablePasswordGate: disableGate, agent: httputil.NewSingleHostReverseProxy(target)}
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
		g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef"), disablePasswordGate: disableGate}
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
			if r.URL.Path == hostedMCPPath && r.Header.Get("Authorization") == "" {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource/api/external/v1/mcp"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef"), disablePasswordGate: disableGate}
		g.agent = proxyFor(upstream.URL)
		for _, path := range []string{
			"/.well-known/oauth-protected-resource/api/external/v1/mcp",
			"/.well-known/oauth-authorization-server",
			"/api/oauth/mcp/register", "/api/oauth/mcp/authorize", "/api/oauth/mcp/token",
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
		g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef"), disablePasswordGate: disableGate}
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
	g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef"), disablePasswordGate: true}
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
	g := &gateway{secret: []byte("0123456789abcdef0123456789abcdef"), userID: "video-studio", disablePasswordGate: true}
	g.agent = proxyFor(agent.URL)
	token, err := g.agentToken()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/wp/api/workspace/export", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-User-ID", "spoofed")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || seenPath != "/api/wp/api/workspace/export" || seenUser == "spoofed" {
		t.Fatalf("status=%d path=%q user=%q", rec.Code, seenPath, seenUser)
	}
}
