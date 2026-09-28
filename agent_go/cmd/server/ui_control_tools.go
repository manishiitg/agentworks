package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

func uiJSON(v interface{}) string { data, _ := json.Marshal(v); return string(data) }
func uiError(err error) string {
	return uiJSON(map[string]interface{}{"status": "rejected", "code": err.Error(), "visible": false})
}

func (api *StreamingAPI) registerUIControlTools(registrar definitionToolRegistrar, session, workspace string) error {
	return api.registerUIControlToolsForContract(registrar, session, workspace, uiControlContract)
}

func uiContractViewIDs(contract uiContract) []string {
	ids := make([]string, 0, len(contract.Views))
	for _, view := range contract.Views {
		ids = append(ids, view.ID)
	}
	return ids
}

func (api *StreamingAPI) registerUIControlToolsForContract(registrar definitionToolRegistrar, session, workspace string, contract uiContract) error {
	b := api.uiBroker()
	b.setScope(session, workspace)
	targetDescription := "For flow/open: exact plan step ID. For report/open: exact top-level report tab label. For files/open: workspace-relative file path (for example code/shared/helpers.py). For notify/expand: run_summary or pulse_review. Omit for other view openings."
	if isProjectProfileID(contract.Product) {
		targetDescription = "Work currently supports opening complete panels only; omit target."
	}
	props := map[string]interface{}{
		"view":                    map[string]interface{}{"type": "string", "enum": uiContractViewIDs(contract)},
		"action":                  map[string]interface{}{"type": "string", "enum": []string{"open", "expand", "refresh"}},
		"target":                  map[string]interface{}{"type": "string", "maxLength": 1024, "description": targetDescription},
		"idempotency_key":         map[string]interface{}{"type": "string", "maxLength": 128, "description": "Reuse for a retry of this exact action; omit to generate a fresh request."},
		"expected_state_revision": map[string]interface{}{"type": "integer", "minimum": 0},
	}
	schema := func(p map[string]interface{}, required ...string) map[string]interface{} {
		return map[string]interface{}{"type": "object", "properties": p, "required": required, "additionalProperties": false}
	}
	if err := registrar.RegisterCustomTool("list_ui_capabilities", "Discover presentation-only actions on the bound "+contract.Product+" workspace. Only advertised actions are implemented; never infer support for another view or target. No MCP connections or external sends.", schema(map[string]interface{}{}), func(context.Context, map[string]interface{}) (string, error) {
		_, err := b.snapshot(session)
		availability := "available"
		if err != nil {
			availability = err.Error()
		}
		return uiJSON(map[string]interface{}{"schema_version": contract.Version, "product": contract.Product, "availability": availability, "views": contract.Views}), nil
	}, "workflow_ui"); err != nil {
		return err
	}
	if err := registrar.RegisterCustomTool("get_ui_state", "Inspect the bound browser's bounded presentation state (no DOM/content/secrets). State is a recent browser observation, not proof that a human read it. An absent or ambiguous browser is explicitly unavailable.", schema(map[string]interface{}{}), func(context.Context, map[string]interface{}) (string, error) {
		s, err := b.snapshot(session)
		if err != nil {
			return uiError(err), nil
		}
		return uiJSON(s), nil
	}, "workflow_ui"); err != nil {
		return err
	}
	if err := registrar.RegisterCustomTool("perform_ui_action", "Open or refresh a workspace view, or expand supported instructions, using one semantic presentation action and wait up to 10 seconds for a browser receipt. Discover capabilities first. applied confirms the view shell rendered and the requested refresh was invoked, not that every data request succeeded. Notify expand confirms its instructions are visible. accepted/applying/expired are NOT success. Returns the completion receipt directly. For an uncertain outcome, repeat the same action with the same idempotency_key to inspect its retained receipt; never use a new key. Does not send, save, run, delete or connect anything.", schema(props, "view", "action"), func(ctx context.Context, args map[string]interface{}) (string, error) {
		return api.performUIActionForContract(ctx, session, workspace, contract, args)
	}, "workflow_ui"); err != nil {
		return err
	}
	return nil
}

func (api *StreamingAPI) performUIAction(ctx context.Context, session, workspace string, args map[string]interface{}) (string, error) {
	return api.performUIActionForContract(ctx, session, workspace, uiControlContract, args)
}

func (api *StreamingAPI) performUIActionForContract(ctx context.Context, session, workspace string, contract uiContract, args map[string]interface{}) (string, error) {
	b := api.uiBroker()
	if b.scope(session) != workspace {
		return uiError(fmt.Errorf("inactive_scope")), nil
	}
	view, _ := args["view"].(string)
	action, _ := args["action"].(string)
	target, _ := args["target"].(string)
	if err := validateUIActionForContract(contract, view, action, target); err != nil {
		return uiError(err), nil
	}
	key, _ := args["idempotency_key"].(string)
	var revision *int64
	if raw, ok := args["expected_state_revision"]; ok {
		n, ok := raw.(float64)
		if !ok || n < 0 || n != float64(int64(n)) {
			return uiError(fmt.Errorf("invalid_revision")), nil
		}
		v := int64(n)
		revision = &v
	}
	a, fresh, err := b.submitForContract(session, contract, view, action, target, key, revision)
	if err != nil {
		return uiError(err), nil
	}
	if fresh {
		api.emitAgentProfileEvent(session, &orchestratorevents.PresentationUpdatedEvent{PresentationID: a.RequestID, Kind: "workflow.ui-action", WorkspacePath: workspace, Title: "Workspace action requested", Payload: map[string]interface{}{"request_id": a.RequestID}})
	}
	if !uiTerminal(a.Status) {
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case <-a.done:
		case <-timer.C:
			b.expirePendingAction(session, a.RequestID, "timeout")
		case <-ctx.Done():
			b.expirePendingAction(session, a.RequestID, "request_cancelled")
		}
	}
	result, err := b.result(session, a.RequestID)
	if err != nil {
		return uiError(err), nil
	}
	return uiJSON(result), nil
}
