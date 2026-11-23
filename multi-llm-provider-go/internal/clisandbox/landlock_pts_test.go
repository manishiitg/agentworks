//go:build linux

package clisandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestAgyRequestsPrivateTerminalsWithoutGrantingHostPTYs(t *testing.T) {
	for _, provider := range []string{"agy-cli", "pi-cli", "muse-cli"} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			runner := filepath.Join(root, "runner")
			if err := os.WriteFile(runner, nil, 0700); err != nil {
				t.Fatal(err)
			}
			policy := &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: provider, LandlockRunner: runner, PrivateHome: filepath.Join(root, "home")}
			args, cleanup, err := LandlockArgs(policy, []string{"/bin/sh"}, root, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			raw, err := os.ReadFile(args[2])
			if err != nil {
				t.Fatal(err)
			}
			var got landlockPolicy
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if got.PrivatePTS != (provider == "agy-cli") {
				t.Fatalf("private terminals for %s: %s", provider, raw)
			}
			for _, paths := range [][]string{got.ReadPaths, got.WritePaths} {
				for _, path := range paths {
					if path == "/dev/pts" || path == "/dev/ptmx" {
						t.Fatalf("host terminal grant: %s", raw)
					}
				}
			}
		})
	}
}
