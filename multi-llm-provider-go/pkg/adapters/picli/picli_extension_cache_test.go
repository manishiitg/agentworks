package picli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfinedPiGetsItsOwnExtensionCache(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared-agent")
	if err := os.MkdirAll(filepath.Join(shared, "tmp", "extensions", "pkg"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", shared)
	agent := filepath.Join(root, "session", "agent")

	// Unconfined launches keep sharing the cache.
	linkSharedPiExtensionCache(agent, false)
	target := filepath.Join(agent, "tmp", "extensions")
	if link, err := os.Readlink(target); err != nil || link != filepath.Join(shared, "tmp", "extensions") {
		t.Fatalf("unconfined link = %q, %v", link, err)
	}

	// A confined launch replaces that link with its own folder, and never writes the shared one.
	linkSharedPiExtensionCache(agent, true)
	info, err := os.Lstat(target)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("confined cache should be a real folder: %v %v", info, err)
	}
	if err := os.WriteFile(filepath.Join(target, "installed"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(shared, "tmp", "extensions", "installed")); err == nil {
		t.Fatal("the confined install reached the shared cache")
	}
}
