package server

import (
	"regexp"
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

func TestWorkflowVersionUpgradePlanAddsScheduledRoutesAfterDirectReports(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractDirectHTMLReportsVersion})
	if len(plan) != 19 || plan[0].label != "upgrade-schedule-execution-model" || plan[0].to != workflowContractScheduleExecutionModelVersion {
		t.Fatalf("unexpected upgrade plan: %+v", plan)
	}
	if plan[1].label != "upgrade-dedicated-pulse-schedule" || plan[1].to != workflowContractDedicatedPulseScheduleVersion {
		t.Fatalf("unexpected dedicated Pulse step: %+v", plan[1])
	}
	if plan[2].label != "upgrade-schedule-prompt-contract" || plan[2].to != workflowContractSchedulePromptContractVersion {
		t.Fatalf("unexpected schedule prompt step: %+v", plan[2])
	}
	if plan[3].label != "upgrade-schedule-finalizer-ownership" || plan[3].to != workflowContractFinalizerOwnedScheduleVersion {
		t.Fatalf("unexpected finalizer-ownership step: %+v", plan[3])
	}
	if plan[4].label != "upgrade-report-activity-section" || plan[4].to != workflowContractReportActivitySectionVersion {
		t.Fatalf("unexpected report-activity-section step: %+v", plan[4])
	}
	if plan[5].label != "upgrade-report-activity-tab" || plan[5].to != workflowContractReportActivityTabVersion {
		t.Fatalf("unexpected report tab upgrade step: %+v", plan[5])
	}
	if plan[6].label != "upgrade-pulse-lifecycle-reconciliation" || plan[6].to != workflowContractPulseLifecycleReconciliationVersion || plan[7].label != "upgrade-pulse-backlog-triage" || plan[7].to != workflowContractPulseBacklogTriageVersion || plan[8].label != "upgrade-pulse-actionable-backlog" || plan[8].to != workflowContractPulseActionableBacklogVersion || plan[9].label != "upgrade-orchestrator-step-type" || plan[9].to != workflowContractOrchestratorStepTypeVersion || plan[10].label != "upgrade-activity-tab-from-run-summary" || plan[10].to != workflowContractActivityTabFromRunSummaryVersion || plan[11].label != "upgrade-scripted-type-stays-regular" || plan[11].to != workflowContractScriptedTypeStaysRegularVersion || plan[12].label != "upgrade-declared-execution-mode-retired" || plan[12].to != workflowContractDeclaredExecutionModeRetiredVersion || plan[13].label != "upgrade-declared-execution-mode-stripped" || plan[13].to != workflowContractDeclaredExecutionModeStrippedVersion || plan[14].label != "upgrade-route-summaries" || plan[14].to != workflowContractRouteSummariesVersion {
		t.Fatalf("unexpected final upgrade step: %+v", plan[6])
	}
}

func TestWorkflowVersionUpgradePlanReauditsEarlierRouteOnlyContract(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractScheduledRouteVersion})
	if len(plan) != 19 || plan[0].label != "upgrade-schedule-execution-model" || plan[0].to != workflowContractScheduleExecutionModelVersion {
		t.Fatalf("1.0.24 workflow did not receive choice-aware schedule audit: %+v", plan)
	}
	for _, want := range []string{"EQUIVALENT ROUTE EXISTS", "DURABLE WORKFLOW BEHAVIOR", "GENUINELY SCHEDULE-SPECIFIC CONVERSATION", "direct_messages_reason"} {
		if !strings.Contains(plan[0].query, want) {
			t.Errorf("choice-aware migration prompt missing %q", want)
		}
	}
	if plan[1].label != "upgrade-dedicated-pulse-schedule" || plan[1].to != workflowContractDedicatedPulseScheduleVersion {
		t.Fatalf("unexpected dedicated Pulse step: %+v", plan[1])
	}
	if plan[2].label != "upgrade-schedule-prompt-contract" || plan[2].to != workflowContractSchedulePromptContractVersion {
		t.Fatalf("unexpected schedule prompt step: %+v", plan[2])
	}
	if plan[3].label != "upgrade-schedule-finalizer-ownership" || plan[3].to != workflowContractFinalizerOwnedScheduleVersion {
		t.Fatalf("unexpected finalizer-ownership step: %+v", plan[3])
	}
	if plan[4].label != "upgrade-report-activity-section" || plan[4].to != workflowContractReportActivitySectionVersion {
		t.Fatalf("unexpected report-activity-section step: %+v", plan[4])
	}
	if plan[5].label != "upgrade-report-activity-tab" || plan[5].to != workflowContractReportActivityTabVersion {
		t.Fatalf("unexpected report tab upgrade step: %+v", plan[5])
	}
	if plan[6].label != "upgrade-pulse-lifecycle-reconciliation" || plan[6].to != workflowContractPulseLifecycleReconciliationVersion || plan[7].label != "upgrade-pulse-backlog-triage" || plan[7].to != workflowContractPulseBacklogTriageVersion || plan[8].label != "upgrade-pulse-actionable-backlog" || plan[8].to != workflowContractPulseActionableBacklogVersion || plan[9].label != "upgrade-orchestrator-step-type" || plan[9].to != workflowContractOrchestratorStepTypeVersion || plan[10].label != "upgrade-activity-tab-from-run-summary" || plan[10].to != workflowContractActivityTabFromRunSummaryVersion || plan[11].label != "upgrade-scripted-type-stays-regular" || plan[11].to != workflowContractScriptedTypeStaysRegularVersion || plan[12].label != "upgrade-declared-execution-mode-retired" || plan[12].to != workflowContractDeclaredExecutionModeRetiredVersion || plan[13].label != "upgrade-declared-execution-mode-stripped" || plan[13].to != workflowContractDeclaredExecutionModeStrippedVersion || plan[14].label != "upgrade-route-summaries" || plan[14].to != workflowContractRouteSummariesVersion {
		t.Fatalf("unexpected final upgrade step: %+v", plan[6])
	}
}

func TestUpgradePostRunPulseEnablementPromptShape(t *testing.T) {
	normalized := strings.Join(strings.Fields(upgradeDedicatedPulseSchedule), " ")
	for _, want := range []string{
		"workflow.json pulse.enabled",
		"It has no independent cron",
		"after each normal scheduled workflow run, Pulse Gate decides",
		"Remove every pulse_review_only schedule",
		`set_workflow_contract_version(version="1.0.27")`,
	} {
		if !strings.Contains(normalized, want) {
			t.Errorf("post-run Pulse enablement prompt missing %q", want)
		}
	}
	for _, mustNotContain := range []string{
		"Leave post_run_monitor_mode unset",
		"leave it on per_run",
		"single source of truth for enablement and cadence",
		"Normal workflow schedules never run Gate/Review+Fix inline",
	} {
		if strings.Contains(normalized, mustNotContain) {
			t.Errorf("post-run Pulse enablement prompt retains obsolete dedicated-schedule guidance: %q", mustNotContain)
		}
	}
}

func TestVersion126ReceivesDedicatedPulseScheduleMigration(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractPeriodicPulseReviewVersion})
	if len(plan) != 18 {
		t.Fatalf("1.0.26 upgrade plan = %+v, want dedicated Pulse, schedule prompt, finalizer ownership, report-activity-section, then report-activity-tab migrations", plan)
	}
	if plan[0].label != "upgrade-dedicated-pulse-schedule" || plan[0].to != workflowContractDedicatedPulseScheduleVersion {
		t.Fatalf("1.0.26 upgrade = %+v, want dedicated Pulse schedule migration", plan[0])
	}
	if plan[1].label != "upgrade-schedule-prompt-contract" || plan[1].to != workflowContractSchedulePromptContractVersion {
		t.Fatalf("1.0.26 final upgrade = %+v, want schedule prompt migration", plan[1])
	}
	if plan[2].label != "upgrade-schedule-finalizer-ownership" || plan[2].to != workflowContractFinalizerOwnedScheduleVersion {
		t.Fatalf("1.0.26 final upgrade = %+v, want finalizer ownership migration", plan[2])
	}
	if plan[3].label != "upgrade-report-activity-section" || plan[3].to != workflowContractReportActivitySectionVersion {
		t.Fatalf("1.0.26 final upgrade = %+v, want report-activity-section migration", plan[3])
	}
	if plan[4].label != "upgrade-report-activity-tab" || plan[4].to != workflowContractReportActivityTabVersion {
		t.Fatalf("1.0.26 report tab upgrade = %+v", plan[4])
	}
	if plan[5].label != "upgrade-pulse-lifecycle-reconciliation" || plan[5].to != workflowContractPulseLifecycleReconciliationVersion || plan[6].label != "upgrade-pulse-backlog-triage" || plan[6].to != workflowContractPulseBacklogTriageVersion || plan[7].label != "upgrade-pulse-actionable-backlog" || plan[7].to != workflowContractPulseActionableBacklogVersion || plan[8].label != "upgrade-orchestrator-step-type" || plan[8].to != workflowContractOrchestratorStepTypeVersion || plan[9].label != "upgrade-activity-tab-from-run-summary" || plan[9].to != workflowContractActivityTabFromRunSummaryVersion || plan[10].label != "upgrade-scripted-type-stays-regular" || plan[10].to != workflowContractScriptedTypeStaysRegularVersion || plan[11].label != "upgrade-declared-execution-mode-retired" || plan[11].to != workflowContractDeclaredExecutionModeRetiredVersion || plan[12].label != "upgrade-declared-execution-mode-stripped" || plan[12].to != workflowContractDeclaredExecutionModeStrippedVersion || plan[13].label != "upgrade-route-summaries" || plan[13].to != workflowContractRouteSummariesVersion {
		t.Fatalf("1.0.26 final upgrade = %+v, want Pulse lifecycle migration", plan[5])
	}
}

func TestVersion127ReceivesSchedulePromptContractMigration(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractDedicatedPulseScheduleVersion})
	if len(plan) != 17 || plan[0].label != "upgrade-schedule-prompt-contract" || plan[0].to != workflowContractSchedulePromptContractVersion {
		t.Fatalf("1.0.27 upgrade plan = %+v, want schedule prompt, finalizer ownership, report-activity-section, then report-activity-tab migration", plan)
	}
	for _, want := range []string{
		"dated incidents",
		"Measurement ownership must remain correct",
		"Do not weaken concrete backup behavior",
		`set_workflow_contract_version(version="1.0.28")`,
	} {
		if !strings.Contains(plan[0].query, want) {
			t.Errorf("schedule prompt migration missing %q", want)
		}
	}
	if plan[1].label != "upgrade-schedule-finalizer-ownership" || plan[1].to != workflowContractFinalizerOwnedScheduleVersion {
		t.Fatalf("1.0.27 final upgrade = %+v, want finalizer ownership migration", plan[1])
	}
	if plan[2].label != "upgrade-report-activity-section" || plan[2].to != workflowContractReportActivitySectionVersion {
		t.Fatalf("1.0.27 final upgrade = %+v, want report-activity-section migration", plan[2])
	}
	if plan[3].label != "upgrade-report-activity-tab" || plan[3].to != workflowContractReportActivityTabVersion {
		t.Fatalf("1.0.27 report tab upgrade = %+v", plan[3])
	}
	if plan[4].label != "upgrade-pulse-lifecycle-reconciliation" || plan[4].to != workflowContractPulseLifecycleReconciliationVersion || plan[5].label != "upgrade-pulse-backlog-triage" || plan[5].to != workflowContractPulseBacklogTriageVersion || plan[6].label != "upgrade-pulse-actionable-backlog" || plan[6].to != workflowContractPulseActionableBacklogVersion || plan[7].label != "upgrade-orchestrator-step-type" || plan[7].to != workflowContractOrchestratorStepTypeVersion || plan[8].label != "upgrade-activity-tab-from-run-summary" || plan[8].to != workflowContractActivityTabFromRunSummaryVersion || plan[9].label != "upgrade-scripted-type-stays-regular" || plan[9].to != workflowContractScriptedTypeStaysRegularVersion || plan[10].label != "upgrade-declared-execution-mode-retired" || plan[10].to != workflowContractDeclaredExecutionModeRetiredVersion || plan[11].label != "upgrade-declared-execution-mode-stripped" || plan[11].to != workflowContractDeclaredExecutionModeStrippedVersion || plan[12].label != "upgrade-route-summaries" || plan[12].to != workflowContractRouteSummariesVersion {
		t.Fatalf("1.0.27 final upgrade = %+v, want Pulse lifecycle migration", plan[4])
	}
}

func TestVersion128ReceivesFinalizerOwnershipMigration(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractSchedulePromptContractVersion})
	if len(plan) != 16 || plan[0].label != "upgrade-schedule-finalizer-ownership" || plan[0].to != workflowContractFinalizerOwnedScheduleVersion {
		t.Fatalf("1.0.28 upgrade plan = %+v, want finalizer ownership, report-activity-section, then report-activity-tab migration", plan)
	}
	normalized := strings.Join(strings.Fields(plan[0].query), " ")
	for _, want := range []string{"saved route selection before any retained schedule message", "The platform owns normal run finalization", `set_workflow_contract_version(version="1.0.29")`} {
		if !strings.Contains(normalized, want) {
			t.Errorf("migration prompt missing %q", want)
		}
	}
	if plan[1].label != "upgrade-report-activity-section" || plan[1].to != workflowContractReportActivitySectionVersion {
		t.Fatalf("1.0.28 final upgrade = %+v, want report-activity-section migration", plan[1])
	}
	if plan[2].label != "upgrade-report-activity-tab" || plan[2].to != workflowContractReportActivityTabVersion {
		t.Fatalf("1.0.28 report tab upgrade = %+v", plan[2])
	}
	if plan[3].label != "upgrade-pulse-lifecycle-reconciliation" || plan[3].to != workflowContractPulseLifecycleReconciliationVersion || plan[4].label != "upgrade-pulse-backlog-triage" || plan[4].to != workflowContractPulseBacklogTriageVersion || plan[5].label != "upgrade-pulse-actionable-backlog" || plan[5].to != workflowContractPulseActionableBacklogVersion || plan[6].label != "upgrade-orchestrator-step-type" || plan[6].to != workflowContractOrchestratorStepTypeVersion || plan[7].label != "upgrade-activity-tab-from-run-summary" || plan[7].to != workflowContractActivityTabFromRunSummaryVersion || plan[8].label != "upgrade-scripted-type-stays-regular" || plan[8].to != workflowContractScriptedTypeStaysRegularVersion || plan[9].label != "upgrade-declared-execution-mode-retired" || plan[9].to != workflowContractDeclaredExecutionModeRetiredVersion || plan[10].label != "upgrade-declared-execution-mode-stripped" || plan[10].to != workflowContractDeclaredExecutionModeStrippedVersion || plan[11].label != "upgrade-route-summaries" || plan[11].to != workflowContractRouteSummariesVersion {
		t.Fatalf("1.0.28 final upgrade = %+v, want Pulse lifecycle migration", plan[3])
	}
}

func TestVersion129ReceivesReportActivitySectionMigration(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractFinalizerOwnedScheduleVersion})
	if len(plan) != 15 || plan[0].label != "upgrade-report-activity-section" || plan[0].to != workflowContractReportActivitySectionVersion {
		t.Fatalf("1.0.29 upgrade plan = %+v, want report-activity-section then report-activity-tab migration", plan)
	}
	normalized := strings.Join(strings.Fields(plan[0].query), " ")
	for _, want := range []string{
		"what did this workflow actually do",
		"Daily Action",
		"Recent Activity",
		"validate_report_html",
		`set_workflow_contract_version(version="1.0.30")`,
	} {
		if !strings.Contains(normalized, want) {
			t.Errorf("report-activity-section migration prompt missing %q", want)
		}
	}
	if plan[1].label != "upgrade-report-activity-tab" || plan[1].to != workflowContractReportActivityTabVersion {
		t.Fatalf("1.0.29 report tab upgrade = %+v", plan[1])
	}
	if plan[2].label != "upgrade-pulse-lifecycle-reconciliation" || plan[2].to != workflowContractPulseLifecycleReconciliationVersion || plan[3].label != "upgrade-pulse-backlog-triage" || plan[3].to != workflowContractPulseBacklogTriageVersion || plan[4].label != "upgrade-pulse-actionable-backlog" || plan[4].to != workflowContractPulseActionableBacklogVersion || plan[5].label != "upgrade-orchestrator-step-type" || plan[5].to != workflowContractOrchestratorStepTypeVersion || plan[6].label != "upgrade-activity-tab-from-run-summary" || plan[6].to != workflowContractActivityTabFromRunSummaryVersion || plan[7].label != "upgrade-scripted-type-stays-regular" || plan[7].to != workflowContractScriptedTypeStaysRegularVersion || plan[8].label != "upgrade-declared-execution-mode-retired" || plan[8].to != workflowContractDeclaredExecutionModeRetiredVersion || plan[9].label != "upgrade-declared-execution-mode-stripped" || plan[9].to != workflowContractDeclaredExecutionModeStrippedVersion || plan[10].label != "upgrade-route-summaries" || plan[10].to != workflowContractRouteSummariesVersion {
		t.Fatalf("1.0.29 final upgrade = %+v, want Pulse lifecycle migration", plan[2])
	}
}

func TestVersion130ReceivesReportActivityTabMigration(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractReportActivitySectionVersion})
	if len(plan) != 14 || plan[0].label != "upgrade-report-activity-tab" || plan[0].to != workflowContractReportActivityTabVersion {
		t.Fatalf("1.0.30 upgrade plan = %+v, want report-activity-tab then Pulse lifecycle migration", plan)
	}
	normalized := strings.Join(strings.Fields(plan[0].query), " ")
	for _, want := range []string{
		"top-level tab",
		"a subsection scrolled past within another tab",
		"promote it into its own top-level tab",
		"validate_report_html",
		`set_workflow_contract_version(version="1.0.31")`,
	} {
		if !strings.Contains(normalized, want) {
			t.Errorf("report-activity-tab migration prompt missing %q", want)
		}
	}
	if plan[1].label != "upgrade-pulse-lifecycle-reconciliation" || plan[1].to != workflowContractPulseLifecycleReconciliationVersion || plan[2].label != "upgrade-pulse-backlog-triage" || plan[2].to != workflowContractPulseBacklogTriageVersion || plan[3].label != "upgrade-pulse-actionable-backlog" || plan[3].to != workflowContractPulseActionableBacklogVersion || len(plan) != 14 || plan[4].label != "upgrade-orchestrator-step-type" || plan[4].to != workflowContractOrchestratorStepTypeVersion || plan[5].label != "upgrade-activity-tab-from-run-summary" || plan[5].to != workflowContractActivityTabFromRunSummaryVersion || plan[6].label != "upgrade-scripted-type-stays-regular" || plan[6].to != workflowContractScriptedTypeStaysRegularVersion || plan[7].label != "upgrade-declared-execution-mode-retired" || plan[7].to != workflowContractDeclaredExecutionModeRetiredVersion || plan[8].label != "upgrade-declared-execution-mode-stripped" || plan[8].to != workflowContractDeclaredExecutionModeStrippedVersion || plan[9].label != "upgrade-route-summaries" || plan[9].to != workflowContractRouteSummariesVersion {
		t.Fatalf("1.0.30 final upgrade = %+v, want Pulse lifecycle migration", plan[1])
	}
}

func TestVersion133SkipsRetiredPulseBacklogMigration(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractPulseBacklogTriageVersion})
	if len(plan) != 11 || plan[0].label != "upgrade-pulse-actionable-backlog" || plan[0].to != workflowContractPulseActionableBacklogVersion || plan[1].label != "upgrade-orchestrator-step-type" || plan[1].to != workflowContractOrchestratorStepTypeVersion || plan[2].label != "upgrade-activity-tab-from-run-summary" || plan[2].to != workflowContractActivityTabFromRunSummaryVersion || plan[3].label != "upgrade-scripted-type-stays-regular" || plan[3].to != workflowContractScriptedTypeStaysRegularVersion || plan[4].label != "upgrade-declared-execution-mode-retired" || plan[4].to != workflowContractDeclaredExecutionModeRetiredVersion || plan[5].label != "upgrade-declared-execution-mode-stripped" || plan[5].to != workflowContractDeclaredExecutionModeStrippedVersion || plan[6].label != "upgrade-route-summaries" || plan[6].to != workflowContractRouteSummariesVersion {
		t.Fatalf("1.0.33 upgrade plan = %+v, want actionable Pulse backlog migration then orchestrator step-type migration", plan)
	}
	for _, want := range []string{
		"legacy Pulse actionable-backlog migration for this version has been retired",
		"Do not inspect or modify Pulse data",
		`set_workflow_contract_version(version="1.0.34")`,
	} {
		if !strings.Contains(plan[0].query, want) {
			t.Errorf("actionable Pulse backlog migration missing %q", want)
		}
	}
}

func TestRetiredPulseMigrationUpgradeTurnsDoNotExposeMigrationTool(t *testing.T) {
	const workspacePath = "Workflow/linkedin"
	tests := []struct {
		version string
		target  string
	}{
		{version: workflowContractReportActivityTabVersion, target: "1.0.32"},
		{version: workflowContractPulseLifecycleReconciliationVersion, target: "1.0.33"},
		{version: workflowContractPulseBacklogTriageVersion, target: "1.0.34"},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			turns, err := manualWorkflowUpgradeTurns(&WorkflowManifest{Version: tt.version}, nil, workspacePath)
			if err != nil {
				t.Fatalf("scheduledWorkshopTurns: %v", err)
			}
			if len(turns) == 0 {
				t.Fatal("expected a Pulse migration upgrade turn")
			}
			query := turns[0].query
			if strings.Contains(query, "record_pulse_migration_reconciliation") {
				t.Errorf("retired Pulse migration prompt still exposes the removed tool: %s", query)
			}
			if want := `set_workflow_contract_version(version="` + tt.target + `")`; !strings.Contains(query, want) {
				t.Errorf("retired Pulse migration prompt missing checkpoint stamp %q\n%s", want, query)
			}
			if strings.Contains(query, workflowUpgradeWorkspacePathPlaceholder) {
				t.Errorf("Pulse migration prompt leaked unbound workspace placeholder: %s", query)
			}
		})
	}
}

func TestRetiredPulseMigrationCheckpointsDoNotRequireWorkspacePath(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractReportActivityTabVersion})
	if len(plan) == 0 || !strings.Contains(plan[0].query, "set_workflow_contract_version") {
		t.Fatalf("missing retained version checkpoint turn: %+v", plan)
	}
	if strings.Contains(plan[0].query, workflowUpgradeWorkspacePathPlaceholder) {
		t.Fatalf("retired no-op Pulse checkpoint unexpectedly requires a workspace path: %s", plan[0].query)
	}
}

// TestUpgradeQueriesNeverNamePlatTickets guards a real mistake made while
// writing upgradeDedicatedPulseSchedule and workflow-tools.md's
// periodic-Pulse guidance: this text runs live on operators' own workflows,
// on their own machines — an internal ticket number in it is meaningless
// noise to them, not useful context. Scoped to the upgrade query constants
// specifically, not the whole package, since a Go doc comment referencing a
// ticket for engineers reading this source is a different, legitimate thing.
func TestUpgradeQueriesNeverNamePlatTickets(t *testing.T) {
	platTicket := regexp.MustCompile(`PLAT-\d+`)
	queries := map[string]string{
		"upgradeAgentCode":                  upgradeAgentCode,
		"upgradeCurrentArtifactContract":    upgradeCurrentArtifactContract,
		"upgradeDirectHTMLReports":          upgradeDirectHTMLReports,
		"upgradeDedicatedPulseSchedule":     upgradeDedicatedPulseSchedule,
		"upgradeSchedulePromptContract":     upgradeSchedulePromptContract,
		"upgradeScheduleFinalizerOwnership": upgradeScheduleFinalizerOwnership,
		"upgradeReportActivitySection":      upgradeReportActivitySection,
	}
	for name, query := range queries {
		if match := platTicket.FindString(query); match != "" {
			t.Errorf("%s references %q — an internal ticket number in a live upgrade prompt is meaningless to the operator running it on their own machine", name, match)
		}
	}
}
