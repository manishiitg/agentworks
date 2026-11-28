package agycli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T3 Code #44189: the model agy ran is read from its transcript, newest reply since the latest prompt only, and only
// when it has a price.
func TestAgyExecutedModelReadsTheLatestReply(t *testing.T) {
	home := t.TempDir()
	logs := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "native", ".system_generated", "logs")
	if err := os.MkdirAll(logs, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(lines ...string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(logs, "transcript.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outcome := agyACPOutcome{home: home}
	if got := outcome.ExecutedModel("native"); got != "" {
		t.Fatalf("no transcript = %q", got)
	}
	write(`{"step_index":0,"type":"USER_INPUT","status":"DONE"}`,
		`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","model":"gemini-4-neon-high"}`,
		`{"step_index":2,"type":"USER_INPUT","status":"DONE"}`,
		`{"step_index":3,"type":"PLANNER_RESPONSE","status":"DONE","model":"gemini-4-hydrogen-low"}`,
		`{"step_index":4,"type":"RUN_COMMAND","status":"DONE"}`)
	if got := agyExecutedModel(home, "native"); got != "gemini-4-hydrogen-low" {
		t.Fatalf("executed model = %q, want the newest reply's", got)
	}
	write(`{"step_index":0,"type":"PLANNER_RESPONSE","status":"DONE","model":"gemini-4-hydrogen-low"}`,
		`{"step_index":1,"type":"USER_INPUT","status":"DONE"}`)
	if got := agyExecutedModel(home, "native"); got != "" {
		t.Fatalf("a reply from before the latest prompt counted: %q", got)
	}
	write(`{"step_index":0,"type":"USER_INPUT","status":"DONE"}`,
		`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","model":"no-such-model-high"}`)
	if got := outcome.ExecutedModel("native"); got != "" {
		t.Fatalf("an unpriced model was reported: %q", got)
	}
	if got := outcome.ExecutedModel("../native"); got != "" {
		t.Fatalf("a session id escaped the folder: %q", got)
	}
}
