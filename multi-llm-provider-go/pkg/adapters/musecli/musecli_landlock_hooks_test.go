package musecli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMuseManagedHooksDirsFollowTheSettings(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(root, "settings.json")
	hooks := filepath.Join(root, "agent-hooks", "muse-hooks.json")
	if err := os.WriteFile(settings, []byte(`{"managed_hooks_path":"`+hooks+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := museManagedHooksDirs([]string{settings}); len(got) != 1 || got[0] != filepath.Dir(hooks) {
		t.Fatalf("dirs = %v, want %s", got, filepath.Dir(hooks))
	}
	if got := museManagedHooksDirs([]string{filepath.Join(root, "missing.json")}); len(got) != 0 {
		t.Fatalf("a missing settings file granted %v", got)
	}
}
