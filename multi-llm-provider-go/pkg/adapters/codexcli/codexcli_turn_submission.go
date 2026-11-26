package codexcli

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// codexSubmissionOracle reports whether Codex has started the turn for the
// input that was just submitted. authoritative is false while no rollout is
// bound to the session yet; the pane heuristics then decide instead.
type codexSubmissionOracle func() (started, authoritative bool)

// codexSubmissionWait tunes waitForCodexInputSubmittedWith. Zero values select
// the live defaults; tests inject capture and shorter delays.
type codexSubmissionWait struct {
	oracle        codexSubmissionOracle
	resubmit      func(context.Context) error
	resubmitAfter time.Duration
	maxResubmits  int
	capture       func(context.Context, string) (string, error)
}

const (
	codexDefaultResubmitAfter = 1500 * time.Millisecond
	codexDefaultMaxResubmits  = 2
)

// codexTurnStartOracle confirms a submission from the session's own rollout.
// Codex writes task_started the moment it accepts the input, which the pane
// cannot be trusted to show: Codex 0.153 keeps a "Conversation interrupted"
// notice after an aborted turn, the next Enter only dismisses that notice, and
// the pane still read as submitted while the prompt sat unsent in the
// composer. The caller holds session.mu.
func codexTurnStartOracle(session *codexInteractiveSession, since time.Time) codexSubmissionOracle {
	return func() (bool, bool) {
		path := resolveCodexRolloutPathLocked(session, since)
		if path == "" {
			return false, false
		}
		return codexRolloutTurnStartedSince(path, since) != "", true
	}
}

// codexRolloutTurnStartedSince returns the turn_id of the first task_started
// Codex recorded at or after since, or "" when no turn has begun since then.
func codexRolloutTurnStartedSince(path string, since time.Time) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	type event struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type   string `json:"type"`
			TurnID string `json:"turn_id"`
		} `json:"payload"`
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		var e event
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		if e.Type != "event_msg" || e.Payload.Type != "task_started" {
			continue
		}
		timestamp, parseErr := time.Parse(time.RFC3339Nano, e.Timestamp)
		if parseErr != nil || timestamp.Before(since) {
			continue
		}
		if id := strings.TrimSpace(e.Payload.TurnID); id != "" {
			return id
		}
		return "started"
	}
	return ""
}

// codexStartupPasteWait bounds how long a submission waits for a relaunched
// TUI to finish replaying its conversation before the prompt is pasted.
const codexStartupPasteWait = 10 * time.Second

// codexPaneIsResuming reports Codex's "Resuming session…" banner. The composer
// placeholder is already drawn underneath it, so without this check the pane
// reads as an idle, ready prompt while Codex is still loading the thread;
// input pasted then is lost, and the replayed history that follows looks like
// a finished turn.
func codexPaneIsResuming(captured string) bool {
	lines := strings.Split(stripCodexANSI(captured), "\n")
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < 12; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		seen++
		// Codex 0.160 leaves its startup banner in scrollback and renders
		// a new header when replay is ready. Do not let that older banner
		// block short resumed conversations indefinitely.
		if strings.HasPrefix(strings.ToLower(strings.TrimLeft(line, "│ ")), ">_ openai codex") {
			return false
		}
		if strings.HasPrefix(strings.ToLower(line), "resuming session") {
			return true
		}
	}
	return false
}

// waitForCodexPaneOutOfStartup holds a submission while the pane is blank or
// still resuming. Any other state returns at once; on timeout the caller
// proceeds and the submission wait reports the outcome.
func waitForCodexPaneOutOfStartup(ctx context.Context, sessionName string, timeout time.Duration, capture func(context.Context, string) (string, error)) {
	deadline := time.Now().Add(timeout)
	for {
		captured, err := capture(ctx, sessionName)
		if err != nil {
			return
		}
		if strings.TrimSpace(stripCodexANSI(captured)) != "" && !codexPaneIsResuming(captured) {
			return
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// codexPaneHoldsUnsentDraft reports that the prompt is still sitting in the
// composer with nothing running above it: Enter did not start a turn.
func codexPaneHoldsUnsentDraft(captured, message string) bool {
	return codexPaneShowsPromptDraft(captured, message) && !hasCodexActivity(captured)
}
