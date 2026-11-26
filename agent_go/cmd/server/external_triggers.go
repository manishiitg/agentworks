package server

import (
	"encoding/json"
	"net/http"
)

// Webhooks of a workflow or Flow over MCP, through the Builder's
// manage_workflow_webhook implementation. A Crew's webhooks belong to its
// workflow (#3286). Changes need owner or editor access; a webhook's secret
// is returned once, on create or rotate, as in the app.

func externalTriggerDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("manage_triggers", "List, create, update or delete the webhooks of a workflow or Flow (a project's webhooks belong to its workflow: pass the workflow), or read a trigger's recent deliveries (runs): authenticated HTTP webhooks; for workflows and Flows also test a trigger and read a test run's status. Pass project. List first: it shows declared variables, saved routes and existing triggers. A webhook's secret is returned once, when it is created or rotated.", true, false, map[string]any{
		"project": externalString("Project ID or name."),
		"action":  map[string]any{"type": "string", "enum": []any{"list", "create", "update", "delete", "test", "status", "runs"}},
		"limit":   externalInteger(1, 100),
		"id":      externalString("Trigger ID from list; required for update, delete, test and runs."),
		"trigger": map[string]any{"type": "object", "description": "For create and update. Workflows and Flows: name, enabled, auth_mode (bearer or github), route_selections, input_preset, inputs, chat_id, step_id, input_mode, allowed_variables, payload_mappings, rotate_secret. Use functions to manage callable functions. For test: payload, event, delivery_id; for status: run_id."},
	}, "action", "project")
}

func (api *StreamingAPI) externalWorkflowTriggerCall(w http.ResponseWriter, r *http.Request, args map[string]any, workflow DiscoveredWorkflow, access WorkflowAccessLevel) {
	claims := GetUserFromContext(r.Context())
	action := externalArg(args, "action")
	if action != "list" && action != "runs" && access != WorkflowAccessOwner && access != WorkflowAccessWrite {
		externalError(w, 403, "forbidden", "Changing or testing triggers needs owner or editor access.")
		return
	}
	if t := claims.AccessToken; t != nil {
		if (action == "test" || action == "status") && !t.Allows("runs:execute") || !t.Allows("workflows:read") && !t.Allows("runs:execute") {
			externalError(w, 403, "insufficient_scope", "This connection does not allow "+action+" here.")
			return
		}
	}
	if action == "runs" {
		if externalArg(args, "id") == "" {
			externalError(w, 400, "invalid_arguments", "id is required for runs.")
			return
		}
		api.externalScheduleRuns(w, r, map[string]any{"schedule_id": externalArg(args, "id"), "limit": float64(externalInt(args, "limit", 20))}, workflow)
		return
	}
	flat := map[string]any{"action": action}
	if trigger, ok := args["trigger"].(map[string]any); ok {
		for key, value := range trigger {
			flat[key] = value
		}
	}
	if id := externalArg(args, "id"); id != "" {
		flat["id"] = id
	}
	out, err := api.manageWorkflowWebhook(r.Context(), workflow.WorkspacePath, flat)
	if err != nil {
		externalError(w, 400, "trigger_rejected", err.Error())
		return
	}
	externalTriggerJSON(w, out)
}

// externalCrewTriggerCall answers manage_triggers for a Crew: a Crew has no webhooks of its own (#3286); its
// webhooks belong to its workflow.
func (api *StreamingAPI) externalCrewTriggerCall(w http.ResponseWriter, _ *http.Request, _ map[string]any, _ string) {
	externalError(w, 400, "use_workflow", errCrewMessageWebhookRemoved.Error()+". Pass the project's workflow as project.")
}

// externalTriggerJSON passes the shared implementation's JSON through, and
// wraps a plain-text answer.
func externalTriggerJSON(w http.ResponseWriter, out string) {
	if json.Valid([]byte(out)) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(out))
		return
	}
	externalJSON(w, map[string]any{"result": out})
}
