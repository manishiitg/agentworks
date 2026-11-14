package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func parseNativeGoalCommand(query, sessionID string) (llmtypes.NativeGoalCommand, bool, error) {
	text := strings.TrimSpace(query)
	head, rest := text, ""
	if boundary := strings.IndexFunc(text, unicode.IsSpace); boundary >= 0 {
		head, rest = text[:boundary], text[boundary:]
	}
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
func (api *StreamingAPI) prepareNativeGoalQuery(w http.ResponseWriter, r *http.Request, req *QueryRequest, sessionID, provider string, allowDirectControl, builder bool) (*http.Request, bool) {
	command, known, err := parseNativeGoalCommand(req.Query, sessionID)
	trustedRestore := false
	if trusted, ok := llmtypes.NativeGoalCommandForSession(r.Context(), sessionID); ok {
		command, known, trustedRestore = trusted, true, true
	}
	if !known {
		if previous, readErr := api.latestNativeGoal(r.Context(), sessionID); readErr == nil && previous != nil && previous.Provider == "codex-cli" && previous.Goal != nil && provider == "codex-cli" {
			// Restart continuation resumes the same goal, retaining its safe
			// continuation message. A normal cold turn keeps a paused goal paused.
			resumeAfterRestart := req.IsAutoNotification && req.TriggeredBy == "restart_continue" && previous.Goal.Status == "active"
			resumeAfterQuota := req.IsAutoNotification && req.TriggeredBy == usageLimitContinueTrigger && previous.Goal.Status == "usage_limited"
			if resumeAfterQuota && api.pendingNativeGoalRestore(r.Context(), sessionID) != nil {
				resumeAfterQuota = false
				r = r.WithContext(llmtypes.WithNativeGoalTurnPause(r.Context(), llmtypes.NativeGoalTurnPause{SessionID: sessionID, Reason: "background_wake"}))
			}
			if resumeAfterRestart || resumeAfterQuota {
				command = llmtypes.NativeGoalCommand{SessionID: sessionID, Action: "resume", Objective: previous.Goal.Objective}
				known, trustedRestore = true, true
				if resumeAfterRestart {
					r = r.WithContext(llmtypes.WithNativeGoalTurnPause(r.Context(), llmtypes.NativeGoalTurnPause{SessionID: sessionID, Reason: "app_restarted"}))
				}
			} else if (previous.Goal.Status == "paused" || (previous.Goal.Status == "active" && !api.sessionTurnRunning(sessionID))) && !(req.IsAutoNotification && (previous.Reason == "no_live_process" || previous.Reason == "native_pause_failed")) {
				reason := previous.Reason
				if !req.IsAutoNotification {
					reason = "person_message"
				}
				if reason == "" {
					reason = "native_goal_reconnect"
				}
				r = r.WithContext(llmtypes.WithNativeGoalTurnPause(r.Context(), llmtypes.NativeGoalTurnPause{SessionID: sessionID, Reason: reason}))
			}
		}
	}
	if !known || ((req.IsAutoNotification || req.AgentMessageInbox != "") && !trustedRestore) {
		return r, false
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return r, true
	}
	if command.Action != "show" {
		owner := ""
		if api.eventStore != nil {
			owner = api.eventStore.GetSessionOwner(sessionID)
		}
		actor := ""
		if claims := GetUserFromContext(r.Context()); claims != nil {
			actor = claims.UserID
		}
		if !builder || (owner != "" && owner != actor) {
			log.Printf("[AGENT_GOAL] session=%s action=skipped reason=goal_owner_required", sessionID)
			http.Error(w, "Only this chat's Builder can change its goal.", http.StatusForbidden)
			return r, true
		}
	}
	if !isToolBackedChatMode(req.AgentMode) {
		log.Printf("[AGENT_GOAL] session=%s action=skipped status=unavailable reason=unsupported_chat_mode", sessionID)
		http.Error(w, "Goal mode is available in Crew chats with a supported program.", http.StatusBadRequest)
		return r, true
	}
	if provider != "codex-cli" {
		log.Printf("[AGENT_GOAL] session=%s provider=%s action=skipped status=unavailable reason=unsupported_program", sessionID, provider)
		http.Error(w, "Goal mode is not available for this program.", http.StatusBadRequest)
		return r, true
	}
	if command.Action != "show" && !trustedRestore && allowDirectControl {
		api.cancelNativeGoalRestore(sessionID, "person_goal_command")
	}
	// A person must be able to pause/clear a running goal without waiting for
	// that goal to release the input lane. Use only the existing owned thread.
	if allowDirectControl && api.sessionTurnRunning(sessionID) && (command.Action == "show" || command.Action == "pause" || command.Action == "clear") {
		if err := api.verifySessionAccess(r, sessionID); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return r, true
		}
		log.Printf("[AGENT_GOAL] session=%s action=started reason=person_%s", sessionID, command.Action)
		update, err := controlNativeGoal(r.Context(), sessionID, command.Action)
		if err != nil {
			log.Printf("[AGENT_GOAL] session=%s action=failed status=unknown reason=control_failed", sessionID)
			http.Error(w, err.Error(), http.StatusConflict)
			return r, true
		}
		if err := api.projectNativeGoal(sessionID, *update, "person_"+command.Action); err != nil {
			http.Error(w, "Could not save goal state", http.StatusInternalServerError)
			return r, true
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
