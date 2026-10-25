package server

import (
	"context"
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
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoices","created_by":"owner","access":{"owners":["owner"],"readers":["reader"]},"backup":{"enabled":false,"destinations":[{"id":"keep-backup","type":"git","repo":"example/backup"}]},"publish":{"enabled":true,"destinations":[{"id":"keep-publish","provider":"example"}]}}`)
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

	if w := f.call(t, "reader", "update_settings", map[string]any{"workflow_id": "invoices", "backup": map[string]any{"after_runs": true}}); w.Code != 403 {
		t.Fatalf("reader changed settings: %d %s", w.Code, w.Body)
	}
	w := f.call(t, "owner", "update_settings", map[string]any{"workflow_id": "invoices", "browser_mode": "headless", "backup": map[string]any{"after_runs": true}, "publish": map[string]any{"after_runs": false},
		"secrets": map[string]any{"set": map[string]any{"API_KEY": value}}, "chat_instructions": "  Explain the source of each number.  "})
	if w.Code != 200 || strings.Contains(w.Body.String(), value) {
		t.Fatalf("owner update: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"after_runs":true`) || !strings.Contains(w.Body.String(), "Set up a backup destination") {
		t.Fatal("settings reply omitted the saved selection or its next step")
	}
	w = f.call(t, "owner", "get_settings", map[string]any{"workflow_id": "invoices"})
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, value) || !strings.Contains(body, `{"name":"API_KEY","selected":true}`) || !strings.Contains(body, `"browser_mode":"headless"`) {
		t.Fatalf("settings after update: %d %s", w.Code, body)
	}
	var view map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view["chat_instructions"] != "Explain the source of each number." {
		t.Fatalf("guidance not persisted: %v", view["chat_instructions"])
	}
	manifest, found, err := ReadWorkflowManifest(context.Background(), "Workflow/invoices")
	if err != nil || !found || manifest.Backup == nil || !manifest.Backup.AfterRuns || manifest.Backup.Enabled || len(manifest.Backup.Destinations) != 1 || manifest.Backup.Destinations[0].ID != "keep-backup" || manifest.Publish == nil || !manifest.Publish.Enabled || len(manifest.Publish.Destinations) != 1 || manifest.Publish.AfterRuns {
		t.Fatalf("after-run patch changed configuration authority or lost selections: %+v %v", manifest, err)
	}
	before := projectChatInstructionsKey("Workflow/invoices")
	if before == "" {
		t.Fatal("retained chats must include saved guidance in their fingerprint")
	}
	if w := f.call(t, "reader", "update_settings", map[string]any{"workflow_id": "invoices", "chat_instructions": "Change your rules"}); w.Code != 403 {
		t.Fatalf("shared person changed guidance: %d", w.Code)
	}
	if w := f.call(t, "owner", "update_settings", map[string]any{"workflow_id": "invoices", "chat_instructions": strings.Repeat("a", 4001)}); w.Code != 400 {
		t.Fatalf("oversize guidance: %d", w.Code)
	}
	if w := f.call(t, "owner", "update_settings", map[string]any{"workflow_id": "invoices", "chat_instructions": ""}); w.Code != 200 {
		t.Fatalf("clear: %d %s", w.Code, w.Body)
	}
	if after := projectChatInstructionsKey("Workflow/invoices"); after == before || after != "" {
		t.Fatal("clearing guidance must refresh retained chats")
	}
}

// Pulse settings follow the Pulse tab: an owner turns Pulse on with an
// outward action permission, and a reader cannot run it.

// The live MCP refusal uses a connection with Builder consent; the general
// settings fixture deliberately lacks that scope and stops before this path.

// Status used to return saved Pulse context records instead of the current
// screen's status. Reading it must also remain possible while Pulse is OFF.
