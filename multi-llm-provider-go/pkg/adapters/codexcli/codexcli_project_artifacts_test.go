package codexcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRenderCodexProjectConfigTOMLBasicShape locks in the exact TOML
// emitted for a typical MCP server map. Output must be deterministic
// (keys sorted) so byte-compare assertions in cleanup tests are stable.
func TestRenderCodexProjectConfigTOMLBasicShape(t *testing.T) {
	withoutPersonalCodexMCPServers(t)
	startup := 12
	enabled := true
	servers := map[string]codexMCPServerSpec{
		"api-bridge": {
			Command:           "/usr/local/bin/mcpbridge",
			Args:              []string{"--port", "9000"},
			Env:               map[string]string{"MCP_API_URL": "http://localhost:9000", "TOKEN": "abc"},
			EnvVars:           []string{"LOCAL_TOKEN"},
			StartupTimeoutSec: &startup,
			Enabled:           &enabled,
		},
	}
	got := renderCodexProjectConfigTOML(servers)
	expected := []string{
		`service_tier = "default"`,
		`check_for_update_on_startup = false`,
		`disable_paste_burst = true`,
		"[tui]",
		`notifications = false`,
		`animations = false`,
		`show_tooltips = false`,
		"[apps._default]",
		"[features]",
		`remote_plugin = false`,
		`skill_mcp_dependency_install = false`,
		"[mcp_servers.api-bridge]",
		`command = "/usr/local/bin/mcpbridge"`,
		`args = ["--port", "9000"]`,
		`env_vars = ["LOCAL_TOKEN"]`,
		"startup_timeout_sec = 12",
		"enabled = true",
		"[mcp_servers.api-bridge.env]",
		`MCP_API_URL = "http://localhost:9000"`,
		`TOKEN = "abc"`,
	}
	for _, want := range expected {
		if !strings.Contains(got, want) {
			t.Errorf("expected TOML to contain %q; got:\n%s", want, got)
		}
	}

	// Env keys must appear in sorted order so the byte output is
	// stable across goroutine schedules. MCP_API_URL < TOKEN
	// lexicographically. Use the assignment suffix as the search key
	// to avoid matching "TOKEN" inside the unrelated "LOCAL_TOKEN"
	// entry in env_vars above.
	mcpIdx := strings.Index(got, `MCP_API_URL = `)
	tokenIdx := strings.Index(got, `TOKEN = "abc"`)
	if mcpIdx == -1 || tokenIdx == -1 || mcpIdx > tokenIdx {
		t.Errorf("env keys must be sorted lexicographically (MCP_API_URL before TOKEN); mcpIdx=%d tokenIdx=%d\nfull TOML:\n%s", mcpIdx, tokenIdx, got)
	}
}

// TestTomlQuoteKeyBareVsQuoted covers the bare-key vs quoted-key
// decision boundary. Server names like "api-bridge" stay bare per
// TOML 1.0; names with dots, spaces, or other punctuation force a
// quoted-key form.
func TestTomlQuoteKeyBareVsQuoted(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"api-bridge", "api-bridge"},
		{"my_server", "my_server"},
		{"Server123", "Server123"},
		{"with.dot", `"with.dot"`},
		{"has space", `"has space"`},
		{"", `""`},
	}
	for _, c := range cases {
		if got := tomlQuoteKey(c.in); got != c.want {
			t.Errorf("tomlQuoteKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWriteCodexProjectConfigTOMLWithoutMCPUsesAgentWorksDefaults(t *testing.T) {
	withoutPersonalCodexMCPServers(t)
	tmp := t.TempDir()
	cleanup, err := writeCodexProjectConfigTOML(tmp, "")
	if err != nil {
		t.Fatalf("writeCodexProjectConfigTOML: %v", err)
	}

	configPath := filepath.Join(tmp, ".codex", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config.toml: %v", err)
	}
	for _, want := range []string{
		`service_tier = "default"`,
		`check_for_update_on_startup = false`,
		`disable_paste_burst = true`,
		"[tui]",
		`notifications = false`,
		`animations = false`,
		`show_tooltips = false`,
		"[apps._default]",
		"[features]",
		`remote_plugin = false`,
		`skill_mcp_dependency_install = false`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("generated config missing %q; got:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "[mcp_servers.") {
		t.Fatalf("empty MCP input must not create MCP server blocks; got:\n%s", data)
	}

	cleanup()
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("cleanup must remove generated config.toml; stat err=%v", err)
	}
}

// (TestWriteCodexProjectDenyBuiltinHooks{Lifecycle,RestoresOperatorScript}
// and TestWriteCodexProjectArtifacts{ComposesAllArtifacts,HooksGatedByEnvVar}
// were removed when the codex .codex/hooks.json projection was deleted.
// Codex deny-builtin behavior now ships via CLI --disable flags
// (codexBridgeOnlyDisabledFeatures in options.go), not via a workspace
// hook script. See the comment block in writeCodexProjectArtifacts +
// codexcli_project_artifacts.go's deleted-helper note for the
// rationale. The remaining ProjectArtifacts tests cover AGENTS.md +
// .codex/config.toml lifecycles, which still ship as workspace
// projections.)

// TestWriteCodexProjectArtifactsComposesAGENTSAndConfigTOML covers
// the post-hooks-removal contract: the composite writer drops exactly
// AGENTS.md + .codex/config.toml when both are non-empty, and the
// cleanup tears both down. Hooks-projection-related expectations
// have been removed.
func TestWriteCodexProjectArtifactsComposesAGENTSAndConfigTOML(t *testing.T) {
	withoutPersonalCodexMCPServers(t)
	tmp := t.TempDir()
	prompt := "Run cargo test before committing."
	mcpJSON := `{"api-bridge":{"command":"/opt/mcpbridge"}}`

	// denyBuiltins=true is now a no-op on codex; we keep passing it
	// to lock in that the parameter remains accepted on the function
	// signature for API symmetry with the other adapters.
	cleanup, err := writeCodexProjectArtifacts(tmp, prompt, mcpJSON, true)
	if err != nil {
		t.Fatalf("writeCodexProjectArtifacts: %v", err)
	}

	expected := []string{
		filepath.Join(tmp, "AGENTS.md"),
		filepath.Join(tmp, ".codex", "config.toml"),
	}
	for _, path := range expected {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected artifact %q to exist after write: %v", path, err)
		}
	}

	// Hooks files MUST NOT land — they were removed as a feature.
	for _, path := range []string{
		filepath.Join(tmp, ".codex", "hooks.json"),
		filepath.Join(tmp, ".codex", "hooks", "deny-builtin.sh"),
	} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("hooks artifact %q must NOT be created — that projection was removed; use codex --disable flags instead", path)
		}
	}

	cleanup()
	for _, path := range expected {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("cleanup must remove artifact %q created from nothing; stat err=%v", path, err)
		}
	}
}

// TestWriteCodexProjectArtifactsEmptyWorkingDirNoOps guards the
// orchestrator-cwd safety: empty workingDir must short-circuit.
func TestWriteCodexProjectArtifactsEmptyWorkingDirNoOps(t *testing.T) {
	cleanup, err := writeCodexProjectArtifacts("", "anything", `{"x":{"command":"y"}}`, true)
	if err != nil {
		t.Errorf("empty workingDir should return nil error; got %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup must be non-nil even for empty workingDir (no-op cleanup)")
	}
	cleanup() // must not panic
	for _, leak := range []string{"AGENTS.md", ".codex"} {
		if _, err := os.Stat(leak); err == nil {
			t.Errorf("%q must NOT be created in process cwd when workingDir is empty", leak)
			_ = os.RemoveAll(leak)
		}
	}
}

// The person's own MCP servers (from the config of the Codex home this session
// uses) are switched off in the session profile, and the AgentWorks bridge is
// never on that list.
func TestCodexSessionProfileDisablesPersonalMCPServers(t *testing.T) {
	home := t.TempDir()
	config := "[mcp_servers.agentworks]\nurl = \"https://x\"\n[mcp_servers.computer-use]\ncommand = \"c\"\n[mcp_servers.computer-use.env]\nA = \"1\"\n[mcp_servers.api-bridge]\ncommand = \"b\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	personal := personalCodexMCPServers(home)
	if strings.Join(personal, ",") != "agentworks,api-bridge,computer-use" {
		t.Fatalf("personal servers = %v", personal)
	}
	got := renderCodexConfigTOML(map[string]codexMCPServerSpec{"api-bridge": {Command: "/bin/bridge"}}, personal)
	for _, want := range []string{"[mcp_servers.agentworks]\nenabled = false", "[mcp_servers.computer-use]\nenabled = false"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Count(got, "computer-use") != 1 {
		t.Errorf("a sub-table must not become its own server:\n%s", got)
	}
	if strings.Contains(got, "api-bridge]\nenabled = false") {
		t.Errorf("the bridge must never be disabled:\n%s", got)
	}
	if none := personalCodexMCPServers(t.TempDir()); len(none) != 0 {
		t.Errorf("a home without a config disables nothing, got %v", none)
	}
}

// withoutPersonalCodexMCPServers keeps a unit test independent of the
// developer's own ~/.codex/config.toml.
func withoutPersonalCodexMCPServers(t *testing.T) {
	t.Helper()
	orig := personalCodexMCPServers
	t.Cleanup(func() { personalCodexMCPServers = orig })
	personalCodexMCPServers = func(string) []string { return nil }
}

// Leftover session profiles older than a day are removed from a Codex home;
// a young one (a live session), other files and non-profile names stay.
func TestSweepStaleCodexProfiles(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, "agentworks-1234567890.config.toml")
	young := filepath.Join(home, "agentworks-987.config.toml")
	notProfile := filepath.Join(home, "agentworks-notes.config.toml")
	config := filepath.Join(home, "config.toml")
	for _, f := range []string{old, young, notProfile, config} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-48 * time.Hour)
	for _, f := range []string{old, notProfile, config} {
		if err := os.Chtimes(f, past, past); err != nil {
			t.Fatal(err)
		}
	}
	codexProfileSweeps.Delete(home)
	sweepStaleCodexProfiles(home)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("an old session profile survived")
	}
	for _, keep := range []string{young, notProfile, config} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%s must stay: %v", keep, err)
		}
	}
}
