package codexcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexTurnCompletionTrackerUsesMatchingRolloutTaskComplete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	dayDir := filepath.Join(home, ".codex", "sessions", "2026", "07", "16")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	wantedWorkingDir := filepath.Join(t.TempDir(), "wanted-session")
	otherWorkingDir := filepath.Join(t.TempDir(), "other-session")
	turnStart := time.Now().UTC().Add(-time.Second)
	completionTime := turnStart.Add(500 * time.Millisecond).Format(time.RFC3339Nano)

	writeRollout := func(name, cwd string, complete bool) string {
		t.Helper()
		path := filepath.Join(dayDir, name)
		lines := []string{fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q}}`, cwd)}
		if complete {
			lines = append(lines, fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"task_complete"}}`, completionTime))
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write rollout: %v", err)
		}
		return path
	}
	wanted := writeRollout("rollout-2026-07-16T12-00-00-11111111-1111-4111-8111-111111111111.jsonl", wantedWorkingDir, true)
	other := writeRollout("rollout-2026-07-16T12-00-01-22222222-2222-4222-8222-222222222222.jsonl", otherWorkingDir, false)
	newer := time.Now().Add(time.Second)
	if err := os.Chtimes(other, newer, newer); err != nil {
		t.Fatalf("set other rollout mtime: %v", err)
	}

	tracker := newCodexTurnCompletionTracker(turnStart, wantedWorkingDir, nil)
	if !tracker.completed() {
		t.Fatal("matching rollout task_complete event was not detected")
	}
	if tracker.rolloutPath != wanted {
		t.Fatalf("rolloutPath = %q, want matching %q", tracker.rolloutPath, wanted)
	}
}

func TestCodexTurnCompletionTrackerReportsBoundRolloutAndNativeCompletion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	workingDir := filepath.Join(t.TempDir(), "diagnostic-session")
	dayDir := filepath.Join(home, ".codex", "sessions", "2026", "08", "19")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	turnStart := time.Now().UTC().Add(-time.Second)
	completedAt := turnStart.Add(500 * time.Millisecond)
	rollout := filepath.Join(dayDir, "rollout-2026-08-19T12-00-00-aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.jsonl")
	transcript := strings.Join([]string{
		fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"cwd":%q}}`, "thread-123", workingDir),
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"task_complete","turn_id":%q}}`, completedAt.Format(time.RFC3339Nano), "turn-456"),
	}, "\n") + "\n"
	if err := os.WriteFile(rollout, []byte(transcript), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	var selectedPath, selectedThread string
	var completedPath, completedThread, completedTurn string
	var observedAt time.Time
	var observedOffset int64
	tracker := newCodexTurnCompletionTracker(turnStart, workingDir, nil)
	tracker.setDiagnosticHooks(&codexCompletionDiagnosticHooks{
		rolloutSelected: func(path, threadID string) {
			selectedPath, selectedThread = path, threadID
		},
		taskComplete: func(path, threadID, turnID string, at time.Time, offset int64) {
			completedPath, completedThread, completedTurn = path, threadID, turnID
			observedAt, observedOffset = at, offset
		},
	})

	if !tracker.completed() {
		t.Fatal("task_complete event was not detected")
	}
	if selectedPath != rollout || completedPath != rollout {
		t.Fatalf("diagnostic rollout paths = selected %q completed %q, want %q", selectedPath, completedPath, rollout)
	}
	if selectedThread != "thread-123" || completedThread != "thread-123" {
		t.Fatalf("diagnostic thread IDs = selected %q completed %q", selectedThread, completedThread)
	}
	if completedTurn != "turn-456" {
		t.Fatalf("diagnostic turn ID = %q, want turn-456", completedTurn)
	}
	if !observedAt.Equal(completedAt) {
		t.Fatalf("diagnostic completion time = %s, want %s", observedAt, completedAt)
	}
	if observedOffset <= 0 {
		t.Fatalf("diagnostic offset = %d, want positive", observedOffset)
	}
}

func TestWaitForCodexInteractiveResponseUsesTaskCompleteWhenFooterIsMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	workingDir := filepath.Join(t.TempDir(), "mlp-cli-session")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatalf("mkdir working dir: %v", err)
	}
	dayDir := filepath.Join(home, ".codex", "sessions", "2026", "07", "16")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	turnStart := time.Now().UTC().Add(-time.Second)
	rollout := filepath.Join(dayDir, "rollout-2026-07-16T12-00-00-33333333-3333-4333-8333-333333333333.jsonl")
	transcript := fmt.Sprintf("%s\n%s\n",
		fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q}}`, workingDir),
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Wrote the aggregate.\\n\\nSTATUS: COMPLETED"}}`, time.Now().UTC().Format(time.RFC3339Nano)),
	)
	if err := os.WriteFile(rollout, []byte(transcript), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	fakeBin := t.TempDir()
	tmuxPath := filepath.Join(fakeBin, "tmux")
	script := `#!/bin/sh
if [ "$1" = "capture-pane" ]; then
  printf '%s\n' \
    '• Working (48s • esc to interrupt)' \
    '• Called api-bridge.execute_shell_command({"command":"verify"})' \
    '  command completed successfully' \
    '────────────────────────────────────────────────────────────────' \
    '• Wrote the aggregate.' \
    '  STATUS: COMPLETED' \
    '────────────────────────────────────────────────────────────────' \
    '› Find and fix a bug in @filename' \
    '  gpt-6-sol medium · /tmp/workspace'
fi
`
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	pane := `
• Working (48s • esc to interrupt)
• Wrote the aggregate.
  STATUS: COMPLETED
────────────────────────────────────────────────────────────────
› Find and fix a bug in @filename
  gpt-6-sol medium · /tmp/workspace
`
	if !hasCodexActivity(pane) {
		t.Fatal("fixture must reproduce stale Working activity without a Worked-for footer")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	captured, err := waitForCodexInteractiveResponse(ctx, "missing-footer-session", "Codex ready\n›", nil, turnStart, workingDir, false, true, nil, nil)
	if err != nil {
		t.Fatalf("native task_complete event did not release response wait: %v", err)
	}
	if !strings.Contains(captured, "STATUS: COMPLETED") {
		t.Fatalf("final response capture was lost: %q", captured)
	}
	// The watcher polls every 250ms. Leave scheduler headroom for a loaded CI
	// host while still proving native completion returns well before timeout.
	if elapsed := time.Since(started); elapsed >= 4*time.Second {
		t.Fatalf("task_complete detection was unexpectedly slow: %v", elapsed)
	}
}

func TestCodexTurnCompletionTrackerIgnoresPriorTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	workingDir := filepath.Join(t.TempDir(), "persistent-session")
	dayDir := filepath.Join(home, ".codex", "sessions", "2026", "07", "16")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	turnStart := time.Now().UTC()
	rollout := filepath.Join(dayDir, "rollout-2026-07-16T12-00-00-44444444-4444-4444-8444-444444444444.jsonl")
	transcript := fmt.Sprintf("%s\n%s\n",
		fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q}}`, workingDir),
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"task_complete"}}`, turnStart.Add(-time.Second).Format(time.RFC3339Nano)),
	)
	if err := os.WriteFile(rollout, []byte(transcript), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	tracker := newCodexTurnCompletionTracker(turnStart, workingDir, nil)
	if tracker.completed() {
		t.Fatal("task_complete from a prior persistent-session turn was accepted")
	}
}

func TestCodexTurnCompletionTrackerBlocksIdleComposerWhileMCPCallPending(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	workingDir := filepath.Join(t.TempDir(), "pending-mcp-session")
	dayDir := filepath.Join(home, ".codex", "sessions", "2026", "07", "17")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	turnStart := time.Now().UTC().Add(-time.Second)
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	rollout := filepath.Join(dayDir, "rollout-2026-07-17T12-00-00-55555555-5555-4555-8555-555555555555.jsonl")
	initial := strings.Join([]string{
		fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q}}`, workingDir),
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"I am calling the bridge now."}]}}`, ts),
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"function_call","call_id":"call_pending","name":"write_contract"}}`, ts),
	}, "\n") + "\n"
	if err := os.WriteFile(rollout, []byte(initial), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	tracker := newCodexTurnCompletionTracker(turnStart, workingDir, nil)
	if tracker.completed() {
		t.Fatal("pending MCP call was treated as task completion")
	}
	if !tracker.blocksTerminalFallback() {
		t.Fatal("pending MCP call must block idle-composer terminal fallback")
	}

	f, err := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open rollout: %v", err)
	}
	completedRows := strings.Join([]string{
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"function_call_output","call_id":"call_pending","output":"ok"}}`, ts),
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"BRIDGE_OK"}]}}`, ts),
	}, "\n") + "\n"
	if _, err := f.WriteString(completedRows); err != nil {
		_ = f.Close()
		t.Fatalf("append rollout: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close rollout: %v", err)
	}
	if tracker.completed() {
		t.Fatal("final_answer without task_complete should not claim native completion")
	}
	if tracker.blocksTerminalFallback() {
		t.Fatal("completed MCP call plus final_answer should allow stable terminal fallback")
	}
}

func TestWaitForCodexInteractiveResponseFiveMinuteFallbackRequiresIdleComposer(t *testing.T) {
	fakeBin := t.TempDir()
	tmuxPath := filepath.Join(fakeBin, "tmux")
	script := `#!/bin/sh
if [ "$1" = "capture-pane" ]; then
  printf '%s\n' \
    '• Working (48s • esc to interrupt)' \
    '• Wrote the aggregate.' \
    '  STATUS: COMPLETED' \
    '────────────────────────────────────────────────────────────────' \
    '› Find and fix a bug in @filename' \
    '  gpt-6-sol medium · /tmp/workspace'
fi
`
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(EnvCodexInteractiveStalePaneBackstopSeconds, "1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	_, err := waitForCodexInteractiveResponse(ctx, "stable-idle-composer", "Codex ready\n›", nil, time.Time{}, "", false, true, nil, nil)
	if err != nil {
		t.Fatalf("stable idle-composer fallback did not release response wait: %v", err)
	}
	// The configured one-second window is a lower bound. Poll scheduling and
	// process startup can add latency on a busy CI host, so keep the upper bound
	// comfortably below the five-second test deadline without making it flaky.
	if elapsed := time.Since(started); elapsed < time.Second || elapsed >= 4*time.Second {
		t.Fatalf("fallback elapsed = %v, want approximately configured 1s", elapsed)
	}
}

func TestWaitForCodexInteractiveResponseFallbackRejectsIdleComposerWithoutCompletedMarker(t *testing.T) {
	fakeBin := t.TempDir()
	tmuxPath := filepath.Join(fakeBin, "tmux")
	script := `#!/bin/sh
if [ "$1" = "capture-pane" ]; then
  printf '%s\n' \
	'• Working (48s • esc to interrupt)' \
	'• The agent produced an intermediate summary but has not declared completion.' \
	'────────────────────────────────────────────────────────────────' \
	'› Find and fix a bug in @filename' \
	'  gpt-6-sol medium · /tmp/workspace'
fi
`
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(EnvCodexInteractiveStalePaneBackstopSeconds, "1")

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := waitForCodexInteractiveResponse(ctx, "stable-composer-no-completed-marker", "Codex ready\n›", nil, time.Time{}, "", false, true, nil, nil)
	if err == nil {
		t.Fatal("stable idle composer without STATUS: COMPLETED was incorrectly accepted")
	}
	if elapsed := time.Since(started); elapsed < 1300*time.Millisecond {
		t.Fatalf("stable idle composer without STATUS: COMPLETED returned early after %v: %v", elapsed, err)
	}
}
