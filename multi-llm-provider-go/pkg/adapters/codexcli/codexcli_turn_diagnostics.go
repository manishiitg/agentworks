package codexcli

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

// DiagnoseTurnCompletion is a best-effort, POST-HOC diagnostic, not a live
// completion oracle (PLAT-116). It exists for exactly one moment: the
// platform's own bridge from a completed turn back to its caller has already
// given up waiting (the scheduler's idle-wait safety net timed out), and the
// resulting error needs to say whether Codex itself actually finished — and
// what it said — instead of leaving that only discoverable by hand-reading
// the rollout JSONL, the way the reproducing incident on PLAT-116 was found.
//
// Because it only ever runs after real completion detection
// (codexTurnCompletionTracker, session-identity bound per PLAT-108) has
// already failed to resolve the turn, using the unsafe working-directory
// lookup here is acceptable: a wrong-session read only degrades the
// diagnostic text in an already-failed turn's error message, it never drives
// control flow or claims the turn succeeded.
func DiagnoseTurnCompletion(workingDir string, since time.Time) (found bool, completedAt time.Time, lastAgentMessage string) {
	path := findCodexRolloutByWorkingDirUnsafe(since, strings.TrimSpace(workingDir))
	if path == "" {
		return false, time.Time{}, ""
	}
	f, err := os.Open(path)
	if err != nil {
		return false, time.Time{}, ""
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			if at, msg, ok := parseCodexTaskCompleteLine(line, since); ok {
				found = true
				completedAt = at
				lastAgentMessage = msg
			}
		}
		if err != nil {
			if err != io.EOF {
				return false, time.Time{}, ""
			}
			break
		}
	}
	return found, completedAt, lastAgentMessage
}

func parseCodexTaskCompleteLine(line string, since time.Time) (time.Time, string, bool) {
	type rolloutEvent struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type             string `json:"type"`
			LastAgentMessage string `json:"last_agent_message"`
		} `json:"payload"`
	}
	var event rolloutEvent
	if json.Unmarshal([]byte(line), &event) != nil {
		return time.Time{}, "", false
	}
	if event.Type != "event_msg" || event.Payload.Type != "task_complete" {
		return time.Time{}, "", false
	}
	timestamp, err := time.Parse(time.RFC3339Nano, event.Timestamp)
	if err != nil || timestamp.Before(since) {
		return time.Time{}, "", false
	}
	return timestamp, event.Payload.LastAgentMessage, true
}
