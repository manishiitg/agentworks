package cursorcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The per-session bridge token never lands in the workspace .cursor/mcp.json;
// the config names a private 0600 file the bridge reads instead.
func TestCursorMCPConfigKeepsTheBridgeTokenOutOfTheWorkspace(t *testing.T) {
	root := t.TempDir()
	old := cursorBridgeTokenRoot
	cursorBridgeTokenRoot = func() string { return root }
	t.Cleanup(func() { cursorBridgeTokenRoot = old })

	in := `{"mcpServers":{"api-bridge":{"command":"mcpbridge","env":{"MCP_API_URL":"http://h/s/a","MCP_API_TOKEN":"mcps1.secret.value","MCP_AUTH":"Authorization: Bearer mcps1.secret.value","MCP_TOOLS":"[]"}}}}`
	out, files, err := externalizeCursorBridgeTokens(in, "")
	if err != nil || len(files) != 1 {
		t.Fatalf("externalize: files=%v err=%v", files, err)
	}
	if strings.Contains(out, "mcps1.secret.value") {
		t.Fatalf("the token is still in the workspace config: %s", out)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatal(err)
	}
	env := cfg.MCPServers["api-bridge"].Env
	if env["MCP_API_TOKEN_FILE"] != files[0] || env["MCP_API_URL"] != "http://h/s/a" || env["MCP_TOOLS"] != "[]" {
		t.Fatalf("config env: %v", env)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil || string(raw) != "mcps1.secret.value" {
		t.Fatalf("token file: %q %v", raw, err)
	}
	if info, _ := os.Stat(files[0]); info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode %v, want 0600", info.Mode().Perm())
	}
	// No token: unchanged.
	plain := `{"mcpServers":{"x":{"command":"y","env":{"A":"b"}}}}`
	if got, files, _ := externalizeCursorBridgeTokens(plain, ""); got != plain || len(files) != 0 {
		t.Fatalf("a config without a token must be unchanged: %s %v", got, files)
	}
}

// Startup removes only the token folders of backends that are gone; a live
// backend's (another server on the same machine) are kept.
func TestSweepStaleBridgeTokenFilesKeepsLiveBackends(t *testing.T) {
	root := t.TempDir()
	old := cursorBridgeTokenRoot
	cursorBridgeTokenRoot = func() string { return root }
	t.Cleanup(func() { cursorBridgeTokenRoot = old })
	if _, err := writeCursorBridgeTokenFile("mine", ""); err != nil {
		t.Fatal(err)
	}
	dead := filepath.Join(root, "999999")
	if err := os.MkdirAll(dead, 0o700); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(root, strconv.Itoa(os.Getppid()))
	if err := os.MkdirAll(live, 0o700); err != nil {
		t.Fatal(err)
	}
	if n := SweepStaleBridgeTokenFiles(); n != 1 {
		t.Fatalf("want only the dead backend's folder removed, removed %d", n)
	}
	for _, keep := range []string{cursorBridgeTokenDir(), live} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("%s must be kept: %v", keep, err)
		}
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatal("the dead backend's folder must be removed")
	}
}
