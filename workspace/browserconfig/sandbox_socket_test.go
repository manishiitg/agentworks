package browserconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxSocketDirIsTheSharedTmpCopyOfTheManagedFolder(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	session := "project-1f505da0c06ad9bd--browser"
	want := filepath.Join(docs, "tmp", ".agent-browser", "o", "p1f505da0c06ad9bd")
	if got := SandboxSocketDir(session); got != want {
		t.Fatalf("SandboxSocketDir = %q, want %q", got, want)
	}
	if err := os.MkdirAll(want, 0o700); err != nil {
		t.Fatal(err)
	}
	if dirs := SandboxSocketDirs(); len(dirs) != 1 || dirs[0] != want {
		t.Fatalf("SandboxSocketDirs = %v", dirs)
	}
	if SandboxSocketDir("shared-browser") != "" {
		t.Fatal("an unmanaged session has no per-owner folder")
	}
	t.Setenv("WORKSPACE_DOCS_PATH", "")
	t.Setenv("DOCS_DIR", "")
	if SandboxSocketDir(session) != "" || SandboxSocketDirs() != nil {
		t.Fatal("without a docs folder there is nothing to look in")
	}
}
