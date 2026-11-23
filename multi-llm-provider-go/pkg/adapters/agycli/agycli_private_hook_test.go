package agycli

import (
	"encoding/json"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-856-145: the same native tool gate must live in the isolated home,
// never overwrite an existing project's hooks or instructions.
func TestPrivateRuntimeHookPreservesProjectConfiguration(t *testing.T) {
	project, home, runtimeDir := t.TempDir(), t.TempDir(), t.TempDir()
	original := []byte(`{"person-hook":{"PreToolUse":[]}}`)
	os.MkdirAll(filepath.Join(project, ".agents"), 0700)
	path := filepath.Join(project, ".agents", "hooks.json")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	release, err := agyHoldToolModeHookForCall(project, home, "mcp_only", &llmtypes.CallOptions{CLIRuntimeDir: runtimeDir})
	if err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(home, ".gemini", "config", "hooks.json")
	raw, err := os.ReadFile(private)
	if err != nil {
		t.Fatal(err)
	}
	var hooks map[string]interface{}
	if json.Unmarshal(raw, &hooks) != nil || !agyIsManagedToolHook(hooks[agyToolModeHookName]) {
		t.Fatal("private runtime has no native gate")
	}
	release()
	if _, err := os.Stat(private); !os.IsNotExist(err) {
		t.Fatal("private hook not released")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatal("project hook changed", err)
	}
}
