package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The "Set up auto mode for your environment?" dialog takes over the input box
// in long-lived auto-mode sessions, so an Enter meant for our message selects
// "Set it up". The adapter records Claude's own "Don't show again" state
// (autoModeEnvSetup.dismissed) and keeps any other fields in that entry.
func TestPrepareClaudeUserConfigDismissesAutoModeSetupDialog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(configPath, []byte(`{"autoModeEnvSetup":{"dismissedAt":1790495921975},"numStartups":400}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prepareClaudeUserConfig(t.TempDir(), "")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	setup, _ := config["autoModeEnvSetup"].(map[string]interface{})
	if dismissed, _ := setup["dismissed"].(bool); !dismissed {
		t.Fatalf("autoModeEnvSetup = %#v, want dismissed=true", config["autoModeEnvSetup"])
	}
	if setup["dismissedAt"] == nil || config["numStartups"] == nil {
		t.Fatalf("existing config fields were dropped: %#v", config)
	}
}
