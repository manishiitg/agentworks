package server

import (
	"strings"
	"testing"
)

func TestScheduledWorkshopMessagesPassesDeclaredRouteSelections(t *testing.T) {
	got := scheduledWorkshopMessages(&ScheduleContext{Schedule: WorkflowSchedule{
		GroupNames:      []string{"draft"},
		RouteSelections: map[string]string{"step-router": "draft-only"},
	}})
	if len(got) != 1 {
		t.Fatalf("messages = %d, want 1", len(got))
	}
	for _, want := range []string{"[\"draft\"]", `"step-router":"draft-only"`, "run_full_workflow"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("generated trigger missing %q: %s", want, got[0])
		}
	}
}

func TestScheduledWorkshopMessagesRunsRouteBeforeCustomFollowUp(t *testing.T) {
	got := scheduledWorkshopMessages(&ScheduleContext{Schedule: WorkflowSchedule{
		GroupNames:      []string{"default"},
		RouteSelections: map[string]string{"step-router": "execution"},
		Messages:        []string{"After the selected work completes, report the result."},
	}})
	if len(got) != 2 {
		t.Fatalf("messages = %d, want route then follow-up: %v", len(got), got)
	}
	for _, want := range []string{"run_full_workflow", `"step-router":"execution"`} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("route trigger missing %q: %s", want, got[0])
		}
	}
	if got[1] != "After the selected work completes, report the result." {
		t.Fatalf("follow-up = %q", got[1])
	}
}

func TestManifestRefusesExtraFullRunMessageForSavedRoute(t *testing.T) {
	for _, tc := range []struct {
		name     string
		routes   map[string]string
		messages []string
		wantErr  bool
	}{
		{"duplicate tool call", map[string]string{"router": "standard"}, []string{`Call run_full_workflow(group_name="default", route_selections={"router":"standard"})`}, true},
		{"tool name without arguments", map[string]string{"router": "standard"}, []string{"Use run_full_workflow for the daily work."}, true},
		{"genuine follow-up", map[string]string{"router": "standard"}, []string{"After the selected work completes, report the result."}, false},
		{"route only", map[string]string{"router": "standard"}, nil, false},
		{"direct message only", nil, []string{`Call run_full_workflow(group_name="default")`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := NewWorkflowManifest("Scheduled work")
			manifest.Schedules = []WorkflowSchedule{{
				ID: "daily", Name: "Daily work", CronExpression: "0 9 * * *", Timezone: "UTC",
				GroupNames: []string{"default"}, RouteSelections: tc.routes, Messages: tc.messages,
			}}
			err := ValidateManifest(manifest)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), `Schedule "Daily work" already runs the workflow; remove its extra 'run the full workflow' message`) {
					t.Fatalf("validation = %v, want duplicate-run refusal", err)
				}
			} else if err != nil {
				t.Fatalf("valid schedule rejected: %v", err)
			}
		})
	}
}

func TestValidateScheduleMessagesRequiresReasonForInlineProcedure(t *testing.T) {
	procedure := []string{"Run sqlite3 db/db.sqlite SELECT * FROM queue"}
	if err := validateScheduleMessages(procedure, ""); err == nil {
		t.Fatal("inline SQL procedure without rationale unexpectedly accepted")
	}
	if err := validateScheduleMessages(procedure, "This is a one-off schedule-specific reconciliation conversation; it is not reusable plan behavior."); err != nil {
		t.Fatalf("intentional direct procedure rejected: %v", err)
	}
	if err := validateScheduleMessages([]string{"Run the selected workflow route."}, ""); err != nil {
		t.Fatalf("compact trigger rejected: %v", err)
	}
}

func TestValidateScheduleMessagesRequiresReasonForSequentialConversation(t *testing.T) {
	messages := []string{"Collect the current evidence.", "Challenge the result and summarize it."}
	if err := validateScheduleMessages(messages, ""); err == nil {
		t.Fatal("multi-message direct conversation without rationale unexpectedly accepted")
	}
	reason := "The second turn intentionally critiques transient context from the first and is unique to this calendar event."
	if err := validateScheduleMessages(messages, reason); err != nil {
		t.Fatalf("direct conversation with rationale rejected: %v", err)
	}
	if advisory := scheduleMessagesAdvisory(messages, reason); !strings.Contains(advisory, "not canonical plan steps") {
		t.Fatalf("advisory does not disclose lifecycle tradeoff: %q", advisory)
	}
}

func TestVersion133SkipsRetiredPulseBacklogMigration(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractPulseBacklogTriageVersion})
	if len(plan) != 16 || plan[0].label != "upgrade-pulse-actionable-backlog" || plan[0].to != workflowContractPulseActionableBacklogVersion || plan[1].label != "upgrade-orchestrator-step-type" || plan[1].to != workflowContractOrchestratorStepTypeVersion || plan[2].label != "upgrade-activity-tab-from-run-summary" || plan[2].to != workflowContractActivityTabFromRunSummaryVersion || plan[3].label != "upgrade-scripted-type-stays-regular" || plan[3].to != workflowContractScriptedPlanTypeVersion || plan[4].label != "upgrade-declared-execution-mode-retired" || plan[4].to != workflowContractDeclaredExecutionModeRetiredVersion || plan[5].label != "upgrade-declared-execution-mode-stripped" || plan[5].to != workflowContractDeclaredExecutionModeStrippedVersion || plan[6].label != "upgrade-route-summaries" || plan[6].to != workflowContractRouteSummariesVersion {
		t.Fatalf("1.0.33 upgrade plan = %+v, want actionable Autopilot backlog migration then agent step-type migration", plan)
	}
	for _, want := range []string{
		"legacy Autopilot actionable-backlog migration for this version has been retired",
		"Do not inspect or modify Autopilot data",
		`set_workflow_contract_version(version="1.0.34")`,
	} {
		if !strings.Contains(plan[0].query, want) {
			t.Errorf("actionable Autopilot backlog migration missing %q", want)
		}
	}
}

// TestUpgradeQueriesNeverNamePlatTickets guards a real mistake made while
// writing upgradeDedicatedPulseSchedule and workflow-tools.md's
// periodic-Pulse guidance: this text runs live on operators' own workflows,
// on their own machines — an internal ticket number in it is meaningless
// noise to them, not useful context. Scoped to the upgrade query constants
// specifically, not the whole package, since a Go doc comment referencing a
// ticket for engineers reading this source is a different, legitimate thing.
