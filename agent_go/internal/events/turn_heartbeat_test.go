package events

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

// A stuck turn must surface in the log: heartbeats at 2, 5, 10 and 20 minutes, then every 20 minutes, one at a time.
func TestTurnHeartbeatDue(t *testing.T) {
	cases := []struct {
		age   time.Duration
		beats int
		want  bool
	}{
		{time.Minute, 0, false},
		{2 * time.Minute, 0, true},
		{3 * time.Minute, 1, false},
		{5 * time.Minute, 1, true},
		{10 * time.Minute, 2, true},
		{20 * time.Minute, 3, true},
		{30 * time.Minute, 4, false},
		{40 * time.Minute, 4, true},
		{40 * time.Minute, 5, false},
		{60 * time.Minute, 5, true},
	}
	for _, c := range cases {
		if got := turnHeartbeatDue(c.age, c.beats); got != c.want {
			t.Errorf("age %s beats %d: got %v want %v", c.age, c.beats, got, c.want)
		}
	}
}

// A turn's still_running line carries the provider's content-free state when a callback is installed.
func TestTurnHeartbeatCarriesDiagnostics(t *testing.T) {
	SetTurnDiagnostics(func(session string) string {
		if session == "muse-session" {
			return "muse_log_seq=412 app_cursor=97 pool=present"
		}
		return ""
	})
	t.Cleanup(func() { SetTurnDiagnostics(nil) })
	if got := turnDiagnosticsFor("muse-session"); got != "muse_log_seq=412 app_cursor=97 pool=present" {
		t.Fatalf("diagnostics = %q", got)
	}
	if got := turnDiagnosticsFor("other-session"); got != "" {
		t.Fatalf("a turn the callback does not know adds nothing, got %q", got)
	}
	var out bytes.Buffer
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	streamingStatsMu.Lock()
	streamingStats["muse-session"] = &streamingTurnStats{startedAt: time.Now().Add(-3 * time.Minute), chunks: 1}
	streamingStatsMu.Unlock()
	t.Cleanup(func() { streamingStatsMu.Lock(); delete(streamingStats, "muse-session"); streamingStatsMu.Unlock() })
	sweepTurnHeartbeats(time.Now())
	if !strings.Contains(out.String(), "still_running") || !strings.Contains(out.String(), "muse_log_seq=412 app_cursor=97 pool=present") {
		t.Fatalf("heartbeat line = %q", out.String())
	}
}
