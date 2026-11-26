package events

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

// The speed line is how start-up time is measured (#3724): it must come once, at
// the first agent activity, carry the setup steps and the agent program's own
// moments (each once), and end with the turn.
func TestTurnSpeedLogsFirstActivityOnceThenTurnEnd(t *testing.T) {
	var out bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&out)
	defer log.SetOutput(previous)
	TurnSpeedStart("s1", time.Now())
	TurnSpeedStep("s1", "setup")
	TurnSpeedSpan("s1", "secrets")()
	TurnSpeedSpan("s1", "secrets")()
	TurnSpeedProcess("s1", "claude-code", "reuse", "launch_unchanged")
	TurnSpeedStage("s1", "claude-code", "prompt_sent")
	TurnSpeedStage("s1", "claude-code", "cli_output")
	TurnSpeedStage("s1", "claude-code", "cli_output")
	trackTurnSpeed("s1", Event{Type: "user_message"})
	// The server's own "sending to the model" event is not the agent's activity.
	trackTurnSpeed("s1", Event{Type: "llm_generation_start"})
	trackTurnSpeed("s1", Event{Type: "tool_call_start"})
	trackTurnSpeed("s1", Event{Type: "tool_call_start"})
	trackTurnSpeed("s1", Event{Type: "streaming_chunk"})
	trackTurnSpeed("s1", Event{Type: "unified_completion"})
	trackTurnSpeed("s1", Event{Type: "streaming_chunk"})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "first=tool_call_start") || !strings.Contains(lines[0], "steps=setup:") ||
		!strings.Contains(lines[0], "secrets:0x2") || !strings.Contains(lines[0], "agent=prompt_sent:") ||
		strings.Count(lines[0], "cli_output") != 1 || !strings.Contains(lines[0], "process=reuse:launch_unchanged") ||
		!strings.Contains(lines[1], "first_text_ms=") || !strings.Contains(lines[2], "turn_ms=") {
		t.Fatalf("speed lines:\n%s", out.String())
	}
}
