package filesnapshots

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A large worktree must reach ignore filtering and still compare real edits.
func TestLargeTreeReachesIgnoreFilteringAndCapturesChanges(t *testing.T) {
	s := Store{Root: t.TempDir(), Private: t.TempDir()}
	if err := os.WriteFile(filepath.Join(s.Root, ".gitignore"), []byte("generated/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(s.Root, "generated")
	if err := os.Mkdir(generated, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20001; i++ {
		if err := os.WriteFile(filepath.Join(generated, fmt.Sprintf("%05d.txt", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(s.Root, "source.txt")
	if err := os.WriteFile(file, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := s.Begin(t.Context(), "large-tree")
	if err != nil {
		t.Fatal(err)
	}
	if before.BeforeCapture.Eligible != 2 {
		t.Fatalf("ignore filtering did not precede capture: %+v", before.BeforeCapture)
	}
	if err := os.WriteFile(file, []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := s.Finish(t.Context(), "large-tree")
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := s.Compare(t.Context(), after.ID, "", func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(comparison.Changes) != 1 || comparison.Changes[0].Path != "source.txt" {
		t.Fatalf("real change missing: %+v", comparison.Changes)
	}
}

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
	write("code/reverted.txt", "original\n")
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
			write("code/reverted.txt", "temporary\n")
		}
		if turn == 10 {
			write("code/reverted.txt", "original\n")
		}
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
	// PLAT-746-158: ten turns form one net comparison, not concatenated patches.
	net, err := s.CompareBetween(ctx, ID("1"), ID("10"), "code/main.py", func(string) bool { return true })
	if err != nil || !strings.Contains(net.Diff, "-print(0)") || !strings.Contains(net.Diff, "+print(10)") {
		t.Fatalf("net comparison: %v %+v", err, net)
	}
	for _, change := range net.Changes {
		if change.Path == "code/reverted.txt" {
			t.Fatal("reverted file appears as a net change")
		}
	}
	if _, err := s.CompareBetween(ctx, ID("missing"), ID("10"), "", func(string) bool { return true }); err == nil {
		t.Fatal("missing baseline became an empty tree")
	}
	if _, err := s.CompareBetween(ctx, ID("1"), ID("10"), "code/main.py", func(string) bool { return false }); err == nil {
		t.Fatal("net comparison exposed a revoked path")
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
	// PLAT-746: review history survives normal reply retention without changing
	// the user's repository. Unreviewed old snapshots still expire.
	if err := s.KeepReviewBoundaries(ctx, ID("1"), ID("5")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"1", "2", "5"} {
		r, err := s.read(ID(id))
		if err != nil {
			t.Fatal(err)
		}
		r.StartedAt = time.Now().Add(-15 * 24 * time.Hour)
		if err := s.save(r); err != nil {
			t.Fatal(err)
		}
	}
	s.prune(ctx)
	if _, err := s.read(ID("2")); !os.IsNotExist(err) {
		t.Fatalf("ordinary old snapshot retained: %v", err)
	}
	reopened := Store{Root: s.Root, Private: s.Private}
	reviewed, err := reopened.CompareBoundaries(ctx, ID("1"), ID("5"), "after", "code/main.py", func(string) bool { return true })
	if err != nil || !strings.Contains(reviewed.Diff, "-print(1)") || !strings.Contains(reviewed.Diff, "+print(5)") {
		t.Fatalf("retained review interval: %v %+v", err, reviewed)
	}
	if _, err := reopened.CompareBetween(ctx, ID("1"), ID("1"), "code/main.py", func(string) bool { return true }); err != nil {
		t.Fatalf("first before-boundary expired: %v", err)
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

// PLAT-746-163: after a restart no file hashes are cached, so the before-capture
// gets the cold budget (not the warm one); a reader never waits for the gate.
func TestColdBeforeCaptureBudgetAndLockFreeReads(t *testing.T) {
	s := Store{Root: t.TempDir(), Private: t.TempDir()}
	if err := os.WriteFile(filepath.Join(s.Root, "a.txt"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	if captureBudget(true, time.Nanosecond, time.Minute) != time.Minute || captureBudget(false, time.Nanosecond, time.Minute) != time.Nanosecond {
		t.Fatal("budget must follow cache state")
	}
	// Cold: the warm budget (already expired) is not applied.
	first, err := s.BeginWithin(time.Second, time.Nanosecond, time.Minute, "cold")
	if err != nil || !first.BeforeCapture.Cold {
		t.Fatalf("cold capture used the warm budget: %v %+v", err, first)
	}
	done, err := s.Finish(t.Context(), "cold")
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := s.lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := s.Compare(ctx, done.ID, "", func(string) bool { return true }); err != nil {
		t.Fatalf("a read waited for the Crew gate: %v", err)
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
	if err != nil || r.Status != "partial" || len(c.Changes) != 0 || len(c.Record.Skipped) != 1 || c.UncapturedChanges != 1 {
		t.Fatalf("false deletion: %v %+v", err, c)
	}
	// Large-Crew responses keep bounded examples; the count and examples
	// must obey the same current file grant as historical contents.
	for i := 0; i < 60; i++ {
		f, err := os.Create(filepath.Join(s.Root, fmt.Sprintf("large-%02d.bin", i)))
		if err != nil {
			t.Fatal(err)
		}
		err = f.Truncate(MaxFileBytes + 1)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Begin(t.Context(), "many-skipped"); err != nil {
		t.Fatal(err)
	}
	many, err := s.Finish(t.Context(), "many-skipped")
	if err != nil {
		t.Fatal(err)
	}
	limited, err := s.Compare(t.Context(), many.ID, "", func(string) bool { return true })
	if err != nil || limited.SkippedCount != 61 || len(limited.Record.Skipped) != 50 || len(many.Skipped) != 122 || limited.UncapturedChanges != 0 {
		t.Fatalf("unbounded or lost omissions: %v %+v", err, limited)
	}
	granted, err := s.Compare(t.Context(), many.ID, "", func(p string) bool { return p == "asset.bin" })
	if err != nil || granted.SkippedCount != 1 || len(granted.Record.Skipped) != 1 || granted.Record.Skipped[0] != "asset.bin" {
		t.Fatalf("skipped response exposed denied paths/count: %v %+v", err, granted)
	}
	// PLAT-746-162: unchanged omitted assets are still outside the capture,
	// but do not warn on every normal edit. A changed omitted file must warn.
	if _, err := s.Begin(t.Context(), "changed-skipped"); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(p, MaxFileBytes+2); err != nil {
		t.Fatal(err)
	}
	changed, err := s.Finish(t.Context(), "changed-skipped")
	if err != nil {
		t.Fatal(err)
	}
	warning, err := s.Compare(t.Context(), changed.ID, "", func(string) bool { return true })
	if err != nil || warning.UncapturedChanges != 1 || warning.SkippedCount != 61 {
		t.Fatalf("changed skipped file lost warning: %v %+v", err, warning)
	}
	// PLAT-746-163: a reply whose only edit is an uncaptured file still gets a card.
	if s.Unchanged(t.Context(), changed) || !s.Unchanged(t.Context(), many) {
		t.Fatal("Unchanged must follow edits to uncaptured files")
	}
	hidden, err := s.Compare(t.Context(), changed.ID, "", func(path string) bool { return path != "asset.bin" })
	if err != nil || hidden.UncapturedChanges != 0 {
		t.Fatalf("warning count exposed denied path: %v %+v", err, hidden)
	}
	net, err := s.CompareBetween(t.Context(), many.ID, changed.ID, "", func(string) bool { return true })
	if err != nil || net.UncapturedChanges != 1 {
		t.Fatalf("chat boundary warning: %v %+v", err, net)
	}
	// One unreadable file must not lose other files or become a false deletion.
	// The remote verifier runs unprivileged; root can still read mode 000.
	unreadable := filepath.Join(s.Root, "unreadable.txt")
	if err := os.WriteFile(unreadable, []byte("prior bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Begin(t.Context(), "unreadable"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(unreadable, 0600)
	f, readErr := os.Open(unreadable)
	if f != nil {
		f.Close()
	}
	if errors.Is(readErr, fs.ErrPermission) {
		record, err := s.Finish(t.Context(), "unreadable")
		if err != nil || record.Status != "partial" || record.AfterCapture.ReadSkipped != 1 {
			t.Fatalf("one read error lost the whole capture: %v %+v", err, record)
		}
		comparison, err := s.Compare(t.Context(), record.ID, "", func(string) bool { return true })
		if err != nil || len(comparison.Changes) != 0 {
			t.Fatalf("unreadable file became a deletion: %v %+v", err, comparison)
		}
	}
	if _, err := s.Begin(t.Context(), "undelivered"); err != nil {
		t.Fatal(err)
	}
	s.Abandon("undelivered")
	if _, err := s.Compare(t.Context(), ID("undelivered"), "", func(string) bool { return true }); err == nil {
		t.Fatal("undelivered turn has a comparison")
	}
}

// PLAT-746-158: large assets exhausted the aggregate budget before later
// source paths, and unchanged stored blobs kept exhausting it on every turn.
func TestLargeAssetsDoNotStarveSourceOrChargeCachedBlobs(t *testing.T) {
	s := Store{Root: t.TempDir(), Private: t.TempDir()}
	for i := 0; i < 11; i++ {
		p := filepath.Join(s.Root, fmt.Sprintf("asset-%02d.bin", i))
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		err = f.Truncate(MaxFileBytes)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(s.Root, "z-source.ts")
	if err := os.WriteFile(source, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := s.Begin(t.Context(), "first")
	if err != nil {
		t.Fatal(err)
	}
	if before.BeforeCapture.BudgetSkipped == 0 {
		t.Fatal("fixture did not exceed capture budget")
	}
	if err := os.WriteFile(source, []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := s.Finish(t.Context(), "first")
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := s.Compare(t.Context(), after.ID, "z-source.ts", func(string) bool { return true })
	if err != nil || !strings.Contains(comparison.Diff, "-before\n+after") {
		t.Fatalf("source starved behind assets: %v %+v", err, comparison)
	}
	// The after capture filled previously skipped assets. A warm before must
	// represent all files even though the tree is larger than the import budget.
	warm, err := s.Begin(t.Context(), "warm")
	if err != nil {
		t.Fatal(err)
	}
	if len(warm.Skipped) != 0 || warm.BeforeCapture.ImportedBytes != 0 || warm.BeforeCapture.Reused != 12 {
		t.Fatalf("cached blobs charged or dropped: %+v", warm)
	}
	if _, err := s.Finish(t.Context(), "warm"); err != nil {
		t.Fatal(err)
	}
	later, err := s.Compare(t.Context(), after.ID, "z-source.ts", func(string) bool { return true })
	if err != nil || later.Diff != comparison.Diff {
		t.Fatalf("saved comparison changed: %v", err)
	}
}

// PLAT-746-158: undoing a reply restores only what the reply left untouched;
// a file changed again later is reported, never overwritten.
func TestUndoRestoresReplyChangesAndSkipsLaterEdits(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	s := Store{Root: filepath.Join(base, "files"), Private: filepath.Join(base, "private")}
	write := func(name, data string) {
		t.Helper()
		p := filepath.Join(s.Root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(s.Root, name))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	write("kept.txt", "kept\n")
	write("edited.txt", "edited before\n")
	write("later.txt", "later before\n")
	write("removed.txt", "removed before\n")
	if _, err := s.Begin(ctx, "reply"); err != nil {
		t.Fatal(err)
	}
	write("edited.txt", "edited after\n")
	write("later.txt", "later after\n")
	write("new/added.txt", "added\n")
	write("new/added-later.txt", "added\n")
	if err := os.Remove(filepath.Join(s.Root, "removed.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Finish(ctx, "reply"); err != nil {
		t.Fatal(err)
	}
	// A later reply touches two files this reply also changed.
	write("later.txt", "later by someone else\n")
	write("new/added-later.txt", "edited after the reply\n")
	all := func(string) bool { return true }
	id := ID("reply")
	plan, err := s.PlanUndo(ctx, id, all, all)
	if err != nil || len(plan.Restore) != 3 || len(plan.Skipped) != 2 || read("edited.txt") != "edited after\n" {
		t.Fatalf("preview must list without writing: %v %+v", err, plan)
	}
	for _, item := range plan.Skipped {
		if item.Reason != "changed_later" {
			t.Fatalf("conflict reason: %+v", item)
		}
	}
	plan, err = s.ApplyUndo(ctx, id, all, all)
	if err != nil || len(plan.Restore) != 3 {
		t.Fatalf("apply: %v %+v", err, plan)
	}
	for name, want := range map[string]string{"edited.txt": "edited before\n", "removed.txt": "removed before\n", "later.txt": "later by someone else\n", "new/added-later.txt": "edited after the reply\n", "new/added.txt": "<missing>", "kept.txt": "kept\n"} {
		if got := read(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	// Running it again finds everything restored or left alone.
	if plan, err = s.PlanUndo(ctx, id, all, all); err != nil || len(plan.Restore) != 0 {
		t.Fatalf("second preview: %v %+v", err, plan)
	}
	// A path the caller may not write is reported, not changed.
	write("edited.txt", "edited after\n")
	if plan, err = s.PlanUndo(ctx, id, all, func(p string) bool { return p != "edited.txt" }); err != nil || len(plan.Restore) != 0 || plan.Skipped[0].Reason == "" {
		t.Fatalf("write guard: %v %+v", err, plan)
	}
}
