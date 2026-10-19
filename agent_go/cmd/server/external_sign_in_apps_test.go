package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// Real external dispatch and the website's encrypted app store: existing
// administrator consent only, no returned credential, and real removal.
func TestExternalSignInAppsUsesAdminGuardsAndSealedStore(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withMCPConnectionsRoot(t)
	api, _ := newCodePrivacyFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"admin"},{"id":"other","username":"other","role":"creator"}]}`)
	api.logger = loggerv2.NewNoop()
	api.mcpConfigPath = filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(api.mcpConfigPath, []byte(`{"mcpServers":{"Slack":{"url":"https://mcp.slack.com/mcp","protocol":"http","oauth":{"auth_url":"https://slack.com/oauth/v2_user/authorize","token_url":"https://slack.com/api/oauth.v2.user.access"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	claims := func(user, scope string) *UserClaims {
		return &UserClaims{UserID: user, Username: user, AccessToken: &accesstokens.Token{Scopes: []string{scope}}}
	}
	call := func(c *UserClaims, args map[string]any, want int) string {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"name": externalSignInAppsTool, "arguments": args})
		w := httptest.NewRecorder()
		api.handleExternalCall(w, adminRequest("POST", "/api/external/v1/call", string(raw), c, nil))
		if w.Code != want {
			t.Fatalf("status=%d want=%d", w.Code, want)
		}
		return w.Body.String()
	}
	const secret = "test-sign-in-app-secret"
	args := map[string]any{"action": "set", "provider": "slack", "client_id": "test-client", "client_secret": secret}
	call(claims("other", "users:manage"), args, 403)
	call(claims("owner", "workflows:read"), args, 403)
	admin := claims("owner", "users:manage")
	tool, ok := externalCatalogTool(externalSignInAppsTool)
	if !ok || !tool.mutates {
		t.Fatal("sign-in app writes were advertised as read-only")
	}
	if out := call(admin, args, 200); strings.Contains(out, secret) {
		t.Fatal("set returned a secret")
	}
	if app, err := readMCPApp("slack"); err != nil || app == nil || app.ClientSecret != secret {
		t.Fatal("website store did not receive the app")
	}
	path, _ := mcpAppFile("slack")
	if raw, err := os.ReadFile(path); err != nil || strings.Contains(string(raw), secret) {
		t.Fatal("app not encrypted at rest")
	}
	if out := call(admin, map[string]any{"action": "list"}, 200); strings.Contains(out, secret) || !strings.Contains(out, `"configured":true`) {
		t.Fatal("list leaked credentials or lost setup status")
	}
	call(admin, map[string]any{"action": "remove", "provider": "slack"}, 200)
	if app, err := readMCPApp("slack"); err != nil || app != nil {
		t.Fatal("remove did not clear the app")
	}
}
