package step_based_workflow

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestWebhookRunBindingKeepsRouteGroupAndHumanInputsScoped(t *testing.T) {
	// Omitted selection permits the resolver's unnamed invocation; an
	// explicit empty selection must never turn into automatic work.
	if _, err := (&WebhookInvocation{}).RoutesForGroup(""); err != nil {
		t.Fatal(err)
	}
	if _, err := (&WebhookInvocation{GroupNames: []string{}}).RoutesForGroup(""); err == nil {
		t.Fatal("empty selection allowed an unnamed invocation")
	}
	binding := &WebhookInvocation{InputFile: "Workflow/testing/webhooks/deliveries/run.json", RouteSelections: map[string]string{"router": "issues"}, GroupNames: []string{"prod"}}
	if _, err := binding.RoutesForGroup("other"); err == nil {
		t.Fatal("unconfigured group accepted")
	}
	routes, err := binding.RoutesForGroup("prod")
	if err != nil {
		t.Fatal(err)
	}
	routes["router"] = "other"
	if binding.RouteSelections["router"] != "issues" {
		t.Fatal("tool can mutate saved route binding")
	}
	var plan PlanningResponse
	if err = json.Unmarshal([]byte(`{"steps":[{"type":"scripted","id":"work","title":"Work","description":"Work"},{"type":"agent","id":"approve","title":"Approval","description":"Use human_feedback if needed"}]}`), &plan); err != nil {
		t.Fatal(err)
	}
	original := map[string]string{"work": "Existing instruction"}
	inputs := attachWebhookStepInputs(plan.Steps, original, binding.InputFile)
	if !strings.Contains(inputs["approve"], binding.InputFile) {
		t.Fatal("agent step did not receive payload context")
	}
	if !strings.Contains(inputs["work"], binding.InputFile) || !strings.Contains(inputs["work"], original["work"]) {
		t.Fatalf("missing step context: %v", inputs)
	}
	if original["work"] != "Existing instruction" {
		t.Fatal("caller inputs mutated")
	}
}

func TestWebhookInputFileReachesScriptEnvironmentAndSandbox(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath("Workflow/testing")
	controller := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, selectedRunFolder: "iteration-0/prod"}
	inputFile := "Workflow/testing/webhooks/deliveries/run.json"
	controller.SetExecutionOptions(&ExecutionOptions{WebhookInputFile: inputFile, TriggerContextFile: "Workflow/testing/context.json"})
	contextFile := "Workflow/testing/context.json"
	readPaths, writePaths := controller.setupExecutionFolderGuard("step-1", "work", KBAccessNone, LearningsAccessNone, "none", nil)
	if !slices.Contains(readPaths, inputFile) || slices.Contains(writePaths, inputFile) {
		t.Fatalf("delivery grants: read=%v write=%v", readPaths, writePaths)
	}
	if !slices.Contains(readPaths, contextFile) || slices.Contains(writePaths, contextFile) {
		t.Fatal("context must be read-only")
	}
	env := controller.codeRuntimeEnv(map[string]string{"WORKFLOW_TRIGGER_INPUT_FILE": "stale"})
	if env["WORKFLOW_TRIGGER_INPUT_FILE"] != filepath.Join(GetPromptDocsRoot(), inputFile) {
		t.Fatalf("input env=%v", env)
	}
	if stepRuntimeEnv(env)["WORKFLOW_TRIGGER_INPUT_FILE"] == "" {
		t.Fatal("agent shell filters trigger input")
	}
	if env["WORKFLOW_TRIGGER_CONTEXT_FILE"] != filepath.Join(GetPromptDocsRoot(), contextFile) || stepRuntimeEnv(env)["WORKFLOW_TRIGGER_CONTEXT_FILE"] == "" {
		t.Fatal("context missing from step environment")
	}
	controller.SetExecutionOptions(nil)
	if _, exists := controller.codeRuntimeEnv(env)["WORKFLOW_TRIGGER_INPUT_FILE"]; exists {
		t.Fatal("delivery leaked into a later ordinary run")
	}
	var parsed ExecutionOptions
	if err = json.Unmarshal([]byte(`{"WebhookInputFile":"evil","webhook_input_file":"evil","TriggerContextFile":"evil","trigger_context_file":"evil"}`), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.WebhookInputFile != "" || parsed.TriggerContextFile != "" {
		t.Fatal("public JSON can inject internal input path")
	}
}
