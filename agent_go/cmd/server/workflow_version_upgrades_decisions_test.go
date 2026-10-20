package server

import (
	"strings"
	"testing"
)

// The disposition lives in the instruction and the archive is moved rather
// than deleted, so the manual migration does not manufacture a destructive
// choice for the operator.

// Every upgrade is operator-started in Workshop. Each instruction must preserve
// that live decision boundary instead of claiming it is unattended maintenance.
func TestEveryUpgradeQueryCarriesTheManualContract(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: "1.0.44"})
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
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: "1.0.44"})
	for _, step := range plan {
		if strings.Contains(step.query, "contract_upgrade_decision") || strings.Contains(step.query, "OPERATOR DECISION ON THIS UPGRADE") {
			t.Errorf("%s query still references the removed upgrade-specific decision channel", step.label)
		}
	}
}

// Measurement remains producer-owned guidance, not a blocking contract
// migration. Guard the complete upgrade chain so a mandatory route, step,
// table, or retired evaluation turn cannot silently return.
