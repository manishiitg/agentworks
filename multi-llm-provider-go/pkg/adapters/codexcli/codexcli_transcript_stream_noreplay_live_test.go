package codexcli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCodexTranscriptStreamNoHistoryReplayLive is the live counterpart of
// TestCodexTranscriptStreamDoesNotReplayHistoryOnFreshProcess: a real codex
// turn commits real history to a real rollout, then a freshly constructed
// reader (offset 0, empty dedup maps — exactly the state a restarted process
// begins in) must not re-emit it. The deterministic test proves the timestamp
// filter against a synthetic rollout; this proves the real CLI writes its
// rollout in the shape the filter assumes. No settle wait is needed: the
// filter keys on row timestamps, which predate the fresh reader's boundary no
// matter when the bytes commit.
//
// Gated behind -coding-cli-p0-live; requires a real codex CLI, node, tmux.
func TestCodexTranscriptStreamNoHistoryReplayLive(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	workDir := t.TempDir()
	owner := "codex-noreplay-live-" + codexRandomHex(4)
	marker := "NOREPLAY_" + strings.ToUpper(codexRandomHex(4))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	turnStart := time.Now()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Task "+marker+": what is 2+2? Reply with one line: the answer, a space, then the task ID.")},
		WithInteractiveSessionID(owner),
		WithPersistentInteractiveSession(true),
		WithProjectDirID(workDir),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	final := ""
	if len(resp.Choices) == 1 {
		final = strings.TrimSpace(resp.Choices[0].Content)
	}
	if !strings.Contains(final, marker) {
		t.Fatalf("turn did not produce the marker; final=%q", final)
	}

	// History + shape proof: the production reader at the turn's own
	// boundary must observe the marker in the real rollout. Without this,
	// absence below could mean "nothing to replay".
	path := findCodexRolloutByWorkingDirUnsafe(turnStart, workDir)
	if path == "" {
		t.Fatalf("no rollout resolved for working dir %s; cannot prove replay suppression", workDir)
	}
	historyEvents, _, err := readCodexTranscriptEventsFromFile(path, 0, turnStart, map[string]time.Time{})
	if err != nil {
		t.Fatalf("read history events: %v", err)
	}
	var history strings.Builder
	for _, event := range historyEvents {
		history.WriteString(event.Text)
	}
	if !strings.Contains(history.String(), marker) {
		t.Fatalf("marker %q not found in rollout %s; cannot prove replay suppression", marker, path)
	}

	// Restart simulation: a brand-new state object at offset 0 with a NOW
	// boundary, resolving the same rollout by working directory.
	state := newCodexTranscriptStreamState(time.Now(), workDir, nil)
	ch := make(chan llmtypes.StreamChunk, 256)
	state.poll(context.Background(), ch)
	close(ch)
	if state.path == "" {
		t.Fatal("fresh reader resolved no rollout; the absence assertion would be vacuous")
	}
	if state.path != path {
		t.Fatalf("fresh reader resolved %s, want the turn's rollout %s", state.path, path)
	}
	var streamed strings.Builder
	for chunk := range ch {
		if chunk.Type == llmtypes.StreamChunkTypeContent {
			streamed.WriteString(chunk.Content)
		}
	}
	if strings.Contains(streamed.String(), marker) {
		t.Fatalf("history replayed on a fresh reader: %q\nstreamed: %q", marker, streamed.String())
	}
	t.Logf("fresh reader emitted no history; marker %q verified in rollout %s", marker, path)
}
