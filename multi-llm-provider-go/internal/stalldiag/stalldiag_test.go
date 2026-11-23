package stalldiag

import (
	"strings"
	"testing"
	"time"
)

func TestSummaryNamesKnownPromptsAndNeverLogsPaneText(t *testing.T) {
	pane := "  1. Sign in with ChatGPT\n  Press enter to continue\nDo you want to send the invoice to Acme? I need permission first.\nthe customer's secret plan is to buy acme\n\n"
	got := Summary(pane)
	if !strings.Contains(got, "sign in with") || !strings.Contains(got, "press enter to continue") {
		t.Fatalf("the matched pattern names must be shown: %s", got)
	}
	for _, leaked := range []string{"Sign in with ChatGPT", "invoice", "Acme", "acme", "secret plan", "permission first"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("pane text %q must never be logged: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "lines=4") {
		t.Fatalf("line count expected: %s", got)
	}
	if !strings.Contains(got, "lines_hash=") {
		t.Fatalf("hash expected: %s", got)
	}
}

func TestWatchLogsOncePerQuietStretchAndRearmsOnChange(t *testing.T) {
	w := &Watch{Provider: "codex-cli", Session: "s1", After: time.Minute}
	start := time.Unix(1000, 0)
	logged := func() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.logged }
	w.Observe(start, "a", true)
	w.Observe(start.Add(30*time.Second), "a", true)
	if logged() {
		t.Fatal("logged before the quiet time")
	}
	w.Observe(start.Add(61*time.Second), "a", true)
	if !logged() {
		t.Fatal("not logged after the quiet time")
	}
	w.Observe(start.Add(90*time.Second), "b", true)
	if logged() {
		t.Fatal("a changed pane must re-arm the watch")
	}
}

func TestObserveSignalCallsDetailOnlyWhenItLogs(t *testing.T) {
	w := &Watch{Provider: "muse-cli", Session: "owner", After: time.Minute}
	calls := 0
	detail := func() string { calls++; return "muse_log_seq=7 app_cursor=3 pool=present" }
	start := time.Unix(2000, 0)
	w.ObserveSignal(start, "100:1", true, detail)
	w.ObserveSignal(start.Add(30*time.Second), "100:1", true, detail)
	if calls != 0 {
		t.Fatalf("detail ran before the quiet time: %d", calls)
	}
	w.ObserveSignal(start.Add(70*time.Second), "100:1", true, detail)
	w.ObserveSignal(start.Add(100*time.Second), "100:1", true, detail)
	if calls != 1 {
		t.Fatalf("detail must run once per quiet stretch, ran %d times", calls)
	}
}

// A name that is not a terminal session comes back as itself, so the line always names something searchable.
func TestOwnerOfFallsBackToTheName(t *testing.T) {
	if got := ownerOf("no-such-tmux-session"); got != "no-such-tmux-session" {
		t.Fatalf("owner = %q", got)
	}
	if got := ownerOf("  "); got != "-" {
		t.Fatalf("empty name = %q", got)
	}
}
