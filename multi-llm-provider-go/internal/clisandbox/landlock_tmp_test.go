//go:build linux

package clisandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The shared /tmp is granted to Cursor only: it needs fixed socket paths there. Every other CLI
// keeps to its private TMPDIR.
func TestSharedTmpIsGrantedToCursorOnly(t *testing.T) {
	for provider, want := range map[string]bool{
		"cursor-cli": true, "Cursor-CLI": true,
		"muse-cli": false, "claude-code": false, "codex-cli": false, "pi-cli": false, "agy-cli": false,
	} {
		dir := t.TempDir()
		runner := filepath.Join(dir, "runner")
		if err := os.WriteFile(runner, nil, 0o700); err != nil {
			t.Fatal(err)
		}
		policy := &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: provider, LandlockRunner: runner, PrivateHome: filepath.Join(dir, "home"), CredentialHome: dir}
		args, cleanup, err := LandlockArgs(policy, []string{"sh"}, dir, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(args[2])
		cleanup()
		if err != nil {
			t.Fatal(err)
		}
		var got landlockPolicy
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		has := false
		for _, path := range got.WritePaths {
			if path == "/tmp" {
				has = true
			}
		}
		if has != want {
			t.Fatalf("%s: /tmp granted = %v, want %v (%s)", provider, has, want, raw)
		}
	}
}
