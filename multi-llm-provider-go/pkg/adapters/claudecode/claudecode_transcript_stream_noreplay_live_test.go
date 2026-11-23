package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

// TestClaudeTranscriptStreamNoHistoryReplayLive proves against a real Claude
// turn what the gap notes only assumed: the JSONL tailer drops rows older
// than turnStart, so a freshly constructed reader (offset 0 — exactly the
// state a restarted process begins in) cannot re-emit prior-turn rows. The
// turn commits real history to a real transcript; the test then reads it back
// at the turn's own boundary (history + shape proof) and at a NOW boundary
// (replay assertion). No settle wait is needed: the filter keys on row
// timestamps, which predate the fresh reader's boundary no matter when the
// bytes commit.
//
// Gated behind -coding-cli-p0-live; requires a real claude CLI, node, tmux.
func TestClaudeTranscriptStreamNoHistoryReplayLive(t *testing.T) {
	skipClaudeInteractiveIntegration(t)
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	workDir := t.TempDir()
	marker := "NOREPLAY_" + strings.ToUpper(randomHex(4))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	turnStart := time.Now()
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Task "+marker+": what is 2+2? Reply with one line: the answer, a space, then the task ID.")},
		WithWorkingDir(workDir),
		WithEffort("low"),
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

	// The turn's transcript is the freshest JSONL under this working dir's
	// project slugs. The dir is unique to this test, so nothing else can
	// share it.
	path := freshestClaudeTranscriptForWorkDir(t, workDir)

	// History + shape proof: the production reader at the turn's own
	// boundary must observe the marker in the real transcript.
	historyEvents, _, err := readClaudeTranscriptEventsFromFile(path, 0, turnStart, map[string]time.Time{})
	if err != nil {
		t.Fatalf("read history events: %v", err)
	}
	var history strings.Builder
	for _, event := range historyEvents {
		history.WriteString(event.Text)
	}
	if !strings.Contains(history.String(), marker) {
		t.Fatalf("marker %q not found in transcript %s; cannot prove replay suppression", marker, path)
	}

	// Restart simulation: offset 0 with a NOW boundary over the same file.
	freshEvents, _, err := readClaudeTranscriptEventsFromFile(path, 0, time.Now(), map[string]time.Time{})
	if err != nil {
		t.Fatalf("read fresh events: %v", err)
	}
	var streamed strings.Builder
	for _, event := range freshEvents {
		streamed.WriteString(event.Text)
	}
	if strings.Contains(streamed.String(), marker) {
		t.Fatalf("history replayed on a fresh reader: %q\nstreamed: %q", marker, streamed.String())
	}
	t.Logf("fresh reader emitted no history; marker %q verified in transcript %s", marker, path)
}

// freshestClaudeTranscriptForWorkDir resolves the turn's transcript the way
// the resolver does — project slugs derived from every candidate spelling of
// the working dir — and returns the most recently modified match.
func freshestClaudeTranscriptForWorkDir(t *testing.T, workDir string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	var freshest string
	var freshestMod time.Time
	for _, dir := range pathidentity.Candidates(workDir) {
		slug := claudeTranscriptProjectSlug(dir)
		if slug == "" {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", slug, "*.jsonl"))
		if err != nil {
			t.Fatalf("glob transcripts: %v", err)
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}
			if freshest == "" || info.ModTime().After(freshestMod) {
				freshest, freshestMod = match, info.ModTime()
			}
		}
	}
	if freshest == "" {
		t.Fatalf("no transcript found for working dir %s; cannot prove replay suppression", workDir)
	}
	return freshest
}
