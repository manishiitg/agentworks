package server

import (
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-990: a slow probe may not erase newer native account windows. The
// complete per-model window list belongs to exactly one provider/account key.
func TestAccountLimitsKeepLiveWindowsOverOlderProbe(t *testing.T) {
	cache := accountLimitCache{}
	started := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	reset := started.Add(time.Hour)
	windows := []llmtypes.AccountUsageWindow{{Label: "Weekly · Opus", UsedPercent: 75, ResetsAt: &reset}, {Label: "5h", UsedPercent: 0}}
	if !cache.put("claude-code", "own-account", "event", started.Add(time.Second), windows) {
		t.Fatal("native snapshot rejected")
	}
	if cache.put("claude-code", "own-account", "probe", started, nil) {
		t.Fatal("older probe erased live event")
	}
	windows[0].Label = "changed caller slice"
	reset = reset.Add(time.Hour)
	got, ok := cache.get("claude-code", "own-account")
	if !ok || len(got.Windows) != 2 || got.Windows[0].Label != "Weekly · Opus" || !got.Windows[0].ResetsAt.Equal(started.Add(time.Hour)) || got.Windows[1].UsedPercent != 0 {
		t.Fatalf("window facts lost: %+v", got)
	}
	got.Windows[0].Label = "changed response"
	again, _ := cache.get("claude-code", "own-account")
	if again.Windows[0].Label != "Weekly · Opus" {
		t.Fatal("reader mutated shared snapshot")
	}
	if _, ok := cache.get("claude-code", "other-account"); ok {
		t.Fatal("another account inherited windows")
	}
	if _, ok := cache.get("codex-cli", "own-account"); ok {
		t.Fatal("another provider inherited windows")
	}
}
