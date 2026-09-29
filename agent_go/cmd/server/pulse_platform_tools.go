package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"

	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Pulse Goal Work looks beyond its own workflow: other workflows the owner can
// see (their plans, runs, files and knowledge) and the Crews on this server.
// Both tools reuse the external API in-process, as the calling session's
// owner (pulseToolScope: never a model-chosen workflow or its owner), so
// visibility and access are exactly what that person gets from the
// agentworks CLI.

// pulsePlatformAPI is set when the server starts. Nil (tests, CLI tools) means
// the platform tools report that they are unavailable.
var pulsePlatformAPI *StreamingAPI

// pulsePlatformReadOperations never change anything anywhere.
var pulsePlatformReadOperations = map[string]bool{
	"list_workflows": true, "get_workflow": true, "get_plan": true,
	"list_files": true, "search_files": true, "read_file": true,
	"list_runs": true, "get_run": true, "get_logs": true,
	"list_workflow_knowledge": true, "read_workflow_knowledge": true,
	"list_crews": true, "get_crew": true, "list_crew_functions": true,
	"list_crew_files": true, "read_crew_file": true, "get_crew_function_call": true,
}

// pulseCrewWorkOperations make a Crew do work in the owner's own conversation
// with it. Goal Work gets them with its Run permission.
var pulseCrewWorkOperations = map[string]bool{"ask_crew": true, "call_crew_function": true, "reply_crew_function_call": true}

func sortedOperations(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func createPulsePlatformTools() ([]llmtypes.Tool, map[string]interface{}, map[string]string) {
	params := func(ops map[string]bool, argsDescription string) *llmtypes.Parameters {
		return llmtypes.NewParameters(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"workspace_path": map[string]interface{}{"type": "string", "description": "Optional. This workflow's own path; any other workflow is refused. The call runs as this session's owner."},
				"operation":      map[string]interface{}{"type": "string", "enum": sortedOperations(ops)},
				"arguments":      map[string]interface{}{"type": "object", "description": argsDescription},
			},
			"required": []string{"operation"},
		})
	}
	searchTool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name: "search_platform",
		Description: "Read-only look across the platform with the workflow owner's access: other workflows (list_workflows, then get_workflow/get_plan/list_runs/get_run/list_files/search_files/read_file/list_workflow_knowledge/read_workflow_knowledge with workflow_id) and Crews (list_crews, then get_crew/list_crew_functions/list_crew_files/read_crew_file with crew_id; get_crew_function_call polls a crew call). " +
			"Use it to reuse what already exists: leads, research, results, code or skills another workflow produced, or a Crew whose functions do what this goal needs. It never changes anything.",
		Parameters: params(pulsePlatformReadOperations, "The operation's arguments, e.g. {\"workflow_id\":\"...\",\"path\":\"...\"} or {\"crew_id\":\"...\"}. list_workflows and list_crews accept {\"query\":\"...\"}."),
	}}
	crewTool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name: "ask_platform_crew",
		Description: "Have a Crew do work toward this workflow's goal, in the owner's own continuing conversation with that Crew (never its main chat). ask_crew takes {crew_id, message}; call_crew_function takes {crew_id, function, args} per list_crew_functions. " +
			"Returns the result if it finishes within wait_seconds, else a call_id for search_platform get_crew_function_call. If that poll shows pending_inputs, answer one with reply_crew_function_call {call_id, request_id, response}. Anything the Crew would post, send or contact outside follows this workflow's outward permission: when that is ask, request only preparation and put the outward step in a decision.",
		Parameters: params(pulseCrewWorkOperations, "The operation's arguments: {\"crew_id\":\"...\",\"message\":\"...\"} or {\"crew_id\":\"...\",\"function\":\"...\",\"args\":{...}}; optional wait_seconds (max 25) or reply_crew_function_call {\"call_id\":\"...\",\"request_id\":\"...\",\"response\":\"...\"}."),
	}}
	executors := map[string]interface{}{
		"search_platform": func(ctx context.Context, args map[string]interface{}) (string, error) {
			return runPulsePlatformOperation(ctx, args, pulsePlatformReadOperations, false)
		},
		"ask_platform_crew": func(ctx context.Context, args map[string]interface{}) (string, error) {
			return runPulsePlatformOperation(ctx, args, pulseCrewWorkOperations, true)
		},
	}
	crewCallsTool, crewCallsExecutor := createCrewCallsTool()
	executors["read_crew_calls"] = crewCallsExecutor
	categories := map[string]string{"search_platform": "workflow", "ask_platform_crew": "workflow", "read_crew_calls": "workflow"}
	return []llmtypes.Tool{searchTool, crewTool, crewCallsTool}, executors, categories
}

func runPulsePlatformOperation(ctx context.Context, args map[string]interface{}, allowed map[string]bool, needWrite bool) (string, error) {
	operation, _ := args["operation"].(string)
	operation = strings.TrimSpace(operation)
	if !allowed[operation] {
		return "", fmt.Errorf("operation %q is not available here; use one of: %s", operation, strings.Join(sortedOperations(allowed), ", "))
	}
	api := pulsePlatformAPI
	if api == nil {
		return "", fmt.Errorf("platform search is unavailable in this process")
	}
	if needWrite {
		if err := refuseUnattendedCrewWork(ctx); err != nil {
			return "", err
		}
	}
	requested, _ := args["workspace_path"].(string)
	_, claims, err := api.pulseToolScope(ctx, requested, needWrite)
	if err != nil {
		return "", err
	}
	callArgs, _ := args["arguments"].(map[string]interface{})
	if callArgs == nil {
		callArgs = map[string]interface{}{}
	}
	body, err := json.Marshal(map[string]interface{}{"name": operation, "arguments": callArgs})
	if err != nil {
		return "", err
	}
	req := httptest.NewRequest(http.MethodPost, "/api/external/call", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(ctx, UserContextKey, claims))
	rec := httptest.NewRecorder()
	api.handleExternalCall(rec, req)
	out := strings.TrimSpace(rec.Body.String())
	if rec.Code >= 400 {
		return "", fmt.Errorf("%s failed (%d): %s", operation, rec.Code, out)
	}
	return out, nil
}

// refuseUnattendedCrewWork keeps Crew work out of a scheduled run's own
// conversation (the Pulse Gate, Plan Drift and finalizer turns). Pulse asks a
// Crew only from a Goal Work agent, whose tool session is admitted there
// only with the workflow's Run permission (background_review_scope.go). A
// person's Builder chat is not scheduled and keeps the tool.
func refuseUnattendedCrewWork(ctx context.Context) error {
	sessionID := strings.TrimSpace(executor.SessionIDFromContext(ctx))
	if sessionID == "" {
		sessionID, _ = ctx.Value(common.ChatSessionIDKey).(string)
	}
	if _, background := step.LookupWorkshopToolSession(sessionID); background {
		return nil
	}
	if isScheduledSession(strings.TrimSpace(sessionID)) {
		return fmt.Errorf("ask_platform_crew is not available in a scheduled run's own turns; hand Crew work to Goal Work (record_pulse_goal_work)")
	}
	return nil
}
