package catalog

import (
	"reflect"
	"testing"
)

// PLAT-825: grouped selectors may use a key other than action; an external
// member backing several actions cannot choose one on the caller's behalf.
func TestInvocationKeepsSourceSelectorsAndAmbiguousMembers(t *testing.T) {
	actions := append([]Action{}, Actions...)
	actions = append(actions,
		Action{Family: "vault", Name: "add", Member: "vault_add", Source: "manage_vault", SourceKey: "operation", SourceAction: "create"},
		Action{Family: "functions", Name: "define", Member: "define_function", ExternalMembers: "update_crew"},
		Action{Family: "functions", Name: "update", Member: "update_function", ExternalMembers: "update_crew"},
		Action{Family: "sessions", Name: "list", Member: "list_sessions", ExternalMembers: "list_project_sessions"},
	)
	for _, tc := range []struct {
		name, source, target string
		input, want          map[string]any
	}{
		{"source selector", "manage_vault", "vault", map[string]any{"operation": "create", "scope": "chosen", "name": "sample"}, map[string]any{"action": "add", "scope": "chosen", "name": "sample"}},
		{"unique external", "list_project_sessions", "sessions", map[string]any{"workflow_id": "sample"}, map[string]any{"action": "list", "workflow_id": "sample"}},
		{"ambiguous external", "update_crew", "update_crew", map[string]any{"crew_id": "sample"}, map[string]any{"crew_id": "sample"}},
		{"foreign source", "foreign_tool", "foreign_tool", map[string]any{"action": "custom", "scope": "chosen"}, map[string]any{"action": "custom", "scope": "chosen"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := map[string]any{}
			for key, value := range tc.input {
				before[key] = value
			}
			name, args := invocation(actions, tc.source, tc.input)
			if name != tc.target || !reflect.DeepEqual(args, tc.want) {
				t.Fatalf("invocation = %s %v, want %s %v", name, args, tc.target, tc.want)
			}
			if !reflect.DeepEqual(tc.input, before) {
				t.Fatal("translation mutated the caller's payload")
			}
		})
	}
}
