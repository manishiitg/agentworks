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

	"github.com/robfig/cron/v3"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// ScheduleContext bundles everything needed to identify and execute a schedule.
type ScheduleContext struct {
	CrewExecutable *crewExecutableBinding

	WorkspacePath string
	WorkflowID    string
	WorkflowLabel string
	WorkflowKind  string
	Schedule      WorkflowSchedule
	WebhookInput  *WorkflowWebhookDelivery
	Capabilities  WorkflowCapabilities
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
	// CallerSessionID is the active chat that started this run with run-now. It
	// is resumed with the run's terminal result (schedule_caller_notice.go).
	// CallerExecutionID is that chat's running turn when it asked.
	CallerSessionID   string
	CallerExecutionID string
	// ChatDestinationSessionID is the chat the schedule itself posts to, when it
	// has one, so a caller that is the same chat is not told twice.
	ChatDestinationSessionID string

	CalendarItem *CalendarScheduleItem
	// ScheduledFor is the durable identity of a cron/calendar occurrence. It is
	// empty for a manual trigger, whose actual trigger time is its identity.
	ScheduledFor time.Time
	// QueuedExpiresAt preserves the first queue attempt's max-start deadline
	// across retries. A busy workflow must not extend a stale occurrence forever.
	QueuedExpiresAt       time.Time
	QueuedOccurrenceCount int
	OrderedScheduleIDs    []string // enabled schedules sharing the serial workflow lease

	ProducedRunEvidence bool

	// CapacityResume* carry a run that was suspended on a provider capacity
	// wall back into execution (PLAT-101). The run is resumed in place — same
	// run row, same run folder, starting at the step that could not run —
	// because the steps before it already completed and did real work that must
	// not be replayed.
	CapacityResumeRunID     string
	CapacityResumeRunFolder string
	CapacityResumeFromStep  int
	ScheduledRunFolder      string
}

const scheduleScopeSeparator = "\x1f"

func workflowScheduleRuntimeKey(workspacePath, scheduleID string) string {
	return strings.Join([]string{"workflow", filepath.Clean(strings.TrimSpace(workspacePath)), strings.TrimSpace(scheduleID)}, scheduleScopeSeparator)
}

// Cost source keys reach summaries over the API; keep filesystem paths out.
func workflowScheduleCostSourceID(workspacePath, scheduleID string) string {
	return fmt.Sprintf("workflow-schedule:%x", sha256.Sum256([]byte(workflowScheduleRuntimeKey(workspacePath, scheduleID))))
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

func scheduleStateLockKeyFromRuntimeKey(runtimeKey string) string {
	parts := strings.Split(runtimeKey, scheduleScopeSeparator)
	if len(parts) < 3 || parts[0] != "workflow" {
		return runtimeKey
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

	// Outcome notification for full runs started from a chat (run_outcome.go).
	stepworkflow.AfterWorkflowRunHook = func(workspacePath, runFolder, status, sessionID string) {
		s.reportManualRunOutcome(workspacePath, runFolder, status, sessionID)
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
			} else if sched.Enabled {
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
// macOS App Nap and sleep/wake monotonic clock drift. A slot missed during
// sleep or a long gap is skipped, not run late (scheduleOnTimeGrace).
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
			s.dispatchProjectTimers(ctx, t)
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
					skipped, fire := splitOnTimeOccurrences(occurrences, t)
					for _, occurrence := range skipped {
						missed = append(missed, dueRegisteredJob{job: job, scheduledFor: occurrence})
					}
					if len(skipped) > 0 && fire == nil && job.lastFired.Before(skipped[len(skipped)-1]) {
						job.lastFired = skipped[len(skipped)-1]
					}
					if fire != nil {
						toFire = append(toFire, dueRegisteredJob{job: job, scheduledFor: *fire})
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
				if err := s.recordScheduleFireDecision(context.Background(), &occurrenceCtx, "missed_scheduler_gap", "scheduler was not evaluating at the scheduled occurrence; the slot is skipped (no catch-up)", "", t.UTC()); err != nil {
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
			lastTick = t
		}
	}
}

// scheduleOnTimeGrace is how late a scheduled slot may still start. Later than
// this the slot is skipped and the schedule waits for its next one: there is no
// catch-up after sleep or a long gap (owner, 2026-10-24: "no catch up at all").
const scheduleOnTimeGrace = 2 * time.Minute

// splitOnTimeOccurrences returns the due occurrences to skip and the one newest
// occurrence to start, or nil when even that one is too late.
func splitOnTimeOccurrences(occurrences []time.Time, now time.Time) (skipped []time.Time, fire *time.Time) {
	if len(occurrences) == 0 {
		return nil, nil
	}
	latest := occurrences[len(occurrences)-1]
	if now.Sub(latest) > scheduleOnTimeGrace {
		return occurrences, nil
	}
	return occurrences[:len(occurrences)-1], &latest
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
		CrewExecutable: manifest.crewExecutable,
		WorkspacePath:  workspacePath,
		WorkflowID:     manifest.ID,
		WorkflowLabel:  manifest.Label,
		WorkflowKind:   manifest.Kind,
		Schedule:       sched,
		Capabilities:   lockedScheduleCapabilities(manifest.Capabilities),
		OwnerUserID:    workflowExecutionOwnerUserID(manifest),
	}
	if manifest.crewExecutable == nil && sched.RunAs != "" {
		sctx.OwnerUserID = sched.RunAs
	}
	if sched.ScheduleType != "webhook" && !scheduleAllowsParallel(sched) {
		for _, candidate := range manifest.Schedules {
			if candidate.Enabled && candidate.ScheduleType != "webhook" && !scheduleAllowsParallel(candidate) {
				sctx.OrderedScheduleIDs = append(sctx.OrderedScheduleIDs, candidate.ID)
			}
		}
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
	if manifest.crewExecutable != nil {
		owner, found := crewProjectOwnerID(manifest.crewExecutable.CrewRoot)
		if found && workflowExecutionAccountActive(owner) {
			return owner
		}
		// A Crew's work never switches to a co-owner's machine or logins.
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
	if err := validateExecutableSchedule(context.Background(), sctx); err != nil {
		if errors.Is(err, errFlowsDisabled) {
			s.logf(sctx, "[SCHEDULER] %s", err.Error())
			_ = s.recordScheduleFireDecision(context.Background(), sctx, "failed_to_start", err.Error(), "", time.Now().UTC())
		}
		return err
	}
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
	// Flows retain the shared function-delivery ledger, but register no timers.

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
		cronSched, err := (projectactions.When{Cron: sched.CronExpression, Timezone: scheduleTimezoneOrDefault(sched.Timezone)}).CronSchedule()
		if err != nil {
			return fmt.Errorf("failed to parse project clock: %w", err)
		}
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
		// An old decision is only a cursor within the current enabled timing
		// window. Re-enabling or retiming must never replay a slot from before
		// that edit; an unchanged restart still catches up inside its window.
		if windowStart, ok := WorkflowScheduleTrackingWindowStart(context.Background(), sctx.WorkspacePath, sched.ID); ok && windowStart.After(lastFired) {
			lastFired = windowStart
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
			merged.WaitingUntil = nil
			if !expiresAt.IsZero() {
				merged.WaitingUntil = &expiresAt
			}
			merged.WaitingReason = pending.Reason
			merged.QueuedOccurrences = pending.OccurrenceCount
		} else if decisions, err := s.stateStore.ListFireDecisions(context.Background(), "workflow", workspacePath, scheduleID, 1); err == nil && len(decisions) > 0 {
			latest := decisions[0]
			if (latest.Decision == "blocked_dependency" || latest.Decision == "expired_dependency_deadline") &&
				(merged.LastRunAt == nil || latest.FiredAt.After(*merged.LastRunAt)) {
				merged.LastStatus, merged.LastError = "error", latest.Reason
				merged.WaitingSince, merged.WaitingUntil = nil, nil
				merged.WaitingReason, merged.QueuedOccurrences = "", 0
			}
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
		completedAt := now
		if _, _, terminalAt, ok := s.durableScheduleRunProjection(ctx, runs[i].ID); ok && terminalAt != nil {
			completedAt = *terminalAt
		}
		durationMs := completedAt.Sub(runs[i].StartedAt).Milliseconds()
		if durationMs < 0 {
			durationMs = 0
		}
		runs[i].DurationMs = &durationMs
		runs[i].CompletedAt = &completedAt
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
	// A workspace outage can lose the history write after SQLite saved the
	// outcome. Preserve that result, even when no session reached the file.
	if status, reason, _, ok := s.durableScheduleRunProjection(context.Background(), run.ID); ok {
		if status == scheduleRunStatusWaitingForCapacity {
			return "", "", false
		}
		return status, reason, true
	}
	s.runCancelsMu.Lock()
	active := s.runCancels[run.ID] != nil
	s.runCancelsMu.Unlock()
	if active {
		return "", "", false
	}

	if strings.TrimSpace(run.SessionID) == "" {
		if now.Sub(run.StartedAt) > 10*time.Minute {
			return "error", "interrupted: no session id recorded", true
		}
		return "", "", false
	}

	if s.api == nil {
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
		// latency-report's 2026-07-31 02:30 run was recorded success/84,599 ms with
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
// the reconciler treats it as abandoned. It must exceed a real run: latency-report's
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
// customer-login: four Technical Review passes theorized a missing
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
func webhookDeliveryStartError(ctx context.Context, workspace string, sched WorkflowSchedule, input *WorkflowWebhookDelivery) error {
	if input.Group != "" {
		return errors.New(retiredWorkflowGroupsMessage)
	}
	if input.DirectRun {
		if !sched.IsInternalTrigger() {
			return errors.New("direct run requires an authenticated internal binding")
		}
		sched.InputPreset = input.DirectInputPreset
		return resolveWorkflowWebhookInputs(ctx, workspace, sched, input)
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
	return resolveWorkflowWebhookInputs(ctx, workspace, sched, input)
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
		if input != nil && input.ConditionCaller != nil {
			sched, err = s.conditionRoute(ctx, workspacePath, scheduleID, input)
			if err != nil {
				return "", err
			}
		}
	}
	if sched == nil {
		return "", fmt.Errorf("schedule %s not found in manifest at %s", scheduleID, workspacePath)
	}

	sctx := buildScheduleContext(workspacePath, manifest, *sched)
	if input != nil {
		sctx.TriggerSource = "webhook"
	} else {
		sctx.TriggerSource = "manual"
	}
	if err := validateExecutableSchedule(ctx, sctx); err != nil {
		if errors.Is(err, errFlowsDisabled) {
			s.logf(sctx, "[SCHEDULER] %s", err.Error())
			_ = s.recordScheduleFireDecision(ctx, sctx, "failed_to_start", err.Error(), "", time.Now().UTC())
		}
		return "", err
	}
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
		if err := webhookDeliveryStartError(ctx, workspacePath, *sched, input); err != nil {
			return "", err
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
	s.rememberScheduleCaller(sctx, originSessionID)
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

// stopChatWorkflowRun uses the run identity admitted to this chat's background
// item. It never selects all runs of a workflow or another delivery's worker.
func (s *SchedulerService) stopChatWorkflowRun(runID, scheduleID string) {
	if s == nil || runID == "" {
		return
	}
	s.runtimeStatesMu.RLock()
	key := ""
	for candidate, state := range s.runtimeStates {
		if state.ActiveRunID == runID && state.LastStatus == "running" {
			key = candidate
			break
		}
	}
	s.runtimeStatesMu.RUnlock()
	if key != "" {
		s.stopRunningJob(key, scheduleID)
	}
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

func (s *SchedulerService) cancelScheduledSessionWork(sessionID, closeReason string, terminalPhase RuntimePhase) {
	if s == nil || s.api == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	s.api.cancelSessionRuntimeWork(sessionID, closeReason, terminalPhase)
}

// triggerSchedule is called by the tick loop when a schedule is due.
func (s *SchedulerService) triggerSchedule(sctx *ScheduleContext, scheduledFor time.Time) {

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

	paused, cfg, err := s.IsProductPaused(context.Background(), workflowPauseProduct(sctx.WorkflowKind))
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
			s.logf(sctx, "[SCHEDULER] ⏸️ Scheduler pause active for %s, skipping %s", workflowPauseProduct(sctx.WorkflowKind), schedID)
		}
		reason := "global scheduler pause is active"
		if cfg != nil && !cfg.GloballyPaused {
			reason = "this product's schedules are paused"
		}
		s.recordScheduleFireDecision(ctx, sctx, "skipped_paused", reason, "", now.UTC())
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
		if source := strings.TrimSpace(sctx.TriggerSource); source == "" || source == "cron" || source == "queued" {
			if windowStart, ok := WorkflowScheduleTrackingWindowStart(ctx, sctx.WorkspacePath, schedID); ok && scheduledFor.Before(windowStart) {
				if err := s.recordScheduleFireDecision(ctx, sctx, "skipped_schedule_changed", "this time passed before the schedule was enabled or its timing changed", "", now.UTC()); err == nil {
					s.discardQueuedOccurrence(ctx, sctx)
				}
				return
			}
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
		freshCtx.Schedule.InputPreset = sctx.Schedule.InputPreset
		freshCtx.Schedule.Inputs = sctx.Schedule.Inputs
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
			decision := "blocked_dependency"
			if strings.Contains(reason, "deadline") {
				decision = "expired_dependency_deadline"
			}
			s.finishBlockedScheduleOccurrence(ctx, freshCtx, decision, reason, startTime)
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

// Busy occurrences retain only the latest pending occurrence of each schedule.
func (s *SchedulerService) recordOrQueueBlockedSchedule(ctx context.Context, sctx *ScheduleContext, reason string, at time.Time) bool {
	if sctx == nil {
		return false
	}
	return s.queueScheduleOccurrence(ctx, sctx, reason, at, "queue_latest")
}

func (s *SchedulerService) queueScheduleOccurrence(ctx context.Context, sctx *ScheduleContext, reason string, at time.Time, policy string) bool {
	if sctx == nil {
		return false
	}
	scopeType, scopeID, _ := scheduleStateScope(sctx)
	scheduledFor := sctx.ScheduledFor
	if scheduledFor.IsZero() {
		scheduledFor = at
	}
	var expiresAt time.Time // Busy runs have no max-start expiry.
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
	s.updateScheduleQueueState(scheduleRuntimeKey(sctx), func(state *ScheduleRuntimeState) {
		state.LastStatus = "waiting_for_workflow"
		state.LastError = reason
		state.WaitingSince = &now
		state.WaitingUntil = nil
		if !expiresAt.IsZero() {
			state.WaitingUntil = &expiresAt
		}
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
				if decision, decisionErr := store.FireDecisionForScheduleOccurrence(ctx, scopeType, scopeID, dependencyID, dayStart, dayEnd); decisionErr == nil && terminalScheduleFireDecision(decision.Decision) {
					return scheduleDependencyBlocked, fmt.Sprintf("prerequisite schedule %s did not run: %s", dependencyID, decision.Reason)
				}
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

func terminalScheduleFireDecision(decision string) bool {
	return strings.HasPrefix(decision, "skipped_") || strings.HasPrefix(decision, "blocked_") ||
		strings.HasPrefix(decision, "expired_") || decision == "failed_to_start" ||
		decision == "missed_scheduler_gap" || decision == "launch_outcome_unknown"
}

// End this occurrence before dropping its queue row. Dependents can then follow
// the terminal decision even though no workflow run was created for this slot.
func (s *SchedulerService) finishBlockedScheduleOccurrence(ctx context.Context, sctx *ScheduleContext, decision, reason string, at time.Time) {
	s.stateStoreMu.RLock()
	alreadyFinished := false
	if s.stateStore != nil {
		scopeType, scopeID, _ := scheduleStateScope(sctx)
		prior, err := s.stateStore.FireDecisionForScheduleOccurrence(ctx, scopeType, scopeID, sctx.Schedule.ID, sctx.ScheduledFor, sctx.ScheduledFor.Add(time.Nanosecond))
		alreadyFinished = err == nil && (prior.Decision == "blocked_dependency" || prior.Decision == "expired_dependency_deadline")
	}
	s.stateStoreMu.RUnlock()
	if alreadyFinished {
		s.discardQueuedOccurrence(ctx, sctx)
		return
	}
	manifest, found, _ := ReadWorkflowManifest(ctx, sctx.WorkspacePath)
	if found && manifest != nil {
		for _, schedule := range manifest.Schedules {
			if schedule.Name != "" {
				reason = strings.ReplaceAll(reason, schedule.ID, schedule.Name)
			}
		}
	}
	if err := s.recordScheduleFireDecision(ctx, sctx, decision, reason, "", at); err != nil {
		return
	}
	s.discardQueuedOccurrence(ctx, sctx)
	s.updateScheduleQueueState(scheduleRuntimeKey(sctx), func(state *ScheduleRuntimeState) {
		state.LastStatus, state.LastError = "error", reason
		state.WaitingSince, state.WaitingUntil = nil, nil
		state.WaitingReason, state.QueuedOccurrences = "", 0
	})
	s.logf(sctx, "[SCHEDULER] Schedule %s did not run scheduled_for=%s: %s", sctx.Schedule.ID, sctx.ScheduledFor.Format(time.RFC3339Nano), reason)
	if manifest != nil {
		if err := s.recordRunOutcome(ctx, sctx, manifest, "", "blocked", "This scheduled run did not start because "+reason+". Fix the earlier schedule in Builder, then run again.", 0); err != nil {
			s.logf(sctx, "[SCHEDULER] Blocked occurrence outcome could not be recorded: %v", err)
		}
	}
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
		// A queued delivery may survive an interrupted launch, but it must not
		// restart that same occurrence. Startup already released the old run's
		// lease; retain its outcome and leave the lane for the next cron slot.
		prior, priorErr := store.RunForScheduleOccurrence(ctx, item.ScopeType, item.ScopeID, item.ScheduleID, item.ScheduledFor, item.ScheduledFor.Add(time.Nanosecond))
		if priorErr == nil && prior.State == schedulerstate.StateInterrupted {
			sctx := buildScheduleContext(item.ScopeID, &WorkflowManifest{}, WorkflowSchedule{ID: item.ScheduleID})
			sctx.TriggerSource, sctx.ScheduledFor = "queued", item.ScheduledFor
			if err := s.recordScheduleFireDecision(ctx, sctx, "skipped_interrupted", "this occurrence was interrupted by a restart; the next scheduled run will start normally", prior.RunID, now); err == nil {
				_ = store.DeletePendingOccurrence(ctx, item.ScopeType, item.ScopeID, item.ScheduleID)
				s.logf(sctx, "[SCHEDULER] Not retrying interrupted occurrence run=%s scheduled_for=%s; waiting for the next scheduled time", prior.RunID, item.ScheduledFor.Format(time.RFC3339Nano))
			}
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
		if schedule == nil || !schedule.Enabled {
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
		_, _, launchKey := scheduleStateScope(sctx)
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
	// A chat that started this run with run-now is owed its terminal result, even
	// when the run ends before it has an outcome of its own.
	callerNotice := s.beginCallerNotice(sctx, runID)
	callerNoticed := false
	defer func() {
		if callerNotice != nil && !callerNoticed {
			// Plain words for the person; the cause is in the scheduler log.
			callerNotice.finish(sctx, "error", "the run did not start", "")
		}
	}()
	if sctx.WebhookInput != nil {
		defer s.pruneWebhookRuns(sctx.WorkspacePath)
	} else {
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
			if state.ActiveRunID != runID {
				return
			}
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
	observedRunID, observedStatus := "", ""
	if activeState != nil {
		observedRunID, observedStatus = activeState.ActiveRunID, activeState.LastStatus
	}
	s.runtimeStatesMu.RUnlock()
	if !ownsActiveRun {
		reason := "schedule ownership changed before execution started"
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		settleErr := errors.New("schedule state store is unavailable")
		s.stateStoreMu.RLock()
		if s.stateStore != nil {
			settleErr = s.stateStore.Transition(settleCtx, schedulerstate.Transition{
				RunID: runID, ExpectedFrom: schedulerstate.StateStarting, To: schedulerstate.StateFailed,
				Reason: reason, ErrorMessage: reason, At: time.Now().UTC(),
			})
		}
		s.stateStoreMu.RUnlock()
		cancel()
		if settleErr == nil {
			s.updateRuntimeState(runtimeKey, func(state *ScheduleRuntimeState) {
				if state.ActiveRunID == runID {
					state.ActiveRunID = ""
					state.LastStatus, state.LastError = "error", reason
				}
			})
			s.logf(sctx, "[SCHEDULER_STATE] rejected run=%s before execution; observed_run=%s observed_status=%s; starting claim finalized as failed", runID, observedRunID, observedStatus)
		} else {
			s.logf(sctx, "[SCHEDULER_STATE] rejected run=%s before execution; observed_run=%s observed_status=%s; starting claim not changed: %v", runID, observedRunID, observedStatus, settleErr)
		}
		return "", fmt.Errorf("scheduled run %s no longer owns %s: %s", runID, runtimeKey, reason)
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
	if sctx.WebhookInput == nil {
		if err := validateTeamScheduleFire(ctx, sctx.WorkspacePath, sctx.Schedule); err != nil {
			failBeforeHistory(err, "")
			return "", err
		}
	}

	// A run whose identity is over its shared-account token limit fails here
	// with the reason, like a refused manual turn (PLAT-683).
	if err := s.api.scheduledRunTokenLimitRefusal(ctx, sctx); err != nil {
		failBeforeHistory(err, "")
		return "", err
	}

	if sctx.WebhookInput == nil {
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

	s.transitionScheduleRun(ctx, sctx, schedulerstate.Transition{
		RunID:  runID,
		To:     schedulerstate.StateWorkflowRunning,
		Reason: startReason,
		At:     time.Now().UTC(),
	})

	sessionID, runFolder, execErr := s.executeJob(ctx, sctx, runID)

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
	} else if partialWorkshopRun(execErr) {
		status = "partial"
		errMsg = execErr.Error()
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] %s partly completed in %dms: %v", schedID, durationMs, execErr)
	} else if execErr != nil {
		status = "error"
		errMsg = execErr.Error()
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] %s failed in %dms: %v", schedID, durationMs, execErr)
	} else {
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] %s completed in %dms, session: %s, folder: %s", schedID, durationMs, sessionID, runFolder)
	}
	finishedReason := "workflow execution finished with status " + status
	finishedSessionKind := "workflow"

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

	s.updateRuntimeState(runtimeKey, func(state *ScheduleRuntimeState) {
		state.LastSessionID = sessionID
	})

	if err := UpdateScheduleRun(persistenceCtx, sctx.WorkspacePath, runID, status, errMsg, &durationMs, runFolder, sessionID); err != nil {
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] Failed to update run entry for %s: %v", schedID, err)
	}
	if sctx.WebhookInput == nil {
		if err := SetScheduleRunRanWorkflow(persistenceCtx, sctx.WorkspacePath, runID, sctx.ProducedRunEvidence); err != nil {
			s.logf(sctx, "[SCHEDULER] could not record ran_workflow for %s: %v", runID, err)
		}
	}

	if errors.Is(execErr, errWorkflowScriptRequired) {
		s.sessionLogf(sctx, sessionID, "[SCHEDULER] %s did not run: this workflow needs saved Python source", schedID)
	}
	// Now the whole scheduled job, including post-run side effects, is done.
	terminalState := schedulerstate.StateCompleted
	if status == scheduleRunStatusWaitingForCapacity {
		terminalState = schedulerstate.StateWaitingForCapacity
	} else if userInterrupted {
		terminalState = schedulerstate.StateStopped
	} else if status == "error" {
		terminalState = schedulerstate.StateFailed
	} else if status == "partial" {
		terminalState = schedulerstate.StatePartial
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
			overallError = "The run finished with warnings"
		}
	}
	// Both the informational Needs you status and linked Crew wake-up use
	// the final outcome.
	if sctx.WebhookInput == nil && overallStatus != scheduleRunStatusWaitingForCapacity && !userInterrupted {
		if manifest, found, err := ReadWorkflowManifest(persistenceCtx, sctx.WorkspacePath); err == nil && found {
			s.reportScheduledRunOutcome(persistenceCtx, sctx, manifest, runID, overallStatus, overallError, time.Since(startTime))
		}
	}
	durationMs = time.Since(startTime).Milliseconds()

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

	s.transitionScheduleRun(persistenceCtx, sctx, schedulerstate.Transition{
		RunID: runID, To: terminalState, Reason: terminalReason,
		SessionID: sessionID, RunFolder: runFolder, ErrorMessage: overallError, At: time.Now().UTC(),
	})
	s.cleanupRemovedScheduleRuntimeState(runtimeKey)
	callerNotice.finish(sctx, overallStatus, overallError, runFolder)
	callerNoticed = true

	if overallStatus == "partial" {
		// The terminal result is recorded above; a mixed outcome must not
		// make the caller log a blanket execution failure.
		return sessionID, nil
	}
	return sessionID, execErr
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

// executeJob builds a session request from the manifest and runs it.
// Returns (sessionID, runFolder, error).
func (s *SchedulerService) executeJob(ctx context.Context, sctx *ScheduleContext, runID string) (sessionID, runFolder string, runErr error) {
	if err := validateExecutableSchedule(ctx, sctx); err != nil {
		return "", "", err
	}
	target, err := s.openCrewAutomationChat(ctx, sctx)
	if err != nil {
		return "", "", err
	}
	if target != nil {
		if strings.TrimSpace(runID) == "" {
			return "", "", fmt.Errorf("automation run identity is required")
		}
		if err := s.appendCrewAutomationMessage(ctx, sctx, target, runID, "started", ""); err != nil {
			return "", "", err
		}
		sctx.OriginSessionID = target.SessionID
		sctx.ChatDestinationSessionID = target.SessionID
		defer func() {
			status, detail := "completed", ""
			if runErr != nil {
				status, detail = "failed", runErr.Error()
			}
			if runFolder != "" {
				detail += fmt.Sprintf("\nRun files: `%s/runs/%s`", sctx.WorkspacePath, runFolder)

			}
			if e := s.appendCrewAutomationMessage(context.Background(), sctx, target, runID, status, detail); e != nil {
				s.logf(sctx, "[AUTOMATION_CHAT] result delivery failed run=%s: %v", runID, e)
				runErr = errors.Join(runErr, fmt.Errorf("chat result delivery: %w", e))
			}
		}()
	}

	if sctx.WebhookInput != nil {
		return s.executeWebhookJob(ctx, sctx, runID)
	}
	if sctx.CrewExecutable != nil && sctx.CrewExecutable.Kind == "script" {
		return s.executeCrewScriptSchedule(ctx, sctx, runID)
	}
	// Workflows run without a chat (owner, 8 Nov): every workflow timer starts its run directly; saved messages are
	// not read (the schedule API refuses new ones).
	return s.executeDirectScheduledRun(ctx, sctx, runID)
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

func readLocalScheduleRuns(workspacePath string) ([]ScheduleRunEntry, bool, error) {
	localPath, err := fsutil.WorkspacePath(fsutil.WorkspaceDocsRoot(), scheduleRunsPath(workspacePath))
	if err != nil {
		return nil, true, err
	}
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

// buildWorkshopRequest creates the base request map for scheduled workflow
// execution. Writable scheduled turns use Workshop; the server pins read-only
// workflow access to Run.
func (s *SchedulerService) buildWorkshopRequest(ctx context.Context, sctx *ScheduleContext, costRunID string) map[string]interface{} {
	reqMap := map[string]interface{}{
		"agent_mode":                  "workflow_phase",
		"phase_id":                    workflowtypes.WorkflowStatusWorkflowBuilder,
		"preset_query_id":             sctx.WorkflowID,
		"selected_folder":             sctx.WorkspacePath,
		"triggered_by":                scheduleSessionTrigger(sctx),
		"triggered_by_label":          automationTriggerLabel(scheduleSessionTrigger(sctx), sctx.Schedule.Name),
		"session_title":               sctx.Schedule.Name,
		"servers":                     sctx.Capabilities.RuntimeServers(),
		"selected_tools":              sctx.Capabilities.SelectedTools,
		"selected_skills":             sctx.Capabilities.SelectedSkills,
		"browser_mode":                sctx.Capabilities.BrowserMode,
		"use_code_execution_mode":     sctx.Capabilities.UseCodeExecutionMode,
		"disable_live_input_delivery": true,
		// Retain the coding CLI across consecutive turns with the same authority.
		"keep_native_session_alive": true,
	}
	// Workflow schedules use this launch path rather than product_schedules.
	// Name the source before requests are cloned for later turns and children.
	if id := strings.TrimSpace(sctx.Schedule.ID); id != "" {
		reqMap["cost_source_id"] = workflowScheduleCostSourceID(sctx.WorkspacePath, id)
		reqMap["cost_source_label"] = firstNonEmptyTrimmed(sctx.Schedule.Name, "Scheduled run")
		reqMap["cost_source_run_id"] = strings.TrimSpace(costRunID)
	}

	s.applyLLMAndSecretsToReqMap(ctx, reqMap, sctx)

	runFolder := ""
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

	execOpts["input_preset"] = sctx.Schedule.InputPreset
	execOpts["inputs"] = sctx.Schedule.Inputs

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

// errWorkflowScriptRequired marks a retired or unbound workflow that has no
// registered saved Python source for an automated run.
var errWorkflowScriptRequired = errors.New("This workflow has not been converted to a saved Python workflow yet, so it cannot start. Ask its owner to convert it.")

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

// Queue events describe a pending occurrence, not the active run. In particular,
// expiring an older queued slot must not revoke a newer run's in-memory owner.
func (s *SchedulerService) updateScheduleQueueState(scheduleID string, update func(*ScheduleRuntimeState)) {
	s.updateRuntimeState(scheduleID, func(state *ScheduleRuntimeState) {
		if state.ActiveRunID != "" {
			return
		}
		update(state)
	})
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
		RunID:              runID,
		ScopeType:          scopeType,
		ScopeID:            scopeID,
		LockKey:            lockKey,
		ScheduleID:         sctx.Schedule.ID,
		TriggerSource:      triggerSource,
		ScheduledFor:       sctx.ScheduledFor,
		StartedAt:          startedAt,
		OrderedScheduleIDs: sctx.OrderedScheduleIDs,
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
	return getNextRunTimeAt(cronExpr, timezone, time.Now())
}

func getNextRunTimeAt(cronExpr string, timezone string, now time.Time) *time.Time {
	loc, err := time.LoadLocation(scheduleTimezoneOrDefault(timezone))
	if err != nil {
		loc = time.UTC
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(cronExpr)
	if err != nil {
		return nil
	}

	next := schedule.Next(now.In(loc)).UTC()
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
	return getNextRunTimeForCalendarAt(sched, time.Now().UTC())
}

func getNextRunTimeForCalendarAt(sched WorkflowSchedule, now time.Time) *time.Time {
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

func lockedScheduleCapabilities(caps WorkflowCapabilities) WorkflowCapabilities {
	caps.LLMConfig = lockedPresetLLMConfig(caps.LLMConfig)
	return caps
}
