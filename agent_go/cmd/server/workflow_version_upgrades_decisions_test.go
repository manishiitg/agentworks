package server

import (
	"strings"
	"testing"
)

// The disposition lives in the instruction and the archive is moved rather
// than deleted, so the manual migration does not manufacture a destructive
// choice for the operator.
func TestArtifactContractUpgradeDecidesTheArchiveItself(t *testing.T) {
	for _, want := range []string{
		// The refusal this replaced read "retire" as "delete". Lead with the
		// property that makes the objection moot.
		"NOTHING IS DELETED IN THIS MIGRATION",
		"migration-backups/",
		"stay on disk, and in git history, at the new location",
		// The turn verifies the no-loss claim rather than taking it on faith.
		"Read them back at the new path",
		"if the move cannot preserve a file, that IS a blocker",
		// Answer the prior revert on its merits instead of talking over it.
		"A previous turn on this workflow may have declined",
		"DESTROYED",
	} {
		if !strings.Contains(upgradeCurrentArtifactContract, want) {
			t.Errorf("1.0.21 upgrade prompt missing %q", want)
		}
	}
	if strings.Contains(upgradeCurrentArtifactContract, "workflow owner's decision") {
		t.Error("1.0.21 upgrade prompt still defers the archive to an owner who is not reading it")
	}
}

// Every upgrade is operator-started in Workshop. Each instruction must preserve
// that live decision boundary instead of claiming it is unattended maintenance.
func TestEveryUpgradeQueryCarriesTheManualContract(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: "1.0.9"})
	if len(plan) < 2 {
		t.Fatalf("expected a multi-rung plan from 1.0.9, got %+v", plan)
	}
	for _, step := range plan {
		for _, want := range []string{
			"EXECUTION CONTEXT",
			"manual platform migration",
			"started by the workflow operator in Workshop chat",
			"do not guess and do not stamp",
			"ask the operator",
		} {
			if !strings.Contains(step.query, want) {
				t.Errorf("%s query missing %q", step.label, want)
			}
		}
		// An earlier draft pressured the agent into compliance. An agent that
		// had correctly blocked this migration cited that framing when it
		// refused again.
		for _, coercive := range []string{
			"do not re-open it as a judgement call",
			"reaches nobody",
			"is not an available move",
			"automated platform migration",
			"relayed through the scheduler",
		} {
			if strings.Contains(step.query, coercive) {
				t.Errorf("%s query pressures the agent (%q); state the context, leave refusing available", step.label, coercive)
			}
		}
	}
}

// Manual upgrades use the live Workshop conversation. A second,
// upgrade-specific answer channel in workflow.json remains unnecessary.
func TestNoParallelContractUpgradeDecisionChannel(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: "1.0.20"})
	for _, step := range plan {
		if strings.Contains(step.query, "contract_upgrade_decision") || strings.Contains(step.query, "OPERATOR DECISION ON THIS UPGRADE") {
			t.Errorf("%s query still references the removed upgrade-specific decision channel", step.label)
		}
	}
}

// Measurement remains producer-owned guidance, not a blocking contract
// migration. Guard the complete upgrade chain so a mandatory route, step,
// table, or retired evaluation turn cannot silently return.
func TestNoUpgradeMandatesMeasurementTopology(t *testing.T) {
	if WorkflowContractCurrentVersion != workflowContractUnifiedAgentVersion {
		t.Fatalf("current contract = %s, want step description layout marker %s", WorkflowContractCurrentVersion, workflowContractStepDescriptionLayoutVersion)
	}
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractInitialVersion})
	joined := strings.ToLower(strings.Join(func() []string {
		out := make([]string, 0, len(plan)*2)
		for _, upgrade := range plan {
			out = append(out, upgrade.label, upgrade.query)
		}
		return out
	}(), "\n"))
	for _, forbidden := range []string{
		"upgrade-eval-retirement", "set_workflow_contract_version(version=\"1.0.43\")",
		"measurement-router", "measure-outcomes", "workflow_metrics",
		"upgrade-measurement-route", "route-skip-measurement", "planning/metrics.json",
	} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("upgrade chain still mandates retired measurement topology %q", forbidden)
		}
	}
	for _, version := range []string{
		workflowContractExplicitSchedulePulseVersion,
		workflowContractRunScopedRoutesVersion,
		workflowContractEvalRetirementVersion,
	} {
		got := workflowVersionUpgradePlan(&WorkflowManifest{Version: version})
		if len(got) != 4 || got[0].label != "upgrade-nested-agent-artifacts" || got[1].label != "upgrade-managed-db-scripts" || got[2].label != "upgrade-step-description-layout" {
			t.Errorf("older marker %s migration plan = %+v, want nested Agent artifacts, managed DB scripts, then step description layout", version, got)
		}
	}
}
