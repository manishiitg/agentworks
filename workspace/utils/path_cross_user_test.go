package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func crossUserFixture(t *testing.T) string {
	t.Helper()
	docs := t.TempDir()
	for _, p := range []string{"_users/alice/Chats/proj", "_users/bob/Chats/proj", "Workflow/shared"} {
		if err := os.MkdirAll(filepath.Join(docs, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for p, c := range map[string]string{
		"_users/bob/Chats/secret.txt":   "bob-secret",
		"_users/alice/Chats/mine.txt":   "alice-file",
		"_users/alice/Chats/proj/a.txt": "alice-proj",
		"Workflow/shared/readme.md":     "shared",
	} {
		if err := os.WriteFile(filepath.Join(docs, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return docs
}

// The app layer authorizes who may name another user's folder (shared Code, Crew owners, administrators),
// so the resolver does not refuse the name; the platform's own system trees must keep resolving too.
func TestResolveUserPathStillResolvesNamedUserAndSystemTrees(t *testing.T) {
	docs := crossUserFixture(t)
	if err := os.MkdirAll(filepath.Join(docs, "_users", "_system_global_secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ req, user string }{
		{"_users/bob/Chats/secret.txt", "alice"},
		{"_users/_system_global_secrets/secrets.json", "default"},
		{"_users/_system_global_secrets/secrets.json", "alice"},
		{"_users/alice/Chats/mine.txt", "alice"},
	} {
		if _, err := ResolveUserPath(docs, tc.req, tc.user); err != nil {
			t.Errorf("%s as %s: %v", tc.req, tc.user, err)
		}
	}
}

func TestResolveUserPathDeniesSymlinkIntoAnotherUser(t *testing.T) {
	docs := crossUserFixture(t)
	alice := filepath.Join(docs, "_users", "alice", "Chats")
	bob := filepath.Join(docs, "_users", "bob", "Chats")
	_ = os.Symlink(filepath.Join(bob, "secret.txt"), filepath.Join(alice, "link.txt"))
	_ = os.Symlink("../../bob/Chats/proj", filepath.Join(alice, "dirlink")) // relative, into a folder
	// and a link from a shared folder into a user's tree
	_ = os.Symlink(filepath.Join(bob, "secret.txt"), filepath.Join(docs, "Workflow", "shared", "leak.txt"))
	for _, req := range []string{"Chats/link.txt", "Chats/dirlink", "Chats/dirlink/anything.txt", "Workflow/shared/leak.txt"} {
		if got, err := ResolveUserPath(docs, req, "alice"); err == nil {
			t.Errorf("alice resolved %q to %s", req, got)
		}
	}
	// the identity-free check used by the other handlers refuses the hop as well
	if IsValidFilePath(filepath.Join(alice, "link.txt"), docs) {
		t.Error("IsValidFilePath allowed a symlink from one user's tree into another's")
	}
}

func TestResolveUserPathStillAllowsOwnAndSharedFiles(t *testing.T) {
	docs := crossUserFixture(t)
	alice := filepath.Join(docs, "_users", "alice", "Chats")
	_ = os.Symlink("proj/a.txt", filepath.Join(alice, "inner-link.txt")) // a link inside her own tree
	for _, req := range []string{
		"Chats/mine.txt", "Chats/proj/a.txt", "Chats/inner-link.txt", "_users/alice/Chats/mine.txt",
		"Chats/not-yet-created.txt", "Workflow/shared/readme.md",
	} {
		if _, err := ResolveUserPath(docs, req, "alice"); err != nil {
			t.Errorf("alice denied %q: %v", req, err)
		}
	}
	// bob still reaches his own file
	if _, err := ResolveUserPath(docs, "Chats/secret.txt", "bob"); err != nil {
		t.Errorf("bob denied his own file: %v", err)
	}
}

// A path whose leaf (or whole user folder) does not exist yet resolves no further than its nearest existing
// parent; that must not be mistaken for a hop into another tree (project-a/customer-b startup, 2026-10-01).
func TestIsValidFilePathAllowsPathsThatDoNotExistYet(t *testing.T) {
	docs := crossUserFixture(t)
	for _, p := range []string{
		"_users/_system_global_secrets/secrets.json", // the platform's own folder, created on first use
		"_users/newuser/Chats/first.txt",             // a user's very first file
		"_users/alice/Chats/not-yet.txt",             // a new file in an existing tree
		"_users/alice/Chats/newdir/deep/file.txt",
	} {
		if !IsValidFilePath(filepath.Join(docs, p), docs) {
			t.Errorf("%s refused although nothing exists to follow", p)
		}
		if _, err := ResolveUserPath(docs, p, "alice"); err != nil {
			t.Errorf("ResolveUserPath %s: %v", p, err)
		}
	}
}

func TestIsValidFilePathRefusesALinkToTheUsersFolderItself(t *testing.T) {
	docs := crossUserFixture(t)
	_ = os.Symlink("../..", filepath.Join(docs, "_users", "alice", "Chats", "everyone"))
	if IsValidFilePath(filepath.Join(docs, "_users", "alice", "Chats", "everyone"), docs) {
		t.Error("a link to the _users folder would list every account")
	}
}

// PLAT-442 step 4: a Crew at Crew/<id> is protected from symlinks the way a Crew in its owner's tree was (the
// "_users/<owner>" rule above): a link made anywhere else must not read it, and a link made in it must not
// carry a path into another Crew or the Crew root.
func TestIsValidFilePathSharedCrewTreesAreClosedToLinksFromElsewhere(t *testing.T) {
	docs := crossUserFixture(t)
	for _, p := range []string{"Crew/sde-1a2b/db", "Crew/ops-9f00/db", "Crew/.migration"} {
		if err := os.MkdirAll(filepath.Join(docs, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for p, c := range map[string]string{
		"Crew/sde-1a2b/db/db.sqlite": "sde-db",
		"Crew/sde-1a2b/notes.md":     "sde",
		"Crew/ops-9f00/notes.md":     "ops",
	} {
		if err := os.WriteFile(filepath.Join(docs, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	alice := filepath.Join(docs, "_users", "alice", "Chats")
	sde := filepath.Join(docs, "Crew", "sde-1a2b")
	mustLink := func(target, link string) {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	// From a user's tree into a Crew, file and folder, absolute and relative.
	mustLink(filepath.Join(sde, "notes.md"), filepath.Join(alice, "crewfile"))
	mustLink("../../../Crew/sde-1a2b", filepath.Join(alice, "crewdir"))
	// From a shared folder, from the Crew root, from another Crew.
	mustLink(filepath.Join(sde, "notes.md"), filepath.Join(docs, "Workflow", "shared", "leak.md"))
	mustLink(filepath.Join(docs, "Crew"), filepath.Join(alice, "allcrews"))
	mustLink(filepath.Join(sde, "notes.md"), filepath.Join(docs, "Crew", "ops-9f00", "peek.md"))
	mustLink("../.migration", filepath.Join(sde, "journal"))
	for _, p := range []string{
		"_users/alice/Chats/crewfile", "_users/alice/Chats/crewdir", "_users/alice/Chats/crewdir/db/db.sqlite",
		"Workflow/shared/leak.md", "_users/alice/Chats/allcrews", "_users/alice/Chats/allcrews/sde-1a2b/notes.md",
		"Crew/ops-9f00/peek.md", "Crew/sde-1a2b/journal",
	} {
		if IsValidFilePath(filepath.Join(docs, p), docs) {
			t.Errorf("%s: a symlink carried a path into a Crew tree it was not made in", p)
		}
		if _, err := ResolveUserPath(docs, p, "alice"); err == nil {
			t.Errorf("ResolveUserPath resolved %s", p)
		}
	}
	// The Crew's own files, its own internal links and the root itself (no link) stay reachable.
	mustLink("notes.md", filepath.Join(sde, "inner-link.md"))
	for _, p := range []string{"Crew/sde-1a2b/notes.md", "Crew/sde-1a2b/db/db.sqlite", "Crew/sde-1a2b/inner-link.md", "Crew/sde-1a2b/new/file.txt", "Crew", "Crew/new-crew/product.json"} {
		if !IsValidFilePath(filepath.Join(docs, p), docs) {
			t.Errorf("%s refused although nothing carries it across trees", p)
		}
	}
}
