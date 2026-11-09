package workproduct

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateGrantAcceptsExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	canonical, err := ValidateGrant(dir, "proj", AccessReadWrite)
	if err != nil {
		t.Fatalf("ValidateGrant failed: %v", err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	if canonical != filepath.Clean(want) {
		t.Fatalf("canonical path = %q, want %q", canonical, want)
	}
}

func TestValidateGrantRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ path, alias, access string }{
		"relative path":  {path: "some/relative", alias: "a", access: AccessReadOnly},
		"missing dir":    {path: filepath.Join(dir, "nope"), alias: "a", access: AccessReadOnly},
		"file not dir":   {path: file, alias: "a", access: AccessReadOnly},
		"empty alias":    {path: dir, alias: "  ", access: AccessReadOnly},
		"junk alias":     {path: dir, alias: "!!!", access: AccessReadOnly},
		"unknown access": {path: dir, alias: "a", access: "admin"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateGrant(tc.path, tc.alias, tc.access); err == nil {
				t.Fatalf("ValidateGrant(%q, %q, %q) unexpectedly accepted", tc.path, tc.alias, tc.access)
			}
		})
	}
}

func TestResolveGrantsTrustsStoredPaths(t *testing.T) {
	dir := t.TempDir()
	grants := []FolderGrant{
		{ID: "1", Alias: "proj", Path: dir, Access: AccessReadWrite},
		{ID: "2", Alias: "docs", Path: dir, Access: AccessReadOnly},
	}
	read, write, readOnly, env := ResolveGrants(grants)
	if len(read) != 1 || len(write) != 1 {
		t.Fatalf("expected deduped read+write paths, got read=%v write=%v", read, write)
	}
	if len(readOnly) != 1 || readOnly[0] != filepath.Clean(dir) {
		t.Fatalf("expected read-only path to be preserved for write-deny enforcement, got %v", readOnly)
	}
	if env["WORK_FOLDER_PROJ"] == "" || env["WORK_FOLDER_DOCS"] == "" {
		t.Fatalf("expected WORK_FOLDER_ env vars, got %v", env)
	}
	// Liveness is reported separately for the UI, never by dropping
	// grants at resolve time (same contract as workflow grants).
	if !FolderGrantAvailable(dir) {
		t.Fatal("live dir reported unavailable")
	}
	if FolderGrantAvailable(filepath.Join(dir, "missing")) {
		t.Fatal("missing path reported available")
	}
}

func TestBuildAttachedFoldersPrompt(t *testing.T) {
	empty := BuildAttachedFoldersPrompt(nil)
	if !strings.Contains(empty, "### Attached Folders") || !strings.Contains(empty, "files action=folder_attach") {
		t.Fatalf("empty prompt section = %q", empty)
	}
	withGrants := BuildAttachedFoldersPrompt([]FolderGrant{
		{ID: "1", Alias: "proj", Access: AccessReadWrite},
	})
	if !strings.Contains(withGrants, "**proj**") || !strings.Contains(withGrants, "$WORK_FOLDER_PROJ") {
		t.Fatalf("grant prompt section = %q", withGrants)
	}
}

func TestPathWithinRootsIsSeparatorSafe(t *testing.T) {
	if !PathWithinRoots("/data/proj", []string{"/data/proj"}) {
		t.Fatal("exact root should match")
	}
	if !PathWithinRoots("/data/proj/sub/dir", []string{"/data/proj"}) {
		t.Fatal("nested path should match")
	}
	for _, outside := range []string{"/data/proj2", "/data/proj2/sub", "/other", "relative/path"} {
		if PathWithinRoots(outside, []string{"/data/proj"}) {
			t.Fatalf("path %q must not match root /data/proj", outside)
		}
	}
	if PathWithinRoots("/data/proj/sub", nil) {
		t.Fatal("no roots must match nothing")
	}
}

func TestValidateGrantForUserEnforcesRoots(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "site")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	if _, err := ValidateGrantForUser(inside, "site", AccessReadOnly, []string{root}, false); err != nil {
		t.Fatalf("in-roots attach rejected: %v", err)
	}
	if _, err := ValidateGrantForUser(outside, "other", AccessReadOnly, []string{root}, false); err == nil {
		t.Fatal("out-of-roots attach accepted for ordinary user")
	}
	if _, err := ValidateGrantForUser(inside, "site", AccessReadOnly, nil, false); err == nil {
		t.Fatal("attach with no assigned roots accepted for ordinary user")
	}
	if _, err := ValidateGrantForUser(outside, "other", AccessReadWrite, nil, true); err != nil {
		t.Fatalf("admin attach rejected: %v", err)
	}
	// Symlink escape: a link inside the roots pointing outside must fail.
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateGrantForUser(link, "escape", AccessReadOnly, []string{root}, false); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func TestNormalizeRootsDropsUnusable(t *testing.T) {
	root := t.TempDir()
	roots := NormalizeRoots([]string{root, filepath.Join(root, "missing"), "relative", ""})
	if len(roots) != 1 {
		t.Fatalf("expected only the live root, got %v", roots)
	}
}
