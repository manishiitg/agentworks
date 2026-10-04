package server

import (
	"sort"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
)

// PLAT-431: a Relay reuses the workflow migrations for the shared runtime and
// skips only the goal-driven ones.
func TestRelayOwesOnlySharedWorkflowMigrations(t *testing.T) {
	goals := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractInitialVersion})
	relay := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractInitialVersion, Kind: "relay"})
	labels := map[string]bool{}
	for _, upgrade := range goals {
		labels[upgrade.label] = true
	}
	for label := range goalsOnlyWorkflowUpgrades {
		if !labels[label] {
			t.Errorf("goalsOnlyWorkflowUpgrades names %q, which is not on the ladder (renamed?)", label)
		}
	}
	if len(relay) == 0 || len(relay) >= len(goals) {
		t.Fatalf("relay plan has %d of %d migrations; want the shared subset", len(relay), len(goals))
	}
	for _, upgrade := range relay {
		if goalsOnlyWorkflowUpgrades[upgrade.label] || relayStoreUpgrades[upgrade.label] {
			t.Errorf("a Relay was given %q, which is goal-driven or for a store a Relay lacks", upgrade.label)
		}
	}
	for label := range relayStoreUpgrades {
		if !labels[label] {
			t.Errorf("relayStoreUpgrades names %q, which is not on the ladder (renamed?)", label)
		}
	}
}

func TestRelayCompatibilityIgnoresSkippedGoalsMigrations(t *testing.T) {
	// 1.0.45 is current for both kinds.
	for _, kind := range []string{"", "relay"} {
		if !manifestContractIsExecutionCompatible(&WorkflowManifest{Version: WorkflowContractCurrentVersion, Kind: kind}) {
			t.Errorf("kind %q on the current contract must run", kind)
		}
	}
	// 1.0.44 owes the managed-DB migration. A Goals workflow is blocked; a
	// Relay has no database, owes nothing and runs.
	if manifestContractIsExecutionCompatible(&WorkflowManifest{Version: workflowContractNestedAgentArtifactsVersion}) {
		t.Error("a Goals workflow on 1.0.44 must be blocked")
	}
	if !manifestContractIsExecutionCompatible(&WorkflowManifest{Version: workflowContractNestedAgentArtifactsVersion, Kind: "relay"}) {
		t.Error("a Relay on 1.0.44 owes nothing (it has no database) and must run")
	}
	// A Relay that still owes a shared runtime migration is blocked.
	if manifestContractIsExecutionCompatible(&WorkflowManifest{Version: workflowContractEvalRetirementVersion, Kind: "relay"}) {
		t.Error("a Relay on 1.0.43 owes the nested-artifacts migration and must be blocked")
	}
	// An unknown (newer) version is never compatible.
	if manifestContractIsExecutionCompatible(&WorkflowManifest{Version: "9.9.9", Kind: "relay"}) {
		t.Error("an unknown version must not run")
	}
}

// The Relay Builder must hold every tool a shared migration tells it to call,
// or a blocked Relay could never be migrated.
func TestRelayBuilderHasEveryToolASharedMigrationCalls(t *testing.T) {
	callable := scheduledBuilderCallableTools(t)
	builderTools, err := relayproduct.BuilderTools()
	if err != nil {
		t.Fatal(err)
	}
	relayTools := map[string]bool{}
	for _, name := range builderTools {
		relayTools[name] = true
	}
	for _, name := range []string{"get_api_spec", "get_prompt", "get_resource", "read_skill"} {
		relayTools[name] = true
	}
	missing := map[string][]string{}
	for _, upgrade := range workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractInitialVersion, Kind: "relay"}) {
		for _, name := range explicitUpgradeToolReferences(upgrade.query, callable) {
			if !relayTools[name] {
				missing[name] = append(missing[name], upgrade.label)
			}
		}
	}
	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Errorf("relay product.yaml lacks %q, called by %v", name, missing[name])
	}
}
