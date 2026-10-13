package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"
)

type functionToolsInitializationProbe struct {
	agents.OrchestratorAgent
	stop error
}

func (a *functionToolsInitializationProbe) Initialize(context.Context) error { return a.stop }
func (a *functionToolsInitializationProbe) Close() error                     { return nil }

func TestWorkflowAgentFunctionToolsReachAgentInitialization(t *testing.T) {
	env := newCrewFunctionEnv(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	scope := stepworkflow.AgentFunctionScope{WorkspacePath: "Workflow/reports", ExecutionID: "run-one", StepID: "orchestrate-icici-bank-login", Group: "default", SessionID: "agent-login"}
	tools, executors, err := env.api.workflowAgentFunctionToolsForRun(ctx)(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := agents.NewOrchestratorAgentConfig(scope.StepID)
	config.MCPSessionID = scope.SessionID
	stop := errors.New("agent initialization reached")
	var definitions []mcpagent.ToolDefinition
	// Exercise the real construction-time tool assembly, then stop before any
	// model or step code runs. A category error must never prevent initialization.
	_, err = base.CreateAndSetupStandardAgentWithConfig(ctx, config, "execution", 0, 0, scope.StepID,
		func(config *agents.OrchestratorAgentConfig, _ loggerv2.Logger, _ observability.Tracer, _ mcpagent.AgentEventListener) agents.OrchestratorAgent {
			definitions = config.DirectTools
			return &functionToolsInitializationProbe{stop: stop}
		}, tools, executors, false)
	if !errors.Is(err, stop) {
		t.Fatalf("function tools blocked agent initialization: %v", err)
	}
	if len(definitions) != 4 {
		t.Fatalf("prepared %d function tools, want 4", len(definitions))
	}
	for _, definition := range definitions {
		if definition.DisplayGroup != agentworksproduct.ExecutionFunctionTools().Category || definition.Execute == nil {
			t.Fatalf("function tool lost category or executor: %+v", definition)
		}
	}
}

func TestWorkflowAgentFunctionAdmissionReadsProductYAML(t *testing.T) {
	env := newCrewFunctionEnv(t)
	for _, toolList := range []string{"[list_functions, get_function_call]", "[]"} {
		t.Run(toolList, func(t *testing.T) {
			// A narrowed/disabled declaration must change the actual registered
			// surface, without editing the Go implementation inventory.
			manifest, err := agentprofiles.LoadProductManifest(fstest.MapFS{
				"product.yaml": {Data: []byte("schema_version: 2\nprofile: {id: test}\nprompt: {file: prompt.md}\nexecution:\n  function_tools:\n    category: workflow_calls\n    tools: " + toolList + "\n")},
				"prompt.md":    {Data: []byte("Test")},
			}, "product.yaml")
			if err != nil {
				t.Fatal(err)
			}
			reg := &recordingRegistrar{}
			def := *manifest.Execution.FunctionTools
			err = env.api.registerCrewFunctionToolSet(reg, "owner", "policy-test", QueryRequest{}, workflowTriggerLinkCaller("Workflow/reports"), nil, crewFunctionToolOptions{ExecutionScope: "policy-test", FunctionTools: def})
			if err != nil {
				t.Fatal(err)
			}
			if len(reg.tools) != len(def.Tools) {
				t.Fatalf("registered tools %v differ from product admission %v", reg.tools, def.Tools)
			}
			for _, name := range def.Tools {
				if _, ok := reg.tools[name]; !ok {
					t.Fatalf("declared tool %s was not registered", name)
				}
			}
		})
	}
}

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
	scope := stepworkflow.AgentFunctionScope{WorkspacePath: "Workflow/reports", ExecutionID: "run-one", RunFolder: "iteration-12-sched/default", StepID: "review", Group: "default", SessionID: "agent-review"}
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
	env.mock.mu.Lock()
	storedPayload := env.mock.files[linkBetaPath+"/triggers/deliveries/"+call.RunID+".json"]
	env.mock.mu.Unlock()
	var delivered map[string]interface{}
	if err := json.Unmarshal([]byte(storedPayload), &delivered); err != nil {
		t.Fatalf("saved function payload: %v", err)
	}
	for key, want := range map[string]string{
		"workflow_id": source.Stamp.ID, "workflow_run_id": scope.ExecutionID,
		"workflow_run_folder": scope.RunFolder, "iteration": "iteration-12-sched",
		"workflow_step_id": scope.StepID, "group": scope.Group,
	} {
		if delivered[key] != want {
			t.Fatalf("function payload %s = %v, want %q", key, delivered[key], want)
		}
	}
	functionPayload, _ := delivered["payload"].(map[string]interface{})
	functionArgs, _ := functionPayload["args"].(map[string]interface{})
	if len(functionArgs) != 1 || functionArgs["build"] != "7" {
		t.Fatalf("caller metadata changed validated function arguments: %v", functionArgs)
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
