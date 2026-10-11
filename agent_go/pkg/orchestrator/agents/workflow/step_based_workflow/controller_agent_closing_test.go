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
			"sub_agent_step":{"type":"regular","id":"w","title":"w","description":"d"}}],
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
	if msgs[0].Type != "message" || msgs[0].Message != "hello" || msgs[0].MaxCorrections != 2 {
		t.Errorf("message[0] back-compat fields lost: %+v", msgs[0])
	}
	if msgs[1].Type != "foreach" || msgs[1].SourceSQL != "SELECT id FROM t" || msgs[1].MaxIterations != 5 {
		t.Errorf("foreach[1] back-compat fields lost: %+v", msgs[1])
	}
	if msgs[2].Type != "prevalidation" || msgs[2].ValidationSchema == nil {
		t.Errorf("prevalidation[2] back-compat fields lost: %+v", msgs[2])
	}
}

// Verifies a standalone message_sequence gets synthetic learnings/KB contribution
// turns appended when (and only when) the step is configured for those writes —
// so it honors step-level learning_objective / knowledgebase_contribution like a
// regular step instead of silently skipping the post-step learnings/KB phase.
func TestAgentClosingItems(t *testing.T) {
	hcpo := newAgentClosingTestOrchestrator(t)

	// Configured for BOTH learnings and KB writes (direct method) -> both items
	// appended, in order. The KB closing turn exists ONLY for write_method=direct:
	// under the default agent method the constraint layer strips KB from every
	// item's guard, so the turn would be a guaranteed-denied write.
	both := &AgentPlanStep{
		Type:             StepTypeAgent,
		CommonStepFields: CommonStepFields{ID: "extract-all", Description: "extract portfolio data"},
		AgentConfigs: &AgentConfigs{
			LearningsAccess:           LearningsAccessReadWrite,
			LearningObjective:         "Capture how to extract MyCAMS portfolio data reliably",
			KnowledgebaseAccess:       KBAccessReadWrite,
			KnowledgebaseContribution: "Record portal-specific selectors and quirks",
		},
	}
	// PLAT-055: one reflection item, not a learnings item followed by a KB item.
	// Appending them separately meant the agent chose a destination based on
	// which turn it was in rather than on which store owns the content, so both
	// write grants now belong to a single turn.
	items := hcpo.agentSequenceClosingItems(context.Background(), both, 0)
	if len(items) != 1 {
		t.Fatalf("expected 1 merged reflection item, got %d", len(items))
	}
	if items[0].Type != "user_message" || items[0].Kind != "learning" {
		t.Errorf("reflection item should be a learning-kind user_message: %+v", items[0])
	}
	if !items[0].WriteAccess.Learnings || !items[0].WriteAccess.Knowledgebase {
		t.Errorf("reflection item must carry BOTH write grants so routing is a real choice: %+v", items[0].WriteAccess)
	}
	if items[0].Message == "" {
		t.Errorf("reflection item should carry a message")
	}
	// The routing rule is the whole point of merging; without it the turn is
	// just the old learnings turn with extra access.
	for _, want := range []string{"Route each thing to the store that owns it", "Pulse Technical Review", "not a learning"} {
		if !strings.Contains(items[0].Message, want) {
			t.Errorf("reflection message missing %q", want)
		}
	}

	// No agent configs -> no synthetic items.
	if got := hcpo.agentSequenceClosingItems(context.Background(), &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "x"}}, 0); len(got) != 0 {
		t.Errorf("expected no closing items without agent configs, got %d", len(got))
	}

	// learnings_access=read-write but empty objective -> no learning item (double-gated).
	noObj := &AgentPlanStep{
		CommonStepFields: CommonStepFields{ID: "y", Description: "d"},
		AgentConfigs:     &AgentConfigs{LearningsAccess: LearningsAccessReadWrite},
	}
	if got := hcpo.agentSequenceClosingItems(context.Background(), noObj, 0); len(got) != 0 {
		t.Errorf("expected no items when learning objective empty, got %d", len(got))
	}

	// KB contribution set but access not write-capable -> no KB item.
	kbReadOnly := &AgentPlanStep{
		CommonStepFields: CommonStepFields{ID: "z", Description: "d"},
		AgentConfigs:     &AgentConfigs{KnowledgebaseAccess: "read", KnowledgebaseContribution: "note something"},
	}
	if got := hcpo.agentSequenceClosingItems(context.Background(), kbReadOnly, 0); len(got) != 0 {
		t.Errorf("expected no KB item when access is read-only, got %d", len(got))
	}

	// KB write-capable with knowledgebase_write_method UNSET -> a KB closing item.
	// This used to be the opposite: unset defaulted to "agent" mode, notes/ was not
	// writable by the step agent, and the separate post-step KB update agent owned
	// the contribution. That agent is retired,
	// so an unset field now resolves to direct and the step agent writes notes/
	// itself in this closing turn. A step carrying a contribution must never end up
	// with no writer at all, which is what a stale "expect zero items" assertion
	// here would silently allow.
	kbUnsetMethod := &AgentPlanStep{
		CommonStepFields: CommonStepFields{ID: "w", Description: "d"},
		AgentConfigs: &AgentConfigs{
			KnowledgebaseAccess:       KBAccessReadWrite,
			KnowledgebaseContribution: "note something",
		},
	}
	if got := hcpo.agentSequenceClosingItems(context.Background(), kbUnsetMethod, 0); len(got) != 1 {
		t.Errorf("expected 1 KB closing item when write method is unset (now resolves to direct), got %d", len(got))
	}

	// An explicit legacy "agent" value must behave identically — old plans are
	// coerced to direct rather than silently losing their KB contribution.
	kbLegacyAgent := &AgentPlanStep{
		CommonStepFields: CommonStepFields{ID: "w2", Description: "d"},
		AgentConfigs: &AgentConfigs{
			KnowledgebaseAccess:       KBAccessReadWrite,
			KnowledgebaseContribution: "note something",
		},
	}
	if got := hcpo.agentSequenceClosingItems(context.Background(), kbLegacyAgent, 0); len(got) != 1 {
		t.Errorf("expected legacy write_method=agent to be coerced to direct and yield 1 KB closing item, got %d", len(got))
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
	if len(planned) != 3 {
		t.Fatalf("planned len = %d, want 3: %+v", len(planned), planned)
	}
	if planned[1].Type != "prevalidation" || planned[2].Kind != "learning" {
		t.Fatalf("expected work -> final validation -> learning, got %+v", planned)
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
