package step_based_workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-174. A run read step_config.json exactly once, at run start
// (controller.go's populateRuntimeFields sweep over every plan step), and
// nothing re-read it afterward. A user's update_step_config change landed on
// disk correctly but a step that had not started yet still ran with the
// snapshot taken at run start, because "before this step started" was the
// wrong boundary — "before the run started" was the one that mattered.
// Confirmed live: customer-login pinned execute-browser-and-capture-apis to
// pi-cli/gemini-3.7-flash 16 minutes before that step began; it ran three
// times in that run and used claude-code/claude-sonnet-5-5 every time.

// fakeStepConfigServer serves step_config.json content from an in-memory
// string the test can mutate between reads, simulating update_step_config
// landing on disk mid-run without needing a real filesystem round trip.
type fakeStepConfigServer struct {
	content string
}

func newFakeStepConfigServer(t *testing.T, workspacePath string) (*httptest.Server, *fakeStepConfigServer) {
	t.Helper()
	f := &fakeStepConfigServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"success": true,
			"data": map[string]any{
				"filepath": r.URL.Path,
				"content":  f.content,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, f
}

func newConfigRefreshTestOrchestrator(t *testing.T) (*StepBasedWorkflowOrchestrator, *fakeStepConfigServer) {
	t.Helper()
	const workspacePath = "Workflow/testing"

	server, fake := newFakeStepConfigServer(t, workspacePath)
	client := workspacepkg.NewClient(server.URL)

	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(),
		nil,
		orchestrator.OrchestratorTypeWorkflow,
		"",
		0,
		"",
		nil,
		nil,
		false,
		&orchestrator.LLMConfig{},
		1,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	base.WorkspaceClient = client
	base.SetWorkspacePath(workspacePath)

	return &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}, fake
}

func stepConfigJSON(executionLLMJSON string) string {
	return `{"steps":[{"id":"execute-browser-and-capture-apis","agent_configs":{` + executionLLMJSON + `"execution_tier":"high"}}]}`
}

func TestRefreshStepAgentConfigsBeforeExecutionPicksUpAMidRunConfigChange(t *testing.T) {
	hcpo, fake := newConfigRefreshTestOrchestrator(t)
	ctx := context.Background()

	// Run start: no pin yet. populateRuntimeFields runs once, as controller.go
	// does today for every step in the plan.
	fake.content = stepConfigJSON("")
	step := &MessageSequencePlanStep{CommonStepFields: CommonStepFields{ID: "execute-browser-and-capture-apis"}}
	initialConfigs, err := hcpo.ReadStepConfigs(ctx)
	if err != nil {
		t.Fatalf("ReadStepConfigs (initial): %v", err)
	}
	if err := populateRuntimeFields(step, initialConfigs); err != nil {
		t.Fatalf("populateRuntimeFields (initial): %v", err)
	}
	if step.AgentConfigs != nil && step.AgentConfigs.ExecutionLLM != nil {
		t.Fatalf("step already has an execution_llm pin before the mid-run change: %+v", step.AgentConfigs.ExecutionLLM)
	}

	// Mid-run: update_step_config pins the step, well before it starts.
	fake.content = stepConfigJSON(`"execution_llm":{"provider":"pi-cli","model_id":"google/gemini-3.7-flash"},`)

	// The step is about to be dispatched. Without a refresh here, it would
	// still run on the pre-run-start snapshot taken above.
	hcpo.refreshStepAgentConfigsBeforeExecution(ctx, step)

	if step.AgentConfigs == nil || step.AgentConfigs.ExecutionLLM == nil {
		t.Fatal("refresh did not pick up the mid-run execution_llm pin")
	}
	if got, want := step.AgentConfigs.ExecutionLLM.Provider, "pi-cli"; got != want {
		t.Errorf("ExecutionLLM.Provider = %q, want %q", got, want)
	}
	if got, want := step.AgentConfigs.ExecutionLLM.ModelID, "google/gemini-3.7-flash"; got != want {
		t.Errorf("ExecutionLLM.ModelID = %q, want %q", got, want)
	}
}

func TestRefreshStepAgentConfigsBeforeExecutionFallsBackSilentlyWhenConfigIsUnreadable(t *testing.T) {
	hcpo, fake := newConfigRefreshTestOrchestrator(t)
	ctx := context.Background()

	fake.content = stepConfigJSON(`"execution_llm":{"provider":"pi-cli","model_id":"google/gemini-3.7-flash"},`)
	step := &MessageSequencePlanStep{CommonStepFields: CommonStepFields{ID: "execute-browser-and-capture-apis"}}
	configs, err := hcpo.ReadStepConfigs(ctx)
	if err != nil {
		t.Fatalf("ReadStepConfigs: %v", err)
	}
	if err := populateRuntimeFields(step, configs); err != nil {
		t.Fatalf("populateRuntimeFields: %v", err)
	}
	if step.AgentConfigs == nil || step.AgentConfigs.ExecutionLLM == nil {
		t.Fatal("setup failed: step should already carry the pin")
	}

	// Corrupt the served content so a re-read mid-dispatch fails. A transient
	// read failure must not blow up step dispatch or silently blank the
	// config the step already has — it should keep what run start gave it.
	fake.content = "{not json"

	hcpo.refreshStepAgentConfigsBeforeExecution(ctx, step)

	if step.AgentConfigs == nil || step.AgentConfigs.ExecutionLLM == nil {
		t.Fatal("a failed refresh must not clear the step's existing config")
	}
	if got, want := step.AgentConfigs.ExecutionLLM.ModelID, "google/gemini-3.7-flash"; got != want {
		t.Errorf("ExecutionLLM.ModelID = %q, want %q (should be unchanged after a failed refresh)", got, want)
	}
}

func TestRefreshStepAgentConfigsBeforeExecutionNoopsOnEmptyStepID(t *testing.T) {
	hcpo, fake := newConfigRefreshTestOrchestrator(t)
	ctx := context.Background()
	fake.content = stepConfigJSON("")

	step := &MessageSequencePlanStep{}
	// Must not panic or error on a step with no ID (sub-agent steps under
	// construction, orphan utility steps mid-plan-edit).
	hcpo.refreshStepAgentConfigsBeforeExecution(ctx, step)
}
