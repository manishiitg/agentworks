package server

import (
	"net/http"
	"strings"
)

// externalPulseCall serves builder_pulse_chat and builder_pulse_status: an MCP
// client talks to the workflow's Pulse like the Pulse tab's direct box (owner,
// 2026-10-08: from MCP, not from Slack). The Builder grant is already checked.
func (api *StreamingAPI) externalPulseCall(w http.ResponseWriter, r *http.Request, name string, args map[string]interface{}, workflow DiscoveredWorkflow) {
	ctx := r.Context()
	if !workflowHasGoal(ctx, workflow.WorkspacePath) {
		externalError(w, http.StatusConflict, "pulse_unavailable", "This workflow's Pulse is off or has no goal yet (Pulse on and soul/soul.md).")
		return
	}
	switch name {
	case "builder_pulse_chat":
		message := strings.TrimSpace(externalArg(args, "message"))
		if message == "" || len([]rune(message)) > goalLeadOwnerMessageRunes {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "message is required (at most 4000 characters)")
			return
		}
		if err := api.sendGoalLeadOwnerMessage(ctx, workflow.WorkspacePath, message); err != nil {
			externalError(w, http.StatusServiceUnavailable, "pulse_unavailable", err.Error())
			return
		}
		externalJSON(w, map[string]interface{}{"status": "started", "next": "Poll builder action=pulse_status until busy is false; Pulse's reply is the newest message."})
	case "builder_pulse_status":
		limit := 10
		if v, ok := args["limit"].(float64); ok && v >= 1 && v <= 40 {
			limit = int(v)
		}
		view := api.goalLeadConversationView(ctx, workflow.WorkspacePath)
		if messages, ok := view["messages"].([]GoalLeadMessage); ok && len(messages) > limit {
			view["messages"] = messages[len(messages)-limit:]
		}
		externalJSON(w, view)
	}
}
