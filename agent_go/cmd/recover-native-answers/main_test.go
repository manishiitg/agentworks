package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
)

// PLAT-872: answers to steers inside a cancelled turn were missing from the
// saved history while the person kept chatting. The repair puts each answer
// right after its steer, keeps newer records, ignores tool-call arguments, and
// a second run adds nothing.
func TestRecoverNativeAnswersInsertsAtTheirSteers(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-10-29T05:08:00Z","message":{"role":"user","content":"start the app"}}`,
		`{"type":"assistant","uuid":"a0","timestamp":"2026-10-29T05:08:30Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"echo The app runs on port 3000."}}],"stop_reason":"tool_use"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-10-29T05:09:00Z","message":{"role":"assistant","content":[{"type":"text","text":"The app runs on port 3000."}],"stop_reason":"end_turn"}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-10-29T05:10:00Z","message":{"role":"user","content":"use port 4000 instead"}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-10-29T05:11:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Moved it to port 4000."}],"stop_reason":"end_turn"}}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conversation := filepath.Join(dir, "builder", "conversation", "2026-10-29", "session-x-conversation.json")
	if err := os.MkdirAll(filepath.Dir(conversation), 0o700); err != nil {
		t.Fatal(err)
	}
	history := `[{"Role":"system","Parts":[{"Text":"sys"}]},` +
		`{"Role":"human","Parts":[{"Text":"start the app"}]},` +
		`{"Role":"ai","Parts":[{"ToolCall":{"FunctionCall":{"Arguments":"The app runs on port 3000."}}}]},` +
		`{"Role":"human","Parts":[{"Text":"use port 4000 instead"}]},` +
		`{"Role":"human","Parts":[{"Text":"a newer message"}]},` +
		`{"Role":"ai","Parts":[{"Text":"a newer answer"}]}]`
	if err := chatlog.Save(conversation, []byte(`{"session_id":"x","conversation_history":`+history+`}`)); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run(transcript, conversation, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "to insert: 2, not placed: 0, history records: 6 -> 8") {
		t.Fatalf("dry run:\n%s", out.String())
	}
	if err := run(transcript, conversation, true, &out); err != nil {
		t.Fatal(err)
	}
	content, err := chatlog.Load(conversation)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		History []historyRow `json:"conversation_history"`
	}
	if err := json.Unmarshal(content, &record); err != nil {
		t.Fatal(err)
	}
	var shape []string
	for _, row := range record.History {
		raw, _ := json.Marshal(row)
		_, texts := rowTexts(raw)
		shape = append(shape, row.Role+":"+strings.Join(texts, ""))
	}
	want := "system:sys | human:start the app | ai: | ai:The app runs on port 3000. | human:use port 4000 instead | ai:Moved it to port 4000. | human:a newer message | ai:a newer answer"
	if got := strings.Join(shape, " | "); got != want {
		t.Fatalf("history after repair:\n got %s\nwant %s", got, want)
	}

	out.Reset()
	if err := run(transcript, conversation, true, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already saved: 2, to insert: 0") {
		t.Fatalf("second run:\n%s", out.String())
	}
}
