package musecli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A `muse serve` subagent item (fields as Muse sends them: revisions with the
// child session appearing later, then resultReady) gives the chat one started
// and one completed helper row, and the row opens the child's own log.
func TestMuseMSPSubagentBecomesAHelperRowThatOpensItsChat(t *testing.T) {
	dataHome := t.TempDir()
	parentDir := filepath.Join(dataHome, "muse", "sessions", "2026", "11", "28", "parent-1")
	childDir := filepath.Join(parentDir, "subagent", "child-1")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "session.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "session.jsonl"), []byte(museIntakeRow(1, time.Now().UnixMicro(), "Reply with exactly: Hello")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var rows []llmtypes.NativeRecord
	SetMSPHelperListener(func(owner string, r llmtypes.NativeRecord) {
		if owner == "chat-sub" {
			rows = append(rows, r)
		}
	})
	defer SetMSPHelperListener(nil)
	s := &museMSPSession{owner: "chat-sub", sessionID: "parent-1", dataHome: dataHome}
	send := func(method, item string) {
		params, _ := json.Marshal(map[string]any{"sessionId": "parent-1", "item": json.RawMessage(item)})
		observeMuseMSPSubagent(s, museRPCMessage{Method: method, Params: params})
	}
	base := `"itemId":"item-1","kind":"subagent","turnId":"turn-1","subagentId":"subagent-1","role":"hello agent","objective":"Reply with exactly: Hello"`
	send("item/started", `{`+base+`,"status":"inProgress","controlStatus":"accepted"}`)
	if _, ok := MuseSubagentConversation("chat-sub", "item-1"); ok {
		t.Fatal("conversation opened before Muse named the child session")
	}
	send("item/updated", `{`+base+`,"status":"inProgress","controlStatus":"running","childSessionId":"child-1"}`)
	send("item/updated", `{`+base+`,"status":"inProgress","controlStatus":"running","childSessionId":"child-1"}`)
	child, ok := MuseSubagentConversation("chat-sub", "item-1")
	if !ok || child.Status != "running" || len(child.Messages) == 0 || child.ToolUseID != "item-1" {
		t.Fatalf("running child = %+v ok=%v", child, ok)
	}
	send("item/updated", `{`+base+`,"status":"inProgress","controlStatus":"resultReady","childSessionId":"child-1","durationMs":1810}`)
	send("item/updated", `{`+base+`,"status":"inProgress","controlStatus":"running","childSessionId":"child-1"}`)
	if len(rows) != 2 || rows[0].State != "started" || rows[1].State != "completed" || rows[1].ToolID != "item-1" || rows[1].TaskID != "subagent-1" || rows[1].DurationMS != 1810 {
		t.Fatalf("rows = %+v", rows)
	}
	if child, _ := MuseSubagentConversation("chat-sub", "item-1"); child.Status != "completed" {
		t.Fatalf("finished child status = %q", child.Status)
	}
	if _, ok := MuseSubagentConversation("another-chat", "item-1"); ok {
		t.Fatal("another chat opened this chat's helper")
	}
}
