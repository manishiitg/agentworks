package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// PLAT-746-1037: the person's own agents set a Codex goal on the person's own chat from a tool, as the person would with
// /goal. The tool sends the same slash command through the chat's own send path, so the existing goal control (owner
// check, supported program, running-goal pause/clear, no prompt text) decides everything; this file only decides who
// may call it and marks the call so its log line says it came from a tool.

type nativeGoalToolContextKey struct{}

// nativeGoalOrigin is the reason a goal command carries: "person_<action>" for text the person typed, "tool_<action>"
// when one of the person's own agents set it through the goal tool.
func nativeGoalOrigin(ctx context.Context, action string) string {
	if _, tool := ctx.Value(nativeGoalToolContextKey{}).(string); tool {
		return "tool_" + action
	}
	return "person_" + action
}

func nativeGoalCallerLog(ctx context.Context) string {
	if caller, tool := ctx.Value(nativeGoalToolContextKey{}).(string); tool {
		return " caller=user:" + caller
	}
	return ""
}

// nativeGoalToolQuery turns the tool's goal and objective into the same slash command a person types.
func nativeGoalToolQuery(goal, objective string) (string, string) {
	objective = strings.TrimSpace(objective)
	switch goal {
	case "show":
		return "/goal", ""
	case "pause", "resume", "clear":
		return "/goal " + goal, ""
	case "set":
		switch objective {
		case "":
			return "", "Give the goal's objective."
		case "pause", "resume", "clear":
			return "", "Word the objective as a sentence; a single word like pause, resume or clear is a goal command."
		}
		if _, _, err := parseNativeGoalCommand("/goal "+objective, ""); err != nil {
			return "", err.Error()
		}
		return "/goal " + objective, ""
	}
	return "", "Choose set, show, pause, resume or clear."
}

// externalCrewChatGoal is sessions action=goal. Only the chat's owner, through their own callers (their MCP agent or an
// agent of theirs in the same Crew), may use it: a teammate, another person's agent and a shared ask are refused, and
// the chat's own goal control then checks the owner again.
func (api *StreamingAPI) externalCrewChatGoal(w http.ResponseWriter, r *http.Request, args map[string]any, chats []externalCrewChat, vars map[string]string, crewID string) {
	claims := GetUserFromContext(r.Context())
	chat, found := findCrewChat(chats, externalArg(args, "chat_id"))
	if !found {
		externalError(w, 404, "chat_not_found", "Choose a chat from list.")
		return
	}
	if claims == nil || chat.OwnerID != claims.UserID || !chat.CanWrite {
		externalError(w, 403, "tab_owner", "A goal can be set, paused or cleared only on your own chats.")
		return
	}
	query, refusal := nativeGoalToolQuery(externalArg(args, "goal"), externalArg(args, "objective"))
	if refusal != "" {
		externalError(w, 400, "invalid_arguments", refusal)
		return
	}
	ctx := context.WithValue(r.Context(), nativeGoalToolContextKey{}, claims.UserID)
	status, body := scheduleHandler(r.WithContext(ctx), api.handleAgentProfileChatQuery, http.MethodPost, "/api/agent-profiles/"+vars["id"]+"/query", vars, nil, AgentProfileChatRequest{Message: query, ConversationKey: chat.key})
	var reply struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &reply) == nil && reply.Status == "notice" {
		externalError(w, 400, "goal_unavailable", firstNonEmptyTrimmed(reply.Message, "Goal mode is not available for this AI program."))
		return
	}
	if status >= 400 {
		externalError(w, status, "goal_refused", strings.TrimSpace(string(body)))
		return
	}
	out := map[string]any{"crew_id": crewID, "chat_id": chat.ChatID, "goal": externalArg(args, "goal"), "started": true}
	if current, err := api.latestNativeGoal(r.Context(), chat.SessionID); err == nil && current != nil && current.Goal != nil {
		out["last_known"] = map[string]any{"status": current.Goal.Status, "objective": current.Goal.Objective}
	}
	externalJSON(w, out)
}
