package codexcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rolloutAuthorityFixture fakes an idle Codex composer that shows STATUS:
// COMPLETED, and a rollout that has recorded this turn's final answer but no
// task_complete, which is the state where pane idleness used to end the turn.
func rolloutAuthorityFixture(t *testing.T) (turnStart time.Time, workingDir string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	workingDir = filepath.Join(t.TempDir(), "mlp-cli-session")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dayDir := filepath.Join(home, ".codex", "sessions", "2026", "09", "23")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	turnStart = time.Now().UTC().Add(-time.Second)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rollout := fmt.Sprintf("%s\n%s\n%s\n",
		fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q}}`, workingDir),
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`, now),
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","phase":"final_answer"}}`, now),
	)
	if err := os.WriteFile(filepath.Join(dayDir, "rollout-2026-09-23T12-00-00-44444444-4444-4444-8444-444444444444.jsonl"), []byte(rollout), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeBin := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "capture-pane" ]; then
  printf '%s\n' \
    '• Wrote the aggregate.' \
    '  STATUS: COMPLETED' \
    '────────────────────────────────────────────────────────────────' \
    '› Find and fix a bug in @filename' \
    '  gpt-6-sol medium · /tmp/workspace'
fi
`
	if err := os.WriteFile(filepath.Join(fakeBin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return turnStart, workingDir
}

// PLAT-354: once the rollout owns the turn, an idle pane must not end it
// before task_complete. Only the bounded late fallback may, and it says so.
func TestWaitForCodexInteractiveResponseRolloutOwnsTurn(t *testing.T) {
	turnStart, workingDir := rolloutAuthorityFixture(t)
	saved := codexRolloutLateFallback
	t.Cleanup(func() { codexRolloutLateFallback = saved })

	codexRolloutLateFallback = time.Minute
	source := ""
	hooks := &codexCompletionDiagnosticHooks{completedBy: func(s string) { source = s }}
	ctx, cancel := context.WithTimeout(context.Background(), codexInteractiveStableWindow+2*time.Second)
	_, err := waitForCodexInteractiveResponse(ctx, "rollout-owns-turn", "Codex ready\n›", nil, turnStart, workingDir, false, false, nil, hooks)
	cancel()
	if err == nil {
		t.Fatalf("idle pane ended a rollout-owned turn before task_complete (source %q)", source)
	}

	codexRolloutLateFallback = codexInteractiveStableWindow + time.Second
	source = ""
	ctx, cancel = context.WithTimeout(context.Background(), codexRolloutLateFallback+4*time.Second)
	defer cancel()
	started := time.Now()
	if _, err := waitForCodexInteractiveResponse(ctx, "rollout-late-fallback", "Codex ready\n›", nil, turnStart, workingDir, false, false, nil, hooks); err != nil {
		t.Fatalf("late fallback did not release the turn: %v", err)
	}
	if source != codexCompletionSourceRolloutLateFallback {
		t.Fatalf("completion source = %q, want %q", source, codexCompletionSourceRolloutLateFallback)
	}
	if elapsed := time.Since(started); elapsed < codexRolloutLateFallback {
		t.Fatalf("late fallback fired after %v, before its %v bound", elapsed, codexRolloutLateFallback)
	}
}
