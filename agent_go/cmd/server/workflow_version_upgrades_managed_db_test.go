package server

import (
	"strings"
	"testing"
)

// PLAT-428: a workflow on 1.0.44 owes exactly one migration, and its text carries
// the conversion recipe and the stamp gate.
func TestManagedDBScriptsMigration(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractNestedAgentArtifactsVersion})
	if len(plan) != 1 || plan[0].label != "upgrade-managed-db-scripts" || plan[0].to != "1.0.45" {
		t.Fatalf("1.0.44 plan = %+v, want only the managed DB scripts migration", plan)
	}
	if len(workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractManagedDBScriptsVersion})) != 0 {
		t.Error("a current workflow owes nothing")
	}
	for _, want := range []string{"scan_workflow_script_db_usage", "agentworks_db", "apply_workflow_db_migration", "db/migrations", "set_workflow_contract_version", "PRAGMA", "do not stamp", "$DB_PATH", "execute_many", "iter_query"} {
		if !strings.Contains(plan[0].query, want) {
			t.Errorf("migration text missing %q", want)
		}
	}
	if workflowContractVersionIsExecutionCompatible(workflowContractNestedAgentArtifactsVersion) {
		t.Error("1.0.44 must wait for the migration before it can run")
	}
	if !workflowContractVersionIsExecutionCompatible("1.0.45") {
		t.Error("1.0.45 is the current contract")
	}
}
