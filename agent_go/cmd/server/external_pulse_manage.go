package server

import (
	"net/http"
	"net/url"
)

// Pulse controls over MCP, through the Pulse tab's own handlers and checks:
// what the tab shows, run Pulse or the goal check now, focus areas and goal
// memory. Turning Pulse on, its autonomy and pace are settings
// (update_settings pulse); talking to Pulse is builder_pulse_chat.

func externalPulseManageDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("manage_pulse", "Work with a workflow's Pulse (the agent that owns its goal) the way the Pulse tab does. status: goal, latest goal check, findings, focus areas and decisions. run_now: start a Pulse pass now. goal_check_now: run the daily goal check now. focus_area: confirm, reject, add, extend or close a focus area. goal_memory: replace the goal memory text. Turn Pulse on, set autonomy and pace with update_settings (pulse); talk to Pulse with builder_pulse_chat. Changes need owner or editor access; running needs run access.", true, true, map[string]any{
		"action": map[string]any{"type": "string", "enum": []any{"status", "run_now", "goal_check_now", "focus_area", "goal_memory"}},
		"focus": map[string]any{"type": "object", "additionalProperties": false, "description": "For focus_area.", "properties": map[string]any{
			"action":   map[string]any{"type": "string", "enum": []any{"confirm", "reject", "add", "extend", "close"}},
			"id":       externalString("Focus area id from status (not for add)."),
			"text":     externalString("The focus, for add."),
			"end_date": externalString("YYYY-MM-DD, for add or extend."),
			"check":    externalString("How progress is checked, for add."),
			"status":   externalString("Outcome, for close."),
			"lesson":   externalString("What was learned, for close."),
		}, "required": []any{"action"}},
		"content": map[string]any{"type": "string", "maxLength": 20000, "description": "For goal_memory: the full new goal memory."},
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
		if action == "run_now" || action == "goal_check_now" {
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
		respond(scheduleHandler(r, api.handleGetPulseContext, http.MethodGet, "/api/workflow/pulse-context", nil, url.Values{"workspace_path": {path}}, nil))
	case "run_now", "goal_check_now":
		if api.scheduler == nil {
			externalError(w, 503, "scheduler_unavailable", "The scheduler is not running on this server.")
			return
		}
		handler, route := triggerWorkflowPulseHandler(api.scheduler), "/api/scheduler/workflows/pulse-run"
		if action == "goal_check_now" {
			handler, route = triggerWorkflowGoalCheckHandler(api.scheduler), "/api/scheduler/workflows/goal-check-run"
		}
		respond(scheduleHandler(r, requireWorkflowWriteAccess(handler), http.MethodPost, route, nil, nil, map[string]any{"workspace_path": path}))
	case "focus_area":
		focus, _ := args["focus"].(map[string]any)
		if focus == nil {
			externalError(w, 400, "invalid_arguments", "focus is required for focus_area.")
			return
		}
		body := map[string]any{"workspace_path": path}
		for key, value := range focus {
			body[key] = value
		}
		respond(scheduleHandler(r, requireReportHumanInputAccess(true, api.handleGoalLeadFocusArea), http.MethodPost, "/api/workflow/goal-lead/focus-areas", nil, nil, body))
	case "goal_memory":
		content, ok := args["content"].(string)
		if !ok {
			externalError(w, 400, "invalid_arguments", "content is required for goal_memory.")
			return
		}
		respond(scheduleHandler(r, requireReportHumanInputAccess(true, api.handlePutGoalMemory), http.MethodPut, "/api/workflow/goal-memory", nil, nil, map[string]any{"workspace_path": path, "content": content}))
	default:
		externalError(w, 400, "invalid_arguments", "Unknown action.")
	}
}
