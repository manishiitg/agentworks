package codexcli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// #3245: a helper's running turn is the one its rollout started and has not
// finished; a finished helper has none, so it cannot be stopped.
func TestCodexRolloutOpenTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	write := func(rows string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(rows), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"type":"session_meta","payload":{"id":"child"}}
{"type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}
{"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":"done"}}
{"type":"event_msg","payload":{"type":"task_started","turn_id":"t2"}}
{"type":"response_item","payload":{"type":"message","content":"working on task_started things"}}
`)
	if got := codexRolloutOpenTurn(path); got != "t2" {
		t.Fatalf("open turn = %q, want t2", got)
	}
	write(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}
{"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"t1"}}
`)
	if got := codexRolloutOpenTurn(path); got != "" {
		t.Fatalf("aborted turn still open: %q", got)
	}
	write(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}
{"type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted"}}
`)
	if got := codexRolloutOpenTurn(path); got != "" {
		t.Fatalf("turn aborted without its id still open: %q", got)
	}
	if codexRolloutOpenTurn(filepath.Join(t.TempDir(), "missing.jsonl")) != "" {
		t.Fatal("a missing rollout has an open turn")
	}
	if CodexSubagentCanStop("no-chat", "spawn-x") {
		t.Fatal("a chat without Codex can stop a helper")
	}
	if err := InterruptCodexSubagent(context.Background(), "no-chat", "spawn-x"); err == nil {
		t.Fatal("interrupted a helper of a chat without Codex")
	}
}
