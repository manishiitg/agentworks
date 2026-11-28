package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

// nativeSubagentConversation finds the child a chat's Claude (Agent call),
// Codex (spawnAgent call), Cursor (task call) or Muse (subagent item) started with toolCallID.
var nativeSubagentConversation = func(owner, toolCallID string) (llmtypes.NativeSubagentTranscript, bool) {
	if child, ok := claudecode.ClaudeSubagentConversation(owner, toolCallID); ok {
		return child, true
	}
	if child, ok := codexcli.CodexSubagentConversation(owner, toolCallID); ok {
		return child, true
	}
	if child, ok := cursorcli.CursorSubagentConversation(owner, toolCallID); ok {
		return child, true
	}
	return musecli.MuseSubagentConversation(owner, toolCallID)
}

// GET /api/sessions/{session_id}/subagents/{tool_call_id} returns, as it is
// now, the conversation of the subagent this chat's agent (Claude, Codex or Cursor) started with that
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
		child, ok = api.agySubagentConversation(sessionID, toolCallID)
	}
	if !ok {
		log.Printf("[SUBAGENT_CHAT] session=%s tool_call=%s action=missing", sessionID, toolCallID)
		http.Error(w, "This helper's conversation is not available. It may have ended before the app restarted.", http.StatusNotFound)
		return
	}
	transcript := orchestrator.NativeSubagentChat(sessionID, child)
	transcript.RedactSensitiveText()
	log.Printf("[SUBAGENT_CHAT] session=%s provider=%s tool_call=%s action=read status=%s messages=%d", sessionID, child.Provider, toolCallID, child.Status, len(child.Messages))
	w.Header().Set("Content-Type", "application/json")
	running := child.Status == "running"
	reply := map[string]any{"running": running, "transcript": transcript}
	if running && child.Provider == "codex-cli" && codexcli.CodexSubagentCanStop(sessionID, toolCallID) {
		// The chat's Stop for this one helper (#3245): background-work stop with this id.
		reply["stop_id"] = codexHelperWorkPrefix + toolCallID
	}
	_ = json.NewEncoder(w).Encode(reply)
}

// codexHelperWorkPrefix stops one helper agent a chat's Codex started, by its
// spawnAgent call, without stopping the chat (#3245).
const codexHelperWorkPrefix = "codex-helper:"

var interruptCodexSubagent = codexcli.InterruptCodexSubagent

func stopCodexHelperBackgroundWork(sessionID, execID string) bool {
	callID := strings.TrimPrefix(execID, codexHelperWorkPrefix)
	if callID == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := interruptCodexSubagent(ctx, sessionID, callID); err != nil {
		log.Printf("[SUBAGENT_CHAT] session=%s provider=codex-cli tool_call=%s action=stop_failed error=%v", sessionID, callID, err)
		return false
	}
	log.Printf("[SUBAGENT_CHAT] session=%s provider=codex-cli tool_call=%s action=stopped", sessionID, callID)
	return true
}

// agySubagentConversation finds a subagent this chat's Antigravity started. agy names its helper rows by the
// child's own conversation id, and keeps the child's conversation next to the chat's own (#3970).
func (api *StreamingAPI) agySubagentConversation(sessionID, childID string) (llmtypes.NativeSubagentTranscript, bool) {
	api.nativeObserversMu.Lock()
	o := api.nativeObservers[sessionID]
	api.nativeObserversMu.Unlock()
	if o == nil || o.provider != "agy-cli" || o.nativeID == "" {
		return llmtypes.NativeSubagentTranscript{}, false
	}
	path := o.nativeRecordPath()
	suffix := filepath.Join(".gemini", "antigravity-cli", "conversations", o.nativeID+".db")
	home, found := strings.CutSuffix(path, string(filepath.Separator)+suffix)
	if !found {
		return llmtypes.NativeSubagentTranscript{}, false
	}
	return agySubagentConversationIn(o.nativeID, childID, home)
}

var agySubagentConversationIn = agycli.AgySubagentConversation
