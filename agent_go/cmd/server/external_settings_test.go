package server

import (
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
// autonomy level and pace, and a reader cannot run it.
func TestExternalPulseSettingsAndRunNeedEditAccess(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AUTH_SECRET", "external-settings-test-auth-secret-0123456789")
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.api.chatStore = store
	if w := f.call(t, "reader", "manage_pulse", map[string]any{"workflow_id": "invoices", "action": "run_now"}); w.Code != 403 {
		t.Fatalf("reader ran Pulse: %d %s", w.Code, w.Body)
	}
	w := f.call(t, "owner", "update_settings", map[string]any{"workflow_id": "invoices", "pulse": map[string]any{"enabled": true, "autonomy_level": 2, "pace": "calm"}})
	if w.Code != 200 {
		t.Fatalf("owner pulse settings: %d %s", w.Code, w.Body)
	}
	body := f.call(t, "owner", "get_settings", map[string]any{"workflow_id": "invoices"}).Body.String()
	if !strings.Contains(body, `"enabled":true`) || !strings.Contains(body, `"autonomy_level":2`) || !strings.Contains(body, `"pace":"calm"`) {
		t.Fatalf("pulse settings not saved: %s", body)
	}
}
