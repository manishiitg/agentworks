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

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Pulse Goal Work looks beyond its own workflow: other workflows the owner can
// see (their plans, runs, files and knowledge) and the Crews on this server.
// Both tools reuse the external API in-process, as the workflow's owner, so
// visibility and access are exactly what that person gets from the agentworks
// CLI; nothing here has its own permission logic.

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
var pulseCrewWorkOperations = map[string]bool{"ask_crew": true, "call_crew_function": true}

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
				"workspace_path": map[string]interface{}{"type": "string", "description": "This workflow's path, e.g. Workflow/linkedin. The call runs with its owner's access."},
				"operation":      map[string]interface{}{"type": "string", "enum": sortedOperations(ops)},
				"arguments":      map[string]interface{}{"type": "object", "description": argsDescription},
			},
			"required": []string{"workspace_path", "operation"},
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
			"Returns the result if it finishes within wait_seconds, else a call_id for search_platform get_crew_function_call. Anything the Crew would post, send or contact outside follows this workflow's outward permission: when that is ask, request only preparation and put the outward step in a decision.",
		Parameters: params(pulseCrewWorkOperations, "The operation's arguments: {\"crew_id\":\"...\",\"message\":\"...\"} or {\"crew_id\":\"...\",\"function\":\"...\",\"args\":{...}}; optional wait_seconds (max 25)."),
	}}
	executors := map[string]interface{}{
		"search_platform":   func(ctx context.Context, args map[string]interface{}) (string, error) { return runPulsePlatformOperation(ctx, args, pulsePlatformReadOperations) },
		"ask_platform_crew": func(ctx context.Context, args map[string]interface{}) (string, error) { return runPulsePlatformOperation(ctx, args, pulseCrewWorkOperations) },
	}
	crewCallsTool, crewCallsExecutor := createCrewCallsTool()
	executors["read_crew_calls"] = crewCallsExecutor
	categories := map[string]string{"search_platform": "workflow", "ask_platform_crew": "workflow", "read_crew_calls": "workflow"}
	return []llmtypes.Tool{searchTool, crewTool, crewCallsTool}, executors, categories
}

func runPulsePlatformOperation(ctx context.Context, args map[string]interface{}, allowed map[string]bool) (string, error) {
	operation, _ := args["operation"].(string)
	operation = strings.TrimSpace(operation)
	if !allowed[operation] {
		return "", fmt.Errorf("operation %q is not available here; use one of: %s", operation, strings.Join(sortedOperations(allowed), ", "))
	}
	api := pulsePlatformAPI
	if api == nil {
		return "", fmt.Errorf("platform search is unavailable in this process")
	}
	workspacePath, _ := args["workspace_path"].(string)
	claims, err := pulsePlatformClaims(ctx, workspacePath)
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

// pulsePlatformClaims is the principal a Pulse platform call acts as: the
// caller already on the context, else the workflow's first owner.
func pulsePlatformClaims(ctx context.Context, workspacePath string) (*UserClaims, error) {
	if claims := GetUserFromContext(ctx); claims != nil && strings.TrimSpace(claims.UserID) != "" {
		copy := *claims
		return &copy, nil
	}
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return nil, fmt.Errorf("workspace_path is required")
	}
	manifest, ok, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !ok {
		return nil, fmt.Errorf("cannot read the workflow at %s to find its owner", workspacePath)
	}
	userID := GetDefaultUserID()
	if owners := manifest.effectiveOwners(); len(owners) > 0 && strings.TrimSpace(owners[0]) != "" {
		userID = strings.TrimSpace(owners[0])
	}
	claims := &UserClaims{UserID: userID}
	if record := directoryUserFor(userID, "", ""); record != nil {
		claims.Username, claims.Email = record.Username, record.Email
	}
	return claims, nil
}
