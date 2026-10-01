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

func TestResolveUserPathDeniesAnotherUsersTree(t *testing.T) {
	docs := crossUserFixture(t)
	for _, req := range []string{
		"_users/bob/Chats/secret.txt", "/_users/bob/Chats/secret.txt",
		"Chats/../_users/bob/Chats/secret.txt", "./_users/bob/Chats/secret.txt", "_users/bob",
	} {
		if got, err := ResolveUserPath(docs, req, "alice"); err == nil {
			t.Errorf("alice resolved %q to %s", req, got)
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
