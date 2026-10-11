package step_based_workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func newJumpTestOrchestrator(t *testing.T) *StepBasedWorkflowOrchestrator {
	t.Helper()
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewDefault(),
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
	return &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
}

func jumpTestSteps() []PlanStepInterface {
	return []PlanStepInterface{
		regularStep("step-1"),
		regularStep("step-2"),
		regularStep("step-3"),
	}
}

func TestNavigateToNextStepIDOutcomes(t *testing.T) {
	hcpo := newJumpTestOrchestrator(t)
	steps := jumpTestSteps()

	cases := []struct {
		name        string
		nextStepID  string
		wantOutcome string
		wantIndex   int // expected i after the call when outcome=="jump"
	}{
		{name: "empty id is none", nextStepID: "", wantOutcome: "none"},
		{name: "end sentinel", nextStepID: "end", wantOutcome: "end"},
		{name: "unknown id falls through", nextStepID: "missing", wantOutcome: "none"},
		{name: "valid target repoints index", nextStepID: "step-3", wantOutcome: "jump", wantIndex: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			progress := &StepProgress{}
			i, startFrom := 0, 0
			outcome, err := hcpo.navigateToNextStepID(context.Background(), "step-1", tc.nextStepID, steps, progress, &i, &startFrom, maxLLMJumpRepeats)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if outcome != tc.wantOutcome {
				t.Fatalf("outcome = %q, want %q", outcome, tc.wantOutcome)
			}
			if outcome == "jump" && i != tc.wantIndex {
				t.Fatalf("i = %d after jump, want %d (target index - 1)", i, tc.wantIndex)
			}
		})
	}
}

func TestNavigateAfterScriptedStepChainsAndConverges(t *testing.T) {
	hcpo := newJumpTestOrchestrator(t)
	scriptA := regularStep("script-a")
	scriptA.NextStepID = "script-b"
	scriptB := regularStep("script-b")
	scriptB.NextStepID = "shared"
	siblingBranch := regularStep("sibling-branch")
	shared := regularStep("shared")
	steps := []PlanStepInterface{scriptA, scriptB, siblingBranch, shared}
	progress := &StepProgress{}

	i, startFrom := 0, 0
	outcome, err := hcpo.navigateAfterScriptedStep(context.Background(), scriptA, steps, progress, &i, &startFrom)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "jump" || i != 0 {
		t.Fatalf("script-a navigation = (%q, i=%d), want jump to script-b", outcome, i)
	}

	i = 1
	outcome, err = hcpo.navigateAfterScriptedStep(context.Background(), scriptB, steps, progress, &i, &startFrom)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "jump" || i != 2 {
		t.Fatalf("script-b navigation = (%q, i=%d), want shared target index minus one", outcome, i)
	}
	// The execution loop increments i after this result, landing on index 3 and
	// skipping the sibling branch at index 2.

	sequential := regularStep("sequential")
	i = 0
	outcome, err = hcpo.navigateAfterScriptedStep(context.Background(), sequential, steps, progress, &i, &startFrom)
	if err != nil || outcome != "none" || i != 0 {
		t.Fatalf("omitted next_step_id must preserve sequential execution: outcome=%q i=%d err=%v", outcome, i, err)
	}

	terminal := regularStep("terminal")
	terminal.NextStepID = "end"
	outcome, err = hcpo.navigateAfterScriptedStep(context.Background(), terminal, steps, progress, &i, &startFrom)
	if err != nil || outcome != "end" {
		t.Fatalf("end navigation = %q, err=%v", outcome, err)
	}
}

func TestNavigateToNextStepIDJumpGuard(t *testing.T) {
	hcpo := newJumpTestOrchestrator(t)
	steps := jumpTestSteps()
	progress := &StepProgress{}

	// The same source->target jump is allowed maxRepeats times, then errors.
	for n := 1; n <= maxLLMJumpRepeats; n++ {
		i, startFrom := 2, 0
		outcome, err := hcpo.navigateToNextStepID(context.Background(), "step-3", "step-1", steps, progress, &i, &startFrom, maxLLMJumpRepeats)
		if err != nil {
			t.Fatalf("jump %d/%d should be allowed, got error: %v", n, maxLLMJumpRepeats, err)
		}
		if outcome != "jump" {
			t.Fatalf("jump %d/%d outcome = %q, want jump", n, maxLLMJumpRepeats, outcome)
		}
	}
	i, startFrom := 2, 0
	_, err := hcpo.navigateToNextStepID(context.Background(), "step-3", "step-1", steps, progress, &i, &startFrom, maxLLMJumpRepeats)
	if err == nil {
		t.Fatalf("jump %d should exceed the limit and error", maxLLMJumpRepeats+1)
	}
	if !strings.Contains(err.Error(), "infinite loop detected") {
		t.Fatalf("error should identify the loop, got: %v", err)
	}

	// A different target from the same source is independent.
	i, startFrom = 2, 0
	if _, err := hcpo.navigateToNextStepID(context.Background(), "step-3", "step-2", steps, progress, &i, &startFrom, maxLLMJumpRepeats); err != nil {
		t.Fatalf("distinct target should not share the exhausted counter, got: %v", err)
	}
}

func TestNavigateToNextStepIDGuardDisabledForRouting(t *testing.T) {
	// Routing passes maxRepeats=0 because it has its own per-route counter —
	// the generic guard must never fire in that mode.
	hcpo := newJumpTestOrchestrator(t)
	steps := jumpTestSteps()
	progress := &StepProgress{}
	for n := 0; n < maxLLMJumpRepeats*3; n++ {
		i, startFrom := 2, 0
		if _, err := hcpo.navigateToNextStepID(context.Background(), "step-3", "step-1", steps, progress, &i, &startFrom, 0); err != nil {
			t.Fatalf("guard should be disabled with maxRepeats=0, got error on jump %d: %v", n+1, err)
		}
	}
	if len(progress.JumpCounts) != 0 {
		t.Fatalf("disabled guard should not record jump counts, got %v", progress.JumpCounts)
	}
}

func TestNavigateToNextStepIDCleansProgressFromTarget(t *testing.T) {
	// Jumping backward must clear completed-step records from the target
	// onward so the re-run executes instead of being skipped as complete.
	hcpo := newJumpTestOrchestrator(t)
	steps := jumpTestSteps()
	progress := &StepProgress{CompletedStepIndices: []int{0, 1, 2}}
	i, startFrom := 2, 0
	outcome, err := hcpo.navigateToNextStepID(context.Background(), "step-3", "step-2", steps, progress, &i, &startFrom, maxHumanJumpRepeats)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != "jump" {
		t.Fatalf("outcome = %q, want jump", outcome)
	}
	if len(progress.CompletedStepIndices) != 1 || progress.CompletedStepIndices[0] != 0 {
		t.Fatalf("completed indices from the target onward should be cleared, got %v", progress.CompletedStepIndices)
	}
}
