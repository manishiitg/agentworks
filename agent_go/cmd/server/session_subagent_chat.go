package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
)

// nativeSubagentConversation finds the child a chat's Claude (Agent call) or
// Codex (spawnAgent call) started with toolCallID.
var nativeSubagentConversation = func(owner, toolCallID string) (llmtypes.NativeSubagentTranscript, bool) {
	if child, ok := claudecode.ClaudeSubagentConversation(owner, toolCallID); ok {
		return child, true
	}
	return codexcli.CodexSubagentConversation(owner, toolCallID)
}

// GET /api/sessions/{session_id}/subagents/{tool_call_id} returns, as it is
// now, the conversation of the subagent this chat's agent (Claude or Codex) started with that
// tool call (#3132). The chat's helper row opens it and reads it again while
// it runs. Only the chat's own user may read it.
func (api *StreamingAPI) handleGetSessionSubagent(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID, toolCallID := vars["session_id"], strings.TrimSpace(vars["tool_call_id"])
	if sessionID == "" || toolCallID == "" || !api.ownsChatSession(r, sessionID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	child, ok := nativeSubagentConversation(sessionID, toolCallID)
	if !ok {
		log.Printf("[SUBAGENT_CHAT] session=%s tool_call=%s action=missing", sessionID, toolCallID)
		http.Error(w, "This helper's conversation is not available. It may have ended before the app restarted.", http.StatusNotFound)
		return
	}
	transcript := orchestrator.NativeSubagentChat(sessionID, child)
	transcript.RedactSensitiveText()
	log.Printf("[SUBAGENT_CHAT] session=%s provider=%s tool_call=%s action=read status=%s messages=%d", sessionID, child.Provider, toolCallID, child.Status, len(child.Messages))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"running": child.Status == "running", "transcript": transcript})
}
