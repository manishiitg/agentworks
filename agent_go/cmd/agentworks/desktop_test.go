package main

import (
	"os"
	"path/filepath"
	"testing"
)

// PLAT-856-434: standalone updater must not mutate the CLI inside an app.
func TestDesktopBundleRecognizesLinkedCLIOnlyInsideApp(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "AgentWorks.app")
	cli := filepath.Join(app, "Contents", "Resources", "bin", "agentworks")
	if err := os.MkdirAll(filepath.Dir(cli), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{cli, filepath.Join(app, "Contents", "Info.plist")} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "agentworks")
	if err := os.Symlink(cli, link); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(app)
	if got := desktopBundle(link); got != want {
		t.Fatalf("linked bundle = %q, want %q", got, want)
	}
	standalone := filepath.Join(root, "standalone")
	if err := os.WriteFile(standalone, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := desktopBundle(standalone); got != "" {
		t.Fatalf("standalone treated as bundle: %q", got)
	}
}
