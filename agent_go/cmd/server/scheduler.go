package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/contractupgrade"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsemodules"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/robfig/cron/v3"
)

const scheduledBackgroundNoPollingInstruction = "After launching background workflow or step work, do not babysit it with sleep/list_executions/query_step polling loops. Use at most one immediate query_step if you need to confirm the execution_id/status, then stop; [AUTO-NOTIFICATION] messages will resume the conversation when background work completes."

// ScheduleContext bundles everything needed to identify and execute a schedule.
type ScheduleContext struct {
	WorkspacePath string
	WorkflowID    string
	WorkflowLabel string
	WorkflowKind  string
	RelayRuntime  string
	// RelayLegacyGroupName reads old release configuration without group execution.
	RelayLegacyGroupName string
	Schedule             WorkflowSchedule
	WebhookInput         *WorkflowWebhookDelivery
	Capabilities         WorkflowCapabilities
	// OwnerUserID is the workflow's authenticated execution identity. It is
	// normally WorkflowManifest.CreatedBy (or the first recorded owner), and
	// is the explicit local owner for an ownerless legacy workflow only in
	// single-user mode. Ambiguous ownerless multi-user workflows stay empty
	// and cannot cross the action-tool authorization boundary.
	OwnerUserID   string
	TriggerSource string // "cron" (default) or "manual"; encoded into the session ID
	// OriginSessionID is the chat session that triggered this run, when one did.
	// A scheduled run mints its own session, so without this link its terminals
	// are invisible to the tab that asked for the run. Empty for cron.
	OriginSessionID string
	// ForcePulseReview is used by the toolbar's one-off Pulse action. Normal
	// schedules use workflow.json pulse.enabled instead.
	ForcePulseReview bool
	// PulseOnly suppresses the normal workflow message for the toolbar's one-off
	// Pulse action. Version preflight still runs before Pulse, which reviews the
	// latest retained workflow evidence and then executes the normal finalizer.
	PulseOnly bool
	// PulseFixRun marks a Pulse fix run (pulse_fix_run.go): no Gate agent,
	// Technical Review+Fix only, and a light finish. PulseFixReason is why.
	PulseFixRun    bool
	PulseFixReason string
	// AfterRunBackup and AfterRunPublish select the after-run housekeeping
	// turn's actions (after_run.go): only those with something to do.
	AfterRunBackup  bool
	AfterRunPublish bool
	// PulseGoalCheck marks the daily goal check (pulse_goal_check.go): one
	// short goal turn, no Gate, reviewers or finalizer. It shares the full
	// Pulse's schedule ID, so it never runs beside a Pulse pass.
	PulseGoalCheck         bool
	PulseEvidenceRunFolder string
	PulseEvidenceRunStatus string
	CalendarItem           *CalendarScheduleItem
	// ScheduledFor is the durable identity of a cron/calendar occurrence. It is
	// empty for a manual trigger, whose actual trigger time is its identity.
	ScheduledFor time.Time
	// QueuedExpiresAt preserves the first queue attempt's max-start deadline
	// across retries. A busy workflow must not extend a stale occurrence forever.
	QueuedExpiresAt       time.Time
	QueuedOccurrenceCount int

	// ProducedRunEvidence reports whether this invocation actually started the
	// workflow and created or restarted a run folder. It is deliberately
	// independent of success: a failed run still produced evidence that Pulse
	// should review. A preflight abort against a pre-existing, untouched run
	// folder (e.g. a workflow reusing iteration-0) did not.
	ProducedRunEvidence bool

	// CapacityResume* carry a run that was suspended on a provider capacity
	// wall back into execution (PLAT-101). The run is resumed in place — same
	// run row, same run folder, starting at the step that could not run —
	// because the steps before it already completed and did real work that must
	// not be replayed.
	CapacityResumeRunID     string
	CapacityResumeRunFolder string
	CapacityResumeFromStep  int
	// ScheduledRunFolder is allocated and durably bound before a producing
	// saved-schedule session starts. Pulse-only runs leave it empty.
	ScheduledRunFolder string
}

const manualWorkflowPulseScheduleID = "manual-pulse"

const scheduleScopeSeparator = "\x1f"

func workflowScheduleRuntimeKey(workspacePath, scheduleID string) string {
	return strings.Join([]string{"workflow", filepath.Clean(strings.TrimSpace(workspacePath)), strings.TrimSpace(scheduleID)}, scheduleScopeSeparator)
}

func scheduleRuntimeKey(sctx *ScheduleContext) string {
	if sctx == nil {
		return ""
	}
	if sctx.WebhookInput != nil && strings.TrimSpace(sctx.WebhookInput.RunID) != "" {
		// Parallel deliveries need independent lifecycle state. Keep the schedule
		// ID last so scheduleRuntimeKeyHasID and schedule status aggregation still
		// recognize every delivery lane.
		return strings.Join([]string{"workflow", filepath.Clean(strings.TrimSpace(sctx.WorkspacePath)), "webhook-run", strings.TrimSpace(sctx.WebhookInput.RunID), strings.TrimSpace(sctx.Schedule.ID)}, scheduleScopeSeparator)
	}
	return workflowScheduleRuntimeKey(sctx.WorkspacePath, sctx.Schedule.ID)
}

func scheduleRuntimeKeyHasID(key, scheduleID string) bool {
	return strings.HasSuffix(key, scheduleScopeSeparator+strings.TrimSpace(scheduleID))
}

func activeWebhookDeliveryCount(states map[string]*ScheduleRuntimeState, workspacePath, scheduleID string) int {
	active := 0
	wantWorkspace := filepath.Clean(strings.TrimSpace(workspacePath))
	for key, state := range states {
		parts := strings.Split(key, scheduleScopeSeparator)
		if len(parts) >= 3 && parts[1] == wantWorkspace && scheduleRuntimeKeyHasID(key, scheduleID) && state != nil && state.LastStatus == "running" {
			active++
		}
	}
	return active
}

// scheduleStateLockKeyFromRuntimeKey retains the legacy sequential/Pulse key
// projection for diagnostics and compatibility tests. Runtime recovery uses
// ActiveRunForSchedule so parallel lane keys are resolved by durable identity.
func scheduleStateLockKeyFromRuntimeKey(runtimeKey string) string {
	parts := strings.Split(runtimeKey, scheduleScopeSeparator)
	if len(parts) < 3 || parts[0] != "workflow" {
		return runtimeKey
	}
	if parts[2] == manualWorkflowPulseScheduleID || parts[2] == pulseFixRunScheduleID {
		return strings.Join([]string{"workflow-pulse", parts[1]}, scheduleScopeSeparator)
	}
	return strings.Join(parts[:2], scheduleScopeSeparator)
}

func scheduleStateScope(sctx *ScheduleContext) (scopeType, scopeID, lockKey string) {
	if sctx != nil {
		scopeID = filepath.Clean(strings.TrimSpace(sctx.WorkspacePath))
		if sctx.Schedule.ScheduleType == "webhook" {
			if sctx.WebhookInput != nil && strings.TrimSpace(sctx.WebhookInput.RunID) != "" {
				return "workflow", scopeID, strings.Join([]string{"workflow-hook-run", scopeID, sctx.Schedule.ID, strings.TrimSpace(sctx.WebhookInput.RunID)}, scheduleScopeSeparator)
			}
			return "workflow", scopeID, strings.Join([]string{"workflow-hook", scopeID, sctx.Schedule.ID}, scheduleScopeSeparator)
		}
		// Pulse runs, scheduled and fix runs alike, hold their own lock: one
		// Pulse at a time per workflow, in parallel with the workflow's own
		// schedules, never blocking them. Fix runs used to take the workflow
		// lock and refuse real schedules that came due while they ran.
		if sctx.Schedule.ID == manualWorkflowPulseScheduleID || sctx.Schedule.ID == pulseFixRunScheduleID {
			return "workflow", scopeID, strings.Join([]string{"workflow-pulse", scopeID}, scheduleScopeSeparator)
		}
		if scheduleAllowsParallel(sctx.Schedule) {
			return "workflow", scopeID, strings.Join([]string{"workflow-parallel", scopeID, sctx.Schedule.ID}, scheduleScopeSeparator)
		}
	}
	return "workflow", scopeID, strings.Join([]string{"workflow", scopeID}, scheduleScopeSeparator)
}

// newScheduleSessionID mints the session ID for a scheduled run. Encoding the
// trigger source (cron vs. manual) and the schedule ID prefix makes it easy to
// tell, just from the builder/ filename, where a conversation originated.
func (s *SchedulerService) newScheduleSessionID(sctx *ScheduleContext) string {
	trigger := sctx.TriggerSource
	if trigger == "" {
		trigger = "cron"
	}
	idPrefix := sctx.Schedule.ID
	if len(idPrefix) > 8 {
		idPrefix = idPrefix[:8]
	}
	sessionID := fmt.Sprintf("schedule-%s--%s_%d", trigger, idPrefix, time.Now().UnixNano())
	// Link it back to the chat that asked, so that chat's terminal rail can show
	// the run it started. No-op for cron, which has no originating chat.
	rememberScheduleOrigin(sessionID, sctx.OriginSessionID)
	return sessionID
}

// ScheduleRuntimeState holds in-memory runtime state for a schedule (not persisted in manifest).
type ScheduleRuntimeState struct {
	ActiveRunID         string     `json:"active_run_id,omitempty"`
	LastStatus          string     `json:"last_status,omitempty"`
	LastRunAt           *time.Time `json:"last_run_at,omitempty"`
	NextRunAt           *time.Time `json:"next_run_at,omitempty"`
	LastSessionID       string     `json:"last_session_id,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	LastDurationMs      *int64     `json:"last_duration_ms,omitempty"`
	RunCount            int        `json:"run_count"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	WaitingSince        *time.Time `json:"waiting_since,omitempty"`
	WaitingUntil        *time.Time `json:"waiting_until,omitempty"`
	WaitingReason       string     `json:"waiting_reason,omitempty"`
	QueuedOccurrences   int        `json:"queued_occurrences,omitempty"`
}

// registeredJob is a schedule registered for wall-clock evaluation.
type registeredJob struct {
	sctx      *ScheduleContext
	cronSched cron.Schedule // nil for calendar (one-time) jobs
	runAt     *time.Time    // non-nil for calendar (one-time) jobs
	lastFired time.Time     // durable scheduled occurrence cursor, not the wall-clock dispatch time
}

type dueRegisteredJob struct {
	job          *registeredJob
	scheduledFor time.Time
}

// SchedulerService manages cron job execution using wall-clock polling.
// Every 60 seconds it evaluates each registered schedule's cron expression against
// the current wall-clock time. This approach is immune to macOS App Nap and sleep/wake
// issues that wedge monotonic-clock-based timers (like gocron).
type SchedulerService struct {
	api  *StreamingAPI
	mu   sync.Mutex
	jobs map[string]*registeredJob // scoped schedule key → job
	// scheduleFingerprints tracks the persisted config loaded for each scoped
	// schedule, including disabled and calendar schedules with no future items.
	scheduleFingerprints map[string]string

	stateStoreMu sync.RWMutex
	stateStore   *schedulerstate.Store
	runCancelsMu sync.Mutex
	runCancels   map[string]context.CancelFunc

	// In-memory runtime state per schedule (survives within server lifetime, reset on restart)
	runtimeStates   map[string]*ScheduleRuntimeState
	runtimeStatesMu sync.RWMutex

	// Schedule-to-workspace index for quick lookups
	workspaceIndex   map[string]string // scheduleID → workspacePath
	workspaceIndexMu sync.RWMutex

	queuedResumeMu  sync.Mutex
	queuedLaunchMu  sync.Mutex
	queuedLaunching map[string]bool
}

func (s *SchedulerService) logf(sctx *ScheduleContext, format string, args ...interface{}) {
	scheduleLogfWithContext(scheduleLogContext(sctx), format, args...)
}

func (s *SchedulerService) sessionLogf(sctx *ScheduleContext, sessionID string, format string, args ...interface{}) {
	scheduleLogfWithContext(scheduleLogContext(sctx).WithSession(sessionID), format, args...)
}

// NewSchedulerService creates a new manifest-based SchedulerService.
func NewSchedulerService(api *StreamingAPI) *SchedulerService {
	return &SchedulerService{
		api:                  api,
		jobs:                 make(map[string]*registeredJob),
		scheduleFingerprints: make(map[string]string),
		runCancels:           make(map[string]context.CancelFunc),
		runtimeStates:        make(map[string]*ScheduleRuntimeState),
		workspaceIndex:       make(map[string]string),
	}
}

func (s *SchedulerService) DiscoverWorkflowManifestsCached(ctx context.Context, ttl time.Duration) ([]DiscoveredWorkflow, error) {
	return workflowManifests.discover(ctx, ttl)
}

func (s *SchedulerService) InvalidateWorkflowManifestCache() {
	invalidateWorkflowManifestIndex()
}

// Start scans all workspace folders for workflow.json manifests, loads enabled schedules,
// and starts the wall-clock tick loop.
func (s *SchedulerService) Start(ctx context.Context) error {
	scheduleLogf("[SCHEDULER] Starting manifest-based scheduler service...")
	s.stateStoreMu.Lock()
	if s.stateStore == nil {
		storePath := filepath.Join(fsutil.WorkspaceDocsRoot(), "_system", "schedule-state.sqlite")
		store, err := schedulerstate.Open(storePath)
		if err != nil {
			s.stateStoreMu.Unlock()
			return fmt.Errorf("initialize schedule state store: %w", err)
		}
		s.stateStore = store
	}
	if interrupted, err := s.stateStore.InterruptActiveRuns(ctx, "interrupted: server restarted", time.Now().UTC()); err != nil {
		s.stateStoreMu.Unlock()
		return fmt.Errorf("reconcile interrupted schedule runs: %w", err)
	} else if interrupted > 0 {
		scheduleLogf("[SCHEDULER] Marked %d durable schedule run(s) interrupted after restart", interrupted)
	}
	s.stateStoreMu.Unlock()

	// Discover all workflows by scanning workspace-docs/Workflow/*/workflow.json
	workflows := s.discoverWorkflows(ctx)
	scheduleLogf("[SCHEDULER] Discovered %d workflows with manifests", len(workflows))
	for i := range workflows {
		legacyPulse := workflows[i].Manifest.MigrateLegacyPulseSchedule()
		// pulse_mode -> after_run (PLAT-697 phase 0): each schedule keeps
		// exactly the backup, publish and notify its mode did.
		afterRun := workflows[i].Manifest.MigrateScheduleAfterRun()
		if legacyPulse || afterRun {
			if err := WriteWorkflowManifest(ctx, workflows[i].WorkspacePath, workflows[i].Manifest); err != nil {
				scheduleLogf("[PULSE] Failed to migrate schedules in %s: %v", workflows[i].WorkspacePath, err)
			} else {
				scheduleLogf("[PULSE] Migrated schedules in %s (legacy Pulse schedule=%v, after-run options=%v)", workflows[i].WorkspacePath, legacyPulse, afterRun)
			}
		}
	}
	// After-run options for full runs started from a chat (after_run.go).
	stepworkflow.AfterWorkflowRunHook = func(workspacePath, runFolder, status, sessionID string) {
		s.runManualAfterRunOptions(workspacePath, runFolder, status, sessionID)
	}
	for _, wf := range workflows {
		// Reviewer rows are stranded by an interrupted agent process and no
		// longer represent live work after restart. This cleanup does not infer
		// backup/publish/notify outcomes; those remain exactly as the agent wrote.
		reviews, err := finalizeAllRunningPulseReviewLogs(ctx, wf.WorkspacePath, "Pulse interrupted because the server restarted")
		if err != nil {
			scheduleLogf("[SCHEDULER] Failed to reconcile stale Pulse review rows in %s: %v", wf.WorkspacePath, err)
		} else if reviews > 0 {
			scheduleLogf("[SCHEDULER] Marked %d stale Pulse review row(s) failed in %s", reviews, wf.WorkspacePath)
		}
		recoveries, err := finalizeRunningPulseReviewRecoveries(ctx, wf.WorkspacePath, "Pulse interrupted because the server restarted before a terminal module result was recorded")
		if err != nil {
			scheduleLogf("[SCHEDULER] Failed to reconcile stale Pulse review recovery attempts in %s: %v", wf.WorkspacePath, err)
		} else if recoveries > 0 {
			scheduleLogf("[SCHEDULER] Marked %d interrupted Pulse review attempt(s) recoverable in %s", recoveries, wf.WorkspacePath)
		}
	}

	// Reconcile the legacy/UI schedule-runs.json projection from the durable
	// scheduler ledger. A final workspace write can fail after the canonical
	// terminal transition succeeds; restart is not evidence that such a run was
	// interrupted (PLAT-017).
	for _, wf := range workflows {
		runs, err := ReadScheduleRuns(ctx, wf.WorkspacePath)
		if err != nil {
			continue
		}
		fixed := 0
		for i := range runs {
			if runs[i].Status == "running" {
				status, errMsg, completedAt, ok := s.durableScheduleRunProjection(ctx, runs[i].ID)
				if !ok {
					status = "interrupted"
					errMsg = "interrupted: server restarted without durable terminal evidence"
					now := time.Now().UTC()
					completedAt = &now
				}
				runs[i].Status = status
				runs[i].Error = errMsg
				runs[i].CompletedAt = completedAt
				if completedAt != nil {
					duration := completedAt.Sub(runs[i].StartedAt).Milliseconds()
					if duration < 0 {
						duration = 0
					}
					runs[i].DurationMs = &duration
				}
				fixed++
			}
		}
		if fixed > 0 {
			_ = WriteScheduleRuns(ctx, wf.WorkspacePath, runs)
			scheduleLogf("[SCHEDULER] Marked %d stale running run(s) as error in %s", fixed, wf.WorkspacePath)
		}
	}

	loaded := 0
	for _, wf := range workflows {
		for _, sched := range wf.Manifest.Schedules {
			sctx := buildScheduleContext(wf.WorkspacePath, wf.Manifest, sched)
			if err := s.LoadSchedule(sctx); err != nil {
				scheduleLogf("[SCHEDULER] Failed to load schedule %s (%s): %v", sched.ID, sched.Name, err)
			} else if sched.Enabled && !sched.PulseReviewOnly {
				loaded++
			}
		}
	}

	scheduleLogf("[SCHEDULER] ✅ Started with %d schedules. Server time: %s, timezone: %s",
		loaded, time.Now().Format(time.RFC3339), time.Now().Location().String())

	// Wall-clock tick loop: every 60s, evaluate all registered schedules against current time.
	go s.tickLoop(ctx)

	// Wait for context cancellation
	<-ctx.Done()
	scheduleLogf("[SCHEDULER] Shutting down (context canceled)")
	return nil
}

func (s *SchedulerService) durableScheduleRunProjection(ctx context.Context, runID string) (string, string, *time.Time, bool) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return "", "", nil, false
	}
	s.stateStoreMu.RLock()
	store := s.stateStore
	if store == nil {
		s.stateStoreMu.RUnlock()
		return "", "", nil, false
	}
	run, err := store.GetRun(ctx, runID)
	s.stateStoreMu.RUnlock()
	if err != nil || (!schedulerstate.IsTerminal(run.State) && run.State != schedulerstate.StateWaitingForCapacity) {
		return "", "", nil, false
	}
	status := "error"
	switch run.State {
	case schedulerstate.StateWaitingForCapacity:
		status = scheduleRunStatusWaitingForCapacity
	case schedulerstate.StateCompleted:
		status = "success"
	case schedulerstate.StatePartial:
		status = "partial"
	case schedulerstate.StateStopped:
		status = "stopped"
	case schedulerstate.StateInterrupted:
		status = "interrupted"
	case schedulerstate.StateFailed:
		status = "error"
	}
	errMsg := run.ErrorMessage
	if status == "success" {
		errMsg = ""
	}
	return status, errMsg, run.CompletedAt, true
}

// tickLoop is the wall-clock scheduler. Every 60 seconds it evaluates each
// registered schedule against the current wall-clock time and fires any that
// are due. Unlike timer-based schedulers (gocron), this approach is immune to
// macOS App Nap and sleep/wake monotonic clock drift — if a job was missed
// during sleep, it fires on the first tick after wake.
func (s *SchedulerService) tickLoop(ctx context.Context) {
	const interval = 60 * time.Second
	const wakeThreshold = 90 * time.Second

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastTick := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			gap := t.Sub(lastTick)
			if gap > wakeThreshold {
				scheduleLogf("[SCHEDULER] 💤 WAKE_DETECTED gap=%s now=%s prev_tick=%s",
					gap.Round(time.Second), t.Format(time.RFC3339), lastTick.Format(time.RFC3339))
			}

			s.mu.Lock()
			parts := make([]string, 0, len(s.jobs))
			var toFire []dueRegisteredJob
			var missed []dueRegisteredJob
			for sid, job := range s.jobs {
				if job.cronSched != nil {
					occurrences := dueCronOccurrences(job.cronSched, job.lastFired, t)
					if len(occurrences) > 0 {
						for _, occurrence := range occurrences[:len(occurrences)-1] {
							missed = append(missed, dueRegisteredJob{job: job, scheduledFor: occurrence})
						}
						latest := occurrences[len(occurrences)-1]
						toFire = append(toFire, dueRegisteredJob{job: job, scheduledFor: latest})
					}
					parts = append(parts, fmt.Sprintf("%s next=%s", sid, job.cronSched.Next(t).UTC().Format(time.RFC3339)))
				} else if job.runAt != nil {
					if !job.runAt.After(t) && job.lastFired.Before(*job.runAt) {
						toFire = append(toFire, dueRegisteredJob{job: job, scheduledFor: job.runAt.UTC()})
					}
					parts = append(parts, fmt.Sprintf("%s at=%s", sid, job.runAt.UTC().Format(time.RFC3339)))
				}
			}
			s.mu.Unlock()

			scheduleLogf("[SCHEDULER] ❤️ heartbeat now=%s gap=%s jobs=%d due=%d missed=%d | %s",
				t.Format(time.RFC3339), gap.Round(time.Second), len(parts), len(toFire), len(missed), strings.Join(parts, ", "))

			failedOccurrenceLedger := make(map[*registeredJob]bool)
			for _, due := range missed {
				occurrenceCtx := *due.job.sctx
				occurrenceCtx.ScheduledFor = due.scheduledFor
				if err := s.recordScheduleFireDecision(context.Background(), &occurrenceCtx, "missed_scheduler_gap", "scheduler was not evaluating at the scheduled occurrence; the latest due occurrence will run as catch-up", "", t.UTC()); err != nil {
					failedOccurrenceLedger[due.job] = true
				}
			}
			for _, due := range toFire {
				occurrenceCtx := *due.job.sctx
				occurrenceCtx.ScheduledFor = due.scheduledFor
				if failedOccurrenceLedger[due.job] {
					s.logf(&occurrenceCtx, "[SCHEDULER] refusing to advance occurrence scheduled_for=%s because an earlier gap decision could not be recorded", due.scheduledFor.Format(time.RFC3339))
					continue
				}
				if err := s.recordScheduleFireDecision(context.Background(), &occurrenceCtx, "attempted", "scheduler selected this occurrence for execution", "", t.UTC()); err != nil {
					s.logf(&occurrenceCtx, "[SCHEDULER] refusing to launch occurrence scheduled_for=%s because its durable attempt could not be recorded: %v", due.scheduledFor.Format(time.RFC3339), err)
					continue
				}
				s.mu.Lock()
				if due.job.lastFired.Before(due.scheduledFor) {
					due.job.lastFired = due.scheduledFor
				}
				s.mu.Unlock()
				go s.triggerSchedule(due.job.sctx, due.scheduledFor)
			}

			// Suspended runs wake on their own reset time rather than on a
			// cron occurrence, so they are evaluated every tick alongside the
			// schedules (PLAT-101).
			go s.resumeDueCapacityWaits(context.Background(), t.UTC())
			go s.resumeQueuedScheduleOccurrences(context.Background(), t.UTC())
			// The workflow's own Pulse schedule: the time each Pulse chose for
			// itself, bounded by the interval guards, or earlier for a durable
			// fast request (pulse_schedule.go).
			// Then fix runs: a short Technical Review+Fix pass for a workflow
			// with something to fix, instead of waiting for the full Pulse.
			go func() {
				// One launcher pass at a time: overlapping ticks could each
				// see room under maxConcurrentPulseRuns and both start a run.
				if !pulseLauncherMu.TryLock() {
					return
				}
				defer pulseLauncherMu.Unlock()
				s.launchDuePulses(context.Background())
				s.launchDueFixRuns(context.Background())
				// Then the daily goal check (PLAT-697).
				s.launchDueGoalChecks(context.Background())
				// QA runs the Pulse asked for (PLAT-697 phase 4).
				s.launchGoalLeadQARequests(context.Background())
				// Workflow Review after a plan change, so most runs find it
				// already done (PLAT-697 phase 0). Not a Pulse run.
				s.launchDueWorkflowReviews(context.Background())
			}()
			lastTick = t
		}
	}
}

const maxCronOccurrenceScans = int(workflowScheduleHistoryRetention/time.Minute) + 1

// dueCronOccurrences returns the retained occurrence window after a durable
// cursor. Cron expressions have one-minute resolution, so a seven-day scan is
// bounded to at most 10,080 real occurrences. Every occurrence in that window
// is returned and durably classified; only the newest one executes as catch-up.
func dueCronOccurrences(schedule cron.Schedule, after, now time.Time) []time.Time {
	if schedule == nil || !now.After(after) {
		return nil
	}
	if floor := now.Add(-workflowScheduleHistoryRetention); after.Before(floor) {
		after = floor
	}
	occurrences := make([]time.Time, 0, 64)
	cursor := after
	for scans := 0; scans < maxCronOccurrenceScans; scans++ {
		next := schedule.Next(cursor)
		if next.After(now) || !next.After(cursor) {
			break
		}
		occurrences = append(occurrences, next.UTC())
		cursor = next
	}
	return occurrences
}

// discoveredWorkflow holds a manifest + its workspace path.
type discoveredWorkflow struct {
	WorkspacePath string
	Manifest      *WorkflowManifest
}

// discoverWorkflows scans workspace-docs/Workflow/ for workflow.json files.
func (s *SchedulerService) discoverWorkflows(ctx context.Context) []discoveredWorkflow {
	var results []discoveredWorkflow

	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		scheduleLogf("[SCHEDULER] Cannot scan workflow directory: %v", err)
		return nil
	}

	for _, item := range discovered {
		if len(item.Manifest.Schedules) > 0 {
			results = append(results, discoveredWorkflow{
				WorkspacePath: item.WorkspacePath,
				Manifest:      item.Manifest,
			})
		}
	}

	return results
}

// buildScheduleContext creates a ScheduleContext from a manifest and schedule.
func buildScheduleContext(workspacePath string, manifest *WorkflowManifest, sched WorkflowSchedule) *ScheduleContext {
	sctx := &ScheduleContext{
		WorkspacePath: workspacePath,
		WorkflowID:    manifest.ID,
		WorkflowLabel: manifest.Label,
		WorkflowKind:  manifest.Kind,
		RelayRuntime:  manifest.RelayRuntime,
		Schedule:      sched,
		Capabilities:  lockedScheduleCapabilities(manifest.Capabilities),
		OwnerUserID:   workflowExecutionOwnerUserID(manifest),
	}
	if manifest.Kind == "relay" {
		if groups := normalizeScheduleGroupNames(sched.GroupNames); len(groups) == 1 {
			sctx.RelayLegacyGroupName = groups[0]
		}
		sctx.Schedule.GroupNames = nil
		sctx.Capabilities.SlackConnectionID = ""
		notifications := WorkflowNotificationConfig{}
		if sctx.Capabilities.Notifications != nil {
			notifications = *sctx.Capabilities.Notifications
		}
		notifications.ExcludeChannels = append(append([]string(nil), notifications.ExcludeChannels...), "slack", "whatsapp")
		notifications.SlackWebhookSecretName = ""
		notifications.RunSummarySlackWebhookSecretNames = nil
		notifications.PulseSummarySlackWebhookSecretNames = nil
		sctx.Capabilities.Notifications = &notifications
	}
	if sched.PulseReviewOnly {
		// PLAT-115: a workflow's own periodic Pulse-review schedule reuses the
		// exact plumbing the manual "Run Pulse now" trigger already exercises
		// (TriggerPulseNow) — skip workflow execution, run the full Gate/
		// Review+Fix/Finalize chain regardless of ordinary run finalization. Unlike
		// TriggerPulseNow this never sets PulseEvidenceRunFolder: that field
		// means "review exactly this one folder," but a periodic pass reviews
		// whatever runs/iteration-N/ backlog exists, decided by Gate's own
		// reasoning (see the dedicated-review Gate branch).
		sctx.PulseOnly = true
		sctx.ForcePulseReview = true
	}
	return sctx
}

// workflowExecutionOwnerUserID converts durable workflow ownership into the
// authenticated identity used by unattended schedule and trigger turns.
// Before action tools required bound session claims, ownerless legacy runs
// could pass an empty string and silently fall through to the local default
// user. Keep that compatibility deliberately in single-user mode, where that
// user is the installation owner, but never invent an owner for a multi-user
// workflow.
//
// The creator runs it while they are still an owner with an active account; otherwise the first active owner does.
// A workflow handed to a new owner (creator removed from its owners, or their account disabled) kept running as the
// creator, a non-owner, and broke once that account was disabled (Dominion trading workflow, 2026-10-07).
func workflowExecutionOwnerUserID(manifest *WorkflowManifest) string {
	if manifest == nil {
		return ""
	}
	owners := manifest.effectiveOwners()
	createdBy := strings.TrimSpace(manifest.CreatedBy)
	if createdBy != "" && containsID(owners, createdBy) && workflowExecutionAccountActive(createdBy) {
		return createdBy
	}
	for _, owner := range owners {
		if owner = strings.TrimSpace(owner); owner != "" && workflowExecutionAccountActive(owner) {
			return owner
		}
	}
	if len(owners) > 0 {
		return strings.TrimSpace(owners[0]) // nobody active: fail closed on a real owner rather than invent one
	}
	if !IsMultiUserMode() {
		return GetDefaultUserID()
	}
	return ""
}

// workflowExecutionAccountActive reports whether an account can run unattended turns: known and enabled where there is
// a user directory; any account in single-user mode.
func workflowExecutionAccountActive(userID string) bool {
	acc := userAccessForClaims(&UserClaims{UserID: userID})
	return !acc.Disabled && (acc.Known || !IsMultiUserMode())
}

func shouldRunPulseLifecycle(sctx *ScheduleContext, manifest *WorkflowManifest) bool {
	if manifest == nil {
		return false
	}
	return effectiveSchedulePulseMode(sctx, manifest) != schedulePulseModeOff
}

func effectiveSchedulePulseMode(sctx *ScheduleContext, manifest *WorkflowManifest) string {
	if sctx != nil && (sctx.WebhookInput != nil || sctx.Schedule.ScheduleType == "webhook") {
		return schedulePulseModeOff
	}
	if sctx != nil && sctx.ForcePulseReview {
		return schedulePulseModeFull
	}
	if sctx == nil || manifest == nil {
		return schedulePulseModeOff
	}
	// A blocking migration may have changed the policy after this run captured
	// its schedule. Use the freshly read manifest for post-run stewardship.
	for _, schedule := range manifest.Schedules {
		if schedule.ID != "" && schedule.ID == sctx.Schedule.ID {
			return manifest.EffectivePulseMode(schedule)
		}
	}
	return manifest.EffectivePulseMode(sctx.Schedule)
}

// Stop shuts down the scheduler.
func (s *SchedulerService) Stop() {
	s.runCancelsMu.Lock()
	runCancels := s.runCancels
	s.runCancels = make(map[string]context.CancelFunc)
	s.runCancelsMu.Unlock()
	for _, cancel := range runCancels {
		cancel()
	}
	s.mu.Lock()
	s.jobs = make(map[string]*registeredJob)
	s.scheduleFingerprints = make(map[string]string)
	s.mu.Unlock()
	s.stateStoreMu.Lock()
	if s.stateStore != nil {
		if err := s.stateStore.Close(); err != nil {
			scheduleLogf("[SCHEDULER] Failed to close schedule state store: %v", err)
		}
		s.stateStore = nil
	}
	s.stateStoreMu.Unlock()
	scheduleLogf("[SCHEDULER] Stopped")
}

// LoadSchedule registers a schedule for wall-clock evaluation from a ScheduleContext.
func (s *SchedulerService) LoadSchedule(sctx *ScheduleContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sched := sctx.Schedule
	runtimeKey := scheduleRuntimeKey(sctx)
	if s.scheduleFingerprints == nil {
		s.scheduleFingerprints = make(map[string]string)
	}

	// Remove existing registration if any.
	delete(s.jobs, runtimeKey)
	calendarPrefix := runtimeKey + "__cal__"
	for key := range s.jobs {
		if strings.HasPrefix(key, calendarPrefix) {
			delete(s.jobs, key)
		}
	}
	// Relays retain the shared function-delivery ledger, but register no timers.
	if sctx.WorkflowKind == "relay" && !sched.IsFunctionTrigger() {
		s.scheduleFingerprints[runtimeKey] = scheduleConfigFingerprint(sctx)
		return nil
	}
	// Legacy manifests may still contain a dedicated Pulse cron. It now serves
	// only as a backwards-compatible enablement signal; recurring Pulse is
	// triggered by completion of a normal scheduled run instead.
	if sched.PulseReviewOnly {
		s.scheduleFingerprints[runtimeKey] = scheduleConfigFingerprint(sctx)
		return nil
	}

	// Update workspace index
	s.workspaceIndexMu.Lock()
	s.workspaceIndex[runtimeKey] = sctx.WorkspacePath
	s.workspaceIndexMu.Unlock()

	if err := EnsureWorkflowScheduleExecutionTracker(context.Background(), sctx.WorkspacePath, sched, time.Now().UTC()); err != nil {
		s.logf(sctx, "[SCHEDULER] Warning: failed to initialize execution history for %s: %v", sched.ID, err)
	}

	if !sched.Enabled {
		s.scheduleFingerprints[runtimeKey] = scheduleConfigFingerprint(sctx)
		return nil
	}

	scheduleType := scheduleTypeOrDefault(sched.ScheduleType)
	if scheduleType == "webhook" {
		s.scheduleFingerprints[runtimeKey] = scheduleConfigFingerprint(sctx)
		s.updateRuntimeState(runtimeKey, func(state *ScheduleRuntimeState) { state.NextRunAt = nil })
		return nil
	}
	var nextRun *time.Time
	sctxCopy := *sctx

	if scheduleType == "calendar" {
		// Calendar schedules: register one job per future calendar item.
		for _, item := range sched.CalendarItems {
			runAt, err := calendarItemRunTime(sched, item)
			if err != nil || !runAt.After(time.Now().UTC()) {
				continue
			}
			if nextRun == nil || runAt.Before(*nextRun) {
				runAtCopy := runAt
				nextRun = &runAtCopy
			}
			itemCopy := item
			itemSctx := sctxCopy
			itemSctx.Schedule = scheduleWithCalendarItem(sched, itemCopy)
			itemSctx.CalendarItem = &itemCopy
			calID := fmt.Sprintf("%s__cal__%s_%s", runtimeKey, item.Date, item.Time)
			s.jobs[calID] = &registeredJob{
				sctx:  &itemSctx,
				runAt: &runAt,
			}
		}
		if nextRun == nil {
			s.logf(sctx, "[SCHEDULER] Calendar schedule %s (%s) has no future items; not registering", sched.ID, sched.Name)
		}
	} else {
		// Cron schedules: parse the expression and register for wall-clock eval.
		loc, err := time.LoadLocation(scheduleTimezoneOrDefault(sched.Timezone))
		if err != nil {
			loc = time.UTC
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		cronSched, err := parser.Parse(sched.CronExpression)
		if err != nil {
			return fmt.Errorf("failed to parse cron expression %q: %w", sched.CronExpression, err)
		}
		// Wrap with timezone-aware location
		cronSched = &tzSchedule{inner: cronSched, loc: loc}
		now := time.Now().UTC()
		lastFired := now.Add(-30 * time.Second) // a genuinely new schedule starts with its next future occurrence
		if latest, ok := s.latestCronOccurrence(sctx); ok {
			lastFired = latest
		} else {
			// The scheduler-state DB can legitimately be empty after an upgrade,
			// replacement, or first deployment of durable fire decisions. Resume
			// from the schedule's persisted tracking window instead of treating an
			// old schedule as newly created and silently skipping every occurrence
			// before this process started.
			if windowStart, ok := WorkflowScheduleTrackingWindowStart(context.Background(), sctx.WorkspacePath, sched.ID); ok && windowStart.Before(now) {
				lastFired = windowStart
			}
		}

		s.jobs[runtimeKey] = &registeredJob{
			sctx:      &sctxCopy,
			cronSched: cronSched,
			lastFired: lastFired,
		}
		nextRun = getNextRunTime(sched.CronExpression, sched.Timezone)
	}

	// Initialize runtime state with next run.
	s.updateRuntimeState(runtimeKey, func(state *ScheduleRuntimeState) {
		state.NextRunAt = nextRun
	})
	s.scheduleFingerprints[runtimeKey] = scheduleConfigFingerprint(sctx)

	nextRunStr := "unknown"
	if nextRun != nil {
		nextRunStr = nextRun.Format(time.RFC3339)
	}
	s.logf(sctx, "[SCHEDULER] Registered schedule %s (%s) type=%s cron=%q timezone=%s next_run=%s",
		sched.ID, sched.Name, scheduleType, sched.CronExpression, sched.Timezone, nextRunStr)
	return nil
}

func (s *SchedulerService) latestCronOccurrence(sctx *ScheduleContext) (time.Time, bool) {
	if sctx == nil {
		return time.Time{}, false
	}
	s.stateStoreMu.RLock()
	defer s.stateStoreMu.RUnlock()
	if s.stateStore == nil {
		return time.Time{}, false
	}
	scopeType, scopeID, _ := scheduleStateScope(sctx)
	decision, err := s.stateStore.LatestFireDecision(context.Background(), scopeType, scopeID, sctx.Schedule.ID, "cron")
	if err != nil || decision.ScheduledFor.IsZero() {
		return time.Time{}, false
	}
	if decision.Decision == "attempted" {
		decision.DecisionID = uuid.NewString()
		decision.Decision = "launch_outcome_unknown"
		decision.Reason = "the occurrence was durably attempted, but no later launch outcome was recorded before restart; execution may or may not have started"
		decision.FiredAt = time.Now().UTC()
		if err := s.stateStore.RecordFireDecision(context.Background(), decision); err != nil {
			s.logf(sctx, "[SCHEDULER_STATE] failed to reconcile interrupted attempted occurrence scheduled_for=%s: %v", decision.ScheduledFor.Format(time.RFC3339), err)
		}
	}
	return decision.ScheduledFor.UTC(), true
}

// tzSchedule wraps a cron.Schedule to evaluate in a specific timezone.
type tzSchedule struct {
	inner cron.Schedule
	loc   *time.Location
}

func (tz *tzSchedule) Next(t time.Time) time.Time {
	return tz.inner.Next(t.In(tz.loc))
}

// ReloadSchedule reloads a schedule from its manifest after it's been updated.
func (s *SchedulerService) ReloadSchedule(ctx context.Context, workspacePath string, scheduleID string) error {
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found {
		return fmt.Errorf("failed to read manifest from %s: %w", workspacePath, err)
	}

	// Find the schedule
	for _, sched := range manifest.Schedules {
		if sched.ID == scheduleID {
			return s.LoadSchedule(buildScheduleContext(workspacePath, manifest, sched))
		}
	}

	// Schedule not found — remove it
	return s.removeJobByKey(workflowScheduleRuntimeKey(workspacePath, scheduleID))
}

func (s *SchedulerService) removeJobByKey(key string) error {
	s.mu.Lock()
	delete(s.jobs, key)
	delete(s.scheduleFingerprints, key)
	// Also remove calendar sub-jobs.
	prefix := key + "__cal__"
	for k := range s.jobs {
		if strings.HasPrefix(k, prefix) {
			delete(s.jobs, k)
		}
	}
	s.mu.Unlock()

	s.workspaceIndexMu.Lock()
	delete(s.workspaceIndex, key)
	s.workspaceIndexMu.Unlock()

	s.runtimeStatesMu.Lock()
	if state := s.runtimeStates[key]; state == nil || state.ActiveRunID == "" {
		delete(s.runtimeStates, key)
		noteScheduleSummaryChange()
	}
	s.runtimeStatesMu.Unlock()

	return nil
}

// RemoveJob removes a schedule only when its ID resolves to one loaded scope.
// Scoped callers should use ReloadSchedule or RemoveWorkflowJob so a copied
// schedule cannot remove another workflow's schedule with the same ID.
func (s *SchedulerService) RemoveJob(scheduleID string) error {
	keys := s.loadedScheduleKeys(scheduleID)
	if len(keys) == 0 {
		return nil
	}
	if len(keys) > 1 {
		return fmt.Errorf("schedule ID %q is ambiguous across %d scopes", scheduleID, len(keys))
	}
	return s.removeJobByKey(keys[0])
}

func (s *SchedulerService) RemoveWorkflowJob(workspacePath, scheduleID string) error {
	return s.removeJobByKey(workflowScheduleRuntimeKey(workspacePath, scheduleID))
}

func (s *SchedulerService) loadedScheduleKeys(scheduleID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, 1)
	for key, job := range s.jobs {
		if strings.Contains(key, "__cal__") || job == nil || job.sctx == nil || job.sctx.Schedule.ID != scheduleID {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// GetRuntimeState is the legacy unscoped lookup. It returns state only when the
// schedule ID resolves to one loaded scope; scoped callers must use the methods
// below so copied workflows and per-user built-ins cannot collide.
func (s *SchedulerService) GetRuntimeState(scheduleID string) ScheduleRuntimeState {
	keys := s.runtimeKeysForScheduleID(scheduleID)
	if len(keys) == 1 {
		return s.getRuntimeStateByKey(keys[0])
	}
	// Preserve tests and pre-migration in-memory state that used a bare key.
	return s.getRuntimeStateByKey(scheduleID)
}

func (s *SchedulerService) GetRuntimeStateForWorkflow(workspacePath, scheduleID string) ScheduleRuntimeState {
	key := workflowScheduleRuntimeKey(workspacePath, scheduleID)
	merged := s.getRuntimeStateByKey(key)
	s.runCancelsMu.Lock()
	ownsActiveRun := merged.ActiveRunID != "" && s.runCancels[merged.ActiveRunID] != nil
	s.runCancelsMu.Unlock()
	_ = s.reconcileWorkflowScheduleRuns(context.Background(), workspacePath, scheduleID)
	runs, err := ReadScheduleRuns(context.Background(), workspacePath)
	if err == nil {
		durableRunID := ""
		// The durable scheduler receipt includes Pulse's terminal outcome;
		// workflow-only file history must not erase a partial/stopped result.
		if !ownsActiveRun {
			latest := -1
			for i := range runs {
				if runs[i].ScheduleID == scheduleID && (latest < 0 || runs[i].StartedAt.After(runs[latest].StartedAt)) {
					latest = i
				}
			}
			if latest >= 0 {
				if status, message, _, ok := s.durableScheduleRunProjection(context.Background(), runs[latest].ID); ok {
					runs[latest].Status, runs[latest].Error = status, message
					durableRunID = runs[latest].ID
				}
			}
		}
		merged = mergeRuntimeStateWithRuns(merged, scheduleID, runs, ownsActiveRun, durableRunID)
	}
	merged.NextRunAt = s.nextWorkflowScheduleRunAt(workspacePath, scheduleID, time.Now())
	s.stateStoreMu.RLock()
	if s.stateStore != nil {
		if pending, pendingErr := s.stateStore.GetPendingOccurrence(context.Background(), "workflow", workspacePath, scheduleID); pendingErr == nil {
			queuedAt, expiresAt := pending.QueuedAt, pending.ExpiresAt
			merged.LastStatus = "waiting_for_workflow"
			merged.LastError = pending.Reason
			merged.WaitingSince = &queuedAt
			merged.WaitingUntil = &expiresAt
			merged.WaitingReason = pending.Reason
			merged.QueuedOccurrences = pending.OccurrenceCount
		}
	}
	s.stateStoreMu.RUnlock()
	return merged
}

func (s *SchedulerService) getRuntimeStateByKey(key string) ScheduleRuntimeState {
	s.runtimeStatesMu.RLock()
	var merged ScheduleRuntimeState
	if state, ok := s.runtimeStates[key]; ok {
		merged = cloneScheduleRuntimeState(state)
	}
	s.runtimeStatesMu.RUnlock()
	return merged
}

func cloneScheduleRuntimeState(state *ScheduleRuntimeState) ScheduleRuntimeState {
	if state == nil {
		return ScheduleRuntimeState{}
	}
	copy := *state
	if state.LastRunAt != nil {
		value := *state.LastRunAt
		copy.LastRunAt = &value
	}
	if state.NextRunAt != nil {
		value := *state.NextRunAt
		copy.NextRunAt = &value
	}
	if state.LastDurationMs != nil {
		value := *state.LastDurationMs
		copy.LastDurationMs = &value
	}
	if state.WaitingSince != nil {
		value := *state.WaitingSince
		copy.WaitingSince = &value
	}
	if state.WaitingUntil != nil {
		value := *state.WaitingUntil
		copy.WaitingUntil = &value
	}
	return copy
}

func (s *SchedulerService) runtimeKeysForScheduleID(scheduleID string) []string {
	s.runtimeStatesMu.RLock()
	defer s.runtimeStatesMu.RUnlock()
	keys := make([]string, 0, 1)
	for key := range s.runtimeStates {
		if scheduleRuntimeKeyHasID(key, scheduleID) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func (s *SchedulerService) reconcileWorkflowScheduleRuns(ctx context.Context, workspacePath, scheduleID string) error {
	if s == nil || s.api == nil || strings.TrimSpace(workspacePath) == "" {
		return nil
	}

	runs, err := ReadScheduleRuns(ctx, workspacePath)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	changed := false
	for i := range runs {
		if runs[i].Status != "running" {
			continue
		}
		if scheduleID != "" && runs[i].ScheduleID != scheduleID {
			continue
		}

		status, errMsg, shouldFinalize := s.reconciledScheduleRunStatus(&runs[i], now)
		if !shouldFinalize {
			continue
		}

		runs[i].Status = status
		runs[i].Error = errMsg
		durationMs := now.Sub(runs[i].StartedAt).Milliseconds()
		if durationMs < 0 {
			durationMs = 0
		}
		runs[i].DurationMs = &durationMs
		runs[i].CompletedAt = &now
		changed = true
	}

	if !changed {
		return nil
	}
	return WriteScheduleRuns(ctx, workspacePath, runs)
}

func (s *SchedulerService) reconciledScheduleRunStatus(run *ScheduleRunEntry, now time.Time) (string, string, bool) {
	if run == nil {
		return "", "", false
	}

	if strings.TrimSpace(run.SessionID) == "" {
		if now.Sub(run.StartedAt) > 10*time.Minute {
			return "error", "interrupted: no session id recorded", true
		}
		return "", "", false
	}

	session, exists := s.api.getActiveSession(run.SessionID)
	if !exists {
		return "error", "interrupted: session not active", true
	}

	switch session.Status {
	case "running":
		return "", "", false
	case "completed":
		// A scheduled run is many sequential turns sharing one session, and
		// between them the session reads "completed" — the previous turn is
		// done, the next has not started. Finalizing here called the whole run
		// a success while it was on turn 1 of 10.
		//
		// rtslatency's 2026-07-31 02:30 run was recorded success/84,599 ms with
		// completed_at 84 seconds after start, while the log shows turns 2-5
		// running for another hour and turn 6 still going when the process shut
		// down. Once stamped, the record never corrected itself, so every run
		// killed by a restart also read as successful and the workflow looked
		// healthy while its digest had not shipped since 2026-07-29.
		//
		// This reconciler exists to clean up runs the scheduler abandoned, and
		// it cannot tell "between turns" from "finished". Success is the
		// scheduler's verdict to record when its own turn loop ends. Past the
		// grace window an abandoned run is reported interrupted, never success.
		if now.Sub(run.StartedAt) <= scheduleRunAbandonedAfter {
			return "", "", false
		}
		return "error", "interrupted: scheduler never recorded a terminal result", true
	case "stopped", "dismissed":
		return "stopped", fmt.Sprintf("session ended with status %s", session.Status), true
	case "error", "failed", "inactive":
		return "error", fmt.Sprintf("session ended with status %s", session.Status), true
	default:
		return "", "", false
	}
}

// scheduleRunAbandonedAfter bounds how long a run may sit between turns before
// the reconciler treats it as abandoned. It must exceed a real run: rtslatency's
// healthy runs take 45 minutes to 2.5 hours, so a shorter window would finalize
// live work as interrupted.
var scheduleRunAbandonedAfter = 6 * time.Hour

func mergeRuntimeStateWithRuns(state ScheduleRuntimeState, scheduleID string, runs []ScheduleRunEntry, ownsActiveRun bool, durableRunID string) ScheduleRuntimeState {
	var filtered []ScheduleRunEntry
	for _, run := range runs {
		if run.ScheduleID == scheduleID {
			filtered = append(filtered, run)
		}
	}
	if len(filtered) == 0 {
		return state
	}

	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].StartedAt.After(filtered[j].StartedAt) })
	latest := filtered[0]
	if state.RunCount < len(filtered) {
		state.RunCount = len(filtered)
	}
	// File history records the workflow result before Pulse finishes. Preserve
	// the live in-memory state for that same run so the UI cannot report success
	// and admit another trigger while Pulse still owns the session.
	if state.LastStatus == "running" && ownsActiveRun {
		return state
	}

	shouldAdoptLatest := state.LastRunAt == nil || latest.StartedAt.After(*state.LastRunAt)
	sameRun := state.LastRunAt != nil && latest.StartedAt.Equal(*state.LastRunAt)
	// Session identity alone is insufficient: resumed sessions span many runs.
	if state.ActiveRunID != "" && latest.ID == state.ActiveRunID {
		sameRun = true
	}
	staleRunning := state.LastStatus == "running" && !ownsActiveRun && sameRun && latest.Status != "running"

	durableSameRun := !ownsActiveRun && sameRun && durableRunID != "" && latest.ID == durableRunID
	if shouldAdoptLatest || staleRunning || durableSameRun {
		state.ActiveRunID = ""
		startedAt := latest.StartedAt
		state.LastRunAt = &startedAt
		state.LastStatus = latest.Status
		state.LastSessionID = latest.SessionID
		state.LastError = latest.Error
		state.LastDurationMs = latest.DurationMs
		return state
	}

	if sameRun {
		if state.LastStatus == "" {
			state.LastStatus = latest.Status
		}
		if state.LastSessionID == "" {
			state.LastSessionID = latest.SessionID
		}
		if state.LastError == "" && state.LastStatus == latest.Status {
			state.LastError = latest.Error
		}
		if state.LastDurationMs == nil {
			state.LastDurationMs = latest.DurationMs
		}
	}

	return state
}

// GetWorkspaceForSchedule returns the workspace path for a schedule ID.
func (s *SchedulerService) GetWorkspaceForSchedule(scheduleID string) string {
	s.workspaceIndexMu.RLock()
	defer s.workspaceIndexMu.RUnlock()
	match := ""
	for key, workspacePath := range s.workspaceIndex {
		if !scheduleRuntimeKeyHasID(key, scheduleID) {
			continue
		}
		if match != "" && match != workspacePath {
			return ""
		}
		match = workspacePath
	}
	return match
}

// ListFireDecisions returns the durable tick-loop decision log for one
// schedule — including occurrences the scheduler correctly skipped
// (global pause, busy, a queued dependency) and never turned into a
// schedule_runs row at all. get_schedule_runs's own history only shows
// actual runs, so a schedule that looks silent for days there can be a
// scheduler correctly honoring a pause the whole time, not a defect —
// this is the only read path that can tell the two apart (found live on
// confida-login: four Technical Review passes theorized a missing
// misfire-recovery mechanism instead of reading this durable, already-logged
// decision trail).
func (s *SchedulerService) ListFireDecisions(ctx context.Context, workspacePath, scheduleID string, limit int) ([]schedulerstate.FireDecision, error) {
	s.stateStoreMu.RLock()
	store := s.stateStore
	s.stateStoreMu.RUnlock()
	if store == nil {
		return nil, errors.New("schedule state store is unavailable")
	}
	scopeID := filepath.Clean(strings.TrimSpace(workspacePath))
	return store.ListFireDecisions(ctx, "workflow", scopeID, scheduleID, limit)
}

// TriggerNow triggers a schedule immediately (for manual trigger API).
func (s *SchedulerService) TriggerNow(workspacePath string, scheduleID string) (string, error) {
	return s.TriggerNowFromSession(workspacePath, scheduleID, "")
}

// TriggerNowFromSession is TriggerNow with the chat session that asked for the
// run, so the run's terminals can be surfaced in that chat.
func (s *SchedulerService) TriggerNowFromSession(workspacePath, scheduleID, originSessionID string) (string, error) {
	return s.triggerSavedSchedule(workspacePath, scheduleID, originSessionID, nil)
}

// webhookDeliveryStartError re-checks a delivery's group and variables
// against the trigger as saved now. A function trigger's variables are its
// declared inputs; any other trigger's are its allowed_variables.
func webhookDeliveryStartError(sched WorkflowSchedule, input *WorkflowWebhookDelivery) error {
	if input.Group != "" && !slices.Contains(sched.GroupNames, input.Group) {
		return errors.New("group is no longer allowed by this trigger")
	}
	if sched.Webhook == nil && !sched.IsFunctionTrigger() {
		return nil
	}
	var allowed []string
	if sched.IsFunctionTrigger() {
		allowed = workflowFunctionInputNames(sched.Function)
	} else {
		allowed = sched.Webhook.AllowedVariables
	}
	for name := range input.Variables {
		if !slices.Contains(allowed, name) {
			return fmt.Errorf("variable %q is no longer allowed by this trigger", name)
		}
	}
	return nil
}

func (s *SchedulerService) triggerSavedSchedule(workspacePath, scheduleID, originSessionID string, input *WorkflowWebhookDelivery) (string, error) {
	ctx := context.Background()

	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found {
		return "", fmt.Errorf("failed to read manifest from %s: %w", workspacePath, err)
	}

	var sched *WorkflowSchedule
	for i := range manifest.Schedules {
		if manifest.Schedules[i].ID == scheduleID {
			sched = &manifest.Schedules[i]
			break
		}
	}
	if sched == nil {
		return "", fmt.Errorf("schedule %s not found in manifest at %s", scheduleID, workspacePath)
	}
	if input != nil && (input.gmailRuleID != "" || sched.IsGmailTrigger() && sched.Gmail != nil && len(sched.Gmail.Rules) > 0) {
		selected, err := gmailRuleSchedule(*sched, input.gmailRuleID)
		if err != nil {
			return "", err
		}
		sched = &selected
	}
	sctx := buildScheduleContext(workspacePath, manifest, *sched)
	sctx.TriggerSource = "manual"
	if sched.ScheduleType == "webhook" {
		if input == nil {
			return "", errors.New("webhook triggers require an authenticated delivery")
		}
		if !sched.Enabled {
			return "", errors.New("webhook is disabled")
		}
		if err := validateWebhookSchedule(*sched); err != nil {
			return "", err
		}
		sctx.TriggerSource = "webhook"
		sctx.WebhookInput = input
		if err := webhookDeliveryStartError(*sched, input); err != nil {
			return "", err
		}
		if input.Group != "" && manifest.Kind != "relay" {
			sctx.Schedule.GroupNames = []string{input.Group}
		}
		resolvedStepID, resolvedRoutes := resolvedWebhookExecutionTarget(*sched, input)
		sctx.Schedule.RouteSelections = resolvedRoutes
		if sched.Webhook != nil {
			webhookConfig := *sched.Webhook
			webhookConfig.StepID = resolvedStepID
			sctx.Schedule.Webhook = &webhookConfig
		}
		if err := validateWebhookTarget(ctx, workspacePath, resolvedStepID, resolvedRoutes); err != nil {
			return "", err
		}
		if err := validateWebhookVariableNames(ctx, workspacePath, keysWebhookVariables(input.Variables)); err != nil {
			return "", err
		}
	} else if input != nil {
		return "", errors.New("target is not a webhook")
	}
	sctx.OriginSessionID = originSessionID
	startTime := time.Now().UTC()
	sctx.ScheduledFor = startTime
	if disposition, reason := s.scheduleDependencyDisposition(ctx, sctx, startTime); disposition != scheduleDependencyReady {
		if disposition == scheduleDependencyWaiting && s.queueScheduleOccurrence(ctx, sctx, reason, startTime, "retry") {
			return "queued", nil
		}
		_ = s.recordScheduleFireDecision(ctx, sctx, "blocked_dependency", reason, "", startTime)
		return "", errors.New(reason)
	}

	// A workflow may have one interactive builder chat and one schedule at the
	// same time. Other workflow executions still block a schedule start.
	if activeExec := s.findActiveExecutionBlockingSchedule(workspacePath, scheduleAllowsParallel(sctx.Schedule)); input == nil && activeExec != nil {
		triggeredBy := activeExec.TriggeredBy
		if strings.TrimSpace(triggeredBy) == "" {
			triggeredBy = "unknown"
		}
		err := fmt.Errorf(
			"workflow already has an active %s run (session: %s)",
			triggeredBy,
			activeExec.SessionID,
		)
		if s.recordOrQueueBlockedSchedule(ctx, sctx, err.Error(), startTime) {
			return "queued", nil
		}
		return "", err
	}

	// Reserve the in-memory run and cancellation handle atomically, then claim
	// the durable lease without holding the global runtime-state mutex.
	runtimeKey := scheduleRuntimeKey(sctx)
	workflowRuntimeKeys := make([]string, 0, len(manifest.Schedules))
	for i := range manifest.Schedules {
		if input == nil && manifest.Schedules[i].ScheduleType != "webhook" && !scheduleAllowsParallel(manifest.Schedules[i]) {
			workflowRuntimeKeys = append(workflowRuntimeKeys, workflowScheduleRuntimeKey(workspacePath, manifest.Schedules[i].ID))
		}
	}
	runID := uuid.NewString()
	if input != nil {
		runID = input.RunID
	}
	s.runtimeStatesMu.Lock()
	if input != nil {
		activeDeliveries := activeWebhookDeliveryCount(s.runtimeStates, workspacePath, scheduleID)
		if activeDeliveries >= maxWebhookConcurrency {
			s.runtimeStatesMu.Unlock()
			return "", fmt.Errorf("%w (%d active deliveries)", ErrWebhookConcurrencyLimit, activeDeliveries)
		}
	}
	state := s.getRuntimeStateLocked(runtimeKey)
	if state.LastStatus == "running" {
		s.runtimeStatesMu.Unlock()
		if s.recordOrQueueBlockedSchedule(ctx, sctx, "schedule is already running", startTime) {
			return "queued", nil
		}
		return "", fmt.Errorf("job is already running (session: %s)", state.LastSessionID)
	}
	if otherKey, otherSession := runningScheduleInSetLocked(s.runtimeStates, workflowRuntimeKeys, runtimeKey); !scheduleAllowsParallel(sctx.Schedule) && otherKey != "" {
		s.runtimeStatesMu.Unlock()
		if s.recordOrQueueBlockedSchedule(ctx, sctx, "another schedule owns the workflow", startTime) {
			return "queued", nil
		}
		return "", fmt.Errorf("another schedule is already running (session: %s)", otherSession)
	}
	previousState := *state
	runCtx := s.activateScheduleRunLocked(state, runID, startTime)
	s.runtimeStatesMu.Unlock()
	if err := s.claimScheduleRun(ctx, sctx, runID, startTime); err != nil {
		s.rollbackScheduleRunActivation(runtimeKey, runID, previousState)
		if s.recordOrQueueBlockedSchedule(ctx, sctx, err.Error(), startTime) {
			return "queued", nil
		}
		return "", err
	}
	if s.abortCanceledScheduleRunBeforeStart(runCtx, sctx, runtimeKey, runID) {
		return "", context.Canceled
	}
	if input != nil {
		data, marshalErr := json.Marshal(input)
		if marshalErr == nil {
			marshalErr = writeFileToWorkspace(ctx, webhookInputPath(workspacePath, runID), string(data))
		}
		if marshalErr != nil {
			s.transitionScheduleRun(ctx, sctx, schedulerstate.Transition{RunID: runID, To: schedulerstate.StateFailed, Reason: "webhook input persistence failed", ErrorMessage: marshalErr.Error(), At: time.Now().UTC()})
			s.rollbackScheduleRunActivation(runtimeKey, runID, previousState)
			return "", fmt.Errorf("persist webhook input: %w", marshalErr)
		}
	}
	s.recordScheduleFireDecision(ctx, sctx, "started", sctx.TriggerSource+" trigger accepted", runID, startTime)

	if err := RecordWorkflowScheduleExecution(context.Background(), workspacePath, *sched, startTime); err != nil {
		s.logf(sctx, "[SCHEDULER] Warning: failed to record manual schedule execution for %s: %v", scheduleID, err)
	}

	go func() {
		if _, err := s.runJob(runCtx, sctx, runID); err != nil {
			scheduleLogf("[SCHEDULER] Triggered job %s failed: %v", scheduleID, err)
		}
	}()

	if input != nil {
		return runID, nil
	}
	return "triggered", nil
}

// TriggerPulseNow runs version-preflight -> Pulse against the latest
// retained workflow evidence. It does not execute the workflow or change a saved
// schedule.
func (s *SchedulerService) TriggerPulseNow(workspacePath string) (string, error) {
	return s.triggerPulseOnly(workspacePath, "manual")
}

// TriggerScheduledPulse is the workflow's own Pulse schedule firing
// (pulse_schedule.go). It uses exactly the manual Pulse path.
func (s *SchedulerService) TriggerScheduledPulse(workspacePath string) (string, error) {
	return s.triggerPulseOnly(workspacePath, "cron")
}

// TriggerPulseFixRun starts a Pulse fix run for a workflow with something to
// fix. It does not move the full Pulse schedule.
func (s *SchedulerService) TriggerPulseFixRun(workspacePath, reason string) (string, error) {
	return s.triggerPulseRun(workspacePath, "cron", strings.TrimSpace(reason))
}

func (s *SchedulerService) triggerPulseOnly(workspacePath, triggerSource string) (string, error) {
	return s.triggerPulseRun(workspacePath, triggerSource, "")
}

// TriggerGoalCheck starts the daily goal check (PLAT-697). It does not move
// the full Pulse schedule.
func (s *SchedulerService) TriggerGoalCheck(workspacePath string) (string, error) {
	return s.triggerPulseRunKind(workspacePath, "cron", "", true)
}

// triggerPulseRun starts a full Pulse, or a fix run when fixReason is set.
func (s *SchedulerService) triggerPulseRun(workspacePath, triggerSource, fixReason string) (string, error) {
	return s.triggerPulseRunKind(workspacePath, triggerSource, fixReason, false)
}

func (s *SchedulerService) triggerPulseRunKind(workspacePath, triggerSource, fixReason string, goalCheck bool) (string, error) {
	ctx := context.Background()
	fixRun := fixReason != ""
	workspacePath = filepath.Clean(strings.TrimSpace(workspacePath))
	if workspacePath == "." || workspacePath == "" {
		return "", errors.New("workspace_path is required")
	}

	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil {
		return "", fmt.Errorf("failed to read manifest from %s: %w", workspacePath, err)
	}
	if !found {
		return "", fmt.Errorf("workflow manifest not found at %s", workspacePath)
	}

	sched := WorkflowSchedule{
		ID:             manualWorkflowPulseScheduleID,
		Name:           "Run Pulse",
		Description:    "One-off Pulse review of the latest retained run; not persisted as a schedule",
		ScheduleType:   "cron",
		CronExpression: "",
		Timezone:       "UTC",
		Mode:           "workshop",
		WorkshopMode:   "workshop",
	}
	if fixRun {
		sched.ID = pulseFixRunScheduleID
		sched.Name = "Pulse fix run"
		sched.Description = "Technical Review+Fix on current issues; started because " + fixReason
	}
	if goalCheck {
		sched.Name = "Daily goal check"
		sched.Description = "Pulse's daily goal check: is the goal measured, moving, and its work running"
	}
	sctx := buildScheduleContext(workspacePath, manifest, sched)
	sctx.TriggerSource = triggerSource
	sctx.ForcePulseReview = true
	sctx.PulseOnly = true
	sctx.PulseFixRun = fixRun
	sctx.PulseFixReason = fixReason
	sctx.PulseGoalCheck = goalCheck && !fixRun
	sctx.PulseEvidenceRunFolder, sctx.PulseEvidenceRunStatus = latestRetainedPulseEvidence(ctx, workspacePath)
	startTime := time.Now().UTC()

	// One interactive builder chat may coexist with this run, matching normal
	// schedule behavior. Another workflow/schedule execution may not.
	if activeExec := s.findActiveNonBuilderExecutionForWorkspace(workspacePath); activeExec != nil {
		triggeredBy := strings.TrimSpace(activeExec.TriggeredBy)
		if triggeredBy == "" {
			triggeredBy = "unknown"
		}
		return "", fmt.Errorf("workflow already has an active %s run (session: %s)", triggeredBy, activeExec.SessionID)
	}

	runtimeKey := workflowScheduleRuntimeKey(workspacePath, sched.ID)
	workflowRuntimeKeys := make([]string, 0, len(manifest.Schedules)+1)
	for i := range manifest.Schedules {
		workflowRuntimeKeys = append(workflowRuntimeKeys, workflowScheduleRuntimeKey(workspacePath, manifest.Schedules[i].ID))
	}
	// A full Pulse and a fix run have different keys; each must see the other,
	// or both start on the same workflow and work the same issues at once.
	workflowRuntimeKeys = append(workflowRuntimeKeys,
		workflowScheduleRuntimeKey(workspacePath, manualWorkflowPulseScheduleID),
		workflowScheduleRuntimeKey(workspacePath, pulseFixRunScheduleID))
	runID := uuid.NewString()
	s.runtimeStatesMu.Lock()
	state := s.getRuntimeStateLocked(runtimeKey)
	if state.LastStatus == "running" {
		s.runtimeStatesMu.Unlock()
		return "", fmt.Errorf("Pulse run is already active (session: %s)", state.LastSessionID)
	}
	if _, otherSession := runningScheduleInSetLocked(s.runtimeStates, workflowRuntimeKeys, runtimeKey); otherSession != "" {
		s.runtimeStatesMu.Unlock()
		return "", fmt.Errorf("another schedule is already running (session: %s)", otherSession)
	}
	previousState := *state
	runCtx := s.activateScheduleRunLocked(state, runID, startTime)
	s.runtimeStatesMu.Unlock()

	if err := s.claimScheduleRun(ctx, sctx, runID, startTime); err != nil {
		s.rollbackScheduleRunActivation(runtimeKey, runID, previousState)
		return "", err
	}
	if s.abortCanceledScheduleRunBeforeStart(runCtx, sctx, runtimeKey, runID) {
		return "", context.Canceled
	}
	// Scheduled and manual full Pulses both reset the Pulse schedule's guards;
	// the new pass chooses the next time with record_pulse_next_run. A fix run
	// is counted separately and leaves the full Pulse schedule alone.
	if fixRun {
		if err := recordPulseFixRunStarted(ctx, workspacePath, runID, fixReason, startTime); err != nil {
			scheduleLogf("[PULSE] could not record fix run start for %s: %v", workspacePath, err)
		}
	} else if sctx.PulseGoalCheck {
		if err := recordGoalCheckRunStarted(ctx, workspacePath, runID, startTime); err != nil {
			scheduleLogf("[PULSE] could not record goal check start for %s: %v", workspacePath, err)
		}
	} else if err := markPulseStarted(ctx, workspacePath, runID, startTime); err != nil {
		scheduleLogf("[PULSE] could not record Pulse start for %s: %v", workspacePath, err)
	}

	go func() {
		defer s.cleanupRemovedScheduleRuntimeState(runtimeKey)
		if _, runErr := s.runJob(runCtx, sctx, runID); runErr != nil {
			scheduleLogf("[SCHEDULER] Pulse-only run (%s) failed for %s: %v", triggerSource, workspacePath, runErr)
		}
	}()

	return runID, nil
}

func latestRetainedPulseEvidence(ctx context.Context, workspacePath string) (string, string) {
	runs, err := ReadScheduleRuns(ctx, workspacePath)
	if err == nil {
		if runFolder, status, ok := latestRetainedPulseEvidenceFromRuns(runs); ok {
			return runFolder, status
		}
	}

	// iteration-0 is the active retained slot even before schedule history has
	// been written. Pulse can still review plan/config/report evidence when the
	// folder has no completed run artifacts yet.
	return "iteration-0", "unknown"
}

func latestRetainedPulseEvidenceFromRuns(runs []ScheduleRunEntry) (string, string, bool) {
	fallbackFolder := ""
	fallbackStatus := ""
	for _, run := range runs {
		if run.ScheduleID == manualWorkflowPulseScheduleID || run.ScheduleID == pulseFixRunScheduleID || strings.TrimSpace(run.RunFolder) == "" {
			continue
		}
		status := strings.TrimSpace(run.Status)
		if status == "" {
			status = "unknown"
		}
		if status == "running" {
			if fallbackFolder == "" {
				fallbackFolder = strings.TrimSpace(run.RunFolder)
				fallbackStatus = status
			}
			continue
		}
		return strings.TrimSpace(run.RunFolder), status, true
	}
	if fallbackFolder != "" {
		return fallbackFolder, fallbackStatus, true
	}
	return "", "", false
}

// StopRunningJob stops a running scheduled job by canceling its session.
func (s *SchedulerService) StopRunningJobForWorkflow(workspacePath, scheduleID string) {
	s.stopRunningJob(workflowScheduleRuntimeKey(workspacePath, scheduleID), scheduleID)
}

func (s *SchedulerService) StopRunningJob(scheduleID string) {
	keys := s.runtimeKeysForScheduleID(scheduleID)
	if len(keys) != 1 {
		return
	}
	s.stopRunningJob(keys[0], scheduleID)
}

func (s *SchedulerService) stopRunningJob(runtimeKey, scheduleID string) {
	s.runtimeStatesMu.Lock()
	state := s.getRuntimeStateLocked(runtimeKey)
	sessionID := state.LastSessionID
	runID := state.ActiveRunID
	state.LastStatus = "stopped"
	state.LastError = "stopped by user"
	state.ActiveRunID = ""
	s.runtimeStatesMu.Unlock()
	if runID == "" {
		parts := strings.Split(runtimeKey, scheduleScopeSeparator)
		workspacePath := ""
		if len(parts) >= 3 && parts[0] == "workflow" {
			workspacePath = parts[1]
		}
		s.stateStoreMu.RLock()
		store := s.stateStore
		if store != nil && workspacePath != "" {
			if active, err := store.ActiveRunForSchedule(context.Background(), "workflow", workspacePath, scheduleID); err == nil {
				runID = active.RunID
			}
		}
		s.stateStoreMu.RUnlock()
	}
	if runID != "" {
		s.cancelScheduleRunContext(runID)
		s.transitionScheduleRun(context.Background(), nil, schedulerstate.Transition{
			RunID: runID, To: schedulerstate.StateStopped, Reason: "stopped by user", SessionID: sessionID,
			ErrorMessage: "stopped by user", At: time.Now().UTC(),
		})
	}
	if sessionID == "" {
		return
	}

	scheduleLogf("[STOP] ===== stop requested: schedule=%s run=%s session=%s =====", scheduleID, runID, sessionID)
	scheduleLogf("[SCHEDULER] Stopping running job %s (session: %s)", scheduleID, sessionID)
	if isScheduledSession(sessionID) {
		s.api.markSessionTurnInterrupted(sessionID)
	}
	s.cancelScheduledSessionWork(sessionID, "scheduled job stopped by user", runtimePhaseCanceled)

	scheduleLogf("[SCHEDULER] Stopped job %s (session: %s)", scheduleID, sessionID)
	// Closing bracket for the trace. Everything Stop did sits between this and
	// the "stop requested" line above, so one grep gives the whole story.
	scheduleLogf("[STOP] ===== stop teardown complete: schedule=%s session=%s =====", scheduleID, sessionID)
}

// cancelScheduledSessionWork stops agent, workflow, background, and tmux work
// owned by a scheduled session without changing the schedule's recorded run
// result. Pulse timeout recovery uses this before continuing finalization in a
// fresh session.
func (s *SchedulerService) cancelScheduledSessionWork(sessionID, closeReason string, terminalPhase RuntimePhase) {
	if s == nil || s.api == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	s.api.cancelSessionRuntimeWork(sessionID, closeReason, terminalPhase)
}

// triggerSchedule is called by the tick loop when a schedule is due.
func (s *SchedulerService) triggerSchedule(sctx *ScheduleContext, scheduledFor time.Time) {
	if sctx.WorkflowKind == "relay" {
		s.logf(sctx, "[SCHEDULER] Relay timers are not supported; use an authenticated API function trigger")
		return
	}
	triggerCtx := *sctx
	triggerCtx.ScheduledFor = scheduledFor.UTC()
	sctx = &triggerCtx
	schedID := sctx.Schedule.ID
	runtimeKey := scheduleRuntimeKey(sctx)
	now := time.Now()
	ctx := context.Background()
	s.logf(sctx, "[SCHEDULER] ⏰ Cron fired for %s (%s) at %s", schedID, sctx.Schedule.Name, now.Format(time.RFC3339))

	// Late-fire detection: compare to the next_run we recorded last time. Drift > 60s
	// usually means a missed-fire catch-up after macOS sleep/wake, or scheduler stall.
	s.runtimeStatesMu.RLock()
	if st, ok := s.runtimeStates[runtimeKey]; ok && st.NextRunAt != nil {
		expected := *st.NextRunAt
		drift := now.Sub(expected)
		if drift > 60*time.Second {
			s.logf(sctx, "[SCHEDULER] ⚠️ LATE_FIRE schedule=%s expected=%s actual=%s drift=%s",
				schedID, expected.Format(time.RFC3339), now.Format(time.RFC3339), drift.Round(time.Second))
		}
	}
	s.runtimeStatesMu.RUnlock()

	paused, cfg, err := s.IsGloballyPaused(context.Background())
	if err != nil {
		s.logf(sctx, "[SCHEDULER] ⚠️ Failed to read scheduler config before trigger %s: %v", schedID, err)
	} else if paused {
		pausedAt := ""
		if cfg != nil && cfg.PausedAt != nil {
			pausedAt = cfg.PausedAt.Format(time.RFC3339)
		}
		if pausedAt != "" {
			s.logf(sctx, "[SCHEDULER] ⏸️ Global scheduler pause active since %s, skipping %s", pausedAt, schedID)
		} else {
			s.logf(sctx, "[SCHEDULER] ⏸️ Global scheduler pause active, skipping %s", schedID)
		}
		s.recordScheduleFireDecision(ctx, sctx, "skipped_paused", "global scheduler pause is active", "", now.UTC())
		return
	}

	// Reload the workflow manifest so a cron tick always uses the latest schedule.
	var freshCtx *ScheduleContext
	var workflowScheduleIDs []string
	{
		manifest, found, err := ReadWorkflowManifest(context.Background(), sctx.WorkspacePath)
		if err != nil || !found {
			s.logf(sctx, "[SCHEDULER] ❌ Failed to reload manifest for %s: %v", schedID, err)
			s.recordScheduleFireDecision(ctx, sctx, "failed_to_start", "failed to reload workflow manifest", "", now.UTC())
			return
		}

		workflowScheduleIDs = make([]string, 0, len(manifest.Schedules))
		for i := range manifest.Schedules {
			if manifest.Schedules[i].ScheduleType != "webhook" && !scheduleAllowsParallel(manifest.Schedules[i]) {
				workflowScheduleIDs = append(workflowScheduleIDs, manifest.Schedules[i].ID)
			}
		}

		// Find current schedule in manifest (may have been updated)
		var currentSched *WorkflowSchedule
		for i := range manifest.Schedules {
			if manifest.Schedules[i].ID == schedID {
				currentSched = &manifest.Schedules[i]
				break
			}
		}
		if currentSched == nil {
			s.logf(sctx, "[SCHEDULER] ❌ Schedule %s not found in manifest, skipping", schedID)
			s.recordScheduleFireDecision(ctx, sctx, "failed_to_start", "schedule no longer exists", "", now.UTC())
			return
		}
		if !currentSched.Enabled {
			if err := EnsureWorkflowScheduleExecutionTracker(context.Background(), sctx.WorkspacePath, *currentSched, time.Now().UTC()); err != nil {
				s.logf(sctx, "[SCHEDULER] Warning: failed to sync disabled execution history for %s: %v", schedID, err)
			}
			s.logf(sctx, "[SCHEDULER] ⏭️ Schedule %s is disabled, skipping", schedID)
			s.recordScheduleFireDecision(ctx, sctx, "skipped_disabled", "schedule is disabled", "", now.UTC())
			return
		}

		if activeExec := s.findActiveExecutionBlockingSchedule(sctx.WorkspacePath, scheduleAllowsParallel(*currentSched)); activeExec != nil {
			triggeredBy := activeExec.TriggeredBy
			if strings.TrimSpace(triggeredBy) == "" {
				triggeredBy = "unknown"
			}
			s.logf(sctx, "[SCHEDULER] ⏭️ Workflow %s already has an active %s run (session: %s), skipping schedule %s",
				sctx.WorkspacePath, triggeredBy, activeExec.SessionID, schedID)
			s.recordOrQueueBlockedSchedule(ctx, sctx, "workflow already has an active execution", now.UTC())
			return
		}

		resolvedSchedule, calendarItem, ok := scheduleWithReloadedCalendarItem(*currentSched, sctx.CalendarItem)
		if !ok {
			s.logf(sctx, "[SCHEDULER] Calendar item for %s no longer exists, skipping", schedID)
			s.recordScheduleFireDecision(ctx, sctx, "failed_to_start", "calendar item no longer exists", "", now.UTC())
			return
		}
		freshCtx = buildScheduleContext(sctx.WorkspacePath, manifest, resolvedSchedule)
		freshCtx.CalendarItem = calendarItem
	}
	freshCtx.TriggerSource = strings.TrimSpace(sctx.TriggerSource)
	if freshCtx.TriggerSource == "" {
		freshCtx.TriggerSource = "cron"
	}
	freshCtx.ScheduledFor = scheduledFor.UTC()
	// The reload above rebuilds the context from the manifest, so a resume's
	// identity has to be carried across explicitly or it is silently dropped
	// and the run restarts from step 1 (PLAT-101).
	freshCtx.WebhookInput = sctx.WebhookInput
	if sctx.WebhookInput != nil {
		freshCtx.Schedule.GroupNames = sctx.Schedule.GroupNames
		freshCtx.RelayLegacyGroupName = sctx.RelayLegacyGroupName
		freshCtx.Schedule.RouteSelections = sctx.Schedule.RouteSelections
		freshCtx.Schedule.Webhook = sctx.Schedule.Webhook
	}
	freshCtx.CapacityResumeRunID = sctx.CapacityResumeRunID
	freshCtx.CapacityResumeRunFolder = sctx.CapacityResumeRunFolder
	freshCtx.CapacityResumeFromStep = sctx.CapacityResumeFromStep
	if freshCtx.CapacityResumeRunID != "" {
		freshCtx.TriggerSource = "capacity_resume"
	}
	startTime := time.Now().UTC()
	freshCtx.QueuedExpiresAt = sctx.QueuedExpiresAt
	freshCtx.QueuedOccurrenceCount = sctx.QueuedOccurrenceCount
	if disposition, reason := s.scheduleDependencyDisposition(ctx, freshCtx, startTime); disposition != scheduleDependencyReady {
		if disposition == scheduleDependencyBlocked {
			s.discardQueuedOccurrence(ctx, freshCtx)
			decision := "blocked_dependency"
			if strings.Contains(reason, "deadline") {
				decision = "expired_dependency_deadline"
			}
			_ = s.recordScheduleFireDecision(ctx, freshCtx, decision, reason, "", startTime)
			return
		}
		s.logf(freshCtx, "[SCHEDULER] ⏳ Queueing %s: %s", schedID, reason)
		s.queueScheduleOccurrence(ctx, freshCtx, reason, startTime, "retry")
		return
	}

	// Reserve in memory before the durable claim so Stop can cancel even while
	// SQLite is claiming the lease. The database call itself runs without the
	// global runtime-state mutex.
	runID := uuid.NewString()
	if freshCtx.CapacityResumeRunID != "" {
		// Continue the suspended run's own identity so history shows one run
		// that waited, not a failed run followed by an unrelated new one.
		runID = freshCtx.CapacityResumeRunID
	}
	runtimeKey = scheduleRuntimeKey(freshCtx)
	s.runtimeStatesMu.Lock()
	state := s.getRuntimeStateLocked(runtimeKey)
	if state.LastStatus == "running" {
		s.runtimeStatesMu.Unlock()
		s.sessionLogf(freshCtx, state.LastSessionID, "[SCHEDULER] ⏭️ Schedule %s is already running (session: %s), skipping", schedID, state.LastSessionID)
		s.recordOrQueueBlockedSchedule(ctx, freshCtx, "schedule is already running", startTime)
		return
	}
	workflowRuntimeKeys := make([]string, 0, len(workflowScheduleIDs))
	for _, workflowScheduleID := range workflowScheduleIDs {
		workflowRuntimeKeys = append(workflowRuntimeKeys, workflowScheduleRuntimeKey(freshCtx.WorkspacePath, workflowScheduleID))
	}
	if otherKey, otherSession := runningScheduleInSetLocked(s.runtimeStates, workflowRuntimeKeys, scheduleRuntimeKey(freshCtx)); !scheduleAllowsParallel(freshCtx.Schedule) && otherKey != "" {
		s.runtimeStatesMu.Unlock()
		s.logf(freshCtx, "[SCHEDULER] ⏭️ Workflow %s already has running schedule %s (session: %s), skipping schedule %s",
			freshCtx.WorkspacePath, otherKey, otherSession, schedID)
		s.recordOrQueueBlockedSchedule(ctx, freshCtx, "another schedule owns the workflow", startTime)
		return
	}
	if freshCtx.CapacityResumeRunID == "" {
		if window, resetsAt, blocked := s.scheduleQuotaBlock(ctx, freshCtx, now); blocked {
			s.runtimeStatesMu.Unlock()
			// The account's window is at its limit, so this run cannot even
			// reach step one. Starting it would record a red run and, once
			// steps begin having side effects, replay them for nothing.
			reason := fmt.Sprintf("provider %s window is exhausted until %s", window, resetsAt.UTC().Format(time.RFC3339))
			s.logf(freshCtx, "[SCHEDULER] ⏭️ Skipping %s: %s", schedID, reason)
			s.recordScheduleFireDecision(ctx, freshCtx, "skipped_waiting_for_capacity", reason, "", startTime)
			return
		}
		if waitingRun, wait := s.outstandingCapacityWait(ctx, freshCtx.WorkspacePath); waitingRun != nil {
			s.runtimeStatesMu.Unlock()
			// Firing here is what turned one capacity wall into a run storm: the
			// new run restarts from step 1, replays the completed steps' side
			// effects, and hits the same wall — every tick until the window
			// happens to reopen. The suspended run resumes itself instead.
			reason := "a run is suspended on provider capacity: " + wait.Describe()
			s.logf(freshCtx, "[SCHEDULER] ⏭️ Skipping %s: %s", schedID, reason)
			s.recordScheduleFireDecision(ctx, freshCtx, "skipped_waiting_for_capacity", reason, waitingRun.ID, startTime)
			return
		}
	}
	previousState := *state
	runCtx := s.activateScheduleRunLocked(state, runID, startTime)
	s.runtimeStatesMu.Unlock()
	if err := s.claimScheduleRun(ctx, freshCtx, runID, startTime); err != nil {
		s.rollbackScheduleRunActivation(runtimeKey, runID, previousState)
		s.recordOrQueueBlockedSchedule(ctx, freshCtx, err.Error(), startTime)
		s.logf(freshCtx, "[SCHEDULER] ⏭️ Durable run lease rejected schedule %s: %v", schedID, err)
		return
	}
	if s.abortCanceledScheduleRunBeforeStart(runCtx, freshCtx, runtimeKey, runID) {
		return
	}
	s.recordScheduleFireDecision(ctx, freshCtx, "started", "cron fire accepted", runID, startTime)

	if err := RecordWorkflowScheduleExecution(context.Background(), freshCtx.WorkspacePath, freshCtx.Schedule, startTime); err != nil {
		s.logf(freshCtx, "[SCHEDULER] Warning: failed to record scheduled execution for %s: %v", schedID, err)
	}

	s.logf(freshCtx, "[SCHEDULER] 🚀 Starting %s (%s)", schedID, freshCtx.Schedule.Name)
	if _, err := s.runJob(runCtx, freshCtx, runID); err != nil {
		s.logf(freshCtx, "[SCHEDULER] ❌ %s failed: %v", schedID, err)
	} else {
		s.logf(freshCtx, "[SCHEDULER] ✅ %s completed", schedID)
	}
}

func (s *SchedulerService) discardQueuedOccurrence(ctx context.Context, sctx *ScheduleContext) {
	if sctx == nil || strings.TrimSpace(sctx.TriggerSource) != "queued" {
		return
	}
	scopeType, scopeID, _ := scheduleStateScope(sctx)
	s.stateStoreMu.RLock()
	defer s.stateStoreMu.RUnlock()
	if s.stateStore != nil {
		_ = s.stateStore.DeletePendingOccurrence(ctx, scopeType, scopeID, sctx.Schedule.ID)
	}
}

const defaultQueuedScheduleStartDelay = 3 * time.Hour

func (s *SchedulerService) recordOrQueueBlockedSchedule(ctx context.Context, sctx *ScheduleContext, reason string, at time.Time) bool {
	if sctx == nil {
		return false
	}
	policy := strings.TrimSpace(sctx.Schedule.CollisionPolicy)
	if policy == "" {
		policy = "skip"
	}
	if policy == "skip" {
		_ = s.recordScheduleFireDecision(ctx, sctx, "skipped_busy", reason, "", at)
		return false
	}
	return s.queueScheduleOccurrence(ctx, sctx, reason, at, policy)
}

func (s *SchedulerService) queueScheduleOccurrence(ctx context.Context, sctx *ScheduleContext, reason string, at time.Time, policy string) bool {
	if sctx == nil {
		return false
	}
	delay := defaultQueuedScheduleStartDelay
	if minutes := sctx.Schedule.MaxStartDelayMinutes; minutes > 0 {
		delay = time.Duration(minutes) * time.Minute
	}
	scopeType, scopeID, _ := scheduleStateScope(sctx)
	scheduledFor := sctx.ScheduledFor
	if scheduledFor.IsZero() {
		scheduledFor = at
	}
	expiresAt := at.Add(delay).UTC()
	if !sctx.QueuedExpiresAt.IsZero() {
		expiresAt = sctx.QueuedExpiresAt.UTC()
	}
	pending := schedulerstate.PendingOccurrence{
		ScopeType: scopeType, ScopeID: scopeID, ScheduleID: sctx.Schedule.ID,
		ScheduledFor: scheduledFor.UTC(), QueuedAt: at.UTC(), ExpiresAt: expiresAt,
		LatestScheduledFor: scheduledFor.UTC(), TriggerSource: strings.TrimSpace(sctx.TriggerSource),
		Policy: policy, OccurrenceCount: 1, Reason: reason,
	}
	if pending.TriggerSource == "" {
		pending.TriggerSource = "cron"
	}
	s.stateStoreMu.RLock()
	store := s.stateStore
	var err error
	if store != nil {
		err = store.UpsertPendingOccurrence(ctx, pending)
	} else {
		err = errors.New("schedule state store is unavailable")
	}
	s.stateStoreMu.RUnlock()
	if err != nil {
		s.logf(sctx, "[SCHEDULER] failed to queue occurrence; recording skipped_busy: %v", err)
		_ = s.recordScheduleFireDecision(ctx, sctx, "skipped_busy", reason+"; queue persistence failed: "+err.Error(), "", at)
		return false
	}
	decision := "queued_busy"
	if len(scheduleDependencyIDs(sctx.Schedule)) > 0 && policy == "retry" {
		decision = "waiting_for_dependency"
	} else if policy == "coalesce" {
		decision = "coalesced_busy"
	} else if policy == "retry" {
		decision = "queued_retry"
	}
	_ = s.recordScheduleFireDecision(ctx, sctx, decision, reason, "", at)
	s.markScheduleWaiting(sctx, reason, expiresAt)
	return true
}

func (s *SchedulerService) markScheduleWaiting(sctx *ScheduleContext, reason string, expiresAt time.Time) {
	if sctx == nil {
		return
	}
	now := time.Now().UTC()
	count := 1
	s.stateStoreMu.RLock()
	if s.stateStore != nil {
		scopeType, scopeID, _ := scheduleStateScope(sctx)
		if pending, err := s.stateStore.GetPendingOccurrence(context.Background(), scopeType, scopeID, sctx.Schedule.ID); err == nil && pending.OccurrenceCount > 0 {
			count = pending.OccurrenceCount
		}
	}
	s.stateStoreMu.RUnlock()
	s.updateRuntimeState(scheduleRuntimeKey(sctx), func(state *ScheduleRuntimeState) {
		state.LastStatus = "waiting_for_workflow"
		state.LastError = reason
		state.WaitingSince = &now
		state.WaitingUntil = &expiresAt
		state.WaitingReason = reason
		state.QueuedOccurrences = count
	})
}

type scheduleDependencyResult int

const (
	scheduleDependencyReady scheduleDependencyResult = iota
	scheduleDependencyWaiting
	scheduleDependencyBlocked
)

func (s *SchedulerService) scheduleDependencyDisposition(ctx context.Context, sctx *ScheduleContext, now time.Time) (scheduleDependencyResult, string) {
	dependencyIDs := scheduleDependencyIDs(sctx.Schedule)
	if len(dependencyIDs) == 0 {
		return scheduleDependencyReady, ""
	}
	scopeType, scopeID, _ := scheduleStateScope(sctx)
	location, err := time.LoadLocation(strings.TrimSpace(sctx.Schedule.Timezone))
	if err != nil {
		location = time.UTC
	}
	occurrence := sctx.ScheduledFor
	if occurrence.IsZero() {
		occurrence = time.Now()
	}
	localOccurrence := occurrence.In(location)
	year, month, day := localOccurrence.Date()
	dayStart := time.Date(year, month, day, 0, 0, 0, 0, location).UTC()
	dayEnd := dayStart.In(location).AddDate(0, 0, 1).UTC()
	// A later occurrence of the prerequisite must never release an older
	// dependent occurrence after restart. Limit the lookup to prerequisite
	// occurrences scheduled no later than this dependent's own durable time.
	if dependentEnd := occurrence.UTC().Add(time.Nanosecond); dependentEnd.Before(dayEnd) {
		dayEnd = dependentEnd
	}
	var dependencyDeadline time.Time
	if deadline := strings.TrimSpace(sctx.Schedule.DependencyDeadline); deadline != "" {
		parsed, _ := time.Parse("15:04", deadline)
		dependencyDeadline = time.Date(year, month, day, parsed.Hour(), parsed.Minute(), 0, 0, location)
	}
	s.stateStoreMu.RLock()
	store := s.stateStore
	if store == nil {
		s.stateStoreMu.RUnlock()
		return scheduleDependencyWaiting, "dependency state is unavailable"
	}
	defer s.stateStoreMu.RUnlock()
	terminalPolicy := strings.TrimSpace(sctx.Schedule.AfterTerminalStatus)
	if terminalPolicy == "" {
		terminalPolicy = "completed"
	}
	waitingReasons := make([]string, 0, len(dependencyIDs))
	for _, dependencyID := range dependencyIDs {
		run, err := store.RunForScheduleOccurrence(ctx, scopeType, scopeID, dependencyID, dayStart, dayEnd)
		if err != nil {
			if errors.Is(err, schedulerstate.ErrRunNotFound) {
				waitingReasons = append(waitingReasons, fmt.Sprintf("waiting for prerequisite schedule %s occurrence on %04d-%02d-%02d", dependencyID, year, month, day))
				continue
			}
			waitingReasons = append(waitingReasons, fmt.Sprintf("could not read prerequisite schedule %s occurrence: %v", dependencyID, err))
			continue
		}
		if !schedulerstate.IsTerminal(run.State) {
			waitingReasons = append(waitingReasons, fmt.Sprintf("prerequisite schedule %s occurrence %s is still %s", dependencyID, run.RunID, run.State))
			continue
		}
		if terminalPolicy == "completed" && run.State != schedulerstate.StateCompleted {
			return scheduleDependencyBlocked, fmt.Sprintf("prerequisite schedule %s occurrence %s ended %s; completed is required", dependencyID, run.RunID, run.State)
		}
		if !dependencyDeadline.IsZero() {
			if run.CompletedAt == nil {
				waitingReasons = append(waitingReasons, fmt.Sprintf("prerequisite schedule %s is terminal but its completion timestamp is unavailable", dependencyID))
				continue
			}
			releaseAt := run.CompletedAt.Add(time.Duration(sctx.Schedule.AfterDelayMinutes) * time.Minute)
			if releaseAt.After(dependencyDeadline) {
				return scheduleDependencyBlocked, fmt.Sprintf("prerequisite schedule %s released at %s after dependency deadline %s", dependencyID, releaseAt.Format(time.RFC3339), dependencyDeadline.Format(time.RFC3339))
			}
		}
		if delay := sctx.Schedule.AfterDelayMinutes; delay > 0 {
			if run.CompletedAt == nil {
				waitingReasons = append(waitingReasons, fmt.Sprintf("prerequisite schedule %s is terminal but its completion timestamp is unavailable", dependencyID))
				continue
			}
			releaseAt := run.CompletedAt.Add(time.Duration(delay) * time.Minute)
			if now.Before(releaseAt) {
				waitingReasons = append(waitingReasons, fmt.Sprintf("prerequisite schedule %s completed; waiting until %s for configured delay", dependencyID, releaseAt.Format(time.RFC3339)))
			}
		}
	}
	if len(waitingReasons) > 0 {
		if !dependencyDeadline.IsZero() && !now.Before(dependencyDeadline) {
			return scheduleDependencyBlocked, fmt.Sprintf("dependency deadline %s %s passed before prerequisites %s released: %s", sctx.Schedule.DependencyDeadline, location, strings.Join(dependencyIDs, ", "), strings.Join(waitingReasons, "; "))
		}
		return scheduleDependencyWaiting, strings.Join(waitingReasons, "; ")
	}
	return scheduleDependencyReady, ""
}

func (s *SchedulerService) resumeQueuedScheduleOccurrences(ctx context.Context, now time.Time) {
	if !s.queuedResumeMu.TryLock() {
		return
	}
	defer s.queuedResumeMu.Unlock()
	s.stateStoreMu.RLock()
	store := s.stateStore
	if store == nil {
		s.stateStoreMu.RUnlock()
		return
	}
	pending, err := store.ListPendingOccurrences(ctx)
	s.stateStoreMu.RUnlock()
	if err != nil {
		scheduleLogf("[SCHEDULER] queued occurrence scan failed: %v", err)
		return
	}
	for _, item := range pending {
		// Expiry is durable queue state and does not depend on the manifest still
		// being readable. Drop expired rows first so a renamed/deleted workflow
		// cannot leave an immortal pending occurrence behind.
		if !now.Before(item.ExpiresAt) {
			s.stateStoreMu.RLock()
			_ = store.DeletePendingOccurrence(ctx, item.ScopeType, item.ScopeID, item.ScheduleID)
			s.stateStoreMu.RUnlock()
			expiredCtx := buildScheduleContext(item.ScopeID, &WorkflowManifest{}, WorkflowSchedule{ID: item.ScheduleID})
			expiredCtx.TriggerSource = "queued"
			expiredCtx.ScheduledFor = item.ScheduledFor
			_ = s.recordScheduleFireDecision(ctx, expiredCtx, "expired_busy", "queued occurrence exceeded max_start_delay", "", now)
			s.updateRuntimeState(workflowScheduleRuntimeKey(item.ScopeID, item.ScheduleID), func(state *ScheduleRuntimeState) {
				state.LastStatus = "error"
				state.LastError = "queued occurrence exceeded max_start_delay"
				state.WaitingSince = nil
				state.WaitingUntil = nil
				state.WaitingReason = ""
				state.QueuedOccurrences = 0
			})
			continue
		}
		manifest, found, readErr := ReadWorkflowManifest(ctx, item.ScopeID)
		if readErr != nil || !found {
			continue
		}
		var schedule *WorkflowSchedule
		for i := range manifest.Schedules {
			if manifest.Schedules[i].ID == item.ScheduleID {
				schedule = &manifest.Schedules[i]
				break
			}
		}
		if schedule == nil || !schedule.Enabled || !now.Before(item.ExpiresAt) {
			s.stateStoreMu.RLock()
			_ = store.DeletePendingOccurrence(ctx, item.ScopeType, item.ScopeID, item.ScheduleID)
			s.stateStoreMu.RUnlock()
			if schedule != nil {
				sctx := buildScheduleContext(item.ScopeID, manifest, *schedule)
				sctx.TriggerSource = "queued"
				sctx.ScheduledFor = item.ScheduledFor
				sctx.QueuedExpiresAt = item.ExpiresAt
				sctx.QueuedOccurrenceCount = item.OccurrenceCount
				decision := "expired_busy"
				reason := "queued occurrence exceeded max_start_delay"
				if !schedule.Enabled {
					decision, reason = "skipped_disabled", "schedule disabled while queued"
				}
				_ = s.recordScheduleFireDecision(ctx, sctx, decision, reason, "", now)
			}
			continue
		}
		// Keep the row until triggerSchedule acquires the durable workflow lease.
		// BeginQueuedRun consumes the row and inserts the run in one SQLite
		// transaction. A busy lease, cancellation, or process crash therefore
		// leaves this exact occurrence recoverable on the next scan.
		sctx := buildScheduleContext(item.ScopeID, manifest, *schedule)
		sctx.TriggerSource = "queued"
		sctx.ScheduledFor = item.ScheduledFor
		sctx.QueuedExpiresAt = item.ExpiresAt
		sctx.QueuedOccurrenceCount = item.OccurrenceCount
		launchKey := workflowScheduleRuntimeKey(item.ScopeID, item.ScheduleID)
		if !s.beginQueuedLaunch(launchKey) {
			continue
		}
		go func() {
			defer s.endQueuedLaunch(launchKey)
			s.triggerSchedule(sctx, item.ScheduledFor)
		}()
	}
}

func (s *SchedulerService) beginQueuedLaunch(key string) bool {
	s.queuedLaunchMu.Lock()
	defer s.queuedLaunchMu.Unlock()
	if s.queuedLaunching == nil {
		s.queuedLaunching = make(map[string]bool)
	}
	if s.queuedLaunching[key] {
		return false
	}
	s.queuedLaunching[key] = true
	return true
}

func (s *SchedulerService) endQueuedLaunch(key string) {
	s.queuedLaunchMu.Lock()
	delete(s.queuedLaunching, key)
	s.queuedLaunchMu.Unlock()
}

// runJob executes a scheduled job: updates runtime state, creates run history, executes, updates results.
func (s *SchedulerService) runJob(ctx context.Context, sctx *ScheduleContext, runID string) (string, error) {
	if sctx.WebhookInput != nil {
		defer s.pruneWebhookRuns(sctx.WorkspacePath)
	} else if !sctx.PulseOnly {
		defer s.pruneScheduledRuns(sctx.WorkspacePath)
	}
	defer s.releaseScheduleRunContext(runID)
	defer s.maintainRunLease(ctx, runID)()
	schedID := sctx.Schedule.ID
	runtimeKey := scheduleRuntimeKey(sctx)
	startTime := time.Now().UTC()
	s.logf(sctx, "[SCHEDULER] runJob starting for %s (%s) at %s, groups=%v",
		schedID, sctx.Schedule.Name, startTime.Format(time.RFC3339), sctx.Schedule.GroupNames)
	if err := ctx.Err(); err != nil {
		s.updateRuntimeState(runtimeKey, func(state *ScheduleRuntimeState) {
			state.ActiveRunID = ""
			state.LastStatus = "stopped"
			state.LastError = "stopped by user before execution started"
		})
		s.transitionScheduleRun(context.Background(), sctx, schedulerstate.Transition{
			RunID: runID, To: schedulerstate.StateStopped, Reason: "stopped before workflow execution started",
			ErrorMessage: "stopped by user", At: time.Now().UTC(),
		})
		return "", errors.Join(errWorkshopSequenceInterrupted, err)
	}
	if strings.TrimSpace(runID) == "" {
		return "", errors.Join(errWorkshopSequenceInterrupted, errors.New("scheduled run is missing its run id"))
	}
	s.runtimeStatesMu.RLock()
	activeState := s.runtimeStates[runtimeKey]
	ownsActiveRun := activeState != nil && activeState.LastStatus == "running" && activeState.ActiveRunID == runID
	s.runtimeStatesMu.RUnlock()
	if !ownsActiveRun {
		return "", errors.Join(errWorkshopSequenceInterrupted, fmt.Errorf("scheduled run %s no longer owns %s", runID, runtimeKey))
	}

	// Clear session/error fields — status is already "running" (set atomically by caller)
	s.updateRuntimeState(runtimeKey, func(state *ScheduleRuntimeState) {
		state.LastSessionID = ""
		state.LastError = ""
	})
	failBeforeHistory := func(cause error, folder string) {
		persistenceCtx := context.WithoutCancel(ctx)
		completedAt := time.Now().UTC()
		durationMs := completedAt.Sub(startTime).Milliseconds()
		triggerSource := strings.TrimSpace(sctx.TriggerSource)
		if triggerSource == "" {
			triggerSource = "cron"
		}
		var scheduledFor *time.Time
		if !sctx.ScheduledFor.IsZero() {
			value := sctx.ScheduledFor.UTC()
			scheduledFor = &value
		}
		entry := &ScheduleRunEntry{
			ID: runID, ScheduleID: schedID, TriggerSource: triggerSource,
			ScheduledFor: scheduledFor, RunFolder: folder, Status: "error",
			ConcurrencyMode:          effectiveScheduleConcurrencyMode(sctx.Schedule),
			ParallelRiskAcknowledged: sctx.Schedule.ParallelRiskAcknowledged,
			Error:                    cause.Error(), DurationMs: &durationMs, GroupNames: sctx.Schedule.GroupNames,
			StartedAt: startTime, CompletedAt: &completedAt,
		}
		if sctx.WebhookInput != nil {
			entry.Webhook = webhookRunMetadata(sctx)
		}
		if err := AppendScheduleRun(persistenceCtx, sctx.WorkspacePath, entry); err != nil {
			s.logf(sctx, "[SCHEDULER] failed to record schedule startup failure: %v", err)
		}
		s.transitionScheduleRun(persistenceCtx, sctx, schedulerstate.Transition{
			RunID: runID, To: schedulerstate.StateFailed, Reason: "scheduled run failed before workflow execution",
			RunFolder: folder, ErrorMessage: cause.Error(), At: completedAt,
		})
		s.updateRuntimeState(runtimeKey, func(state *ScheduleRuntimeState) {
			state.ActiveRunID = ""
			state.LastStatus = "error"
			state.LastError = cause.Error()
			state.LastDurationMs = &durationMs
			state.NextRunAt = s.nextWorkflowScheduleRunAt(sctx.WorkspacePath, schedID, completedAt)
			state.RunCount++
			state.ConsecutiveFailures++
		})
		s.cleanupRemovedScheduleRuntimeState(runtimeKey)
	}
	if sctx.WorkflowKind == "relay" && (!sctx.Schedule.IsFunctionTrigger() || sctx.WebhookInput == nil) {
		err := errors.New("Relays require an authenticated API function delivery; schedules are not supported")
		failBeforeHistory(err, "")
		return "", err
	}
	// A run whose identity is over its shared-account token limit fails here
	// with the reason, like a refused manual turn (PLAT-683).
	if err := s.api.scheduledRunTokenLimitRefusal(ctx, sctx); err != nil {
		failBeforeHistory(err, "")
		return "", err
	}

	if !sctx.PulseOnly && sctx.WebhookInput == nil {
		folder := strings.TrimSpace(sctx.CapacityResumeRunFolder)
		if folder == "" {
			var allocationErr error
			folder, allocationErr = allocateScheduledRunFolder(sctx.WorkspacePath, runID)
			if allocationErr != nil {
				wrapped := fmt.Errorf("allocate scheduled run folder: %w", allocationErr)
				failBeforeHistory(wrapped, "")
				return "", wrapped
			}
			s.stateStoreMu.RLock()
			store := s.stateStore
			s.stateStoreMu.RUnlock()
			if store == nil {
				wrapped := errors.New("bind scheduled run folder: schedule state store unavailable")
				failBeforeHistory(wrapped, folder)
				return "", wrapped
			}
			if err := store.AssignRunFolder(ctx, runID, folder); err != nil {
				wrapped := fmt.Errorf("bind scheduled run folder: %w", err)
				failBeforeHistory(wrapped, folder)
				return "", wrapped
			}
		}
		sctx.ScheduledRunFolder = folder
	}

	// Create run history entry (file-based)
	if strings.TrimSpace(runID) == "" {
		runID = uuid.New().String()
	}
	triggerSource := strings.TrimSpace(sctx.TriggerSource)
	if triggerSource == "" {
		triggerSource = "cron"
	}
	var scheduledFor *time.Time
	if !sctx.ScheduledFor.IsZero() {
		value := sctx.ScheduledFor.UTC()
		scheduledFor = &value
	}
	run := &ScheduleRunEntry{
		ID:                       runID,
		ScheduleID:               schedID,
		TriggerSource:            triggerSource,
		ScheduledFor:             scheduledFor,
		Status:                   "running",
		GroupNames:               sctx.Schedule.GroupNames,
		StartedAt:                startTime,
		RunFolder:                sctx.ScheduledRunFolder,
		ConcurrencyMode:          effectiveScheduleConcurrencyMode(sctx.Schedule),
		ParallelRiskAcknowledged: sctx.Schedule.ParallelRiskAcknowledged,
	}
	if sctx.WebhookInput != nil {
		run.Webhook = webhookRunMetadata(sctx)
	}
	if sctx.CapacityResumeRunID != "" {
		// A resumed run continues its own history row rather than opening a
		// second one. Two rows would read as two runs, when what happened is one
		// run that waited — and it would leave the first row reporting
		// waiting_for_capacity forever, which suppresses the schedule.
		if err := UpdateScheduleRun(ctx, sctx.WorkspacePath, sctx.CapacityResumeRunID, "running", "", nil, sctx.CapacityResumeRunFolder, ""); err != nil {
			s.logf(sctx, "[SCHEDULER] Failed to reopen waiting run entry %s: %v", sctx.CapacityResumeRunID, err)
		}
	} else if err := AppendScheduleRun(ctx, sctx.WorkspacePath, run); err != nil {
		s.logf(sctx, "[SCHEDULER] Failed to create run entry for %s: %v", schedID, err)
	}
	startReason := "workflow execution starting"
	if sctx.PulseOnly {
		startReason = "Pulse version preflight starting; workflow execution is skipped"
	}
	s.transitionScheduleRun(ctx, sctx, schedulerstate.Transition{
		RunID:  runID,
		To:     schedulerstate.StateWorkflowRunning,
		Reason: startReason,
		At:     time.Now().UTC(),
	})
	if sctx.PulseOnly && !sctx.PulseGoalCheck {
		// A cron/manual Pulse that starts before the tick loop dispatches a
		// pending fast request already satisfies the request. Coalescing here
		// prevents a second review of the same evidence one minute later.
		if err := markFastPulseRequestDelivered(ctx, sctx.WorkspacePath, runID); err != nil {
			s.logf(sctx, "[PULSE] failed to consume pending fast request: %v", err)
		}
	}

	// Workflow Review before the run (PLAT-697 phase 0): a code check, and the
	// review only when the plan changed or has new breaks since the last one.
	// A break it could not fix stops the run with the reason.
	var sessionID, runFolder string
	var execErr error
	if !sctx.PulseOnly && sctx.WebhookInput == nil && sctx.WorkflowKind != "relay" {
		gate := s.ensureWorkflowReviewedBeforeRun(ctx, sctx.WorkspacePath, nil, triggerSource)
		if gate.Notice != "" {
			s.logf(sctx, "[WORKFLOW REVIEW] %s", gate.Notice)
		}
		if !gate.Proceed {
			execErr = errors.New(gate.BlockReason)
			if ctx.Err() != nil {
				execErr = errors.Join(errWorkshopSequenceInterrupted, ctx.Err())
			}
		}
	}

	// Execute
	if execErr == nil {
		sessionID, runFolder, execErr = s.executeJob(ctx, sctx, runID)
	}
	// Execution cancellation must stop work, not the durable record explaining
	// why it stopped. Preserve scheduler-scoped values while removing cancellation
	// from all post-execution history and state writes.
	persistenceCtx := context.WithoutCancel(ctx)

	// Calculate results
	durationMs := time.Since(startTime).Milliseconds()

	status := "success"
	errMsg := ""
	userInterrupted := errors.Is(execErr, errWorkshopSequenceInterrupted) || errors.Is(execErr, context.Canceled)
	if userInterrupted {
		status = "stopped"
		if execErr != nil {
			errMsg = execErr.Error()
		} else {
			errMsg = "stopped by user"
			execErr = errWorkshopSequenceInterrupted
		}
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] %s stopped by user after %dms", schedID, durationMs)
	} else if wait, waiting := s.turnLevelCapacityWait(ctx, sctx, execErr, runFolder, time.Now().UTC()); waiting {
		// The wall landed before any step ran, so no step recorded where to
		// resume from. Suspend anyway: the run executed nothing, so restarting
		// it from the top when capacity returns is safe.
		status = scheduleRunStatusWaitingForCapacity
		errMsg = wait.Describe()
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] ⏸️ %s suspended before any step ran: %s", schedID, errMsg)
	} else if wait, waiting := s.classifyCapacityWait(ctx, sctx, execErr, runFolder, startTime); waiting {
		// Not a failure. The run stopped because the provider has no capacity
		// left, holds completed steps whose side effects must not be replayed,
		// and continues from the same step once the window reopens (PLAT-101).
		status = scheduleRunStatusWaitingForCapacity
		errMsg = wait.Describe()
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] ⏸️ %s suspended after %dms: %s", schedID, durationMs, errMsg)
	} else if execErr != nil {
		status = "error"
		errMsg = execErr.Error()
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] %s failed in %dms: %v", schedID, durationMs, execErr)
	} else {
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] %s completed in %dms, session: %s, folder: %s", schedID, durationMs, sessionID, runFolder)
	}
	finishedReason := "workflow execution finished with status " + status
	finishedSessionKind := "workflow"
	if sctx.PulseOnly {
		finishedReason = "Pulse version preflight finished with status " + status + "; workflow execution was skipped"
		finishedSessionKind = "pulse"
	}
	s.transitionScheduleRun(persistenceCtx, sctx, schedulerstate.Transition{
		RunID:        runID,
		To:           schedulerstate.StateWorkflowFinished,
		Reason:       finishedReason,
		SessionID:    sessionID,
		SessionKind:  finishedSessionKind,
		RunFolder:    runFolder,
		ErrorMessage: errMsg,
		At:           time.Now().UTC(),
	})

	// Keep the runtime state as "running" until all post-run side effects finish.
	// Pulse runs as several resumed builder-chat turns after the workflow result
	// is recorded; if we mark the schedule successful before Pulse finishes, a
	// frequent cron can start the next workflow run while Pulse is between steps.
	// That makes the next Pulse turn fail with workflow_busy (commonly after the
	// LLM/cost/time report), so cadence/backup/publish/notify never run.
	s.updateRuntimeState(runtimeKey, func(state *ScheduleRuntimeState) {
		state.LastSessionID = sessionID
	})

	// Update run history entry for the workflow run. Post-run Pulse
	// may continue after this, but it does not change the recorded run result.
	pulseResult := pulseLifecycleNotRun
	if err := UpdateScheduleRun(persistenceCtx, sctx.WorkspacePath, runID, status, errMsg, &durationMs, runFolder, sessionID); err != nil {
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] Failed to update run entry for %s: %v", schedID, err)
	}
	if !sctx.PulseOnly && sctx.WebhookInput == nil {
		if err := SetScheduleRunRanWorkflow(persistenceCtx, sctx.WorkspacePath, runID, sctx.ProducedRunEvidence); err != nil {
			s.logf(sctx, "[SCHEDULER] could not record ran_workflow for %s: %v", runID, err)
		}
	}

	// After the run: a Pulse-only run (full Pulse, fix run, goal check) runs
	// its Pulse lifecycle; a normal schedule runs its after-run options
	// (backup, publish, notify; after_run.go), which are code unless backup or
	// publish has something to do (PLAT-697 phase 0). Never affects the run's
	// recorded result.
	// An automated lane that still requires a current contract (currently a
	// direct webhook) can return the migration sentinel. The workflow never
	// executed, so there is nothing to review, back up or publish. Skip it and
	// leave the workflow upgrade to the operator's Workshop chat.
	migrationRequired := errors.Is(execErr, errWorkflowContractMigrationRequired)
	if migrationRequired {
		s.sessionLogf(sctx, sessionID, "[PULSE] skipped for %s: this workflow requires a manual contract migration, so it did not run and there is no evidence to review", schedID)
	}
	if status != scheduleRunStatusWaitingForCapacity && !migrationRequired && !userInterrupted {
		if manifest, found, mErr := ReadWorkflowManifest(ctx, sctx.WorkspacePath); mErr == nil && found {
			if sctx.PulseOnly {
				if runFolder != "" && shouldRunPulseLifecycle(sctx, manifest) {
					pulseEvidenceStatus := status
					if strings.TrimSpace(sctx.PulseEvidenceRunStatus) != "" {
						pulseEvidenceStatus = sctx.PulseEvidenceRunStatus
					}
					s.transitionScheduleRun(ctx, sctx, schedulerstate.Transition{
						RunID: runID, To: schedulerstate.StatePulseGate, Reason: "dedicated Pulse review started", SessionID: sessionID, SessionKind: "pulse", At: time.Now().UTC(),
					})
					pulseResult = s.runPulseLifecycle(ctx, sctx, effectiveSchedulePulseMode(sctx, manifest), pulseEvidenceStatus, runFolder, sessionID, runID, errMsg)
				}
			} else if sctx.WebhookInput == nil {
				// The housekeeping turn, when needed, resumes the run's own chat.
				pulseResult = s.runAfterRunOptions(ctx, sctx, manifest, status, errMsg, runFolder, sessionID, runID, time.Since(startTime))
			}
		}
	}

	// Now the whole scheduled job, including post-run side effects, is done.
	terminalState := schedulerstate.StateCompleted
	if status == scheduleRunStatusWaitingForCapacity {
		terminalState = schedulerstate.StateWaitingForCapacity
	} else if userInterrupted {
		terminalState = schedulerstate.StateStopped
	} else if status == "error" {
		terminalState = schedulerstate.StateFailed
	} else if pulseResult == pulseLifecyclePartial {
		terminalState = schedulerstate.StatePartial
	} else if pulseResult == pulseLifecycleStopped {
		terminalState = schedulerstate.StateStopped
	}
	overallStatus := status
	overallError := errMsg
	if terminalState == schedulerstate.StateStopped {
		overallStatus = "stopped"
		if overallError == "" {
			overallError = "stopped by user"
		}
	} else if terminalState == schedulerstate.StatePartial {
		overallStatus = "partial"
		if overallError == "" {
			overallError = "Pulse completed partially"
		}
	}
	// Runtime state measures the complete scheduled job, including post-run
	// Pulse. Normal history remains the workflow result.
	durationMs = time.Since(startTime).Milliseconds()
	if sctx.PulseOnly {
		if err := UpdateScheduleRun(persistenceCtx, sctx.WorkspacePath, runID, overallStatus, overallError, &durationMs, runFolder, sessionID); err != nil {
			s.sessionLogf(sctx, sessionID, "[SCHEDULER] failed to finalize schedule run history: %v", err)
		}
	}
	nextRun := s.nextWorkflowScheduleRunAt(sctx.WorkspacePath, schedID, time.Now())
	s.updateRuntimeState(runtimeKey, func(state *ScheduleRuntimeState) {
		state.ActiveRunID = ""
		state.LastStatus = overallStatus
		state.LastError = overallError
		state.LastDurationMs = &durationMs
		state.NextRunAt = nextRun
		state.RunCount++
		if overallStatus == "error" {
			state.ConsecutiveFailures++
		} else {
			state.ConsecutiveFailures = 0
		}
	})
	terminalReason := "scheduled run finished with status " + overallStatus
	if sctx.PulseOnly {
		terminalReason = "one-off Pulse finished with status " + overallStatus + "; workflow execution was skipped"
	}
	s.transitionScheduleRun(persistenceCtx, sctx, schedulerstate.Transition{
		RunID: runID, To: terminalState, Reason: terminalReason,
		SessionID: sessionID, RunFolder: runFolder, ErrorMessage: overallError, At: time.Now().UTC(),
	})
	s.cleanupRemovedScheduleRuntimeState(runtimeKey)

	return sessionID, execErr
}

// runPulseLifecycle continues the scheduled run's main-agent conversation with
// three conceptual turns: Gate, optional Review+Fix, and Finalize.
// The agent owns reviewer selection, specialist delegation, diagnosis, repair,
// and verification. Go supplies tools and permissions, preserves turn ordering,
// and validates the typed durable receipts between turns. Pulse never changes
// the workflow run's recorded status; post-run failures are logged separately.
type pulseLifecycleResult string

const (
	pulseLifecycleNotRun    pulseLifecycleResult = "not_run"
	pulseLifecycleCompleted pulseLifecycleResult = "completed"
	pulseLifecyclePartial   pulseLifecycleResult = "partial"
	pulseLifecycleStopped   pulseLifecycleResult = "stopped"
)

func (s *SchedulerService) runPulseLifecycle(ctx context.Context, sctx *ScheduleContext, pulseMode, runStatus, runFolder, runSessionID, scheduleRunID, runFailureReason string) (pulseResult pulseLifecycleResult) {
	if sctx.WebhookInput != nil || strings.EqualFold(sctx.Schedule.ScheduleType, "webhook") {
		return pulseLifecycleNotRun
	}
	pulseResult = pulseLifecyclePartial
	// Resume the SAME session the workflow run just used, so Pulse continues in the
	// run's chat thread — the user sees the run and its post-run steward as one
	// conversation, not a fresh session spun up out of nowhere. Fall back to a new id
	// only if the run somehow didn't record one.
	sessionID := strings.TrimSpace(runSessionID)
	if sessionID == "" {
		sessionID = s.newScheduleSessionID(sctx)
	}
	pulseRunID := sessionID
	defer func() {
		if r := recover(); r != nil {
			s.logf(sctx, "[PULSE] post-run pulse panic (recovered): %v", r)
		}
	}()
	baseReqMap := s.buildWorkshopRequest(ctx, sctx)
	if err := initializePulseFinalCommandStates(ctx, sctx.WorkspacePath, pulseRunID); err != nil {
		s.sessionLogf(sctx, sessionID, "[PULSE] failed to initialize final command state: %v", err)
		return
	}

	// Pulse is one continuing agent conversation. Go sends three ordered turns:
	// Gate, Review+Fix, and Finalize. The selected module's terminal result is
	// the single durable completion boundary; Go preserves ordering while agents
	// own the semantic choices and per-finding lifecycle.
	pulseContext := "A scheduled run of this workflow just finished"
	if pulseMode == schedulePulseModeBasic {
		pulseContext = "A run of this workflow just finished; its after-run options need backup or publish, which this one short turn does. This is not a Pulse pass"
	}
	if sctx.PulseOnly {
		pulseContext = "This is a manual Pulse-only review of the latest retained workflow evidence. The workflow was not executed by this action"
	}
	if sctx.PulseGoalCheck {
		pulseContext = "This is the workflow's daily goal check, one short turn. The workflow was not executed by this action"
	}
	intro := pulseLifecycleIntro(pulseContext, sctx.WorkspacePath, pulseRunID, runStatus, runFolder)

	// Pulse does not carry contract upgrades. It used to: b4e4fc14 (2026-07-08)
	// delivered them through this Review+Fix turn, and f58ac5b5 (2026-07-16)
	// replaced that with the blocking pre-run preflight — "contract upgrades are
	// a blocking preflight, not post-run cleanup" — without removing the older
	// path. Both stayed live, and the difference matters: the preflight runs one
	// rung per turn and re-reads the manifest to verify that exact target before
	// advancing, while this path concatenated every outstanding rung into a
	// single review turn with no verification or ordering between them. Observed
	// on confida-login 2026-08-12, where a failed-open preflight left four
	// migrations (1.0.21/22/23/25) bundled into one dispatch. The preflight owns
	// migrations; it retries on the next trigger.
	//
	// Pulse shares the scheduler's session, so withdraw any stamp authorization
	// the preflight left behind rather than letting it outlive its turn.
	contractupgrade.Revoke(sessionID)
	defer contractupgrade.Revoke(sessionID)
	s.sessionLogf(sctx, sessionID, "[PULSE] starting pulse for %s (run_folder=%s status=%s)", sctx.Schedule.ID, runFolder, runStatus)
	if pulseMode == schedulePulseModeFull && !sctx.PulseGoalCheck {
		// Retire unowned legacy step observations and hand platform-owned
		// findings off before Gate reads the backlog. Idempotent.
		if reconciled, err := stepworkflow.ReconcilePulseActionableBacklog(ctx, sctx.WorkspacePath); err != nil {
			s.sessionLogf(sctx, sessionID, "[PULSE] backlog reconciliation failed (continuing): %v", err)
		} else if reconciled.RetiredLegacyObservations > 0 || reconciled.PlatformHandoffs > 0 {
			s.sessionLogf(sctx, sessionID, "[PULSE] backlog reconciliation retired %d legacy step observation(s) and handed off %d platform finding(s)", reconciled.RetiredLegacyObservations, reconciled.PlatformHandoffs)
		}
	}
	introSent := false
	recoveryNotes := []string{}
	runStep := func(st pulseLifecycleStep) pulseLifecycleStepRunResult {
		reqMap := cloneStringInterfaceMap(baseReqMap)
		markPulseLifecycleTurn(reqMap)
		query := st.query
		includesIntro := false
		if !introSent {
			priorFailureContext := ""
			if len(recoveryNotes) > 0 {
				priorFailureContext = "\n\nPRIOR PULSE FAILURE CONTEXT. Earlier work in this same conversation did not finish cleanly. Preserve partial/failed status honestly and do not claim skipped work succeeded:\n- " + strings.Join(recoveryNotes, "\n- ")
			}
			query = intro + priorFailureContext + "\n\n" + query
			includesIntro = true
		}
		reqMap["query"] = query
		if err := s.api.startSessionInternal(ctx, reqMap, sessionID, sctx.OwnerUserID, nil); err != nil {
			s.sessionLogf(sctx, sessionID, "[PULSE] step %q did not finish: %v", st.label, err)
			outcome := pulseLifecycleStepWaitFailed
			if errors.Is(err, errWorkshopSequenceInterrupted) || errors.Is(err, context.Canceled) {
				outcome = pulseLifecycleStepInterrupted
			} else if errors.Is(err, errWorkshopIdleWaitTimeout) {
				outcome = pulseLifecycleStepTimedOut
			}
			return pulseLifecycleStepRunResult{outcome: outcome, err: err}
		}
		if includesIntro {
			introSent = true
		}
		s.sessionLogf(sctx, sessionID, "[PULSE] step %q done for %s", st.label, sctx.Schedule.ID)
		return pulseLifecycleStepRunResult{outcome: pulseLifecycleStepCompleted}
	}
	handleStepFailure := func(st pulseLifecycleStep, result pulseLifecycleStepRunResult, needsFollowup bool) string {
		failureLabel := "failed"
		if result.outcome == pulseLifecycleStepTimedOut {
			failureLabel = fmt.Sprintf("made no observable progress for %s", st.idleMaxInactivity())
		}
		reason := fmt.Sprintf("Pulse step %s %s", st.label, failureLabel)
		if result.err != nil && result.outcome != pulseLifecycleStepTimedOut {
			reason += ": " + result.err.Error()
		}
		recoveryNotes = append(recoveryNotes, reason)
		if needsFollowup {
			s.sessionLogf(sctx, sessionID, "[PULSE] continuing in the same Pulse conversation after %s", reason)
		}
		return reason
	}
	abortIfInterrupted := func(st pulseLifecycleStep, result pulseLifecycleStepRunResult) bool {
		if result.outcome != pulseLifecycleStepInterrupted {
			return false
		}
		reason := fmt.Sprintf("Pulse stopped by user during %s", st.label)
		pulseResult = pulseLifecycleStopped
		s.sessionLogf(sctx, sessionID, "[PULSE] %s; no later Review, Fix, Finalize, publish, or notification turn will run", reason)
		return true
	}

	// A scheduled Pulse reviews evidence produced by this invocation. A run that
	// failed still counts; a preflight abort against a pre-existing, untouched
	// run folder does not. Manual PulseOnly is the intentional exception because
	// it explicitly reviews retained evidence without executing the workflow.
	reviewEvidenceAvailable := sctx.ProducedRunEvidence || sctx.PulseOnly
	if pulseMode == schedulePulseModeFull && !reviewEvidenceAvailable {
		s.sessionLogf(sctx, sessionID, "[PULSE] workflow did not start in this invocation; skipping Gate, reviewers, Fixer, dashboard and publish")
	}

	var steps []pulseLifecycleStep
	if pulseMode == schedulePulseModeBasic {
		// After-run housekeeping (after_run.go): only the backup and publish
		// the code check found work for. Notify is sent by code after this
		// turn; its command is closed after the turn.
		skip := func(command, reason string) {
			if _, err := markPulseFinalCommandState(ctx, sctx.WorkspacePath, command, pulseRunID, "skipped", reason); err != nil {
				s.sessionLogf(sctx, sessionID, "[AFTER RUN] could not mark %s skipped: %v", command, err)
			}
		}
		if !sctx.AfterRunBackup {
			skip(pulseFinalCommandBackup, "Not selected for this run, or nothing changed since the last backup.")
		}
		if !sctx.AfterRunPublish {
			skip(pulseFinalCommandPublish, "Not selected for this run, or the published report is current.")
		}
		steps = afterRunHousekeepingSteps(scheduleRunID, sctx.AfterRunBackup, sctx.AfterRunPublish)
		s.sessionLogf(sctx, sessionID, "[AFTER RUN] housekeeping turn for %s: backup=%v publish=%v", sctx.Schedule.ID, sctx.AfterRunBackup, sctx.AfterRunPublish)
	} else if !reviewEvidenceAvailable {
		steps = pulseLifecycleNoRunSteps(pulseRunID, runFailureReason, notificationInstructionsFromCapabilities(sctx.Capabilities))
	} else if sctx.PulseGoalCheck {
		// The goal check runs in the workflow's persistent Pulse
		// conversation (PLAT-697 phase 4), not in this pass's own session.
		check := pulseLifecycleGoalCheckStep(ctx, sctx.WorkspacePath, pulseRunID, notificationInstructionsFromCapabilities(sctx.Capabilities))
		check.goalLead = true
		steps = []pulseLifecycleStep{check}
		s.sessionLogf(sctx, sessionID, "[PULSE] daily goal check for %s, in the Pulse conversation", sctx.WorkspacePath)
	} else if sctx.PulseFixRun {
		// A fix run records its own worklist instead of running the Gate agent
		// and runs Technical Review+Fix only. Workflow Review runs before runs
		// and backup/publish/notify are per-schedule after-run options
		// (PLAT-697 phase 0), so neither is part of a fix run.
		if err := recordPulseFixRunWorklist(ctx, sctx.WorkspacePath, pulseRunID, sctx.PulseFixReason); err != nil {
			s.sessionLogf(sctx, sessionID, "[PULSE] fix run could not record its worklist: %v", err)
		} else {
			steps = append(steps, pulseLifecycleModuleReviewStep(pulseRunID, pulseModuleTechnicalReview))
		}
		s.sessionLogf(sctx, sessionID, "[PULSE] fix run selected %d steps for %s: %s", len(steps), sctx.WorkspacePath, sctx.PulseFixReason)
	} else {
		// A workflow with a goal: its Pulse conversation owns QA and
		// architecture, so the pass runs only Goal Work after Gate
		// (goal_lead_owns_reviews.go, PLAT-697).
		hasGoal := workflowHasGoal(ctx, sctx.WorkspacePath)
		gateStep := pulseLifecycleGateStep(pulseRunID, runFolder, runStatus)
		if sctx.Schedule.PulseReviewOnly {
			folders, foldersErr := s.api.loadRunFoldersInternal(ctx, sctx.WorkspacePath)
			if foldersErr != nil {
				s.sessionLogf(sctx, sessionID, "[PULSE] periodic backlog listing failed, Gate will reason with no folder listing: %v", foldersErr)
				folders = nil
			}
			gateStep = pulseLifecycleBacklogGateStep(pulseRunID, pulseReviewBacklogSummary(folders))
		}
		if hasGoal {
			gateStep.query += goalLeadGateNote
		}
		gateCompleted := false
		for attempt := 1; attempt <= 2; attempt++ {
			result := runStep(gateStep)
			if abortIfInterrupted(gateStep, result) {
				return
			}
			if result.outcome == pulseLifecycleStepCompleted {
				if err := validatePulseGateCompletion(ctx, sctx.WorkspacePath, pulseRunID); err == nil {
					gateCompleted = true
					break
				} else {
					s.sessionLogf(sctx, sessionID, "[PULSE] Gate completion contract failed (attempt %d/2): %v", attempt, err)
					result = pulseLifecycleStepRunResult{outcome: pulseLifecycleStepWaitFailed, err: err}
				}
			} else if err := validatePulseGateCompletion(ctx, sctx.WorkspacePath, pulseRunID); err == nil {
				// The agent may time out after it has already committed the
				// complete durable worklist. Preserve the failure truth, but do
				// not discard its due-module routing.
				handleStepFailure(gateStep, result, true)
				gateCompleted = true
				s.sessionLogf(sctx, sessionID, "[PULSE] Gate stage failed after recording a complete durable worklist; continuing with selected modules")
				break
			}
			if attempt == 1 {
				handleStepFailure(gateStep, result, true)
				continue
			}
			handleStepFailure(gateStep, result, true)
		}
		if !gateCompleted {
			if err := validatePulseGateCompletion(ctx, sctx.WorkspacePath, pulseRunID); err == nil {
				gateCompleted = true
				s.sessionLogf(sctx, sessionID, "[PULSE] recovered complete durable Gate worklist after the stage failure")
			}
		}
		if gateCompleted {
			// Workflow Review (Plan Drift) is not a Pulse module any more: it
			// runs before every run (PLAT-697 phase 0, workflow_review_prerun.go),
			// so nothing here waits for it or holds Goal Work's levels for it.
			if hasGoal {
				// A recovery, the prompt budget or a protected boundary can
				// still make Architecture or Technical due; close them so
				// nothing waits on a turn this pass does not run.
				if err := closeGoalLeadOwnedModules(ctx, sctx.WorkspacePath, pulseRunID); err != nil {
					s.sessionLogf(sctx, sessionID, "[PULSE] could not close the reviews the Pulse conversation owns: %v", err)
				}
			}
			// Goal Work runs first; for a goal workflow it is the only module
			// turn and runs in the persistent Pulse conversation (PLAT-697).
			moduleSteps := pulsePassModuleSteps(pulseRunID, hasGoal, func(module string) bool {
				due, err := pulseWorklistModulesDue(ctx, sctx.WorkspacePath, pulseRunID, module)
				return err != nil || due
			})
			for _, step := range moduleSteps {
				if pulsemodules.ForStepLabel(step.label) == pulseModuleStrategicReview {
					perms, text := goalWorkAutonomy(ctx, sctx.WorkspacePath)
					step.query += "\n\n" + text
					step.goalWork = &perms
				}
				steps = append(steps, step)
			}
			steps = append(steps, pulseLifecycleFinalSteps(pulseRunID, notificationInstructionsFromCapabilities(sctx.Capabilities))...)
			if len(steps) > 0 && !isPulseLifecycleFinalStep(steps[0].label) {
				s.transitionScheduleRun(ctx, sctx, schedulerstate.Transition{
					RunID: scheduleRunID, To: schedulerstate.StatePulseModules, Reason: "Pulse Gate recorded due modules",
					SessionID: sessionID, SessionKind: "pulse", At: time.Now().UTC(),
				})
			}
			s.sessionLogf(sctx, sessionID, "[PULSE] selected %d post-gate steps for %s", len(steps), sctx.Schedule.ID)
		} else {
			steps = pulseLifecycleFinalSteps(pulseRunID, notificationInstructionsFromCapabilities(sctx.Capabilities))
		}
	}

	// runHeldStep runs a turn; a Goal Work turn holds the session's tools to its
	// autonomy levels only while it runs (pulse_autonomy_guard.go).
	runHeldStep := func(st pulseLifecycleStep) pulseLifecycleStepRunResult {
		if st.goalLead {
			// The Pulse conversation holds its own tools for the turn.
			return s.runGoalLeadPassStep(ctx, sctx, st)
		}
		if st.goalWork != nil {
			defer beginGoalWorkTurn(sessionID, *st.goalWork)()
			s.sessionLogf(sctx, sessionID, "[PULSE] Goal Work tools held to run=%v outward=%v change=%v for %s", st.goalWork.Run, st.goalWork.Outward, st.goalWork.Change, st.label)
		}
		return runStep(st)
	}

	// Index-based (not range) so a late review-fix insertion below is picked
	// up by the loop that is already in progress — range would have captured
	// the pre-insertion length and never see it.
	for i := 0; i < len(steps); i++ {
		st := steps[i]
		reviewModule := pulsemodules.ForStepLabel(st.label)
		isImprovementReview := reviewModule != "" && reviewModule != pulseModulePlanDriftReview
		if st.label == "finalize" {
			st.query += s.prepareWorkflowDatabaseBackupSnapshot(ctx, sctx)
		}
		// No Pulse turn stamps a contract version, so none of them is granted
		// one. Revoking after each turn keeps that true even if a future step
		// starts minting.
		if isImprovementReview {
			if err := beginDuePulseReviewRecoveries(ctx, sctx.WorkspacePath, pulseRunID, reviewModule); err != nil {
				result := pulseLifecycleStepRunResult{outcome: pulseLifecycleStepWaitFailed, err: fmt.Errorf("create Pulse review recovery attempts: %w", err)}
				handleStepFailure(st, result, i < len(steps)-1)
				continue
			}
		}
		// The Pulse conversation's own agent does each module review in its own
		// turn, using its own subagents if it wants; there is no separately
		// launched background reviewer.
		result := runHeldStep(st)
		contractupgrade.Revoke(sessionID)
		if abortIfInterrupted(st, result) {
			return
		}
		if result.outcome == pulseLifecycleStepCompleted && (isImprovementReview || st.label == "plan-drift-review") {
			var receiptErr error
			if st.label == "plan-drift-review" {
				receiptErr = validatePulseDueModuleResultsFor(ctx, sctx.WorkspacePath, pulseRunID, pulseModulePlanDriftReview)
			} else {
				receiptErr = validatePulseDueModuleResultsFor(ctx, sctx.WorkspacePath, pulseRunID, reviewModule)
			}
			if receiptErr != nil {
				s.sessionLogf(sctx, sessionID, "[PULSE] %s receipt incomplete; asking the same conversation to reconcile it: %v", st.label, receiptErr)
				continuation := pulseLifecycleReviewFixContinuationStep(pulseRunID, receiptErr)
				continuation.goalWork = st.goalWork
				continuation.goalLead = st.goalLead
				result = runHeldStep(continuation)
				if abortIfInterrupted(continuation, result) {
					return
				}
				if result.outcome == pulseLifecycleStepCompleted {
					var retryErr error
					if st.label == "plan-drift-review" {
						retryErr = validatePulseDueModuleResultsFor(ctx, sctx.WorkspacePath, pulseRunID, pulseModulePlanDriftReview)
					} else {
						retryErr = validatePulseDueModuleResultsFor(ctx, sctx.WorkspacePath, pulseRunID, reviewModule)
					}
					if retryErr != nil {
						result = pulseLifecycleStepRunResult{outcome: pulseLifecycleStepWaitFailed, err: retryErr}
					}
				}
			}
			// Only technical_review promises to drain workflow-owned repairs.
			// A strategic-only review must not fail on unrelated repair debt.
			if reviewModule == pulseModuleTechnicalReview && result.outcome == pulseLifecycleStepCompleted {
				if err := validatePulseTechnicalRepairDrain(ctx, sctx.WorkspacePath, pulseRunID); err != nil {
					result = pulseLifecycleStepRunResult{outcome: pulseLifecycleStepWaitFailed, err: err}
				}
			}
		}
		if isImprovementReview {
			// This is intentionally scheduler-owned. A child can write a useful
			// checkpoint and then time out or exit before its final tool call; an
			// instruction telling the parent agent to remember that fact is not a
			// durable recovery mechanism.
			if recovered, recoveryErr := recordIncompletePulseReviewRecoveries(ctx, sctx.WorkspacePath, pulseRunID, result, reviewModule); recoveryErr != nil {
				s.sessionLogf(sctx, sessionID, "[PULSE] could not persist incomplete review recovery: %v", recoveryErr)
				if result.outcome == pulseLifecycleStepCompleted {
					result = pulseLifecycleStepRunResult{outcome: pulseLifecycleStepWaitFailed, err: fmt.Errorf("persist incomplete Pulse review recovery: %w", recoveryErr)}
				}
			} else if len(recovered) > 0 {
				s.sessionLogf(sctx, sessionID, "[PULSE] persisted recovery for incomplete review module(s): %s", strings.Join(recovered, ", "))
			}
		}
		if result.outcome != pulseLifecycleStepCompleted {
			handleStepFailure(st, result, i < len(steps)-1)
		}
	}
	if pulseMode == schedulePulseModeBasic {
		if _, err := markPulseFinalCommandState(ctx, sctx.WorkspacePath, pulseFinalCommandNotify, pulseRunID, "skipped", "The run notification is sent by the platform after the run, not by this turn."); err != nil {
			s.sessionLogf(sctx, sessionID, "[AFTER RUN] could not close the notify command: %v", err)
		}
	}
	if len(recoveryNotes) > 0 {
		pulseResult = pulseLifecyclePartial
		s.sessionLogf(sctx, sessionID, "[PULSE] pulse finalized partially for %s after %d failed/timed-out step(s)", sctx.Schedule.ID, len(recoveryNotes))
	} else {
		pulseResult = pulseLifecycleCompleted
		s.sessionLogf(sctx, sessionID, "[PULSE] pulse completed for %s", sctx.Schedule.ID)
	}
	// Pulse owns its own notification from the durable SQLite state. The popup is
	// the only Pulse presentation; there is no parallel HTML journal to update.
	return pulseResult
}

// prepareWorkflowDatabaseBackupSnapshot makes the protected live SQLite DB
// backupable before the agent begins the ordinary Git/object-store backup.
// This is deliberately deterministic: an agent never needs shell access to
// db.sqlite, its WAL, or its SHM file, and a snapshot failure cannot suppress
// the later publish or notification operations.
func (s *SchedulerService) prepareWorkflowDatabaseBackupSnapshot(ctx context.Context, sctx *ScheduleContext) string {
	if sctx == nil {
		return ""
	}
	manifest, exists, err := ReadWorkflowManifest(ctx, sctx.WorkspacePath)
	if err != nil {
		s.logf(sctx, "[BACKUP] could not inspect workflow backup config before finalization: %v", err)
		return fmt.Sprintf("\n\nMANAGED DATABASE SNAPSHOT. The backend could not inspect workflow backup config: %v. Keep backup status truthful, but continue independently through Publish and Notify.", err)
	}
	if !exists || !workflowBackupRequiresDatabaseSnapshot(manifest.Backup, sctx.TriggerSource) {
		return ""
	}

	client := workspace.NewClient(getWorkspaceAPIURL())
	dbPath := filepath.ToSlash(filepath.Join(sctx.WorkspacePath, "db", "db.sqlite"))
	result, err := client.CreateWorkflowDatabaseBackupSnapshot(ctx, workspace.CreateWorkflowDatabaseBackupSnapshotParams{DBPath: dbPath})
	if err != nil {
		s.logf(sctx, "[BACKUP] managed workflow database snapshot failed: %v", err)
		return fmt.Sprintf("\n\nMANAGED DATABASE SNAPSHOT. The backend attempted the required WAL-aware snapshot before this turn, but it failed: %v. Mark the database portion of Backup partial/failed as appropriate; do not try to read or stage live db/db.sqlite. Publish and Notify are independent and must still run.", err)
	}
	s.logf(sctx, "[BACKUP] managed workflow database snapshot ready at %s", result.SnapshotPath)
	return fmt.Sprintf("\n\nMANAGED DATABASE SNAPSHOT. The backend already created and integrity-checked the current SQLite backup image at %q (sha256 %s, %d bytes). Stage that snapshot and its checksum at %q for every destination covering db-sqlite; never read or stage live db/db.sqlite. Publish and Notify are independent of the Backup result.", result.SnapshotPath, result.SHA256, result.SizeBytes, result.ChecksumPath)
}

func workflowBackupRequiresDatabaseSnapshot(config *WorkflowBackupConfig, triggerSource string) bool {
	if config == nil || !config.Enabled {
		return false
	}
	manual := strings.EqualFold(strings.TrimSpace(triggerSource), "manual")
	if manual && !config.Triggers.AfterManualRun {
		return false
	}
	if !manual && !config.Triggers.AfterScheduledRun {
		return false
	}
	for _, destination := range config.Destinations {
		for _, covered := range destination.Covers {
			if strings.EqualFold(strings.TrimSpace(covered), "db-sqlite") {
				return true
			}
		}
	}
	return false
}

type pulseLifecycleStep struct {
	label, query string
	// goalWork, when set, are the autonomy levels the server enforces on the
	// Pulse session's tools for the length of this turn (Goal Work only).
	goalWork *stepworkflow.GoalWorkPermissions
	// goalLead runs the turn in the workflow's Pulse conversation
	// (goal_lead_conversation.go) instead of the pass's own session.
	goalLead bool
}

type pulseLifecycleStepOutcome string

const (
	pulseLifecycleStepCompleted   pulseLifecycleStepOutcome = "completed"
	pulseLifecycleStepWaitFailed  pulseLifecycleStepOutcome = "wait_failed"
	pulseLifecycleStepTimedOut    pulseLifecycleStepOutcome = "timed_out"
	pulseLifecycleStepInterrupted pulseLifecycleStepOutcome = "interrupted"
)

type pulseLifecycleStepRunResult struct {
	outcome pulseLifecycleStepOutcome
	err     error
}

func pulseLifecycleIntro(contextSummary, workspacePath, pulseRunID, runStatus, runFolder string) string {
	return fmt.Sprintf("PULSE RUN CONTEXT. %s. workspace_path=%q, pulse_run_id=%q, evidence_status=%q, run_folder=%q. This is one continuing Pulse conversation. The scheduler sends Gate, Review+Fix, and Finalize turns in order. Own the reasoning and any useful specialist delegation inside the current turn, use durable workflow state for human answers, keep user-facing output concise, persist the required receipt, then stop so the next turn can continue.",
		contextSummary, workspacePath, pulseRunID, runStatus, runFolder)
}

// isPulseLifecycleFinalStep marks the finalizer, which must still run when
// earlier maintenance failed so notification/final command state stay truthful.
func isPulseLifecycleFinalStep(label string) bool {
	return label == "finalize"
}

// A module review runs inside this step's own turn (there is no separately
// launched reviewer whose child work extended the wait), so it gets a longer
// quiet period than the Gate or the finalizer (PLAT-452).
var schedulerPulseReviewMaxInactivity = 30 * time.Minute

func (st pulseLifecycleStep) idleMaxInactivity() time.Duration {
	if st.label == "plan-drift-review" || pulsemodules.ForStepLabel(st.label) != "" {
		return schedulerPulseReviewMaxInactivity
	}
	return schedulerWorkshopMaxInactivity
}

func workflowHasPendingPlanChangelogArtifactReview(ctx context.Context, workspacePath string) (bool, error) {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if workspacePath == "" {
		return false, nil
	}

	folder := workspacePath + "/planning/changelog"
	listing, exists, err := listWorkspaceFolder(ctx, folder, 1)
	if err != nil {
		return true, err
	}
	if !exists {
		return false, nil
	}

	var filePaths []string
	collectWorkspaceFilePaths(listing, &filePaths)
	for _, filePath := range filePaths {
		base := filepath.Base(filePath)
		if !strings.HasPrefix(base, "changelog-") || !strings.HasSuffix(strings.ToLower(base), ".json") {
			continue
		}

		content, exists, err := readFileFromWorkspace(ctx, filePath)
		if err != nil {
			return true, err
		}
		if !exists || strings.TrimSpace(content) == "" {
			continue
		}

		var changelog planChangelogFile
		if err := json.Unmarshal([]byte(content), &changelog); err != nil {
			// A malformed changelog still needs human/agent attention; keep the
			// Pulse Artifact Review turn rather than treating it as clean.
			return true, nil
		}
		for _, entry := range changelog.Entries {
			if entry.ArtifactReview == nil || !entry.ArtifactReview.Done {
				return true, nil
			}
		}
	}

	return false, nil
}

func pulseLifecycleSteps() []pulseLifecycleStep {
	steps := []pulseLifecycleStep{
		pulseLifecycleGateStep("<pulse_run_id>", "<run_folder>", "<run_status>"),
	}
	for _, module := range pulsemodules.ExecutionOrder {
		if module == pulseModulePlanDriftReview {
			steps = append(steps, pulseLifecyclePlanDriftReviewStep("<pulse_run_id>"))
			continue
		}
		steps = append(steps, pulseLifecycleModuleReviewStep("<pulse_run_id>", module))
	}
	steps = append(steps, pulseLifecycleFinalSteps("<pulse_run_id>")...)
	return steps
}

func pulseLifecycleGateStep(pulseRunID, runFolder, runStatus string) pulseLifecycleStep {
	return pulseLifecycleStep{
		label: "gate",
		query: fmt.Sprintf("PULSE GATE / WORKLIST. pulse_run_id=%q, run_folder=%q, run_status=%q. Load read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/pulse-gate.md\"}]) and follow it exactly. Perform only the progressive Gate scan. Choose the pass mode yourself from backlog and new-run evidence, then call record_pulse_worklist exactly once with mode, mode_reason, and all %d module decisions, and stop. Workflow steps and evaluation/collector steps own metric observations; do not create duplicate impact records. Do not fabricate measurements, write workflow artifacts, launch reviewers, fix, back up, publish, or notify.",
			pulseRunID, runFolder, runStatus, len(pulseModuleOrder)),
	}
}

// pulseLifecycleBacklogGateStep is Gate's prompt for a PulseReviewOnly
// periodic pass (PLAT-115): it hands Gate a listing of currently-existing run
// folders instead of pinning it to one run_folder=%q. Deliberately does not
// pre-filter "what's new" in Go — that reasoning belongs to Gate, comparing
// this listing against get_pulse_state's own last_checked_at per module, the
// same kind of judgment call Gate already makes for mode selection.
func pulseLifecycleBacklogGateStep(pulseRunID, backlogSummary string) pulseLifecycleStep {
	return pulseLifecycleStep{
		label: "gate",
		query: fmt.Sprintf("PULSE GATE / WORKLIST — PERIODIC BACKLOG REVIEW. pulse_run_id=%q. This is your workflow's own periodic Pulse review pass: it does not follow one specific run, it reviews whatever has accumulated since your last check. Currently existing run folders (name, status, started_at, completed_at), newest first:\n%s\n\nCompare these against get_pulse_state's last_checked_at per module to reason about what is genuinely new since you last looked — do not assume every listed folder is new, and do not skip the whole backlog because only some of it is. If the number of runs since your last check plausibly exceeds what run_retention_count preserved, say so explicitly in your worklist evidence rather than reviewing a partial sample as if it were complete, and consider raising it as a technical_review finding. Load read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/pulse-gate.md\"}]) and follow it exactly. Perform only the progressive Gate scan. Choose the pass mode yourself from backlog and new-run evidence, then call record_pulse_worklist exactly once with mode, mode_reason, and all %d module decisions, and stop. Workflow steps and evaluation/collector steps own metric observations; do not create duplicate impact records. Do not fabricate measurements, write workflow artifacts, launch reviewers, fix, back up, publish, or notify.",
			pulseRunID, backlogSummary, len(pulseModuleOrder)),
	}
}

// pulseReviewBacklogSummary renders currently-existing run folders as a
// compact, newest-first text listing for pulseLifecycleBacklogGateStep.
// iteration-0 (the live/reused slot, never a stable identity across time —
// see PLAT-115) is included only when its own metadata reports a terminal
// status, so Gate is never handed a run that may still be in flight.
func pulseReviewBacklogSummary(folders []RunFolderInfo) string {
	if len(folders) == 0 {
		return "(no run folders exist yet)"
	}
	var lines []string
	for _, folder := range folders {
		name := strings.TrimSpace(folder.Name)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "iteration-0") && !runFolderMetadataIsTerminal(folder.Metadata) {
			continue
		}
		lines = append(lines, "- "+pulseReviewBacklogFolderLine(folder))
	}
	if len(lines) == 0 {
		return "(no reviewable run folders — the only existing folder is still in progress)"
	}
	return strings.Join(lines, "\n")
}

func runFolderMetadataIsTerminal(metadata *RunMetadata) bool {
	if metadata == nil {
		return false
	}
	switch strings.TrimSpace(strings.ToLower(metadata.Status)) {
	case "completed", "failed", "canceled":
		return true
	default:
		return false
	}
}

func pulseReviewBacklogFolderLine(folder RunFolderInfo) string {
	if folder.Metadata == nil {
		return fmt.Sprintf("%s (status=unknown, no run_metadata.json)", folder.Name)
	}
	completedAt := "not completed"
	if folder.Metadata.CompletedAt != nil {
		completedAt = folder.Metadata.CompletedAt.Format(time.RFC3339)
	}
	return fmt.Sprintf("%s (status=%s, started_at=%s, completed_at=%s)",
		folder.Name, folder.Metadata.Status, folder.Metadata.StartedAt.Format(time.RFC3339), completedAt)
}

// pulseLifecyclePlanDriftReviewStep runs plan_drift_review as its own
// lifecycle step, sequenced strictly BEFORE the technical_review step, so
// technical_review only ever reads a Pulse backlog that already reflects this
// run's plan_drift_review findings. The Pulse agent does the review itself in
// this turn (using its own subagents if it wants) and the step does not return
// until the turn completes.
func pulseLifecyclePlanDriftReviewStep(pulseRunID string) pulseLifecycleStep {
	return pulseLifecycleStep{
		label: "plan-drift-review",
		query: fmt.Sprintf(`PULSE PLAN DRIFT REVIEW. pulse_run_id=%q. Read the durable Gate worklist via get_pulse_state(view="module", pulse_run_id=<this id>). If plan_drift_review is not due, do nothing and end this turn immediately.

When it is due, do the review yourself in this turn. You may use your own subagents for parallel reading or analysis, but you stay responsible for the result and only you record it. %s`, pulseRunID, pulseReviewerInstruction(pulseRunID, pulseModulePlanDriftReview)),
	}
}

// One blocking lifecycle step per module prevents review evidence and mutation
// from racing. The scheduler calls this in pulsemodules.ExecutionOrder:
// Architecture proposes against the clean baseline, Technical repairs concrete
// behavior, and Strategic evaluates outcomes.
func pulseLifecycleModuleReviewStep(pulseRunID, module string) pulseLifecycleStep {
	label, _, _ := pulseModuleReviewParts(module)
	return pulseLifecycleStep{label: label, query: fmt.Sprintf(`PULSE MODULE REVIEW. pulse_run_id=%q. This step owns ONLY module=%q. Earlier lifecycle steps have finished; later modules are not yours.
Read the durable Gate worklist. If this module is not due or already has a terminal result, do nothing and end this turn. Otherwise do the review yourself in this turn. You may use your own subagents for parallel reading or analysis, but you stay responsible for the result and only you record it.

%s`, pulseRunID, module, pulseReviewerInstruction(pulseRunID, module))}
}

// pulseModuleReviewParts is one module's review contract.
func pulseModuleReviewParts(module string) (label, reference, contract string) {
	label, reference, contract = "technical-review", "technical-review", "First close every open workflow issue in get_pulse_state(view=\"backlog\"): fix it, close it as not a problem with the check that shows it, ask the user through a decision with the exact change, or hand a platform defect off. Nothing stays open waiting: an old next_check that waits for a future run or more evidence is not a reason to wait (check whether the fix is already in place and close it, or fix it now), and a due or failed Plan Drift is not a reason to leave issues open. Then investigate new correctness failures and apply safe workflow-owned repairs. Do not perform a general optimization audit."
	switch module {
	case pulseModuleArchitectureReview:
		label, reference, contract = "architecture-review", "architecture-review", "Improve the construction of a working workflow, including evidence-based execution tier/model choices. Use actual quality, retries, cost and latency evidence; preserve explicit user pins and propose measured trials with a checkpoint and rollback through architecture decisions. Runtime does not change tiers from run counts. Research and propose bounded improvements. The one exception (PLAT-556, see architecture-review.md): with focus prompt_design, learning_quality or knowledgebase_design you may apply a pure text-moving consolidation yourself when check_plan_no_loss passes, then run the step once and keep it only if validation passes (else restore_step_from_changelog), and you may set a settled learnings_access=read-write step to read; record each with record_pulse_result consolidations. Behaviour, rule, output and topology changes stay owner proposals. If the worklist evidence carries architecture_scope_excludes, do not touch those steps: Plan Drift owns them this pass."
	case pulseModuleStrategicReview:
		label, reference, contract = "strategic-review", "strategy-auditor", "GOAL WORK: this is Pulse's main job. Do work that moves the user's goals, not only proposals. Read soul.md and get_goal_metrics early, follow up earlier Goal Work items (get_pulse_state view=goal_work), find what would move the primary metric that nobody is doing or the user does not know, and complete 1-3 bounded items now within the permission levels below, which the tools enforce for this turn (prepare under pulse/work/; run existing steps, act outward or change the workflow yourself only where that level is auto; where it is ask, put it to the user as a ready decision request). Record each with record_pulse_goal_work. Challenge soul.md constraints only with evidence through a keep/test/change decision; boundary constraints only get clarification; never break one meanwhile. This module is not blocked by a due Plan Drift; when Drift is due, Run and Change are held for this turn: prepare and research but do not run steps or edit the workflow."
	}
	return label, reference, contract
}

// pulseReviewerRecordRules tells a reviewer what to persist and how to
// write for the user.
const pulseReviewerRecordRules = `Use saved notes and typed records for interrupted work; read an old Markdown file only if a specific historical record points to it. Do not create or maintain mandatory Markdown checkpoints. Persist only canonical issues (and, for strategic_review, Goal Work items with record_pulse_goal_work), a human decision when genuinely required, and one terminal review result for this module. Put the expected benefit, baseline, guardrails and next outcome boundary in the issue or review summary rather than a separate proposal/impact lifecycle. Finish with one record_pulse_result using reason and optional review_note for new reasoning, limitations and next steps. No separate reporting turn or repeated history scan. Reuse existing records. Applied is not evidence of improved outcomes. The shared pass mode must not suppress another module's research.
The user reads issue concerns and summaries, Goal Work titles and actions, decision questions and your result reason. Write those in plain language: lead with what changed or what the user needs to do, short sentences, everyday words, no issue IDs, state names or code terms. Keep IDs, evidence and technical detail in review_note, impact, evidence and the Goal Work detail; the tools refuse user-facing text that carries them.
`

func pulseLifecycleReviewFixContinuationStep(pulseRunID string, receiptErr error) pulseLifecycleStep {
	return pulseLifecycleStep{
		label: "review-fix-continuation",
		query: fmt.Sprintf(`PULSE REVIEW + FIX RECEIPT CHECK. pulse_run_id=%q. Continue after all registered background sequences completed. The prior stage is missing receipts: %s. Load the Gate worklist, typed Pulse state, child status, and saved SQLite review notes. Do not reconstruct findings or fixes from truncated automatic-notification prose, and do not add a consolidation pass. Validate the receipts already persisted by each sequence. Resolve only a genuine cross-module ownership conflict using saved notes and typed rows. If a child ended before its final persistence turn, do not infer or invent its findings, restart it automatically, or mutate workflow artifacts. The scheduler records durable recovery for any module still missing its terminal result after this step. Keep the response compact, then stop.`, pulseRunID, receiptErr),
	}
}

// recordIncompletePulseReviewRecoveries turns a missing current-run receipt
// into a durable next-Gate obligation. It is safe to call after every
// review-fix outcome: modules with a terminal receipt are ignored.
func recordIncompletePulseReviewRecoveries(ctx context.Context, workspacePath, pulseRunID string, stepResult pulseLifecycleStepRunResult, modules ...string) ([]string, error) {
	worklist, ok, err := getPulseWorklistForRun(ctx, workspacePath, pulseRunID)
	if err != nil || !ok {
		if err != nil {
			return nil, fmt.Errorf("read Pulse worklist: %w", err)
		}
		return nil, fmt.Errorf("Pulse worklist %q is missing", pulseRunID)
	}
	reason := "review-fix ended before the module persisted a terminal result"
	if stepResult.err != nil {
		reason += ": " + stepResult.err.Error()
	} else if stepResult.outcome != pulseLifecycleStepCompleted {
		reason += fmt.Sprintf(" (outcome=%s)", stepResult.outcome)
	}
	reviewModules := []string{pulseModuleTechnicalReview, pulseModuleArchitectureReview, pulseModuleStrategicReview}
	var recovered []string
	for _, module := range reviewModules {
		if len(modules) > 0 && !slices.Contains(modules, module) {
			continue
		}
		state, exists := worklist[module]
		if !exists || !strings.EqualFold(strings.TrimSpace(state.LastDecision), "due") || strings.TrimSpace(state.LastResult) != "" {
			continue
		}
		if err := markPulseReviewRecovery(ctx, workspacePath, module, pulseRunID, "", reason); err != nil {
			return recovered, err
		}
		recovered = append(recovered, module)
	}
	return recovered, nil
}

func beginDuePulseReviewRecoveries(ctx context.Context, workspacePath, pulseRunID string, modules ...string) error {
	worklist, ok, err := getPulseWorklistForRun(ctx, workspacePath, pulseRunID)
	if err != nil || !ok {
		if err != nil {
			return err
		}
		return fmt.Errorf("Pulse worklist %q is missing", pulseRunID)
	}
	reviewModules := []string{pulseModuleTechnicalReview, pulseModuleArchitectureReview, pulseModuleStrategicReview}
	for _, module := range reviewModules {
		if len(modules) > 0 && !slices.Contains(modules, module) {
			continue
		}
		state, exists := worklist[module]
		if !exists || !strings.EqualFold(strings.TrimSpace(state.LastDecision), "due") || strings.TrimSpace(state.LastResult) != "" {
			continue
		}
		if err := beginPulseReviewRecovery(ctx, workspacePath, module, pulseRunID, ""); err != nil {
			return err
		}
	}
	return nil
}

type workflowNotificationContentInstructions struct {
	runSummary             string
	pulseSummary           string
	runSummaryChannels     []string
	pulseSummaryChannels   []string
	runSummaryRecipients   []string
	pulseSummaryRecipients []string
}

func notificationInstructionsFromCapabilities(capabilities WorkflowCapabilities) workflowNotificationContentInstructions {
	notifications := capabilities.Notifications
	return workflowNotificationContentInstructions{
		runSummary:             runSummaryInstructionsOrDefault(notifications),
		pulseSummary:           pulseSummaryInstructionsOrDefault(notifications),
		runSummaryChannels:     notificationChannels(notifications, true),
		pulseSummaryChannels:   notificationChannels(notifications, false),
		runSummaryRecipients:   notificationRecipients(notifications, true),
		pulseSummaryRecipients: notificationRecipients(notifications, false),
	}
}

func notificationChannels(config *WorkflowNotificationConfig, run bool) []string {
	if config == nil {
		return nil
	}
	if run {
		return append([]string(nil), config.RunSummaryChannels...)
	}
	return append([]string(nil), config.PulseSummaryChannels...)
}

func notificationRecipients(config *WorkflowNotificationConfig, run bool) []string {
	if config == nil {
		return nil
	}
	if run {
		return append([]string(nil), config.RunSummaryRecipients...)
	}
	return append([]string(nil), config.PulseSummaryRecipients...)
}

// pulseSafeRunFailureReason tells the finalizer why the workflow did not run
// without handing it the upgrade instruction a second time.
//
// The finalizer shares the scheduler's session. On confida-login 2026-08-12 it
// was told `did not stamp required version "1.0.21"` three seconds after the
// turn that owed that stamp had been adjudicated and closed — and ten minutes
// later something in that session stamped 1.0.21, which the next preflight
// trusted and skipped the migration for. The grant in pkg/contractupgrade is
// what makes the write impossible; keeping the instruction out of unrelated
// turns is what stops the attempt from being made and reported as a confusing
// refusal in the operator's run-summary email.
//
// The preflight label survives, because naming which migration stalled is real
// diagnostic value. The target version and the stamp verb do not.
//
// Scheduled migrations are now retired. This sanitizer remains only for old
// persisted run failures that can still be rendered by the no-run finalizer.
func pulseSafeRunFailureReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if !strings.HasPrefix(reason, "workflow upgrade preflight") {
		return reason
	}
	idx := strings.Index(reason, " did not stamp")
	if idx <= 0 {
		return reason
	}
	return reason[:idx] + " did not complete, so the scheduled workflow was not started." +
		" That migration belongs to its own upgrade turn: do not attempt it here and do not stamp a contract version from this turn."
}

// Keep Gmail rendering explicit in every finalizer prompt. Full Pulse also
// loads pulse-finalizer.md and notify_user advertises the same contract, but
// basic/no-run finalizers do not load that skill and an agent can otherwise
// satisfy the neutral summary fields while accidentally sending plain email.
const finalizerRichEmailInstruction = "\n\nRICH GMAIL OUTPUT. When Gmail is configured for this notification, the same notify_user call must include a non-empty plain-text email_subject and one compact, inline-styled email_html body. Keep message_for_user as the channel-neutral plain fallback; do not paste a browser report or a full HTML document into the email."

// pulseLifecycleNoRunSteps is the truthful finalizer for an invocation that
// never produced new run evidence — the workshop session ran but the workflow
// itself never started or restarted a run folder (e.g. a preflight abort
// against a pre-existing, untouched iteration-0). There is no run to review or
// render, so it skips Gate, Review+Fix, and dashboard, preserves any preflight
// edits through the normal backup contract, and tells the user why no result
// exists.
func pulseLifecycleNoRunSteps(pulseRunID, reason string, instructions ...workflowNotificationContentInstructions) []pulseLifecycleStep {
	ownerInstructions := workflowNotificationContentInstructions{}
	if len(instructions) > 0 {
		ownerInstructions = instructions[0]
	}
	reason = pulseSafeRunFailureReason(reason)
	if reason == "" {
		reason = "the scheduler recorded no error, but no workflow run was started or resumed during this invocation"
	}
	routing := ""
	if len(ownerInstructions.runSummaryChannels) > 0 {
		routing += fmt.Sprintf(" Configured run-summary channels: %s; the backend enforces them from notification_kind.", notificationChannelSummary(ownerInstructions.runSummaryChannels))
	}
	if len(ownerInstructions.runSummaryRecipients) > 0 {
		routing += fmt.Sprintf(" The backend addresses email from the workflow's saved run-summary recipients (%s); do not pass email_to.", notificationRecipientSummary(ownerInstructions.runSummaryRecipients))
	}
	content := ""
	if runInstructions := strings.TrimSpace(ownerInstructions.runSummary); runInstructions != "" {
		content = "\n\nApply these saved run-summary content instructions without changing the facts:\n" + runInstructions
	}
	return []pulseLifecycleStep{{label: "finalize", query: fmt.Sprintf(
		"PULSE FINALIZER — WORKFLOW DID NOT RUN. pulse_run_id=%q. The scheduled workflow never started in this invocation, so there is no new run evidence. Gate, reviewers, Fixer, dashboard, and publish were intentionally skipped. Do not run them, do not read old evidence as this run, do not write builder/improve.html, and do not invent an outcome.\n\n"+
			"Do these actions in order and record every command with record_pulse_result(command=..., result=..., reason=...): dashboard has no record_pulse_result command and needs no receipt — it is already intentionally skipped by not being rendered. (1) run the configured source-hash-gated backup and record its truthful terminal result; (2) mark publish skipped because nothing was produced; (3) call notify_user exactly once with notification_kind=\"run_summary\" and plainly say the workflow did not start, no results were produced, and the next schedule will retry unless the cause is fixed. Set summary_status=\"no_run\"; include title, compact facts, and sections. Then record notify truthfully.%s%s\n\nThe scheduler's reason is:\n%s%s",
		pulseRunID, routing, finalizerRichEmailInstruction, reason, content,
	)}}
}

func pulseLifecycleFinalSteps(pulseRunID string, instructions ...workflowNotificationContentInstructions) []pulseLifecycleStep {
	ownerInstructions := workflowNotificationContentInstructions{}
	if len(instructions) > 0 {
		ownerInstructions = instructions[0]
	}
	notificationContext := ""
	if runInstructions := strings.TrimSpace(ownerInstructions.runSummary); runInstructions != "" {
		notificationContext += "\n\nWORKFLOW RUN SUMMARY INSTRUCTIONS. Apply these only to the section describing what happened in the workflow execution:\n" + runInstructions
	}
	if pulseInstructions := strings.TrimSpace(ownerInstructions.pulseSummary); pulseInstructions != "" {
		notificationContext += "\n\nPULSE REVIEW SUMMARY INSTRUCTIONS. Apply these only to the section describing what Pulse reviewed, fixed, recommended, or needs from the user:\n" + pulseInstructions
	}
	if len(ownerInstructions.runSummaryChannels) > 0 || len(ownerInstructions.pulseSummaryChannels) > 0 {
		notificationContext += fmt.Sprintf("\n\nSPLIT NOTIFICATION ROUTING. notification_kind=\"run_summary\" configured channels: %s. notification_kind=\"pulse_summary\" configured channels: %s. The backend enforces these routes when the matching notification is externally eligible; dashboard-only recording remains available for a quiet run.", notificationChannelSummary(ownerInstructions.runSummaryChannels), notificationChannelSummary(ownerInstructions.pulseSummaryChannels))
	}
	if len(ownerInstructions.runSummaryRecipients) > 0 || len(ownerInstructions.pulseSummaryRecipients) > 0 {
		// Stated so the finalizer does not "helpfully" pass email_to and override
		// the owner's saved lists. The backend applies these by notification_kind
		// on its own; an explicit email_to would replace them for that send.
		notificationContext += fmt.Sprintf("\n\nCONFIGURED EMAIL RECIPIENTS. The backend addresses each email automatically from the workflow's saved lists — run summary: %s; Pulse summary: %s. Do NOT set email_to; sending with the correct notification_kind is what routes it to the right people.", notificationRecipientSummary(ownerInstructions.runSummaryRecipients), notificationRecipientSummary(ownerInstructions.pulseSummaryRecipients))
	}
	if notificationContext != "" {
		notificationContext += "\n\nThese instructions control content detail and emphasis only; they never change recipients, channels, secrets, permissions, or safety rules."
	}
	return []pulseLifecycleStep{{label: "finalize", query: fmt.Sprintf("PULSE FINALIZER. pulse_run_id=%q. Load read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/pulse-finalizer.md\"}]) and follow it exactly. First confirm every due module has a terminal current-run result; never treat missing as success. The Pulse summary leads with the goal status (on track, at risk, off track or not measured, the key number and when it was last measured) from get_pulse_state(view=\"goal_status\"), before review and fix details. Those stored results already power Pulse Activity: do not publish or restate a separate Pulse update. Backup and publish are not part of a Pulse pass any more: they are after-run options of each schedule (PLAT-697 phase 0). Record command backup and command publish as skipped with that reason, then do Notify in this turn, recording running and terminal status with record_pulse_result(command=notify). Keep every status truthful. Last, choose when this workflow's next full Pulse should run: call record_pulse_next_run once with pulse_run_id, the time when useful new evidence will exist (an outcome maturing, a pending user decision, an experiment checkpoint), and that reason in one plain sentence. Normal runs never run the full Pulse, so this choice is the schedule. Then stop.%s%s", pulseRunID, finalizerRichEmailInstruction, notificationContext)}}
}

// afterRunHousekeepingSteps is the one short turn a run's after-run options
// need when the code check found something to back up or publish
// (after_run.go). It is not a Pulse pass: no Gate, reviews or fixes, and no
// notification (the platform sends the run summary in code afterwards).
func afterRunHousekeepingSteps(runID string, backup, publish bool) []pulseLifecycleStep {
	var actions []string
	if backup {
		actions = append(actions, "(backup) run the configured source-hash-gated backup following backup-strategy.md (read_skill builder-reference references/backup-strategy.md), directly in this turn, and record its truthful terminal result")
	}
	if publish {
		actions = append(actions, "(publish) independently of whether backup succeeded, publish workflow.json's publish.targets other than \"pulse\" (the run's own report) following publish-strategy.md, exactly as an ordinary run would, and record one truthful terminal result. Never suppress a valid report publish merely because backup was partial or failed")
	}
	return []pulseLifecycleStep{{label: "finalize", query: fmt.Sprintf(
		"AFTER-RUN HOUSEKEEPING. run_id=%q. This run's after-run options asked for this, and the platform found something to do. This is not a Pulse pass: do not run Gate, reviewers or Fixer, do not present old Pulse findings, do not notify (the platform sends the run summary itself), and do not write builder/improve.html.\n\n"+
			"Do only these, in order, recording each with record_pulse_result(command=..., result=..., reason=...) as running and then terminal: %s. The other final commands are already closed. Then stop.",
		runID, strings.Join(actions, "; "),
	)}}
}

func notificationChannelSummary(channels []string) string {
	if len(channels) == 0 {
		return "all enabled channels (legacy default)"
	}
	return strings.Join(channels, ", ")
}

// notificationRecipientSummary describes a saved recipient list for the Pulse
// finalizer prompt. An unset list is spelled out as the account default rather
// than left blank, so the finalizer does not read it as "nobody".
func notificationRecipientSummary(recipients []string) string {
	if len(recipients) == 0 {
		return "the account default recipient"
	}
	return strings.Join(recipients, ", ")
}

func validatePulseDueModuleResults(ctx context.Context, workspacePath, pulseRunID string) error {
	worklist, ok, err := getPulseWorklistForRun(ctx, workspacePath, pulseRunID)
	if err != nil {
		return fmt.Errorf("read Pulse worklist: %w", err)
	}
	if !ok {
		return fmt.Errorf("Pulse worklist %q is missing", pulseRunID)
	}
	var unresolved []string
	for _, module := range pulseModuleOrder {
		state, exists := worklist[module]
		if !exists || strings.TrimSpace(strings.ToLower(state.LastDecision)) != "due" {
			continue
		}
		if strings.TrimSpace(state.LastResult) == "" {
			unresolved = append(unresolved, module)
		}
	}
	if len(unresolved) > 0 {
		return fmt.Errorf("due Pulse modules lack terminal current-run results: %s", strings.Join(unresolved, ", "))
	}
	return nil
}

// validatePulseDueModuleResultsFor is validatePulseDueModuleResults scoped to
// specific modules, needed once plan_drift_review became its own preceding
// lifecycle step: checking the full pulseModuleOrder right after that step
// would wrongly flag technical_review/strategic_review as missing a receipt
// when they simply have not had their turn yet (it runs next, not before).
func validatePulseDueModuleResultsFor(ctx context.Context, workspacePath, pulseRunID string, modules ...string) error {
	worklist, ok, err := getPulseWorklistForRun(ctx, workspacePath, pulseRunID)
	if err != nil {
		return fmt.Errorf("read Pulse worklist: %w", err)
	}
	if !ok {
		return fmt.Errorf("Pulse worklist %q is missing", pulseRunID)
	}
	var unresolved []string
	for _, module := range modules {
		state, exists := worklist[module]
		if !exists || strings.TrimSpace(strings.ToLower(state.LastDecision)) != "due" {
			continue
		}
		if strings.TrimSpace(state.LastResult) == "" {
			unresolved = append(unresolved, module)
		}
	}
	if len(unresolved) > 0 {
		return fmt.Errorf("due Pulse modules lack terminal current-run results: %s", strings.Join(unresolved, ", "))
	}
	return nil
}

func pulseWorklistHasDueModule(ctx context.Context, workspacePath, pulseRunID string) (bool, error) {
	worklist, ok, err := getPulseWorklistForRun(ctx, workspacePath, pulseRunID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("Pulse worklist %q is missing", pulseRunID)
	}
	for _, module := range pulseModuleOrder {
		state, exists := worklist[module]
		if exists && strings.TrimSpace(strings.ToLower(state.LastDecision)) == "due" {
			return true, nil
		}
	}
	return false, nil
}

// pulseWorklistModulesDue reports whether ANY of the given specific modules
// is marked due in the durable worklist — unlike pulseWorklistHasDueModule
// (any module at all), this lets the caller ask a narrower question, needed
// once plan_drift_review and technical_review/strategic_review became
// separately-sequenced lifecycle steps (each only needs to know about its
// own module set, not "is anything due at all").
func pulseWorklistModulesDue(ctx context.Context, workspacePath, pulseRunID string, modules ...string) (bool, error) {
	worklist, ok, err := getPulseWorklistForRun(ctx, workspacePath, pulseRunID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("Pulse worklist %q is missing", pulseRunID)
	}
	for _, module := range modules {
		state, exists := worklist[module]
		if exists && strings.TrimSpace(strings.ToLower(state.LastDecision)) == "due" {
			return true, nil
		}
	}
	return false, nil
}

func validatePulseTechnicalRepairDrain(ctx context.Context, workspacePath, pulseRunID string) error {
	due, err := pulseWorklistModulesDue(ctx, workspacePath, pulseRunID, pulseModuleTechnicalReview)
	if err != nil {
		return fmt.Errorf("read technical review completion contract: %w", err)
	}
	if !due {
		return nil
	}
	// Only the work this pass owns: issues routed to the fixer and issues seen
	// since the previous Pulse. A large historical backlog no longer fails
	// every Technical pass (social-media: 87 stale rows).
	remaining, err := stepworkflow.CountPulseActionableWorkflowIssuesForPass(ctx, workspacePath, stepConcernWindowStart(ctx, workspacePath))
	if err != nil {
		return fmt.Errorf("read actionable Pulse repair backlog: %w", err)
	}
	if remaining > 0 {
		// Name them: an unnamed count let a pass leave the same issue
		// untouched run after run (upwork PUL-2B0668E4, filed by Goal Work,
		// skipped as "strategy-owned" by 4 fix runs in one night).
		left := ""
		if issues, listErr := stepworkflow.ListPulseActionableWorkflowIssues(ctx, workspacePath); listErr == nil && len(issues) > 0 {
			ids := make([]string, 0, len(issues))
			for i, issue := range issues {
				if i == 10 {
					ids = append(ids, fmt.Sprintf("and %d more", len(issues)-10))
					break
				}
				ids = append(ids, issue.ID)
			}
			left = " (" + strings.Join(ids, ", ") + ")"
		}
		return fmt.Errorf("Review+Fix left %d actionable workflow-owned Pulse issue(s)%s without a recorded outcome; the repair drain is incomplete. Every one is this pass's, whichever review filed it: fix and verify it, reject it with the reason, or turn it into a decision for the user, and record that with record_pulse_result", remaining, left)
	}
	return nil
}

func compactScheduleMessages(messages []string) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		if strings.TrimSpace(msg) == "" {
			continue
		}
		out = append(out, msg)
	}
	return out
}

type scheduledWorkshopTurn struct {
	label         string
	query         string
	upgradeTarget string
	// decisionDrain marks the pre-run turn that applies operator decisions the
	// user already answered. It runs on the Pulse LLM like an upgrade turn, but
	// unlike one it is never allowed to fail the run — see the loop in
	// executeWorkshopJob.
	decisionDrain bool
	// failureBlocksRun is set only by an approved decision contract that names
	// a safety/public-action boundary. Ordinary failed repairs keep the old safe
	// plan and do not cancel the scheduled run.
	failureBlocksRun bool
}

// workshopMode returns the workflow phase for a scheduler-owned conversation.
// All writable scheduled turns use Workshop because the same session owns
// migrations, approved decisions, and execution. Read-only access is pinned to
// Run later by the server's workflow access policy.
func (turn scheduledWorkshopTurn) workshopMode() string {
	return "workshop"
}

func scheduledDecisionApplyMode(input ReportHumanInput) string {
	// Owner acceptance of a user suggestion is review, not unattended edit authority.
	if input.Source == "user_suggestion" {
		return "legacy_manual"
	}
	mode := strings.ToLower(strings.TrimSpace(input.ApplyContract.Mode))
	switch mode {
	case "no_change", "direct_apply", "targeted_fixer", "external_wait":
		return mode
	default:
		return "legacy_manual"
	}
}

func scheduledWorkshopMessages(sctx *ScheduleContext) []string {
	if sctx == nil {
		return nil
	}
	messages := compactScheduleMessages(sctx.Schedule.Messages)
	if sctx.PulseOnly {
		return messages
	}

	// A saved route is executable configuration, not optional prose.  Older
	// schedules could carry both route_selections and a follow-up message such
	// as "after the selected work completes...".  Returning only that message
	// silently discarded the selected route and left the builder to guess what
	// (if anything) it should run.  Always put the canonical route turn first;
	// retained schedule-specific messages are follow-ups, never replacements.
	if len(sctx.Schedule.RouteSelections) > 0 {
		groups, _ := json.Marshal(sctx.Schedule.GroupNames)
		routes, err := json.Marshal(sctx.Schedule.RouteSelections)
		instruction := fmt.Sprintf("Run the full workflow once for each configured schedule group %s using run_full_workflow.", string(groups))
		if err == nil {
			instruction = fmt.Sprintf("Run the full workflow once for each configured schedule group %s using run_full_workflow with route_selections=%s. Do not substitute a schedule-local procedure for the selected plan route.", string(groups), string(routes))
		}
		return append([]string{instruction + " " + scheduledBackgroundNoPollingInstruction}, messages...)
	}
	if len(messages) == 0 {
		groups, _ := json.Marshal(sctx.Schedule.GroupNames)
		instruction := fmt.Sprintf("Run the full workflow once for each configured schedule group %s using run_full_workflow.", string(groups))
		return []string{instruction + " " + scheduledBackgroundNoPollingInstruction}
	}
	if mode := strings.TrimSpace(sctx.Schedule.ExecutionMode); mode != "" {
		prefix := fmt.Sprintf("This invocation has backend-enforced execution_mode=%q; do not weaken or reinterpret it. ", mode)
		for i := range messages {
			messages[i] = prefix + messages[i]
		}
	}
	if sctx.QueuedOccurrenceCount > 1 {
		prefix := fmt.Sprintf("This catch-up invocation coalesces %d scheduled occurrences into one run; do not replay them separately. ", sctx.QueuedOccurrenceCount)
		for i := range messages {
			messages[i] = prefix + messages[i]
		}
	}
	return messages
}

// executeJob builds a session request from the manifest and runs it.
// Returns (sessionID, runFolder, error).
func (s *SchedulerService) executeJob(ctx context.Context, sctx *ScheduleContext, runID string) (string, string, error) {
	if sctx.WebhookInput != nil {
		return s.executeWebhookJob(ctx, sctx, runID)
	}
	if mode := strings.TrimSpace(sctx.Schedule.Mode); mode != "" && mode != "workshop" {
		s.logf(sctx, "[SCHEDULER] Schedule %s uses legacy mode=%s; executing through workshop mode", sctx.Schedule.ID, mode)
	}
	return s.executeWorkshopJob(ctx, sctx, runID)
}

// executeWorkshopJob runs a workflow via the workshop builder path (workflow_phase mode).
func (s *SchedulerService) executeWorkshopJob(ctx context.Context, sctx *ScheduleContext, runID string) (string, string, error) {
	messages := scheduledWorkshopMessages(sctx)
	if sctx.WebhookInput != nil {
		messages[0] += " " + webhookRunInputInstruction(webhookInputPath(sctx.WorkspacePath, sctx.WebhookInput.RunID)) + " This is an isolated webhook invocation. Execute only the saved workflow binding. Do not run Pulse, reviewers, post-run backup, publish, or maintenance. Stop after reporting the run result."
	}
	runFolder := "iteration-0"
	if strings.TrimSpace(sctx.ScheduledRunFolder) != "" {
		runFolder = strings.TrimSpace(sctx.ScheduledRunFolder)
	}
	if sctx.WebhookInput != nil {
		var allocationErr error
		runFolder, allocationErr = allocateWebhookRunFolder(sctx.WorkspacePath, runID)
		if allocationErr != nil {
			return "", "", allocationErr
		}
	}
	if sctx.PulseOnly && strings.TrimSpace(sctx.PulseEvidenceRunFolder) != "" {
		runFolder = strings.TrimSpace(sctx.PulseEvidenceRunFolder)
	}

	// Snapshot existing run folders before this invocation. A producing saved
	// schedule is already bound to its immutable iteration-N-sched folder; an
	// interactive Builder run uses iteration-0 and a webhook is allocated an
	// iteration-N-hook folder. This snapshot is retained for legacy metadata and
	// exact-folder outcome reconciliation.
	preRunFolders, preRunFoldersErr := s.api.loadRunFoldersInternal(ctx, sctx.WorkspacePath)
	preRunFolders = webhookInvocationFolders(preRunFolders, runFolder, sctx.WebhookInput != nil)
	preRunFolders = scheduleInvocationFolders(preRunFolders, runFolder, sctx.ScheduledRunFolder != "")
	preRunFolderNames := runFolderNameSet(preRunFolders)
	// A failed snapshot is not an empty workspace. Without this distinction the
	// reconciler below compares against an empty baseline, concludes every
	// pre-existing folder is new, and attributes an old failed run to this
	// invocation. The listing is most likely to fail right after a server
	// restart, while the workspace API is still warming up — exactly when
	// scheduled runs resume.
	runFolderBaselineKnown := preRunFoldersErr == nil
	invocationStartedAt := time.Now().UTC()
	sctx.ProducedRunEvidence = false

	sessionID := s.newScheduleSessionID(sctx)
	if sctx.WebhookInput != nil && sctx.Schedule.IsFunctionTrigger() && strings.HasPrefix(sctx.WebhookInput.DeliveryID, "fn-") {
		ctx = virtualtools.WithFeedbackOperation(ctx, sctx.WebhookInput.DeliveryID)
	}
	// Mark the complete scheduled-session lifetime in server-owned state so a
	// late contract migration cannot stamp the workflow after its owning
	// scheduled session has ended.
	contractupgrade.MarkScheduled(sessionID)
	defer contractupgrade.ClearScheduled(sessionID)
	if sctx.WebhookInput == nil && !sctx.PulseOnly && sctx.ScheduledRunFolder != "" {
		s.api.scheduleInvocations.Store(sessionID, &stepworkflow.ScheduleInvocation{
			RunID: runID, ScheduleID: sctx.Schedule.ID, RunFolder: sctx.ScheduledRunFolder,
			TriggerSource: sctx.TriggerSource, ScheduledFor: sctx.ScheduledFor,
			AllowParallel: scheduleAllowsParallel(sctx.Schedule),
		})
		defer s.api.scheduleInvocations.Delete(sessionID)
	}
	if sctx.WebhookInput != nil {
		s.api.webhookInvocations.Store(sessionID, &stepworkflow.WebhookInvocation{
			InputFile:       webhookInputPath(sctx.WorkspacePath, sctx.WebhookInput.RunID),
			RunFolder:       runFolder,
			Variables:       sctx.WebhookInput.Variables,
			RouteSelections: sctx.Schedule.RouteSelections, GroupNames: sctx.Schedule.GroupNames,
		})
		defer s.api.webhookInvocations.Delete(sessionID)
	}

	s.updateRuntimeState(scheduleRuntimeKey(sctx), func(state *ScheduleRuntimeState) {
		state.LastSessionID = sessionID
	})

	if runID != "" {
		_ = UpdateScheduleRun(ctx, sctx.WorkspacePath, runID, "running", "", nil, runFolder, sessionID)
	}

	s.sessionLogf(sctx, sessionID, "[SCHEDULER] Scheduled workflow: executing %d messages for %s (session=%s workspace=%s run_folder=%s pulse_only=%t)",
		len(messages), sctx.Schedule.ID, sessionID, sctx.WorkspacePath, runFolder, sctx.PulseOnly)

	baseReqMap := s.buildWorkshopRequest(ctx, sctx)
	if sctx.WebhookInput != nil {
		baseReqMap["execution_options"].(map[string]interface{})["selected_run_folder"] = runFolder
		s.stateStoreMu.RLock()
		store := s.stateStore
		s.stateStoreMu.RUnlock()
		if store == nil {
			return sessionID, runFolder, fmt.Errorf("webhook run store unavailable")
		}
		if err := store.AssignRunFolder(ctx, runID, runFolder); err != nil {
			return sessionID, runFolder, err
		}
	}

	// Scheduled runs deliberately keep executing the saved workflow contract.
	// Contract upgrades are operator-owned and remain blocked from this session
	// by the contractupgrade fence above, but a pending upgrade is not allowed to
	// interrupt an automation that was already running unattended.
	turns := make([]scheduledWorkshopTurn, 0, len(messages))
	for i, message := range messages {
		turns = append(turns, scheduledWorkshopTurn{
			label: fmt.Sprintf("schedule-message-%d", i+1),
			query: message,
		})
	}

	// Scheduled runs never apply operator decisions (owner decision
	// 2026-10-03, replacing the PLAT-093 pre-run drain). A decision is applied
	// in the workflow's Builder chat: in the turn that answers it, or from the
	// "Apply in chat" button Needs you shows for every answered decision that
	// is not applied yet.
	// Unanswered decisions are not part of a scheduled run at all (owner decision
	// 2026-10-05): no notice is added to the schedule's messages. They live in
	// Needs you and the Builder chat.

	// This run owns the session until its work is done: it alone hands step
	// results back to the agent (scheduled_turn_followups.go). Released when
	// this function returns, before the Pulse finalizer starts.
	releaseCompletions := s.api.claimSessionCompletions(sessionID)
	defer releaseCompletions()

	for i, turn := range turns {
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] Workshop turn %d/%d (%s): %q", i+1, len(turns), turn.label, turn.query)

		reqMap := requestWithWorkshopMode(baseReqMap, turn.workshopMode())
		reqMap["query"] = turn.query

		// Resume the workflow's latest thread (same CLI) on the first message
		// only — later messages already share this run's live session.
		if i == 0 {
			if resumed := s.maybeResumeLatestWorkflowThread(ctx, sctx, reqMap, sessionID); resumed != "" {
				s.sessionLogf(sctx, sessionID, "[SCHEDULER] Resuming latest workflow thread %s for schedule %s", resumed, sctx.Schedule.ID)
			}
		}

		turnStartedAt := time.Now().UTC()
		if err := s.api.startSessionInternal(ctx, reqMap, sessionID, sctx.OwnerUserID, nil); err != nil {
			// A non-blocking direct decision turn that cannot start must not cost
			// the operator the run itself. The decisions stay answered-and-
			// unapplied, exactly as before this turn existed, and the post-run
			// Pulse pass still sees them (PLAT-093). A targeted Fixer with a
			// block_run contract is intentionally not exempt.
			if turn.decisionDrain && !turn.failureBlocksRun {
				s.sessionLogf(sctx, sessionID, "[SCHEDULER] Pre-run decision drain could not start (continuing to the run): %v", err)
				continue
			}
			s.preserveRunEvidenceAfterFailedTurn(ctx, sctx, sessionID, invocationStartedAt)
			return sessionID, runFolder, fmt.Errorf("workshop turn %d/%d (%s) failed: %w", i+1, len(turns), turn.label, err)
		}
		// A turn can dispatch cleanly and still produce nothing: every LLM
		// attempt fails, the failure is recorded as events, the session status
		// stays "completed", and no error reaches here. Without this check the
		// run is recorded success — hetzner-ssh did exactly that on 2026-08-18,
		// failing both turns on quota_exhausted in 8.1 seconds and being filed
		// as a successful security audit.
		//
		// A non-blocking decision turn is exempt for the same reason its
		// dispatch failure is: it must not cost the operator the run itself
		// (PLAT-093). A targeted Fixer with block_run is intentionally checked.
		if !turn.decisionDrain || turn.failureBlocksRun {
			if failure := scheduledTurnFailure(s.api.eventStore, sessionID, turnStartedAt); failure != "" {
				s.preserveRunEvidenceAfterFailedTurn(ctx, sctx, sessionID, invocationStartedAt)
				return sessionID, runFolder, fmt.Errorf("workshop turn %d/%d (%s) produced no response: %s", i+1, len(turns), turn.label, failure)
			}
		}

		// First message of the workshop sequence — stamp schedule name on
		// the session for frontend tab labeling. Subsequent calls are
		// no-ops (helper guards against overwriting an existing Title).
		s.stampScheduleNameOnSession(sessionID, sctx)

		// The turn ending is not the work ending. Wait for every step this turn
		// started, hand the agent their results as its next turn, and repeat
		// until a turn starts nothing new (e.g. all 12 groups).
		turnMode := turn.workshopMode()
		rounds, followErr := s.api.runScheduledFollowUps(ctx, sessionID, invocationStartedAt, schedulerWorkshopLiveChildCeiling, func(ctx context.Context, query string) error {
			followReq := requestWithWorkshopMode(baseReqMap, turnMode)
			followReq["query"] = query
			followStartedAt := time.Now().UTC()
			if err := s.api.startSessionInternal(ctx, followReq, sessionID, sctx.OwnerUserID, nil); err != nil {
				return err
			}
			if failure := scheduledTurnFailure(s.api.eventStore, sessionID, followStartedAt); failure != "" {
				return fmt.Errorf("step-result turn produced no response: %s", failure)
			}
			return nil
		})
		if rounds > 0 {
			s.sessionLogf(sctx, sessionID, "[SCHEDULER] Workshop turn %d/%d (%s): handed step results back %d time(s)", i+1, len(turns), turn.label, rounds)
		}
		if followErr != nil {
			if !turn.decisionDrain || turn.failureBlocksRun {
				s.preserveRunEvidenceAfterFailedTurn(ctx, sctx, sessionID, invocationStartedAt)
				return sessionID, runFolder, fmt.Errorf("workshop turn %d/%d (%s) step results: %w", i+1, len(turns), turn.label, followErr)
			}
			s.sessionLogf(sctx, sessionID, "[SCHEDULER] Pre-run decision drain step results failed (continuing to the run): %v", followErr)
		}

		s.sessionLogf(sctx, sessionID, "[SCHEDULER] Workshop turn %d/%d (%s) completed", i+1, len(turns), turn.label)
	}

	// Note: backup-on-completion is not appended here as a message turn. Backup is
	// owned by two arms that share one source-hash-gated contract: the Pulse pass
	// (runPulseLifecycle, final step) for dedicated Pulse runs, and the
	// interactive-run completion directive for interactive runs (and as the fallback
	// when Pulse is off). The shared source-hash
	// gate means whichever arm runs second sees the state already backed up and skips
	// the push — so the overlap can't double-back up.

	// Previously auto-generated a static markdown report here via the report agent.
	// The dynamic report (design doc §2) is a live frontend view over db/ + graph.json;
	// there is no post-run artifact to produce, so scheduled runs now finish without a
	// report side-effect. Users open the report in the UI whenever they want.

	// The workshop chat session finishing its scheduled turns without an
	// infrastructure-level error is not proof the triggered workflow run
	// succeeded — the LLM's run_full_workflow tool call can fail deep inside
	// (e.g. an orchestrator agent-creation error) while the chat session
	// itself still goes idle normally. Reconcile against the run(s) actually
	// created during this invocation, per their own run_metadata.json — the
	// record the execution machinery writes independently of the scheduler
	// (BUG-20260729-10, social-media 2026-07-29: the scheduler recorded
	// "success" for a run that fully failed at its first posting step).
	postRunFolders, postRunFoldersErr := s.api.loadRunFoldersInternal(ctx, sctx.WorkspacePath)
	// The listing fails when the workspace service is briefly down (restarts,
	// shutdown). Retry before judging, so a transient outage neither passes a
	// run unchecked nor fails a healthy one.
	for attempt := 1; postRunFoldersErr != nil && attempt <= 3 && ctx.Err() == nil; attempt++ {
		time.Sleep(time.Duration(attempt*2) * time.Second)
		postRunFolders, postRunFoldersErr = s.api.loadRunFoldersInternal(ctx, sctx.WorkspacePath)
	}
	postRunFolders = webhookInvocationFolders(postRunFolders, runFolder, sctx.WebhookInput != nil)
	postRunFolders = scheduleInvocationFolders(postRunFolders, runFolder, sctx.ScheduledRunFolder != "")
	sctx.ProducedRunEvidence = workshopRunProducedEvidence(preRunFolderNames, postRunFolders, invocationStartedAt)
	if !sctx.ProducedRunEvidence && s.scheduledWorkflowExecutionProducedEvidence(sessionID, invocationStartedAt) {
		sctx.ProducedRunEvidence = true
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] the schedule's linked workflow execution receipt is this invocation's authoritative Pulse evidence")
	}
	if !sctx.ProducedRunEvidence {
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] no run folder was created or restarted during this invocation for %s; Pulse evidence-dependent stages will be skipped", sctx.Schedule.ID)
		// A plain workflow schedule exists to run the workflow; finishing its
		// turns without starting a run is a failure, not a success (for example
		// the agent refused or misread the scheduled message). Schedules with
		// their own messages, a query or an execution mode may legitimately run
		// no workflow; their runs record ran_workflow=false for Pulse to judge.
		if scheduleExpectsWorkflowRun(sctx) && postRunFoldersErr == nil && runFolderBaselineKnown {
			return sessionID, runFolder, fmt.Errorf("the scheduled workflow did not start: the session finished without creating or restarting a run folder")
		}
	}
	// Reconciliation is a set difference, so it is only meaningful when both
	// sides are real. When either listing failed, skip it and say so rather than
	// reconcile against a baseline we do not have: this check exists to catch a
	// run that failed while its session looked healthy, and inventing a failure
	// from a missing snapshot is a worse error than missing one. Failing open is
	// safe here — the run's own metadata remains the durable record, and the
	// next invocation reconciles normally once the listing recovers.
	if !runFolderBaselineKnown || postRunFoldersErr != nil {
		// Without both listings the outcome cannot be checked. Recording
		// success here made runs that stopped when the workspace service went
		// down look complete (jobsearch finished 2 of 8 steps as "success").
		s.sessionLogf(sctx, sessionID,
			"[SCHEDULER] run outcome for %s could not be verified: run-folder listing unavailable (pre-run err=%v, post-run err=%v)",
			sctx.Schedule.ID, preRunFoldersErr, postRunFoldersErr)
		return sessionID, runFolder, fmt.Errorf("run outcome could not be verified: the run-folder listing was unavailable: %w", errors.Join(preRunFoldersErr, postRunFoldersErr))
	} else if failedFolder, found := reconcileWorkshopRunOutcome(preRunFolderNames, postRunFolders, invocationStartedAt); found {
		for _, folder := range postRunFolders {
			if folder.Name == failedFolder && folder.Metadata != nil && folder.Metadata.Recovery["status"] == "step_recovered_run_unverified" {
				return sessionID, runFolder, fmt.Errorf("workflow run %s remains failed: a step retry succeeded, but whole-run recovery is unverified (%v)", failedFolder, folder.Metadata.Recovery["reason"])
			}
		}
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] ⚠️ Workshop session for %s completed normally, but run %s recorded status \"failed\" in its own run_metadata.json", sctx.Schedule.ID, failedFolder)
		return sessionID, runFolder, fmt.Errorf("workflow run %s failed (its run_metadata.json records status \"failed\"), even though the orchestrating workshop session completed its turns without an infrastructure error", failedFolder)
	}

	s.sessionLogf(sctx, sessionID, "[SCHEDULER] ✅ Workshop execution completed for %s, session=%s, folder=%s", sctx.Schedule.ID, sessionID, runFolder)
	return sessionID, runFolder, nil
}

// scheduleExpectsWorkflowRun is true for a plain workflow schedule: no custom
// messages, no query and no execution mode, so its only job is to run the
// workflow. Pulse-only, webhook and manual Pulse invocations are excluded.
func scheduleExpectsWorkflowRun(sctx *ScheduleContext) bool {
	if sctx == nil || sctx.PulseOnly || sctx.WebhookInput != nil {
		return false
	}
	sched := sctx.Schedule
	return len(sched.Messages) == 0 && strings.TrimSpace(sched.Query) == "" && strings.TrimSpace(sched.ExecutionMode) == ""
}

func runFolderNameSet(folders []RunFolderInfo) map[string]bool {
	names := make(map[string]bool, len(folders))
	for _, folder := range folders {
		if folder.Name != "" {
			names[folder.Name] = true
		}
	}
	return names
}

// preserveRunEvidenceAfterFailedTurn consults the durable record before a failed
// turn returns, so Pulse is never told "the workflow did not run" merely because
// the session stopped progressing.
//
// This is PLAT-071 restored. It shipped 2026-08-10 in the dedicated
// waitForWorkshopIdle failure branch, was extended by PLAT-084 the next day, and
// was dropped whole on 2026-08-13 when that branch was replaced by
// waitForConversationTurnTree — leaving workshopRunStartedDuringInvocation as
// dead code and both its unit tests still green, because they call that helper
// directly instead of driving this path. social-media then lost a second run to
// the identical bug on 2026-08-16: a post landed and was verified, the idle wait
// expired anyway, and the operator was emailed that nothing had run.
//
// It sits on the generic turn-failure return rather than an idle-wait-specific
// branch, which widens it deliberately: "did a run actually start?" is answered
// by the run's own metadata regardless of how the session died. Widening is safe
// because workshopRunStartedDuringInvocation demands a real timestamp at or after
// the invocation began and counts an unreadable record as nothing, so no failure
// mode here can manufacture evidence that does not exist.
func (s *SchedulerService) preserveRunEvidenceAfterFailedTurn(ctx context.Context, sctx *ScheduleContext, sessionID string, since time.Time) {
	if s == nil || s.api == nil || sctx == nil || sctx.ProducedRunEvidence {
		return
	}
	// Deliberately the baseline-free check: the pre-run snapshot can itself be
	// lost (PLAT-070), and an empty baseline would make every folder look new
	// and answer "evidence" unconditionally — the opposite error.
	if folders, listErr := s.api.loadRunFoldersInternal(ctx, sctx.WorkspacePath); listErr == nil && sctx.WebhookInput == nil {
		folders = webhookInvocationFolders(folders, "iteration-0", false)
		folders = scheduleInvocationFolders(folders, sctx.ScheduledRunFolder, sctx.ScheduledRunFolder != "")
		if workshopRunStartedDuringInvocation(folders, since) {
			sctx.ProducedRunEvidence = true
			s.sessionLogf(sctx, sessionID, "[SCHEDULER] turn failed for %s, but a full workflow run started during this invocation and its own metadata is the authority; preserving run evidence for Pulse", sctx.Schedule.ID)
			return
		}
	}
	if s.scheduledWorkflowExecutionProducedEvidence(sessionID, since) {
		sctx.ProducedRunEvidence = true
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] turn failed for %s, but a linked workflow execution started during this invocation; preserving run evidence for Pulse", sctx.Schedule.ID)
	}
}

// workshopRunStartedDuringInvocation reports whether any run's own metadata says
// it started during this invocation, using only the durable record.
//
// Deliberately takes no before-set. workshopRunProducedEvidence answers "is this
// folder new?" first, which is correct when it holds a real baseline but inverts
// when it does not: an empty baseline (a failed listing — PLAT-070) makes the
// very first folder look new and returns true unconditionally. On the idle-wait
// failure path the question is narrower and has a trustworthy answer that needs
// no baseline at all — did a run actually start? — so ask only that.
//
// A run whose metadata carries no usable timestamp is not counted. The caller
// treats a false here as "no evidence recorded", which is the pre-existing
// behavior, so an unreadable record never manufactures evidence.
func workshopRunStartedDuringInvocation(after []RunFolderInfo, since time.Time) bool {
	for _, folder := range after {
		if folder.Name == "" || folder.Metadata == nil {
			continue
		}
		for _, stamp := range []time.Time{folder.Metadata.StartedAt, folder.Metadata.CreatedAt} {
			if !stamp.IsZero() && !stamp.Before(since) {
				return true
			}
		}
	}
	return false
}

func workshopRunProducedEvidence(before map[string]bool, after []RunFolderInfo, since time.Time) bool {
	for _, folder := range after {
		if workshopRunStartedSince(before, folder, since) {
			return true
		}
	}
	return false
}

// Rotation changes folder names, not execution timestamps. Prefer StartedAt
// over CreatedAt so copying/rotating metadata cannot rejuvenate an old run.
// Name-only evidence is retained solely for legacy metadata without timestamps.
func workshopRunStartedSince(before map[string]bool, folder RunFolderInfo, since time.Time) bool {
	if folder.Name == "" {
		return false
	}
	if folder.Metadata != nil {
		if !folder.Metadata.StartedAt.IsZero() {
			return !folder.Metadata.StartedAt.Before(since)
		}
		if !folder.Metadata.CreatedAt.IsZero() {
			return !folder.Metadata.CreatedAt.Before(since)
		}
	}
	return !before[folder.Name]
}

// scheduledWorkflowExecutionProducedEvidence uses the execution receipt linked
// to this exact schedule session as the authoritative invocation boundary. This
// covers both run_full_workflow's full-run container and execute_step's declared
// workflow-step execution.
//
// The run-folder listing is intentionally only a secondary signal: it is capped
// for dashboard performance, so a workflow that reuses iteration-0/group after
// ten newer iterations exist can be absent even though its run_metadata.json was
// just completed. A generic background agent still does not count.
func (s *SchedulerService) scheduledWorkflowExecutionProducedEvidence(sessionID string, since time.Time) bool {
	if s == nil || s.api == nil || s.api.bgAgentRegistry == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	for _, agent := range s.api.bgAgentRegistry.GetAll(sessionID) {
		if agent == nil {
			continue
		}
		snapshot := agent.GetSnapshot()
		if snapshot.CreatedAt.Before(since) {
			continue
		}
		kind := orchestratorevents.ExecutionKind(snapshot.Kind)
		if kind == orchestratorevents.ExecutionKindFullRun ||
			kind == orchestratorevents.ExecutionKindWorkflowStep ||
			snapshot.Metadata["execution_type"] == "full-workflow" ||
			snapshot.Metadata["execution_type"] == "workflow-step" {
			return true
		}
	}
	return false
}

// reconcileWorkshopRunOutcome finds explicit failures started in the invocation
// window. Timestamps take precedence over names because iteration rotation
// moves historical runs into newly named folders. Legacy timestamp-less records
// still use the before-set as a best-effort fallback.
//
// The since-based fallback matters because a workflow that reuses the same
// run-folder name every cycle (e.g. iteration-0/<group>, confirmed live on
// confida-login — see PLAT-182) never appears "new" by name after its first
// cycle: before[folder.Name] is already true every time, so a name-only check
// would skip inspecting that folder's metadata on every single subsequent
// invocation, regardless of what it actually recorded. workshopRunProducedEvidence,
// called moments earlier in the same code path for a different purpose, already
// carries this same since-based fallback for exactly this reason; this function
// previously lacked it.
//
// Ambiguous states — no metadata, "running", "completed", anything else — are
// still never treated as failure; only an explicit "failed" is, so a
// transient listing hiccup, or a workflow genuinely still running in the
// background, fails open toward "cannot verify" rather than toward a false
// failure.
func reconcileWorkshopRunOutcome(before map[string]bool, after []RunFolderInfo, since time.Time) (failedFolder string, found bool) {
	for _, folder := range after {
		if folder.Name == "" || folder.Metadata == nil {
			continue
		}
		if !workshopRunStartedSince(before, folder, since) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(folder.Metadata.Status), "failed") {
			return folder.Name, true
		}
	}
	return "", false
}

// maxWorkflowResumeScan bounds how many of this schedule's most recent runs the
// scheduler inspects when resume_previous=true: if a same-CLI resumable scheduled
// chat is among the latest few, resume it; otherwise start a fresh session.
const maxWorkflowResumeScan = 5

// maybeResumeLatestWorkflowThread wires restored_conversation_session_id into reqMap
// so an opt-in scheduled workflow run continues the schedule's most recent
// scheduled chat with the same access-derived authority instead of starting fresh.
//
// We look at schedule-runs.json for this exact schedule_id first, then validate
// the referenced chat runtime. This deliberately excludes normal user/builder
// chats in the same workflow workspace. A different CLI's external session ID
// is meaningless to the new one, and crossing the Workshop/Run boundary would
// retain the wrong prompt/tool context. Prior run status (success/error) is
// intentionally ignored: resume happens regardless so the agent can recover
// from a failed run.
// Returns the resumed thread's session ID, or "" when the run should start fresh.
func (s *SchedulerService) maybeResumeLatestWorkflowThread(ctx context.Context, sctx *ScheduleContext, reqMap map[string]interface{}, currentSessionID string) string {
	if !sctx.Schedule.ShouldResumePrevious() {
		return ""
	}

	runs, err := listScheduleRunsForResume(ctx, sctx.WorkspacePath, sctx.Schedule.ID)
	if err != nil || len(runs) == 0 {
		return ""
	}
	return s.maybeResumeLatestScheduledThread(sctx, reqMap, currentSessionID, runs, sctx.WorkspacePath)
}

func (s *SchedulerService) maybeResumeLatestScheduledThread(sctx *ScheduleContext, reqMap map[string]interface{}, currentSessionID string, runs []ScheduleRunEntry, workspacePath string) string {
	currentProvider := ""
	if llmConfig := sctx.Capabilities.LLMConfig; llmConfig != nil {
		if llmConfig.BuilderLLM != nil {
			currentProvider = strings.TrimSpace(llmConfig.BuilderLLM.Provider)
		}
		if currentProvider == "" {
			currentProvider = strings.TrimSpace(llmConfig.Provider)
		}
		if currentProvider == "" {
			if builder, _, ok := workflowtypes.ResolveProviderProfileConfig(llmConfig); ok && builder != nil {
				currentProvider = strings.TrimSpace(builder.Provider)
			}
		}
	}
	if currentProvider == "" {
		return ""
	}
	currentWorkshopMode := "workshop"
	if execOpts, ok := reqMap["execution_options"].(map[string]interface{}); ok {
		if requested, ok := execOpts["workshop_mode"].(string); ok {
			currentWorkshopMode = normalizeChatHistoryWorkshopMode(requested)
		}
	}
	if currentWorkshopMode == "" {
		currentWorkshopMode = "workshop"
	}

	// Runs are newest-first. Within the latest maxWorkflowResumeScan scheduled
	// chats, resume the most recent one that is a resumable coding-agent thread
	// on the same CLI; skip any that don't qualify (e.g. an API-model thread).
	// If none qualify, start fresh.
	//
	// Validate via ReadChatHistoryRuntimeForSession(sessionID, workspace) — the
	// SAME resolver handleQuery uses when it later honors
	// restored_conversation_session_id — so what we match here is provably what
	// gets resumed.
	checked := 0
	for _, run := range runs {
		sessionID := strings.TrimSpace(run.SessionID)
		if sessionID == "" || sessionID == currentSessionID {
			continue
		}
		checked++
		if checked > maxWorkflowResumeScan {
			break
		}

		rt, ok, rErr := ReadChatHistoryRuntimeForSession("", sessionID, workspacePath)
		if rErr != nil || !ok || rt == nil {
			continue
		}
		// A coding-agent thread the CLI can resume: kind, matching CLI provider,
		// and a captured external session ID to hand to the CLI's --resume.
		if rt.Kind != "coding_agent" {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(rt.Provider), currentProvider) {
			continue
		}
		if !rt.ResumeSupported || strings.TrimSpace(rt.ExternalSessionID) == "" {
			continue
		}
		if !chatHistoryResumeModesCompatible(rt.WorkshopMode, currentWorkshopMode) {
			continue
		}
		reqMap["restored_conversation_session_id"] = sessionID
		return sessionID
	}
	return ""
}

func listScheduleRunsForResume(ctx context.Context, workspacePath, scheduleID string) ([]ScheduleRunEntry, error) {
	if localRuns, ok, err := readLocalScheduleRuns(workspacePath); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return filterScheduleRunsNewestFirst(localRuns, scheduleID), nil
	}
	runs, _, err := ListScheduleRuns(ctx, workspacePath, scheduleID, maxScheduleRuns, 0)
	return runs, err
}

func readLocalScheduleRuns(workspacePath string) ([]ScheduleRunEntry, bool, error) {
	localPath := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(scheduleRunsPath(workspacePath)))
	data, err := os.ReadFile(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, true, err
	}
	var runs []ScheduleRunEntry
	if err := json.Unmarshal(data, &runs); err != nil {
		return nil, true, err
	}
	return runs, true, nil
}

func filterScheduleRunsNewestFirst(runs []ScheduleRunEntry, scheduleID string) []ScheduleRunEntry {
	filtered := make([]ScheduleRunEntry, 0, len(runs))
	for _, run := range runs {
		if run.ScheduleID == scheduleID {
			filtered = append(filtered, run)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].StartedAt.After(filtered[j].StartedAt)
	})
	if len(filtered) > maxScheduleRuns {
		filtered = filtered[:maxScheduleRuns]
	}
	return filtered
}

// scheduleSessionTrigger preserves the webhook origin in active session metadata.
// automationTriggerLabel names what started an automation run, for the
// active-work list. Internal Crew/workflow links are named "Called by <caller>"
// already; other triggers get their kind in front of their name.
func automationTriggerLabel(triggeredBy, name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "Called by ") {
		return name
	}
	kind := "Schedule"
	if triggeredBy == "webhook" {
		kind = "Webhook"
	}
	if name == "" {
		return kind
	}
	return kind + ": " + name
}

func scheduleSessionTrigger(sctx *ScheduleContext) string {
	if sctx != nil && (sctx.Schedule.ScheduleType == "webhook" || sctx.WebhookInput != nil) {
		return "webhook"
	}
	return "cron"
}

// stampScheduleNameOnSession updates the tracked session with the
// schedule's display name and trigger source so the frontend reconnect
// path can identify this as a scheduled run and label the tab using the
// schedule name instead of falling back to the literal "Workflow".
// Safe to call after startSessionInternal returns — the session is
// already tracked by then.
func (s *SchedulerService) stampScheduleNameOnSession(sessionID string, sctx *ScheduleContext) {
	if sctx == nil || strings.TrimSpace(sctx.Schedule.Name) == "" {
		return
	}
	s.api.activeSessionsMux.Lock()
	defer s.api.activeSessionsMux.Unlock()
	if sess, ok := s.api.activeSessions[sessionID]; ok && sess != nil {
		if sess.Title == "" {
			sess.Title = sctx.Schedule.Name
		}
		if sess.TriggeredBy == "" {
			sess.TriggeredBy = scheduleSessionTrigger(sctx)
		}
	}
}

// applyLLMAndSecretsToReqMap adds LLM config, API keys, secrets, and trigger payload to a request map.
func (s *SchedulerService) applyLLMAndSecretsToReqMap(ctx context.Context, reqMap map[string]interface{}, sctx *ScheduleContext) {
	if sctx.Capabilities.SelectedGlobalSecretNames != nil {
		reqMap["selected_global_secrets"] = sctx.Capabilities.SelectedGlobalSecretNames
	}

	if sctx.Capabilities.LLMConfig != nil {
		llmCfg := sctx.Capabilities.LLMConfig
		builderLLM := llmCfg.BuilderLLM
		if builderLLM == nil {
			if resolved, _, ok := workflowtypes.ResolveProviderProfileConfig(llmCfg); ok {
				builderLLM = resolved
			}
		}
		provider := ""
		modelID := ""
		var options map[string]interface{}
		if builderLLM != nil {
			provider = strings.TrimSpace(builderLLM.Provider)
			modelID = strings.TrimSpace(builderLLM.ModelID)
			options = builderLLM.Options
		}
		llmConfigSource := ""
		if provider != "" && modelID != "" {
			primary := map[string]interface{}{
				"provider": provider,
				"model_id": modelID,
			}
			if len(options) > 0 {
				primary["options"] = options
			}
			llmConfig := map[string]interface{}{
				"primary": primary,
			}
			reqMap["llm_config"] = llmConfig
			if llmConfigSource != "" {
				reqMap["llm_config_source"] = llmConfigSource
			}
		}
	}
	// API keys are now handled by MergedProviderAPIKeys in buildWorkshopConfig

	if len(sctx.Schedule.TriggerPayload) > 0 {
		var overrides map[string]interface{}
		if err := json.Unmarshal(sctx.Schedule.TriggerPayload, &overrides); err == nil {
			for k, v := range overrides {
				reqMap[k] = v
			}
		}
	}
}

func cloneStringInterfaceMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// requestWithWorkshopMode clones the request and its nested execution options
// before setting a per-turn mode. A shallow clone would share execution_options
// with later turns and could accidentally leave a normal run with Workshop
// authority after a migration or decision preflight.
func requestWithWorkshopMode(base map[string]interface{}, mode string) map[string]interface{} {
	reqMap := cloneStringInterfaceMap(base)
	execOpts := map[string]interface{}{}
	if current, ok := reqMap["execution_options"].(map[string]interface{}); ok {
		execOpts = cloneStringInterfaceMap(current)
	}
	execOpts["workshop_mode"] = mode
	reqMap["execution_options"] = execOpts
	return reqMap
}

// markPulseLifecycleTurn tags a Pulse lifecycle turn (Gate, review dispatch,
// Finalize) and elevates that turn to Workshop mode. It keeps the workflow's
// Builder model. Reviews run in this turn on that model; pulse_llm no longer
// applies to them (a retained CLI cannot switch model mid-conversation).
func markPulseLifecycleTurn(reqMap map[string]interface{}) {
	if reqMap == nil {
		return
	}
	reqMap["pulse_lifecycle_turn"] = true
	execOpts := map[string]interface{}{}
	if current, ok := reqMap["execution_options"].(map[string]interface{}); ok {
		execOpts = cloneStringInterfaceMap(current)
	}
	execOpts["workshop_mode"] = "workshop"
	reqMap["execution_options"] = execOpts
}

// buildWorkshopRequest creates the base request map for scheduled workflow
// execution. Writable scheduled turns use Workshop; the server pins read-only
// workflow access to Run.
func (s *SchedulerService) buildWorkshopRequest(ctx context.Context, sctx *ScheduleContext) map[string]interface{} {
	reqMap := map[string]interface{}{
		"agent_mode":                  "workflow_phase",
		"phase_id":                    workflowtypes.WorkflowStatusWorkflowBuilder,
		"preset_query_id":             sctx.WorkflowID,
		"selected_folder":             sctx.WorkspacePath,
		"triggered_by":                scheduleSessionTrigger(sctx),
		"triggered_by_label":          automationTriggerLabel(scheduleSessionTrigger(sctx), sctx.Schedule.Name),
		"session_title":               sctx.Schedule.Name,
		"servers":                     sctx.Capabilities.SelectedServers,
		"selected_tools":              sctx.Capabilities.SelectedTools,
		"selected_skills":             sctx.Capabilities.SelectedSkills,
		"browser_mode":                sctx.Capabilities.BrowserMode,
		"use_code_execution_mode":     sctx.Capabilities.UseCodeExecutionMode,
		"disable_live_input_delivery": true,
		// Retain the coding CLI across consecutive turns with the same authority.
		"keep_native_session_alive": true,
	}
	if len(sctx.Capabilities.CDPPorts) > 0 {
		reqMap["cdp_ports"] = append([]int(nil), sctx.Capabilities.CDPPorts...)
	}
	if sctx.Capabilities.Notifications != nil {
		if secretName := strings.TrimSpace(sctx.Capabilities.Notifications.SlackWebhookSecretName); secretName != "" {
			reqMap["notification_slack_webhook_secret_name"] = secretName
		}
	}

	s.applyLLMAndSecretsToReqMap(ctx, reqMap, sctx)

	runFolder := "iteration-0"
	if strings.TrimSpace(sctx.ScheduledRunFolder) != "" {
		runFolder = strings.TrimSpace(sctx.ScheduledRunFolder)
	}
	execOpts := map[string]interface{}{
		"selected_run_folder": runFolder,
		"execution_strategy":  "start_from_beginning_no_human",
		// Scheduled turns act for the workflow owner and may need to migrate the
		// workflow or apply an approved decision before launching existing work.
		// Read-only access is pinned back to Run by the server policy.
		"workshop_mode": "workshop",
	}
	if sctx.CapacityResumeFromStep > 0 {
		// Resume exactly where the capacity wall stopped this run. Resume-from-step
		// cleans step N and everything after it, which is precisely the right
		// scope: step N never completed and no later step started, while steps
		// 1..N-1 completed and are preserved.
		execOpts["execution_strategy"] = stepworkflow.ExecutionStrategyResumeFromStepNoHuman
		execOpts["resume_from_step"] = sctx.CapacityResumeFromStep
		if sctx.CapacityResumeRunFolder != "" {
			execOpts["selected_run_folder"] = sctx.CapacityResumeRunFolder
		}
	}
	// Quota pacing, opt-in per workflow. The orchestrator cannot resolve
	// credentials, so the account identity is resolved here — where it already
	// is for the schedule-level gate — and passed down as a hash.
	if accountKey, threshold := s.quotaPacingForSchedule(ctx, sctx); accountKey != "" && threshold > 0 {
		execOpts["capacity_account_key"] = accountKey
		execOpts["pace_threshold_percent"] = threshold
	}
	if sctx.WorkflowKind != "relay" && len(sctx.Schedule.GroupNames) > 0 {
		execOpts["enabled_group_names"] = sctx.Schedule.GroupNames
	}
	if mode := strings.TrimSpace(sctx.Schedule.ExecutionMode); mode != "" {
		execOpts["execution_mode"] = mode
	}
	reqMap["execution_options"] = execOpts

	return reqMap
}

var schedulerWorkshopMaxInactivity = 10 * time.Minute

// schedulerWorkshopLiveChildCeiling bounds how long a turn may be held open by
// child work that is registered as running but emits nothing observable. The
// inactivity window above answers "is anything happening?"; it is the wrong
// question once a child is demonstrably alive, because a legitimate step can be
// silent far longer than that — browser workflows deliberately pace themselves
// in tens of seconds per action, and a full run_full_workflow child runs for
// hours. This ceiling exists only so a child that hangs forever cannot block its
// schedule forever.
var schedulerWorkshopLiveChildCeiling = 3 * time.Hour

var errWorkshopIdleWaitTimeout = errors.New("workshop idle wait timed out")
var errWorkshopSequenceInterrupted = errors.New("workshop sequence interrupted by user")
var errWorkshopSessionFailed = errors.New("workshop session failed")

// errWorkflowContractMigrationRequired marks an automated invocation whose
// lane still requires a current contract (currently direct webhooks). Normal
// schedules are allowed to keep running their saved contract.
var errWorkflowContractMigrationRequired = errors.New("workflow contract migration required")

func (s *SchedulerService) refreshSessionTmuxSnapshotsForIdleCheck(ctx context.Context, sessionID string) error {
	if s == nil || s.api == nil {
		return nil
	}
	return s.api.refreshSessionTmuxSnapshotsForIdleCheck(ctx, sessionID)
}

func (api *StreamingAPI) refreshSessionTmuxSnapshotsForIdleCheck(ctx context.Context, sessionID string) error {
	if api == nil || api.terminalStore == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	seenTmuxSessions := map[string]struct{}{}
	for _, snapshot := range api.terminalStore.ListMetadata(sessionID) {
		tmuxSession := strings.TrimSpace(snapshot.TmuxSession)
		if tmuxSession == "" {
			continue
		}
		if _, seen := seenTmuxSessions[tmuxSession]; seen {
			continue
		}
		seenTmuxSessions[tmuxSession] = struct{}{}

		captureCtx, cancel := context.WithTimeout(ctx, terminalTmuxActionTimeout)
		content, err := captureTerminalPaneLines(captureCtx, tmuxSession, terminalDefaultRefreshLines)
		cancel()
		if err != nil {
			if isMissingTmuxTargetError(err) {
				api.terminalStore.MarkStale(snapshot.TerminalID)
				continue
			}
			return fmt.Errorf("refresh tmux snapshot %q: %w", tmuxSession, err)
		}
		api.terminalStore.ReplaceContentWithSource(snapshot.TerminalID, content, "tmux_capture")
	}
	return nil
}

// updateRuntimeState owns the complete read-modify-write operation. Callers must
// never retain a runtime-state pointer after the mutex is released.
func (s *SchedulerService) updateRuntimeState(scheduleID string, update func(*ScheduleRuntimeState)) ScheduleRuntimeState {
	s.runtimeStatesMu.Lock()
	defer s.runtimeStatesMu.Unlock()
	defer noteScheduleSummaryChange()
	state, ok := s.runtimeStates[scheduleID]
	if !ok {
		state = &ScheduleRuntimeState{}
		s.runtimeStates[scheduleID] = state
	}
	if update != nil {
		update(state)
	}
	return *state
}

// activateScheduleRunLocked publishes the active run and its cancellation
// handle as one runtime-state operation. Caller must hold runtimeStatesMu.
func (s *SchedulerService) activateScheduleRunLocked(state *ScheduleRuntimeState, runID string, startedAt time.Time) context.Context {
	state.LastStatus = "running"
	state.ActiveRunID = runID
	state.LastRunAt = &startedAt
	state.LastError = ""
	state.WaitingSince = nil
	state.WaitingUntil = nil
	state.WaitingReason = ""
	state.QueuedOccurrences = 0
	return s.registerScheduleRunContext(runID)
}

func (s *SchedulerService) rollbackScheduleRunActivation(runtimeKey, runID string, previous ScheduleRuntimeState) {
	s.runtimeStatesMu.Lock()
	if current := s.runtimeStates[runtimeKey]; current != nil && current.ActiveRunID == runID {
		*current = previous
	}
	s.runtimeStatesMu.Unlock()
	s.releaseScheduleRunContext(runID)
}

func (s *SchedulerService) abortCanceledScheduleRunBeforeStart(ctx context.Context, sctx *ScheduleContext, runtimeKey, runID string) bool {
	if ctx.Err() == nil {
		return false
	}
	s.updateRuntimeState(runtimeKey, func(state *ScheduleRuntimeState) {
		if state.ActiveRunID != runID {
			return
		}
		state.ActiveRunID = ""
		state.LastStatus = "stopped"
		state.LastError = "stopped before execution started"
	})
	s.transitionScheduleRun(context.Background(), sctx, schedulerstate.Transition{
		RunID: runID, To: schedulerstate.StateStopped, Reason: "stopped before execution started",
		ErrorMessage: "stopped before execution started", At: time.Now().UTC(),
	})
	s.releaseScheduleRunContext(runID)
	s.cleanupRemovedScheduleRuntimeState(runtimeKey)
	return true
}

func (s *SchedulerService) cleanupRemovedScheduleRuntimeState(runtimeKey string) {
	s.mu.Lock()
	_, known := s.scheduleFingerprints[runtimeKey]
	s.mu.Unlock()
	if known {
		return
	}
	s.runtimeStatesMu.Lock()
	if state := s.runtimeStates[runtimeKey]; state == nil || state.ActiveRunID == "" {
		delete(s.runtimeStates, runtimeKey)
		noteScheduleSummaryChange()
	}
	s.runtimeStatesMu.Unlock()
}

func (s *SchedulerService) registerScheduleRunContext(runID string) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	s.runCancelsMu.Lock()
	if s.runCancels == nil {
		s.runCancels = make(map[string]context.CancelFunc)
	}
	if previous := s.runCancels[runID]; previous != nil {
		previous()
	}
	s.runCancels[runID] = cancel
	s.runCancelsMu.Unlock()
	return ctx
}

func (s *SchedulerService) cancelScheduleRunContext(runID string) {
	s.runCancelsMu.Lock()
	cancel := s.runCancels[runID]
	s.runCancelsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *SchedulerService) releaseScheduleRunContext(runID string) {
	s.runCancelsMu.Lock()
	cancel := s.runCancels[runID]
	delete(s.runCancels, runID)
	s.runCancelsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *SchedulerService) claimScheduleRun(ctx context.Context, sctx *ScheduleContext, runID string, startedAt time.Time) error {
	s.stateStoreMu.RLock()
	defer s.stateStoreMu.RUnlock()
	if s.stateStore == nil {
		if sctx.WebhookInput != nil {
			return errors.New("API trigger run store is unavailable")
		}
		return nil
	}
	if sctx.CapacityResumeRunID != "" {
		return s.stateStore.ResumeCapacityRun(ctx, sctx.CapacityResumeRunID, startedAt)
	}
	scopeType, scopeID, lockKey := scheduleStateScope(sctx)
	triggerSource := strings.TrimSpace(sctx.TriggerSource)
	if triggerSource == "" {
		triggerSource = "cron"
	}
	run := schedulerstate.Run{
		RunID:         runID,
		ScopeType:     scopeType,
		ScopeID:       scopeID,
		LockKey:       lockKey,
		ScheduleID:    sctx.Schedule.ID,
		TriggerSource: triggerSource,
		ScheduledFor:  sctx.ScheduledFor,
		StartedAt:     startedAt,
	}
	if triggerSource == "queued" {
		return s.stateStore.BeginQueuedRun(ctx, run)
	}
	return s.stateStore.BeginRun(ctx, run)
}

func (s *SchedulerService) transitionScheduleRun(ctx context.Context, sctx *ScheduleContext, transition schedulerstate.Transition) {
	if strings.TrimSpace(transition.RunID) == "" {
		return
	}
	s.stateStoreMu.RLock()
	defer s.stateStoreMu.RUnlock()
	if s.stateStore == nil {
		return
	}
	transitionCtx := ctx
	if transitionCtx == nil {
		transitionCtx = context.Background()
	}
	attempts := 1
	if schedulerstate.IsTerminal(transition.To) {
		transitionCtx = context.WithoutCancel(transitionCtx)
		attempts = 3
	}
	var transitionErr error
	for attempt := 0; attempt < attempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(transitionCtx, 5*time.Second)
		transitionErr = s.stateStore.Transition(attemptCtx, transition)
		cancel()
		if transitionErr == nil {
			return
		}
		if attempt+1 < attempts {
			time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
		}
	}
	if schedulerstate.IsTerminal(transition.To) {
		recoveryCtx, cancel := context.WithTimeout(transitionCtx, 5*time.Second)
		recoveryErr := s.stateStore.ForceTerminal(recoveryCtx, transition)
		cancel()
		if recoveryErr == nil {
			if sctx != nil {
				s.logf(sctx, "[SCHEDULER_STATE] recovered terminal transition run=%s to=%s after error: %v", transition.RunID, transition.To, transitionErr)
			} else {
				scheduleLogf("[SCHEDULER_STATE] recovered terminal transition run=%s to=%s after error: %v", transition.RunID, transition.To, transitionErr)
			}
			return
		}
		transitionErr = errors.Join(transitionErr, recoveryErr)
	}
	if transitionErr != nil {
		if sctx != nil {
			s.logf(sctx, "[SCHEDULER_STATE] transition run=%s to=%s failed: %v", transition.RunID, transition.To, transitionErr)
		} else {
			scheduleLogf("[SCHEDULER_STATE] transition run=%s to=%s failed: %v", transition.RunID, transition.To, transitionErr)
		}
	}
}

func (s *SchedulerService) recordScheduleFireDecision(ctx context.Context, sctx *ScheduleContext, decision, reason, runID string, firedAt time.Time) error {
	if sctx == nil {
		return errors.New("schedule context is required")
	}
	s.stateStoreMu.RLock()
	defer s.stateStoreMu.RUnlock()
	if s.stateStore == nil {
		return errors.New("schedule state store is unavailable")
	}
	scopeType, scopeID, _ := scheduleStateScope(sctx)
	triggerSource := strings.TrimSpace(sctx.TriggerSource)
	if triggerSource == "" {
		triggerSource = "cron"
	}
	scheduledFor := sctx.ScheduledFor
	if scheduledFor.IsZero() {
		scheduledFor = firedAt
	}
	if err := s.stateStore.RecordFireDecision(ctx, schedulerstate.FireDecision{
		DecisionID:    uuid.NewString(),
		ScopeType:     scopeType,
		ScopeID:       scopeID,
		ScheduleID:    sctx.Schedule.ID,
		TriggerSource: triggerSource,
		Decision:      decision,
		Reason:        reason,
		RunID:         runID,
		ScheduledFor:  scheduledFor.UTC(),
		FiredAt:       firedAt,
	}); err != nil {
		s.logf(sctx, "[SCHEDULER_STATE] record fire decision=%s failed: %v", decision, err)
		return err
	}
	return nil
}

// getRuntimeStateLocked returns or creates runtime state. Caller MUST hold runtimeStatesMu write lock.
func (s *SchedulerService) getRuntimeStateLocked(scheduleID string) *ScheduleRuntimeState {
	if state, ok := s.runtimeStates[scheduleID]; ok {
		return state
	}
	state := &ScheduleRuntimeState{}
	s.runtimeStates[scheduleID] = state
	noteScheduleSummaryChange()
	return state
}

func runningScheduleInSetLocked(runtimeStates map[string]*ScheduleRuntimeState, scheduleIDs []string, ignoreScheduleID string) (string, string) {
	for _, scheduleID := range scheduleIDs {
		if scheduleID == "" || scheduleID == ignoreScheduleID {
			continue
		}
		state := runtimeStates[scheduleID]
		if state == nil || state.LastStatus != "running" {
			continue
		}
		return scheduleID, state.LastSessionID
	}
	return "", ""
}

func (s *SchedulerService) findActiveNonBuilderExecutionForWorkspace(workspacePath string) *ActiveWorkflowExecution {
	if s == nil || s.api == nil || strings.TrimSpace(workspacePath) == "" {
		return nil
	}

	tracked := s.api.findRunningTrackedExecutionForWorkspaceWhere(workspacePath, func(exec *TrackedWorkflowExecution) bool {
		if invocation, ok := s.api.scheduleInvocations.Load(exec.SessionID); ok && invocation.(*stepworkflow.ScheduleInvocation).AllowParallel {
			return false
		}
		return exec.TriggeredBy != "webhook" && exec.Metadata["trigger_source"] != "webhook" && !strings.HasPrefix(exec.SessionID, "schedule-webhook--") && trackedExecutionBlocksScheduledWorkflow(exec)
	})
	if tracked == nil {
		return nil
	}
	active := trackedExecutionToActive(tracked)
	return &active
}

// findActiveExecutionBlockingSchedule keeps manual workflow runs and Pulse
// exclusive. A human-approved parallel schedule may overlap other producing
// schedules, but it does not silently broaden that approval to manual work or
// maintenance. Sequential schedules ignore only already-running schedules that
// themselves carry the parallel opt-in.
func (s *SchedulerService) findActiveExecutionBlockingSchedule(workspacePath string, incomingParallel bool) *ActiveWorkflowExecution {
	if !incomingParallel {
		return s.findActiveNonBuilderExecutionForWorkspace(workspacePath)
	}
	if s == nil || s.api == nil || strings.TrimSpace(workspacePath) == "" {
		return nil
	}
	tracked := s.api.findRunningTrackedExecutionForWorkspaceWhere(workspacePath, func(exec *TrackedWorkflowExecution) bool {
		if exec.TriggeredBy == "webhook" || exec.Metadata["trigger_source"] == "webhook" || strings.HasPrefix(exec.SessionID, "schedule-webhook--") {
			return false
		}
		// A trusted schedule binding identifies a normal workflow-producing
		// schedule. Pulse-only sessions deliberately have no such binding and
		// therefore continue to block this lane.
		if _, scheduled := s.api.scheduleInvocations.Load(exec.SessionID); scheduled {
			return false
		}
		return trackedExecutionBlocksScheduledWorkflow(exec)
	})
	if tracked == nil {
		return nil
	}
	active := trackedExecutionToActive(tracked)
	return &active
}

// ScheduleSearchResult holds the result of finding a schedule by ID.
type ScheduleSearchResult struct {
	WorkspacePath string
	Manifest      *WorkflowManifest
	Index         int
}

// findScheduleByIDAny resolves a workflow schedule by ID.
func findScheduleByIDAny(ctx context.Context, scheduleID string) (*ScheduleSearchResult, error) {
	wsPath, manifest, idx, err := findScheduleByID(ctx, scheduleID)
	if err == nil {
		return &ScheduleSearchResult{
			WorkspacePath: wsPath,
			Manifest:      manifest,
			Index:         idx,
		}, nil
	}
	return nil, fmt.Errorf("schedule %s not found", scheduleID)
}

// findScheduleByID scans all workspace manifests to find a schedule by ID.
// Returns (workspacePath, manifest, scheduleIndex, error).
func findScheduleByID(ctx context.Context, scheduleID string) (string, *WorkflowManifest, int, error) {
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return "", nil, 0, fmt.Errorf("cannot scan workflow directory: %w", err)
	}

	for _, item := range discovered {
		for i, sched := range item.Manifest.Schedules {
			if sched.ID == scheduleID {
				return item.WorkspacePath, item.Manifest, i, nil
			}
		}
	}

	return "", nil, 0, fmt.Errorf("schedule %s not found in any manifest", scheduleID)
}

// getNextRunTime calculates the next scheduled run time.
func getNextRunTime(cronExpr string, timezone string) *time.Time {
	loc, err := time.LoadLocation(scheduleTimezoneOrDefault(timezone))
	if err != nil {
		loc = time.UTC
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(cronExpr)
	if err != nil {
		return nil
	}

	next := schedule.Next(time.Now().In(loc)).UTC()
	return &next
}

func buildScheduleCronExpression(cronExpr string, timezone string) string {
	return fmt.Sprintf("CRON_TZ=%s %s", scheduleTimezoneOrDefault(timezone), cronExpr)
}

func scheduleTypeOrDefault(scheduleType string) string {
	if scheduleType == "" {
		return "cron"
	}
	return scheduleType
}

func calendarItemRunTime(sched WorkflowSchedule, item CalendarScheduleItem) (time.Time, error) {
	loc, err := time.LoadLocation(scheduleTimezoneOrDefault(sched.Timezone))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timezone %q: %w", sched.Timezone, err)
	}
	if item.Date == "" || item.Time == "" {
		return time.Time{}, fmt.Errorf("calendar item date and time are required")
	}
	local, err := time.ParseInLocation("2006-01-02 15:04", item.Date+" "+item.Time, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid calendar item %q %q: expected date YYYY-MM-DD and time HH:MM", item.Date, item.Time)
	}
	return local.UTC(), nil
}

func getNextRunTimeForCalendar(sched WorkflowSchedule) *time.Time {
	now := time.Now().UTC()
	var next *time.Time
	for _, item := range sched.CalendarItems {
		runAt, err := calendarItemRunTime(sched, item)
		if err != nil || !runAt.After(now) {
			continue
		}
		if next == nil || runAt.Before(*next) {
			runAtCopy := runAt
			next = &runAtCopy
		}
	}
	return next
}

func scheduleWithCalendarItem(sched WorkflowSchedule, item CalendarScheduleItem) WorkflowSchedule {
	sched.TriggerPayload = item.TriggerPayload
	if len(item.Messages) > 0 {
		sched.Messages = item.Messages
	}
	return sched
}

func scheduleWithReloadedCalendarItem(sched WorkflowSchedule, requested *CalendarScheduleItem) (WorkflowSchedule, *CalendarScheduleItem, bool) {
	if requested == nil {
		return sched, nil, true
	}
	for i := range sched.CalendarItems {
		item := sched.CalendarItems[i]
		matches := requested.ID != "" && item.ID == requested.ID
		if requested.ID == "" {
			matches = item.Date == requested.Date && item.Time == requested.Time
		}
		if !matches {
			continue
		}
		itemCopy := item
		return scheduleWithCalendarItem(sched, itemCopy), &itemCopy, true
	}
	return sched, nil, false
}

func scheduleConfigFingerprint(sctx *ScheduleContext) string {
	if sctx == nil {
		return ""
	}
	payload, err := json.Marshal(struct {
		Schedule     WorkflowSchedule     `json:"schedule"`
		Capabilities WorkflowCapabilities `json:"capabilities"`
	}{Schedule: sctx.Schedule, Capabilities: sctx.Capabilities})
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(payload))
}

func ValidateScheduleTimezone(timezone string) error {
	if timezone == "" {
		return fmt.Errorf("timezone is required; use an IANA timezone like UTC, Asia/Kolkata, or America/New_York")
	}
	if timezone != "UTC" && !strings.Contains(timezone, "/") {
		return fmt.Errorf("invalid timezone %q: use an IANA timezone like UTC, Asia/Kolkata, or America/New_York; abbreviations like EST, PST, or IST are not accepted", timezone)
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return fmt.Errorf("invalid timezone %q: use an IANA timezone like UTC, Asia/Kolkata, or America/New_York", timezone)
	}
	return nil
}

// ValidateCronExpression validates a 5-field cron expression.
func ValidateCronExpression(expr string) error {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	_, err := parser.Parse(expr)
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	return nil
}

// lockedScheduleCapabilities is the manifest's capabilities as a scheduled run
// (and Pulse, and the capacity wait) must see them: the LLM config rewritten
// to the published default under LLM_CONFIG_LOCKED (lockedPresetLLMConfig),
// everything else untouched. Applied once where the ScheduleContext is built
// so every scheduler consumer of Capabilities.LLMConfig agrees.
func lockedScheduleCapabilities(caps WorkflowCapabilities) WorkflowCapabilities {
	caps.LLMConfig = lockedPresetLLMConfig(caps.LLMConfig)
	return caps
}

// goalWorkAutonomy is the workflow's Goal Work permission levels for the
// strategic review turn, and the same levels as text. A manifest that cannot be
// read gives the defaults (run auto, outward and change ask).
func goalWorkAutonomy(ctx context.Context, workspacePath string) (stepworkflow.GoalWorkPermissions, string) {
	manifestJSON := ""
	if manifest, found, err := ReadWorkflowManifest(ctx, workspacePath); err == nil && found {
		if encoded, marshalErr := json.Marshal(manifest); marshalErr == nil {
			manifestJSON = string(encoded)
		}
	}
	perms := stepworkflow.GoalWorkEffectivePermissions(manifestJSON)
	return perms, stepworkflow.GoalWorkAutonomyInstructions(perms)
}
