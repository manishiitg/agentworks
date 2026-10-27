package filesnapshots

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-746-158: nested repositories must be file blobs, and later turns must
// not change a historical comparison or the user's Git index/branches.
func TestNestedRepositoriesKeepHistoricalComparisons(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	s := Store{Root: filepath.Join(base, "files"), Private: filepath.Join(base, "private")}
	write := func(path, data string) {
		t.Helper()
		p := filepath.Join(s.Root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("code/main.py", "print(0)\n")
	write("image.bin", "before\x00bytes")
	write("code/.gitignore", "ignored.txt\n")
	write("code/ignored.txt", "ignored bytes")
	write("code/.env", "credential bytes")
	write("code/[x].txt", "literal before\n")
	write("code/x.txt", "other before\n")
	write("code/becomes-ignored.txt", "not a deletion\n")
	cmd := exec.Command("git", "init", "--quiet", filepath.Join(s.Root, "code"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	head, err := os.ReadFile(filepath.Join(s.Root, "code/.git/HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside private bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.Root, "link")); err != nil {
		t.Fatal(err)
	}
	var first Comparison
	for turn := 1; turn <= 10; turn++ {
		id := fmt.Sprint(turn)
		if _, err := s.Begin(ctx, id); err != nil {
			t.Fatal(err)
		}
		write("code/main.py", fmt.Sprintf("print(%d)\n", turn))
		if turn == 1 {
			write("image.bin", "after\x00bytes")
			write("code/[x].txt", "literal after\n")
			write("code/x.txt", "other after\n")
			write("code/.gitignore", "ignored.txt\nbecomes-ignored.txt\n")
		}
		r, err := s.Finish(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != "complete" {
			t.Fatalf("capture: %+v", r)
		}
		if turn == 1 {
			first, err = s.Compare(ctx, r.ID, "code/main.py", func(string) bool { return true })
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	later, err := s.Compare(ctx, ID("1"), "code/main.py", func(string) bool { return true })
	if err != nil || first.Diff != later.Diff || !strings.Contains(later.Diff, "+print(1)") || strings.Contains(later.Diff, "print(10)") {
		t.Fatalf("historical diff changed: %v %+v", err, later)
	}
	for _, change := range later.Changes {
		if strings.Contains(change.Path, "ignored") || strings.Contains(change.Path, ".env") {
			t.Fatalf("excluded file exposed as a change: %+v", change)
		}
	}
	// A granted filename containing a Git wildcard must not expose another file.
	literal, err := s.Compare(ctx, ID("1"), "code/[x].txt", func(p string) bool { return p == "code/[x].txt" })
	if err != nil || !strings.Contains(literal.Diff, "literal after") || strings.Contains(literal.Diff, "other after") {
		t.Fatalf("literal path access: %v %+v", err, literal)
	}
	if _, err := s.Compare(ctx, ID("1"), "code/x.txt", func(p string) bool { return p == "code/[x].txt" }); err == nil {
		t.Fatal("denied file diff was exposed")
	}
	// B2: the saved bytes and text counts remain historical, binary files
	// have no invented line counts, and revoking a path blocks every side.
	for _, change := range later.Changes {
		if change.Path == "code/main.py" && (change.Added == nil || *change.Added != 1 || change.Removed == nil || *change.Removed != 1) {
			t.Fatalf("text counts: %+v", change)
		}
		if change.Path == "image.bin" && (!change.Binary || change.Added != nil || change.Removed != nil) {
			t.Fatalf("binary line counts: %+v", change)
		}
	}
	for side, want := range map[string]string{"before": "print(0)\n", "after": "print(1)\n"} {
		got, err := s.ReadFile(ctx, ID("1"), "code/main.py", side, func(string) bool { return true })
		if err != nil || string(got) != want {
			t.Fatalf("saved %s: %q %v", side, got, err)
		}
		if _, err := s.ReadFile(ctx, ID("1"), "code/main.py", side, func(string) bool { return false }); err == nil {
			t.Fatal("revoked file preview exposed")
		}
	}
	r, _ := s.read(ID("1"))
	tree, err := s.command(ctx, "ls-tree", "-r", r.Before).Output()
	if err != nil || bytes.Contains(tree, []byte("160000")) || !bytes.Contains(tree, []byte("code/main.py")) || bytes.Contains(tree, []byte(".env")) || bytes.Contains(tree, []byte("\tcode/ignored.txt\n")) {
		t.Fatalf("nested tree: %v %s", err, tree)
	}
	link, err := s.command(ctx, "show", r.Before+":link").Output()
	if err != nil || string(link) != outside {
		t.Fatalf("symlink followed: %v %q", err, link)
	}
	current, _ := os.ReadFile(filepath.Join(s.Root, "code/.git/HEAD"))
	if !bytes.Equal(head, current) {
		t.Fatal("user branch changed")
	}
	if _, err := os.Stat(filepath.Join(s.Root, "code/.git/index")); !os.IsNotExist(err) {
		t.Fatal("user Git index was created")
	}
}

// PLAT-746-158: an after-capture holding the Crew lock must not make the
// next send wait beyond its before-capture context budget.
func TestSnapshotLockWaitHonorsSendDeadline(t *testing.T) {
	s := Store{Root: t.TempDir(), Private: t.TempDir()}
	unlock, err := s.lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Begin(ctx, "waiting"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock ignored send deadline: %v", err)
	}
}

// Size omissions cannot masquerade as a deletion, and failed delivery cannot
// produce a fabricated after-snapshot (PLAT-746-158).
func TestSkippedFilesAndUndeliveredTurnsHaveNoFalseDiff(t *testing.T) {
	s := Store{Root: t.TempDir(), Private: t.TempDir()}
	p := filepath.Join(s.Root, "asset.bin")
	if err := os.WriteFile(p, []byte("small"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Begin(t.Context(), "one"); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(p, MaxFileBytes+1); err != nil {
		t.Fatal(err)
	}
	r, err := s.Finish(t.Context(), "one")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Compare(t.Context(), r.ID, "", func(string) bool { return true })
	if err != nil || r.Status != "partial" || len(c.Changes) != 0 || len(c.Record.Skipped) != 1 {
		t.Fatalf("false deletion: %v %+v", err, c)
	}
	if _, err := s.Begin(t.Context(), "undelivered"); err != nil {
		t.Fatal(err)
	}
	s.Abandon("undelivered")
	if _, err := s.Compare(t.Context(), ID("undelivered"), "", func(string) bool { return true }); err == nil {
		t.Fatal("undelivered turn has a comparison")
	}
}
