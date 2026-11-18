package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

var claudeSubagentConversation = claudecode.ClaudeSubagentConversation

// GET /api/sessions/{session_id}/subagents/{tool_call_id} returns, as it is
// now, the conversation of the subagent this chat's agent started with that
// tool call (#3132). The chat's helper row opens it and reads it again while
// it runs. Only the chat's own user may read it.
func (api *StreamingAPI) handleGetSessionSubagent(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID, toolCallID := vars["session_id"], strings.TrimSpace(vars["tool_call_id"])
	if sessionID == "" || toolCallID == "" || !api.ownsChatSession(r, sessionID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	child, ok := claudeSubagentConversation(sessionID, toolCallID)
	if !ok {
		log.Printf("[SUBAGENT_CHAT] session=%s tool_call=%s action=missing", sessionID, toolCallID)
		http.Error(w, "This helper's conversation is not available. It may have ended before the app restarted.", http.StatusNotFound)
		return
	}
	transcript := orchestrator.NativeSubagentChat(sessionID, child)
	transcript.RedactSensitiveText()
	log.Printf("[SUBAGENT_CHAT] session=%s tool_call=%s action=read status=%s messages=%d", sessionID, toolCallID, child.Status, len(child.Messages))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"running": child.Status == "running", "transcript": transcript})
}
