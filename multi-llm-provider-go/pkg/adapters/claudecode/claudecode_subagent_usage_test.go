package claudecode

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeSubagentCallsAreReportedOnceAndOnlyAfterTurnStart(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "sess.jsonl")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(dir, "sess", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	row := func(id string, at time.Time, in int) string {
		return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":%q,"model":"claude-sonnet-5","usage":{"input_tokens":%d,"output_tokens":7,"cache_read_input_tokens":3}}}`+"\n", at.UTC().Format(time.RFC3339Nano), id, in)
	}
	body := row("old", start.Add(-time.Hour), 999) + row("m1", start.Add(time.Second), 100) + row("m1", start.Add(time.Second), 100) + row("m2", start.Add(2*time.Second), 50)
	if err := os.WriteFile(filepath.Join(subDir, "agent-x.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := claudeSubagentCalls(parent, start)
	if len(got) != 2 || got[0].ID != "m1" || got[0].Input != 100 || got[1].ID != "m2" || got[0].Model != "claude-sonnet-5" {
		t.Fatalf("first read = %+v, want m1 and m2 once each, the call before the turn skipped", got)
	}
	if again := claudeSubagentCalls(parent, start); len(again) != 0 {
		t.Fatalf("second read = %+v, want nothing: calls are reported once", again)
	}
}
