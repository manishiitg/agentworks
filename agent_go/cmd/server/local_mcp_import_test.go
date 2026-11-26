package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitHub #4279: the person's own MCP servers are read from Claude, Codex, Cursor, agy and Claude Desktop; AgentWorks'
// own servers are left out, variables resolve, a missing program is not importable, and the list the screen gets
// holds names only, never a value.
func TestLocalMCPCandidatesReadEveryApp(t *testing.T) {
	home, folder, bin := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("PATH", bin)
	t.Setenv("LOCAL_MCP_TEST_TOKEN", "token-value-from-env")
	program := filepath.Join(bin, "fake-mcp")
	if err := os.WriteFile(program, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	claude, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{"agentworks": map[string]any{"type": "http", "url": "http://127.0.0.1:1/mcp"}},
		"projects": map[string]any{folder: map[string]any{"mcpServers": map[string]any{
			"posthog": map[string]any{"type": "http", "url": "https://mcp.example.com/mcp", "headers": map[string]string{"Authorization": "Bearer ${LOCAL_MCP_TEST_TOKEN}"}},
		}}},
	})
	write(filepath.Join(home, ".claude.json"), string(claude))
	write(filepath.Join(home, ".codex", "config.toml"), `
[mcp_servers.notes]
command = "fake-mcp"
args = ["--secret-arg-value"]
env_vars = ["LOCAL_MCP_TEST_TOKEN"]

[mcp_servers.off]
command = "fake-mcp"
enabled = false
`)
	write(filepath.Join(home, ".cursor", "mcp.json"), `{"mcpServers":{"agentworks-messages":{"command":"agentworks"},"notes":{"command":"fake-mcp","args":["--secret-arg-value"]}}}`)
	write(filepath.Join(home, ".gemini", "config", "mcp_config.json"), `{"mcpServers":{"missing":{"command":"not-installed-anywhere","env":{"API_KEY":"${NOT_SET_ANYWHERE}"}}}}`)

	got := (&StreamingAPI{}).localMCPCandidates(folder)
	byName := map[string]localMCPCandidate{}
	for _, c := range got {
		byName[c.Name] = c
	}
	if len(got) != 3 {
		t.Fatalf("candidates = %+v, want posthog, notes, missing", got)
	}
	posthog := byName["posthog"]
	if posthog.Kind != "url" || posthog.Scope != "project" || posthog.headers["Authorization"] != "Bearer token-value-from-env" || !posthog.Importable {
		t.Fatalf("posthog = %+v", posthog)
	}
	notes := byName["notes"]
	if notes.Kind != "command" || notes.command != program || strings.Join(notes.Sources, ",") != "Codex,Cursor" || notes.env["LOCAL_MCP_TEST_TOKEN"] != "token-value-from-env" || notes.env["PATH"] == "" || notes.dir != folder {
		t.Fatalf("notes = %+v (sources %v)", notes, notes.Sources)
	}
	launch, _ := notes.launch()
	if !strings.Contains(launch, program) || !strings.Contains(launch, "--secret-arg-value") {
		t.Fatalf("launch = %s", launch)
	}
	missing := byName["missing"]
	if missing.Importable || !strings.Contains(missing.Note, "not installed") || !strings.Contains(missing.Note, "NOT_SET_ANYWHERE") {
		t.Fatalf("missing = %+v", missing)
	}
	screen, _ := json.Marshal(got)
	for _, secret := range []string{"token-value-from-env", "--secret-arg-value", program} {
		if strings.Contains(string(screen), secret) {
			t.Fatalf("the list for the screen carries %q: %s", secret, screen)
		}
	}
}
