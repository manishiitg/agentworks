package codexcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestReadRetainedTurnMessagesWaitsForCommittedFinalAnswer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	workingDir := filepath.Join(t.TempDir(), "workflow")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatalf("mkdir working dir: %v", err)
	}
	dayDir := filepath.Join(home, ".codex", "sessions", "2026", "08", "14")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}

	ownerSessionID := "retained-commentary-regression"
	oldRegistry := codexPersistentRegistry.Replace(map[string]*codexInteractiveSession{
		ownerSessionID: {ownerSessionID: ownerSessionID, workingDir: workingDir},
	})
	t.Cleanup(func() { codexPersistentRegistry.Replace(oldRegistry) })

	turnStart := time.Now().UTC().Add(-time.Second)
	commentaryTime := turnStart.Add(200 * time.Millisecond).Format(time.RFC3339Nano)
	finalTime := turnStart.Add(2 * time.Second).Format(time.RFC3339Nano)
	rollout := filepath.Join(dayDir, "rollout-2026-08-14T20-46-18-11111111-1111-4111-8111-111111111111.jsonl")
	base := []string{
		fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q}}`, workingDir),
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"I am checking the Pulse controls."}]}}`, commentaryTime),
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"agent_message","message":"I am checking the Pulse controls."}}`, commentaryTime),
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"function_call","name":"read_skill","arguments":"{}","call_id":"call-1"}}`, commentaryTime),
	}
	if err := os.WriteFile(rollout, []byte(strings.Join(base, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write in-progress rollout: %v", err)
	}
	mtime := turnStart.Add(time.Second)
	if err := os.Chtimes(rollout, mtime, mtime); err != nil {
		t.Fatalf("set rollout mtime: %v", err)
	}

	if got := ReadRetainedTurnMessages(ownerSessionID, turnStart); len(got) != 0 {
		t.Fatalf("in-progress commentary was treated as completion: %+v", got)
	}
	progress := ReadRetainedTurnProgressMessages(ownerSessionID, turnStart)
	if len(progress) != 1 || progress[0].Parts[0].(llmtypes.TextContent).Text != "I am checking the Pulse controls." {
		t.Fatalf("missing live progress: %+v", progress)
	}
	if got := ReadRetainedTurnProgressMessages(ownerSessionID, turnStart); len(got) != 0 {
		t.Fatalf("repeated progress: %+v", got)
	}

	completed := append(base,
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Pulse is currently off."}]}}`, finalTime),
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Pulse is currently off."}}`, finalTime),
	)
	if err := os.WriteFile(rollout, []byte(strings.Join(completed, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write completed rollout: %v", err)
	}
	if err := os.Chtimes(rollout, turnStart.Add(3*time.Second), turnStart.Add(3*time.Second)); err != nil {
		t.Fatalf("advance rollout mtime: %v", err)
	}

	got := ReadRetainedTurnMessages(ownerSessionID, turnStart)
	if len(got) != 1 || got[0].Role != llmtypes.ChatMessageTypeAI || len(got[0].Parts) != 1 {
		t.Fatalf("completed retained response = %+v", got)
	}
	text, ok := got[0].Parts[0].(llmtypes.TextContent)
	if !ok || text.Text != "Pulse is currently off." {
		t.Fatalf("completed retained response text = %#v", got[0].Parts[0])
	}
	progress = ReadRetainedTurnProgressMessages(ownerSessionID, turnStart)
	if len(progress) != 1 || progress[0].Parts[0].(llmtypes.TextContent).Text != text.Text {
		t.Fatalf("missing final flush: %+v", progress)
	}
	if got := ReadRetainedTurnProgressMessages(ownerSessionID, turnStart.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("follow-up replayed previous turn: %+v", got)
	}
}
