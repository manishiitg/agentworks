package codexcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCodexMaxUsedPercent(t *testing.T) {
	if _, ok := codexMaxUsedPercent(nil); ok {
		t.Fatal("nil rate limits must be unknown")
	}
	used, ok := codexMaxUsedPercent(&codexRateLimits{Primary: &codexRateLimitWindow{UsedPercent: 12}, Secondary: &codexRateLimitWindow{UsedPercent: 64}})
	if !ok || used != 64 {
		t.Fatalf("used=%v ok=%v", used, ok)
	}
	if known, _ := UsageLimitState("no-such-tmux-session"); known {
		t.Fatal("an unknown session must be unknown")
	}
}

// Codex's rollout rate-limit windows reach the shared structured form (same
// contract as Claude): named by length, with the absolute reset instant.
func TestCodexRolloutRateLimitWindowsAreStructured(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dayDir := filepath.Join(tmpHome, ".codex", "sessions", "2026", "06", "04")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(dayDir, "rollout-2026-06-04T12-00-00-aaaabbbb-cccc-4ddd-8eee-ffff00002222.jsonl")
	turnStart := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	ts := turnStart.Add(2 * time.Second).Format(time.RFC3339Nano)
	lines := []string{
		`{"type":"session_meta","timestamp":"` + ts + `","payload":{"id":"aaaabbbb-cccc-4ddd-8eee-ffff00002222","cwd":"/x"}}`,
		`{"type":"event_msg","timestamp":"` + ts + `","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":500,"output_tokens":80,"total_tokens":580}},"rate_limits":{"primary":{"used_percent":17.5,"window_minutes":300,"resets_at":1790497800},"secondary":{"used_percent":35,"window_minutes":10080,"resets_at":0}}}}`,
	}
	if err := os.WriteFile(rollout, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mtime := turnStart.Add(5 * time.Second)
	if err := os.Chtimes(rollout, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	gi, _, _ := readCodexTranscriptUsage(turnStart, "/x")
	if gi == nil {
		t.Fatal("no usage read")
	}
	windows, ok := gi.Additional[llmtypes.RateLimitWindowsMetaKey].([]llmtypes.RateLimitWindow)
	if !ok || len(windows) != 2 {
		t.Fatalf("windows = %#v", gi.Additional[llmtypes.RateLimitWindowsMetaKey])
	}
	if windows[0].Name != "five_hour" || windows[0].UsedPercent != 17.5 || !windows[0].ResetsAt.Equal(time.Unix(1790497800, 0)) {
		t.Fatalf("primary = %#v", windows[0])
	}
	if windows[1].Name != "seven_day" || windows[1].UsedPercent != 35 || !windows[1].ResetsAt.IsZero() {
		t.Fatalf("secondary = %#v (an unstated reset must stay zero)", windows[1])
	}
	status := &llmtypes.StatusLine{}
	status.SetRateLimitWindows(windows)
	if got := status.RateLimitWindows(); len(got) != 2 {
		t.Fatalf("status line windows = %#v", got)
	}
	if name := codexWindowName(60, "primary"); name != "window_60m" {
		t.Fatalf("60-minute window name = %q", name)
	}
}
