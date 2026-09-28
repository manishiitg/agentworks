package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/executor"

	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Each reviewer's task carries what the old dispatch turn told the dispatcher
// to pass on, and none of the dispatch plumbing.
func TestPulseReviewerInstructionIsTheReviewersTaskOnly(t *testing.T) {
	for module, reference := range map[string]string{
		pulseModulePlanDriftReview:    "plan-drift-review",
		pulseModuleTechnicalReview:    "technical-review",
		pulseModuleArchitectureReview: "architecture-review",
		pulseModuleStrategicReview:    "strategy-auditor",
	} {
		got := pulseReviewerInstruction("run-42", module)
		for _, want := range []string{`pulse_run_id="run-42"`, `module="` + module + `"`, "references/" + reference + ".md"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s instruction missing %q", module, want)
			}
		}
		if module != pulseModulePlanDriftReview && !strings.Contains(got, "Finish with one record_pulse_result") {
			t.Errorf("%s instruction lost the record rules", module)
		}
		if strings.Contains(got, "launch exactly one") || strings.Contains(got, "end this parent turn") {
			t.Errorf("%s instruction still carries dispatch plumbing", module)
		}
	}
	// The fallback dispatch turn keeps the same contract text.
	dispatch := pulseLifecycleModuleReviewStep("run-42", pulseModuleTechnicalReview).query
	if !strings.Contains(dispatch, "launch exactly one run_in_background executor") || !strings.Contains(dispatch, pulseReviewerRecordRules) {
		t.Fatal("the dispatch fallback must keep its instructions and the record rules")
	}
}

// Not a reviewer step, or no workshop yet (a fix run's first reviewer): the
// caller keeps using the dispatch turn.
func TestPulseReviewerDirectFallsBackWhenItCannotStart(t *testing.T) {
	s := &SchedulerService{api: &StreamingAPI{bgAgentRegistry: NewBackgroundAgentRegistry()}}
	sctx := &ScheduleContext{WorkspacePath: "Workflow/direct"}
	if _, direct := s.runPulseReviewerDirect(context.Background(), sctx, "session-x", "run-1", "finalize"); direct {
		t.Fatal("the finalizer is not a reviewer")
	}
	if _, direct := s.runPulseReviewerDirect(context.Background(), sctx, "session-x", "run-1", "technical-review"); direct {
		t.Fatal("a session with no workshop must fall back to the dispatch turn")
	}
}

// The runtime starts the due reviewer through the session's own
// run_in_background tool, with its review scope, waits for it, takes its
// result so no notice reaches the conversation, and releases the session.
func TestPulseReviewerIsStartedAndAwaitedByTheRuntime(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	old := pulseReviewerPollInterval
	pulseReviewerPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { pulseReviewerPollInterval = old })

	api := &StreamingAPI{bgAgentRegistry: NewBackgroundAgentRegistry()}
	s := &SchedulerService{api: api}
	session := "schedule-cron--direct-test"
	api.workshopChatSessions.Store(session, &todo_creation_human.WorkshopChatSession{})

	var started map[string]interface{}
	codeexec.InitRegistryForSession(session, map[string]func(context.Context, map[string]interface{}) (string, error){
		"run_in_background": func(ctx context.Context, args map[string]interface{}) (string, error) {
			started = args
			// The bound tool refuses a caller that is not its own session.
			if got := executor.SessionIDFromContext(ctx); got != session {
				t.Errorf("the start must carry the session as its caller, got %q", got)
			}
			if !api.sessionCompletionsOwned(session) {
				t.Error("the session must be claimed while the reviewer runs")
			}
			agent := &BackgroundAgent{ID: "bg-technical-review-1", SessionID: session, Status: BGAgentRunning, CreatedAt: time.Now()}
			api.bgAgentRegistry.Register(session, agent)
			go func() {
				time.Sleep(50 * time.Millisecond)
				agent.mu.Lock()
				agent.Status = BGAgentCompleted
				agent.mu.Unlock()
			}()
			return "Background task \"Technical Review\" started (type=executor, completion_mode=continue).\nexecution_id: \"bg-technical-review-1\"\n", nil
		},
	}, nil)
	t.Cleanup(func() { codeexec.CleanupSession(session) })

	result, direct := s.runPulseReviewerDirect(context.Background(), &ScheduleContext{WorkspacePath: "Workflow/direct"}, session, "run-7", "technical-review")
	if !direct || result.outcome != pulseLifecycleStepCompleted {
		t.Fatalf("want a completed direct start, got direct=%v %+v", direct, result)
	}
	if started["review_module"] != pulseModuleTechnicalReview || started["pulse_run_id"] != "run-7" ||
		!strings.Contains(started["instruction"].(string), "references/technical-review.md") {
		t.Fatalf("reviewer started with the wrong scope: %v", started)
	}
	if !api.bgAgentRegistry.Get(session, "bg-technical-review-1").GetSnapshot().CompletionNotified {
		t.Fatal("the runtime must take the reviewer's result so no notice reaches the conversation")
	}
	if api.sessionCompletionsOwned(session) {
		t.Fatal("the session must be released after the reviewer finishes")
	}
}
