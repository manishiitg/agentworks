package server

import (
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
)

// HTTP fields are a projection of the common when.after record, never another
// saved dependency policy. Keep the existing workflow adapter on that policy.
func scheduleWhenAfter(schedule WorkflowSchedule) *projectactions.After {
	ids := scheduleDependencyIDs(schedule)
	outcome, deadline := strings.TrimSpace(schedule.AfterTerminalStatus), strings.TrimSpace(schedule.DependencyDeadline)
	if len(ids) == 0 && outcome == "" && deadline == "" && schedule.AfterDelayMinutes == 0 {
		return nil
	}
	return &projectactions.After{Schedules: ids, Outcome: outcome, DelayMinutes: schedule.AfterDelayMinutes, Deadline: deadline}
}

func updateCrewScheduleDependencies(schedule *productschedule.Schedule, req UpdateScheduleRequest) {
	if req.AfterScheduleID == nil && req.AfterScheduleIDs == nil && req.AfterTerminalStatus == nil && req.AfterDelayMinutes == nil && req.DependencyDeadline == nil {
		return
	}
	when := projectactions.When{}
	if schedule.When != nil {
		when = *schedule.When
	}
	projection := WorkflowSchedule{}
	if after := when.After; after != nil {
		projection.AfterScheduleIDs, projection.AfterTerminalStatus = after.Schedules, after.Outcome
		projection.AfterDelayMinutes, projection.DependencyDeadline = after.DelayMinutes, after.Deadline
	}
	applyScheduleDependencyUpdate(&projection, req)
	when.After = scheduleWhenAfter(projection)
	schedule.When = &when
}

// API callers use the job IDs returned by list; SQLite keeps the schedule ID
// inside its owning Crew scope. Never turn another Crew's job into a local ID.
func normalizeCrewScheduleDependencies(schedule *productschedule.Schedule, profileID, crewID string) error {
	if schedule.When == nil || schedule.When.After == nil {
		return nil
	}
	when, after := *schedule.When, *schedule.When.After
	after.Schedules = nil
	seen := map[string]bool{}
	for _, input := range schedule.When.After.Schedules {
		id := strings.TrimSpace(input)
		if strings.HasPrefix(id, projectScheduleJobPrefix) {
			profile, crew, raw, ok := parseProjectScheduleJobID(id)
			if !ok || profile != profileID || crew != crewID {
				return fmt.Errorf("Choose a prerequisite schedule from this Crew.")
			}
			id = raw
		}
		if !seen[id] {
			after.Schedules = append(after.Schedules, id)
			seen[id] = true
		}
	}
	when.After, schedule.When = &after, &when
	return nil
}

func validateCrewScheduleDependencies(schedules []productschedule.Schedule) error {
	dependencies := make(map[string][]string, len(schedules))
	names := make(map[string]string, len(schedules))
	for _, schedule := range schedules {
		names[schedule.ID] = schedule.Name
		if schedule.When != nil && schedule.When.After != nil {
			dependencies[schedule.ID] = schedule.When.After.Schedules
		}
	}
	return validateScheduleDependencyGraph(dependencies, names)
}
