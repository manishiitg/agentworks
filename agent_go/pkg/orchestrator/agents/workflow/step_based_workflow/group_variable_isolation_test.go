package step_based_workflow

import "testing"

// Separate scheduled group executions inherit the same workspace tool env.
// A declared optional variable absent from the second group's overrides must
// stay unset, rather than retaining the first execution's non-empty value.
func TestResolvedGroupClearsPriorExecutionOverride(t *testing.T) {
	manifest := &VariablesManifest{Variables: []Variable{
		{Name: "FILTER", Value: ""},
		{Name: "REGION", Value: "default-region"},
	}}
	shared := map[string]string{"MCP_API_URL": "https://example.com/api", "SECRET_EXAMPLE": "synthetic-sentinel"}
	first := newAgentClosingTestOrchestrator(t)
	first.SetWorkspaceEnvRef(shared)
	SyncGroupVariablesToWorkspaceEnv(first.BaseOrchestrator, manifest, MergeGroupWithDefaults(manifest, map[string]string{"FILTER": "selected", "REGION": "first-region"}))
	if first.snapshotWorkspaceEnv()["VAR_FILTER"] != "selected" {
		t.Fatal("first group override was not exposed to its shell")
	}

	second := newAgentClosingTestOrchestrator(t)
	second.SetWorkspaceEnvRef(shared)
	values := MergeGroupWithDefaults(manifest, map[string]string{"REGION": "second-region"})
	SyncGroupVariablesToWorkspaceEnv(second.BaseOrchestrator, manifest, values)
	env := second.snapshotWorkspaceEnv()
	if value, exists := env["VAR_FILTER"]; exists {
		t.Fatalf("second group inherited the first group's optional filter: present=%v value=%q", exists, value)
	}
	if env["VAR_REGION"] != "second-region" {
		t.Fatal("the current group's explicit override was lost")
	}
	if env["MCP_API_URL"] != "https://example.com/api" || env["SECRET_EXAMPLE"] != "synthetic-sentinel" {
		t.Fatal("group resolution changed non-variable environment entries")
	}

	// Preserve the earlier initialization-order fix: a replacement env must
	// backfill this execution's resolved snapshot without the old optional value.
	second.SetWorkspaceEnvRef(map[string]string{"MCP_API_URL": "https://example.com/new-api"})
	reinitialized := second.snapshotWorkspaceEnv()
	if _, exists := reinitialized["VAR_FILTER"]; exists || reinitialized["VAR_REGION"] != "second-region" {
		t.Fatal("environment reinitialization lost the current group's exact values")
	}
	SyncGroupVariablesToWorkspaceEnv(second.BaseOrchestrator, manifest, MergeGroupWithDefaults(manifest, map[string]string{"FILTER": ""}))
	if value, exists := second.snapshotWorkspaceEnv()["VAR_FILTER"]; !exists || value != "" {
		t.Fatal("an explicitly empty group override must stay present and empty")
	}

	// The batch manager receives raw overrides, unlike the other resolved
	// callers. It must restore defaults before replacing the group snapshot.
	second.variablesManifest = manifest
	second.GetExecutionManager().ApplyExecutionContext(&ExecutionSetup{
		Context: &ExecutionContext{}, VariableValues: map[string]string{},
	})
	batchEnv := second.snapshotWorkspaceEnv()
	if _, exists := batchEnv["VAR_FILTER"]; exists || batchEnv["VAR_REGION"] != "default-region" || second.variableValues["REGION"] != "default-region" {
		t.Fatal("batch group resolution must clear the optional override and retain shared defaults")
	}
}
