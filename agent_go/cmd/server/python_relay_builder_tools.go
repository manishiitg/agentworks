package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

func (api *StreamingAPI) registerPythonRelayBuilderTools(registrar interface {
	RegisterCustomTool(string, string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error), string) error
}, workspace, userID string) error {
	requestSchema := map[string]interface{}{"type": "object", "required": []string{"input"}, "properties": map[string]interface{}{"input": map[string]interface{}{"type": "object"}, "function": map[string]interface{}{"type": "string"}, "idempotency_key": map[string]interface{}{"type": "string"}}}
	if err := registrar.RegisterCustomTool("test_relay", "Run this Relay draft once with the supplied INPUT object. Returns a durable run_id; inspect with get_relay_run. Does not publish or resume.", requestSchema, func(ctx context.Context, args map[string]interface{}) (string, error) {
		manifest, found, err := ReadWorkflowManifest(ctx, workspace)
		if err != nil {
			return "", err
		}
		if !found || !isPythonRelay(manifest) {
			return "", fmt.Errorf("Python Relay required")
		}
		access := workflowAccessForManifest(&UserClaims{UserID: userID}, manifest)
		if access != WorkflowAccessOwner && access != WorkflowAccessWrite {
			return "", fmt.Errorf("Relay write access required")
		}
		input, ok := args["input"].(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("input object required")
		}
		function, _ := args["function"].(string)
		if function == "" {
			for _, trigger := range manifest.Schedules {
				if trigger.Enabled && trigger.IsFunctionTrigger() {
					if function != "" {
						return "", fmt.Errorf("choose a function when multiple triggers are enabled")
					}
					function = trigger.Function.Name
				}
			}
		}
		key, _ := args["idempotency_key"].(string)
		if key == "" {
			key = uuid.NewString()
		}
		if api.scheduler == nil {
			return "", fmt.Errorf("Relay runner unavailable")
		}
		_, delivery, err := api.scheduler.dispatchWorkflowFunction(ctx, workflowFunctionCall{WorkflowID: manifest.ID, Function: function, draftRelay: true, Caller: triggerCaller{Type: triggerCallerUser, ID: userID}, DeliveryID: "draft\x00" + userID + "\x00" + key, Args: map[string]interface{}{"INPUT": input}, Payload: map[string]interface{}{"relay_caller": userID}})
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(delivery)
		return string(data), err
	}, "relay_runtime_tools"); err != nil {
		return err
	}
	return registrar.RegisterCustomTool("get_relay_run", "Inspect this Relay invocation's status, final returned JSON and recorded calls. Failed Python runs are terminal; a new test gets a new run_id.", map[string]interface{}{"type": "object", "required": []string{"run_id"}, "properties": map[string]interface{}{"run_id": map[string]interface{}{"type": "string"}}}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		manifest, found, err := ReadWorkflowManifest(ctx, workspace)
		if err != nil {
			return "", err
		}
		if !found || manifest.Kind != "relay" || workflowAccessForManifest(&UserClaims{UserID: userID}, manifest) == WorkflowAccessNone {
			return "", fmt.Errorf("Relay access required")
		}
		id, _ := args["run_id"].(string)
		if id == "" || api.scheduler == nil {
			return "", fmt.Errorf("run_id and Relay runner required")
		}
		run, err := api.scheduler.existingWebhookRun(ctx, id)
		if err != nil {
			return "", err
		}
		draft, err := relayDraftWorkspaceForRelease(ctx, run.ScopeID)
		if err != nil || draft != workspace {
			return "", fmt.Errorf("run belongs to a different Relay")
		}
		result, err := readWebhookRunResult(run.ScopeID, run)
		if err != nil {
			return "", err
		}
		applyRelayResult(manifest, &result, run.ScopeID, run)
		data, err := json.Marshal(result)
		return string(data), err
	}, "relay_runtime_tools")
}

// Source edits use existing workspace file tools; runtime tests use the same
// admitted function trigger and durable run lifecycle as public API calls.
