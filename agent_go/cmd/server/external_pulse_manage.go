package server

import (
	"net/http"
	"time"
)

// Pulse controls over MCP, through the Pulse tab's own handlers and checks:
// what the tab shows, run Pulse or the goal check now, focus areas and goal
// memory. Turning Pulse on, its outward actions are settings
// (update_settings pulse); talking to Pulse is builder_pulse_chat.

func externalPulseManageDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("manage_pulse", "Read Autopilot settings, saved goal metrics, measurement history and its conversation (status), or start an Autopilot turn (run_now). Turn Autopilot on/off and set Outward with update_settings; talk with builder_pulse_chat. OFF starts no Autopilot activity.", true, true, map[string]any{
		"action": map[string]any{"type": "string", "enum": []any{"status", "run_now"}},
	}, "action")
}

func (api *StreamingAPI) externalPulseManageCall(w http.ResponseWriter, r *http.Request, args map[string]any, workflow DiscoveredWorkflow, access WorkflowAccessLevel) {
	claims := GetUserFromContext(r.Context())
	action := externalArg(args, "action")
	if action != "status" && access != WorkflowAccessOwner && access != WorkflowAccessWrite {
		externalError(w, 403, "forbidden", "This needs owner or editor access to the workflow.")
		return
	}
	if t := claims.AccessToken; t != nil {
		allowed := t.Allows("workflows:read") || t.Allows("runs:execute")
		if action == "run_now" {
			allowed = t.Allows("runs:execute")
		}
		if !allowed {
			externalError(w, 403, "insufficient_scope", "This connection does not allow "+action+".")
			return
		}
	}
	path := workflow.WorkspacePath
	respond := func(status int, body []byte) { externalScheduleRespond(w, status, body) }
	switch action {
	case "status":
		page, err := pulseOverview(r.Context(), path, time.Now().UTC())
		if err != nil {
			externalFailure(w, err)
			return
		}
		page["workflow_id"] = workflow.Manifest.ID
		page["conversation"] = api.savedGoalLeadConversationView(r.Context(), path)
		externalJSON(w, page)
	case "run_now":
		if api.scheduler == nil {
			externalError(w, 503, "scheduler_unavailable", "The scheduler is not running on this server.")
			return
		}
		handler, route := triggerWorkflowPulseHandler(api.scheduler), "/api/scheduler/workflows/pulse-run"

		respond(scheduleHandler(r, requireWorkflowWriteAccess(handler), http.MethodPost, route, nil, nil, map[string]any{"workspace_path": path}))
	default:
		externalError(w, 400, "invalid_arguments", "Unknown action.")
	}
}
