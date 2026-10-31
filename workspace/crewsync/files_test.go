package crewsync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-387: a return sync must preserve edits outside AgentWorks and must
// never carry credentials, platform records, or symlinks out of a project.
func TestRoundTripPreservesExternalEditsAndPrivateFiles(t *testing.T) {
	makeRoots := func() crewroot.Roots {
		base := t.TempDir()
		r := crewroot.Roots{Files: filepath.Join(base, "source"), Resources: filepath.Join(base, "resources")}
		for _, dir := range []string{r.Files, r.Resources} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
		}
		return r
	}
	local, server := makeRoots(), makeRoots()
	write := func(root, name, value string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(local.Files, "src/main.txt", "first")
	write(local.Files, ".env", "private")
	write(local.Resources, "product.json", "private")
	write(local.Resources, "skills/review/SKILL.md", "Review the changes")
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(local.Files, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	first, err := Capture(t.Context(), local, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 2 {
		t.Fatalf("unexpected files: %v", first.Files)
	}
	if err = Apply(t.Context(), server, first); err != nil {
		t.Fatal(err)
	}
	write(server.Files, "src/main.txt", "server changed")
	back, err := Capture(t.Context(), server, first.Files)
	if err != nil {
		t.Fatal(err)
	}
	write(local.Files, "src/main.txt", "external local edit")
	if err = Apply(t.Context(), local, back); err == nil {
		t.Fatal("overwrote an external edit")
	}
	actual, _ := os.ReadFile(filepath.Join(local.Files, "src/main.txt"))
	if string(actual) != "external local edit" {
		t.Fatal("local edit lost")
	}
	write(local.Files, "src/main.txt", "first")
	if err = Apply(t.Context(), local, back); err != nil {
		t.Fatal(err)
	}
	if err = Apply(t.Context(), local, back); err != nil {
		t.Fatalf("retry: %v", err)
	}
	// PLAT-856-423: excluding an existing file is not a request to delete its copy.
	write(local.Files, ".gitignore", "src/main.txt\n")
	excluded, err := Capture(t.Context(), local, back.Files)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range excluded.Changes {
		if change.Path == "files/src/main.txt" {
			t.Fatal("newly ignored file was treated as deleted")
		}
	}
	if err := Apply(t.Context(), server, excluded); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(server.Files, "src/main.txt")); err != nil || string(raw) != "server changed" {
		t.Fatalf("ignored destination copy changed: %q %v", raw, err)
	}
	// PLAT-856-424: ignoring and later re-including work retains its sync base.
	write(local.Files, "src/main.txt", "edited while excluded")
	write(local.Files, ".gitignore", "")
	included, err := Capture(t.Context(), local, excluded.Files)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(t.Context(), server, included); err != nil {
		t.Fatalf("re-included file lost its baseline: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(server.Files, "src/main.txt")); err != nil || string(raw) != "edited while excluded" {
		t.Fatalf("re-included file: %q %v", raw, err)
	}
	for _, bad := range []string{"files/../outside", "files/.env", "resources/product.json", "resources/_files/escape"} {
		data := []byte("x")
		file := fingerprint(data, false)
		if err = Apply(t.Context(), server, Snapshot{Files: Manifest{bad: file}, Changes: []Change{{Path: bad, After: &file, Data: data}}}); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
