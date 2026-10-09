package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"net/http"
	"net/url"
	"strings"
)

func (api *StreamingAPI) registerWebhookTools(reg definitionToolRegistrar, userID, workspace string, policy workflowChatPolicy) error {
	if policy.Mode != "builder" || policy.Origin != "interactive" || !policy.allows("plan_authoring") {
		return nil
	}
	if err := api.registerCrewBuilderTools(reg, userID, workspace); err != nil {
		return err
	}
	description := "Create and manage inbound route webhooks directly in Builder chat. First list to discover valid steps, routes and groups. Set step_id and route_selections={} to run only a saved step directly, without Builder. For immutable raw payloads, payload_mappings can map a scalar source field to an allowed group, route on a routing/branch step, or standalone step. Fixed and mapped route selections are merged before execution. The platform generates/encrypts the secret; the UI displays existing triggers but has no creation form. Create/update require a complete name, enabled, auth_mode, route_selections and group_names configuration. Create/rotation returns a one-time secret: provide it only to the requesting user or their explicitly requested secret store, never shell logs. Bearer is for CI POSTs; github verifies signed GitHub webhooks. action=test sends an authenticated internal delivery through the receiver and executes the route; use only when the user requested testing. It does not verify public DNS/gateway connectivity. Configure input_mode=raw (default, native event JSON) or envelope (group/variables/payload), and allowed_variables for declared non-secret string overrides. group_names bounds caller group selection. Each trigger accepts up to four concurrent deliveries; every delivery receives its own immutable iteration-N-hook folder. A fifth concurrent delivery gets HTTP 503 with Retry-After: 30 and may be retried with the same delivery ID. action=status with id and run_id reads step progress, outputs and artifact links without revealing the trigger secret. Retains 10 finished hook folders plus active runs. All calls enforce current workflow permissions."
	params := map[string]interface{}{
		"type": "object", "additionalProperties": false, "required": []string{"action"}, "properties": map[string]interface{}{
			"action":      map[string]interface{}{"type": "string", "enum": []string{"list", "create", "update", "delete", "test", "status"}},
			"payload":     map[string]interface{}{"type": "object", "description": "JSON test event. test executes the saved route and can have external effects."},
			"run_id":      map[string]interface{}{"type": "string", "description": "Run ID returned by test or delivery; required for status."},
			"delivery_id": map[string]interface{}{"type": "string", "description": "Reuse for retries; omit for a new test event."},
			"event":       map[string]interface{}{"type": "string", "description": "Event type; defaults to agentworks.test."},
			"id":          map[string]interface{}{"type": "string"}, "name": map[string]interface{}{"type": "string"}, "enabled": map[string]interface{}{"type": "boolean"},
			"input_mode":        map[string]interface{}{"type": "string", "enum": []string{"raw", "envelope"}},
			"allowed_variables": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"auth_mode":         map[string]interface{}{"type": "string", "enum": []string{"bearer", "github"}},
			"kind":              map[string]interface{}{"type": "string", "enum": []string{"internal", "function"}, "description": "function exposes this route as a typed function other Crews, workflows and MCP/CLI tools call with call_function (no URL or secret; set function). internal binds one named caller (legacy; prefer function)."},
			"function": map[string]interface{}{"type": "object", "additionalProperties": false, "required": []string{"name", "inputs"}, "description": "kind=function only. The callable contract: each input is a declared workflow variable set for that run from the caller's argument. Mark inputs the run cannot do without (e.g. PR_NUMBER) required, so a call without them is rejected before anything runs instead of falling back to a saved value.", "properties": map[string]interface{}{
				"name":        map[string]interface{}{"type": "string", "description": "snake_case, e.g. review_pr."},
				"description": map[string]interface{}{"type": "string", "description": "What the function does, for callers."},
				"inputs": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object", "additionalProperties": false, "required": []string{"name"}, "properties": map[string]interface{}{
					"name":        map[string]interface{}{"type": "string", "description": "A declared, non-secret workflow variable (from list.declared_variables)."},
					"type":        map[string]interface{}{"type": "string", "enum": []string{"string", "integer", "number", "boolean", "object"}},
					"required":    map[string]interface{}{"type": "boolean"},
					"description": map[string]interface{}{"type": "string"},
					"enum":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				}}},
				"allowed_callers": map[string]interface{}{"type": "array", "description": "Optional: only these Crews/workflows may call; omit for anyone who can run this workflow.", "items": triggerCallerToolSchema(triggerCallerCrew, triggerCallerWorkflow)},
			}},
			"caller":           triggerCallerToolSchema(triggerCallerCrew, triggerCallerWorkflow),
			"step_id":          map[string]interface{}{"type": "string", "description": "Optional single-step target from list.steps; requires empty route_selections. Empty selects route/full workflow; omission preserves an existing target on update. Only this step runs; prior outputs must not be assumed."},
			"route_selections": map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}, "description": "Map routing step IDs to saved route IDs, obtained from list."},
			"payload_mappings": webhookPayloadMappingsSchema(),
			"group_names":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}, "rotate_secret": map[string]interface{}{"type": "boolean"},
		}}
	if manifest, found, err := ReadWorkflowManifest(context.Background(), workspace); err == nil && found && manifest.Kind == "relay" {
		description = "Create and manage this Relay's authenticated API function triggers. First list to discover declared variables and saved routes. Create/update require name, enabled, kind=function, route_selections and function with a required object INPUT argument. Relays execute once per call using configuration values and caller inputs; there are no variable groups. Omit group_names. action=test executes the draft; action=status reads durable run results. All calls enforce current permissions."
		properties := params["properties"].(map[string]interface{})
		delete(properties, "group_names")
		properties["kind"] = map[string]interface{}{"type": "string", "enum": []string{triggerKindFunction}}
	}
	return reg.RegisterCustomTool("manage_workflow_webhook", description, params, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: userID})
		if userAccessForClaims(&UserClaims{UserID: userID}).Disabled {
			return "", fmt.Errorf("Access denied: disabled account")
		}
		return api.manageWorkflowWebhook(ctx, workspace, args)
	}, "workflow_webhooks")
}

// manageWorkflowWebhook lists, saves, deletes or tests one workflow's or
// Relay's triggers as the person in ctx. The Builder tool and the MCP
// manage_triggers tool share it.
func (api *StreamingAPI) manageWorkflowWebhook(ctx context.Context, workspace string, args map[string]interface{}) (string, error) {
	if _, err := authorizeWorkflowContextPaths(ctx, []string{workspace}); err != nil {
		return "", fmt.Errorf("Workflow is unavailable or access denied")
	}
	if api.scheduler == nil {
		return "", fmt.Errorf("Workflow scheduler is unavailable")
	}
	action, _ := args["action"].(string)
	if action == "test" || action == "status" {
		return api.testWorkflowWebhook(ctx, workspace, args)
	}
	method, target := http.MethodGet, "/api/workflow-webhooks?workspace_path="+url.QueryEscape(workspace)
	handler := api.scheduler.listWorkflowWebhooks
	payload := map[string]interface{}{"workspace_path": workspace}
	switch action {
	case "list":
	case "create", "update":
		kind, _ := args["kind"].(string)
		required := []string{"name", "enabled", "auth_mode", "route_selections", "group_names"}
		if isInternalTriggerKind(kind) {
			required = []string{"name", "enabled", "route_selections", "group_names", "caller"}
		}
		if isFunctionTriggerKind(kind) {
			required = []string{"name", "enabled", "route_selections", "group_names", "function"}
		}
		manifest, found, err := ReadWorkflowManifest(ctx, workspace)
		if err != nil || !found {
			return "", fmt.Errorf("Workflow manifest unavailable")
		}
		if manifest.Kind == "relay" {
			required = []string{"name", "enabled", "route_selections", "function"}
			if value, supplied := args["group_names"]; supplied {
				payload["group_names"] = value
			}
		}
		for _, key := range required {
			v, ok := args[key]
			if !ok {
				return "", fmt.Errorf("%s is required for %s", key, action)
			}
			payload[key] = v
		}
		for _, key := range []string{"input_mode", "allowed_variables", "step_id", "payload_mappings", "kind", "caller", "function"} {
			if v, ok := args[key]; ok {
				payload[key] = v
			}
		}
		if v, ok := args["rotate_secret"]; ok {
			payload["rotate_secret"] = v
		}
		method = http.MethodPost
		handler = api.scheduler.saveWorkflowWebhook
		if action == "update" {
			method = http.MethodPut
		}
	case "delete":
		method = http.MethodDelete
		handler = api.scheduler.deleteWorkflowWebhook
	default:
		return "", fmt.Errorf("Unknown webhook action")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("Invalid webhook arguments")
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	if action == "update" || action == "delete" {
		id, _ := args["id"].(string)
		if strings.TrimSpace(id) == "" {
			return "", fmt.Errorf("id is required")
		}
		req = mux.SetURLVars(req, map[string]string{"id": id})
	}
	rec := &accessToolResponse{header: make(http.Header)}
	handler(rec, req)
	if rec.status >= 400 {
		return "", fmt.Errorf("Webhook operation failed (%d): %s", rec.status, strings.TrimSpace(rec.String()))
	}
	if rec.Len() == 0 {
		return `{"success":true}`, nil
	}
	return rec.String(), nil
}

// A test uses the stored credential internally; it never echoes it into a shell.
func (api *StreamingAPI) testWorkflowWebhook(ctx context.Context, workspace string, args map[string]interface{}) (string, error) {
	level, manifest := workflowAccessForWorkspacePath(ctx, GetUserFromContext(ctx), workspace)
	if level != WorkflowAccessOwner && level != WorkflowAccessWrite {
		return "", fmt.Errorf("Workflow owner access required to test a webhook")
	}
	id, _ := args["id"].(string)
	index := -1
	if manifest != nil {
		for i, s := range manifest.Schedules {
			if s.ID == id && s.ScheduleType == "webhook" {
				index = i
				break
			}
		}
	}
	if index < 0 {
		return "", fmt.Errorf("Webhook not found in the active workflow")
	}
	sched := manifest.Schedules[index]
	if sched.Webhook == nil {
		return "", fmt.Errorf("Webhook authentication is unavailable")
	}
	secret, err := decryptSecretValueWithAAD(sched.Webhook.EncryptedSecret, webhookAAD(manifest.ID, id))
	if err != nil {
		return "", fmt.Errorf("Cannot read webhook authentication")
	}
	if args["action"] == "status" {
		runID, _ := args["run_id"].(string)
		if runID == "" {
			return "", fmt.Errorf("run_id is required")
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, webhookStatusPath(id, runID), nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		req = mux.SetURLVars(req, map[string]string{"id": id, "run": runID})
		rec := &accessToolResponse{header: make(http.Header)}
		api.scheduler.pollWebhookRun(rec, req)
		if rec.status >= 400 {
			return "", fmt.Errorf("Webhook status failed (%d): %s", rec.status, rec.String())
		}
		return rec.String(), nil
	}
	payload, ok := args["payload"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("payload must be a JSON object")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("Invalid payload")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/hooks/workflow/"+id, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req = mux.SetURLVars(req, map[string]string{"id": id})
	req.Header.Set("Content-Type", "application/json")
	delivery, _ := args["delivery_id"].(string)
	if delivery == "" {
		delivery = "builder-test-" + uuid.NewString()
	}
	event, _ := args["event"].(string)
	if event == "" {
		event = "agentworks.test"
	}
	if sched.Webhook.AuthMode == "github" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(data)
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-GitHub-Delivery", delivery)
		req.Header.Set("X-GitHub-Event", event)
	} else {
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Idempotency-Key", delivery)
		req.Header.Set("X-Webhook-Event", event)
	}
	receiver := webhookReceiver{find: func(context.Context, string) (*ScheduleSearchResult, error) {
		return &ScheduleSearchResult{Manifest: manifest, Index: index, WorkspacePath: workspace}, nil
	}, start: api.scheduler.triggerSavedSchedule, existing: api.scheduler.existingWebhookRun}
	rec := &accessToolResponse{header: make(http.Header)}
	receiver.receive(rec, req)
	result, _ := json.Marshal(map[string]interface{}{"status_code": rec.status, "delivery_id": delivery, "response": strings.TrimSpace(rec.String()), "scope": "internal receiver test; public endpoint reachability is not tested"})
	if rec.status >= 400 {
		return "", fmt.Errorf("Webhook test: %s", result)
	}
	return string(result), nil
}

// One JSON contract for all raw event sources.
func webhookPayloadMappingsSchema() map[string]interface{} {
	valueMappingSchema := map[string]interface{}{"type": "object", "additionalProperties": false, "required": []string{"source", "values"}, "properties": map[string]interface{}{
		"source": map[string]interface{}{"type": "string"}, "values": map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}}, "default": map[string]interface{}{"type": "string"},
	}}
	return map[string]interface{}{"type": "object", "additionalProperties": false, "description": "Map fixed scalar paths in raw JSON (including numeric array segments) to allowed execution choices. Missing or unknown values reject delivery.", "properties": map[string]interface{}{
		"group": valueMappingSchema, "routes": map[string]interface{}{"type": "object", "additionalProperties": valueMappingSchema}, "step": valueMappingSchema,
	}}
}
