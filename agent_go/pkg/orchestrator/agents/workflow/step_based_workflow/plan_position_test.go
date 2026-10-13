package step_based_workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

func planStep(id, title, output string, deps []string) PlanStepInterface {
	return &ScriptedPlanStep{CommonStepFields: CommonStepFields{ID: id, Title: title, ContextOutput: FlexibleContextOutput(output), ContextDependencies: deps}}
}

// The real renderers must agree on placement, and the latest same-run outcome
// wins even when retry counters restart. Missing evidence is not invented.
func TestStepPlacementAcrossExecutionPaths(t *testing.T) {
	c := newAgentClosingTestOrchestrator(t)
	c.selectedRunFolder = "iteration-0/default"
	files := map[string]string{
		"soul/soul.md": "## Objective\nProduce trustworthy research.\n## Success Criteria\nAll evidence checked.\n## Constraints\nPRIVATE_CONSTRAINT_BODY",
		"runs/iteration-0/default/logs/fetch/execution/execution-attempt-9-iteration-0.json": `{"execution_result":"Old attempt.\nSTATUS: FAILED","completed_at":"2026-10-13T16:00:00Z"}`,
		"runs/iteration-0/default/logs/fetch/execution/execution-final-summary.json":         `{"status":"completed","execution_result":"Fetched evidence.\nSTATUS: COMPLETED","completed_at":"2026-10-13T17:00:00Z"}`,
		"runs/iteration-0/default/logs/fetch/execution/execution-attempt-1-iteration-0.json": `{"execution_result":"Provider unavailable.\nSTATUS: FAILED","completed_at":"2026-10-13T18:00:00Z"}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("placement wrote data: %s", r.Method)
			w.WriteHeader(405)
			return
		}
		if r.URL.Path == "/api/documents" {
			dir := strings.TrimPrefix(r.URL.Query().Get("folder"), "Workflow/test-flow/")
			var entries []map[string]string
			for name := range files {
				if strings.HasPrefix(name, dir+"/") {
					entries = append(entries, map[string]string{"filepath": "Workflow/test-flow/" + name, "type": "file"})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": entries})
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/documents/Workflow/test-flow/")
		content, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"content": content}})
	}))
	defer server.Close()
	c.WorkspaceClient = workspace.NewClient(server.URL)
	step := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "transform", Title: "Transform", ContextOutput: FlexibleContextOutput("clean.json")}, PromptContract: AgentDescriptionFirstPromptContract}
	all := []PlanStepInterface{planStep("fetch", "Fetch evidence", "raw.json", nil), step, planStep("report", "Report", "report.md", []string{"clean.json"})}
	ctx := withExecutionPlan(t.Context(), &PlanningResponse{Steps: all})
	for _, path := range []string{"direct", "manager", "child"} {
		t.Run(path, func(t *testing.T) {
			active := ctx
			if path == "child" {
				active = withStepParentPlacement(ctx, "Research manager", "Normalize the downloaded evidence.")
			}
			var vars map[string]string
			if path == "manager" {
				vars = c.buildOrchestratorTemplateVars(active, step, 1, "transform", nil, nil, all, "", nil)
			} else {
				vars = c.buildAgentTemplateVars(active, step, AgentItem{}, 1, "transform", "", nil, nil, AgentWriteAccess{})
			}
			block := vars["PlanPosition"]
			for _, text := range []string{"Goal: Produce trustworthy research.", "Success: All evidence checked.", "step 2 of 3", "Before: Fetch evidence — FAILED; Provider unavailable.", "After: Report", "Output consumers: Report"} {
				if !strings.Contains(block, text) {
					t.Fatalf("%s placement lacks %q: %s", path, text, block)
				}
			}
			if strings.Contains(block, "PRIVATE_CONSTRAINT_BODY") || strings.Contains(block, "Fetched evidence") {
				t.Fatal("placement dumped Constraints or selected stale success")
			}
			if path == "child" && (!strings.Contains(block, "Parent: Research manager") || !strings.Contains(block, "Delegated task: Normalize")) {
				t.Fatal("child lost owning call")
			}
			prompt := (&WorkflowExecutionOnlyAgent{}).executionOnlySystemPromptProcessor(vars)
			if !strings.Contains(prompt, "## Where you fit\n"+block) {
				t.Fatal("renderer dropped placement")
			}
			if len([]rune(block)) > 2100 || len(strings.Split(block, "\n")) > 8 {
				t.Fatal("placement is unbounded")
			}
		})
	}
	delete(files, "runs/iteration-0/default/logs/fetch/execution/execution-attempt-1-iteration-0.json")
	for key := range files {
		if strings.Contains(key, "logs/") {
			delete(files, key)
		}
	}
	files["soul/soul.md"] = "## Objective\n" + strings.Repeat("long objective ", 300) + "\n## Success Criteria\n" + strings.Repeat("long success ", 300)
	block := c.buildStepPlacement(withStepParentPlacement(ctx, strings.Repeat("parent ", 300), strings.Repeat("task ", 300)), all, 1)
	if len([]rune(block)) > 2100 || strings.Contains(block, "Before: Fetch evidence —") {
		t.Fatal("size bound or missing evidence policy failed")
	}
}
