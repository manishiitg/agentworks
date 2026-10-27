package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// Public enrollment must never claim another person's email, mint an admin,
// or add file/device authority to the messaging sign-in.
func TestPublicNetworkSignupAndConsent(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "bullpen")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "bullpen")
	t.Setenv("AUTH_PUBLIC_SIGNUP", "true")
	t.Setenv("AUTH_PROVIDERS", "simple,supabase-google")
	t.Setenv("AUTH_PUBLIC_SSO_ONLY", "false")
	t.Setenv("ADMIN_USERS", "administrator")
	withMemoryUserDirectory(t, `{"version":1,"users":[]}`)
	api := &StreamingAPI{}
	register := func(body string) int {
		w := httptest.NewRecorder()
		api.handleRegister(w, httptest.NewRequest("POST", "/api/auth/register", strings.NewReader(body)))
		return w.Code
	}
	if got := register(`{"username":"person","password":"test-password-long-enough","email":"administrator@example.com"}`); got != 400 {
		t.Fatalf("unverified email claim accepted: %d", got)
	}
	if got := register(`{"username":"person","password":"test-password-long-enough"}`); got != 201 {
		t.Fatalf("registration: %d", got)
	}
	rec := validateDirectoryCredentials("person", "test-password-long-enough")
	if rec == nil || rec.Admin || rec.CanCreate || rec.Email != "" || !slices.Equal(rec.Products, []string{"bullpen"}) {
		t.Fatalf("unexpected public account: %+v", rec)
	}
	if got := register(`{"username":"person","password":"another-long-password"}`); got != 409 {
		t.Fatalf("duplicate registration: %d", got)
	}
	ext := &ExternalUser{ExternalID: "google-person", Provider: "supabase-google", Username: "administrator", Email: "another-person@example.com"}
	if got := ensureDirectoryUserForExternal(ext.ExternalID, ext); got != nil {
		t.Fatal("unverified Google identity enrolled")
	}
	ext.EmailVerified = true
	google := ensureDirectoryUserForExternal(ext.ExternalID, ext)
	if google == nil || google.Admin || isConfiguredAdmin(google) || google.CanCreate || google.ID == rec.ID || strings.Contains(google.Username, "@") {
		t.Fatal("Google display name granted account authority")
	}

	if strings.Contains(publicNetworkUsername(&userDirectory{}, "person-id", "private@example.com"), "@") {
		t.Fatal("Google account email became its public name")
	}
	for _, reserved := range []string{"admin", "owner", "support", "agentworks"} {
		if !publicNetworkReservedUsername(reserved) {
			t.Fatalf("reserved name offered: %s", reserved)
		}
	}
	presence := map[string]any{}
	addAgentPresence(presence, namedMessageAgent{ID: "public-agent", Attachment: &namedAgentAttachment{Provider: "codex", Repository: "private-repository", Branch: "private-branch", Activity: "busy", ActivityAt: time.Now()}})
	for _, key := range []string{"repository", "branch", "activity", "activity_at"} {
		if _, present := presence[key]; present {
			t.Fatalf("public directory leaked %s", key)
		}
	}
	scopes, ok := validCLIOAuthScopes(nil)
	if !ok || !slices.Equal(scopes, []string{"messages:read", "messages:send"}) || !slices.Equal(cliPersonScopes(&UserClaims{UserID: rec.ID}), scopes) {
		t.Fatalf("messaging consent: %v", scopes)
	}
	if _, ok := validCLIOAuthScopes([]string{"devices:connect"}); ok {
		t.Fatal("public network offered laptop file access")
	}
	w := httptest.NewRecorder()
	api.handleGetAuthMode(w, httptest.NewRequest("GET", "/api/auth/mode", nil))
	var mode AuthModeResponse
	if json.Unmarshal(w.Body.Bytes(), &mode) != nil || !mode.RegistrationEnabled || !mode.MessagingOnly {
		t.Fatal("website did not receive sign-up availability")
	}
	guard := agentBridgeAPIMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, path := range []string{"/api/dashboards", "/api/workflows", "/api/agent-profiles", "/api/wp/api/execute", "/api/external/v1/files/content", "/api/devices"} {
		w := httptest.NewRecorder()
		guard.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusForbidden {
			t.Fatalf("messaging-only network exposed %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/auth/me", "/api/auth/password", "/api/oauth/cli/token", "/api/oauth/mcp/consent", "/api/external/v1/mcp", "/api/external/v1/agents/attachment", "/api/bullpen/agents", "/api/downloads/cli/version.json"} {
		if !agentBridgeAPIPath(path) {
			t.Fatalf("messaging endpoint refused: %s", path)
		}
	}
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "work,bullpen")
	t.Setenv("AGENT_PRODUCTS", "work,bullpen")
	w = httptest.NewRecorder()
	guard.ServeHTTP(w, httptest.NewRequest("GET", "/api/dashboards", nil))
	if w.Code != http.StatusNoContent {
		t.Fatal("company API policy changed")
	}
	if got := register(`{"username":"new-person","password":"test-password-long-enough"}`); got != 403 {
		t.Fatal("public signup enabled on a company server")
	}
}

// PLAT-856-374: hiding passwords must also refuse public credentials at the
// real handlers, while an exact pre-provisioned internal identity still works.
func TestPublicNetworkSSOOnlyWithInternalTestIdentity(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "bullpen")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "bullpen")
	t.Setenv("AUTH_PUBLIC_SIGNUP", "true")
	t.Setenv("AUTH_PUBLIC_SSO_ONLY", "true")
	t.Setenv("AUTH_INTERNAL_TEST_USER_IDS", "test-id,disabled-id")
	t.Setenv("AUTH_PROVIDERS", "simple,supabase-google,supabase-github")
	t.Setenv("SUPABASE_URL", "https://example.supabase.co")
	t.Setenv("SUPABASE_ANON_KEY", "test-anon-key")
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	original := GetAuthProviders()
	authProviders = map[string]AuthProvider{"simple": NewSimpleProvider(), "supabase-google": NewSupabaseGoogleProvider(), "supabase-github": NewSupabaseGitHubProvider()}
	t.Cleanup(func() { authProviders = original })
	hash, err := hashPassword("a-long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	dir := userDirectoryFile{Users: []UserRecord{
		{ID: "public-id", Username: "public-person", PasswordHash: hash, Admin: true},
		{ID: "test-id", Username: "internal-test", PasswordHash: hash, Products: []string{"bullpen"}},
		{ID: "disabled-id", Username: "disabled-test", PasswordHash: hash, Disabled: true},
	}}
	initial, _ := json.Marshal(dir)
	withMemoryUserDirectory(t, string(initial))
	api := &StreamingAPI{}
	post := func(path, body string, handler http.HandlerFunc) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
		return w
	}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"username":"public-person","password":"a-long-test-password"}`, 403},
		{`{"provider":"simple","username":"public-person","password":"a-long-test-password"}`, 403},
		{`{"username":"disabled-test","password":"a-long-test-password"}`, 403},
		{`{"provider":"supabase","username":"internal-test","password":"a-long-test-password"}`, 403},
		{`{"username":"internal-test","password":"wrong-password"}`, 401},
		{`{"username":"internal-test","password":"a-long-test-password"}`, 200},
	} {
		if got := post("/api/auth/login", tc.body, api.handleLogin).Code; got != tc.status {
			t.Fatalf("login status=%d want=%d", got, tc.status)
		}
	}
	if got := post("/api/auth/register", `{"username":"new-person","password":"a-long-test-password"}`, api.handleRegister).Code; got != 403 {
		t.Fatalf("public password registration=%d", got)
	}
	if got := post("/api/auth/start", `{"provider":"supabase","redirect_uri":"https://example.com/auth/callback"}`, api.handleAuthStart).Code; got != 403 {
		t.Fatalf("other OAuth provider=%d", got)
	}
	if got := post("/api/auth/start", `{"provider":"supabase-google","redirect_uri":"https://example.com/auth/callback"}`, api.handleAuthStart).Code; got != 200 {
		t.Fatalf("Google OAuth start=%d", got)
	}
	storeState("other-provider-state", "supabase", "https://example.com/auth/callback", "")
	w := httptest.NewRecorder()
	api.handleAuthCallback(w, httptest.NewRequest("GET", "/api/auth/callback?state=other-provider-state&code=test-code", nil))
	if w.Code != 403 {
		t.Fatalf("old other-provider callback=%d", w.Code)
	}
	w = httptest.NewRecorder()
	api.handleGetAuthMode(w, httptest.NewRequest("GET", "/api/auth/mode", nil))
	var mode AuthModeResponse
	if json.Unmarshal(w.Body.Bytes(), &mode) != nil || mode.RegistrationEnabled || len(mode.Providers) != 2 || mode.Providers[0].Name != "supabase-google" || mode.Providers[1].Name != "supabase-github" {
		t.Fatal("public screen offered a password alternative")
	}
	if !publicNetworkExternalSignup(&ExternalUser{Provider: "supabase-google", ExternalID: "verified-id", Email: "person@example.com", EmailVerified: true}) {
		t.Fatal("verified Google enrollment disabled")
	}
	if got := post("/api/auth/start", `{"provider":"supabase-github","redirect_uri":"https://example.com/auth/callback"}`, api.handleAuthStart).Code; got != 200 {
		t.Fatalf("GitHub OAuth start=%d", got)
	}
	t.Setenv("AUTH_PROVIDERS", "simple,supabase-google")
	if got := post("/api/auth/start", `{"provider":"supabase-github","redirect_uri":"https://example.com/auth/callback"}`, api.handleAuthStart).Code; got != 403 {
		t.Fatalf("unselected GitHub provider=%d", got)
	}
	storeState("unselected-github-state", "supabase-github", "https://example.com/auth/callback", "")
	w = httptest.NewRecorder()
	api.handleAuthCallback(w, httptest.NewRequest("GET", "/api/auth/callback?state=unselected-github-state&code=test-code", nil))
	if w.Code != 403 {
		t.Fatalf("unselected GitHub callback=%d", w.Code)
	}
	t.Setenv("AUTH_INTERNAL_TEST_USER_IDS", "")
	if got := post("/api/auth/login", `{"username":"internal-test","password":"a-long-test-password"}`, api.handleLogin).Code; got != 403 {
		t.Fatalf("empty test allowlist=%d", got)
	}
	t.Setenv("AGENT_PRODUCTS", "work,bullpen")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "work,bullpen")
	if got := post("/api/auth/login", `{"username":"public-person","password":"a-long-test-password"}`, api.handleLogin).Code; got != 200 {
		t.Fatalf("company password login changed=%d", got)
	}
}
