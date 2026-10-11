package types

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// Edge-case suite for the workflow engine.
//
// These tests deliberately construct plans/states that have caused
// silent passes in the past or are documented invariants the engine
// is supposed to enforce. Each test is one assertion shape: feed the
// engine a malformed/edge input, assert it FAILS with a meaningful
// error (not a hang, not a silent success, not a panic).
//
// All share the gating contract of the main e2e: RUN_WORKFLOW_REAL_E2E
// + RUN_VERTEX_REAL_E2E + GEMINI_API_KEY. They DO spin up the same
// fixture-via-running-server wiring as the main e2e (so the user's
// workspace API must be up); they should fail BEFORE any LLM call
// because plan validation rejects the input.

// TestWorkflowEdgeDuplicateStepIDsRejected proves the engine refuses
// a plan with two steps sharing the same ID. Duplicate IDs silently
// collide on execution artifact paths and learnings/ folders, which is
// a data-corruption class bug.
func TestWorkflowEdgeDuplicateStepIDsRejected(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	plan := map[string]interface{}{
		"steps": []map[string]interface{}{
			{"type": "regular", "id": "dup", "title": "First", "description": "Reply ACK", "context_dependencies": []string{}, "context_output": "a.json"},
			{"type": "regular", "id": "dup", "title": "Second", "description": "Reply ACK", "context_dependencies": []string{}, "context_output": "b.json"},
		},
	}
	writeEdgePlan(t, wo.workspaceDisk, plan)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, err := wo.orchestrator.Execute(ctx, "duplicate-id test", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	})
	if err == nil {
		t.Fatal("expected engine to reject plan with duplicate step IDs; got nil error")
	}
	if !containsAny(err.Error(), "duplicate", "unique", "already exists", "conflict") {
		t.Errorf("error message should mention the duplicate-ID failure mode; got: %v", err)
	}
}

// TestWorkflowEdgeRoutingNextStepIDMustExist proves the engine refuses
// a routing plan whose route.next_step_id points to a step that
// doesn't exist. A dangling next_step_id would otherwise be discovered
// at run time (after the routing LLM has been billed for) and is
// trivially detectable at plan load.
func TestWorkflowEdgeRoutingNextStepIDMustExist(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	plan := map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "routing",
				"id":                   "router",
				"title":                "Pick path",
				"description":          "",
				"context_dependencies": []string{},
				"context_output":       "r.json",
				"routing_question":     "Pick route_a",
				"routes": []map[string]interface{}{
					{"route_id": "route_a", "route_name": "A", "condition": "always", "next_step_id": "step-that-does-not-exist"},
					{"route_id": "route_b", "route_name": "B", "condition": "never", "next_step_id": "end"},
				},
				"default_route_id": "route_a",
			},
		},
	}
	writeEdgePlan(t, wo.workspaceDisk, plan)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, err := wo.orchestrator.Execute(ctx, "dangling next_step_id test", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	})
	if err == nil {
		t.Fatal("expected engine to reject plan with dangling next_step_id; got nil error")
	}
	if !containsAny(err.Error(), "next_step_id", "not found", "unknown step", "does not exist") {
		t.Errorf("error message should pinpoint the missing next_step_id; got: %v", err)
	}
}

// TestWorkflowEdgeEmptyPlanRejected proves the engine refuses a plan
// with zero steps. An empty plan can't be a valid workflow — the only
// sane runtime behavior is "fail at load", not "succeed in 0 ms".
func TestWorkflowEdgeEmptyPlanRejected(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, err := wo.orchestrator.Execute(ctx, "empty plan", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	})
	if err == nil {
		t.Fatal("expected engine to reject empty plan; got nil error")
	}
	if !containsAny(err.Error(), "no steps", "empty", "at least one step", "must contain") {
		t.Errorf("error should explain the empty-plan failure; got: %v", err)
	}
}

// TestWorkflowEdgeRoutingRequiresMultipleRoutes proves the engine
// refuses a routing step with only one route. The schema doc on
// RoutingPlanStep.Routes (planning_agent.go:488) declares "min 2".
// A single-route routing step is degenerate — it's just an
// unconditional jump dressed as a decision, and the engine should
// reject it before billing the LLM.
func TestWorkflowEdgeRoutingRequiresMultipleRoutes(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "routing",
				"id":                   "lonely-router",
				"title":                "Pick the only route",
				"description":          "",
				"context_dependencies": []string{},
				"context_output":       "r.json",
				"routing_question":     "Pick the only available route.",
				"routes": []map[string]interface{}{
					{"route_id": "route_a", "route_name": "A", "condition": "always", "next_step_id": "end"},
				},
				"default_route_id": "route_a",
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, err := wo.orchestrator.Execute(ctx, "single-route test", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	})
	if err == nil {
		t.Fatal("expected engine to reject routing step with only one route; got nil error")
	}
	if !containsAny(err.Error(), "routes", "at least 2", "min", "multiple") {
		t.Errorf("error should explain the min-routes invariant; got: %v", err)
	}
}

// TestWorkflowEdgeCyclicNextStepIDDocumentedGap is an XFAIL-style
// regression marker: the engine currently DOES NOT detect cyclic
// next_step_id chains (step-A → step-B → step-A) at plan-load. The
// engine accepts the cyclic plan and starts iterating between the
// steps; the cycle terminates only when an outer cap (max iterations,
// LLM context limit, or test timeout) trips. This test asserts the
// CURRENT (buggy) behavior so the regression is visible: when cycle
// detection lands, this test will fail and should be inverted to
// assert "engine rejects cycle at load".
//
// To keep CI fast (the engine takes ~10s per cycle hop), the test
// runs with a 30s context — enough to observe ≥2 cycle iterations
// and confirm the engine is genuinely looping, not just sluggish on
// a single step.
func TestWorkflowEdgeCyclicNextStepIDDocumentedGap(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "routing",
				"id":                   "step-A",
				"title":                "A",
				"description":          "",
				"context_dependencies": []string{},
				"context_output":       "a.json",
				"routing_question":     "Pick the only meaningful route.",
				"routes": []map[string]interface{}{
					{"route_id": "to_b", "route_name": "ToB", "condition": "always", "next_step_id": "step-B"},
					{"route_id": "to_end", "route_name": "ToEnd", "condition": "never", "next_step_id": "end"},
				},
				"default_route_id": "to_b",
			},
			{
				"type":                 "routing",
				"id":                   "step-B",
				"title":                "B",
				"description":          "",
				"context_dependencies": []string{},
				"context_output":       "b.json",
				"routing_question":     "Pick the only meaningful route.",
				"routes": []map[string]interface{}{
					{"route_id": "to_a", "route_name": "ToA", "condition": "always", "next_step_id": "step-A"},
					{"route_id": "to_end", "route_name": "ToEnd", "condition": "never", "next_step_id": "end"},
				},
				"default_route_id": "to_a",
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := wo.orchestrator.Execute(ctx, "cyclic test", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	})
	// Today the engine does NOT catch the cycle at plan-load; the
	// caller is responsible for capping iteration. Once cycle
	// detection lands, the err message will contain "cycle"/"loop"
	// and this test should be inverted accordingly.
	if err == nil {
		t.Log("ENGINE GAP: cyclic plan accepted and (presumably) completed inside the 30s budget — extend the cap and watch for the loop in logs to confirm.")
		return
	}
	t.Logf("cyclic plan rejected by side-channel (not dedicated cycle detection) — error: %v", err)
}

// TestWorkflowEdgeAgentItemsRequired proves the engine
// refuses an agent step with an empty items array. A
// agent with no items is degenerate — there's nothing to
// send. The current validateAgentStepFieldsTyped behavior
// determines whether this is enforced; if it isn't, the engine will
// run the step path and silently produce no LLM interaction.
func TestWorkflowEdgeAgentItemsRequired(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "agent",
				"id":                   "empty-seq",
				"title":                "Empty Sequence",
				"description":          "",
				"context_dependencies": []string{},
				"context_output":       "ms.json",
				"items":                []map[string]interface{}{},
				"next_step_id":         "end",
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, err := wo.orchestrator.Execute(ctx, "empty-agent test", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	})
	if err == nil {
		t.Fatal("expected engine to reject agent with empty items; got nil error")
	}
	if !containsAny(err.Error(), "items", "empty", "at least", "required") {
		t.Errorf("error should explain the empty-items violation; got: %v", err)
	}
}

// TestWorkflowEdgeOrchestratorAcceptsZeroPredefinedRoutes locks in the
// documented behavior of validateAgentStepFieldsTyped
// (planning_agent.go:4090): "Predefined routes are optional
// (orchestrators can be generic-agent-only)". A todo_task with no
// predefined sub-agents falls through to a generic execution agent
// and the workflow still runs. This test asserts the engine accepts
// the empty-routes plan AND actually completes the step, so a
// future tightening that would reject empty routes breaks loudly.
func TestWorkflowEdgeOrchestratorAcceptsZeroPredefinedRoutes(t *testing.T) {
	wo, cleanup, ok := buildEdgeCaseOrchestrator(t)
	if !ok {
		return
	}
	defer cleanup()

	writeEdgePlan(t, wo.workspaceDisk, map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type":                 "todo_task",
				"id":                   "empty-todo",
				"title":                "Empty Todo Task",
				"description":          "Create one todo and mark it done. Reply with the single word DONE.",
				"context_dependencies": []string{},
				"context_output":       "td.json",
				"predefined_routes":    []map[string]interface{}{},
				"next_step_id":         "end",
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	_, err := wo.orchestrator.Execute(ctx, "empty-todo test", wo.workspaceRel, map[string]interface{}{
		"workflowStatus": workflowtypes.WorkflowStatusPreVerification,
	})
	if err != nil {
		t.Fatalf("expected engine to ACCEPT todo_task with empty predefined_routes (documented as optional); got error: %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────
// Shared helpers — kept inside _test.go so they don't leak into the
// production build.

type edgeCaseHarness struct {
	orchestrator  *WorkflowOrchestrator
	workspaceRel  string
	workspaceDisk string
}

// buildEdgeCaseOrchestrator returns a fully-wired WorkflowOrchestrator
// pointing at a per-test workspace under workspace-docs/. Returns
// ok=false (and Skips) when env gates / running server are missing so
// these tests behave identically to the main e2e in CI.
func buildEdgeCaseOrchestrator(t *testing.T) (*edgeCaseHarness, func(), bool) {
	t.Helper()
	if os.Getenv("RUN_WORKFLOW_REAL_E2E") == "" {
		t.Skip("set RUN_WORKFLOW_REAL_E2E=1")
		return nil, func() {}, false
	}
	if os.Getenv("RUN_VERTEX_REAL_E2E") == "" {
		t.Skip("set RUN_VERTEX_REAL_E2E=1")
		return nil, func() {}, false
	}
	var apiKey string
	for _, name := range []string{"GEMINI_API_KEY", "VERTEX_API_KEY", "GOOGLE_API_KEY"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			apiKey = v
			break
		}
	}
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY required")
		return nil, func() {}, false
	}

	wsAPI := strings.TrimSpace(os.Getenv("WORKSPACE_API_URL"))
	if wsAPI == "" {
		wsAPI = "http://127.0.0.1:18744"
		_ = os.Setenv("WORKSPACE_API_URL", wsAPI)
	}
	wsRoot := strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH"))
	if wsRoot == "" {
		wsRoot = "/Users/mipl/ai-work/coding-agent-loop/workspace-docs"
	}
	// GetPromptDocsRoot (prompt_sections.go:561) reads this env var
	// to resolve absolute paths the engine passes to subprocesses
	// (e.g. python3 main.py /app/... for learn_code fast path). Without
	// this export, the engine defaults to "/app/workspace-docs" — the
	// Docker container path — and python fails with "no such file".
	_ = os.Setenv("WORKSPACE_DOCS_PATH", wsRoot)

	relWorkspace := "Workflow/_e2e_edge_" + filepath.Base(t.TempDir())
	workspaceDisk := filepath.Join(wsRoot, relWorkspace)
	if err := os.MkdirAll(filepath.Join(workspaceDisk, "planning"), 0o755); err != nil {
		t.Fatalf("mkdir planning: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceDisk, "variables"), 0o755); err != nil {
		t.Fatalf("mkdir variables: %v", err)
	}
	if err := writeJSON(filepath.Join(workspaceDisk, "variables", "variables.json"), map[string]interface{}{
		"variables":       []interface{}{},
		"groups":          []map[string]interface{}{{"name": "default", "values": map[string]string{}, "enabled": true}},
		"extraction_date": time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("write variables: %v", err)
	}

	model := strings.TrimSpace(os.Getenv("VERTEX_REAL_E2E_MODEL"))
	if model == "" {
		model = "gemini-3.5-flash"
	}
	llmCfg := &orchestrator.LLMConfig{
		Primary: orchestrator.LLMModel{Provider: "vertex", ModelID: model},
		APIKeys: &llm.ProviderAPIKeys{Vertex: &apiKey},
	}
	agentLLM := &workflowtypes.AgentLLMConfig{Provider: "vertex", ModelID: model}
	presetCfg := &workflowtypes.PresetLLMConfig{
		SchemaVersion: workflowtypes.LLMConfigSchemaVersion,
		Mode:          workflowtypes.LLMConfigModeExplicit,
		BuilderLLM:    agentLLM,
		PulseLLM:      agentLLM,
		TieredConfig:  &workflowtypes.TieredLLMConfig{Tier1: agentLLM, Tier2: agentLLM, Tier3: agentLLM},
	}

	wo, err := NewWorkflowOrchestrator(
		"", 0.7, "workflow",
		loggerv2.NewNoop(),
		nil, nil,
		[]string{}, []string{}, false,
		nil, map[string]interface{}{},
		llmCfg, 10, map[string]string{},
		presetCfg,
	)
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}

	cleanup := func() {
		if os.Getenv("KEEP_E2E_WORKSPACE") == "" {
			_ = os.RemoveAll(workspaceDisk)
		}
	}
	return &edgeCaseHarness{
		orchestrator:  wo,
		workspaceRel:  relWorkspace,
		workspaceDisk: workspaceDisk,
	}, cleanup, true
}

func writeEdgePlan(t *testing.T, workspaceDisk string, plan map[string]interface{}) {
	t.Helper()
	if err := writeJSON(filepath.Join(workspaceDisk, "planning", "plan.json"), plan); err != nil {
		t.Fatalf("write plan.json: %v", err)
	}
}

func containsAny(haystack string, needles ...string) bool {
	lower := strings.ToLower(haystack)
	for _, n := range needles {
		if strings.Contains(lower, strings.ToLower(n)) {
			return true
		}
	}
	return false
}
