package remoteplacement

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServerFor(t *testing.T) {
	root := t.TempDir()
	if IsRemote(root, "Workflow/a") {
		t.Fatal("no placement file must mean local")
	}
	if err := os.MkdirAll(filepath.Join(root, "_system"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, RelPath), []byte(`{"workflows":{"Workflow/a":"team"}}`), 0o600); err != nil {
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
