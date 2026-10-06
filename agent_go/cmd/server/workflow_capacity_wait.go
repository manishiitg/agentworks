package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// scheduleRunStatusWaitingForCapacity is a run that stopped without failing.
//
// PLAT-101. It is deliberately its own status rather than a flavor of
// "error". A failed run is finished and its remaining steps are abandoned; a
// waiting run is mid-flight, holds completed steps whose side effects must not
// be replayed, and will continue on its own. Collapsing the two is what made a
// capacity wall look identical to a defect in run history, and what let the
// next cron tick start a duplicate run over the top of it.
const scheduleRunStatusWaitingForCapacity = "waiting_for_capacity"

// readWorkflowCapacityWait loads a run's capacity-wait record, if any.
func readWorkflowCapacityWait(ctx context.Context, workspacePath, runFolder string) (*stepworkflow.WorkflowCapacityWait, bool) {
	if workspacePath == "" || runFolder == "" {
		return nil, false
	}
	content, exists, err := readFileFromWorkspace(ctx, stepworkflow.WorkflowCapacityWaitPath(workspacePath, runFolder))
	if err != nil || !exists {
		return nil, false
	}
	return stepworkflow.ParseWorkflowCapacityWait(content)
}

// outstandingCapacityWait returns the newest run of a workflow that is
// currently suspended on capacity, along with its record.
//
// "Currently" is decided by the run's own status rather than by the presence of
// the file, so a record left behind by a run that later completed, was stopped,
// or failed cannot suppress the schedule.
func (s *SchedulerService) outstandingCapacityWait(ctx context.Context, workspacePath string) (*ScheduleRunEntry, *stepworkflow.WorkflowCapacityWait) {
	if workspacePath == "" {
		return nil, nil
	}
	runs, err := ReadScheduleRuns(ctx, workspacePath)
	if err != nil {
		return nil, nil
	}
	var newest *ScheduleRunEntry
	for i := range runs {
		if runs[i].Status != scheduleRunStatusWaitingForCapacity {
			continue
		}
		if newest == nil || runs[i].StartedAt.After(newest.StartedAt) {
			newest = &runs[i]
		}
	}
	if newest == nil {
		return nil, nil
	}
	wait, ok := readWorkflowCapacityWait(ctx, workspacePath, newest.RunFolder)
	if !ok {
		// The run says it is waiting but the record that says where to resume
		// from is gone. Reporting no wait is the safe reading: it lets the
		// schedule fire again rather than blocking forever on a resume that
		// cannot be armed.
		return nil, nil
	}
	return newest, wait
}

// capacityWaitOutlastsNextRun reports whether the wait extends past when this
// schedule would next have fired.
//
// A pause shorter than the gap to the next run is self-healing and worth no
// interruption. A pause that outlasts it means the schedule is genuinely
// degraded — runs are being skipped — which is the point at which somebody
// should be told rather than left to discover a quiet gap in the history.
func capacityWaitOutlastsNextRun(wait *stepworkflow.WorkflowCapacityWait, nextRun time.Time) bool {
	if wait == nil {
		return false
	}
	if wait.RetryAt.IsZero() {
		// An unknown reset has no end, so it always outlasts the next run.
		return true
	}
	if nextRun.IsZero() {
		return false
	}
	return wait.RetryAt.After(nextRun)
}

// classifyCapacityWait decides whether a finished run stopped on a capacity
// wall rather than failing.
//
// Two independent signals must agree, and both are needed. The typed error says
// the step loop chose to suspend, but it crosses several wrapping layers on the
// way here and any one of them formatting instead of wrapping would lose it.
// The on-disk record is what a resume is actually armed from, so a wall with no
// record is not resumable and must be reported as the failure it effectively
// is.
//
// The record is also bounded by the run's own start time. Run folders are
// reused — iteration-0 is the live slot — so a record left by an earlier run in
// the same folder would otherwise suspend a run that never hit a wall at all.
func (s *SchedulerService) classifyCapacityWait(ctx context.Context, sctx *ScheduleContext, execErr error, runFolder string, runStartedAt time.Time) (*stepworkflow.WorkflowCapacityWait, bool) {
	if execErr == nil || sctx == nil || sctx.RelayRuntime == "python" {
		return nil, false
	}
	if !errors.Is(execErr, stepworkflow.ErrWorkflowWaitingForCapacity) {
		return nil, false
	}
	wait, ok := readWorkflowCapacityWait(ctx, sctx.WorkspacePath, runFolder)
	if !ok {
		s.logf(sctx, "[SCHEDULER] a step reported a capacity wall but no %s was written for run folder %q; treating it as a failure because there is nothing to resume from",
			stepworkflow.WorkflowCapacityWaitFilename, runFolder)
		return nil, false
	}
	if !capacityWaitBelongsToRun(wait, runStartedAt) {
		s.logf(sctx, "[SCHEDULER] ignoring a capacity-wait record from %s: it predates this run, which started %s",
			wait.RecordedAt.Format(time.RFC3339), runStartedAt.Format(time.RFC3339))
		return nil, false
	}
	return wait, true
}

// resumeDueCapacityWaits wakes suspended runs whose window has reopened.
//
// Runs suspended on capacity do not wake on a cron occurrence — their reset
// instant is set by the provider and almost never lands on the schedule — so
// they are evaluated on every tick instead.
//
// A wait with no stated reset is never woken here. Waking on a guessed instant
// resumes straight back into the same wall and burns the run's remaining steps
// a second time; those runs wait for a person, which is the honest outcome when
// the provider declined to say when capacity returns.
func (s *SchedulerService) resumeDueCapacityWaits(ctx context.Context, now time.Time) {
	if s == nil {
		return
	}
	type candidate struct {
		sctx *ScheduleContext
	}
	seen := make(map[string]bool)
	var candidates []candidate
	s.mu.Lock()
	for _, job := range s.jobs {
		if job == nil || job.sctx == nil {
			continue
		}
		// One workflow can carry several schedules; a suspended run belongs to
		// the workflow, so evaluating it once per workflow avoids several
		// schedules racing to resume the same run.
		if seen[job.sctx.WorkspacePath] {
			continue
		}
		seen[job.sctx.WorkspacePath] = true
		candidates = append(candidates, candidate{sctx: job.sctx})
	}
	s.mu.Unlock()

	// API invocations are durable rows, not registered cron jobs. Include every
	// suspended invocation, even when several share one published version.
	s.stateStoreMu.RLock()
	store := s.stateStore
	s.stateStoreMu.RUnlock()
	if store != nil {
		runs, err := store.WaitingCapacityRuns(ctx)
		if err != nil {
			scheduleLogf("[SCHEDULER] capacity wait discovery failed: %v", err)
			return
		}
		for _, run := range runs {
			resumeCtx, err := s.capacityResumeContext(ctx, run, now)
			if err != nil {
				s.transitionScheduleRun(ctx, nil, schedulerstate.Transition{RunID: run.RunID, To: schedulerstate.StateFailed, ErrorMessage: err.Error(), Reason: "capacity resume unavailable", At: now})
				_ = UpdateScheduleRun(ctx, run.ScopeID, run.RunID, "error", err.Error(), nil, run.RunFolder, "")
				continue
			}
			if resumeCtx != nil {
				go s.triggerSchedule(resumeCtx, now)
			}
		}
		return
	}
	for _, c := range candidates {
		waitingRun, wait := s.outstandingCapacityWait(ctx, c.sctx.WorkspacePath)
		if waitingRun == nil || !wait.ResumeDue(now) {
			continue
		}
		resumeCtx := *c.sctx
		resumeCtx.CapacityResumeRunID = waitingRun.ID
		resumeCtx.CapacityResumeRunFolder = waitingRun.RunFolder
		resumeCtx.CapacityResumeFromStep = wait.StepNumber
		s.logf(&resumeCtx, "[SCHEDULER] ▶️ Provider capacity returned; resuming run %s at step %d (%s)",
			waitingRun.ID, wait.StepNumber, wait.Describe())
		go s.triggerSchedule(&resumeCtx, now)
	}
}

// capacityResumeContext restores the pinned workspace and delivery. It never
// synthesizes a schedule INPUT for an existing API invocation.
func (s *SchedulerService) capacityResumeContext(ctx context.Context, run schedulerstate.Run, now time.Time) (*ScheduleContext, error) {
	if run.ScopeType != "workflow" {
		return nil, nil
	}
	wait, exists := readWorkflowCapacityWait(ctx, run.ScopeID, run.RunFolder)
	if !exists {
		return nil, fmt.Errorf("capacity wait checkpoint is unavailable")
	}
	if !wait.ResumeDue(now) {
		return nil, nil
	}
	manifest, found, err := ReadWorkflowManifest(ctx, run.ScopeID)
	if err != nil || !found {
		return nil, fmt.Errorf("capacity resume workflow is unavailable")
	}
	var sched *WorkflowSchedule
	for i := range manifest.Schedules {
		if manifest.Schedules[i].ID == run.ScheduleID {
			sched = &manifest.Schedules[i]
			break
		}
	}
	if sched == nil || !sched.Enabled {
		return nil, fmt.Errorf("capacity resume schedule is disabled or removed")
	}
	resume := buildScheduleContext(run.ScopeID, manifest, *sched)
	resume.TriggerSource = run.TriggerSource
	raw, exists, readErr := readFileFromWorkspace(ctx, webhookInputPath(run.ScopeID, run.RunID))
	if readErr != nil {
		return nil, readErr
	}
	if exists {
		var input WorkflowWebhookDelivery
		if json.Unmarshal([]byte(raw), &input) != nil || input.RunID != run.RunID {
			return nil, fmt.Errorf("capacity resume delivery is invalid")
		}
		if err := webhookDeliveryStartError(*sched, &input); err != nil {
			return nil, err
		}
		resume.WebhookInput = &input
		if input.Group != "" && manifest.Kind != "relay" {
			resume.Schedule.GroupNames = []string{input.Group}
		}
		stepID, routes := resolvedWebhookExecutionTarget(*sched, &input)
		resume.Schedule.RouteSelections = routes
		if sched.Webhook != nil {
			webhook := *sched.Webhook
			webhook.StepID = stepID
			resume.Schedule.Webhook = &webhook
		}
	} else if run.TriggerSource == "webhook" {
		return nil, fmt.Errorf("capacity resume delivery is unavailable")
	}
	if manifest.Kind == "relay" && resume.WebhookInput != nil && sched.IsFunctionTrigger() {
		draft, err := relayDraftWorkspaceForRelease(ctx, run.ScopeID)
		if err != nil {
			return nil, err
		}
		if version := strings.Split(run.ScopeID, "/"); workflowtypes.RelayReleaseWorkspace(run.ScopeID) != "" {
			release, releaseWorkspace, err := readRelayRelease(ctx, draft, version[3])
			if err != nil {
				return nil, err
			}
			if err := verifyRelayRelease(ctx, release, releaseWorkspace); err != nil {
				return nil, err
			}
		}
		live, found, err := ReadWorkflowManifest(ctx, draft)
		if err != nil || !found {
			return nil, fmt.Errorf("capacity resume Relay is unavailable")
		}
		var payload struct {
			Function string        `json:"function"`
			Caller   triggerCaller `json:"relay_trigger_caller"`
			User     string        `json:"relay_caller"`
		}
		if json.Unmarshal(resume.WebhookInput.Payload, &payload) != nil {
			return nil, fmt.Errorf("capacity resume caller is unavailable")
		}
		if payload.Caller.ID == "" && payload.User != "" {
			payload.Caller = triggerCaller{Type: triggerCallerUser, ID: payload.User}
		}
		liveSchedule, err := findWorkflowFunctionTrigger(live, payload.Function)
		if payload.Caller.ID == "" || err != nil || !workflowFunctionCallerAllowed(liveSchedule.Function, payload.Caller) {
			return nil, fmt.Errorf("capacity resume caller is no longer allowed")
		}
		access := workflowAccessForManifest(principalClaims(payload.Caller.ID), live)
		if payload.Caller.Type == triggerCallerUser && (access == WorkflowAccessNone || !userAllowedWorkflowID(principalClaims(payload.Caller.ID), live.ID)) {
			return nil, fmt.Errorf("capacity resume caller no longer has Relay access")
		}
	}
	resume.CapacityResumeRunID = run.RunID
	resume.CapacityResumeRunFolder = run.RunFolder
	resume.CapacityResumeFromStep = wait.StepNumber
	return resume, nil
}

// capacityWaitBelongsToRun reports whether a record was written by the run that
// just finished.
//
// Run folders are reused — iteration-0 is the live slot, rotated to a permanent
// name only on the next run — so a record left behind by an earlier run in the
// same folder is readable by a later one. Without this check that stale record
// would suspend a run that never hit a wall, and suspend it at whatever step
// the earlier run happened to stop on.
func capacityWaitBelongsToRun(wait *stepworkflow.WorkflowCapacityWait, runStartedAt time.Time) bool {
	if wait == nil {
		return false
	}
	if runStartedAt.IsZero() {
		// Nothing to compare against. Trusting the record is the lesser risk:
		// the typed error already established that this run hit a wall.
		return true
	}
	return !wait.RecordedAt.Before(runStartedAt)
}

// scheduleQuotaMaxReadingAge bounds how old a cached quota reading may be
// before it is treated as no information at all.
//
// A stale number looks exactly as confident as a fresh one, and this gate
// decides whether a scheduled run happens. Refusing to fire on a reading from
// hours ago would skip runs for a window that has long since reopened.
const scheduleQuotaMaxReadingAge = 30 * time.Minute

// scheduleUsesClaudeCode reports whether this schedule's builder LLM is Claude
// Code.
//
// The gate below keys on the Claude Code credential, and a workflow can share
// that deployment-wide credential without ever calling Claude — a Codex or Pi
// workflow, for instance. Skipping its runs because a Claude window is
// exhausted would stop a schedule for a limit it never touches, which is a
// worse failure than the one this gate prevents.
func scheduleUsesClaudeCode(sctx *ScheduleContext) bool {
	if sctx == nil || sctx.Capabilities.LLMConfig == nil {
		return false
	}
	builderLLM := sctx.Capabilities.LLMConfig.BuilderLLM
	if builderLLM == nil {
		resolved, _, ok := workflowtypes.ResolveProviderProfileConfig(sctx.Capabilities.LLMConfig)
		if !ok {
			return false
		}
		builderLLM = resolved
	}
	if builderLLM == nil {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(builderLLM.Provider))
	return provider == claudeCodeProviderID || provider == "claudecode"
}

// scheduleQuotaBlock reports whether the account this schedule runs under has
// an exhausted quota window that has not yet reopened.
//
// This is the cheap half of the capacity story and it deliberately answers only
// the question that needs no guesswork: is starting right now definitely
// pointless? A window at 100% cannot serve step one, so firing would create a
// run that fails immediately and, before stage 3, would have replayed nothing
// but still recorded a red run.
//
// It does not try to predict whether a run will FIT in the remaining quota.
// That would need a per-step cost estimate, and steps are agentic — the same
// step can make three tool calls one day and forty the next, so any predictor
// would either idle the schedule constantly or fail to protect it. Runs that
// exhaust their window mid-flight are handled by suspending and resuming, not
// by refusing to start.
//
// Unknown always means proceed: no credential, no cached reading, a stale
// reading, or a window with no stated reset all fall through to a normal fire.
func (s *SchedulerService) scheduleQuotaBlock(ctx context.Context, sctx *ScheduleContext, now time.Time) (string, time.Time, bool) {
	if s == nil || s.api == nil || sctx == nil {
		return "", time.Time{}, false
	}
	if !scheduleUsesClaudeCode(sctx) {
		return "", time.Time{}, false
	}
	keys, err := s.api.scheduleProviderAPIKeys(ctx, sctx)
	if err != nil || keys == nil || keys.ClaudeCodeOAuthToken == nil {
		return "", time.Time{}, false
	}
	accountKey := llmtypes.AccountRateLimitKey(*keys.ClaudeCodeOAuthToken)
	windows, _, ok := llmtypes.AccountRateLimitWindows(accountKey, now, scheduleQuotaMaxReadingAge)
	if !ok {
		return "", time.Time{}, false
	}
	// EarliestReset is the right question here, not MostConstrainedReset: this
	// gate only blocks on a window that is genuinely at its limit, and when
	// several are, the first to reopen is when work can continue.
	resetsAt, window := llmtypes.EarliestReset(windows, now)
	if resetsAt.IsZero() {
		return "", time.Time{}, false
	}
	return window, resetsAt, true
}

// quotaPacingForSchedule resolves the account identity and threshold a run
// should pace against, or ("", 0) when this workflow did not opt in.
//
// The account key is resolved here rather than in the orchestrator for the same
// reason the schedule-level gate resolves it here: credential access belongs to
// the server layer, and pushing it into the workflow engine would widen that
// surface for a feature that only needs an opaque identity.
func (s *SchedulerService) quotaPacingForSchedule(ctx context.Context, sctx *ScheduleContext) (string, int) {
	if s == nil || s.api == nil || sctx == nil {
		return "", 0
	}
	if !scheduleUsesClaudeCode(sctx) {
		return "", 0
	}
	manifest, found, err := ReadWorkflowManifest(ctx, sctx.WorkspacePath)
	if err != nil || !found || !manifest.PacingEnabled() {
		return "", 0
	}
	keys, err := s.api.scheduleProviderAPIKeys(ctx, sctx)
	if err != nil || keys == nil || keys.ClaudeCodeOAuthToken == nil {
		return "", 0
	}
	accountKey := llmtypes.AccountRateLimitKey(*keys.ClaudeCodeOAuthToken)
	if accountKey == "" {
		return "", 0
	}
	return accountKey, manifest.PaceThreshold()
}

// quotaExhaustedMarker is how llmerrors renders a permanently exhausted quota
// inside a wrapped provider failure.
const quotaExhaustedMarker = "[quota_exhausted]"

// turnLevelCapacityWait recognizes a run that hit the wall before any step ran.
//
// The step loop writes capacity_wait.json and returns a typed wait, so a wall
// mid-run suspends cleanly. A wall on the orchestrator's OWN first turn reaches
// neither: no step started, so nothing recorded where to resume from, and the
// run would be filed as a plain error. hetzner-ssh on 2026-08-18 20:06 did
// exactly that — both turns failed on quota_exhausted in 8.1 seconds, before
// step 1.
//
// The reset instant comes from the account's cached quota reading rather than
// from the error, because the failure text that survives the turn boundary is a
// formatted string and a formatted string cannot carry an instant. When the
// cache has nothing fresh the wait is recorded with no reset — honest, and it
// waits for a person rather than waking on a guess.
func (s *SchedulerService) turnLevelCapacityWait(ctx context.Context, sctx *ScheduleContext, execErr error, runFolder string, now time.Time) (*stepworkflow.WorkflowCapacityWait, bool) {
	if execErr == nil || sctx == nil || sctx.RelayRuntime == "python" {
		return nil, false
	}
	if !strings.Contains(execErr.Error(), quotaExhaustedMarker) {
		return nil, false
	}
	wait := &stepworkflow.WorkflowCapacityWait{
		SchemaVersion: 1,
		WorkspacePath: sctx.WorkspacePath,
		RunFolder:     runFolder,
		// Step 0: the wall landed before the first step, so there is no step to
		// resume AT — the run restarts from the beginning, which is safe
		// precisely because nothing executed and nothing has side effects yet.
		StepNumber: 0,
		RecordedAt: now.UTC(),
		Reason:     execErr.Error(),
	}
	if accountKey, _ := s.quotaPacingAccount(ctx, sctx); accountKey != "" {
		if windows, _, ok := llmtypes.AccountRateLimitWindows(accountKey, now, scheduleQuotaMaxReadingAge); ok {
			if resetsAt, window := llmtypes.MostConstrainedReset(windows, now); !resetsAt.IsZero() {
				wait.RetryAt = resetsAt
				wait.Window = window
			}
		}
	}
	return wait, true
}

// quotaPacingAccount resolves just the account identity, without requiring the
// workflow to have opted into pacing. A capacity wall needs the account's reset
// time whether or not the workflow asked to be paced.
func (s *SchedulerService) quotaPacingAccount(ctx context.Context, sctx *ScheduleContext) (string, error) {
	if s == nil || s.api == nil || sctx == nil || !scheduleUsesClaudeCode(sctx) {
		return "", nil
	}
	keys, err := s.api.scheduleProviderAPIKeys(ctx, sctx)
	if err != nil || keys == nil || keys.ClaudeCodeOAuthToken == nil {
		return "", err
	}
	return llmtypes.AccountRateLimitKey(*keys.ClaudeCodeOAuthToken), nil
}

func (api *StreamingAPI) scheduleProviderAPIKeys(ctx context.Context, sctx *ScheduleContext) (*llm.ProviderAPIKeys, error) {
	keys, err := api.resolveEffectiveAPIKeys(ctx, sctx.OwnerUserID, sctx.WorkspacePath, nil)
	if err != nil {
		return nil, err
	}
	if config := sctx.Capabilities.LLMConfig; config != nil {
		builder := presetPrimaryLLMForChat(config)
		if builder != nil && builder.ConnectionID != "" {
			return api.connectionAPIKeys(ctx, providerAccountScope{Principal: sctx.OwnerUserID, WorkspacePath: sctx.WorkspacePath}, builder.Provider, builder.ConnectionID)
		}
	}
	return keys, nil
}
