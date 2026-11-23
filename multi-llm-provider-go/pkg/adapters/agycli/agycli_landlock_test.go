package agycli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAgyMCPLaunchReadPathsAdmitsOnlyPrivateCatalogPrograms(t *testing.T) {
	home, external := t.TempDir(), t.TempDir()
	program := filepath.Join(external, "mcpbridge")
	helper := filepath.Join(external, "helper.js")
	unrelated := filepath.Join(external, "account.env")
	for _, path := range []string{program, helper, unrelated} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(home, ".gemini", "config", "mcp_config.json")
	if err := agyWritePrivateJSON(cfg, map[string]any{"mcpServers": map[string]any{"bridge": map[string]any{"command": program, "args": []string{helper}, "env": map[string]string{"PRIVATE_ACCOUNT_FILE": unrelated}}}}); err != nil {
		t.Fatal(err)
	}
	paths := agyMCPLaunchReadPaths(home)
	for _, want := range []string{program, helper} {
		if !slices.Contains(paths, want) {
			t.Errorf("MCP program/file argument missing: %s", want)
		}
	}
	for _, forbidden := range []string{external, unrelated, home} {
		if slices.Contains(paths, forbidden) {
			t.Errorf("unexpected broad or credential grant: %s", forbidden)
		}
	}
	if paths := agyMCPLaunchReadPaths(t.TempDir()); len(paths) != 0 {
		t.Fatalf("missing private catalog granted paths: %v", paths)
	}
}
