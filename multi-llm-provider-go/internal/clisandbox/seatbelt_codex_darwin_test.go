//go:build darwin

package clisandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A Seatbelt-confined Codex gets a CODEX_HOME of its own with the person's login
// linked in, so the person's own MCP servers and config are not loaded.
func TestSeatbeltCodexGetsItsOwnCodexHomeWithTheLoginLinked(t *testing.T) {
	account := t.TempDir()
	if err := os.MkdirAll(filepath.Join(account, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	auth := filepath.Join(account, ".codex", "auth.json")
	if err := os.WriteFile(auth, []byte(`{"login":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", account)
	home := filepath.Join(t.TempDir(), "private")
	policy := &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: "codex-cli", Seatbelt: true, PrivateHome: home}
	opts := &llmtypes.CallOptions{CLISecurity: policy}
	if got := llmtypes.SandboxHomeEnvironment(opts)["CODEX_HOME"]; got != filepath.Join(home, ".codex") {
		t.Fatalf("CODEX_HOME = %q, want the private home's .codex", got)
	}
	if _, _, err := SeatbeltArgs(policy, []string{"codex"}, t.TempDir(), SeatbeltGrants{}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(canonical(home), ".codex", "auth.json")
	if target, err := os.Readlink(link); err != nil || canonical(target) != canonical(auth) {
		t.Fatalf("login not linked: %q, %v", target, err)
	}
	// Other CLIs are untouched: they keep the person's own home.
	other := &llmtypes.CallOptions{CLISecurity: &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: "claude-code", Seatbelt: true, PrivateHome: home}}
	if env := llmtypes.SandboxHomeEnvironment(other); env != nil {
		t.Fatalf("claude-code must keep the person's home under Seatbelt, got %v", env)
	}
}
