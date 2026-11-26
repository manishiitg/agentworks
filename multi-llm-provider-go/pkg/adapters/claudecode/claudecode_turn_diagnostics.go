package claudecode

import (
	"strings"
	"time"
)

// DiagnoseTurnCompletion is a best-effort, POST-HOC diagnostic, not a live
// completion oracle (PLAT-116). It exists for exactly one moment: the
// platform's own bridge from a completed turn back to its caller has already
// given up waiting (the scheduler's idle-wait safety net timed out), and the
// resulting error needs to say whether Claude Code itself actually finished
// — and what it said — instead of that only being discoverable by hand-
// reading the session's own JSONL transcript.
//
// It reuses completedAssistantResponseFromTranscript, the same session-bound
// reader interactive completion already treats as authoritative over the
// tmux pane. Because this only ever runs after that real detection has
// already failed to resolve the turn, a stale or wrong read here only
// degrades the diagnostic text in an already-failed turn's error message —
// it never drives control flow or claims the turn succeeded.
func DiagnoseTurnCompletion(nativeSessionID, workingDir string, since time.Time) (found bool, lastAssistantText string) {
	response := completedAssistantResponseFromTranscript(nativeSessionID, workingDir, since)
	if !response.Completed || strings.TrimSpace(response.Text) == "" {
		return false, ""
	}
	return true, strings.TrimSpace(response.Text)
}
