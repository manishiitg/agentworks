package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Adding the tools server keeps the person's other entries, makes a copy of the old file, does nothing
// the second time, and never replaces a different server that already has the name.
func TestMergeToolsServerURLKeepsOthersAndIsRepeatable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(`{"theme":"dark","mcpServers":{"other":{"url":"https://other.example.com/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	url := "https://agents.example.com/api/external/v1/mcp"
	changed, backup, err := mergeToolsServerURL(path, "agentworks", url)
	if err != nil || !changed || backup == "" {
		t.Fatalf("first add: changed=%t backup=%q err=%v", changed, backup, err)
	}
	raw, _ := os.ReadFile(path)
	for _, want := range []string{`"theme": "dark"`, "other.example.com", url} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("result lost %q:\n%s", want, raw)
		}
	}
	if changed, _, err = mergeToolsServerURL(path, "agentworks", url); err != nil || changed {
		t.Errorf("second add must change nothing: changed=%t err=%v", changed, err)
	}
	if info, e := os.Stat(path); e != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the file mode must be kept: %v %v", info, e)
	}
	if _, _, err = mergeToolsServerURL(path, "agentworks", "https://different.example.com/mcp"); err == nil {
		t.Error("a different server under the same name must be refused")
	}
}

// A named login registers under its own name so several logins do not collide in one app.
func TestToolsServerNameFollowsTheLogin(t *testing.T) {
	cfg := filepath.Join("home", ".config", "agentworks")
	if got := toolsServerName(filepath.Join(cfg, "config.json")); got != "agentworks" {
		t.Errorf("default login: %q", got)
	}
	if got := toolsServerName(filepath.Join(cfg, "logins", "partner.json")); got != "agentworks-partner" {
		t.Errorf("named login: %q", got)
	}
}
