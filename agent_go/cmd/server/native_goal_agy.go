package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const (
	agyGoalProvider    = "agy-cli"
	claudeGoalProvider = "claude-code"
)

// turnGoalProvider: a program whose goal runs as one /goal turn, with its state kept here (Antigravity, and Claude
// Code, whose goal checks continue its turn until the goal is met, #4626).
func turnGoalProvider(provider string) bool {
	return provider == agyGoalProvider || provider == claudeGoalProvider
}

func turnGoalProgram(provider string) string {
	if provider == claudeGoalProvider {
		return "Claude Code"
	}
	return "Antigravity"
}

// prepareAgyGoalCommand handles an Antigravity chat's /goal command (#4604). agy keeps working on `/goal <objective>`
// until it is finished, but has no pause, resume or goal read. The goal's state is the chat's own goal record:
// active while the /goal turn runs, complete when it ends, paused when it is stopped. show, pause and clear are
// answered here and never reach agy; resume (Start again) sends the stored objective again. It returns true when
// it answered; false leaves a set or resume to run as the turn.
func (api *StreamingAPI) prepareAgyGoalCommand(w http.ResponseWriter, r *http.Request, sessionID, provider string, command *llmtypes.NativeGoalCommand, allowDirectControl bool) bool {
	previous, _ := api.latestNativeGoal(r.Context(), sessionID)
	if previous != nil && (previous.Goal == nil || previous.Provider != provider) {
		previous = nil
	}
	running := previous != nil && previous.Goal.Status == "active" && api.sessionTurnRunning(sessionID)
	switch command.Action {
	case "show":
		if previous == nil {
			api.answerNativeGoalCommand(w, sessionID, "No goal is set. Set one with /goal and what to do.")
		} else {
			api.answerNativeGoalCommand(w, sessionID, "Goal ("+agyGoalStatusLabel(previous.Goal.Status)+"): "+previous.Goal.Objective)
		}
		return true
	case "pause":
		if !running || !allowDirectControl {
			api.answerNativeGoalCommand(w, sessionID, turnGoalProgram(provider)+" can't pause a goal. Stop ends it; Start again picks it up later.")
			return true
		}
		if err := api.verifySessionAccess(r, sessionID); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return true
		}
		api.cancelAgyGoalTurn(sessionID, provider)
		api.stopAgyGoal(sessionID, previous)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "session_id": sessionID})
		return true
	case "clear":
		if previous == nil {
			api.answerNativeGoalCommand(w, sessionID, "There is no goal to clear.")
			return true
		}
		if running && allowDirectControl {
			api.cancelAgyGoalTurn(sessionID, provider)
		}
		if err := api.projectNativeGoal(sessionID, llmtypes.NativeGoalUpdate{Provider: provider, NativeSessionID: previous.NativeSessionID}, "person_clear"); err != nil {
			http.Error(w, "Could not save goal state", http.StatusInternalServerError)
			return true
		}
		api.answerNativeGoalCommand(w, sessionID, "Goal cleared.")
		return true
	case "resume":
		switch {
		case previous == nil:
			api.answerNativeGoalCommand(w, sessionID, "There is no goal to start again. Set one with /goal and what to do.")
			return true
		case previous.Goal.Status == "complete":
			api.answerNativeGoalCommand(w, sessionID, "This goal is already done. Type the next task or set a new goal.")
			return true
		case running:
			api.answerNativeGoalCommand(w, sessionID, turnGoalProgram(provider)+" is already working on this goal.")
			return true
		}
		command.Objective = previous.Goal.Objective
	}
	return false
}

// stopAgyGoal records a running agy goal as stopped (paused); Start again sends it again.
func (api *StreamingAPI) stopAgyGoal(sessionID string, previous *llmtypes.NativeGoalUpdate) {
	if previous == nil || previous.Goal == nil || previous.Goal.Status != "active" {
		return
	}
	update := *previous
	goal := *previous.Goal
	goal.Status = "paused"
	update.Goal = &goal
	_ = api.projectNativeGoal(sessionID, update, "person_stop")
}

// cancelAgyGoalTurn ends the chat's running /goal turn the way the chat's Stop does.
func (api *StreamingAPI) cancelAgyGoalTurn(sessionID, provider string) {
	api.agentCancelMux.Lock()
	_, hasGoTurn := api.agentCancelFuncs[sessionID]
	api.agentCancelMux.Unlock()
	api.interruptInteractiveCLITurn(sessionID, hasGoTurn || api.retainedMainTurnTracked(sessionID))
	api.agentCancelMux.Lock()
	if cancel, ok := api.agentCancelFuncs[sessionID]; ok {
		cancel()
		delete(api.agentCancelFuncs, sessionID)
	}
	api.agentCancelMux.Unlock()
	log.Printf("[AGENT_GOAL] session=%s provider=%s action=stopped reason=person_goal_stop", sessionID, provider)
}

func agyGoalStatusLabel(status string) string {
	switch status {
	case "active":
		return "working"
	case "paused":
		return "stopped"
	case "complete":
		return "done"
	}
	return status
}
