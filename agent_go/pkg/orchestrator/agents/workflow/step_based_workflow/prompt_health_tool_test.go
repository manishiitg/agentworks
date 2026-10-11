package step_based_workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

func TestRegisteredPromptHealthReadsCurrentPlan(t *testing.T) {
	controller := newAgentClosingTestOrchestrator(t)
	stale := &PlanningResponse{Steps: []PlanStepInterface{
		&AgentPlanStep{CommonStepFields: CommonStepFields{ID: "old", Description: "cached description"}},
	}}
	executionCtx := withExecutionPlan(context.Background(), stale)
	var mu sync.Mutex
	var content string
	missing := false
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/api/documents/Workflow/test-flow/workflow.json" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/documents/Workflow/test-flow/planning/plan.json" {
			t.Errorf("unexpected workspace request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		reads++
		w.Header().Set("Content-Type", "application/json")
		if missing {
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"content": content}})
	}))
	defer server.Close()
	controller.WorkspaceClient = workspace.NewClient(server.URL)
	agent := newWorkshopDefinitionDraft()
	RegisterWorkshopChatTools(agent, &WorkshopChatSession{
		controller: controller, StepRegistry: NewWorkshopStepRegistry(),
		config: &WorkshopConfig{WorkspacePath: controller.GetWorkspacePath()},
	}, workshopToolTestLogger{})
	tool := agent.tools["get_plan_prompt_health"]
	if tool.Execute == nil {
		t.Fatal("prompt health tool not registered")
	}
	for _, descriptions := range [][]string{
		{strings.Repeat("Shared contract. ", 30), strings.Repeat("Shared contract. ", 30)},
		{"Shortened first description.", "Distinct second description."},
	} {
		steps := make([]PlanStepInterface, 0, len(descriptions))
		for i, description := range descriptions {
			// Decode real persisted step JSON, including its execution contract.
			encoded, _ := json.Marshal(map[string]interface{}{"steps": []interface{}{map[string]interface{}{
				"id": string(rune('a' + i)), "title": "Work", "type": "agent", "description": description,
				"items": []interface{}{map[string]interface{}{"id": "work", "type": "user_message", "message": "Do and verify the work."}},
			}}})
			var single PlanningResponse
			if err := json.Unmarshal(encoded, &single); err != nil {
				t.Fatal(err)
			}
			steps = append(steps, single.Steps...)
		}
		encoded, err := json.Marshal(PlanningResponse{Steps: steps})
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		content = string(encoded)
		mu.Unlock()
		result, err := tool.Execute(executionCtx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got PromptHealthReport
		if err := json.Unmarshal([]byte(result), &got); err != nil {
			t.Fatal(err)
		}
		if want := BuildPromptHealthReport(steps); !reflect.DeepEqual(got, want) {
			t.Fatalf("registered tool returned stale health: got %+v, want %+v", got, want)
		}
		if executionPlanFromContext(executionCtx) != stale {
			t.Fatal("read-only health tool replaced the execution plan cache")
		}
	}
	for _, invalid := range []string{"{invalid", ""} {
		mu.Lock()
		content, missing = invalid, invalid == ""
		mu.Unlock()
		result, err := tool.Execute(executionCtx, nil)
		if err == nil || result != "" {
			t.Fatalf("unavailable/invalid current plan returned stale success: %q, %v", result, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if reads != 4 {
		t.Fatalf("workspace reads = %d, want one per tool invocation (4)", reads)
	}
}
