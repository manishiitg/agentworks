package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAuthMiddlewareRequiresJWTInSingleUserMode(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")

	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/terminals", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddlewareAcceptsJWTInSingleUserMode(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")

	token, err := GenerateJWT("default", "user", "")
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := GetUserIDFromContext(r.Context()); got != "default" {
			t.Fatalf("user id = %q, want default", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/terminals", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

// A sign-in used every day was cut off exactly 7 days after login
// (PLAT-825). A plain session older than a day is renewed; a scoped token is not.
func TestAuthMiddlewareRenewsAnInUseSignIn(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	sign := func(c *UserClaims) string {
		s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(GetAuthSecret())
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	claims := func(issued time.Time, scope string) *UserClaims {
		return &UserClaims{UserID: "default", Username: "user", Scope: scope,
			RegisteredClaims: jwt.RegisteredClaims{IssuedAt: jwt.NewNumericDate(issued), ExpiresAt: jwt.NewNumericDate(issued.Add(sessionLifetime))}}
	}
	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tc := range []struct {
		name, token string
		renew       bool
	}{
		{"six days old", sign(claims(time.Now().Add(-6*24*time.Hour), "")), true},
		{"fresh", sign(claims(time.Now().Add(-time.Hour), "")), false},
		{"scoped", sign(claims(time.Now().Add(-6*24*time.Hour), "report-preview")), false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/terminals", nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		renewed := rr.Header().Get(renewedTokenHeader)
		if (renewed != "") != tc.renew {
			t.Fatalf("%s: renewed=%t (status %d), want %t", tc.name, renewed != "", rr.Code, tc.renew)
		}
		if renewed != "" {
			got := &UserClaims{}
			if _, err := jwt.ParseWithClaims(renewed, got, func(*jwt.Token) (interface{}, error) { return GetAuthSecret(), nil }); err != nil || got.UserID != "default" || time.Until(got.ExpiresAt.Time) < sessionLifetime-time.Minute {
				t.Fatalf("%s: renewed token not a fresh 7-day sign-in for the same user: %v", tc.name, err)
			}
		}
	}
}

// Showcase: POST /api/auth/logout returned 200 and the same Bearer token kept working. Signing out ends that sign-in
// (the token and an older one it was renewed from, also after a restart); the person's other sign-in stays.
func TestLogoutEndsThatSignInOnly(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	api := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	logout := AuthMiddleware(http.HandlerFunc((&StreamingAPI{}).handleLogout))
	call := func(handler http.Handler, method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}
	refused := func(when, token string) {
		t.Helper()
		if rr := call(api, http.MethodGet, "/api/terminals", token); rr.Code != http.StatusUnauthorized || !strings.Contains(rr.Body.String(), "expired") {
			t.Fatalf("%s: status %d %q, want 401 with the wording that shows the login screen", when, rr.Code, rr.Body.String())
		}
	}
	signedOut, err := GenerateJWT("default", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := GenerateJWT("default", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Now().Add(-2 * 24 * time.Hour)
	older, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &UserClaims{UserID: "default", Username: "user",
		RegisteredClaims: jwt.RegisteredClaims{ID: "renewed-session", IssuedAt: jwt.NewNumericDate(issued), ExpiresAt: jwt.NewNumericDate(issued.Add(sessionLifetime))}}).SignedString(GetAuthSecret())
	if err != nil {
		t.Fatal(err)
	}
	renewed := call(api, http.MethodGet, "/api/terminals", older).Header().Get(renewedTokenHeader)
	if renewed == "" {
		t.Fatal("the older token was not renewed")
	}
	for _, token := range []string{signedOut, renewed} {
		if rr := call(logout, http.MethodPost, "/api/auth/logout", token); rr.Code != http.StatusOK {
			t.Fatalf("logout: status %d", rr.Code)
		}
	}
	check := func(when string) {
		t.Helper()
		refused(when+": signed-out token", signedOut)
		refused(when+": renewed token", renewed)
		refused(when+": token it was renewed from", older)
		if rr := call(api, http.MethodGet, "/api/terminals", other); rr.Code != http.StatusNoContent {
			t.Fatalf("%s: the person's other sign-in got status %d", when, rr.Code)
		}
	}
	check("after logout")
	revokedSessions.Lock()
	revokedSessions.until = map[string]time.Time{}
	revokedSessions.Unlock()
	if err := loadRevokedSessions(); err != nil {
		t.Fatal(err)
	}
	check("after a restart")
}

func TestSingleUserProductWorkspaceMapsGatewayIdentityToDefaultOwner(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("DEFAULT_USER_ID", "default")
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")

	// The rootless gateway uses a product identity for its signed loopback
	// token. Single-user project data is intentionally stored under the
	// deployment's default user, so the request must resolve to that owner.
	token, err := GenerateJWT("example-product", "example-product", "")
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := GetUserIDFromContext(r.Context()); got != "example-product" {
			t.Fatalf("request identity = %q, want gateway identity", got)
		}
		if got := productWorkspaceUserID(r.Context()); got != "default" {
			t.Fatalf("product workspace owner = %q, want default", got)
		}
		if got := GetUserIDFromContext(productWorkspaceContext(r.Context())); got != "default" {
			t.Fatalf("forwarded workspace owner = %q, want default", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/agent-profiles/example-product/query", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestSingleUserPublicWorkspaceMapsGatewayIdentityToDefaultOwner(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("DEFAULT_USER_ID", "default")
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")

	token, err := GenerateJWT("example-product", "example-product", "")
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := publicWorkspaceUserID(r); got != "default" {
			t.Fatalf("public workspace owner = %q, want default", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/public/file?uid=another-user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestAuthMiddlewareRequiresAuthForPprof(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")

	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, path := range []string{"/debug/pprof/", "/debug/pprof/heap?debug=1", "/debug/pprof/goroutine?debug=2"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s status = %d, want %d", path, rr.Code, http.StatusUnauthorized)
		}
	}
}

func TestAuthMiddlewareAcceptsJWTForPprof(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")

	token, err := GenerateJWT("default", "user", "")
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/heap?debug=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestAuthSecretMustBeExplicitAndNonDefault(t *testing.T) {
	if err := ValidateAuthSecretValue(""); err == nil {
		t.Fatal("ValidateAuthSecretValue accepted an empty secret")
	}
	if err := ValidateAuthSecretValue(deprecatedDefaultAuthSecret); err == nil {
		t.Fatal("ValidateAuthSecretValue accepted the deprecated public default")
	}
	if err := ValidateAuthSecretValue("test-auth-secret-with-enough-entropy"); err != nil {
		t.Fatalf("ValidateAuthSecretValue rejected explicit secret: %v", err)
	}
}

func TestCORSMiddlewareAllowsLoopbackOrigin(t *testing.T) {
	api := &StreamingAPI{config: ServerConfig{CORSOrigins: []string{"loopback"}}}
	handler := api.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/terminals", nil)
	req.Header.Set("Origin", "http://127.0.0.1:51734")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:51734" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want loopback origin", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want true", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPatch) {
		t.Fatalf("Access-Control-Allow-Methods = %q, want PATCH", got)
	}
}

func TestCORSMiddlewareRejectsNonLoopbackOrigin(t *testing.T) {
	api := &StreamingAPI{config: ServerConfig{CORSOrigins: []string{"loopback"}}}
	handler := api.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/terminals", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty", got)
	}
}

// A URL token must never authenticate a browser stream, including when a
// caller supplies a valid header too. Other URL-only clients are unchanged.
func TestAuthEventStreamsRejectQueryTokens(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	token, err := GenerateJWT("default", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, path := range []string{"/api/live", "/api/sessions/chat/events/stream"} {
		for _, query := range []string{"", "?token=", "?token=" + token} {
			req := httptest.NewRequest(http.MethodGet, path+query, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			want := http.StatusNoContent
			if query != "" {
				want = http.StatusBadRequest
			}
			if rec.Code != want || strings.Contains(rec.Body.String(), token) {
				t.Fatalf("stream path=%s query_present=%t status=%d want=%d", path, query != "", rec.Code, want)
			}
		}
	}
	// No token, and a token supplied only in the query, cannot bypass auth.
	for _, suffix := range []string{"", "?token=" + token} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/live"+suffix, nil))
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusBadRequest {
			t.Fatalf("anonymous stream status=%d", rec.Code)
		}
	}
}
