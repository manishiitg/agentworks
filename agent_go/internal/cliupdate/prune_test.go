package cliupdate

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestPruneOldVersionsKeepsCurrentAndRunningReleases(t *testing.T) {
	home := t.TempDir()
	versions := filepath.Join(home, ".local/share/cursor-agent/versions")
	for _, v := range []string{"2026.09.18-a", "2026.09.23-b", "2026.09.26-c"} {
		if err := os.MkdirAll(filepath.Join(versions, v), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(versions, v, "cursor-agent"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(home, ".local/bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bin, "cursor-agent")
	if err := os.Symlink(filepath.Join(versions, "2026.09.26-c", "cursor-agent"), link); err != nil {
		t.Fatal(err)
	}
	realVersions, _ := filepath.EvalSymlinks(versions)
	restore := runningCommandLines
	defer func() { runningCommandLines = restore }()
	runningCommandLines = func(context.Context) (string, error) {
		return "node " + filepath.Join(realVersions, "2026.09.23-b", "index.js") + " --resume x\n", nil
	}

	removed, err := pruneOldVersions(context.Background(), "cursor-agent", link)
	if err != nil || strings.Join(removed, ",") != "2026.09.18-a" {
		t.Fatalf("removed %v err %v", removed, err)
	}
	left, _ := os.ReadDir(versions)
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "2026.09.23-b,2026.09.26-c" {
		t.Fatalf("left %v", names)
	}

	// Once the old chat exits, the next check removes it too.
	runningCommandLines = func(context.Context) (string, error) { return "", nil }
	if removed, _ := pruneOldVersions(context.Background(), "cursor-agent", link); strings.Join(removed, ",") != "2026.09.23-b" {
		t.Fatalf("idle release kept: %v", removed)
	}

	// If running processes cannot be listed, nothing is removed.
	if err := os.MkdirAll(filepath.Join(versions, "2026.09.01-z"), 0o755); err != nil {
		t.Fatal(err)
	}
	runningCommandLines = func(context.Context) (string, error) { return "", os.ErrPermission }
	if removed, _ := pruneOldVersions(context.Background(), "cursor-agent", link); len(removed) != 0 {
		t.Fatalf("pruned without process list: %v", removed)
	}
}

// A "versions" directory that is not the CLI's own store (e.g. nvm's
// ~/.nvm/versions/node/*) is never touched.
func TestPruneOldVersionsIgnoresOtherLayouts(t *testing.T) {
	home := t.TempDir()
	nvm := filepath.Join(home, ".nvm/versions/node")
	for _, v := range []string{"v20", "v22"} {
		if err := os.MkdirAll(filepath.Join(nvm, v, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(nvm, "v22/bin/claude")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := runningCommandLines
	defer func() { runningCommandLines = restore }()
	runningCommandLines = func(context.Context) (string, error) { return "", nil }
	for _, provider := range []string{"claude", "pi", "codex"} {
		if removed, err := pruneOldVersions(context.Background(), provider, exe); len(removed) != 0 || err != nil {
			t.Fatalf("%s pruned outside its store: %v %v", provider, removed, err)
		}
	}
	if _, err := os.Stat(filepath.Join(nvm, "v20")); err != nil {
		t.Fatal("nvm install removed")
	}
}
