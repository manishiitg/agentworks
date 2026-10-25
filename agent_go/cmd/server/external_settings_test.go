package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// Settings over MCP follow the caller's role, and secret values are
// write-only: an owner can store one, but no reply ever carries it back.
func TestExternalUpdateSettingsFollowsRoleAndNeverReturnsSecretValues(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AUTH_SECRET", "external-settings-test-auth-secret-0123456789")
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.api.chatStore = store
	// Project secrets now live in Vault; the old fixture supplied only the
	// retired per-workflow store and could never exercise a successful write.
	managedGlobalsMu.Lock()
	previous := managedGlobals
	managedGlobals = map[string]string{}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() { managedGlobalsMu.Lock(); managedGlobals = previous; managedGlobalsMu.Unlock() })
	names := []string{}
	vault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-CapLayer-Actor") != "owner" {
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/api/project-vaults":
			w.Write([]byte(`{"id":"v-123456"}`))
		case "/api/vaults/v-123456/secrets":
			var in struct{ Name string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			names = append(names, in.Name)
			w.Write([]byte(`{}`))
		case "/api/vaults/v-123456":
			_ = json.NewEncoder(w).Encode(map[string]any{"secret_names": names})
		default:
			w.Write([]byte(`{"vaults":[],"servers":[],"groups":[],"secrets":[]}`))
		}
	}))
	defer vault.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", vault.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	const value = "sk-settings-test-value-0042"

	if w := f.call(t, "reader", "update_settings", map[string]any{"workflow_id": "invoices", "browser_mode": "headless"}); w.Code != 403 {
		t.Fatalf("reader changed settings: %d %s", w.Code, w.Body)
	}
	w := f.call(t, "owner", "update_settings", map[string]any{"workflow_id": "invoices", "browser_mode": "headless",
		"secrets": map[string]any{"set": map[string]any{"API_KEY": value}}})
	if w.Code != 200 || strings.Contains(w.Body.String(), value) {
		t.Fatalf("owner update: %d %s", w.Code, w.Body)
	}
	w = f.call(t, "owner", "get_settings", map[string]any{"workflow_id": "invoices"})
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, value) || !strings.Contains(body, `{"name":"API_KEY","selected":true}`) || !strings.Contains(body, `"browser_mode":"headless"`) {
		t.Fatalf("settings after update: %d %s", w.Code, body)
	}
}

// Pulse settings follow the Pulse tab: an owner turns Pulse on with an
// outward action permission, and a reader cannot run it.

// The live MCP refusal uses a connection with Builder consent; the general
// settings fixture deliberately lacks that scope and stops before this path.

// Status used to return saved Pulse context records instead of the current
// screen's status. Reading it must also remain possible while Pulse is OFF.
