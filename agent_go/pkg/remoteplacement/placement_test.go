package remoteplacement

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerFor(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace-docs")
	t.Setenv(FileEnv, "")
	if IsRemote(root, "Workflow/a") {
		t.Fatal("no placement file must mean local")
	}
	if rel, err := filepath.Rel(root, File(root)); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("placement file %s is inside the docs root", File(root))
	}
	if err := os.WriteFile(File(root), []byte(`{"workflows":{"Workflow/a":"team"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"Workflow/a":                             "team",
		"/Workflow/a/planning/x":                 "team",
		filepath.Join(root, "Workflow/a/runs/1"): "team",
		"Workflow/ab":                            "",
		"Workflow":                               "",
		"":                                       "",
	} {
		if got := ServerFor(root, path); got != want {
			t.Errorf("ServerFor(%q) = %q, want %q", path, got, want)
		}
	}
	if got, ok := LocalScratchDir(root, filepath.Join(root, "Workflow/a/runs/1")); !ok || got != filepath.Join(root, ScratchRelPath, "team/a/runs/1") {
		t.Fatalf("absolute remote scratch = %q, %v", got, ok)
	}
}
