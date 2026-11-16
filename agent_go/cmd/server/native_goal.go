package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
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
	if req.RequestedSkillID != "" {
		known = false
		err = nil
	} // Slash text is this explicit skill's arguments.
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
					log.Printf("[AGENT_GOAL] session=%s action=resumed reason=app_restarted", sessionID)
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
	if !nativeGoalSupported(provider) || !isToolBackedChatMode(req.AgentMode) {
		log.Printf("[AGENT_GOAL] session=%s provider=%s action=skipped status=unavailable reason=unsupported_program", sessionID, provider)
		api.answerNativeGoalCommand(w, sessionID, "Goal mode is not available for this AI program")
		return r, true
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
	if command.Action == "resume" && !trustedRestore {
		// A finished goal has nothing to resume: say so in the chat instead of starting a turn that cannot do it
		// (it left the message queued as "Delivery unconfirmed", GitHub #2845).
		if previous, readErr := api.latestNativeGoal(r.Context(), sessionID); readErr == nil {
			message, outcome := "", ""
			switch {
			case previous == nil || previous.Goal == nil:
				message, outcome = "There is no goal to resume. Set one with /goal and what to do.", "no_goal"
			case previous.Goal.Status == "complete":
				message, outcome = "This goal is already done. Type the next task or set a new goal.", "already_done"
			}
			if message != "" {
				log.Printf("[AGENT_GOAL] session=%s event=command command=resume outcome=%s%s", sessionID, outcome, nativeGoalCallerLog(r.Context()))
				api.answerNativeGoalCommand(w, sessionID, message)
				return r, true
			}
		}
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
		log.Printf("[AGENT_GOAL] session=%s action=started reason=%s%s", sessionID, nativeGoalOrigin(r.Context(), command.Action), nativeGoalCallerLog(r.Context()))
		update, err := controlNativeGoal(r.Context(), sessionID, command.Action)
		if err != nil {
			log.Printf("[AGENT_GOAL] session=%s action=failed status=unknown reason=control_failed", sessionID)
			http.Error(w, err.Error(), http.StatusConflict)
			return r, true
		}
		if err := api.projectNativeGoal(sessionID, *update, nativeGoalOrigin(r.Context(), command.Action)); err != nil {
			http.Error(w, "Could not save goal state", http.StatusInternalServerError)
			return r, true
		}

		log.Printf("[AGENT_GOAL] session=%s action=done reason=%s%s", sessionID, nativeGoalOrigin(r.Context(), command.Action), nativeGoalCallerLog(r.Context()))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "session_id": sessionID})
		return r, true
	}
	req.DisableLiveInputDelivery = true
	log.Printf("[AGENT_GOAL] session=%s event=command command=%s outcome=started%s", sessionID, command.Action, nativeGoalCallerLog(r.Context()))
	log.Printf("[AGENT_GOAL] session=%s action=started reason=%s%s", sessionID, nativeGoalOrigin(r.Context(), command.Action), nativeGoalCallerLog(r.Context()))
	return r.WithContext(llmtypes.WithNativeGoalCommand(r.Context(), command)), false
}

// answerNativeGoalCommand answers a goal command that needs no turn: one line in the chat, and a handled result so a
// queued command is cleared instead of left as an undelivered message.
func (api *StreamingAPI) answerNativeGoalCommand(w http.ResponseWriter, sessionID, message string) {
	event := storeevents.Event{ID: fmt.Sprintf("native-goal-notice:%d", time.Now().UnixNano()), Type: "native_goal_notice", SessionID: sessionID, Timestamp: time.Now(), ExecutionID: "main:" + sessionID, ParentExecutionID: "session:" + sessionID, ExecutionKind: "main_agent", Data: unifiedevents.NewAgentEvent(&unifiedevents.GenericEventData{Data: map[string]interface{}{"message": message}})}
	if api.eventStore != nil {
		api.eventStore.AddEvent(sessionID, event)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "notice", "session_id": sessionID, "notice": event, "message": message})
}

// Program support is a backend fact, shared by admission and composer metadata.
func nativeGoalSupported(provider string) bool { return provider == "codex-cli" }
func nativeGoalProgramCapabilities() map[string]bool {
	out := make(map[string]bool, len(supportedLLMProviders))
	for _, provider := range supportedLLMProviders {
		out[provider] = nativeGoalSupported(provider)
	}
	return out
}
