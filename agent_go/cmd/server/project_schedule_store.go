package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
)

// scheduleExecutionInput is passed to existing product executors. It contains
// work, not a second scheduler policy. Team stamps remain server-owned.
type scheduleExecutionInput struct {
	Messages           []string                 `json:"messages,omitempty"`
	Groups             []string                 `json:"groups,omitempty"`
	Routes             map[string]string        `json:"routes,omitempty"`
	Payload            json.RawMessage          `json:"payload,omitempty"`
	Condition          *conditiontrigger.Config `json:"condition,omitempty"`
	TeamAuthorizedBy   string                   `json:"team_authorized_by,omitempty"`
	TeamPolicyRevision int                      `json:"team_policy_revision,omitempty"`
}

func readProjectSchedules(ctx context.Context, root string) ([]projectactions.Schedule, error) {
	raw, found, err := readFileFromWorkspace(ctx, path.Join(root, "schedules.json"))
	if err != nil || !found {
		return nil, err
	}
	var doc projectactions.Schedules
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("read project schedules: %w", err)
	}
	seen := map[string]bool{}
	for _, schedule := range doc.Schedules {
		if err := schedule.Validate(); err != nil {
			return nil, fmt.Errorf("schedule %s: %w", schedule.ID, err)
		}
		if seen[schedule.ID] {
			return nil, fmt.Errorf("duplicate schedule %s", schedule.ID)
		}
		seen[schedule.ID] = true
	}
	return doc.Schedules, nil
}

func writeProjectSchedules(ctx context.Context, root string, schedules []projectactions.Schedule) error {
	if schedules == nil {
		schedules = []projectactions.Schedule{}
	}
	for _, schedule := range schedules {
		if err := schedule.Validate(); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(projectactions.Schedules{Schedules: schedules}, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	file := path.Join(root, "schedules.json")
	previous, found, err := readFileFromWorkspace(ctx, file)
	if err != nil {
		return err
	}
	if found && strings.TrimSpace(previous) == strings.TrimSpace(string(raw)) {
		return nil
	}
	return writeFileToWorkspace(ctx, file, string(raw))
}

// WorkflowSchedule and productschedule.Schedule are executor/view projections.
// Only projectactions.Schedule is persisted for timers. There is deliberately
// no reader for the retired timer arrays in product/workflow.json.
func workflowScheduleFromAction(record projectactions.Schedule) (WorkflowSchedule, error) {
	var input scheduleExecutionInput
	if err := json.Unmarshal(record.Input, &input); err != nil {
		return WorkflowSchedule{}, err
	}
	s := WorkflowSchedule{ID: record.ID, Name: record.Name, Enabled: record.Enabled,
		CronExpression: record.When.Cron, Timezone: record.When.Timezone,
		Mode: "workshop", WorkshopMode: "run", CollisionPolicy: "queue_latest",
		Messages: input.Messages, GroupNames: input.Groups, RouteSelections: input.Routes,
		TriggerPayload: input.Payload, TeamAuthorizedBy: input.TeamAuthorizedBy,
		TeamPolicyRevision: input.TeamPolicyRevision, RunAs: record.RunAs}
	if record.Action.Type == "message" {
		s.WorkshopMode = "workshop"
		s.ChatKey, s.Conversation = record.Action.Tab, record.Action.Conversation
	}
	if len(record.When.Dates) > 0 {
		s.ScheduleType = "calendar"
		for _, date := range record.When.Dates {
			var dated scheduleExecutionInput
			if len(date.Input) > 0 {
				if err := json.Unmarshal(date.Input, &dated); err != nil {
					return WorkflowSchedule{}, err
				}
			}
			s.CalendarItems = append(s.CalendarItems, CalendarScheduleItem{ID: date.ID, Date: date.Date,
				Time: date.Time, Messages: dated.Messages, TriggerPayload: dated.Payload})
		}
	}
	if after := record.When.After; after != nil {
		s.AfterScheduleIDs = after.Schedules
		s.AfterTerminalStatus = after.Outcome
		s.AfterDelayMinutes = after.DelayMinutes
		s.DependencyDeadline = after.Deadline
	}
	return s, nil
}

func actionFromWorkflowSchedule(project projectactions.Project, caller string, s WorkflowSchedule) (projectactions.Schedule, error) {
	input := scheduleExecutionInput{Messages: s.Messages, Groups: s.GroupNames, Routes: s.RouteSelections,
		Payload: s.TriggerPayload, TeamAuthorizedBy: s.TeamAuthorizedBy, TeamPolicyRevision: s.TeamPolicyRevision}
	raw, err := json.Marshal(input)
	if err != nil {
		return projectactions.Schedule{}, err
	}
	record := projectactions.Schedule{ID: s.ID, Name: s.Name, Project: project, Enabled: s.Enabled,
		RunAs: caller, Input: raw, Action: projectactions.Action{Type: "run"},
		When: projectactions.When{Cron: s.CronExpression, Timezone: scheduleTimezoneOrDefault(s.Timezone)}}
	if len(s.Messages) > 0 {
		// Only a message schedule has a chat; a plan run always starts its own run.
		record.Action = projectactions.Action{Type: "message", Tab: s.ChatKey, Conversation: s.Conversation}
	}
	if s.RunAs != "" {
		record.RunAs = s.RunAs
	}
	for _, item := range s.CalendarItems {
		input, err := json.Marshal(scheduleExecutionInput{Messages: item.Messages, Payload: item.TriggerPayload})
		if err != nil {
			return record, err
		}
		record.When.Dates = append(record.When.Dates, projectactions.Date{ID: item.ID, Date: item.Date, Time: item.Time, Input: input})
	}
	if len(record.When.Dates) > 0 {
		record.When.Cron = ""
	}
	if ids := scheduleDependencyIDs(s); len(ids) > 0 {
		record.When.After = &projectactions.After{Schedules: ids, Outcome: s.AfterTerminalStatus,
			DelayMinutes: s.AfterDelayMinutes, Deadline: s.DependencyDeadline}
	}
	return record, record.Validate()
}

func loadWorkflowTimerActions(ctx context.Context, root string, manifest *WorkflowManifest) error {
	if isProductRuntimeManifestWorkspace(root) {
		return nil
	}
	records, err := readProjectSchedules(ctx, root)
	if err != nil {
		return err
	}
	hooks := make([]WorkflowSchedule, 0, len(manifest.Schedules))
	for _, s := range manifest.Schedules {
		if s.ScheduleType == "webhook" {
			hooks = append(hooks, s)
		}
	}
	manifest.Schedules = hooks
	expectedKind := "goal"
	if manifest.Kind == "relay" {
		expectedKind = "relay"
	}
	for _, record := range records {
		if record.Project.ID != manifest.ID || record.Project.Kind != expectedKind {
			return fmt.Errorf("schedule project does not match its Goal or Relay")
		}
		owners := manifest.effectiveOwners()
		if (len(owners) == 0 && (workflowExecutionOwnerUserID(manifest) == "" || record.RunAs != workflowExecutionOwnerUserID(manifest))) ||
			(len(owners) > 0 && !containsID(owners, record.RunAs)) {
			return fmt.Errorf("schedule caller no longer owns this project; save it with a current owner")
		}
		s, err := workflowScheduleFromAction(record)
		if err != nil {
			return err
		}
		manifest.Schedules = append(manifest.Schedules, s)
	}
	return nil
}

func saveWorkflowTimerActions(ctx context.Context, root string, manifest *WorkflowManifest) error {
	if isProductRuntimeManifestWorkspace(root) {
		return nil
	}
	project := projectactions.Project{Kind: "goal", ID: manifest.ID}
	if manifest.Kind == "relay" {
		project.Kind = "relay"
	}
	var records []projectactions.Schedule
	for _, s := range manifest.Schedules {
		if s.ScheduleType == "webhook" {
			continue
		}
		caller := workflowExecutionOwnerUserID(manifest)
		if s.RunAs != "" {
			owners := manifest.effectiveOwners()
			if (len(owners) == 0 && s.RunAs != caller) || (len(owners) > 0 && !containsID(owners, s.RunAs)) {
				return fmt.Errorf("schedule caller no longer owns this project; save it with a current owner")
			}
		}
		record, err := actionFromWorkflowSchedule(project, caller, s)
		if err != nil {
			return err
		}
		records = append(records, record)
	}
	if err := writeProjectSchedules(ctx, root, records); err != nil {
		return err
	}
	notifyAgentWorksGoalChanged(manifest)
	return nil
}

func loadCrewTimerActions(ctx context.Context, root string, manifest *productProjectManifest) error {
	records, err := readProjectSchedules(ctx, root)
	if err != nil {
		return err
	}
	manifest.Schedules = nil
	for _, record := range records {
		if record.Project.Kind != "crew" || record.Project.ID != manifest.ID {
			return fmt.Errorf("schedule project does not match its Crew")
		}
		// The registry decides who owns a Crew, never its manifest (PLAT-449).
		owner, registered := crewProjectOwnerID(root)
		if !registered {
			owner = manifest.OwnerID
		}
		if owner == "" && !IsMultiUserMode() {
			owner = GetDefaultUserID()
		}
		if owner == "" || record.RunAs != owner {
			return fmt.Errorf("schedule caller does not match its Crew owner")
		}
		var input scheduleExecutionInput
		if err := json.Unmarshal(record.Input, &input); err != nil {
			return err
		}
		s := productschedule.Schedule{ID: record.ID, Name: record.Name, Enabled: record.Enabled,
			CronExpression: record.When.Cron, Timezone: record.When.Timezone, CreatedAt: record.When.Anchor,
			Messages: input.Messages, Condition: input.Condition, ChatKey: record.Action.Tab,
			Isolated: record.Action.Conversation == "isolated", When: &record.When}
		manifest.Schedules = append(manifest.Schedules, s)
	}
	return nil
}

func saveCrewTimerActions(ctx context.Context, root string, manifest productProjectManifest) error {
	owner, registered := crewProjectOwnerID(root)
	if !registered {
		owner = manifest.OwnerID
	}
	var records []projectactions.Schedule
	for _, s := range manifest.Schedules {
		raw, err := json.Marshal(scheduleExecutionInput{Messages: s.Messages, Condition: s.Condition})
		if err != nil {
			return err
		}
		record := projectactions.Schedule{ID: s.ID, Name: s.Name, Enabled: s.Enabled, RunAs: owner,
			Project: projectactions.Project{Kind: "crew", ID: manifest.ID}, Input: raw,
			Action: projectactions.Action{Type: "message", Tab: s.ChatKey},
			When:   projectactions.When{Cron: s.CronExpression, Timezone: scheduleTimezoneOrDefault(s.Timezone), Anchor: s.CreatedAt}}
		if record.RunAs == "" && !IsMultiUserMode() {
			record.RunAs = GetDefaultUserID()
		}
		if s.Isolated {
			record.Action.Conversation = "isolated"
		}
		if s.When != nil {
			record.When = *s.When
			record.When.Cron = s.CronExpression
			record.When.Timezone = scheduleTimezoneOrDefault(s.Timezone)
		}
		if s.CronExpression != "" {
			record.When.Dates = nil
		}
		if s.RunAt != "" {
			at, err := time.Parse(time.RFC3339, s.RunAt)
			if err != nil {
				return err
			}
			record.When.Timezone = scheduleTimezoneOrDefault(s.Timezone)
			loc, err := time.LoadLocation(record.When.Timezone)
			if err != nil {
				return err
			}
			at = at.In(loc)
			record.When.Cron = ""
			record.When.Dates = []projectactions.Date{{Date: at.Format("2006-01-02"), Time: at.Format("15:04")}}
		}
		records = append(records, record)
	}
	return writeProjectSchedules(ctx, root, records)
}
