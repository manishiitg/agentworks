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
		"Workflow/a":             "team",
		"/Workflow/a/planning/x": "team",
		"Workflow/ab":            "",
		"Workflow":               "",
		"":                       "",
	} {
		if got := ServerFor(root, path); got != want {
			t.Errorf("ServerFor(%q) = %q, want %q", path, got, want)
		}
	}
}
