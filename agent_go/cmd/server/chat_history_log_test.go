package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The person sees the turn as done as soon as the answer is complete; the
// history save (an append) comes after, not before (PLAT-856-521: the turn
// stayed "working" about 3 s while the whole chat was rewritten).
func TestTurnIsMarkedDoneBeforeItsHistoryIsSaved(t *testing.T) {
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{"s": {Status: "running"}}}
	statusAtSave := ""
	workspace.onAppend = func() {
		if session, ok := api.getActiveSession("s"); ok {
			statusAtSave = session.Status
		}
	}
	history := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hi"}}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hello"}}},
	}
	status, completed := settleTurnThenSave(func() (string, bool) {
		api.updateSessionStatus("s", "completed")
		return "completed", true
	}, func() {
		api.persistChatConversationToPathWithTerminalSession("s", "", "multi-agent", "alice", "q1", history, nil, nil, "Crew/demo/builder/conversation/session-s/header.json")
	})
	if status != "completed" || !completed {
		t.Fatalf("settled = %q %v", status, completed)
	}
	if statusAtSave != "completed" {
		t.Fatalf("status when the history was saved = %q, want completed", statusAtSave)
	}
}

// A shared agent's reader polls every half second: each poll reads only the
// turn lines after its position, skips lines another sender started, and still
// gets a turn whole (PLAT-856-521, PLAT-856-509).
func TestChatEventWindowReadsOnlyNewLinesOfThisSender(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	dir := filepath.Join(root, filepath.FromSlash(chatHistoryRoot("alice")), "session-s")
	event := func(seq int, kind, sender string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"id":"e%d","sequence":%d,"type":%q,"data":{"data":{"metadata":{"turn_id":"t%d","sender_user_id":%q}}}}`, seq, seq, kind, (seq+1)/2, sender))
	}
	for i, sender := range []string{"a", "c", "a"} {
		events := []json.RawMessage{event(2*i+1, "user_message", sender), event(2*i+2, "unified_completion", "")}
		turn := chatlog.Turn{ID: fmt.Sprintf("q%d", i+1), At: fmt.Sprintf("2026-11-04T10:0%d:00Z", i), SenderUserID: chatlog.TurnSender(events), UIEvents: events,
			Messages: []json.RawMessage{json.RawMessage(fmt.Sprintf(`{"Role":"human","Parts":[{"Text":"question number %d"}]}`, i+1))}}
		if _, err := chatlog.AppendTurns(dir, []chatlog.Turn{turn}, map[string]json.RawMessage{"session_id": json.RawMessage(`"s"`), "user_id": json.RawMessage(`"alice"`)}, chatlog.ModeReplace); err != nil {
			t.Fatal(err)
		}
	}
	seqs := func(after int) []int {
		t.Helper()
		events, ok, err := chatEventWindow("alice", "s", "", after, "a")
		if err != nil || !ok {
			t.Fatalf("window after %d: ok=%v err=%v", after, ok, err)
		}
		var out []int
		for _, raw := range events {
			var head struct {
				Sequence int `json:"sequence"`
			}
			_ = json.Unmarshal(raw, &head)
			out = append(out, head.Sequence)
		}
		return out
	}
	if got := fmt.Sprint(seqs(4)); got != "[5 6]" {
		t.Fatalf("after 4 = %s, want only the newest line", got)
	}
	if got := fmt.Sprint(seqs(2)); got != "[5 6]" {
		t.Fatalf("after 2 = %s, want c's line skipped", got)
	}
	if got := fmt.Sprint(seqs(6)); got != "[]" {
		t.Fatalf("after 6 = %s, want nothing new", got)
	}
	if _, err := os.Stat(filepath.Join(dir, chatlog.TurnsFile)); err != nil {
		t.Fatal(err)
	}
}
