package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

func TestWorkflowAgentFunctionCallsIdentityResultsAndRetryScope(t *testing.T) {
	env := newCrewFunctionEnv(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env.svc.heldConversation = nil
	env.svc.automationTurnRunner = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		return internalSessionTurnResult{FinalResponse: "Checked build."}, nil
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	if _, err := env.alpha["define_function"].exec(ctx, map[string]interface{}{
		"target": "Beta", "name": "check_build", "description": "Check a build.", "instructions": "Check this build.",
		"input_schema": map[string]interface{}{"type": "object", "required": []interface{}{"build"}, "properties": map[string]interface{}{"build": map[string]interface{}{"type": "string"}}},
	}); err != nil {
		t.Fatal(err)
	}
	factory := env.api.workflowAgentFunctionToolsForRun(ctx)
	scope := stepworkflow.AgentFunctionScope{WorkspacePath: "Workflow/reports", ExecutionID: "run-one", StepID: "review", Group: "default", SessionID: "agent-review"}
	tools, executors, err := factory(ctx, scope)
	if err != nil || len(tools) != 4 {
		t.Fatalf("execution tool surface: %v %v", tools, err)
	}
	invoke := func(executors map[string]interface{}, name string, args map[string]interface{}) (map[string]interface{}, error) {
		t.Helper()
		raw, err := executors[name].(func(context.Context, map[string]interface{}) (string, error))(ctx, args)
		var result map[string]interface{}
		if err == nil {
			err = json.Unmarshal([]byte(raw), &result)
		}
		return result, err
	}
	args := map[string]interface{}{"target": "Beta", "function": "check_build", "args": map[string]interface{}{"build": "7"}, "wait_seconds": 3}
	if _, err := invoke(executors, "call_function", map[string]interface{}{"target": "Beta", "function": "check_build"}); err == nil || !strings.Contains(err.Error(), "build") {
		t.Fatalf("missing input was not refused: %v", err)
	}
	first, err := invoke(executors, "call_function", args)
	if err != nil || first["status"] != "completed" || first["answer"] != "Checked build." || first["auto_notification"] != nil {
		t.Fatalf("function outcome = %v %v", first, err)
	}
	id := first["call_id"].(string)
	call := lookupCrewFunctionCall(id)
	source, err := workflowTriggerLinkCaller(scope.WorkspacePath)(ctx)
	if err != nil || call.CallerKind != triggerCallerWorkflow || call.CallerID != source.Stamp.ID || call.ExecutionScope == "" {
		t.Fatalf("caller stamp = %+v, %v", call.snapshot(), err)
	}
	// Completed calls and retries survive the same saved-record path as MCP.
	crewFunctionCalls.Lock()
	delete(crewFunctionCalls.m, id)
	crewFunctionCalls.Unlock()
	retry, err := invoke(executors, "call_function", args)
	if err != nil || retry["call_id"] != id {
		t.Fatalf("retry after reload = %v %v", retry, err)
	}
	for _, changed := range []stepworkflow.AgentFunctionScope{
		{WorkspacePath: scope.WorkspacePath, ExecutionID: "run-two", StepID: scope.StepID, Group: scope.Group, SessionID: "agent-review-two"},
		{WorkspacePath: scope.WorkspacePath, ExecutionID: scope.ExecutionID, StepID: scope.StepID, Group: "second", SessionID: "agent-review-second"},
	} {
		_, other, err := factory(ctx, changed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := invoke(other, "get_function_call", map[string]interface{}{"call_id": id}); err == nil || !strings.Contains(err.Error(), "another execution") {
			t.Fatalf("foreign execution read result: %v", err)
		}
		fresh, err := invoke(other, "call_function", args)
		if err != nil || fresh["call_id"] == id || fresh["status"] != "completed" {
			t.Fatalf("new execution reused old work: %v %v", fresh, err)
		}
	}
	args["notify"] = true
	if _, err := invoke(executors, "call_function", args); err == nil || !strings.Contains(err.Error(), "notify must be false") {
		t.Fatalf("execution installed a chat completion watcher: %v", err)
	}
	// A user allow-list must not admit the same user's workflow caller.
	env.mock.mu.Lock()
	var targetManifest WorkflowManifest
	_ = json.Unmarshal([]byte(env.mock.files[manifestPath("Workflow/pipeline")]), &targetManifest)
	trigger := reviewPRTrigger()
	trigger.Function.AllowedCallers = []triggerCaller{{Type: triggerCallerUser, ID: "owner"}}
	targetManifest.Schedules = []WorkflowSchedule{trigger}
	targetJSON, _ := json.Marshal(targetManifest)
	env.mock.files[manifestPath("Workflow/pipeline")] = string(targetJSON)
	env.mock.mu.Unlock()
	refused, err := invoke(executors, "call_function", map[string]interface{}{
		"target": "Pipeline", "function": "review_pr",
		"args": map[string]interface{}{"GITHUB_OWNER": "example", "GITHUB_REPO": "app", "PR_NUMBER": 7, "group": "staging"},
	})
	if err != nil || refused["status"] != "failed" || !strings.Contains(refused["error"].(string), "no longer names this caller") || refused["run_id"] != "" {
		t.Fatalf("workflow function admitted the wrong caller kind: %v %v", refused, err)
	}
	env.mock.mu.Lock()
	var manifest WorkflowManifest
	_ = json.Unmarshal([]byte(env.mock.files[manifestPath(scope.WorkspacePath)]), &manifest)
	manifest.Access = &WorkflowAccess{Owners: []string{"other"}}
	encoded, _ := json.Marshal(manifest)
	env.mock.files[manifestPath(scope.WorkspacePath)] = string(encoded)
	env.mock.mu.Unlock()
	if _, err := invoke(executors, "get_function_call", map[string]interface{}{"call_id": id}); err == nil {
		t.Fatal("revoked source workflow still read function results")
	}
}

func TestWorkflowAgentFunctionCallsPreserveMCPConsentAndBounds(t *testing.T) {
	env := newCrewFunctionEnv(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("AUTH_SECRET", "workflow-function-test-signing-secret")
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	source, err := workflowTriggerLinkCaller("Workflow/reports")(ctx)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, tc := range []struct {
		name          string
		scopes, crews []string
		allowed       bool
	}{
		{"workflow-only", []string{"runs:execute", "workflows:read", "files:read"}, nil, false},
		{"wrong-crew", []string{"runs:execute", "workflows:read", "files:read", "crews:read", "crews:run"}, []string{"alpha"}, false},
		{"crew-reader", []string{"runs:execute", "workflows:read", "files:read", "crews:read"}, []string{"beta"}, true},
		{"allowed-crew", []string{"runs:execute", "workflows:read", "files:read", "crews:read", "crews:run"}, []string{"beta"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			token, _, err := store.Issue(ctx, accesstokens.Token{Name: tc.name, UserID: "owner", Scopes: tc.scopes, WorkflowIDs: []string{source.Stamp.ID}, CrewIDs: tc.crews, ExpiresAt: now.Add(time.Hour)}, now)
			if err != nil {
				t.Fatal(err)
			}
			launch := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "owner", AccessToken: &token})
			factory := env.api.workflowAgentFunctionToolsForRun(launch)
			_, tools, err := factory(launch, stepworkflow.AgentFunctionScope{WorkspacePath: "Workflow/reports", ExecutionID: "mcp-run", StepID: "review", SessionID: "mcp-agent"})
			if err != nil {
				t.Fatal(err)
			}
			list := tools["list_functions"].(func(context.Context, map[string]interface{}) (string, error))
			_, err = list(context.Background(), map[string]interface{}{"target": "Beta"})
			if (err == nil) != tc.allowed {
				t.Fatalf("Crew bounds: allowed=%v err=%v", tc.allowed, err)
			}
			if tc.name == "crew-reader" {
				call := tools["call_function"].(func(context.Context, map[string]interface{}) (string, error))
				if _, err := call(context.Background(), map[string]interface{}{"target": "Beta", "function": "check_build"}); err == nil || !strings.Contains(err.Error(), "crews:run") {
					t.Fatalf("reader called a Crew function: %v", err)
				}
			}
			// Even a workflow owner cannot reach another workflow outside the grant.
			if _, err := list(context.Background(), map[string]interface{}{"target": "Pipeline"}); err == nil {
				t.Fatal("workflow connection bound was dropped")
			}
			if err := store.Revoke(ctx, token.ID, "owner", time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := list(context.Background(), map[string]interface{}{"target": "Beta"}); err == nil {
				t.Fatal("revoked MCP connection still used runtime function tools")
			}
		})
	}
}
