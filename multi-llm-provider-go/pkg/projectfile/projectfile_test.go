package projectfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

// The user's own file survives one session and any number of overlapping ones.
func TestUsersOwnFileSurvivesOverlappingSessions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "AGENTS.md")
	const own = "# Team rules\nUse tabs.\n"
	if err := os.WriteFile(p, []byte(own), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Acquire(p, "PROMPT-A"); err != nil {
		t.Fatal(err)
	}
	if err := Acquire(p, "PROMPT-B"); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if !strings.Contains(got, "Use tabs.") || !strings.Contains(got, "PROMPT-B") {
		t.Fatalf("file while held: %q", got)
	}
	if strings.Count(got, "BEGIN agentworks") != 1 {
		t.Fatalf("expected one shared block: %q", got)
	}
	Release(p) // first session ends; the second is still running
	if got := read(t, p); !strings.Contains(got, "PROMPT-B") || !strings.Contains(got, "Use tabs.") {
		t.Fatalf("second session lost its instructions: %q", got)
	}
	Release(p)
	if got := read(t, p); got != own {
		t.Fatalf("user's file changed: %q", got)
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode changed: %v", info.Mode())
	}
}

// A file this package created is removed by the last session, not the first.
func TestCreatedFileRemovedByLastSession(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "AGENTS.md")
	Acquire(p, "A")
	Acquire(p, "B")
	Release(p)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("removed while a session is running: %v", err)
	}
	Release(p)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("file left behind: %v", err)
	}
}

// Edits the user makes while a session runs are kept.
func TestEditsDuringSessionAreKept(t *testing.T) {
	p := filepath.Join(t.TempDir(), "AGENTS.md")
	os.WriteFile(p, []byte("one\n"), 0o644)
	Acquire(p, "P")
	os.WriteFile(p, []byte(read(t, p)+"\nadded by user\n"), 0o644)
	Release(p)
	if got := read(t, p); !strings.Contains(got, "one") || !strings.Contains(got, "added by user") || strings.Contains(got, "BEGIN agentworks") {
		t.Fatalf("got %q", got)
	}
}

// A block left by a crashed process is cleaned up without touching user text,
// and a stale file the block created is removed.
func TestStripStale(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "AGENTS.md")
	os.WriteFile(p, []byte("mine\n"), 0o644)
	Acquire(p, "P")
	holders = map[string]*held{} // simulate a restart: the registry is gone
	StripStale(p)
	if got := read(t, p); got != "mine\n" {
		t.Fatalf("got %q", got)
	}
	q := filepath.Join(d, "new.md")
	Acquire(q, "P")
	holders = map[string]*held{}
	StripStale(q)
	if _, err := os.Stat(q); !os.IsNotExist(err) {
		t.Fatalf("stale created file kept")
	}
	// A live session's block is never stripped.
	Acquire(p, "P")
	StripStale(p)
	if !strings.Contains(read(t, p), "BEGIN agentworks") {
		t.Fatalf("live block stripped")
	}
	Release(p)
}

// Cleanup that runs twice must not release another session's hold.
func TestLeaseReleasesOnce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "AGENTS.md")
	a, _ := AcquireLease(p, "A")
	b, _ := AcquireLease(p, "B")
	ReleaseToken(a)
	ReleaseToken(a)
	if !strings.Contains(read(t, p), "BEGIN agentworks") {
		t.Fatalf("second release dropped the other session's block")
	}
	ReleaseToken(b)
	if got := read(t, p); got != "<missing>" {
		t.Fatalf("left behind: %q", got)
	}
}

func TestOwnedLeaseCountsSessionsAndRestoresPrior(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, ".cursor", "rules", "mlp-system.mdc")
	a, _ := AcquireOwnedLease(p, []byte("A"))
	b, _ := AcquireOwnedLease(p, []byte("B"))
	ReleaseToken(a)
	ReleaseToken(a)
	if got := read(t, p); got != "B" {
		t.Fatalf("running session lost its rules: %q", got)
	}
	ReleaseToken(b)
	if got := read(t, p); got != "<missing>" {
		t.Fatalf("left behind: %q", got)
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("user"), 0o644)
	c, _ := AcquireOwnedLease(p, []byte("C"))
	ReleaseToken(c)
	if got := read(t, p); got != "user" {
		t.Fatalf("prior not restored: %q", got)
	}
}

func TestLimitedLeaseRejectsWithoutChangingLiveFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "AGENTS.md")
	token, err := AcquireLease(p, "active prompt")
	if err != nil {
		t.Fatal(err)
	}
	before := read(t, p)
	// Unicode must be counted as UTF-8 bytes, not characters. Include the
	// managed markers in the budget and keep an existing lease untouched.
	if rejected, err := AcquireLeaseLimit(p, strings.Repeat("界", 100), len(before)); err == nil || rejected != "" {
		t.Fatalf("oversized lease accepted: token=%q err=%v", rejected, err)
	}
	if got := read(t, p); got != before || !Held(p) {
		t.Fatalf("rejection changed live instructions: %q", got)
	}
	ReleaseToken(token)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("failed lease leaked a holder: %v", err)
	}
}

func TestLimitedLeaseCountsOperatorContentAndExactBoundary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "AGENTS.md")
	const operator = "# Operator rules\nKeep these.\n"
	if err := os.WriteFile(p, []byte(operator), 0o640); err != nil {
		t.Fatal(err)
	}
	token, err := AcquireLease(p, "prompt")
	if err != nil {
		t.Fatal(err)
	}
	limit := len(read(t, p))
	ReleaseToken(token)
	if token, err = AcquireLeaseLimit(p, "prompt", limit-1); err == nil {
		ReleaseToken(token)
		t.Fatal("accepted file exceeding limit by one byte")
	}
	if got := read(t, p); got != operator || Held(p) {
		t.Fatalf("rejected projection changed operator file: %q", got)
	}
	token, err = AcquireLeaseLimit(p, "prompt", limit)
	if err != nil {
		t.Fatal(err)
	}
	ReleaseToken(token)
	if got := read(t, p); got != operator {
		t.Fatalf("operator content not restored: %q", got)
	}
	if info, err := os.Stat(p); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("operator mode changed: %v, %v", info, err)
	}
}
