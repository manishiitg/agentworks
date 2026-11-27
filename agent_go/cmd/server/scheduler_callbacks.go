package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// buildSchedulerCallbacks creates SchedulerCallbacks that bridge the workshop tools
// to the workflow.json manifest and scheduler service. No database dependency.
func (api *StreamingAPI) buildSchedulerCallbacks() *todo_creation_human.SchedulerCallbacks {
	return &todo_creation_human.SchedulerCallbacks{
		ListSchedules: func(ctx context.Context, workspacePath string) (string, error) {
			manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
			if err != nil {
				return "", fmt.Errorf("current workflow schedules unavailable: %w", err)
			}
			if !found || manifest == nil {
				return "No workflow manifest found; current schedule state is unknown.", nil
			}
			current := readWorkflowSchedulerState(ctx, manifest, time.Now().UTC())
			encoded, err := json.MarshalIndent(current, "", "  ")
			if err != nil {
				return "", err
			}
			var sb strings.Builder
			sb.WriteString("## Current scheduler state\n\n```json\n" + string(encoded) + "\n```\n\n")
			if len(manifest.Schedules) == 0 {
				sb.WriteString("No schedules found for this workflow.\n")
			}
			sb.WriteString(fmt.Sprintf("## Schedules (%d found)\n\n", len(manifest.Schedules)))
			for _, sched := range manifest.Schedules {
				status := "disabled"
				if sched.Enabled {
					status = "enabled"
				}
				scheduleType := scheduleTypeOrDefault(sched.ScheduleType)
				mode := scheduleModeOrDefault(sched.Mode)
				workshopMode := strings.TrimSpace(sched.WorkshopMode)
				if workshopMode == "" {
					workshopMode = "run"
				}
				sb.WriteString(fmt.Sprintf("### %s\n", sched.Name))
				sb.WriteString(fmt.Sprintf("- **ID**: `%s`\n", sched.ID))
				sb.WriteString(fmt.Sprintf("- **Type**: %s\n", scheduleType))
				sb.WriteString(fmt.Sprintf("- **Mode**: `%s`\n", mode))
				sb.WriteString(fmt.Sprintf("- **Workshop Mode**: `%s`\n", workshopMode))
				if scheduleType == "calendar" {
					sb.WriteString(fmt.Sprintf("- **Calendar Items**: %d\n", len(sched.CalendarItems)))
				} else {
					sb.WriteString(fmt.Sprintf("- **Cron**: `%s`\n", sched.CronExpression))
				}
				sb.WriteString(fmt.Sprintf("- **Timezone**: %s\n", sched.Timezone))
				sb.WriteString("- **Timing**: " + scheduleTimingSummary(ScheduledJobResponse{ScheduleType: scheduleType, CronExpression: sched.CronExpression, CalendarItems: sched.CalendarItems, Timezone: sched.Timezone}) + "\n")
				sb.WriteString(fmt.Sprintf("- **Status**: %s\n", status))
				if api.scheduler != nil {
					state := api.scheduler.GetRuntimeStateForWorkflow(workspacePath, sched.ID)
					if state.LastStatus != "" {
						sb.WriteString(fmt.Sprintf("- **Last Run**: %v (status: %s)\n", state.LastRunAt, state.LastStatus))
					}
					if state.NextRunAt != nil {
						sb.WriteString(fmt.Sprintf("- **Nominal Next Run** (runtime projection; see current scheduler state for pause blockers): %v\n", state.NextRunAt))
					}
					sb.WriteString(fmt.Sprintf("- **Run Count**: %d\n", state.RunCount))
				}

				if len(sched.GroupNames) > 0 {
					sb.WriteString(fmt.Sprintf("- **Groups**: %v\n", sched.GroupNames))
				} else {
					sb.WriteString("- **Groups**: all\n")
				}

				if len(sched.RouteSelections) > 0 {
					sb.WriteString(fmt.Sprintf("- **Route selections**: %v\n", sched.RouteSelections))
				}
				if scheduleType == "webhook" {
					sb.WriteString(fmt.Sprintf("- **Delivery concurrency**: parallel, up to %d active deliveries (server-enforced)\n", maxWebhookConcurrency))
					sb.WriteString("- **Overflow policy**: retryable busy response (HTTP 503 with Retry-After)\n")
				} else {
					if strings.TrimSpace(sched.CollisionPolicy) != "" {
						sb.WriteString(fmt.Sprintf("- **Collision policy**: %s\n", sched.CollisionPolicy))
					}
					concurrencyMode := strings.TrimSpace(sched.ConcurrencyMode)
					if concurrencyMode == "" {
						concurrencyMode = "sequential"
					}
					sb.WriteString(fmt.Sprintf("- **Concurrency mode**: %s\n", concurrencyMode))
				}
				if sched.MaxStartDelayMinutes > 0 {
					sb.WriteString(fmt.Sprintf("- **Maximum start delay**: %d minutes\n", sched.MaxStartDelayMinutes))
				}
				if dependencyIDs := scheduleDependencyIDs(sched); len(dependencyIDs) > 0 {
					sb.WriteString(fmt.Sprintf("- **Waits for schedules**: %v\n", dependencyIDs))
					dependencyRelease := strings.TrimSpace(sched.AfterTerminalStatus)
					if dependencyRelease == "" {
						dependencyRelease = "completed"
					}
					sb.WriteString(fmt.Sprintf("- **Dependency release**: %s", dependencyRelease))
					if sched.AfterDelayMinutes > 0 {
						sb.WriteString(fmt.Sprintf(" + %d minute delay", sched.AfterDelayMinutes))
					}
					if strings.TrimSpace(sched.DependencyDeadline) != "" {
						sb.WriteString(fmt.Sprintf("; deadline %s local", sched.DependencyDeadline))
					}
					sb.WriteString("\n")
				}
				if strings.TrimSpace(sched.DirectMessagesReason) != "" {
					sb.WriteString(fmt.Sprintf("- **Direct message rationale**: %s\n", sched.DirectMessagesReason))
				}
				sb.WriteString("\n")
			}
			return sb.String(), nil
		},
		CreateSchedule: func(ctx context.Context, workspacePath, name, cronExpr, timezone string, groupNames []string, routeSelections map[string]string, mode string, messages []string, directMessagesReason string, workshopMode string, resumePrevious *bool, policy todo_creation_human.ScheduleRuntimePolicy) (string, error) {

			mode = scheduleModeOrDefault(mode)
			if mode == "multi-agent" {
				return "", fmt.Errorf("workflow schedules must use workflow mode")
			}
			if err := ValidateCronExpression(cronExpr); err != nil {
				return "", fmt.Errorf("invalid cron expression %q: %w", cronExpr, err)
			}
			if err := ValidateScheduleTimezone(timezone); err != nil {
				return "", err
			}
			if err := validateScheduleMessages(messages, directMessagesReason); err != nil {
				return "", err
			}
			manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
			if err != nil || !found {
				return "", fmt.Errorf("workflow manifest not found at %s", workspacePath)
			}

			groupNames, err = validateScheduleGroupNamesForWorkspace(ctx, workspacePath, groupNames)
			if err != nil {
				return "", err
			}

			newSched := WorkflowSchedule{
				ID:             generateScheduleID(),
				Name:           name,
				CronExpression: cronExpr,
				Timezone:       timezone,
				InputPreset: func() string {
					if policy.InputPreset != nil {
						return *policy.InputPreset
					}
					return ""
				}(),
				Inputs:               policy.Inputs,
				RouteSelections:      routeSelections,
				Enabled:              true,
				Mode:                 mode,
				Messages:             messages,
				DirectMessagesReason: directMessagesReason,
				WorkshopMode:         workshopMode,
				ResumePrevious:       resumePrevious,

				ExecutionMode:            strings.TrimSpace(policy.ExecutionMode),
				CollisionPolicy:          strings.TrimSpace(policy.CollisionPolicy),
				ConcurrencyMode:          strings.TrimSpace(policy.ConcurrencyMode),
				ParallelRiskAcknowledged: policy.ParallelRiskAcknowledged,
				MaxStartDelayMinutes:     policy.MaxStartDelayMinutes,
				AfterScheduleID:          strings.TrimSpace(policy.AfterScheduleID),
				AfterScheduleIDs:         normalizeScheduleDependencyIDs(policy.AfterScheduleIDs),
				AfterTerminalStatus:      strings.TrimSpace(policy.AfterTerminalStatus),
				AfterDelayMinutes:        policy.AfterDelayMinutes,
				DependencyDeadline:       strings.TrimSpace(policy.DependencyDeadline),
			}
			if _, err := resolveWorkflowInvocationInputs(ctx, workspacePath, newSched.InputPreset, newSched.Inputs); err != nil {
				return "", err
			}
			if err := validateScheduleRuntimePolicy(newSched); err != nil {
				return "", err
			}
			if err := applyGoalScheduleDestination(ctx, manifest, workspacePath, &newSched, policy.RunDestination, policy.ChatID); err != nil {
				return "", err
			}
			manifest.Schedules = append(manifest.Schedules, newSched)
			if err := ValidateManifest(manifest); err != nil {
				return "", err
			}
			if err := WriteWorkflowManifest(ctx, workspacePath, manifest); err != nil {
				return "", fmt.Errorf("failed to write manifest: %w", err)
			}
			// Load into gocron scheduler
			if api.scheduler != nil {
				sctx := buildScheduleContext(workspacePath, manifest, newSched)
				if err := api.scheduler.LoadSchedule(sctx); err != nil {
					return fmt.Sprintf("Schedule created (ID: %s) but failed to activate: %v", newSched.ID, err), nil
				}
			}
			nextRun := getNextRunTime(cronExpr, timezone)
			nextRunStr := "unknown"
			if nextRun != nil {
				nextRunStr = nextRun.Format(time.RFC3339)
			}
			result := fmt.Sprintf("Schedule created and activated.\n- **ID**: `%s`\n- **Name**: %s\n- **Cron**: `%s`\n- **Timezone**: %s\n- **Next Run**: %s", newSched.ID, name, cronExpr, timezone, nextRunStr)
			result += "\n- **Timing**: " + scheduleTimingSummary(ScheduledJobResponse{ScheduleType: "cron", CronExpression: cronExpr, Timezone: timezone})
			if advisory := scheduleMessagesAdvisory(messages, directMessagesReason); advisory != "" {
				result += "\n- **Execution model**: " + advisory
			}
			return result, nil
		},
		CreateCalendarSchedule: func(ctx context.Context, workspacePath, name, timezone string, groupNames []string, calendarItemsJSON string, mode string, messages []string, directMessagesReason string, workshopMode string, policy todo_creation_human.ScheduleRuntimePolicy) (string, error) {
			mode = scheduleModeOrDefault(mode)
			if mode == "multi-agent" {
				return "", fmt.Errorf("workflow calendar schedules must use workshop mode")
			}
			if err := ValidateScheduleTimezone(timezone); err != nil {
				return "", err
			}
			var calendarItems []CalendarScheduleItem
			if err := json.Unmarshal([]byte(calendarItemsJSON), &calendarItems); err != nil {
				return "", fmt.Errorf("invalid calendar_items JSON: %w", err)
			}
			calendarItems = normalizeCalendarScheduleItems(calendarItems)
			if err := validateScheduleRequest("calendar", "", calendarItems); err != nil {
				return "", err
			}
			allMessages := append([]string(nil), messages...)
			for _, item := range calendarItems {
				allMessages = append(allMessages, item.Messages...)
			}
			if err := validateScheduleMessages(allMessages, directMessagesReason); err != nil {
				return "", err
			}
			manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
			if err != nil || !found {
				return "", fmt.Errorf("workflow manifest not found at %s", workspacePath)
			}

			groupNames, err = validateScheduleGroupNamesForWorkspace(ctx, workspacePath, groupNames)
			if err != nil {
				return "", err
			}
			newSched := WorkflowSchedule{
				ID:            generateScheduleID(),
				Name:          name,
				ScheduleType:  "calendar",
				Timezone:      timezone,
				CalendarItems: calendarItems,
				InputPreset: func() string {
					if policy.InputPreset != nil {
						return *policy.InputPreset
					}
					return ""
				}(),
				Inputs:               policy.Inputs,
				Enabled:              true,
				Mode:                 mode,
				Messages:             messages,
				DirectMessagesReason: directMessagesReason,
				WorkshopMode:         workshopMode,

				CollisionPolicy:          strings.TrimSpace(policy.CollisionPolicy),
				ConcurrencyMode:          strings.TrimSpace(policy.ConcurrencyMode),
				ParallelRiskAcknowledged: policy.ParallelRiskAcknowledged,
				MaxStartDelayMinutes:     policy.MaxStartDelayMinutes,
				AfterScheduleIDs:         normalizeScheduleDependencyIDs(policy.AfterScheduleIDs),
				AfterTerminalStatus:      strings.TrimSpace(policy.AfterTerminalStatus),
				AfterDelayMinutes:        policy.AfterDelayMinutes,
				DependencyDeadline:       strings.TrimSpace(policy.DependencyDeadline),
			}
			if _, err := resolveWorkflowInvocationInputs(ctx, workspacePath, newSched.InputPreset, newSched.Inputs); err != nil {
				return "", err
			}
			if err := validateScheduleRuntimePolicy(newSched); err != nil {
				return "", err
			}
			manifest.Schedules = append(manifest.Schedules, newSched)
			if err := ValidateManifest(manifest); err != nil {
				return "", err
			}
			if err := WriteWorkflowManifest(ctx, workspacePath, manifest); err != nil {
				return "", fmt.Errorf("failed to write manifest: %w", err)
			}
			if api.scheduler != nil {
				sctx := buildScheduleContext(workspacePath, manifest, newSched)
				if err := api.scheduler.LoadSchedule(sctx); err != nil {
					return fmt.Sprintf("Calendar schedule created (ID: %s) but failed to activate: %v", newSched.ID, err), nil
				}
			}
			nextRun := getNextRunTimeForCalendar(newSched)
			nextRunStr := "unknown"
			if nextRun != nil {
				nextRunStr = nextRun.Format(time.RFC3339)
			}
			result := fmt.Sprintf("Calendar schedule created and activated.\n- **ID**: `%s`\n- **Name**: %s\n- **Items**: %d\n- **Timezone**: %s\n- **Next Run**: %s", newSched.ID, name, len(calendarItems), timezone, nextRunStr)
			result += "\n- **Timing**: " + scheduleTimingSummary(ScheduledJobResponse{ScheduleType: "calendar", CalendarItems: calendarItems, Timezone: timezone})
			if advisory := scheduleMessagesAdvisory(allMessages, directMessagesReason); advisory != "" {
				result += "\n- **Execution model**: " + advisory
			}
			return result, nil
		},
		UpdateSchedule: func(ctx context.Context, jobID, name, cronExpr, timezone string, groupNames []string, setGroupNames bool, routeSelections map[string]string, setRouteSelections bool, enabled *bool, mode string, messages []string, setMessages bool, directMessagesReason *string, workshopMode string, resumePrevious *bool, policy *todo_creation_human.ScheduleRuntimePolicy) (string, error) {
			if cronExpr != "" {
				if err := ValidateCronExpression(cronExpr); err != nil {
					return "", fmt.Errorf("invalid cron expression %q: %w", cronExpr, err)
				}
			}
			if timezone != "" {
				if err := ValidateScheduleTimezone(timezone); err != nil {
					return "", err
				}
			}
			workspacePath, manifest, idx, err := findScheduleByID(ctx, jobID)
			if err != nil {
				return "", fmt.Errorf("schedule not found: %w", err)
			}
			sched := &manifest.Schedules[idx]
			if manifest.Team != nil {
				sched.TeamAuthorizedBy = teamActor(ctx)
				sched.TeamPolicyRevision = manifest.Team.Revision
			}
			if name != "" {
				sched.Name = name
			}
			if cronExpr != "" {
				sched.CronExpression = cronExpr
			}
			if timezone != "" {
				sched.Timezone = timezone
			}

			if setGroupNames {
				validGroupNames, err := validateScheduleGroupNamesForWorkspace(ctx, workspacePath, groupNames)
				if err != nil {
					return "", err
				}
				sched.GroupNames = validGroupNames
			}
			if setRouteSelections {
				sched.RouteSelections = routeSelections
			}
			if enabled != nil {
				sched.Enabled = *enabled
			}
			if mode != "" || sched.Mode == "" || sched.Mode == "workflow" {
				normalizedMode := scheduleModeOrDefault(mode)
				if normalizedMode == "multi-agent" {
					return "", fmt.Errorf("workflow schedules must use workflow mode")
				}
				sched.Mode = normalizedMode
			}
			candidateMessages := sched.Messages
			candidateReason := sched.DirectMessagesReason
			if setMessages {
				candidateMessages = messages
			}
			if directMessagesReason != nil {
				candidateReason = *directMessagesReason
			}
			if setMessages || directMessagesReason != nil {
				if err := validateScheduleMessages(candidateMessages, candidateReason); err != nil {
					return "", err
				}
			}
			if setMessages {
				sched.Messages = messages
			}
			if directMessagesReason != nil {
				sched.DirectMessagesReason = *directMessagesReason
			}
			if workshopMode != "" {
				sched.WorkshopMode = workshopMode
			}
			if resumePrevious != nil {
				sched.ResumePrevious = resumePrevious
			}

			if policy != nil {
				if policy.InputPreset != nil {
					sched.InputPreset = *policy.InputPreset
				}
				if policy.Inputs != nil {
					sched.Inputs = policy.Inputs
				}

				if policy.SetExecutionMode {
					sched.ExecutionMode = strings.TrimSpace(policy.ExecutionMode)
				}
				if policy.SetCollisionPolicy {
					sched.CollisionPolicy = strings.TrimSpace(policy.CollisionPolicy)
				}
				if policy.SetConcurrencyMode {
					sched.ConcurrencyMode = strings.TrimSpace(policy.ConcurrencyMode)
					if sched.ConcurrencyMode != "parallel" && !policy.SetParallelRiskAcknowledged {
						sched.ParallelRiskAcknowledged = false
					}
				}
				if policy.SetParallelRiskAcknowledged {
					sched.ParallelRiskAcknowledged = policy.ParallelRiskAcknowledged
				}
				if policy.SetMaxStartDelayMinutes {
					sched.MaxStartDelayMinutes = policy.MaxStartDelayMinutes
				}
				if policy.SetAfterScheduleID {
					sched.AfterScheduleID = strings.TrimSpace(policy.AfterScheduleID)
				}
				if policy.SetAfterScheduleIDs {
					replaceScheduleDependencyIDs(sched, policy.AfterScheduleIDs, policy.SetAfterScheduleID)
				}
				if policy.SetAfterTerminalStatus {
					sched.AfterTerminalStatus = strings.TrimSpace(policy.AfterTerminalStatus)
				}
				if policy.SetAfterDelayMinutes {
					sched.AfterDelayMinutes = policy.AfterDelayMinutes
				}
				if policy.SetDependencyDeadline {
					sched.DependencyDeadline = strings.TrimSpace(policy.DependencyDeadline)
				}
				if err := validateScheduleRuntimePolicy(*sched); err != nil {
					return "", err
				}
				if err := applyGoalScheduleDestination(ctx, manifest, workspacePath, sched, policy.RunDestination, policy.ChatID); err != nil {
					return "", err
				}
			}

			validGroupNames, err := validateScheduleGroupNamesForWorkspace(ctx, workspacePath, sched.GroupNames)
			if err != nil {
				return "", err
			}
			sched.GroupNames = validGroupNames

			if err := ValidateManifest(manifest); err != nil {
				return "", err
			}
			if err := WriteWorkflowManifest(ctx, workspacePath, manifest); err != nil {
				return "", fmt.Errorf("failed to write manifest: %w", err)
			}
			if api.scheduler != nil {
				if err := api.scheduler.ReloadSchedule(ctx, workspacePath, jobID); err != nil {
					return fmt.Sprintf("Schedule updated but failed to reload: %v", err), nil
				}
			}
			nextRun := getNextRunTime(sched.CronExpression, sched.Timezone)
			nextRunStr := "unknown"
			if nextRun != nil {
				nextRunStr = nextRun.Format(time.RFC3339)
			}
			result := fmt.Sprintf("Schedule updated.\n- **ID**: `%s`\n- **Name**: %s\n- **Cron**: `%s`\n- **Enabled**: %v\n- **Next Run**: %s", sched.ID, sched.Name, sched.CronExpression, sched.Enabled, nextRunStr)
			result += "\n- **Timing**: " + scheduleTimingSummary(ScheduledJobResponse{ScheduleType: sched.ScheduleType, CronExpression: sched.CronExpression, CalendarItems: sched.CalendarItems, Timezone: sched.Timezone})
			if advisory := scheduleMessagesAdvisory(sched.Messages, sched.DirectMessagesReason); advisory != "" {
				result += "\n- **Execution model**: " + advisory
			}
			return result, nil
		},
		DeleteSchedule: func(ctx context.Context, jobID string) error {
			workspacePath, manifest, idx, err := findScheduleByID(ctx, jobID)
			if err != nil {
				return err
			}
			if api.scheduler != nil {
				_ = api.scheduler.RemoveWorkflowJob(workspacePath, jobID)
			}
			manifest.Schedules = append(manifest.Schedules[:idx], manifest.Schedules[idx+1:]...)
			return WriteWorkflowManifest(ctx, workspacePath, manifest)
		},
		TriggerSchedule: func(ctx context.Context, jobID string) (string, error) {
			if api.scheduler == nil {
				return "", fmt.Errorf("scheduler not available")
			}
			workspacePath := api.scheduler.GetWorkspaceForSchedule(jobID)
			if workspacePath == "" {
				wp, _, _, err := findScheduleByID(ctx, jobID)
				if err != nil {
					return "", err
				}
				workspacePath = wp
			}
			if err := authorizeTeamScheduleTrigger(ctx, workspacePath, jobID); err != nil {
				return "", err
			}
			// Pass the chat that asked, so the run's terminals show up in its rail
			// instead of only under the schedule's own session.
			_, err := api.scheduler.TriggerNowFromSession(workspacePath, jobID, chatSessionIDFromContext(ctx))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Schedule triggered. Job ID: `%s`", jobID), nil
		},
		GetScheduleRuns: func(ctx context.Context, jobID string, limit int) (string, error) {
			if limit <= 0 {
				limit = 10
			}
			workspacePath := ""
			if api.scheduler != nil {
				workspacePath = api.scheduler.GetWorkspaceForSchedule(jobID)
			}
			if workspacePath == "" {
				wp, _, _, err := findScheduleByID(ctx, jobID)
				if err != nil {
					return "", err
				}
				workspacePath = wp
			}
			runs, total, err := ListScheduleRuns(ctx, workspacePath, jobID, limit, 0)
			if err != nil {
				return "", err
			}
			var sb strings.Builder
			if len(runs) == 0 {
				sb.WriteString("No runs found for this schedule.\n\n")
			} else {
				sb.WriteString(fmt.Sprintf("## Run History (%d of %d)\n\n", len(runs), total))
				for _, r := range runs {
					duration := ""
					if r.DurationMs != nil {
						duration = fmt.Sprintf(" (%dms)", *r.DurationMs)
					}
					idPrefix := r.ID
					if len(idPrefix) > 8 {
						idPrefix = idPrefix[:8]
					}
					sb.WriteString(fmt.Sprintf("- **%s** [%s]%s — %s", idPrefix, r.Status, duration, r.StartedAt.Format("2006-01-02 15:04:05")))
					if r.RunFolder != "" {
						sb.WriteString(fmt.Sprintf(" → `%s`", r.RunFolder))
					}
					if r.Error != "" {
						sb.WriteString(fmt.Sprintf("\n  Error: %s", r.Error))
					}
					sb.WriteString("\n")
				}
			}
			// Every scheduled occurrence is recorded here, including the ones
			// that never produce a schedule_runs row. They are grouped by what
			// they mean: lost work, deferred work, or deliberately not run
			// (schedule_fire_decision_kind.go).
			if api.scheduler != nil {
				if decisions, decErr := api.scheduler.ListFireDecisions(ctx, workspacePath, jobID, limit); decErr == nil {
					sb.WriteString(formatScheduleNonRunOccurrences(decisions, collisionPolicyForSchedule(ctx, workspacePath, jobID)))
				}
			}
			return sb.String(), nil
		},
	}
}
