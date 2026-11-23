package codexcli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCodexPlanUsageLive is the CertPlanUsage P0 proof for Codex: after a real
// turn, GetStatusLine for the session carries the account's plan usage
// windows (from the rollout's token_count rate_limits) as structured
// RateLimitWindows, and UsageLimitState reads them for the watchdog.
//
// Gated behind -coding-cli-p0-live; requires a real codex CLI, node, tmux.
func TestCodexPlanUsageLive(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	started := time.Now()
	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	owner := "codex-plan-usage-live-" + codexRandomHex(4)
	marker := "PLAN_" + strings.ToUpper(codexRandomHex(4))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Task "+marker+": what is 2+2? Reply with one line: the answer, a space, then the task ID.")},
		WithInteractiveSessionID(owner),
		WithPersistentInteractiveSession(true),
		WithProjectDirID(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	if len(resp.Choices) != 1 || !strings.Contains(resp.Choices[0].Content, marker) {
		t.Fatalf("turn did not produce the marker: %+v", resp)
	}
	status, err := adapter.GetStatusLine(ctx, owner)
	if err != nil || status == nil {
		t.Fatalf("GetStatusLine: %v %v", status, err)
	}
	windows := status.RateLimitWindows()
	if len(windows) == 0 {
		t.Fatalf("no plan usage windows on the status line after a real turn: %+v", status.Metadata)
	}
	for _, w := range windows {
		if strings.TrimSpace(w.Name) == "" || w.UsedPercent < 0 {
			t.Fatalf("malformed window %+v", w)
		}
		if !w.ResetsAt.IsZero() && w.ResetsAt.Before(started.Add(-time.Minute)) {
			t.Fatalf("window %s resets in the past: %v", w.Name, w.ResetsAt)
		}
	}
	if tmux, _ := status.Metadata["tmux_session"].(string); tmux != "" {
		if known, _ := UsageLimitState(tmux); !known {
			t.Fatalf("UsageLimitState(%s) unknown although the status line has windows", tmux)
		}
	}
}
