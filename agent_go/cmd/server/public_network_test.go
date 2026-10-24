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
