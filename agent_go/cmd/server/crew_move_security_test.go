package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// PLAT-450 applied to the Crew move: no file of any Crew is ever read or written through a symlink, and nothing of
// another user's is ever copied, listed under a different owner, or changed.
func TestCrewMoveNeverFollowsSymlinks(t *testing.T) {
	f := newCrewMoveFixture(t)
	// The victim: a private file, and a Crew of their own.
	secret := filepath.Join(f.Docs, "_users", "zvictim", "Chats", "secret.txt")
	f.write("_users/zvictim/Chats/secret.txt", "victim secret", 0o600)
	f.crew("zvictim", "vic-eeee5555", "c-v")
	victimManifest := filepath.Join(f.source("zvictim", "vic-eeee5555"), "product.json")
	manifestBefore, _ := os.ReadFile(victimManifest)
	victimDigest := treeDigestOf(t, f.source("zvictim", "vic-eeee5555"))
	link := func(target, at string) {
		t.Helper()
		if err := os.Symlink(target, at); err != nil {
			t.Fatal(err)
		}
	}
	// (a) an absolute symlink and (b) a relative one that climbs out, inside the attacker's own Crew
	f.crew("mallory", "evil-ffff6666", "c-e")
	evil := f.source("mallory", "evil-ffff6666")
	link(secret, filepath.Join(evil, "absolute-link"))
	link("../../../../../zvictim/Chats/secret.txt", filepath.Join(evil, "code", "relative-link"))
	// (c) a symlinked project folder pointing at the victim's Crew
	projects := filepath.Join(f.Docs, "_users", "mallory", "Chats", "Work", "projects")
	link(f.source("zvictim", "vic-eeee5555"), filepath.Join(projects, "bait-1111aaaa"))
	// (d) a symlinked manifest inside an otherwise ordinary Crew
	f.crew("mallory", "mani-aaaa7777", "c-m")
	if err := os.Remove(filepath.Join(f.source("mallory", "mani-aaaa7777"), "product.json")); err != nil {
		t.Fatal(err)
	}
	link(victimManifest, filepath.Join(f.source("mallory", "mani-aaaa7777"), "product.json"))
	// (e) a symlinked user directory that points at the victim's tree
	link(filepath.Join(f.Docs, "_users", "zvictim"), filepath.Join(f.Docs, "_users", "yattacker"))
	// (f) a named pipe inside a Crew
	f.crew("mallory", "pipe-bbbb8888", "c-p")
	if err := syscall.Mkfifo(filepath.Join(f.source("mallory", "pipe-bbbb8888"), "code", "pipe"), 0o660); err != nil {
		t.Skip("cannot make a fifo here")
	}

	report, err := f.run()
	if err != nil {
		t.Fatal(err)
	}
	blockers := map[string]string{}
	for _, plan := range report.Crews {
		blockers[plan.Owner+"/"+plan.Folder] = strings.Join(plan.Blockers, " | ")
	}
	for key, want := range map[string]string{
		"mallory/evil-ffff6666": "leaves the Crew folder",
		"mallory/mani-aaaa7777": "UNSAFE_PATH",
		"mallory/pipe-bbbb8888": "special file",
	} {
		if !strings.Contains(blockers[key], want) {
			t.Errorf("%s: blockers = %q, want %q", key, blockers[key], want)
		}
	}
	// The bait folder and the attacker's alias of the victim's tree are not Crews of the attacker's.
	for key := range blockers {
		if strings.HasPrefix(key, "mallory/bait") || strings.HasPrefix(key, "yattacker/") {
			t.Errorf("%s was listed as a Crew", key)
		}
	}
	// Apply: the victim's own Crew moves under its own name; none of the attacker's bait moves; nothing of the victim's
	// is ever copied under another name or changed.
	if _, err := f.run(apply); !errors.Is(err, errCrewMoveIncomplete) {
		t.Fatalf("apply returned %v", err)
	}
	// (the victim's own Crew moved, so its manifest is now at the shared root)
	if got, _ := os.ReadFile(filepath.Join(f.dest("vic-eeee5555"), "product.json")); string(got) != string(manifestBefore) {
		t.Fatal("the victim's manifest was changed through a symlink")
	}
	if got, _ := os.ReadFile(secret); string(got) != "victim secret" {
		t.Fatal("the victim's file was changed")
	}
	if treeDigestOf(t, f.dest("vic-eeee5555")) != victimDigest {
		t.Fatal("the victim's own Crew did not move intact under its own name")
	}
	entries, _ := os.ReadDir(filepath.Join(f.Docs, "Crew"))
	for _, e := range entries {
		switch e.Name() {
		case cmCrewA, cmCrewB, cmCrewC, "vic-eeee5555", ".migrating":
		default:
			t.Errorf("unexpected entry %q at the shared root (an attacker's Crew moved, or a victim's file was copied)", e.Name())
		}
	}
	for _, name := range []string{"evil-ffff6666", "mani-aaaa7777", "pipe-bbbb8888"} {
		if _, err := os.Stat(f.dest(name)); !os.IsNotExist(err) {
			t.Errorf("%s moved although it is blocked", name)
		}
	}
	// Nothing of the victim's secret appears anywhere under the shared root or in the backup.
	found := false
	for _, root := range []string{filepath.Join(f.Docs, "Crew"), f.Backup} {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Type()&os.ModeSymlink == 0 {
				if raw, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(raw), "victim secret") {
					found = true
				}
			}
			return nil
		})
	}
	if found {
		t.Fatal("the victim's file content was copied into the shared root or the backup")
	}
}

// An internal symlink (inside the Crew, relative) is copied as a symlink; a directory symlink is not expanded into a copy.
func TestCrewMoveCopiesInternalSymlinksAsSymlinks(t *testing.T) {
	f := newCrewMoveFixture(t)
	src := f.source("alice", cmCrewA)
	if err := os.Symlink("code", filepath.Join(src, "code-alias")); err != nil {
		t.Fatal(err)
	}
	mustMove(t, f, only(cmCrewA))
	info, err := os.Lstat(filepath.Join(f.dest(cmCrewA), "code-alias"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the folder symlink was not copied as a symlink: %v %v", info, err)
	}
	if target, _ := os.Readlink(filepath.Join(f.dest(cmCrewA), "code-alias")); target != "code" {
		t.Fatalf("target = %q", target)
	}
	j, _ := loadCrewMoveJournal(f.State, cmCrewA)
	if j == nil || j.Tree.Symlinks != 2 {
		t.Fatalf("journal = %+v", j)
	}
}

// A link planted INSIDE a Crew while it is being copied (after it was listed) cannot carry the copy out of the Crew.
func TestCrewMoveSurvivesALinkPlantedMidCopy(t *testing.T) {
	f := newCrewMoveFixture(t)
	victim := filepath.Join(t.TempDir(), "victim-dir")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "er", "x"), nil, 0o600); err == nil {
		t.Fatal("unexpected")
	}
	if err := os.MkdirAll(filepath.Join(victim, "er"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "er", "file.txt"), []byte("VICTIM CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	planted := false
	hook := func(o *crewMoveOptions) {
		o.Hook = func(point string) error {
			if point == "copy:1" && !planted {
				planted = true
				deep := filepath.Join(f.source("alice", cmCrewA), "code", "deep")
				if err := os.RemoveAll(deep); err != nil {
					return err
				}
				// Absolute, then also tried relative by the same hook if the first were followed.
				return os.Symlink(victim, deep)
			}
			return nil
		}
	}
	_, err := f.run(apply, only(cmCrewA), hook)
	if err == nil {
		t.Fatal("a Crew whose tree was swapped for a link mid-copy was moved")
	}
	if _, statErr := os.Stat(f.dest(cmCrewA)); !os.IsNotExist(statErr) {
		t.Fatal("a partial Crew appeared at the shared root")
	}
	// Nothing of the victim's directory was copied anywhere under the shared root.
	_ = filepath.WalkDir(filepath.Join(f.Docs, "Crew"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Type()&os.ModeSymlink == 0 {
			if raw, _ := os.ReadFile(p); strings.Contains(string(raw), "VICTIM CONTENT") {
				t.Errorf("the victim's content was copied to %s", p)
			}
		}
		return nil
	})
	if raw, _ := os.ReadFile(filepath.Join(victim, "er", "file.txt")); string(raw) != "VICTIM CONTENT" {
		t.Fatal("the victim's file was changed")
	}
	// The symlink now in the Crew leaves it: the next dry run blocks the Crew instead of following it.
	report, err := f.run()
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range report.Crews {
		if plan.Folder == cmCrewA && !strings.Contains(strings.Join(plan.Blockers, " "), "leaves the Crew folder") {
			t.Fatalf("blockers = %v", plan.Blockers)
		}
	}
}
