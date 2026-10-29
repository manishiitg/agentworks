package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Triggers of a workflow, Flow or Crew over MCP: webhooks, function triggers
// and caller-bound internal triggers. Workflows and Flows share the
// Builder's manage_workflow_webhook implementation; Crews use the Crew
// trigger store, which only reaches the caller's own Crews. Changes need
// owner or editor access on a workflow or Flow; a webhook's secret is
// returned once, on create or rotate, as in the app.

func externalTriggerDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("manage_triggers", "List, create, update or delete the triggers of a workflow, Flow or Crew, or read a trigger's recent deliveries (runs): authenticated HTTP webhooks; for workflows and Flows also test a trigger and read a test run's status. Pass project. List first: it shows declared variables, saved routes and existing triggers. A webhook's secret is returned once, when it is created or rotated.", true, false, map[string]any{
		"project": externalString("Project ID or name."),
		"action":  map[string]any{"type": "string", "enum": []any{"list", "create", "update", "delete", "test", "status", "runs"}},
		"limit":   externalInteger(1, 100),
		"id":      externalString("Trigger ID from list; required for update, delete, test and runs."),
		"trigger": map[string]any{"type": "object", "description": "For create and update. Workflows and Flows: name, enabled, kind (webhook default, function, internal), auth_mode (bearer or github), route_selections, group_names (workflows), function {name, description, inputs, allowed_callers}, caller, step_id, input_mode, allowed_variables, payload_mappings, rotate_secret. Crews: name, enabled, message, auth_mode, run_destination (chat or isolated), kind (internal), caller, rotate_secret. For test: payload, event, delivery_id; for status: run_id."},
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

func (api *StreamingAPI) externalCrewTriggerCall(w http.ResponseWriter, r *http.Request, args map[string]any, crewID string) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	action := externalArg(args, "action")
	if trigger, ok := args["trigger"].(map[string]interface{}); ok {
		if kind, _ := trigger["kind"].(string); kind != "" && kind != "webhook" {
			externalError(w, 400, "use_functions", "Use functions for reusable work.")
			return
		}
		if trigger["caller"] != nil || trigger["function"] != nil {
			externalError(w, 400, "use_functions", "Use functions for reusable work.")
			return
		}
	}

	if t := claims.AccessToken; t != nil {
		scope := "crews:write"
		if action == "list" || action == "runs" {
			scope = "crews:read"
		}
		if !t.Allows(scope) || !t.AllowsCrew(crewID) {
			externalError(w, 403, "insufficient_scope", "This connection does not allow "+action+" on this Crew.")
			return
		}
	}
	if api.projectActions == nil {
		externalError(w, 503, "triggers_unavailable", "Crew triggers are unavailable.")
		return
	}
	crew, _, _, ok := api.externalCrewResolve(ctx, claims, crewID)
	if !ok {
		externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
		return
	}
	if action != "list" && action != "runs" && !crew.OwnedByCaller {
		externalError(w, 403, "forbidden", "Only the Crew's owner can change its triggers.")
		return
	}
	userID, profileID := claims.UserID, "work"
	trigger, _ := args["trigger"].(map[string]any)
	if trigger == nil {
		trigger = map[string]any{}
	}
	str := func(key, fallback string) string {
		if value, ok := trigger[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
		return fallback
	}
	triggers, err := api.projectActions.projectWebhookConfigs(ctx, userID, profileID, crewID)
	if err != nil {
		externalError(w, 404, "not_found", err.Error())
		return
	}
	id := externalArg(args, "id")
	for _, entry := range triggers {
		if entry.ID == id && entry.IsInternal() {
			externalError(w, 400, "use_functions", "Use functions or the saved step binding for that entry.")
			return
		}
	}
	switch action {
	case "list":
		out := make([]productWebhookResponse, 0, len(triggers))
		for _, item := range triggers {
			if item.IsInternal() {
				continue
			}
			out = append(out, productWebhookDTO(item))
		}
		externalJSON(w, map[string]any{"crew_id": crewID, "triggers": out})
	case "create", "update":
		req := productWebhookRequest{ProfileID: profileID, ProjectID: crewID, Enabled: true, RunDestination: runDestinationChat}
		if action == "update" {
			var current *productWebhookTrigger
			for i := range triggers {
				if triggers[i].ID == id {
					current = &triggers[i]
				}
			}
			if current == nil {
				externalError(w, 404, "trigger_not_found", "No trigger with that ID on this Crew; use action list.")
				return
			}
			req.Name, req.Message, req.Enabled, req.Kind, req.Caller = current.Name, current.Message, current.Enabled, current.Kind, current.Caller
			req.RunDestination = firstNonEmptyTrimmed(current.RunDestination, runDestinationChat)
			if current.Webhook != nil {
				req.AuthMode = current.Webhook.AuthMode
			}
		}
		req.Name, req.Message, req.AuthMode = str("name", req.Name), str("message", req.Message), str("auth_mode", req.AuthMode)
		req.RunDestination, req.Kind = str("run_destination", req.RunDestination), str("kind", req.Kind)
		if value, ok := trigger["enabled"].(bool); ok {
			req.Enabled = value
		}
		req.RotateSecret, _ = trigger["rotate_secret"].(bool)
		if caller := triggerCallerFromArgs(trigger); caller != nil {
			req.Caller = caller
		}
		if err := authorizeTriggerCallerWorkflow(ctx, userID, req.Caller); err != nil {
			externalError(w, 403, "forbidden", err.Error())
			return
		}
		saveID := ""
		if action == "update" {
			saveID = id
		}
		response, _, err := api.projectActions.saveProductWebhookConfig(ctx, userID, req, saveID)
		if err != nil {
			externalError(w, 400, "trigger_rejected", err.Error())
			return
		}
		externalJSON(w, response)
	case "runs":
		query := url.Values{"profile_id": {profileID}, "project_id": {crewID}, "limit": {fmt.Sprint(externalInt(args, "limit", 20))}}
		status, body := scheduleHandler(r, api.projectActions.listProductWebhookRuns, http.MethodGet, "/api/product-webhooks/"+url.PathEscape(id)+"/runs", map[string]string{"id": id}, query, nil)
		externalScheduleRespond(w, status, body)
	case "delete":
		if err := api.projectActions.deleteProductWebhookConfig(ctx, userID, profileID, crewID, id); err != nil {
			externalError(w, 400, "trigger_rejected", err.Error())
			return
		}
		externalJSON(w, map[string]any{"deleted": id})
	default:
		externalError(w, 400, "invalid_arguments", "Crews support list, create, update and delete.")
	}
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
