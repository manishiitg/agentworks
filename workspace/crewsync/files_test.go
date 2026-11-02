package crewsync

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
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
	// Owner 2 Nov (PLAT-856-509): the owner's project secrets sync to their own server; publishing still skips them.
	write(local.Files, ".env", "API_KEY=x")
	write(local.Files, "keys/deploy.pem", "key")
	// Projects gitignore their secrets; they still reach the owner's server, other ignored files do not.
	write(local.Files, ".gitignore", ".env\nkeys/\n*.log\n")
	write(local.Files, "build.log", "ignored")
	write(local.Files, ".cache/token", "private")
	write(local.Resources, "product.json", "private")
	write(local.Resources, "skills/review/SKILL.md", "Review the changes")
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(local.Files, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	first, err := Capture(t.Context(), local, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 5 {
		t.Fatalf("unexpected files: %v", first.Files)
	}
	// The readiness count must say exactly what a first sync sends.
	if m, err := Measure(t.Context(), local); err != nil || m.Files != len(first.Files) {
		t.Fatalf("Measure counted %d files, Capture %d: %v", m.Files, len(first.Files), err)
	}
	for _, key := range []string{"files/.env", "files/keys/deploy.pem"} {
		if _, ok := first.Files[key]; !ok || !publication.Excluded(strings.TrimPrefix(key, "files/")) {
			t.Fatalf("%s: synced=%v, publication must still exclude it", key, ok)
		}
	}
	if _, ok := first.Files["files/build.log"]; ok {
		t.Fatal("a gitignored file that is not a credential synced")
	}
	if err = Apply(t.Context(), server, first); err != nil {
		t.Fatal(err)
	}
	write(server.Files, "src/main.txt", "server changed")
	back, err := Capture(t.Context(), server, first.Files, t.TempDir())
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
	excluded, err := Capture(t.Context(), local, back.Files, t.TempDir())
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
	included, err := Capture(t.Context(), local, excluded.Files, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(t.Context(), server, included); err != nil {
		t.Fatalf("re-included file lost its baseline: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(server.Files, "src/main.txt")); err != nil || string(raw) != "edited while excluded" {
		t.Fatalf("re-included file: %q %v", raw, err)
	}
	for _, bad := range []string{"files/../outside", "files/.cache/token", "resources/product.json", "resources/_files/escape"} {
		data := []byte("x")
		file := fingerprint(data, false)
		if err = Apply(t.Context(), server, Snapshot{Files: Manifest{bad: file}, Changes: []Change{{Path: bad, After: &file, DataPath: testDataPath(t, data)}}}); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func testDataPath(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "content")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func testChangeData(t *testing.T, c Change) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := c.CopyTo(t.Context(), &b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// PLAT-856-509 / PR845: cancellation after the first streamed write must not
// turn a valid destination into a partial file that refuses the next retry.
type cancelDuringApply struct {
	context.Context
	cancel         context.CancelFunc
	folder, target string
}

func (c cancelDuringApply) Err() error {
	paths, _ := filepath.Glob(filepath.Join(c.folder, ".aw-sync-*.tmp"))
	paths = append(paths, c.target)
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.Size() >= 64<<10 {
			c.cancel()
			break
		}
	}
	return c.Context.Err()
}
func TestCanceledSyncApplyPreservesOriginalAndRetries(t *testing.T) {
	source, dest := t.TempDir(), t.TempDir()
	before := []byte("original")
	after := bytes.Repeat([]byte("replacement-data"), 32<<10)
	name := "note.txt"
	target := filepath.Join(dest, name)
	if err := os.WriteFile(filepath.Join(source, name), after, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, before, 0640); err != nil {
		t.Fatal(err)
	}
	prior := Manifest{"resources/" + name: fingerprint(before, false)}
	snap, err := Capture(t.Context(), crewroot.Roots{Files: source, Resources: source}, prior, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := cancelDuringApply{base, cancel, dest, target}
	roots := crewroot.Roots{Files: dest, Resources: dest}
	if err = Apply(ctx, roots, snap); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected mid-copy cancellation, got %v", err)
	}
	if actual, err := os.ReadFile(target); err != nil || !bytes.Equal(actual, before) {
		t.Fatalf("canceled apply changed original: bytes=%d err=%v", len(actual), err)
	}
	if temps, _ := filepath.Glob(filepath.Join(dest, ".aw-sync-*.tmp")); len(temps) != 0 {
		t.Fatalf("canceled apply left temporary contents: %v", temps)
	}
	if err = Apply(t.Context(), roots, snap); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if actual, err := os.ReadFile(target); err != nil || !bytes.Equal(actual, after) {
		t.Fatal("retry lost replacement bytes")
	}
	if info, err := os.Stat(target); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0640 {
		t.Fatalf("replacement mode: %v %v", info, err)
	}
}
