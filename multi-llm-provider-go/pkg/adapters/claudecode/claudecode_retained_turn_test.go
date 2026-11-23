package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestReadRetainedTurnMessagesWaitsForEndTurn reproduces the Sales Outreach
// regression captured on 2026-08-19: Claude emitted useful narration on a
// tool_use message, ran several tools, and committed the real answer later.
// The retained-session completion poller must see no final response until the
// provider-native end_turn row exists.
func TestReadRetainedTurnMessagesWaitsForEndTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workingDir := filepath.Join(t.TempDir(), "salesoutreach")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatalf("mkdir working dir: %v", err)
	}

	const (
		ownerSessionID  = "sales-outreach-retained-regression"
		nativeSessionID = "87de1833-360c-4df8-9bfb-db7d10b014ad"
	)
	projectDir := filepath.Join(home, ".claude", "projects", "-captured-salesoutreach")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir Claude project dir: %v", err)
	}
	transcript := filepath.Join(projectDir, nativeSessionID+".jsonl")

	oldRegistry := claudeInteractivePersistentRegistry.Replace(map[string]*claudeInteractivePersistentSession{
		ownerSessionID: {
			ownerSessionID:  ownerSessionID,
			nativeSessionID: nativeSessionID,
			workingDir:      workingDir,
		},
	})
	t.Cleanup(func() { claudeInteractivePersistentRegistry.Replace(oldRegistry) })

	turnStart := time.Date(2026, 8, 19, 5, 45, 40, 0, time.UTC)
	inProgress := []string{
		`{"type":"assistant","timestamp":"2026-08-19T05:45:45.066Z","message":{"id":"msg_tool_1","stop_reason":"tool_use","content":[{"type":"text","text":"Let me check what sequence the workflow actually defines."}]}}`,
		`{"type":"assistant","timestamp":"2026-08-19T05:45:46.482Z","message":{"id":"msg_tool_1","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__api-bridge__execute_shell_command","input":{}}]}}`,
		`{"type":"user","timestamp":"2026-08-19T05:45:46.570Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}}`,
		`{"type":"assistant","timestamp":"2026-08-19T05:45:55.910Z","message":{"id":"msg_tool_2","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_2","name":"mcp__api-bridge__execute_shell_command","input":{}}]}}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(inProgress, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write in-progress transcript: %v", err)
	}

	if got := ReadRetainedTurnMessages(ownerSessionID, turnStart); len(got) != 0 {
		t.Fatalf("tool_use narration was treated as completion: %+v", got)
	}
	progress := ReadRetainedTurnProgressMessages(ownerSessionID, turnStart)
	if len(progress) != 1 || progress[0].Parts[0].(llmtypes.TextContent).Text != "Let me check what sequence the workflow actually defines." {
		t.Fatalf("missing live narration while the final answer is pending: %+v", progress)
	}
	if got := ReadRetainedTurnProgressMessages(ownerSessionID, turnStart); len(got) != 0 {
		t.Fatalf("poll replayed narration: %+v", got)
	}

	completed := append(inProgress,
		`{"type":"assistant","timestamp":"2026-08-19T05:46:01.930Z","message":{"id":"msg_final","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"done"}]}}`,
		`{"type":"assistant","timestamp":"2026-08-19T05:46:10.058Z","message":{"id":"msg_final","stop_reason":"end_turn","content":[{"type":"text","text":"Good instinct — and yes, that's the right sequence."}]}}`,
	)
	if err := os.WriteFile(transcript, []byte(strings.Join(completed, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write completed transcript: %v", err)
	}

	got := ReadRetainedTurnMessages(ownerSessionID, turnStart)
	if len(got) != 1 || got[0].Role != llmtypes.ChatMessageTypeAI || len(got[0].Parts) != 1 {
		t.Fatalf("completed retained response = %+v", got)
	}
	text, ok := got[0].Parts[0].(llmtypes.TextContent)
	if !ok || text.Text != "Good instinct — and yes, that's the right sequence." {
		t.Fatalf("completed retained response text = %#v", got[0].Parts[0])
	}
	progress = ReadRetainedTurnProgressMessages(ownerSessionID, turnStart)
	if len(progress) != 1 || progress[0].Parts[0].(llmtypes.TextContent).Text != text.Text {
		t.Fatalf("final flush lost the newly committed message: %+v", progress)
	}
	// A follow-up watcher starts after the previous turn, so old narration
	// must not be replayed even though it reads the same native transcript.
	if got := ReadRetainedTurnProgressMessages(ownerSessionID, turnStart.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("new turn replayed previous messages: %+v", got)
	}

	// PLAT-872: the CLI queues a new user immediately after end_turn. The
	// earlier final must not close the follow-up before its native tools run.
	queued := append(completed,
		`{"type":"user","timestamp":"2026-08-19T05:46:10.100Z","message":{"content":"Check the source too."}}`,
	)
	write := func(rows []string) {
		t.Helper()
		if err := os.WriteFile(transcript, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(queued)
	if got := ReadRetainedTurnMessages(ownerSessionID, turnStart); len(got) != 0 {
		t.Fatalf("queued user closed on the previous answer: %+v", got)
	}
	queued = append(queued, `{"type":"assistant","timestamp":"2026-08-19T05:46:15Z","message":{"id":"followup-tool","stop_reason":"tool_use","content":[{"type":"tool_use","id":"queued-fetch","name":"WebFetch","input":{}}]}}`)
	write(queued)
	if got := ReadRetainedTurnMessages(ownerSessionID, turnStart); len(got) != 0 {
		t.Fatalf("queued tool closed on the previous answer: %+v", got)
	}
	queued = append(queued,
		`{"type":"user","timestamp":"2026-08-19T05:46:20Z","message":{"content":[{"type":"tool_result","tool_use_id":"queued-fetch","content":"source"}]}}`,
		`{"type":"assistant","timestamp":"2026-08-19T05:46:25Z","message":{"id":"followup-final","stop_reason":"end_turn","content":[{"type":"text","text":"Source checked."}]}}`,
	)
	write(queued)
	if got := ReadRetainedTurnMessages(ownerSessionID, turnStart); len(got) != 1 || got[0].Parts[0].(llmtypes.TextContent).Text != "Source checked." {
		t.Fatalf("queued final = %+v", got)
	}
}
