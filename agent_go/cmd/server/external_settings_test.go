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
// outward action permission, and a reader cannot run it.
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
	if w := f.call(t, "reader", "run_after_run", map[string]any{"workflow_id": "invoices", "option": "backup"}); w.Code != 403 {
		t.Fatalf("reader started a backup: %d %s", w.Code, w.Body)
	}
	w := f.call(t, "owner", "update_settings", map[string]any{"workflow_id": "invoices", "pulse": map[string]any{"enabled": true, "outward_actions": "allow"}, "after_manual_run": map[string]any{"notify": true}})
	if w.Code != 200 {
		t.Fatalf("owner pulse settings: %d %s", w.Code, w.Body)
	}
	body := f.call(t, "owner", "get_settings", map[string]any{"workflow_id": "invoices"}).Body.String()
	if !strings.Contains(body, `"enabled":true`) || !strings.Contains(body, `"outward_actions":"allow"`) || !strings.Contains(body, `"after_manual_run":{"backup":false,"publish":false,"notify":true}`) {
		t.Fatalf("pulse settings not saved: %s", body)
	}
}

// The live MCP refusal uses a connection with Builder consent; the general
// settings fixture deliberately lacks that scope and stops before this path.
func TestExternalPulseOffChatHasPlainNextStep(t *testing.T) {
	f := newBuilderOperationFixture(t)
	_, token := f.issue(t, "owner")
	w := f.call(t, token, "builder_pulse_chat", map[string]any{"message": "How is the goal doing?"}, 409)
	if !strings.Contains(w.Body.String(), errPulseOff.Error()) {
		t.Fatalf("OFF chat needs the plain next step: %s", w.Body)
	}
}
