package server

import (
	"testing"
)

func TestScheduleExpectsWorkflowRun(t *testing.T) {
	plain := &ScheduleContext{Schedule: WorkflowSchedule{ID: "daily", CronExpression: "0 9 * * *"}}
	if !scheduleExpectsWorkflowRun(plain) {
		t.Fatal("a plain workflow schedule exists to run the workflow")
	}
	calendar := &ScheduleContext{Schedule: WorkflowSchedule{ID: "cal", ScheduleType: "calendar", CalendarItems: []CalendarScheduleItem{{Date: "2026-10-01", Time: "09:00"}}}}
	if !scheduleExpectsWorkflowRun(calendar) {
		t.Fatal("a calendar schedule also exists to run the workflow")
	}
	for name, sctx := range map[string]*ScheduleContext{
		"direct messages": {Schedule: WorkflowSchedule{ID: "m", Messages: []string{"Run only step-send-email-outreach"}}},
		"query":           {Schedule: WorkflowSchedule{ID: "q", Query: "summarize"}},
		"execution mode":  {Schedule: WorkflowSchedule{ID: "c", ExecutionMode: "close_only"}},
	} {
		if scheduleExpectsWorkflowRun(sctx) {
			t.Errorf("%s schedule may legitimately run no workflow", name)
		}
	}
}
