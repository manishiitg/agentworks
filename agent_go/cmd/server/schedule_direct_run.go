package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/contractupgrade"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// executeDirectScheduledRun runs the saved workflow in the schedule's own
// pre-allocated run folder. Cron-side gating (ordering, collision policy,
// catch-up, ownership, inputs) has already been applied by the caller; a failed
// start comes back as the run's error, which the scheduler records, notifies and
// wakes the linked Crew for exactly as it does for a failed run.
func (s *SchedulerService) executeDirectScheduledRun(ctx context.Context, sctx *ScheduleContext, runID string) (string, string, error) {
	folder := strings.TrimSpace(sctx.ScheduledRunFolder)
	if folder == "" {
		return "", "", fmt.Errorf("this schedule has no assigned run folder")
	}
	manifest, found, err := ReadWorkflowManifest(ctx, sctx.WorkspacePath)
	if err != nil {
		return "", folder, err
	}
	if !found {
		return "", folder, fmt.Errorf("workflow manifest not found")
	}
	if err := directWebhookPreflight(manifest); err != nil {
		return "", folder, err
	}
	if costobserver.AttributionFromContext(ctx).Activity == "" {
		ctx = costActivityContext(ctx, sctx.OwnerUserID, "scheduled_run", "")
	}
	sessionID := s.newScheduleSessionID(sctx)
	// A late contract migration must not stamp the workflow under a run that is
	// still going.
	contractupgrade.MarkScheduled(sessionID)
	defer contractupgrade.ClearScheduled(sessionID)
	invocation := &stepworkflow.ScheduleInvocation{RunID: runID, ScheduleID: sctx.Schedule.ID, RunFolder: folder, TriggerSource: sctx.TriggerSource, ScheduledFor: sctx.ScheduledFor, AllowParallel: scheduleAllowsParallel(sctx.Schedule)}
	s.api.scheduleInvocations.Store(sessionID, invocation)
	defer s.api.scheduleInvocations.Delete(sessionID)
	s.updateRuntimeState(scheduleRuntimeKey(sctx), func(state *ScheduleRuntimeState) { state.LastSessionID = sessionID })
	if err := UpdateScheduleRun(ctx, sctx.WorkspacePath, runID, "running", "", nil, folder, sessionID); err != nil {
		return sessionID, folder, err
	}
	req := s.buildWorkshopRequest(ctx, sctx, runID)
	opts, err := configureDirectWebhookRequest(req, sctx, folder)
	if err != nil {
		return sessionID, folder, err
	}
	opts.RunKind = invocation.RunKind()
	opts.ScheduleRunID = invocation.RunID
	opts.ScheduleID = invocation.ScheduleID
	opts.TriggerSource = invocation.TriggerSource
	opts.ScheduledFor = invocation.ScheduledFor
	req["query"] = "Run: " + sctx.Schedule.Name
	// A catch-up start stands for every occurrence it replaced: one run.
	s.sessionLogf(sctx, sessionID, "[SCHEDULER] Direct schedule run=%s run_as=%s folder=%s occurrences=%d", runID, sctx.OwnerUserID, folder, max(1, sctx.QueuedOccurrenceCount))
	startedAt := time.Now().UTC()
	err = s.api.startSessionInternal(context.WithValue(ctx, directWebhookExecutionKey{}, opts), req, sessionID, sctx.OwnerUserID, nil)
	sctx.ProducedRunEvidence = err == nil || s.scheduledWorkflowExecutionProducedEvidence(sessionID, startedAt)
	return sessionID, folder, err
}
