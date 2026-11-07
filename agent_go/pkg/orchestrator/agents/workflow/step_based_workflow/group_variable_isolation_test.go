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

	// Execution consumes an already resolved snapshot, without re-reading defaults.
	second.variablesManifest = manifest
	resolved, err := ResolveWorkflowInputs(manifest, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	second.GetExecutionManager().ApplyExecutionContext(&ExecutionSetup{Context: &ExecutionContext{}, VariableValues: resolved})
	env = second.snapshotWorkspaceEnv()
	if env["VAR_FILTER"] != "" || env["VAR_REGION"] != "default-region" {
		t.Fatal("resolved snapshot inherited a prior override")
	}

}

// PLAT-746-265: omission may run once; an empty/unknown selection
// must never accidentally start work or select another group's data.
func TestOptionalInputSetsDoNotEnableUnselectedWork(t *testing.T) {
	manifest := &VariablesManifest{Variables: []Variable{{Name: "region", Value: "base"}}, Groups: []VariableGroup{
		{Name: "one", Values: map[string]string{"region": "one"}},
		{Name: "two", Values: map[string]string{"region": "two"}},
	}}
	cases := []struct {
		name      string
		manifest  *VariablesManifest
		selection []string
		wantError bool
	}{
		{"absent inputs", nil, nil, false},
		{"defaults only", &VariablesManifest{Variables: manifest.Variables}, nil, false},
		{"explicit empty", nil, []string{}, true},
		{"omitted preset uses defaults", manifest, nil, false},
		{"saved presets never auto-run", &VariablesManifest{Groups: manifest.Groups[1:]}, nil, false},
		{"unknown without groups", nil, []string{"missing"}, true},
		{"partly unknown", manifest, []string{"one", "missing"}, true},
		{"explicit selection", manifest, []string{"two"}, false},
		{"label is not a run folder", &VariablesManifest{Groups: []VariableGroup{{Name: " Execution! "}}}, nil, false},
		{"dot label is plain data", &VariablesManifest{Groups: []VariableGroup{{Name: ".temporary"}}}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, err := ResolveSelectedInputPreset(tc.manifest, tc.selection)
			if (err != nil) != tc.wantError {
				t.Fatalf("items=%+v err=%v", items, err)
			}
			if tc.manifest == nil && err == nil && items.Name != "" {
				t.Fatal("invented named group")
			}
		})
	}
	items, err := ResolveSelectedInputPreset(manifest, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	items.Values["region"] = "modified"
	if manifest.Groups[0].Values["region"] != "one" {
		t.Fatal("run changed saved inputs")
	}
}
