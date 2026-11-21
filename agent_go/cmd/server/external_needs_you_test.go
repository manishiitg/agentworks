package server

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// The MCP inbox shows a paused agent's question only to people who may answer
// it, and answering it from MCP reaches the paused chat.
func TestExternalNeedsYouListsAndAnswersOnlyYourQuestions(t *testing.T) {
	f := newExternalToolsFixture(t)
	const session = "owner-invoices-chat"
	f.api.activeSessions = map[string]*ActiveSessionInfo{session: {SessionID: session, UserID: "owner", WorkspacePath: "Workflow/invoices"}}
	id := "hf-" + uuid.NewString()
	store := virtualtools.GetHumanFeedbackStore()
	if err := store.CreatePendingRequest(id, "Send 40 reminder emails?", "", session, []string{"Send", "Skip today"}, false, time.Minute); err != nil {
		t.Fatal(err)
	}
	if w := f.call(t, "outsider", "list_needs_you", map[string]any{}); w.Code != 200 || strings.Contains(w.Body.String(), id) {
		t.Fatalf("another user saw the question: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "outsider", "answer_needs_you", map[string]any{"id": id, "option": "Send"}); w.Code != 404 {
		t.Fatalf("another user answered: %d %s", w.Code, w.Body)
	}
	w := f.call(t, "owner", "list_needs_you", map[string]any{"workflow_id": "invoices"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), id) || !strings.Contains(w.Body.String(), `"type":"question"`) {
		t.Fatalf("owner inbox: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "owner", "answer_needs_you", map[string]any{"id": id, "option": "Send"}); w.Code != 200 {
		t.Fatalf("owner answer: %d %s", w.Code, w.Body)
	}
	if response, ok := store.GetResponse(id); !ok || response != "Send" {
		t.Fatalf("paused chat got %q (%v)", response, ok)
	}
}
