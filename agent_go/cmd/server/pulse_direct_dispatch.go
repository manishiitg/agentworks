package server

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/executor"

	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsemodules"
)

// The Gate (an agent) decides which reviewers are due and records that in the
// worklist; the scheduler already reads it to build the pass. It used to then
// ask the Pulse conversation's agent, per reviewer, to re-read the same
// worklist and call run_in_background itself: an agent doing plumbing, which
// could misfire, and whose missing receipts were reconciled by yet another
// turn in that conversation. The runtime now starts each due reviewer itself,
// through the session's own run_in_background tool, so the reviewer gets
// exactly the scope, model and tool limits it had, waits for it, and reads its
// recorded result. The reviewer owns its steps (background_step_ownership.go)
// and is reminded to record its result before it finishes.

var pulseReviewerExecutionIDPattern = regexp.MustCompile(`execution_id: "([^"]+)"`)

var pulseReviewerPollInterval = time.Second

// pulseReviewerNames are the reviewer names shown in the UI and logs.
var pulseReviewerNames = map[string]string{
	pulseModulePlanDriftReview:    "Plan Drift Review",
	pulseModuleTechnicalReview:    "Technical Review",
	pulseModuleArchitectureReview: "Architecture Review",
	pulseModuleStrategicReview:    "Goal Work",
}

// pulseReviewerInstruction is the reviewer's task, the part of the old
// dispatch turn that was meant for the reviewer rather than the dispatcher.
func pulseReviewerInstruction(pulseRunID, module string) string {
	if module == pulseModulePlanDriftReview {
		return fmt.Sprintf(`PULSE PLAN DRIFT REVIEW. pulse_run_id=%q, module=%q. The Gate marked this module due and the runtime started you. Load read_skill(skills=[{"name":"builder-reference","path":"references/plan-drift-review.md"}]) and follow it exactly. Establish ground truth per due step, apply and verify safe workflow-owned fixes directly, and only route what you cannot safely fix yourself: a genuine human decision, a platform-owned boundary, or (rarely, as a last resort) a fixer_handoff for technical_review. Finish with the terminal result for plan_drift_review. Do not render a dashboard, back up, publish or notify.`, pulseRunID, module)
	}
	_, reference, contract := pulseModuleReviewParts(module)
	return fmt.Sprintf(`PULSE %s. pulse_run_id=%q, module=%q. The Gate marked this module due and the runtime started you. Read get_pulse_state(view="review_notes", module=%q) once for relevant prior reasoning. Load read_skill(skills=[{"name":"builder-reference","path":"references/%s.md"}]). %s
%sDo not render a dashboard, back up, publish or notify.`, strings.ToUpper(strings.ReplaceAll(module, "_", " ")), pulseRunID, module, module, reference, contract, pulseReviewerRecordRules)
}

// runPulseReviewerDirect starts the reviewer for a Pulse lifecycle step and
// waits for it. direct is false when the step is not a reviewer or the session
// has no workshop yet, and the caller uses the dispatch turn instead.
func (s *SchedulerService) runPulseReviewerDirect(ctx context.Context, sctx *ScheduleContext, sessionID, pulseRunID, stepLabel string) (result pulseLifecycleStepRunResult, direct bool) {
	module := pulsemodules.ForStepLabel(stepLabel)
	name, known := pulseReviewerNames[module]
	if !known || s == nil || s.api == nil {
		return pulseLifecycleStepRunResult{}, false
	}
	value, ok := s.api.workshopChatSessions.Load(sessionID)
	if !ok {
		return pulseLifecycleStepRunResult{}, false
	}
	workshop, ok := value.(*todo_creation_human.WorkshopChatSession)
	if !ok || workshop == nil {
		return pulseLifecycleStepRunResult{}, false
	}
	// Same guard the dispatch turn applied: not due, or already recorded.
	if validatePulseDueModuleResultsFor(ctx, sctx.WorkspacePath, pulseRunID, module) == nil {
		s.sessionLogf(sctx, sessionID, "[PULSE] %s: not due or already recorded; nothing to start", module)
		return pulseLifecycleStepRunResult{outcome: pulseLifecycleStepCompleted}, true
	}

	// Only the runtime delivers results in this session while the reviewer
	// runs, so its completion cannot start a competing turn.
	release := s.api.claimSessionCompletions(sessionID)
	defer release()
	workshop.SetPulseLifecycleTurn(true)

	// The call carries this session as its caller, so the workshop's bound
	// run_in_background re-checks the session's owner and workflow access, and
	// refuses an executor bound to any other session (the registry's global
	// fallback holds whichever workshop registered last).
	callCtx := executor.WithSessionID(ctx, sessionID)
	out, err := codeexec.CallCustomToolWithSession(callCtx, sessionID, "run_in_background", map[string]interface{}{
		"name":          name,
		"instruction":   pulseReviewerInstruction(pulseRunID, module),
		"review_module": module,
		"pulse_run_id":  pulseRunID,
	})
	if err != nil {
		return pulseLifecycleStepRunResult{outcome: pulseLifecycleStepWaitFailed, err: fmt.Errorf("start %s: %w", name, err)}, true
	}
	match := pulseReviewerExecutionIDPattern.FindStringSubmatch(out)
	if len(match) != 2 {
		return pulseLifecycleStepRunResult{outcome: pulseLifecycleStepWaitFailed, err: fmt.Errorf("start %s: no execution id in %q", name, out)}, true
	}
	executionID := match[1]
	s.sessionLogf(sctx, sessionID, "[PULSE] started %s directly (execution %s)", name, executionID)

	agent, err := s.waitForPulseReviewer(ctx, sessionID, executionID, schedulerWorkshopLiveChildCeiling)
	if err != nil {
		outcome := pulseLifecycleStepWaitFailed
		if ctx.Err() != nil {
			outcome = pulseLifecycleStepInterrupted
		}
		return pulseLifecycleStepRunResult{outcome: outcome, err: fmt.Errorf("%s: %w", name, err)}, true
	}
	// The runtime has the result; no notice goes to the conversation.
	if agent.beginCompletionNotification() {
		agent.finishCompletionNotification(true)
	}
	snap := agent.GetSnapshot()
	s.sessionLogf(sctx, sessionID, "[PULSE] %s finished: %s", name, snap.Status)
	switch snap.Status {
	case BGAgentCompleted:
		return pulseLifecycleStepRunResult{outcome: pulseLifecycleStepCompleted}, true
	case BGAgentCanceled:
		return pulseLifecycleStepRunResult{outcome: pulseLifecycleStepInterrupted, err: fmt.Errorf("%s was canceled", name)}, true
	default:
		return pulseLifecycleStepRunResult{outcome: pulseLifecycleStepWaitFailed, err: fmt.Errorf("%s failed: %s", name, strings.TrimSpace(snap.Error))}, true
	}
}

// waitForPulseReviewer waits until the reviewer execution is no longer
// running.
func (s *SchedulerService) waitForPulseReviewer(ctx context.Context, sessionID, executionID string, ceiling time.Duration) (*BackgroundAgent, error) {
	started := time.Now()
	ticker := time.NewTicker(pulseReviewerPollInterval)
	defer ticker.Stop()
	for {
		if agent := s.api.bgAgentRegistry.Get(sessionID, executionID); agent != nil {
			if agent.GetSnapshot().Status != BGAgentRunning {
				return agent, nil
			}
		} else if time.Since(started) > 30*time.Second {
			return nil, fmt.Errorf("execution %s was never registered", executionID)
		}
		if ceiling > 0 && time.Since(started) >= ceiling {
			return nil, fmt.Errorf("still running after %s", ceiling)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
