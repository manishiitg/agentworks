package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// Validates sequence-unification Stage 3: a todo_task's `messages` (old
// OrchestratorMessage JSON shape) deserializes, back-compat, into the unified
// []AgentItem — no plan migration needed.
func TestOrchestratorMessagesDeserializeToUnifiedItem(t *testing.T) {
	planJSON := `{"steps":[{
		"type":"todo_task","id":"orch","title":"o","description":"d",
		"predefined_routes":[{"route_id":"w","route_name":"W","condition":"c",
			"sub_agent_step":{"type":"scripted","id":"w","title":"w","description":"d"}}],
		"messages":[
			{"id":"m1","type":"message","message":"hello","max_corrections":2},
			{"id":"m2","type":"foreach","source_sql":"SELECT id FROM t","max_iterations":5,"message":"row {{.id}}"},
			{"id":"m3","type":"prevalidation","validation_schema":{"files":[]}}
		]
	}]}`
	var pr PlanningResponse
	if err := json.Unmarshal([]byte(planJSON), &pr); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	var todo *AgentPlanStep
	for _, s := range pr.Steps {
		if tt, ok := s.(*AgentPlanStep); ok {
			todo = tt
		}
	}
	if todo == nil {
		t.Fatal("todo_task step not found after unmarshal")
	}
	// Compile-time + runtime proof the field is the unified type.
	var msgs []AgentItem = todo.Items
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages, got %d", len(msgs))
	}
	if msgs[0].Type != "user_message" || msgs[0].Message != "hello" || msgs[0].MaxCorrections != 2 {
		t.Errorf("message[0] back-compat fields lost: %+v", msgs[0])
	}
	if msgs[1].Type != "foreach" || msgs[1].SourceSQL != "SELECT id FROM t" || msgs[1].MaxIterations != 5 {
		t.Errorf("foreach[1] back-compat fields lost: %+v", msgs[1])
	}
	if msgs[2].Type != "prevalidation" || msgs[2].ValidationSchema == nil {
		t.Errorf("prevalidation[2] back-compat fields lost: %+v", msgs[2])
	}
}

// A legacy objective must not reintroduce the removed hidden reflection turn.
func TestAgentClosingItems(t *testing.T) {
	c := newAgentClosingTestOrchestrator(t)
	step := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "research"}, AgentConfigs: &AgentConfigs{LearningsAccess: LearningsAccessReadWrite, LearningObjective: "Capture methods", KnowledgebaseAccess: KBAccessReadWrite, KnowledgebaseContribution: "Capture context"}}
	if items := c.agentSequenceClosingItems(context.Background(), step, 0); len(items) != 0 {
		t.Fatalf("automatic closing turns: %+v", items)
	}
	if access := c.agentSequenceStepFullWriteAccess(step); !access.Learnings || !access.Knowledgebase {
		t.Fatalf("explicit task grants lost: %+v", access)
	}
}

func TestAppendAgentFinalValidation(t *testing.T) {
	topLevel := &ValidationSchema{Files: []FileValidationRule{{FileName: "result.json", MustExist: true}}}
	other := &ValidationSchema{Files: []FileValidationRule{{FileName: "intermediate.json", MustExist: true}}}

	tests := []struct {
		name        string
		items       []AgentItem
		schema      *ValidationSchema
		wantLen     int
		wantAdded   bool
		wantFinalID string
	}{
		{
			name:      "no top-level schema",
			items:     []AgentItem{{ID: "work", Type: "user_message"}},
			wantLen:   1,
			wantAdded: false,
		},
		{
			name:      "adds final gate",
			items:     []AgentItem{{ID: "work", Type: "user_message"}},
			schema:    topLevel,
			wantLen:   2,
			wantAdded: true,
		},
		{
			name: "final gate falls back to top-level schema",
			items: []AgentItem{
				{ID: "work", Type: "user_message"},
				{ID: "verify", Type: "prevalidation"},
			},
			schema:      topLevel,
			wantLen:     2,
			wantAdded:   false,
			wantFinalID: "verify",
		},
		{
			name: "final gate has equal item schema",
			items: []AgentItem{
				{ID: "work", Type: "user_message"},
				{ID: "verify", Type: "prevalidation", ValidationSchema: topLevel},
			},
			schema:      topLevel,
			wantLen:     2,
			wantAdded:   false,
			wantFinalID: "verify",
		},
		{
			name: "different intermediate gate does not replace final gate",
			items: []AgentItem{
				{ID: "work", Type: "user_message"},
				{ID: "verify-intermediate", Type: "prevalidation", ValidationSchema: other},
			},
			schema:    topLevel,
			wantLen:   3,
			wantAdded: true,
		},
		{
			name: "synthetic id avoids configured collision",
			items: []AgentItem{
				{ID: "__automatic_final_validation__", Type: "user_message"},
			},
			schema:      topLevel,
			wantLen:     2,
			wantAdded:   true,
			wantFinalID: "__automatic_final_validation_2__",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalLen := len(tt.items)
			got := appendAgentFinalValidation(tt.items, tt.schema)
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d: %+v", len(got), tt.wantLen, got)
			}
			if len(tt.items) != originalLen {
				t.Fatalf("input items mutated: len = %d, want %d", len(tt.items), originalLen)
			}
			if len(got) == 0 {
				return
			}
			final := got[len(got)-1]
			if tt.wantAdded {
				if final.Type != "prevalidation" || !equalValidationSchemas(final.ValidationSchema, tt.schema) {
					t.Fatalf("final item is not the automatic top-level gate: %+v", final)
				}
			}
			if tt.wantFinalID != "" && final.ID != tt.wantFinalID {
				t.Fatalf("final id = %q, want %q", final.ID, tt.wantFinalID)
			}
		})
	}
}

func TestAgentFinalValidationPrecedesClosingItems(t *testing.T) {
	hcpo := newAgentClosingTestOrchestrator(t)
	step := &AgentPlanStep{
		CommonStepFields: CommonStepFields{
			ID:               "work-and-learn",
			Description:      "do the work",
			ValidationSchema: &ValidationSchema{Files: []FileValidationRule{{FileName: "result.json", MustExist: true}}},
		},
		Items: []AgentItem{{ID: "work", Type: "user_message", Message: "Produce result.json"}},
		AgentConfigs: &AgentConfigs{
			LearningsAccess:   LearningsAccessReadWrite,
			LearningObjective: "Capture the durable pattern",
		},
	}

	planned := appendAgentFinalValidation(step.Items, step.ValidationSchema)
	planned = append(planned, hcpo.agentSequenceClosingItems(context.Background(), step, 0)...)
	if len(planned) != 2 {
		t.Fatalf("planned len = %d, want 2: %+v", len(planned), planned)
	}
	if planned[1].Type != "prevalidation" {
		t.Fatalf("expected work -> final validation, without automatic learning, got %+v", planned)
	}
}

func TestFormatAgentTurnLogResult(t *testing.T) {
	item := AgentItem{ID: "review", Type: "user_message"}
	if got := formatAgentTurnLogResult(item, "STATUS: COMPLETED\nReport updated", nil); got != "Agent item: review (user_message)\nSTATUS: COMPLETED\nReport updated" {
		t.Fatalf("success log result = %q", got)
	}

	got := formatAgentTurnLogResult(item, "partial output", fmt.Errorf("provider disconnected"))
	for _, want := range []string{"Agent item: review (user_message)", "STATUS: FAILED", "provider disconnected", "partial output"} {
		if !strings.Contains(got, want) {
			t.Fatalf("failure log result %q missing %q", got, want)
		}
	}
}

func TestAgentClosingItemsHonorReadOnlyLearningsAccess(t *testing.T) {
	hcpo := newAgentClosingTestOrchestrator(t)
	step := &AgentPlanStep{
		CommonStepFields: CommonStepFields{ID: "seq", Description: "do work"},
		AgentConfigs: &AgentConfigs{
			LearningsAccess:   LearningsAccessRead,
			LearningObjective: "capture durable execution patterns",
		},
	}

	if got := hcpo.agentSequenceClosingItems(context.Background(), step, 0); len(got) != 0 {
		t.Fatalf("read-only learning access should suppress synthetic learning item, got %+v", got)
	}
}

func newAgentClosingTestOrchestrator(t *testing.T) *StepBasedWorkflowOrchestrator {
	t.Helper()
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "",
		[]string{"test-server"}, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	base.SetWorkspacePath("Workflow/test-flow")
	return &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
}

// A step's work turns are prompted to create a JSON output ONLY when the step is
// actually validated on a file. No file-validation (db-only or no schema) → no
// throwaway output file; validate on the db / real effect instead.
func TestFirstValidationFileNameGatesTheOutputFile(t *testing.T) {
	// No schema → no output file prompted.
	if got := firstValidationFileName(nil); got != "" {
		t.Fatalf("nil schema should yield no output file, got %q", got)
	}
	// DB-only validation → no output file (validate on the db, not a marker).
	dbOnly := &ValidationSchema{DB: []DBValidationRule{{Name: "row", SQL: "SELECT 1 FROM t"}}}
	if got := firstValidationFileName(dbOnly); got != "" {
		t.Fatalf("db-only schema should yield no output file, got %q", got)
	}
	// File validation → prompt exactly the file the gate checks (not a default name).
	files := &ValidationSchema{Files: []FileValidationRule{{FileName: "portfolio.json", MustExist: true}}}
	if got := firstValidationFileName(files); got != "portfolio.json" {
		t.Fatalf("file schema should name its validated file, got %q", got)
	}
}
