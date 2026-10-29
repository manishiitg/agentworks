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
	"strconv"
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

	// PLAT-746 repository-aware capture: a checkout lists its own files, so an
	// ignored dependency tree is neither walked nor counted. A file the agent
	// commits mid-reply still shows as added, a tracked edit as modified and a
	// deleted file as deleted (the saved before/after trees are compared).
	repo := Store{Root: t.TempDir(), Private: t.TempDir()}
	app := filepath.Join(repo.Root, "app")
	gitIn := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Dir = app
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	put := func(rel, data string) {
		t.Helper()
		p := filepath.Join(app, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	put(".gitignore", "node_modules/\n")
	put("tracked.txt", "before\n")
	put("gone.txt", "bye\n")
	for i := 0; i < 30; i++ {
		put(fmt.Sprintf("node_modules/pkg/%02d.js", i), "x")
	}
	gitIn("init", "--quiet")
	gitIn("add", ".gitignore", "tracked.txt", "gone.txt")
	gitIn("commit", "--quiet", "-m", "init")
	first, err := repo.Begin(t.Context(), "repo-turn")
	if err != nil {
		t.Fatal(err)
	}
	if c := first.BeforeCapture; c.Eligible != 3 || c.Repos != 1 || c.WalkedFiles != 0 {
		t.Fatalf("ignored tree was walked or counted: %+v", c)
	}
	put("tracked.txt", "after\n")
	put("new.txt", "new\n")
	gitIn("add", "new.txt")
	gitIn("commit", "--quiet", "-m", "agent commit")
	if err := os.Remove(filepath.Join(app, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	done, err := repo.Finish(t.Context(), "repo-turn")
	if err != nil {
		t.Fatal(err)
	}
	cmp, err := repo.Compare(t.Context(), done.ID, "", func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range cmp.Changes {
		got[c.Path] = c.Status
	}
	if len(got) != 3 || got["app/tracked.txt"] != "M" || got["app/new.txt"] != "A" || got["app/gone.txt"] != "D" {
		t.Fatalf("repo changes: %+v", got)
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

// PLAT-746-163: a cold capture on the send path held every first message after
// a restart for 15 s and the message was then marked not delivered. A Crew with
// no private index is never read on the send path: the reply is unavailable at
// once, a background Warm builds the index, and the next send captures within
// budget reusing it. While the build runs, a send skips instead of waiting.
// Readers take no gate.
func TestColdBeforeCaptureBudgetAndLockFreeReads(t *testing.T) {
	s := Store{Root: t.TempDir(), Private: t.TempDir()}
	if err := os.WriteFile(filepath.Join(s.Root, "a.txt"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	backdate(t, filepath.Join(s.Root, "a.txt"))
	started := time.Now()
	cold, err := s.BeginWithin(time.Minute, "cold")
	if err != nil || cold.Status != "unavailable" || cold.BeforeCapture.Deferred != "cold" || cold.BeforeCapture.Captured != 0 || time.Since(started) > 5*time.Second {
		t.Fatalf("cold send-path capture was attempted: %v %+v", err, cold)
	}
	s.state().warming.Store(true)
	if _, err := s.BeginWithin(time.Minute, "during-warmup"); !errors.Is(err, ErrWarming) {
		t.Fatalf("send waited for a running warm-up: %v", err)
	}
	s.state().warming.Store(false)
	// The deferred reply's Finish neither waits on a held gate nor captures.
	held, err := s.lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if r, err := s.Finish(t.Context(), "cold"); err != nil || r.Status != "unavailable" || r.After != "" || r.AfterCapture.Captured != 0 {
		t.Fatalf("finish of a deferred reply captured: %v %+v", err, r)
	}
	held()
	if stats, err := s.Warm(time.Minute); err != nil || stats.Captured != 1 {
		t.Fatalf("warm-up failed: %v %+v", err, stats)
	}
	warm, err := s.BeginWithin(time.Minute, "warm")
	if err != nil || warm.Status != "capturing" || warm.BeforeCapture.Cold || warm.BeforeCapture.Reused != 1 {
		t.Fatalf("send after warm-up did not reuse the cache: %v %+v", err, warm)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "a.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	done, err := s.Finish(t.Context(), "warm")
	if err != nil || done.Status != "complete" {
		t.Fatalf("warm reply after-capture failed: %v %+v", err, done)
	}
	if changed, err := s.Compare(t.Context(), done.ID, "", func(string) bool { return true }); err != nil || len(changed.Changes) != 1 {
		t.Fatalf("warm reply does not show its edit: %v %+v", err, changed)
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

// The before-capture runs beside the send. One that finishes after the turn
// has started (here: held behind the Crew gate) is still the reply's
// baseline (PLAT-746-163, owner decision 2026-10-29).
func TestBeforeCaptureFinishingDuringTheTurnIsTheBaseline(t *testing.T) {
	s := Store{Root: t.TempDir(), Private: t.TempDir()}
	if err := os.WriteFile(filepath.Join(s.Root, "a.txt"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	backdate(t, filepath.Join(s.Root, "a.txt"))
	if _, err := s.Warm(time.Minute); err != nil {
		t.Fatal(err)
	}
	held, err := s.lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		r   Record
		err error
	}
	begun := make(chan result, 1)
	go func() {
		r, err := s.BeginWithin(time.Minute, "slow-before")
		begun <- result{r, err}
	}()
	time.Sleep(200 * time.Millisecond) // the turn is running; the capture still waits
	held()
	got := <-begun
	if got.err != nil || got.r.Status != "capturing" {
		t.Fatalf("late-finishing before-capture was dropped: %v %+v", got.err, got.r)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "a.txt"), []byte("edited"), 0600); err != nil {
		t.Fatal(err)
	}
	done, err := s.Finish(t.Context(), "slow-before")
	if err != nil || done.Status != "complete" {
		t.Fatalf("finish: %v %+v", err, done)
	}
	if changed, err := s.Compare(t.Context(), done.ID, "", func(string) bool { return true }); err != nil || len(changed.Changes) != 1 {
		t.Fatalf("reply does not show its edit: %v %+v", err, changed)
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
	assets, _ := filepath.Glob(filepath.Join(s.Root, "asset-*.bin"))
	backdate(t, assets...)
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
	// Only the source edited just before Finish may be re-read (racily clean).
	warm, err := s.Begin(t.Context(), "warm")
	if err != nil {
		t.Fatal(err)
	}
	if c := warm.BeforeCapture; len(warm.Skipped) != 0 || c.ImportedBytes > int64(len("after\n")) || c.Reused < 11 || c.Reused+c.Changed != 12 {
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

// backdate moves files out of the current timestamp tick. Git correctly
// re-reads an entry written in the same tick as its index (racily clean),
// which would make exact reuse counts depend on timing.
func backdate(t *testing.T, paths ...string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	for _, p := range paths {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
}

type crewFixture struct {
	t *testing.T
	s Store
}

func newCrew(t *testing.T) crewFixture {
	base := t.TempDir()
	return crewFixture{t: t, s: Store{Root: filepath.Join(base, "files"), Private: filepath.Join(base, "private")}}
}

func (f crewFixture) put(rel, data string) string {
	f.t.Helper()
	p := filepath.Join(f.s.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f crewFixture) git(dir string, args ...string) {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = filepath.Join(f.s.Root, filepath.FromSlash(dir))
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		f.t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// repo makes dir a checkout with one commit of the given files.
func (f crewFixture) repo(dir string, files map[string]string) {
	f.t.Helper()
	for name, data := range files {
		f.put(dir+"/"+name, data)
	}
	f.git(dir, "init", "--quiet")
	f.git(dir, "add", "-A")
	f.git(dir, "commit", "--quiet", "-m", "init")
}

func (f crewFixture) backdateAll() {
	f.t.Helper()
	var paths []string
	_ = filepath.WalkDir(f.s.Root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.Contains(p, string(filepath.Separator)+".git"+string(filepath.Separator)) {
			paths = append(paths, p)
		}
		return nil
	})
	backdate(f.t, paths...)
}

// tree lists a saved capture as path -> contents.
func (f crewFixture) tree(commit string) map[string]string {
	f.t.Helper()
	out, err := f.s.command(context.Background(), "ls-tree", "-r", "-z", "--name-only", commit).Output()
	if err != nil {
		f.t.Fatal(err)
	}
	files := map[string]string{}
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			data, err := f.s.command(context.Background(), "cat-file", "blob", commit+":"+string(p)).Output()
			if err != nil {
				f.t.Fatal(err)
			}
			files[string(p)] = string(data)
		}
	}
	return files
}

func (f crewFixture) changes(id string) map[string]string {
	f.t.Helper()
	c, err := f.s.Compare(context.Background(), id, "", func(string) bool { return true })
	if err != nil {
		f.t.Fatal(err)
	}
	got := map[string]string{}
	for _, ch := range c.Changes {
		got[ch.Path] = ch.Status
	}
	return got
}

// Git-native capture: after a restart the persisted private index makes a
// no-change capture read nothing; Git's racy-clean and ctime checks still
// catch same-size edits (one with the old mtime put back, which the earlier
// size+mtime cache missed); an edit between replies belongs to the next
// reply's before; the user's own index, HEAD, refs and config are untouched.
func TestPersistentIndexReuseAfterRestartAndSameSizeEdits(t *testing.T) {
	f := newCrew(t)
	f.repo("app", map[string]string{"a.txt": "aaaa\n", "b.txt": "bbbb\n"})
	f.put("notes.md", "notes\n")
	f.backdateAll()
	userFiles := []string{"app/.git/index", "app/.git/HEAD", "app/.git/config", "app/.git/refs/heads/main"}
	type state struct {
		data []byte
		mod  time.Time
	}
	snapshot := func() map[string]state {
		m := map[string]state{}
		for _, rel := range userFiles {
			p := filepath.Join(f.s.Root, filepath.FromSlash(rel))
			data, err := os.ReadFile(p)
			info, statErr := os.Stat(p)
			if err != nil || statErr != nil {
				t.Fatalf("user Git file %s: %v %v", rel, err, statErr)
			}
			m[rel] = state{data, info.ModTime()}
		}
		return m
	}
	userBefore := snapshot()
	if _, err := f.s.Begin(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Finish(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	states.Delete(f.s.Private) // a server restart drops all in-memory state
	started := time.Now()
	warm, err := f.s.BeginWithin(5*time.Second, "after-restart")
	if c := warm.BeforeCapture; err != nil || warm.Status != "capturing" || c.Cold || c.Deferred != "" || c.Changed != 0 || c.ImportedBytes != 0 || c.Reused != 3 || c.IndexesReused != 2 || c.IndexesNew != 0 {
		t.Fatalf("restarted no-change capture did not reuse the persisted index: %v %+v", err, warm)
	}
	t.Logf("restarted no-change capture took %v", time.Since(started))
	if done, err := f.s.Finish(t.Context(), "after-restart"); err != nil || !f.s.Unchanged(t.Context(), done) {
		t.Fatalf("no-change reply: %v %+v", err, done)
	}
	a := filepath.Join(f.s.Root, "app", "a.txt")
	for i, restoreMtime := range []bool{false, true} {
		id := fmt.Sprintf("same-size-%d", i)
		if _, err := f.s.Begin(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		recorded, err := os.Stat(a)
		if err != nil {
			t.Fatal(err)
		}
		f.put("app/a.txt", fmt.Sprintf("edit%d", i)) // same size as "aaaa\n"
		if restoreMtime {
			// Same size and the exact mtime the index recorded: only ctime differs.
			if err := os.Chtimes(a, recorded.ModTime(), recorded.ModTime()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.s.Finish(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		if got := f.changes(ID(id)); len(got) != 1 || got["app/a.txt"] != "M" {
			t.Fatalf("same-size edit (mtime restored=%t) missed: %+v", restoreMtime, got)
		}
	}
	// The person edits between replies: the next reply starts from that edit.
	f.put("app/b.txt", "between\n")
	next, err := f.s.Begin(t.Context(), "next")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.tree(next.Before)["app/b.txt"]; got != "between\n" {
		t.Fatalf("between-reply edit not in the next before: %q", got)
	}
	if _, err := f.s.Finish(t.Context(), "next"); err != nil {
		t.Fatal(err)
	}
	if got := f.changes(ID("next")); len(got) != 0 {
		t.Fatalf("person's edit attributed to the next reply: %+v", got)
	}
	userAfter := snapshot()
	for _, rel := range userFiles {
		if !bytes.Equal(userBefore[rel].data, userAfter[rel].data) || !userBefore[rel].mod.Equal(userAfter[rel].mod) {
			t.Fatalf("user Git file changed: %s", rel)
		}
	}
}

// The persisted index follows the validated candidate list: deleted and newly
// ignored files leave it (the newly ignored one is not shown as deleted), a
// tracked file the repo also ignores stays captured, and a file skipped for
// budget is neither in the tree nor kept as an older blob.
func TestIndexReconcilesDeletedIgnoredTrackedIgnoredAndBudgetSkipped(t *testing.T) {
	f := newCrew(t)
	f.repo("app", map[string]string{".gitignore": "*.log\n", "tracked.txt": "tracked\n", "gone.txt": "gone\n"})
	f.put("app/keep.log", "kept\n")
	f.git("app", "add", "-f", "keep.log")
	f.git("app", "commit", "--quiet", "-m", "tracked but ignored")
	f.put("app/scratch.txt", "scratch\n")
	f.put("loose.txt", "loose\n")
	f.backdateAll()
	first, err := f.s.Begin(t.Context(), "reconcile")
	if err != nil {
		t.Fatal(err)
	}
	if tree := f.tree(first.Before); tree["app/keep.log"] != "kept\n" || tree["app/scratch.txt"] != "scratch\n" || tree["loose.txt"] != "loose\n" {
		t.Fatalf("before tree: %+v", tree)
	}
	if err := os.Remove(filepath.Join(f.s.Root, "app", "gone.txt")); err != nil {
		t.Fatal(err)
	}
	f.put("app/.gitignore", "*.log\nscratch.txt\n")
	f.put("app/keep.log", "kept and edited\n")
	done, err := f.s.Finish(t.Context(), "reconcile")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.changes(done.ID); len(got) != 3 || got["app/gone.txt"] != "D" || got["app/keep.log"] != "M" || got["app/.gitignore"] != "M" {
		t.Fatalf("reconciled changes: %+v", got)
	}
	if tree := f.tree(done.After); tree["app/scratch.txt"] != "" || tree["app/gone.txt"] != "" {
		t.Fatalf("removed paths kept in the after tree: %+v", tree)
	}
	f.put("app/tracked.txt", "tracked, edited\n")
	for round := 0; round < 2; round++ {
		stats, state := CaptureStats{}, map[string]string{}
		commit, skipped, _, err := f.s.capture(t.Context(), fmt.Sprintf("refs/turns/budget%d/before", round), "", &stats, state, 1)
		if err != nil {
			t.Fatal(err)
		}
		tree := f.tree(commit)
		if _, kept := tree["app/tracked.txt"]; kept || stats.BudgetSkipped == 0 || state["app/tracked.txt"] == "" || !strings.Contains(strings.Join(skipped, "\n"), "app/tracked.txt") {
			t.Fatalf("round %d: budget-skipped file kept an old blob or lost its skip record: %+v %+v %v", round, tree, stats, skipped)
		}
	}
	full, err := f.s.Begin(t.Context(), "full")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.tree(full.Before)["app/tracked.txt"]; got != "tracked, edited\n" {
		t.Fatalf("capture after budget skip: %q", got)
	}
}

// Each checkout has its own private index; trees are assembled under their
// Crew-relative prefixes with the loose files, so equal relative names never
// collide, a renamed checkout gets a fresh index, and a Crew root that is
// itself a checkout keeps loose files of a nested fallback checkout.
func TestCheckoutsComposeUnderPrefixesWithLooseFiles(t *testing.T) {
	f := newCrew(t)
	f.repo("one", map[string]string{"same.txt": "one\n"})
	f.repo("two", map[string]string{"same.txt": "two\n"})
	f.put("same.txt", "loose\n")
	if _, err := f.s.Begin(t.Context(), "pair"); err != nil {
		t.Fatal(err)
	}
	f.put("two/same.txt", "two, edited\n")
	done, err := f.s.Finish(t.Context(), "pair")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.changes(done.ID); len(got) != 1 || got["two/same.txt"] != "M" {
		t.Fatalf("changes across same-named files: %+v", got)
	}
	tree := f.tree(done.After)
	if len(tree) != 3 || tree["one/same.txt"] != "one\n" || tree["two/same.txt"] != "two, edited\n" || tree["same.txt"] != "loose\n" {
		t.Fatalf("composed tree: %+v", tree)
	}
	if err := os.Rename(filepath.Join(f.s.Root, "two"), filepath.Join(f.s.Root, "three")); err != nil {
		t.Fatal(err)
	}
	moved, err := f.s.Begin(t.Context(), "moved")
	if err != nil {
		t.Fatal(err)
	}
	if c := moved.BeforeCapture; c.IndexesNew != 1 || c.IndexesReused != 2 {
		t.Fatalf("renamed checkout reused an index: %+v", c)
	}
	if tree := f.tree(moved.Before); len(tree) != 3 || tree["three/same.txt"] != "two, edited\n" {
		t.Fatalf("tree after rename: %+v", tree)
	}
	if idx, _ := filepath.Glob(filepath.Join(f.s.indexDir(), "*.idx")); len(idx) != 3 {
		t.Fatalf("stale index kept for the removed checkout: %v", idx)
	}

	g := newCrew(t)
	g.repo(".", map[string]string{"main.go": "package main\n"})
	g.put("sub/x.txt", "x\n")
	g.git("sub", "init", "--quiet") // no commits: walked as loose files
	if _, err := g.s.Begin(t.Context(), "rooted"); err != nil {
		t.Fatal(err)
	}
	g.put("sub/x.txt", "x, edited\n")
	rooted, err := g.s.Finish(t.Context(), "rooted")
	if err != nil {
		t.Fatal(err)
	}
	if got := g.changes(rooted.ID); len(got) != 1 || got["sub/x.txt"] != "M" {
		t.Fatalf("root checkout with loose files: %+v", got)
	}
	if tree := g.tree(rooted.After); tree["main.go"] != "package main\n" || tree["sub/x.txt"] != "x, edited\n" {
		t.Fatalf("root checkout tree: %+v", tree)
	}
}

// Snapshots store exactly the bytes on disk whatever the work tree's
// .gitattributes ask for, inside a checkout and as loose files, and Git's
// re-hash agrees with those bytes, so the next capture reuses them.
func TestRawBytesIgnoreEOLIdentEncodingAndFilterAttributes(t *testing.T) {
	f := newCrew(t)
	attributes := "* text=auto eol=crlf\n*.txt ident\nu16.txt working-tree-encoding=UTF-16LE\n*.bin filter=lfs diff=lfs merge=lfs -text\n"
	files := map[string]string{
		"crlf.txt": "one\r\ntwo\r\n",
		"lf.txt":   "one\ntwo\n",
		"id.txt":   "$Id: 0123456789abcdef $\n",
		"u16.txt":  "h\x00i\x00\n\x00",
		"lfs.bin":  "version https://git-lfs.github.com/spec/v1\noid sha256:4d7a214614ab2935c943f9e0ff69d22eadbb8f32b1258daaa5e2ca24d17e2393\nsize 12345\n",
	}
	f.repo("app", map[string]string{".gitattributes": attributes})
	f.put(".gitattributes", attributes)
	for name, data := range files {
		f.put("app/"+name, data)
		f.put(name, data)
	}
	f.backdateAll()
	first, err := f.s.Begin(t.Context(), "raw")
	if err != nil {
		t.Fatal(err)
	}
	tree := f.tree(first.Before)
	for name, data := range files {
		for _, p := range []string{name, "app/" + name} {
			if tree[p] != data {
				t.Errorf("%s stored %q, disk has %q", p, tree[p], data)
			}
		}
	}
	if _, err := f.s.Finish(t.Context(), "raw"); err != nil {
		t.Fatal(err)
	}
	again, err := f.s.Begin(t.Context(), "raw-again")
	if c := again.BeforeCapture; err != nil || c.Changed != 0 || c.Reused != c.Eligible {
		t.Fatalf("Git's re-hash disagreed with the raw bytes: %v %+v", err, c)
	}
}

// The background initial import commits each batch: cancelled after one
// batch (and with a killed run's temporary files and locks left behind), the
// next warm-up keeps that batch and finishes the rest.
func TestInitialImportResumesAfterCancel(t *testing.T) {
	f := newCrew(t)
	f.repo("app", map[string]string{"seed.txt": strings.Repeat("s", 99) + "\n"})
	for i := 0; i < 9; i++ {
		f.put(fmt.Sprintf("app/f%d.txt", i), strings.Repeat("a", 99)+"\n")
		f.put(fmt.Sprintf("loose%d.txt", i), strings.Repeat("b", 99)+"\n")
	}
	f.backdateAll()
	ctx, cancel := context.WithCancel(t.Context())
	var firstBatch CaptureStats
	_, err := f.s.warm(ctx, 500, func(st CaptureStats) { firstBatch = st; cancel() })
	if !errors.Is(err, context.Canceled) || firstBatch.Changed != 5 || firstBatch.BudgetSkipped != 14 {
		t.Fatalf("first batch: %v %+v", err, firstBatch)
	}
	// What a killed batch leaves: a temporary index, a pack, a ref lock.
	for _, p := range []string{filepath.Join(f.s.indexDir(), "x.idx.tmp"), filepath.Join(f.s.gitDir(), "objects", "pack", "tmp_pack_x"), filepath.Join(f.s.gitDir(), "refs", "snapshot-index", "head.lock")} {
		if err := os.WriteFile(p, []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var resumed []CaptureStats
	total, err := f.s.warm(t.Context(), 500, func(st CaptureStats) { resumed = append(resumed, st) })
	if err != nil || len(resumed) != 3 || resumed[0].Reused != 5 || total.Batches != 3 || total.BudgetSkipped != 0 || total.Captured != 19 {
		t.Fatalf("resumed import: %v %+v %+v", err, total, resumed)
	}
	if left, _ := filepath.Glob(filepath.Join(f.s.indexDir(), "*.tmp")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
	r, err := f.s.BeginWithin(5*time.Second, "after-import")
	if c := r.BeforeCapture; err != nil || r.Status != "capturing" || c.Cold || c.Changed != 0 || c.Reused != 19 {
		t.Fatalf("send after import: %v %+v", err, r)
	}
}

// indexStat returns the mtime seconds and size the persisted private index
// records for a loose file.
func (f crewFixture) indexStat(rel string) (mtime, size string) {
	f.t.Helper()
	cmd := exec.Command("git", "--git-dir="+f.s.gitDir(), "--work-tree="+f.s.Root, "ls-files", "--debug", "--", rel)
	cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+f.s.indexFile("loose"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.Output()
	if err != nil {
		f.t.Fatal(err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "mtime: "); ok {
			mtime, _, _ = strings.Cut(v, ":")
		}
		if v, ok := strings.CutPrefix(line, "size: "); ok {
			size, _, _ = strings.Cut(v, "\t")
		}
	}
	return mtime, size
}

// Review of the git-native capture (P2): Git found a touched file's bytes
// unchanged but the new stat was never saved, so every later capture re-hashed
// it. A successful capture now persists refreshed stat data: a touched,
// byte-identical file and a fresh, racily clean file stop being re-checked,
// also after a restart, and nothing is re-imported.
func TestRefreshedStatPersistsForUnchangedContent(t *testing.T) {
	f := newCrew(t)
	touched := f.put("touched.txt", "same bytes\n")
	f.backdateAll()
	if _, err := f.s.Begin(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	newer := time.Now().Add(-30 * time.Minute).Truncate(time.Second)
	if err := os.Chtimes(touched, newer, newer); err != nil {
		t.Fatal(err)
	}
	// Phase 1: only the touch; nothing is added, removed or edited.
	r, err := f.s.Begin(t.Context(), "touched")
	if c := r.BeforeCapture; err != nil || c.Changed != 0 || c.ImportedBytes != 0 || c.Removed != 0 {
		t.Fatalf("touched file was re-imported: %v %+v", err, c)
	}
	if mtime, _ := f.indexStat("touched.txt"); mtime != strconv.FormatInt(newer.Unix(), 10) {
		t.Fatalf("refreshed stat not persisted: index mtime %s, file %d", mtime, newer.Unix())
	}
	// Phase 2: a fresh file, possibly racily clean in the capture that adds it.
	f.put("fresh.txt", "fresh\n")
	if r, err := f.s.Begin(t.Context(), "fresh"); err != nil || r.BeforeCapture.Changed != 1 {
		t.Fatalf("fresh file capture: %v %+v", err, r.BeforeCapture)
	}
	time.Sleep(1100 * time.Millisecond) // leave fresh.txt's timestamp tick
	if _, err := f.s.Begin(t.Context(), "settle"); err != nil {
		t.Fatal(err)
	}
	if _, size := f.indexStat("fresh.txt"); size != strconv.Itoa(len("fresh\n")) {
		t.Fatalf("racily clean entry never got its stat: size %s", size)
	}
	states.Delete(f.s.Private) // restart
	again, err := f.s.BeginWithin(5*time.Second, "after-restart")
	if c := again.BeforeCapture; err != nil || c.Changed != 0 || c.Reused != 2 || c.ImportedBytes != 0 {
		t.Fatalf("after restart: %v %+v", err, c)
	}
}

// PLAT-746-165: a linked worktree the agent added during a reply showed every
// checked-out file as added (3,081 files). Its baseline is the commit it was
// added at: only the reply's own edits show, including one it committed, the
// chat's combined changes agree, Undo restores just those edits (and still
// removes files of a checkout the agent made from scratch with git init),
// and removing the worktree later reports no deletions.
func TestNewCheckoutComparesAgainstItsCheckedOutCommit(t *testing.T) {
	f := newCrew(t)
	ctx := t.Context()
	all := func(string) bool { return true }
	t.Setenv("GIT_COMMITTER_DATE", "@1700000000 +0000")
	f.repo("main", map[string]string{"a.txt": "a\n", "b.txt": "b\n", "c.txt": "c\n", "d.txt": "d\n"})
	os.Unsetenv("GIT_COMMITTER_DATE")
	f.put("notes.md", "notes\n")
	// Uncommitted, so the committed d.txt is only in the checkout's own objects.
	f.put("main/d.txt", "d local\n")
	f.backdateAll()
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(f.s.Root, rel))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	reply := func(name string, work func()) string {
		t.Helper()
		if _, err := f.s.Begin(ctx, name); err != nil {
			t.Fatal(err)
		}
		work()
		if r, err := f.s.Finish(ctx, name); err != nil || r.Status != "complete" {
			t.Fatalf("finish %s: %v %+v", name, err, r)
		}
		return ID(name)
	}
	first := reply("worktree", func() {
		f.git("main", "worktree", "add", "--quiet", "-b", "feature", "../wt")
		f.put("wt/a.txt", "a edited\n")
		os.Remove(filepath.Join(f.s.Root, "wt", "b.txt"))
		os.Remove(filepath.Join(f.s.Root, "wt", "d.txt"))
		f.put("wt/new.txt", "new\n")
		f.put("wt/c.txt", "c committed\n")
		f.git("wt", "commit", "--quiet", "-m", "turn", "--", "c.txt")
		// A worktree added at a commit made earlier in this turn compares
		// against that commit (its reflog), not the last one before the turn.
		f.git("main", "worktree", "add", "--quiet", "-b", "side", "../wt2")
		f.put("wt2/e.txt", "e\n")
		f.git("wt2", "add", "e.txt")
		f.git("wt2", "commit", "--quiet", "-m", "e")
		f.git("wt2", "worktree", "add", "--quiet", "-b", "later", "../wt3")
		f.put("wt3/a.txt", "a in wt3\n")
		f.git("wt3", "commit", "--quiet", "-am", "a3")
		f.put("fresh/x.txt", "x\n")
		f.git("fresh", "init", "--quiet")
		f.git("fresh", "add", "-A")
		f.git("fresh", "commit", "--quiet", "-m", "fresh")
	})
	want := map[string]string{"wt/a.txt": "M", "wt/b.txt": "D", "wt/c.txt": "M", "wt/d.txt": "D", "wt/new.txt": "A", "fresh/x.txt": "A", "wt2/e.txt": "A", "wt3/a.txt": "M"}
	if got := f.changes(first); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("reply changes = %v, want %v", got, want)
	}
	second := reply("more", func() { f.put("wt/more.txt", "more\n") })
	chat, err := f.s.CompareBoundaries(ctx, first, second, "before", "", all)
	if err != nil {
		t.Fatal(err)
	}
	want["wt/more.txt"] = "A"
	got := map[string]string{}
	for _, c := range chat.Changes {
		got[c.Path] = c.Status
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("chat changes = %v, want %v", got, want)
	}
	plan, err := f.s.ApplyUndo(ctx, first, all, all)
	if err != nil || len(plan.Restore) != 8 || plan.Note != "" {
		t.Fatalf("undo: %v %+v", err, plan)
	}
	for rel, want := range map[string]string{"wt/a.txt": "a\n", "wt/b.txt": "b\n", "wt/c.txt": "c\n", "wt/d.txt": "d\n", "wt/new.txt": "<missing>", "wt/more.txt": "more\n", "fresh/x.txt": "<missing>", "wt2/e.txt": "<missing>", "wt3/a.txt": "a\n", "wt3/e.txt": "e\n"} {
		if got := read(rel); got != want {
			t.Fatalf("after undo %s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(f.s.Root, "wt", ".git")); err != nil {
		t.Fatalf("undo touched the checkout: %v", err)
	}
	removed := reply("remove", func() {
		f.git("main", "worktree", "remove", "--force", "../wt")
		os.RemoveAll(filepath.Join(f.s.Root, "fresh"))
	})
	if got := f.changes(removed); len(got) != 0 {
		t.Fatalf("removing checkouts reported %v", got)
	}
}
