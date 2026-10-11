package step_based_workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestScriptedDelegationEnvIsChildScopedAndDoesNotMutateSharedEnv(t *testing.T) {
	shared := map[string]string{"VAR_ACCOUNT": "acme"}
	ctxA := withScriptedDelegationContext(context.Background(), "route-a", "todo-a", "inspect region A", nil)
	ctxB := withScriptedDelegationContext(context.Background(), "route-b", "todo-b", "inspect region B", nil)

	envA := appendScriptedDelegationEnv(ctxA, shared)
	envB := appendScriptedDelegationEnv(ctxB, shared)

	if _, leaked := shared[ScriptedDelegationInstructionsEnv]; leaked {
		t.Fatal("delegation leaked into the shared workspace environment")
	}
	if got := envA[ScriptedDelegationInstructionsEnv]; got != "inspect region A" {
		t.Fatalf("child A instructions = %q", got)
	}
	if got := envB[ScriptedDelegationInstructionsEnv]; got != "inspect region B" {
		t.Fatalf("child B instructions = %q", got)
	}
	if envA[ScriptedDelegationRouteIDEnv] != "route-a" || envA[ScriptedDelegationTodoIDEnv] != "todo-a" {
		t.Fatalf("child A route identity missing: %#v", envA)
	}
	if envB[ScriptedDelegationRouteIDEnv] != "route-b" || envB[ScriptedDelegationTodoIDEnv] != "todo-b" {
		t.Fatalf("child B route identity missing: %#v", envB)
	}
}

// This is the exact saved-script orchestrator-route regression. It crosses the
// same boundary production does: a per-call route decision is put on the child
// context, the saved-script runner builds a real workspace shell request, and
// the workspace client serializes it to /api/execute. The assertion is against
// that request, not a helper return value. Positional argv is checked too so the
// new contract cannot disturb existing context_dependency ordering.
func TestScriptedStepInvokedAsOrchestratorRouteReceivesDelegationEndToEnd(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())

	var received workspacepkg.ExecuteShellCommandParams
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/execute":
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Fatalf("decode shell request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"data":{"stdout":"route complete","stderr":"","exit_code":0}}`))
		case r.URL.Path == "/api/folders" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"success":true}`))
		case r.URL.Path == "/api/documents" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"message":"Folder exists but contains no files","data":[]}`))
		case strings.HasSuffix(r.URL.Path, "/code/scripted-child/main.py") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"data":{"filepath":"code/scripted-child/main.py","content":"import json, os\nprint(json.loads(os.environ['STEP_PARAMS_JSON'])['market'])\n"}}`))
		case strings.HasSuffix(r.URL.Path, "/execution/source/input.json") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"data":{"filepath":"execution/source/input.json","content":"{}"}}`))
		case strings.HasPrefix(r.URL.Path, "/api/documents/") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"message":"File does not exist","data":{"content":""}}`))
		case strings.HasPrefix(r.URL.Path, "/api/documents/") && (r.Method == http.MethodPut || r.Method == http.MethodDelete):
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "",
		nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	base.WorkspaceClient = workspacepkg.NewClient(server.URL)
	base.SetWorkspacePath("Workflow/delegation-e2e")
	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator:  base,
		selectedRunFolder: "iteration-1/default",
	}
	hcpo.codeLayoutVersion.Store(1)

	child := &RegularPlanStep{
		Type: StepTypeRegular,
		CommonStepFields: CommonStepFields{
			ID:    "scripted-child",
			Title: "Scripted child",
			ScriptParameters: map[string]ScriptParameterDefinition{
				"market": {Type: "string", Description: "Market to process", Required: true},
			},
		},
	}
	dependency := filepath.Join(GetPromptDocsRoot(), "Workflow/delegation-e2e/runs/iteration-1/default/execution/source/input.json")
	child.ContextDependencies = []string{dependency}
	parent := &AgentPlanStep{
		Type: StepTypeAgent,
		CommonStepFields: CommonStepFields{
			ID:    "parent",
			Title: "Parent",
		},
		PredefinedRoutes: []PlanOrchestrationRoute{{
			RouteID:      "route-a",
			RouteName:    "Route A",
			SubAgentStep: child,
		}},
	}
	decision := &AgentDelegationDecision{
		SelectedRouteID:  "route-a",
		TodoIDToExecute:  "todo-42",
		ScriptParameters: map[string]interface{}{"market": "dubai"},
	}
	output, _, err := hcpo.executePredefinedSubAgent(
		t.Context(), parent, 0, "step-1", decision, []PlanStepInterface{parent},
		&StepProgress{TotalSteps: 1}, nil,
	)
	if err != nil || !strings.Contains(output, "route complete") {
		t.Fatalf("scripted orchestrator route result=(%q,%v)", output, err)
	}
	if got := received.ExtraEnv[ScriptedParametersEnv]; got != `{"market":"dubai"}` {
		t.Fatalf("script parameters did not reach saved script: %q", got)
	}
	if received.ExtraEnv[ScriptedDelegationRouteIDEnv] != "route-a" || received.ExtraEnv[ScriptedDelegationTodoIDEnv] != "todo-42" {
		t.Fatalf("delegation identity did not reach saved script: %#v", received.ExtraEnv)
	}
	wantInvocation := "'" + dependency + "'"
	if !strings.Contains(received.Command, wantInvocation) {
		t.Fatalf("declared dependency argv changed or disappeared: command=%q, want %q", received.Command, wantInvocation)
	}
	if strings.Contains(received.Command, "dubai") {
		t.Fatalf("parameters must not be appended to positional argv: %q", received.Command)
	}
}

func TestScriptedDelegationAuthoringPromptDocumentsRuntimeContract(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}
	prompt := agent.executionOnlySystemPromptProcessor(map[string]string{
		"IsCodeExecutionMode":     "true",
		"IsScriptedMode":          "true",
		"StepExecutionPath":       "/docs/Workflow/test/runs/run/execution/child",
		"ScriptedWorkingDir":      "/docs/Workflow/test/code/child",
		"ScriptedEnvVarNames":     ScriptedParametersEnv + "\n" + ScriptedDelegationRouteIDEnv + "\n" + ScriptedDelegationTodoIDEnv,
		"ScriptedParameterSchema": `{"market":{"type":"string","description":"Market to process","required":true}}`,
		"ScriptedParameterValues": `{"market":"dubai"}`,
	})
	for _, want := range []string{
		"Script parameter contract",
		"`STEP_PARAMS_JSON`",
		"Do not hardcode current parameter values",
		"Positional arguments remain reserved for declared context dependencies",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("scripted authoring prompt missing %q:\n%s", want, prompt)
		}
	}
}
