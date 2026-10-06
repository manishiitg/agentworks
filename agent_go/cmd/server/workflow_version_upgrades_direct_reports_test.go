package server

import (
	"strings"
	"testing"
)

func TestWorkflowVersionUpgradePlanFrom122MigratesReportsThenScheduledRoutes(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: "1.0.22"})
	if len(plan) != 20 {
		t.Fatalf("plan from 1.0.22 = %d steps, want retained migrations through explicit schedule Pulse: %+v", len(plan), plan)
	}
	if plan[0].label != "upgrade-direct-html-reports" || plan[0].to != "1.0.23" {
		t.Fatalf("plan[0] = %+v, want direct-report migration to 1.0.23", plan[0])
	}
	if plan[1].label != "upgrade-schedule-execution-model" || plan[1].to != workflowContractScheduleExecutionModelVersion {
		t.Fatalf("plan[1] = %+v, want schedule execution-model migration to %s", plan[1], workflowContractScheduleExecutionModelVersion)
	}
	if plan[2].label != "upgrade-dedicated-pulse-schedule" || plan[2].to != workflowContractDedicatedPulseScheduleVersion {
		t.Fatalf("plan[2] = %+v, want dedicated-Pulse migration", plan[2])
	}
	if plan[3].label != "upgrade-schedule-prompt-contract" || plan[3].to != workflowContractSchedulePromptContractVersion {
		t.Fatalf("plan[3] = %+v, want schedule-prompt migration", plan[3])
	}
	if plan[4].label != "upgrade-schedule-finalizer-ownership" || plan[4].to != workflowContractFinalizerOwnedScheduleVersion {
		t.Fatalf("plan[4] = %+v, want finalizer-ownership migration", plan[4])
	}
	if plan[5].label != "upgrade-report-activity-section" || plan[5].to != workflowContractReportActivitySectionVersion {
		t.Fatalf("plan[5] = %+v, want report-activity-section migration", plan[5])
	}
	if plan[6].label != "upgrade-report-activity-tab" || plan[6].to != workflowContractReportActivityTabVersion {
		t.Fatalf("plan[6] = %+v, want report-activity-tab migration", plan[6])
	}
	if plan[7].label != "upgrade-pulse-lifecycle-reconciliation" || plan[7].to != workflowContractPulseLifecycleReconciliationVersion || plan[8].label != "upgrade-pulse-backlog-triage" || plan[8].to != workflowContractPulseBacklogTriageVersion || plan[9].label != "upgrade-pulse-actionable-backlog" || plan[9].to != workflowContractPulseActionableBacklogVersion || plan[10].label != "upgrade-orchestrator-step-type" || plan[10].to != workflowContractOrchestratorStepTypeVersion || plan[11].label != "upgrade-activity-tab-from-run-summary" || plan[11].to != workflowContractActivityTabFromRunSummaryVersion || plan[12].label != "upgrade-scripted-type-stays-regular" || plan[12].to != workflowContractScriptedTypeStaysRegularVersion || plan[13].label != "upgrade-declared-execution-mode-retired" || plan[13].to != workflowContractDeclaredExecutionModeRetiredVersion || plan[14].label != "upgrade-declared-execution-mode-stripped" || plan[14].to != workflowContractDeclaredExecutionModeStrippedVersion || plan[15].label != "upgrade-route-summaries" || plan[15].to != workflowContractRouteSummariesVersion || plan[16].label != "upgrade-explicit-schedule-pulse" || plan[16].to != workflowContractExplicitSchedulePulseVersion {
		t.Fatalf("plan[7] = %+v, want Pulse lifecycle reconciliation migration to current", plan[7])
	}
	if plan[17].label != "upgrade-nested-agent-artifacts" || plan[17].to != workflowContractNestedAgentArtifactsVersion {
		t.Fatalf("migration 17 = %+v, want nested Agent artifacts", plan[17])
	}
	if last := plan[len(plan)-1]; len(plan) != 20 || last.label != "upgrade-step-description-layout" || last.to != workflowContractStepDescriptionLayoutVersion {
		t.Fatalf("final migration = %+v (of %d), want step description layout", last, len(plan))
	}
}

func TestUpgradeDirectHTMLReportsPreservesPrimaryDocuments(t *testing.T) {
	normalizedPrompt := strings.ToLower(strings.Join(strings.Fields(upgradeDirectHTMLReports), " "))
	for _, want := range []string{
		"reports/report_plan.json",
		"inventory every enabled HTML file widget",
		"db/report.html",
		"db/reports/index.html",
		"platform does not create report tabs or navigation",
		"Preserve the old primary dashboard",
		"including the primary dashboard",
		"validate_report_html",
		`set_workflow_contract_version(version="1.0.23")`,
	} {
		if !strings.Contains(normalizedPrompt, strings.ToLower(want)) {
			t.Errorf("direct-report upgrade prompt missing %q", want)
		}
	}
}
