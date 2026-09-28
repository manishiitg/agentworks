package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulepolicy"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

// ScheduledJobResponse is the API response for a scheduled job.
// Designed to be backward-compatible with the old DB-based ScheduledJob shape.
type ScheduledJobResponse struct {
	StepID               string                 `json:"step_id,omitempty"`
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name"`
	Description          string                 `json:"description"`
	EntityType           string                 `json:"entity_type"`
	WorkspacePath        string                 `json:"workspace_path"`
	WorkflowID           string                 `json:"workflow_id,omitempty"`
	WorkflowLabel        string                 `json:"workflow_label,omitempty"`
	PresetQueryID        string                 `json:"preset_query_id,omitempty"` // empty — kept for frontend compat
	TriggerPayload       json.RawMessage        `json:"trigger_payload,omitempty"`
	GroupNames           []string               `json:"group_names,omitempty"`
	RouteSelections      map[string]string      `json:"route_selections,omitempty"`
	Mode                 string                 `json:"mode,omitempty"`
	Messages             []string               `json:"messages,omitempty"` // Predefined messages for workshop schedules
	DirectMessagesReason string                 `json:"direct_messages_reason,omitempty"`
	WorkshopMode         string                 `json:"workshop_mode,omitempty"`   // workshop for writable users; Run is access-derived
	ResumePrevious       bool                   `json:"resume_previous,omitempty"` // Coding-agent CLI only: opt in to resume latest prior thread instead of fresh session
	ScheduleType         string                 `json:"schedule_type,omitempty"`
	CalendarItems        []CalendarScheduleItem `json:"calendar_items,omitempty"`
	CronExpression       string                 `json:"cron_expression"`
	Timezone             string                 `json:"timezone"`
	Enabled              bool                   `json:"enabled"`
	LastRunAt            *time.Time             `json:"last_run_at,omitempty"`
	NextRunAt            *time.Time             `json:"next_run_at,omitempty"`
	LastSessionID        string                 `json:"last_session_id,omitempty"`
	LastStatus           string                 `json:"last_status,omitempty"`
	LastError            string                 `json:"last_error,omitempty"`
	LastDurationMs       *int64                 `json:"last_duration_ms,omitempty"`
	AvgDurationMs        *int64                 `json:"avg_duration_ms,omitempty"`
	AvgDurationSamples   int                    `json:"avg_duration_samples,omitempty"`
	RunCount             int                    `json:"run_count"`
	ConsecutiveFailures  int                    `json:"consecutive_failures"`
	// DeferredReason is set while a due product schedule is held back by its
	// quiet rule (the user is active); empty otherwise.
	DeferredReason           string     `json:"deferred_reason,omitempty"`
	ExecutionMode            string     `json:"execution_mode,omitempty"`
	CollisionPolicy          string     `json:"collision_policy,omitempty"`
	ConcurrencyMode          string     `json:"concurrency_mode,omitempty"`
	ParallelRiskAcknowledged bool       `json:"parallel_risk_acknowledged,omitempty"`
	MaxConcurrency           int        `json:"max_concurrency,omitempty"`
	MaxStartDelayMinutes     int        `json:"max_start_delay_minutes,omitempty"`
	AfterScheduleID          string     `json:"after_schedule_id,omitempty"`
	AfterScheduleIDs         []string   `json:"after_schedule_ids,omitempty"`
	AfterTerminalStatus      string     `json:"after_terminal_status,omitempty"`
	AfterDelayMinutes        int        `json:"after_delay_minutes,omitempty"`
	DependencyDeadline       string     `json:"dependency_deadline,omitempty"`
	WaitingSince             *time.Time `json:"waiting_since,omitempty"`
	WaitingUntil             *time.Time `json:"waiting_until,omitempty"`
	WaitingReason            string     `json:"waiting_reason,omitempty"`
	QueuedOccurrences        int        `json:"queued_occurrences,omitempty"`
	MissedRunCount           int        `json:"missed_run_count,omitempty"`
	LatestMissedRunAt        *time.Time `json:"latest_missed_run_at,omitempty"`
	MissedRunReason          string     `json:"missed_run_reason,omitempty"`
	PulseReviewOnly          bool       `json:"pulse_review_only,omitempty"`
	PulseMode                string     `json:"pulse_mode,omitempty"`
	PulseModeReason          string     `json:"pulse_mode_reason,omitempty"`
	CreatedAt                string     `json:"created_at,omitempty"`
	UpdatedAt                string     `json:"updated_at,omitempty"`
	RunDestination           string     `json:"run_destination,omitempty"`
}

// CreateScheduleRequest is the request body for creating a schedule.
type CreateScheduleRequest struct {
	EntityType               string                 `json:"entity_type,omitempty"`
	ProductProfileID         string                 `json:"product_profile_id,omitempty"`
	ProductProjectID         string                 `json:"product_project_id,omitempty"`
	WorkspacePath            string                 `json:"workspace_path"` // Required for workflow/workshop mode
	Name                     string                 `json:"name"`
	Description              string                 `json:"description,omitempty"`
	ScheduleType             string                 `json:"schedule_type,omitempty"`
	CronExpression           string                 `json:"cron_expression"`
	Timezone                 string                 `json:"timezone"`
	CalendarItems            []CalendarScheduleItem `json:"calendar_items,omitempty"`
	Enabled                  bool                   `json:"enabled"`
	TriggerPayload           json.RawMessage        `json:"trigger_payload,omitempty"`
	GroupNames               []string               `json:"group_names,omitempty"`
	RouteSelections          map[string]string      `json:"route_selections,omitempty"`
	Mode                     string                 `json:"mode,omitempty"`
	Messages                 []string               `json:"messages,omitempty"` // Predefined messages for workshop schedules
	DirectMessagesReason     string                 `json:"direct_messages_reason,omitempty"`
	WorkshopMode             string                 `json:"workshop_mode,omitempty"`   // workshop for writable users; Run is access-derived
	ResumePrevious           *bool                  `json:"resume_previous,omitempty"` // Coding-agent CLI only: explicit true resumes latest prior thread; nil/false starts fresh
	ExecutionMode            string                 `json:"execution_mode,omitempty"`
	CollisionPolicy          string                 `json:"collision_policy,omitempty"`
	ConcurrencyMode          string                 `json:"concurrency_mode,omitempty"`
	ParallelRiskAcknowledged bool                   `json:"parallel_risk_acknowledged,omitempty"`
	MaxStartDelayMinutes     int                    `json:"max_start_delay_minutes,omitempty"`
	AfterScheduleID          string                 `json:"after_schedule_id,omitempty"`
	AfterScheduleIDs         []string               `json:"after_schedule_ids,omitempty"`
	AfterTerminalStatus      string                 `json:"after_terminal_status,omitempty"`
	AfterDelayMinutes        int                    `json:"after_delay_minutes,omitempty"`
	DependencyDeadline       string                 `json:"dependency_deadline,omitempty"`
	PulseReviewOnly          bool                   `json:"pulse_review_only,omitempty"`
	PulseMode                string                 `json:"pulse_mode,omitempty"`
	PulseModeReason          string                 `json:"pulse_mode_reason,omitempty"`
	RunDestination           string                 `json:"run_destination,omitempty"`
}

// UpdateScheduleRequest is the request body for updating a schedule.
type UpdateScheduleRequest struct {
	Name                     string                 `json:"name,omitempty"`
	Description              string                 `json:"description,omitempty"`
	ScheduleType             string                 `json:"schedule_type,omitempty"`
	CronExpression           string                 `json:"cron_expression,omitempty"`
	Timezone                 string                 `json:"timezone,omitempty"`
	CalendarItems            []CalendarScheduleItem `json:"calendar_items,omitempty"`
	Enabled                  *bool                  `json:"enabled,omitempty"`
	TriggerPayload           json.RawMessage        `json:"trigger_payload,omitempty"`
	GroupNames               []string               `json:"group_names,omitempty"`
	RouteSelections          map[string]string      `json:"route_selections,omitempty"`
	Mode                     string                 `json:"mode,omitempty"`
	Messages                 []string               `json:"messages,omitempty"` // Predefined messages for workshop schedules
	DirectMessagesReason     *string                `json:"direct_messages_reason,omitempty"`
	WorkshopMode             string                 `json:"workshop_mode,omitempty"`   // workshop for writable users; Run is access-derived
	ResumePrevious           *bool                  `json:"resume_previous,omitempty"` // Coding-agent CLI only: explicit true resumes latest prior thread; nil/false starts fresh
	ExecutionMode            *string                `json:"execution_mode,omitempty"`
	CollisionPolicy          *string                `json:"collision_policy,omitempty"`
	ConcurrencyMode          *string                `json:"concurrency_mode,omitempty"`
	ParallelRiskAcknowledged *bool                  `json:"parallel_risk_acknowledged,omitempty"`
	RunDestination           *string                `json:"run_destination,omitempty"`
	MaxStartDelayMinutes     *int                   `json:"max_start_delay_minutes,omitempty"`
	AfterScheduleID          *string                `json:"after_schedule_id,omitempty"`
	AfterScheduleIDs         []string               `json:"after_schedule_ids,omitempty"`
	AfterTerminalStatus      *string                `json:"after_terminal_status,omitempty"`
	AfterDelayMinutes        *int                   `json:"after_delay_minutes,omitempty"`
	DependencyDeadline       *string                `json:"dependency_deadline,omitempty"`
	PulseMode                *string                `json:"pulse_mode,omitempty"`
	PulseModeReason          *string                `json:"pulse_mode_reason,omitempty"`
}

type TriggerPulseRequest struct {
	WorkspacePath string `json:"workspace_path"`
}

func buildJobResponse(workspacePath string, manifest *WorkflowManifest, sched WorkflowSchedule, state ScheduleRuntimeState, missed WorkflowScheduleMissedStatus) ScheduledJobResponse {
	maxConcurrency := 0
	collisionPolicy := sched.CollisionPolicy
	concurrencyMode := sched.ConcurrencyMode
	if sched.ScheduleType == "webhook" {
		maxConcurrency = maxWebhookConcurrency
		collisionPolicy = ""
		concurrencyMode = ""
	}
	return ScheduledJobResponse{
		StepID:                   workflowWebhookDTO(sched).StepID,
		ID:                       sched.ID,
		Name:                     sched.Name,
		Description:              sched.Description,
		EntityType:               "workflow",
		WorkspacePath:            workspacePath,
		WorkflowID:               manifest.ID,
		WorkflowLabel:            manifest.Label,
		PresetQueryID:            manifest.ID,
		TriggerPayload:           sched.TriggerPayload,
		GroupNames:               sched.GroupNames,
		RouteSelections:          sched.RouteSelections,
		Mode:                     "workshop",
		Messages:                 sched.Messages,
		DirectMessagesReason:     sched.DirectMessagesReason,
		WorkshopMode:             sched.WorkshopMode,
		ResumePrevious:           sched.ShouldResumePrevious(),
		ScheduleType:             scheduleTypeOrDefault(sched.ScheduleType),
		CalendarItems:            sched.CalendarItems,
		CronExpression:           sched.CronExpression,
		Timezone:                 sched.Timezone,
		Enabled:                  sched.Enabled,
		LastRunAt:                state.LastRunAt,
		NextRunAt:                state.NextRunAt,
		LastSessionID:            state.LastSessionID,
		LastStatus:               state.LastStatus,
		LastError:                state.LastError,
		LastDurationMs:           state.LastDurationMs,
		RunCount:                 state.RunCount,
		ConsecutiveFailures:      state.ConsecutiveFailures,
		ExecutionMode:            sched.ExecutionMode,
		CollisionPolicy:          collisionPolicy,
		ConcurrencyMode:          concurrencyMode,
		ParallelRiskAcknowledged: sched.ParallelRiskAcknowledged,
		MaxConcurrency:           maxConcurrency,
		MaxStartDelayMinutes:     sched.MaxStartDelayMinutes,
		AfterScheduleID:          sched.AfterScheduleID,
		AfterScheduleIDs:         sched.AfterScheduleIDs,
		AfterTerminalStatus:      sched.AfterTerminalStatus,
		AfterDelayMinutes:        sched.AfterDelayMinutes,
		DependencyDeadline:       sched.DependencyDeadline,
		WaitingSince:             state.WaitingSince,
		WaitingUntil:             state.WaitingUntil,
		WaitingReason:            state.WaitingReason,
		QueuedOccurrences:        state.QueuedOccurrences,
		MissedRunCount:           missed.MissedRunCount,
		LatestMissedRunAt:        missed.LatestMissedRunAt,
		MissedRunReason:          missed.MissedRunReason,
		PulseReviewOnly:          sched.PulseReviewOnly,
		PulseMode:                sched.PulseMode,
		PulseModeReason:          sched.PulseModeReason,
		CreatedAt:                manifest.CreatedAt,
		UpdatedAt:                manifest.UpdatedAt,
	}
}

func runtimeStateForScheduleResult(svc *SchedulerService, result *ScheduleSearchResult, scheduleID string) ScheduleRuntimeState {
	if svc == nil || result == nil {
		return ScheduleRuntimeState{}
	}
	return svc.GetRuntimeStateForWorkflow(result.WorkspacePath, scheduleID)
}

func validateScheduleRequest(scheduleType string, cronExpr string, calendarItems []CalendarScheduleItem) error {
	switch scheduleType {
	case "webhook":
		return nil
	case "cron":
		if strings.TrimSpace(cronExpr) == "" {
			return errBadRequest("cron_expression is required for cron schedules")
		}
		return ValidateCronExpression(cronExpr)
	case "calendar":
		if len(calendarItems) == 0 {
			return errBadRequest("calendar_items is required for calendar schedules")
		}
		for i, item := range calendarItems {
			if strings.TrimSpace(item.Date) == "" || strings.TrimSpace(item.Time) == "" {
				return errBadRequest("calendar_items[%d].date and time are required", i)
			}
			if _, err := time.Parse("2006-01-02", item.Date); err != nil {
				return errBadRequest("calendar_items[%d].date must be YYYY-MM-DD", i)
			}
			if _, err := time.Parse("15:04", item.Time); err != nil {
				return errBadRequest("calendar_items[%d].time must be HH:MM", i)
			}
		}
		return nil
	default:
		return errBadRequest("schedule_type must be 'cron', 'calendar', or 'webhook'")
	}
}

func normalizeCalendarScheduleItems(items []CalendarScheduleItem) []CalendarScheduleItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]CalendarScheduleItem, 0, len(items))
	for _, item := range items {
		if item.ID == "" {
			item.ID = uuid.New().String()
		}
		out = append(out, item)
	}
	return out
}

func normalizeScheduleDependencyIDs(ids []string) []string {
	if ids == nil {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strings.TrimSpace(id)
	}
	return out
}

// replaceScheduleDependencyIDs applies the canonical list update. The legacy
// singular field remains a read-compatible input, but it must not survive as a
// hidden extra dependency when a caller replaces the list.
func replaceScheduleDependencyIDs(schedule *WorkflowSchedule, ids []string, legacySupplied bool) {
	if schedule == nil {
		return
	}
	schedule.AfterScheduleIDs = normalizeScheduleDependencyIDs(ids)
	if !legacySupplied {
		schedule.AfterScheduleID = ""
	}
}

type badRequestError string

func (e badRequestError) Error() string { return string(e) }

func errBadRequest(format string, args ...interface{}) error {
	return badRequestError(fmt.Sprintf(format, args...))
}

type workflowMissedStatusResolver struct {
	ctx     context.Context
	svc     *SchedulerService
	history map[string]*WorkflowScheduleExecutionHistoryFile
}

func newWorkflowMissedStatusResolver(ctx context.Context, svc *SchedulerService) *workflowMissedStatusResolver {
	return &workflowMissedStatusResolver{
		ctx:     ctx,
		svc:     svc,
		history: make(map[string]*WorkflowScheduleExecutionHistoryFile),
	}
}

func (r *workflowMissedStatusResolver) get(workspacePath string, sched WorkflowSchedule) WorkflowScheduleMissedStatus {
	now := time.Now().UTC()
	workflowScheduleExecutionHistoryMu.Lock()
	defer workflowScheduleExecutionHistoryMu.Unlock()

	history, ok := r.history[workspacePath]
	if !ok {
		loaded, err := ReadWorkflowScheduleExecutionHistory(r.ctx, workspacePath)
		if err != nil {
			loaded = &WorkflowScheduleExecutionHistoryFile{
				Version:   workflowScheduleExecutionHistoryVersion,
				Schedules: map[string]WorkflowScheduleExecutionTrack{},
			}
		}
		history = loaded
		r.history[workspacePath] = history
	}
	if history == nil || history.Schedules == nil {
		history = &WorkflowScheduleExecutionHistoryFile{
			Version:   workflowScheduleExecutionHistoryVersion,
			Schedules: map[string]WorkflowScheduleExecutionTrack{},
		}
		r.history[workspacePath] = history
	}

	tracker, changed := ensureWorkflowScheduleExecutionTracker(history, sched, now)
	if changed {
		history.Schedules[sched.ID] = tracker
		if err := WriteWorkflowScheduleExecutionHistory(r.ctx, workspacePath, history); err != nil {
			// Listing schedules should still work if the history sync fails.
			scheduleLogf("[SCHEDULER] Warning: failed to sync execution history for %s: %v", sched.ID, err)
		}
	}
	if !sched.Enabled {
		return WorkflowScheduleMissedStatus{}
	}
	missed := ComputeWorkflowScheduleMissedStatus(sched, &tracker, now)
	// "No run" has causes the scheduler records: a global pause, a busy
	// workflow, an expired queue. Report that instead of a bare "missed".
	if missed.MissedRunCount > 0 && missed.LatestMissedRunAt != nil && r.svc != nil {
		if decisions, err := r.svc.ListFireDecisions(r.ctx, workspacePath, sched.ID, 50); err == nil {
			if reason := missedReasonFromDecisions(decisions, *missed.LatestMissedRunAt); reason != "" {
				missed.MissedRunReason = reason
			}
		}
	}
	return missed
}

// SchedulerRoutes registers the scheduler API routes.
func SchedulerRoutes(router *mux.Router, svc *SchedulerService) {
	apiRouter := router.PathPrefix("/api/scheduler").Subrouter()

	apiRouter.HandleFunc("/config", getSchedulerConfigHandler(svc)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/config", requireWorkflowWriteAccess(updateSchedulerConfigHandler(svc))).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/jobs", listScheduledJobsHandler(svc)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/jobs", createScheduledJobHandler(svc)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/jobs/{id}", getScheduledJobHandler(svc)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/jobs/{id}", updateScheduledJobHandler(svc)).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/jobs/{id}", deleteScheduledJobHandler(svc)).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/jobs/{id}/enable", enableScheduledJobHandler(svc)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/jobs/{id}/disable", disableScheduledJobHandler(svc)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/jobs/{id}/trigger", triggerScheduledJobHandler(svc)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflows/pulse-run", requireWorkflowWriteAccess(triggerWorkflowPulseHandler(svc))).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/jobs/{id}/stop", stopScheduledJobHandler(svc)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/jobs/{id}/reset-history", resetHistoryScheduledJobHandler(svc)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/jobs/{id}/runs", getScheduledJobRunsHandler(svc)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/jobs/runs/cleanup", requireWorkflowWriteAccess(cleanupScheduledJobRunsHandler(svc))).Methods("DELETE", "OPTIONS")
}

// cleanupScheduledJobRunsHandler bulk-deletes old terminal run entries for a
// workspace, optionally restricted to schedule_ids (comma-separated). Run
// folders and conversation transcripts are left alone.
func cleanupScheduledJobRunsHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = svc
		workspacePath := strings.TrimSpace(r.URL.Query().Get("workspace_path"))
		if workspacePath == "" {
			http.Error(w, "workspace_path is required", http.StatusBadRequest)
			return
		}
		olderThanDays := 30
		if v := strings.TrimSpace(r.URL.Query().Get("older_than_days")); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				http.Error(w, "older_than_days must be a positive integer", http.StatusBadRequest)
				return
			}
			olderThanDays = n
		}
		var scheduleIDs []string
		if v := strings.TrimSpace(r.URL.Query().Get("schedule_ids")); v != "" {
			scheduleIDs = strings.Split(v, ",")
		}
		deleted, err := DeleteScheduleRunsOlderThan(r.Context(), workspacePath, scheduleIDs, olderThanDays)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted_count": deleted, "workspace_path": workspacePath})
	}
}

func triggerWorkflowPulseHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		var req TriggerPulseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.WorkspacePath) == "" {
			http.Error(w, "workspace_path is required", http.StatusBadRequest)
			return
		}

		runID, err := svc.TriggerPulseNow(req.WorkspacePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"run_id": runID})
	}
}

// WorkflowScheduleSummary is the header-level schedule count summary shown
// before the schedules popup is opened. Mirrors the frontend's previous
// client-side summarizeWorkflowSchedules(jobs), computed here instead so the
// full job list never has to be fetched just to show four numbers.
type WorkflowScheduleSummary struct {
	ScheduledWorkflows int `json:"scheduled_workflows"`
	RunningWorkflows   int `json:"running_workflows"`
	TotalSchedules     int `json:"total_schedules"`
	RunningSchedules   int `json:"running_schedules"`
}

// SummarizeWorkflowSchedules computes the same entity_type=workflow schedule
// counts as listScheduledJobsHandler without building the full per-job
// response payload.
func (svc *SchedulerService) SummarizeWorkflowSchedules(ctx context.Context) (WorkflowScheduleSummary, error) {
	workflows, err := svc.DiscoverWorkflowManifestsCached(ctx, 5*time.Second)
	if err != nil {
		return WorkflowScheduleSummary{}, err
	}

	scheduledWorkflowKeys := make(map[string]struct{})
	runningWorkflowKeys := make(map[string]struct{})
	summary := WorkflowScheduleSummary{}

	for _, dw := range workflows {
		for _, sched := range dw.Manifest.Schedules {
			summary.TotalSchedules++
			scheduledWorkflowKeys[dw.Manifest.ID] = struct{}{}

			state := svc.GetRuntimeStateForWorkflow(dw.WorkspacePath, sched.ID)
			if state.LastStatus == "running" {
				summary.RunningSchedules++
				runningWorkflowKeys[dw.Manifest.ID] = struct{}{}
			}
		}
	}

	summary.ScheduledWorkflows = len(scheduledWorkflowKeys)
	summary.RunningWorkflows = len(runningWorkflowKeys)
	return summary, nil
}

func listScheduledJobsHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		limit := 50
		offset := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		if v := r.URL.Query().Get("offset"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				offset = n
			}
		}

		enabledFilter := r.URL.Query().Get("enabled")

		modeFilter := r.URL.Query().Get("mode")
		entityTypeFilter := r.URL.Query().Get("entity_type")

		var allJobs []ScheduledJobResponse
		missedResolver := newWorkflowMissedStatusResolver(r.Context(), svc)

		if (entityTypeFilter == "" || entityTypeFilter == "workflow") &&
			(modeFilter == "" || modeFilter == "workflow" || modeFilter == "workshop") {
			// Discover all workflows and collect schedules. This is workspace-API
			// backed, so keep a short cache for repeated UI polling.
			workflows, err := svc.DiscoverWorkflowManifestsCached(r.Context(), 5*time.Second)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			for _, dw := range workflows {
				for _, sched := range dw.Manifest.Schedules {
					if enabledFilter != "" {
						wantEnabled := enabledFilter == "true" || enabledFilter == "1"
						if sched.Enabled != wantEnabled {
							continue
						}
					}
					if modeFilter != "" && sched.Mode != modeFilter {
						continue
					}

					state := svc.GetRuntimeStateForWorkflow(dw.WorkspacePath, sched.ID)
					missed := missedResolver.get(dw.WorkspacePath, sched)
					allJobs = append(allJobs, buildJobResponse(dw.WorkspacePath, dw.Manifest, sched, state, missed))
				}
			}
		}

		if entityTypeFilter == "" || entityTypeFilter == "product" {
			if modeFilter == "" || modeFilter == "workshop" {
				productJobs, err := productScheduleJobResponses(r, svc, enabledFilter)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				allJobs = append(allJobs, productJobs...)
			}
		}
		total := len(allJobs)

		// Pagination
		if offset >= total {
			allJobs = []ScheduledJobResponse{}
		} else {
			end := offset + limit
			if end > total {
				end = total
			}
			allJobs = allJobs[offset:end]
		}

		// One history read per workspace keeps the list useful without making a
		// separate request for every schedule. Missing history is not a list error.
		durationByWorkspace := make(map[string]map[string]scheduleRunDurationAverage)
		for i := range allJobs {
			job := &allJobs[i]
			if job.WorkspacePath == "" {
				continue
			}
			averages, ok := durationByWorkspace[job.WorkspacePath]
			if !ok {
				runs, err := ReadScheduleRuns(r.Context(), job.WorkspacePath)
				if err == nil {
					averages = recentSuccessfulRunAverages(runs, 10)
				}
				durationByWorkspace[job.WorkspacePath] = averages
			}
			if average, found := averages[job.ID]; found {
				value := average.DurationMs
				job.AvgDurationMs = &value
				job.AvgDurationSamples = average.Samples
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jobs":   allJobs,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		})
	}
}

func createScheduledJobHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		var req CreateScheduleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := ValidateScheduleTimezone(req.Timezone); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := validateScheduleRequest(scheduleTypeOrDefault(req.ScheduleType), req.CronExpression, req.CalendarItems); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.EntityType == "product" {
			if svc == nil || svc.api == nil || svc.api.productSchedules == nil {
				http.Error(w, "product schedules are not available", http.StatusNotFound)
				return
			}
			if len(req.Messages) != 1 || strings.TrimSpace(req.Messages[0]) == "" {
				http.Error(w, "a Work project schedule requires exactly one message", http.StatusBadRequest)
				return
			}
			isolated, err := isolatedForRunDestination(req.RunDestination)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			job, err := svc.api.productSchedules.CreateProjectSchedule(r.Context(), productWorkspaceUserID(r.Context()), req.ProductProfileID, req.ProductProjectID, productschedule.Schedule{
				Name: req.Name, Description: req.Description, Enabled: req.Enabled,
				CronExpression: req.CronExpression, Timezone: scheduleTimezoneOrDefault(req.Timezone), Messages: req.Messages, Isolated: isolated,
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			runsWorkspace, _ := svc.api.productSchedules.RunsWorkspace(r.Context(), job)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(svc.api.productSchedules.jobResponse(job, runsWorkspace))
			return
		}
		if req.WorkspacePath != "" && !requireWorkflowOwner(w, r, req.WorkspacePath) {
			return
		}
		messagesForValidation := append([]string(nil), req.Messages...)
		for _, item := range req.CalendarItems {
			messagesForValidation = append(messagesForValidation, item.Messages...)
		}
		if err := validateScheduleMessages(messagesForValidation, req.DirectMessagesReason); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Workflow schedule creation always runs through the workshop builder path.
		if req.WorkspacePath == "" {
			http.Error(w, "workspace_path is required", http.StatusBadRequest)
			return
		}
		mode := scheduleModeOrDefault(req.Mode)

		// Read manifest
		manifest, found, err := ReadWorkflowManifest(r.Context(), req.WorkspacePath)
		if err != nil || !found {
			http.Error(w, "workflow manifest not found at "+req.WorkspacePath, http.StatusBadRequest)
			return
		}
		// PLAT-115: a PulseReviewOnly schedule never runs the workflow, so it has
		// no group to validate against — the same reason the chat-facing
		// create_workflow_schedule tool path skips this validation too.
		if !req.PulseReviewOnly {
			req.GroupNames, err = validateScheduleGroupNamesForWorkspace(r.Context(), req.WorkspacePath, req.GroupNames)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}

		// Create new schedule
		newSched := WorkflowSchedule{
			ID:                       uuid.New().String(),
			Name:                     req.Name,
			Description:              req.Description,
			ScheduleType:             scheduleTypeOrDefault(req.ScheduleType),
			CronExpression:           req.CronExpression,
			Timezone:                 req.Timezone,
			CalendarItems:            normalizeCalendarScheduleItems(req.CalendarItems),
			Enabled:                  req.Enabled,
			TriggerPayload:           req.TriggerPayload,
			GroupNames:               req.GroupNames,
			RouteSelections:          req.RouteSelections,
			Mode:                     mode,
			Messages:                 req.Messages,
			DirectMessagesReason:     req.DirectMessagesReason,
			WorkshopMode:             req.WorkshopMode,
			ResumePrevious:           req.ResumePrevious,
			ExecutionMode:            strings.TrimSpace(req.ExecutionMode),
			CollisionPolicy:          strings.TrimSpace(req.CollisionPolicy),
			ConcurrencyMode:          strings.TrimSpace(req.ConcurrencyMode),
			ParallelRiskAcknowledged: req.ParallelRiskAcknowledged,
			MaxStartDelayMinutes:     req.MaxStartDelayMinutes,
			AfterScheduleID:          strings.TrimSpace(req.AfterScheduleID),
			AfterScheduleIDs:         normalizeScheduleDependencyIDs(req.AfterScheduleIDs),
			AfterTerminalStatus:      strings.TrimSpace(req.AfterTerminalStatus),
			AfterDelayMinutes:        req.AfterDelayMinutes,
			DependencyDeadline:       strings.TrimSpace(req.DependencyDeadline),
			PulseReviewOnly:          req.PulseReviewOnly,
			PulseMode:                strings.ToLower(strings.TrimSpace(req.PulseMode)),
			PulseModeReason:          strings.TrimSpace(req.PulseModeReason),
		}

		if err := schedulepolicy.ValidatePulse(newSched.PulseMode, newSched.PulseModeReason); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		manifest.Schedules = append(manifest.Schedules, newSched)
		if err := ValidateManifest(manifest); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := WriteWorkflowManifest(r.Context(), req.WorkspacePath, manifest); err != nil {
			http.Error(w, "failed to write manifest: "+err.Error(), http.StatusInternalServerError)
			return
		}
		svc.InvalidateWorkflowManifestCache()

		// Register in scheduler if enabled
		if newSched.Enabled {
			sctx := buildScheduleContext(req.WorkspacePath, manifest, newSched)
			if err := svc.LoadSchedule(sctx); err != nil {
				scheduleLogf("[SCHEDULER] Failed to load new schedule %s: %v", newSched.ID, err)
			}
		}

		state := svc.GetRuntimeStateForWorkflow(req.WorkspacePath, newSched.ID)
		missedResolver := newWorkflowMissedStatusResolver(r.Context(), svc)
		resp := buildJobResponse(req.WorkspacePath, manifest, newSched, state, missedResolver.get(req.WorkspacePath, newSched))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(resp)
	}
}

func getScheduledJobHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		id := mux.Vars(r)["id"]
		if handleProductScheduleJob(w, r, svc, id, "get") {
			return
		}
		result, err := findScheduleByIDAny(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !requireWorkflowVisible(w, r, result.WorkspacePath) {
			return
		}

		state := runtimeStateForScheduleResult(svc, result, id)
		missedResolver := newWorkflowMissedStatusResolver(r.Context(), svc)
		sched := result.Manifest.Schedules[result.Index]
		resp := buildJobResponse(result.WorkspacePath, result.Manifest, sched, state, missedResolver.get(result.WorkspacePath, sched))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func updateScheduledJobHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		id := mux.Vars(r)["id"]
		if handleProductScheduleJob(w, r, svc, id, "update") {
			return
		}

		var req UpdateScheduleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Timezone != "" {
			if err := ValidateScheduleTimezone(req.Timezone); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}

		result, err := findScheduleByIDAny(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !requireWorkflowOwner(w, r, result.WorkspacePath) {
			return
		}

		// Workflow schedule update
		workspacePath := result.WorkspacePath
		manifest := result.Manifest
		idx := result.Index

		sched := &manifest.Schedules[idx]
		legacyFull := strings.EqualFold(strings.TrimSpace(sched.PulseMode), schedulepolicy.LegacyFullPulseMode)
		if req.Name != "" {
			sched.Name = req.Name
		}
		if req.Description != "" {
			sched.Description = req.Description
		}
		if req.ScheduleType != "" {
			sched.ScheduleType = req.ScheduleType
		}
		if req.CronExpression != "" {
			sched.CronExpression = req.CronExpression
		}
		if req.Timezone != "" {
			sched.Timezone = req.Timezone
		}
		if req.CalendarItems != nil {
			sched.CalendarItems = normalizeCalendarScheduleItems(req.CalendarItems)
		}
		if req.Enabled != nil {
			sched.Enabled = *req.Enabled
		}
		if req.TriggerPayload != nil {
			sched.TriggerPayload = req.TriggerPayload
		}
		if req.GroupNames != nil {
			validGroupNames, err := validateScheduleGroupNamesForWorkspace(r.Context(), workspacePath, req.GroupNames)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			sched.GroupNames = validGroupNames
		}
		if req.RouteSelections != nil {
			sched.RouteSelections = req.RouteSelections
		}
		if req.Mode != "" || sched.Mode == "" || sched.Mode == "workflow" {
			mode := scheduleModeOrDefault(req.Mode)
			sched.Mode = mode
		}
		candidateDefaultMessages := sched.Messages
		if req.Messages != nil {
			candidateDefaultMessages = req.Messages
		}
		candidateMessages := append([]string(nil), candidateDefaultMessages...)
		for _, item := range sched.CalendarItems {
			candidateMessages = append(candidateMessages, item.Messages...)
		}
		candidateReason := sched.DirectMessagesReason
		if req.DirectMessagesReason != nil {
			candidateReason = *req.DirectMessagesReason
		}
		if req.Messages != nil || req.CalendarItems != nil || req.DirectMessagesReason != nil {
			if err := validateScheduleMessages(candidateMessages, candidateReason); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		if req.Messages != nil {
			sched.Messages = req.Messages
		}
		if req.DirectMessagesReason != nil {
			sched.DirectMessagesReason = *req.DirectMessagesReason
		}
		if req.WorkshopMode != "" {
			sched.WorkshopMode = req.WorkshopMode
		}
		if req.ResumePrevious != nil {
			sched.ResumePrevious = req.ResumePrevious
		}
		if req.PulseMode != nil && strings.ToLower(strings.TrimSpace(*req.PulseMode)) != sched.PulseMode && req.PulseModeReason == nil {
			http.Error(w, "pulse_mode_reason is required when changing pulse_mode", http.StatusBadRequest)
			return
		}
		if req.PulseModeReason != nil {
			sched.PulseModeReason = strings.TrimSpace(*req.PulseModeReason)
		}
		if req.PulseMode != nil {
			sched.PulseMode = strings.ToLower(strings.TrimSpace(*req.PulseMode))
		}
		if req.ExecutionMode != nil {
			sched.ExecutionMode = strings.TrimSpace(*req.ExecutionMode)
		}
		if req.CollisionPolicy != nil {
			sched.CollisionPolicy = strings.TrimSpace(*req.CollisionPolicy)
		}
		if req.ConcurrencyMode != nil {
			sched.ConcurrencyMode = strings.TrimSpace(*req.ConcurrencyMode)
			if sched.ConcurrencyMode != "parallel" && req.ParallelRiskAcknowledged == nil {
				sched.ParallelRiskAcknowledged = false
			}
		}
		if req.ParallelRiskAcknowledged != nil {
			sched.ParallelRiskAcknowledged = *req.ParallelRiskAcknowledged
		}
		if req.MaxStartDelayMinutes != nil {
			sched.MaxStartDelayMinutes = *req.MaxStartDelayMinutes
		}
		if req.AfterScheduleID != nil {
			sched.AfterScheduleID = strings.TrimSpace(*req.AfterScheduleID)
		}
		if req.AfterScheduleIDs != nil {
			replaceScheduleDependencyIDs(sched, req.AfterScheduleIDs, req.AfterScheduleID != nil)
		}
		if req.AfterTerminalStatus != nil {
			sched.AfterTerminalStatus = strings.TrimSpace(*req.AfterTerminalStatus)
		}
		if req.AfterDelayMinutes != nil {
			sched.AfterDelayMinutes = *req.AfterDelayMinutes
		}
		if req.DependencyDeadline != nil {
			sched.DependencyDeadline = strings.TrimSpace(*req.DependencyDeadline)
		}
		// A persisted legacy "full" was not chosen in this request; save it as
		// its effective value. Explicitly requesting full is still rejected.
		if req.PulseMode == nil {
			sched.PulseMode = schedulepolicy.NormalizePulse(sched.PulseMode)
		}
		// Leaving legacy full keeps the workflow's review: it now runs on the
		// workflow's own Pulse schedule, so record that intent explicitly.
		if legacyFull && sched.Enabled && sched.PulseMode != schedulepolicy.LegacyFullPulseMode {
			if manifest.Pulse == nil {
				manifest.Pulse = &WorkflowPulseConfig{}
			}
			manifest.Pulse.Enabled = true
		}
		if err := schedulepolicy.ValidatePulse(sched.PulseMode, sched.PulseModeReason); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		validGroupNames, err := validateScheduleGroupNamesForWorkspace(r.Context(), workspacePath, sched.GroupNames)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sched.GroupNames = validGroupNames
		if err := validateScheduleRequest(scheduleTypeOrDefault(sched.ScheduleType), sched.CronExpression, sched.CalendarItems); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := ValidateManifest(manifest); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := WriteWorkflowManifest(r.Context(), workspacePath, manifest); err != nil {
			http.Error(w, "failed to write manifest: "+err.Error(), http.StatusInternalServerError)
			return
		}
		svc.InvalidateWorkflowManifestCache()

		if err := svc.ReloadSchedule(r.Context(), workspacePath, id); err != nil {
			scheduleLogf("[SCHEDULER] Failed to reload schedule %s after update: %v", id, err)
		}

		state := svc.GetRuntimeStateForWorkflow(workspacePath, id)
		missedResolver := newWorkflowMissedStatusResolver(r.Context(), svc)
		resp := buildJobResponse(workspacePath, manifest, *sched, state, missedResolver.get(workspacePath, *sched))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func deleteScheduledJobHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		id := mux.Vars(r)["id"]
		if handleProductScheduleJob(w, r, svc, id, "delete") {
			return
		}

		result, err := findScheduleByIDAny(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !requireWorkflowOwner(w, r, result.WorkspacePath) {
			return
		}

		_ = svc.RemoveWorkflowJob(result.WorkspacePath, id)
		manifest := result.Manifest
		manifest.Schedules = append(manifest.Schedules[:result.Index], manifest.Schedules[result.Index+1:]...)
		if err := WriteWorkflowManifest(r.Context(), result.WorkspacePath, manifest); err != nil {
			http.Error(w, "failed to write manifest: "+err.Error(), http.StatusInternalServerError)
			return
		}
		svc.InvalidateWorkflowManifestCache()

		w.WriteHeader(http.StatusNoContent)
	}
}

func enableScheduledJobHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		id := mux.Vars(r)["id"]
		if handleProductScheduleJob(w, r, svc, id, "enable") {
			return
		}

		result, err := findScheduleByIDAny(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !requireWorkflowOwner(w, r, result.WorkspacePath) {
			return
		}

		result.Manifest.Schedules[result.Index].Enabled = true
		if err := WriteWorkflowManifest(r.Context(), result.WorkspacePath, result.Manifest); err != nil {
			http.Error(w, "failed to write manifest: "+err.Error(), http.StatusInternalServerError)
			return
		}
		svc.InvalidateWorkflowManifestCache()
		if err := svc.ReloadSchedule(r.Context(), result.WorkspacePath, id); err != nil {
			scheduleLogf("[SCHEDULER] Failed to reload schedule %s after enable: %v", id, err)
		}
		state := svc.GetRuntimeStateForWorkflow(result.WorkspacePath, id)
		missedResolver := newWorkflowMissedStatusResolver(r.Context(), svc)
		sched := result.Manifest.Schedules[result.Index]
		resp := buildJobResponse(result.WorkspacePath, result.Manifest, sched, state, missedResolver.get(result.WorkspacePath, sched))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func disableScheduledJobHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		id := mux.Vars(r)["id"]
		if handleProductScheduleJob(w, r, svc, id, "disable") {
			return
		}

		result, err := findScheduleByIDAny(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !requireWorkflowOwner(w, r, result.WorkspacePath) {
			return
		}

		state := runtimeStateForScheduleResult(svc, result, id)
		_ = svc.RemoveWorkflowJob(result.WorkspacePath, id)
		result.Manifest.Schedules[result.Index].Enabled = false
		if err := WriteWorkflowManifest(r.Context(), result.WorkspacePath, result.Manifest); err != nil {
			http.Error(w, "failed to write manifest: "+err.Error(), http.StatusInternalServerError)
			return
		}
		svc.InvalidateWorkflowManifestCache()
		missedResolver := newWorkflowMissedStatusResolver(r.Context(), svc)
		sched := result.Manifest.Schedules[result.Index]
		resp := buildJobResponse(result.WorkspacePath, result.Manifest, sched, state, missedResolver.get(result.WorkspacePath, sched))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func triggerScheduledJobHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		id := mux.Vars(r)["id"]
		if handleProductScheduleJob(w, r, svc, id, "trigger") {
			return
		}

		result, err := findScheduleByIDAny(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !requireWorkflowVisible(w, r, result.WorkspacePath) {
			return
		}
		trigResult, err := svc.TriggerNow(result.WorkspacePath, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"session_id": trigResult})
	}
}

// resetHistoryScheduledJobHandler clears an isolated product schedule's own
// conversation (see ProductScheduleService.ResetHistory) — a workflow
// schedule has no equivalent, so this is product-only.
func resetHistoryScheduledJobHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		id := mux.Vars(r)["id"]
		if handleProductScheduleJob(w, r, svc, id, "reset_history") {
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func stopScheduledJobHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		id := mux.Vars(r)["id"]
		if handleProductScheduleJob(w, r, svc, id, "stop") {
			return
		}
		result, err := findScheduleByIDAny(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !requireWorkflowVisible(w, r, result.WorkspacePath) {
			return
		}

		state := runtimeStateForScheduleResult(svc, result, id)
		if state.LastStatus != "running" {
			http.Error(w, "job is not running", http.StatusBadRequest)
			return
		}

		runtimeKey := workflowScheduleRuntimeKey(result.WorkspacePath, id)
		svc.StopRunningJobForWorkflow(result.WorkspacePath, id)

		durationMs := int64(0)
		svc.updateRuntimeState(runtimeKey, func(state *ScheduleRuntimeState) {
			if state.LastRunAt != nil {
				durationMs = time.Since(*state.LastRunAt).Milliseconds()
			}
			state.LastStatus = "stopped"
			state.LastError = "stopped by user"
			state.LastDurationMs = &durationMs
		})

		// Update the latest workflow run entry in the same resolved scope.
		if result.WorkspacePath != "" {
			runs, err := ReadScheduleRuns(r.Context(), result.WorkspacePath)
			if err == nil && len(runs) > 0 {
				for i := range runs {
					if runs[i].ScheduleID == id && runs[i].Status == "running" {
						_ = UpdateScheduleRun(r.Context(), result.WorkspacePath, runs[i].ID, "stopped", "stopped by user", &durationMs, "", "")
						break
					}
				}
			}
		}

		updatedState := runtimeStateForScheduleResult(svc, result, id)
		missedResolver := newWorkflowMissedStatusResolver(r.Context(), svc)
		sched := result.Manifest.Schedules[result.Index]
		resp := buildJobResponse(result.WorkspacePath, result.Manifest, sched, updatedState, missedResolver.get(result.WorkspacePath, sched))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func getScheduledJobRunsHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		id := mux.Vars(r)["id"]
		if handleProductScheduleJob(w, r, svc, id, "runs") {
			return
		}

		limit := 50
		offset := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		if v := r.URL.Query().Get("offset"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				offset = n
			}
		}

		workspacePath := svc.GetWorkspaceForSchedule(id)
		if workspacePath == "" {
			result, findErr := findScheduleByIDAny(r.Context(), id)
			if findErr != nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			workspacePath = result.WorkspacePath
		}
		_ = svc.reconcileWorkflowScheduleRuns(r.Context(), workspacePath, id)
		runs, total, err := ListScheduleRuns(r.Context(), workspacePath, id, limit, offset)

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Map to response format compatible with frontend ScheduledJobRun
		type RunResponse struct {
			ID                       string              `json:"id"`
			JobID                    string              `json:"job_id"`
			TriggerSource            string              `json:"trigger_source,omitempty"`
			Webhook                  *WebhookRunMetadata `json:"webhook,omitempty"`
			ScheduledFor             *time.Time          `json:"scheduled_for,omitempty"`
			RunFolder                string              `json:"run_folder,omitempty"`
			ConcurrencyMode          string              `json:"concurrency_mode,omitempty"`
			ParallelRiskAcknowledged bool                `json:"parallel_risk_acknowledged,omitempty"`
			ArtifactsExpired         bool                `json:"artifacts_expired,omitempty"`
			SessionID                string              `json:"session_id,omitempty"`
			Status                   string              `json:"status"`
			Error                    string              `json:"error,omitempty"`
			DurationMs               *int64              `json:"duration_ms,omitempty"`
			GroupNames               []string            `json:"group_names,omitempty"`
			StartedAt                time.Time           `json:"started_at"`
			CompletedAt              *time.Time          `json:"completed_at,omitempty"`
		}

		var respRuns []RunResponse
		for _, run := range runs {
			respRuns = append(respRuns, RunResponse{
				ID:                       run.ID,
				JobID:                    id,
				TriggerSource:            run.TriggerSource,
				Webhook:                  run.Webhook,
				ScheduledFor:             run.ScheduledFor,
				RunFolder:                run.RunFolder,
				ConcurrencyMode:          run.ConcurrencyMode,
				ParallelRiskAcknowledged: run.ParallelRiskAcknowledged,
				ArtifactsExpired:         scheduleArtifactsExpired(workspacePath, run.ID) || webhookArtifactsExpired(workspacePath, run.ID),
				SessionID:                run.SessionID,
				Status:                   run.Status,
				Error:                    run.Error,
				DurationMs:               run.DurationMs,
				GroupNames:               run.GroupNames,
				StartedAt:                run.StartedAt,
				CompletedAt:              run.CompletedAt,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"runs":   respRuns,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		})
	}
}

// productScheduleJobResponses lists the requesting user's product schedules
// in the same shape as workflow schedules.
func productScheduleJobResponses(r *http.Request, svc *SchedulerService, enabledFilter string) ([]ScheduledJobResponse, error) {
	if svc == nil || svc.api == nil || svc.api.productSchedules == nil {
		return nil, nil
	}
	ps := svc.api.productSchedules
	userID := productWorkspaceUserID(r.Context())
	jobs, err := ps.JobsForUser(r.Context(), userID)
	if err != nil {
		return nil, err
	}
	var out []ScheduledJobResponse
	for _, job := range jobs {
		if enabledFilter != "" {
			wantEnabled := enabledFilter == "true" || enabledFilter == "1"
			if job.Effective().Enabled != wantEnabled {
				continue
			}
		}
		runsWorkspace, _ := ps.RunsWorkspace(r.Context(), job)
		out = append(out, ps.jobResponse(job, runsWorkspace))
	}
	return out, nil
}

// handleProductScheduleJob serves the per-job scheduler routes for product
// schedule ids ("product:<profile>:<schedule>"); false means the id is a
// workflow schedule and the caller continues as before.
func handleProductScheduleJob(w http.ResponseWriter, r *http.Request, svc *SchedulerService, id, action string) bool {
	_, _, productOK := parseProductScheduleJobID(id)
	_, _, _, projectOK := parseProjectScheduleJobID(id)
	if !productOK && !projectOK {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	if svc == nil || svc.api == nil || svc.api.productSchedules == nil {
		http.Error(w, "product schedules are not available", http.StatusNotFound)
		return true
	}
	ps := svc.api.productSchedules
	ctx := r.Context()
	userID := productWorkspaceUserID(ctx)
	job, err := ps.Job(ctx, userID, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return true
	}
	respond := func(job productScheduleJob) {
		runsWorkspace, _ := ps.RunsWorkspace(ctx, job)
		_ = json.NewEncoder(w).Encode(ps.jobResponse(job, runsWorkspace))
	}
	switch action {
	case "get":
		respond(job)
	case "enable", "disable":
		updated, err := ps.SetEnabled(ctx, userID, id, action == "enable")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return true
		}
		respond(updated)
	case "trigger":
		sessionID, err := ps.Trigger(ctx, userID, id)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, productschedule.ErrAlreadyRunning) {
				status = http.StatusConflict
			}
			http.Error(w, err.Error(), status)
			return true
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"session_id": sessionID})
	case "stop":
		if !ps.Stop(userID, id) {
			http.Error(w, "job is not running", http.StatusBadRequest)
			return true
		}
		respond(job)
	case "reset_history":
		if err := ps.ResetHistory(ctx, userID, id); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, productschedule.ErrAlreadyRunning) {
				status = http.StatusConflict
			}
			http.Error(w, err.Error(), status)
			return true
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	case "runs":
		limit, offset := 50, 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		if v := r.URL.Query().Get("offset"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				offset = n
			}
		}
		runsWorkspace, err := ps.RunsWorkspace(ctx, job)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return true
		}
		runs, total, err := ListScheduleRuns(ctx, runsWorkspace, id, limit, offset)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return true
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"runs": runs, "total": total, "limit": limit, "offset": offset})
	case "update":
		if !projectOK {
			http.Error(w, "product schedules are declared by the product and cannot be edited here", http.StatusMethodNotAllowed)
			return true
		}
		var req UpdateScheduleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return true
		}
		var requestedIsolation *bool
		if req.RunDestination != nil {
			isolated, destinationErr := isolatedForRunDestination(*req.RunDestination)
			if destinationErr != nil {
				http.Error(w, destinationErr.Error(), http.StatusBadRequest)
				return true
			}
			requestedIsolation = &isolated
		}
		updated, err := ps.UpdateProjectSchedule(ctx, userID, id, func(schedule *productschedule.Schedule) {
			if req.Name != "" {
				schedule.Name = req.Name
			}
			if req.Description != "" {
				schedule.Description = req.Description
			}
			if req.CronExpression != "" {
				schedule.CronExpression = req.CronExpression
			}
			if req.Timezone != "" {
				schedule.Timezone = req.Timezone
			}
			if req.Enabled != nil {
				schedule.Enabled = *req.Enabled
			}
			if req.Messages != nil {
				schedule.Messages = req.Messages
			}
			if requestedIsolation != nil {
				schedule.Isolated = *requestedIsolation
			}
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return true
		}
		runsWorkspace, _ := ps.RunsWorkspace(ctx, updated)
		_ = json.NewEncoder(w).Encode(ps.jobResponse(updated, runsWorkspace))
	case "delete":
		if !projectOK {
			http.Error(w, "product schedules are declared by the product and cannot be deleted here", http.StatusMethodNotAllowed)
			return true
		}
		if err := ps.DeleteProjectSchedule(ctx, userID, id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		// A product declares its schedules in product.yaml; users only
		// enable, disable and trigger them.
		http.Error(w, "product schedules are declared by the product and cannot be edited here", http.StatusMethodNotAllowed)
	}
	return true
}
