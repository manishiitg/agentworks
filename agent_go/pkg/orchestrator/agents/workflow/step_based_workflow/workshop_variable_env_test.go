package step_based_workflow

import "testing"

// PLAT-746-265: a saved preset never overrides the shell defaults unless selected.
func TestWorkshopInputsUseDefaultsUnlessPresetSelected(t *testing.T) {
	manifest := &VariablesManifest{
		Variables: []Variable{{Name: "BASE_URL", Value: "https://default.example"}},
		Groups:    []VariableGroup{{Name: "saved", Values: map[string]string{"BASE_URL": "https://selected.example"}}},
	}
	values, preset, ok := ResolveWorkshopVariableValues(manifest, nil)
	if !ok || preset != "" || values["BASE_URL"] != "https://default.example" {
		t.Fatalf("defaults: values=%v preset=%q ok=%v", values, preset, ok)
	}
	values, preset, ok = ResolveWorkshopVariableValues(manifest, []string{"saved"})
	if !ok || preset != "saved" || values["BASE_URL"] != "https://selected.example" {
		t.Fatalf("selection: values=%v preset=%q ok=%v", values, preset, ok)
	}
}
