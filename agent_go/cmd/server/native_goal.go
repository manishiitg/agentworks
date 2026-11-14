package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
)

func parseNativeGoalCommand(query, sessionID string) (llmtypes.NativeGoalCommand, bool, error) {
	text := strings.TrimSpace(query)
	head, rest, _ := strings.Cut(text, " ")
	// Only the slash command is control input, not a quoted mention or prefix.
	if head != "/goal" {
		return llmtypes.NativeGoalCommand{}, false, nil
	}
	objective := strings.TrimSpace(rest)
	command := llmtypes.NativeGoalCommand{SessionID: sessionID, Action: "set", Objective: objective}
	switch objective {
	case "":
		command.Action = "show"
	case "pause", "resume", "clear":
		command.Action = objective
		command.Objective = ""
	}
	if command.Action == "set" && utf8.RuneCountInString(objective) > 4000 {
		return command, true, fmt.Errorf("The goal objective must be at most 4000 characters; the current goal was not changed")
	}
	return command, true, nil
}

// Called only after ordinary project, tab and account admission. Goal commands
// never become raw retained input, and unsupported programs never receive them
// as a model prompt. Queued commands are parsed again at their own admission.
func (api *StreamingAPI) prepareNativeGoalQuery(w http.ResponseWriter, r *http.Request, req *QueryRequest, sessionID, provider string) (*http.Request, bool) {
	command, known, err := parseNativeGoalCommand(req.Query, sessionID)
	if !known || req.IsAutoNotification || req.AgentMessageInbox != "" {
		return r, false
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return r, true
	}
	if provider != "codex-cli" {
		log.Printf("[AGENT_GOAL] session=%s provider=%s action=skipped status=unavailable reason=unsupported_program", sessionID, provider)
		http.Error(w, "Goal mode is not available for this program.", http.StatusBadRequest)
		return r, true
	}
	// A person must be able to pause/clear a running goal without waiting for
	// that goal to release the input lane. Use only the existing owned thread.
	if api.sessionTurnRunning(sessionID) && (command.Action == "show" || command.Action == "pause" || command.Action == "clear") {
		if err := api.verifySessionAccess(r, sessionID); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return r, true
		}
		goal, err := codexcli.ControlCodexNativeGoal(r.Context(), sessionID, command.Action)
		if err != nil {
			log.Printf("[AGENT_GOAL] session=%s action=failed status=unknown reason=control_failed", sessionID)
			http.Error(w, err.Error(), http.StatusConflict)
			return r, true
		}
		if api.eventStore != nil {
			api.eventStore.AddEvent(sessionID, storeevents.Event{ID: fmt.Sprintf("native-goal-control:%d", time.Now().UnixNano()), Type: string(unifiedevents.NativeGoalUpdated), SessionID: sessionID, Timestamp: time.Now(), Data: unifiedevents.NewAgentEvent(&unifiedevents.NativeGoalEvent{Update: llmtypes.NativeGoalUpdate{Provider: provider, Goal: goal, Reason: "person_" + command.Action}})})
		}
		log.Printf("[AGENT_GOAL] session=%s action=done reason=person_%s", sessionID, command.Action)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "session_id": sessionID})
		return r, true
	}
	req.DisableLiveInputDelivery = true
	log.Printf("[AGENT_GOAL] session=%s action=started reason=person_%s", sessionID, command.Action)
	return r.WithContext(llmtypes.WithNativeGoalCommand(r.Context(), command)), false
}
