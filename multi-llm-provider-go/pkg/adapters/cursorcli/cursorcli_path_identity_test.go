package cursorcli

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"

	"path/filepath"
	"testing"
)

func TestCursorCleanupPreservesUnrelatedAndPriorFiles(t *testing.T) {
	dir := t.TempDir()
	cursor := filepath.Join(dir, ".cursor")
	if err := os.Mkdir(cursor, 0700); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(cursor, "user-notes")
	if err := os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(cursor, "cli.json")
	original := `{"user":"original"}`
	if err := os.WriteFile(config, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	opts := &llmtypes.CallOptions{}
	WithMCPConfig(`{"mcpServers":{"fixture":{"command":"fixture-only"}}}`)(opts)
	cleanup, err := prepareCursorProjectFiles(dir, "fixture", opts, "fixture-owner")
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	for path, want := range map[string]string{unrelated: "keep", config: original} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("cleanup changed %s: %v", path, err)
		}
	}
}
