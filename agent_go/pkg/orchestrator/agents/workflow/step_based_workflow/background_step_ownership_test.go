package step_based_workflow

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	mcpexecutor "github.com/manishiitg/mcpagent/executor"

	orchestratoragents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func withFastBackgroundSteps(t *testing.T) {
	t.Helper()
	old := backgroundStepPollInterval
	backgroundStepPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { backgroundStepPollInterval = old })
}

// startOwnedTestStep registers a running step the way execute_step does for a
// background agent, and finishes it after d.
func startOwnedTestStep(registry *WorkshopStepRegistry, owner *backgroundStepOwner, id, stepID, result string, d time.Duration) {
	exec := &WorkshopStepExecution{ID: id, StepID: stepID, Status: WorkshopStepRunning, CreatedAt: time.Now()}
	registry.Register(exec)
	owner.add(id)
	go func() {
		time.Sleep(d)
		exec.mu.Lock()
		exec.Status = WorkshopStepDone
		exec.Result = result
		exec.mu.Unlock()
	}()
}

// reviewerAgent answers each turn; on its first step-results turn it may start
// one more step, like a reviewer that checks its fix and then re-verifies.
type reviewerAgent struct {
	turns    []string
	onResult func(turn int)
}

func (a *reviewerAgent) Execute(_ context.Context, vars map[string]string, history []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
	a.turns = append(a.turns, vars["Instruction"])
	if a.onResult != nil && strings.HasPrefix(vars["Instruction"], "[STEP RESULTS]") {
		a.onResult(len(a.turns))
	}
	return "recorded: " + vars["Instruction"][:20], append(history, llmtypes.MessageContent{}), nil
}
func (a *reviewerAgent) GetType() string                                        { return "test" }
func (a *reviewerAgent) GetConfig() *orchestratoragents.OrchestratorAgentConfig { return nil }
func (a *reviewerAgent) Initialize(context.Context) error                       { return nil }
func (a *reviewerAgent) Close() error                                           { return nil }
func (a *reviewerAgent) GetBaseAgent() *orchestratoragents.BaseAgent            { return nil }

// The social-media case: the Technical reviewer fixes a step and starts a
// verification run, then ends its turn. It must get the verification result as
// its next turn, in the same conversation, before it is done, instead of
// finishing with nothing recorded while the run completes unseen.
func TestBackgroundAgentGetsTheResultOfTheStepItStarted(t *testing.T) {
	withFastBackgroundSteps(t)
	registry := NewWorkshopStepRegistry()
	owner := newBackgroundStepOwner("bg-technical-review")
	startOwnedTestStep(registry, owner, "exec-verify", "review-strategy-review", "audit passed: 4 strategies kept", 50*time.Millisecond)

	agent := &reviewerAgent{}
	history := []llmtypes.MessageContent{{}, {}}
	result, _, err := handOwnedStepResults(context.Background(), agent, map[string]string{}, history, "sequence done", owner, registry)
	if err != nil {
		t.Fatalf("handOwnedStepResults: %v", err)
	}
	if len(agent.turns) != 1 || !strings.Contains(agent.turns[0], "audit passed: 4 strategies kept") || !strings.Contains(agent.turns[0], "review-strategy-review") {
		t.Fatalf("the reviewer must get the verification result as its next turn, got %q", agent.turns)
	}
	if !strings.HasPrefix(result, "recorded:") {
		t.Fatalf("the agent's final answer must come from the results turn, got %q", result)
	}
}

// A result turn that starts another step gets that step's result too; the
// agent is done only when a turn starts nothing new.
func TestBackgroundAgentLoopsUntilATurnStartsNothing(t *testing.T) {
	withFastBackgroundSteps(t)
	registry := NewWorkshopStepRegistry()
	owner := newBackgroundStepOwner("bg-review")
	startOwnedTestStep(registry, owner, "exec-1", "step-a", "first run failed check", 20*time.Millisecond)

	agent := &reviewerAgent{}
	agent.onResult = func(turn int) {
		if turn == 1 {
			startOwnedTestStep(registry, owner, "exec-2", "step-a", "second run passed", 20*time.Millisecond)
		}
	}
	if _, _, err := handOwnedStepResults(context.Background(), agent, nil, nil, "", owner, registry); err != nil {
		t.Fatalf("handOwnedStepResults: %v", err)
	}
	if len(agent.turns) != 2 || !strings.Contains(agent.turns[1], "second run passed") {
		t.Fatalf("want two result turns ending with the re-run, got %q", agent.turns)
	}
	if strings.Contains(agent.turns[1], "first run failed check") {
		t.Fatal("a result already handed back must not be repeated")
	}
}

// With no steps started, the agent's own answer stands and nothing waits.
func TestBackgroundAgentWithoutStepsIsUnchanged(t *testing.T) {
	agent := &reviewerAgent{}
	result, _, err := handOwnedStepResults(context.Background(), agent, nil, nil, "final", newBackgroundStepOwner("bg"), NewWorkshopStepRegistry())
	if err != nil || result != "final" || len(agent.turns) != 0 {
		t.Fatalf("want the original result and no extra turns, got %q %d %v", result, len(agent.turns), err)
	}
}

// A step that never finishes stops the agent with a clear error at the ceiling.
func TestBackgroundAgentStopsAtTheCeiling(t *testing.T) {
	withFastBackgroundSteps(t)
	old := backgroundStepCeiling
	backgroundStepCeiling = 100 * time.Millisecond
	t.Cleanup(func() { backgroundStepCeiling = old })
	registry := NewWorkshopStepRegistry()
	owner := newBackgroundStepOwner("bg")
	startOwnedTestStep(registry, owner, "stuck", "step-a", "", time.Hour)
	if _, _, err := handOwnedStepResults(context.Background(), &reviewerAgent{}, nil, nil, "", owner, registry); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("want a still-running error, got %v", err)
	}
}

// execute_step identifies the calling background agent from the trusted
// tool session the MCP bridge attaches, and only while that agent runs.
func TestBackgroundStepOwnerIsFoundFromTheCallerToolSession(t *testing.T) {
	iwm := &InteractiveWorkshopManager{}
	owner := newBackgroundStepOwner("bg-1")
	release := iwm.registerBackgroundStepOwner("tool-session-bg-1", owner)

	if got := iwm.backgroundStepOwnerFor(mcpexecutor.WithSessionID(context.Background(), "tool-session-bg-1")); got != owner {
		t.Fatal("a call from the background agent's tool session must find its owner")
	}
	if got := iwm.backgroundStepOwnerFor(mcpexecutor.WithSessionID(context.Background(), "main-builder-session")); got != nil {
		t.Fatal("a call from another session must not be attributed to the background agent")
	}
	if got := iwm.backgroundStepOwnerFor(context.Background()); got != nil {
		t.Fatal("a call with no trusted session must not be attributed")
	}
	release()
	if got := iwm.backgroundStepOwnerFor(mcpexecutor.WithSessionID(context.Background(), "tool-session-bg-1")); got != nil {
		t.Fatal("the owner must be forgotten once the background agent ends")
	}
}

// A reviewer about to finish without its recorded result gets one more turn,
// in its own conversation, naming what is missing; one that recorded it, or a
// non-Pulse background agent, gets nothing extra.
func TestPulseReviewerIsAskedToRecordAMissingResult(t *testing.T) {
	agent := &reviewerAgent{}
	checks := 0
	check := func(_ context.Context, module, runID string) error {
		checks++
		if module != "technical_review" || runID != "run-1" {
			t.Fatalf("checked %s/%s", module, runID)
		}
		return fmt.Errorf("technical_review has no terminal result")
	}
	result, err := ensurePulseReviewerRecorded(context.Background(), agent, nil, nil, "fixed it", "technical_review", "run-1", check, newBackgroundStepOwner("bg"), NewWorkshopStepRegistry())
	if err != nil {
		t.Fatalf("ensurePulseReviewerRecorded: %v", err)
	}
	if checks != 1 || len(agent.turns) != 1 || !strings.HasPrefix(agent.turns[0], "[RESULT NOT RECORDED]") ||
		!strings.Contains(agent.turns[0], `record_pulse_result(module="technical_review", pulse_run_id="run-1")`) {
		t.Fatalf("want one record-your-result turn, got %q", agent.turns)
	}
	if !strings.HasPrefix(result, "recorded:") {
		t.Fatalf("the reminder turn's answer must be the result, got %q", result)
	}

	recorded := &reviewerAgent{}
	if _, err := ensurePulseReviewerRecorded(context.Background(), recorded, nil, nil, "done", "technical_review", "run-1", func(context.Context, string, string) error { return nil }, nil, nil); err != nil || len(recorded.turns) != 0 {
		t.Fatalf("a recorded result needs no extra turn, got %q %v", recorded.turns, err)
	}
	plain := &reviewerAgent{}
	if _, err := ensurePulseReviewerRecorded(context.Background(), plain, nil, nil, "done", "", "", check, nil, nil); err != nil || len(plain.turns) != 0 {
		t.Fatalf("a non-Pulse background agent is not checked, got %q %v", plain.turns, err)
	}
}

// A background agent that exits without handing its steps back (its turn
// failed or was cancelled) stops the ones still running: their notice is
// suppressed, so nobody else would take their result.
func TestBackgroundAgentStopsUnhandedStepsOnExit(t *testing.T) {
	registry := NewWorkshopStepRegistry()
	owner := newBackgroundStepOwner("bg-1")
	running := &WorkshopStepExecution{ID: "exec-running", StepID: "s1", Status: WorkshopStepRunning, CreatedAt: time.Now(), cancel: func() {}}
	registry.Register(running)
	owner.add("exec-running")
	handed := &WorkshopStepExecution{ID: "exec-handed", StepID: "s2", Status: WorkshopStepRunning, CreatedAt: time.Now(), cancel: func() {}}
	registry.Register(handed)
	owner.add("exec-handed")
	owner.handed["exec-handed"] = true

	owner.stopUnhanded(func(id string) { _, _ = stopWorkshopExecution(registry, nil, id) })
	if snap, _ := registry.GetSnapshot("exec-running"); snap.Status != WorkshopStepCancelled {
		t.Fatalf("an unhanded running step must be stopped, got %s", snap.Status)
	}
	if snap, _ := registry.GetSnapshot("exec-handed"); snap.Status != WorkshopStepRunning {
		t.Fatalf("a handed step is not the owner's to stop, got %s", snap.Status)
	}
}
