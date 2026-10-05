package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
)

func TestChromeExtensionPairingRequiresWorkspaceWriteAccess(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"bob","username":"bob","can_create":true,"products":[]}]}`)
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"content": `{"version":"1","id":"one","label":"One","access":{"owners":["alice"],"readers":["bob"]},"capabilities":{"browser_mode":"auto"}}`}})
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	previous := browserrelay.Default
	browserrelay.Default = browserrelay.New()
	defer func() { browserrelay.Default.Close(); browserrelay.Default = previous }()
	api := &StreamingAPI{}
	call := func(user, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{"action":"pair"}`))
		if user != "" {
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user}))
		}
		w := httptest.NewRecorder()
		api.handleBrowserExtension(w, req)
		return w
	}
	for _, user := range []string{"", "bob", "stranger"} {
		if w := call(user, "/?workspace_path=Workflow/one"); w.Code != 403 {
			t.Fatalf("%q paired: %d %s", user, w.Code, w.Body.String())
		}
	}
	if w := call("alice", "/?workspace_path=../Workflow/one"); w.Code != 403 {
		t.Fatal("noncanonical scope admitted")
	}
	if w := call("alice", "/?workspace_path=Workflow/one"); w.Code != 200 || !strings.Contains(w.Body.String(), "token") {
		t.Fatalf("owner pairing: %d %s", w.Code, w.Body.String())
	}
	if !shouldSkipAuth(browserExtensionConnectPath) || shouldSkipAuth("/api/browser/extension") || shouldSkipAuth(browserExtensionConnectPath+"/other") {
		t.Fatal("extension auth exemption is not exact")
	}
}
