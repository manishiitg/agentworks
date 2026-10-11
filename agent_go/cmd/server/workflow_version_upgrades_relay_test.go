package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
)

// Every new migration inherits Relay eligibility unless it is explicitly
// excluded. Check the complete pending list at each ladder boundary, rather
// than merely checking that excluded labels are absent.
func TestRelayKeepsEveryApplicableMigrationAtEachVersion(t *testing.T) {
	versions := []string{workflowContractInitialVersion, workflowContractRunScopedRoutesVersion, workflowContractEvalRetirementVersion}
	for _, upgrade := range fullWorkflowVersionUpgradePlan(&WorkflowManifest{}) {
		versions = append(versions, upgrade.to)
	}
	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			var want []workflowVersionUpgrade
			for _, upgrade := range fullWorkflowVersionUpgradePlan(&WorkflowManifest{Version: version}) {
				if !goalsOnlyWorkflowUpgrades[upgrade.label] && !relayStoreUpgrades[upgrade.label] {
					want = append(want, upgrade)
				}
			}
			got := workflowVersionUpgradePlan(&WorkflowManifest{Version: version, Kind: "relay"})
			if len(got) != len(want) || (len(got) > 0 && !reflect.DeepEqual(got, want)) {
				t.Fatalf("Relay lost or reordered shared migrations: got %v, want %v", got, want)
			}
		})
	}
}

func TestRelayMigrationHistoryNeverClaimsSkippedMigrationsWereApplied(t *testing.T) {
	for _, version := range []string{workflowContractInitialVersion, workflowContractDeclaredExecutionModeStrippedVersion, workflowContractNestedAgentArtifactsVersion, WorkflowContractCurrentVersion} {
		t.Run(version, func(t *testing.T) {
			pending, applied := workflowContractUpgradeLists(&WorkflowManifest{Kind: "relay", Version: version, CodeLayoutVersion: 1})
			for _, item := range append(pending, applied...) {
				if goalsOnlyWorkflowUpgrades[item.Label] || relayStoreUpgrades[item.Label] {
					t.Errorf("Relay history claims inapplicable migration %q", item.Label)
				}
			}
			shared := workflowVersionUpgradePlan(&WorkflowManifest{Kind: "relay"})
			if len(pending)+len(applied) != len(shared) {
				t.Fatalf("history lost shared migrations: pending=%d applied=%d shared=%d", len(pending), len(applied), len(shared))
			}
		})
	}
}

// Exercise the workspace-backed Builder guard and next-stamp lookup together
// with the API preflight. Only absent-feature migrations may be bypassed.
func TestRelayMigrationRunGatesAndNextStampAgree(t *testing.T) {
	for _, tc := range []struct {
		name, kind, version, next string
		layout                    int
		allowed                   bool
	}{
		{name: "Relay owes shared artifact migration", kind: "relay", version: "1.0.43", next: "1.0.44", layout: 1},
		{name: "Relay skips DB migration but owes description layout", kind: "relay", version: "1.0.44", next: "1.0.46", layout: 1},
		{name: "Relay on the current contract runs", kind: "relay", version: "1.0.47", layout: 1, allowed: true},
		{name: "Goals owes DB migration", version: "1.0.44", next: "1.0.45", layout: 1},
		{name: "Relay still needs code layout", kind: "relay", version: "1.0.46"},
		{name: "Relay refuses unknown version", kind: "relay", version: "9.9.9", layout: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := &WorkflowManifest{SchemaVersion: 1, ID: "relay-migration", Kind: tc.kind, Version: tc.version, CodeLayoutVersion: tc.layout}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			const workspacePath = "Workflow/relay-migration"
			server := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{workspacePath + "/workflow.json": string(data)}})
			t.Cleanup(server.Close)
			t.Setenv("WORKSPACE_API_URL", server.URL)
			if err := requireCurrentWorkflowContractForManualRun(context.Background(), workspacePath); (err == nil) != tc.allowed {
				t.Fatalf("Builder allowed=%v want=%v: %v", err == nil, tc.allowed, err)
			}
			if err := directWebhookPreflight(manifest); (err == nil) != tc.allowed {
				t.Fatalf("API allowed=%v want=%v: %v", err == nil, tc.allowed, err)
			}
			next, _, err := nextWorkflowContractUpgrade(context.Background(), workspacePath)
			if err != nil || next != tc.next {
				t.Fatalf("next stamp=%q want=%q: %v", next, tc.next, err)
			}
		})
	}
}

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
	// 1.0.46 is current for both kinds.
	for _, kind := range []string{"", "relay"} {
		if !manifestContractIsExecutionCompatible(&WorkflowManifest{Version: WorkflowContractCurrentVersion, Kind: kind}) {
			t.Errorf("kind %q on the current contract must run", kind)
		}
	}
	// 1.0.44 owes the managed-DB migration, which a Relay (no database) skips,
	// and the shared description layout migration, which it does not.
	if manifestContractIsExecutionCompatible(&WorkflowManifest{Version: workflowContractNestedAgentArtifactsVersion}) {
		t.Error("a Goals workflow on 1.0.44 must be blocked")
	}
	relay144 := &WorkflowManifest{Version: workflowContractNestedAgentArtifactsVersion, Kind: "relay"}
	if plan := workflowVersionUpgradePlan(relay144); len(plan) != 2 || plan[0].label != "upgrade-step-description-layout" {
		t.Errorf("a Relay on 1.0.44 owes only the description layout migration, got %+v", plan)
	}
	if manifestContractIsExecutionCompatible(relay144) {
		t.Error("a Relay on 1.0.44 owes the description layout migration and must be blocked")
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

// Python execution has no workflow contract or shared-step migration debt.
func TestPythonRelayDoesNotOweWorkflowMigrations(t *testing.T) {
	manifest := &WorkflowManifest{Version: workflowContractInitialVersion, Kind: "relay", RelayRuntime: "python"}
	if upgrades := workflowVersionUpgradePlan(manifest); len(upgrades) != 0 {
		t.Fatalf("Python Relay owes workflow migrations: %v", upgrades)
	}
	if !manifestContractIsExecutionCompatible(manifest) {
		t.Fatal("Python Relay was refused by a workflow contract")
	}
}
