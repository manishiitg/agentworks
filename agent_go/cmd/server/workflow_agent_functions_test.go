package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"
)

// PLAT-746: private Python asks retain actual run authority, and concurrent
// questions to one Crew never read each other's explicit inbox reply.
func TestPythonAsksKeepRunAuthorityAndIndependentInboxes(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newWorkflowFunctionEnv(t)
	env.api.agentProfiles = env.svc.registry
	if err := defaultProjectOwners().SetPrivate("work", "gamma", true); err != nil {
		t.Fatal(err)
	}
	resetCrewOwnerCache()
	type askTurn struct{ actor, owner string }
	turns := make(chan askTurn, 3)
	agentMessageTurn = func(_ *StreamingAPI, ctx context.Context, _ map[string]interface{}, sessionID, owner string) (internalSessionTurnResult, error) {
		turns <- askTurn{actor: costobserver.AttributionFromContext(ctx).ActorID, owner: owner}
		return internalSessionTurnResult{FinalResponse: "Ordinary final text is not an explicit reply."}, nil
	}
	t.Cleanup(func() { agentMessageTurn = nil })
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	binding := crewExecutableBinding{ID: "python-asks", CrewID: "alpha", CrewRoot: linkAlphaPath, Root: "Workflow/python-asks", Kind: "script"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	env.mock.mu.Lock()
	env.mock.files[binding.Root+"/code/script.json"] = `{"id":"python-asks","name":"Python asks"}`
	env.mock.mu.Unlock()
	scope := stepworkflow.AgentFunctionScope{WorkspacePath: binding.Root, ExecutionID: "run-one", StepID: "ask", SessionID: "python-asks"}
	_, executors, err := env.api.workflowAgentFunctionToolsForRun(ctx)(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	ask := executors["ask_crew"].(func(context.Context, map[string]interface{}) (string, error))
	read := executors["read_agent_messages"].(func(context.Context, map[string]interface{}) (string, error))
	if _, err := ask(ctx, map[string]interface{}{"crew": "alpha", "message": "Ask myself", "submission_id": "self"}); err == nil || !strings.Contains(err.Error(), "own Crew") {
		t.Fatalf("script asked its own Crew: %v", err)
	}
	if _, err := updateWorkWorkflowReferences(ctx, linkAlphaPath, func([]string) ([]string, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := ask(ctx, map[string]interface{}{"crew": "beta", "message": "Unlinked", "submission_id": "unlinked"}); err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("script bypassed Crew link rule: %v", err)
	}
	if _, err := updateWorkWorkflowReferences(ctx, linkAlphaPath, func([]string) ([]string, error) { return []string{linkBetaPath}, nil }); err != nil {
		t.Fatal(err)
	}
	var unavailable string
	for _, name := range []string{"gamma", "unknown"} {
		_, err := ask(ctx, map[string]interface{}{"crew": name, "message": "Please review", "submission_id": name})
		if err == nil || strings.Contains(err.Error(), name) {
			t.Fatalf("private/unknown Crew leaked or accepted: %s %v", name, err)
		}
		if unavailable != "" && unavailable != err.Error() {
			t.Fatalf("private Crew existence disclosed: %q vs %q", unavailable, err)
		}
		unavailable = err.Error()
	}
	var wg sync.WaitGroup
	receipts := make([]map[string]interface{}, 2)
	errors := make([]error, 2)
	for i := range receipts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			raw, err := ask(ctx, map[string]interface{}{"crew": "Beta", "message": []string{"First question", "Second question"}[i], "submission_id": []string{"first-question", "second-question"}[i]})
			if err == nil {
				err = json.Unmarshal([]byte(raw), &receipts[i])
			}
			errors[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errors {
		if err != nil || receipts[i]["inbox_id"] == nil {
			t.Fatalf("allowed ask %d refused: %v %v", i, err, receipts[i])
		}
		select {
		case turn := <-turns:
			if turn.actor != "owner" || turn.owner != "owner" {
				t.Fatalf("same-owner attribution changed: %+v", turn)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("accepted ask never reached its agent")
		}
	}
	if receipts[0]["inbox_id"] == receipts[1]["inbox_id"] {
		t.Fatal("independent questions reused one inbox")
	}
	for i, receipt := range receipts {
		inbox := receipt["inbox_id"].(string)
		before, err := read(ctx, map[string]interface{}{"inbox_id": inbox, "wait_seconds": float64(0)})
		if err != nil || len(decodeToolJSON(t, before)["messages"].([]interface{})) != 0 {
			t.Fatalf("ordinary final text was fabricated into an answer: %s %v", before, err)
		}
		store, err := readAgentMessageSnapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, conversation := range store.Conversations {
			if conversation.ID != inbox {
				continue
			}
			peer := conversation.Endpoints[1]
			caller := triggerLinkCaller{Stamp: triggerCaller{Type: peer.Kind, ID: peer.ID, ProfileID: peer.Profile}, Label: peer.Label, Path: peer.Path, Chat: &codeChat{Key: peer.ChatKey, SessionID: peer.Session}}
			if _, err := env.api.sendAgentMessage(context.Background(), peer.UserID, caller, triggerTarget{}, []string{"First answer", "Second answer"}[i], inbox, "reply-"+inbox); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i, receipt := range receipts {
		raw, err := read(ctx, map[string]interface{}{"inbox_id": receipt["inbox_id"], "after": receipt["sequence"], "wait_seconds": float64(0)})
		if err != nil {
			t.Fatal(err)
		}
		messages := decodeToolJSON(t, raw)["messages"].([]interface{})
		if len(messages) != 1 || messages[0].(map[string]interface{})["message"] != []string{"First answer", "Second answer"}[i] {
			t.Fatalf("ask consumed another inbox reply: %s", raw)
		}
	}
	// A shared Crew executes as its own owner, while the requesting person
	// remains the cost actor and the source endpoint in the audit store.
	if err := defaultProjectOwners().SetPrivate("work", "gamma", false); err != nil {
		t.Fatal(err)
	}
	resetCrewOwnerCache()
	raw, err := ask(ctx, map[string]interface{}{"crew": "gamma", "message": "Attribution check", "submission_id": "cross-owner"})
	if err != nil {
		t.Fatal(err)
	}
	crossOwner := decodeToolJSON(t, raw)
	select {
	case turn := <-turns:
		if turn.actor != "owner" || turn.owner != "other" {
			t.Fatalf("target borrowed caller authority or lost cost actor: %+v", turn)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cross-owner ask never reached its own owner")
	}
	audit, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	audited := false
	for _, conversation := range audit.Conversations {
		if conversation.ID == crossOwner["inbox_id"] {
			audited = conversation.Endpoints[0].UserID == "owner" && conversation.Endpoints[1].UserID == "other"
		}
	}
	if !audited {
		t.Fatal("ask audit record lost source person or target owner")
	}
	// Revoke the source account after admission: a saved executor is no lasting grant.
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","disabled":true},{"id":"other","username":"other","can_create":true}]}`)
	if _, err := read(ctx, map[string]interface{}{"inbox_id": receipts[0]["inbox_id"]}); err == nil {
		t.Fatal("revoked source account still read an ask reply")
	}

}

// The older dispatcher fixture names its manifests alpha/beta/gamma. Registry
// IDs must match those manifests; the real resolver refuses mismatched IDs.
func newWorkflowFunctionEnv(t *testing.T) crewFunctionEnv {
	t.Helper()
	env := newCrewFunctionEnv(t)
	if err := defaultProjectOwners().modify(func(records map[string]projectOwnerRecord) (bool, error) {
		for key, record := range records {
			record.ProjectID = record.Folder
			records[key] = record
		}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"alpha", "beta", "gamma"} {
		if err := os.MkdirAll(filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), "_system/crew-data", id), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

type functionToolsInitializationProbe struct {
	agents.OrchestratorAgent
	stop error
}

func (a *functionToolsInitializationProbe) Initialize(context.Context) error { return a.stop }
func (a *functionToolsInitializationProbe) Close() error                     { return nil }

func TestWorkflowAgentFunctionToolsReachAgentInitialization(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newWorkflowFunctionEnv(t)
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
	env := newWorkflowFunctionEnv(t)
	for _, toolList := range []string{"[project_functions_list, project_functions_get_call]", "[]"} {
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
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newWorkflowFunctionEnv(t)
	env.svc.heldConversation = nil
	env.svc.automationTurnRunner = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		return internalSessionTurnResult{FinalResponse: "Checked build."}, nil
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	if _, err := env.alpha["project_functions_define"].exec(ctx, map[string]interface{}{
		"project": "Beta", "definition": map[string]interface{}{"name": "check_build", "description": "Check a build.", "action": map[string]interface{}{"type": "message"}, "input": map[string]interface{}{"messages": []string{"Check this build."}},
			"input_schema": map[string]interface{}{"type": "object", "required": []interface{}{"build"}, "properties": map[string]interface{}{"build": map[string]interface{}{"type": "string"}}}},
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
	args := map[string]interface{}{"project": "Beta", "function": "check_build", "input": map[string]interface{}{"build": "7"}, "wait_seconds": 3}
	if _, err := invoke(executors, "project_functions_call", map[string]interface{}{"project": "Beta", "function": "check_build"}); err == nil || !strings.Contains(err.Error(), "build") {
		t.Fatalf("missing input was not refused: %v", err)
	}
	first, err := invoke(executors, "project_functions_call", args)
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
	retry, err := invoke(executors, "project_functions_call", args)
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
		if _, err := invoke(other, "project_functions_get_call", map[string]interface{}{"call_id": id}); err == nil || !strings.Contains(err.Error(), "another execution") {
			t.Fatalf("foreign execution read result: %v", err)
		}
		fresh, err := invoke(other, "project_functions_call", args)
		if err != nil || fresh["call_id"] == id || fresh["status"] != "completed" {
			t.Fatalf("new execution reused old work: %v %v", fresh, err)
		}
	}
	args["notify"] = true
	if _, err := invoke(executors, "project_functions_call", args); err == nil || !strings.Contains(err.Error(), "notify must be false") {
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
	definitionsJSON, _ := json.Marshal(reviewPRInputDefinitions())
	env.mock.files["Workflow/pipeline/variables/variables.json"] = string(definitionsJSON)
	env.mock.mu.Unlock()
	if err := saveWorkflowEntryActions(ctx, "Workflow/pipeline", &targetManifest); err != nil {
		t.Fatal(err)
	}
	refused, err := invoke(executors, "project_functions_call", map[string]interface{}{
		"project": "Pipeline", "function": "review_pr",
		"input": map[string]interface{}{"GITHUB_OWNER": "example", "GITHUB_REPO": "app", "PR_NUMBER": 7},
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
	if _, err := invoke(executors, "project_functions_get_call", map[string]interface{}{"call_id": id}); err == nil {
		t.Fatal("revoked source workflow still read function results")
	}
}

// PLAT-746-441: converted Python scripts must act as their Crew and retain the
// same-owner link rule; the old workflow exemption must not grant new access.
func TestPythonScriptFunctionCallerRequiresCrewLinkAndKeepsRunScope(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newWorkflowFunctionEnv(t)
	t.Setenv("AUTH_SECRET", "python-function-test-signing-secret")
	env.svc.heldConversation = nil
	env.svc.automationTurnRunner = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		return internalSessionTurnResult{FinalResponse: "Checked build."}, nil
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	// The common function fixture links Alpha to Beta. This regression starts
	// unlinked so its first refusal must come from the real link rule.
	if _, err := updateWorkWorkflowReferences(ctx, linkAlphaPath, func([]string) ([]string, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	binding := crewExecutableBinding{ID: "python-source", CrewID: "alpha", CrewRoot: linkAlphaPath, Root: "Workflow/python-source", Kind: "script"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	env.mock.mu.Lock()
	env.mock.files[binding.Root+"/code/script.json"] = `{"id":"python-source","name":"Python source","capabilities":{"browser_mode":"headless"}}`
	env.mock.mu.Unlock()
	if _, err := env.alpha["project_functions_define"].exec(ctx, map[string]interface{}{
		"project": "Beta", "definition": map[string]interface{}{"name": "check_build", "description": "Check a build.", "action": map[string]interface{}{"type": "message"}, "input": map[string]interface{}{"messages": []string{"Check this build."}},
			"input_schema": map[string]interface{}{"type": "object", "required": []interface{}{"build"}, "properties": map[string]interface{}{"build": map[string]interface{}{"type": "string"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	factory := env.api.workflowAgentFunctionToolsForRun(ctx)
	scope := stepworkflow.AgentFunctionScope{WorkspacePath: binding.Root, ExecutionID: "saved-run-one", StepID: "main", SessionID: "python-function-session"}
	_, executors, err := factory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(name string, args map[string]interface{}) (map[string]interface{}, error) {
		raw, err := executors[name].(func(context.Context, map[string]interface{}) (string, error))(ctx, args)
		var value map[string]interface{}
		if err == nil {
			err = json.Unmarshal([]byte(raw), &value)
		}
		return value, err
	}
	args := map[string]interface{}{"project": "Beta", "function": "check_build", "input": map[string]interface{}{"build": "7"}, "wait_seconds": 3}
	if _, err := invoke("project_functions_call", args); err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("unlinked Crew admitted: %v", err)
	}
	if _, err := updateWorkWorkflowReferences(ctx, linkAlphaPath, func(refs []string) ([]string, error) { return append(refs, linkBetaPath), nil }); err != nil {
		t.Fatal(err)
	}
	value, err := invoke("project_functions_call", args)
	if err != nil || value["status"] != "completed" {

		t.Fatalf("linked Crew call = %v %v", value, err)
	}
	id := value["call_id"].(string)
	call := lookupCrewFunctionCall(id)
	if call.CallerKind != triggerCallerCrew || call.CallerID != "alpha" {
		t.Fatalf("script kept workflow exemption: %+v", call.snapshot())
	}
	status, err := invoke("project_functions_get_call", map[string]interface{}{"call_id": id})
	if err != nil || status["status"] != "completed" {
		t.Fatalf("authorized status failed: %v %v", status, err)
	}
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, tc := range []struct {
		name              string
		workflows, scopes []string
		allowed           bool
	}{
		{"script-id", []string{binding.ID}, []string{"runs:execute", "workflows:read", "crews:read", "crews:run"}, true},
		{"crew-id-is-not-script-id", []string{binding.CrewID}, []string{"runs:execute", "workflows:read", "crews:read", "crews:run"}, false},
		{"crew-only", nil, []string{"crews:read", "crews:run"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			token, _, err := store.Issue(ctx, accesstokens.Token{Name: tc.name, UserID: "owner", Scopes: tc.scopes, WorkflowIDs: tc.workflows, CrewIDs: []string{"alpha", "beta"}, ExpiresAt: now.Add(time.Hour)}, now)
			if err != nil {
				t.Fatal(err)
			}
			launch := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "owner", AccessToken: &token})
			_, tools, err := env.api.workflowAgentFunctionToolsForRun(launch)(launch, scope)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tools["project_functions_get_call"].(func(context.Context, map[string]interface{}) (string, error))(ctx, map[string]interface{}{"call_id": id})
			if (err == nil) != tc.allowed {
				t.Fatalf("source scope allowed=%v err=%v", tc.allowed, err)
			}
		})
	}
	if _, err := updateWorkWorkflowReferences(ctx, linkAlphaPath, func([]string) ([]string, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("project_functions_get_call", map[string]interface{}{"call_id": id}); err != nil {
		t.Fatalf("unlinking lost an existing authorized receipt: %v", err)
	}
	if _, err := invoke("project_functions_call", args); err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("unlinked Crew still started function work: %v", err)
	}
}

func TestWorkflowAgentFunctionCallsPreserveMCPConsentAndBounds(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newWorkflowFunctionEnv(t)
	// Keep the isolated state root that contains this fixture's Crew owner registry.
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
			list := tools["project_functions_list"].(func(context.Context, map[string]interface{}) (string, error))
			_, err = list(context.Background(), map[string]interface{}{"project": "Beta"})
			if (err == nil) != tc.allowed {
				t.Fatalf("Crew bounds: allowed=%v err=%v", tc.allowed, err)
			}
			if tc.name == "crew-reader" {
				call := tools["project_functions_call"].(func(context.Context, map[string]interface{}) (string, error))
				if _, err := call(context.Background(), map[string]interface{}{"project": "Beta", "function": "check_build"}); err == nil || !strings.Contains(err.Error(), "crews:run") {
					t.Fatalf("reader called a Crew function: %v", err)
				}
			}
			// Even a workflow owner cannot reach another workflow outside the grant.
			if _, err := list(context.Background(), map[string]interface{}{"project": "Pipeline"}); err == nil {
				t.Fatal("workflow connection bound was dropped")
			}
			if err := store.Revoke(ctx, token.ID, "owner", time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := list(context.Background(), map[string]interface{}{"project": "Beta"}); err == nil {
				t.Fatal("revoked MCP connection still used runtime function tools")
			}
		})
	}
}
