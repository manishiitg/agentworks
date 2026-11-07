package step_based_workflow

import (
	"strings"
	"testing"
)

// run_full_workflow variables must be declared: a typo must not become a
// silently ignored value.
func TestUndeclaredRunVariables(t *testing.T) {
	manifest := &VariablesManifest{Variables: []Variable{{Name: "PR_NUMBER"}, {Name: "GITHUB_REPO"}}}
	if problem := undeclaredRunVariables(manifest, map[string]string{"PR_NUMBER": "149"}); problem != "" {
		t.Fatalf("declared variable refused: %s", problem)
	}
	problem := undeclaredRunVariables(manifest, map[string]string{"PR_NUBMER": "149"})
	if !strings.Contains(problem, "PR_NUBMER") || !strings.Contains(problem, "GITHUB_REPO, PR_NUMBER") {
		t.Fatalf("unknown variable not reported with the declared names: %q", problem)
	}
	if undeclaredRunVariables(nil, map[string]string{"X": "1"}) == "" {
		t.Fatal("no manifest: every variable is undeclared")
	}
}

// PLAT-746-265: presets are values only. Explicit empty strings override
// defaults, and unknown inputs/credentials fail before any step is launched.
func TestSavedInputPrecedenceAndNoAutomaticSelection(t *testing.T) {
	manifest := &VariablesManifest{Variables: []Variable{{Name: "region", Value: "default"}, {Name: "note", Value: "keep"}}, Groups: []VariableGroup{{Name: "Saved", Values: map[string]string{"region": "saved"}}}}
	for _, test := range []struct {
		name, preset, region string
		inputs               map[string]string
	}{
		{name: "defaults", region: "default"},
		{name: "preset", preset: "Saved", region: "saved"},
		{name: "override", preset: "Saved", region: "explicit", inputs: map[string]string{"region": "explicit"}},
		{name: "explicit empty", preset: "Saved", region: "", inputs: map[string]string{"region": ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveWorkflowInputs(manifest, test.preset, test.inputs)
			if err != nil || got["region"] != test.region || got["note"] != "keep" {
				t.Fatalf("resolved=%v err=%v", got, err)
			}
		})
	}
	if manifest.Groups[0].Values["region"] != "saved" {
		t.Fatal("run mutated saved inputs")
	}
	if _, err := ResolveWorkflowInputs(manifest, "missing", nil); err == nil {
		t.Fatal("unknown preset accepted")
	}
	if _, err := ResolveWorkflowInputs(manifest, "", map[string]string{"typo": "x"}); err == nil {
		t.Fatal("unknown input accepted")
	}
	if err := ValidatePlainWorkflowInput("API_KEY", "credential"); err == nil {
		t.Fatal("credential accepted as a plain saved input")
	}
	if _, err := ResolveSelectedInputPreset(manifest, []string{"Saved", "Other"}); err == nil {
		t.Fatal("preset selection became a batch")
	}
}

// PLAT-746-265: one typed contract covers defaults, presets and call values;
// local nested references work, while external references cannot read host data.
func TestWorkflowTypedInputsAndRequiredAdmission(t *testing.T) {
	manifest := &VariablesManifest{Variables: []Variable{{Name: "count", Type: "integer", Required: true}, {Name: "enabled", Type: "boolean", Value: "false"}, {Name: "details", Type: "object", Schema: map[string]interface{}{"type": "object", "$defs": map[string]interface{}{"code": map[string]interface{}{"type": "string", "pattern": "^ok-"}}, "properties": map[string]interface{}{"code": map[string]interface{}{"$ref": "#/$defs/code"}}, "required": []interface{}{"code"}}}}}
	if err := ValidateWorkflowInputDefinitions(manifest); err != nil {
		t.Fatal(err)
	}
	defaults, err := ResolveWorkflowInputs(manifest, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, supplied := defaults["details"]; supplied {
		t.Fatal("omitted optional object became an explicit value")
	}
	if err := ValidateWorkflowLaunchInputs(manifest, defaults); err == nil {
		t.Fatal("missing required input launched")
	}
	for _, values := range []map[string]string{{"count": ""}, {"enabled": ""}, {"count": "not a number"}, {"count": "2.5"}, {"enabled": "yes"}, {"details": `{"code":"wrong"}`}, {"details": "{}"}} {
		if _, err := ResolveWorkflowInputs(manifest, "", values); err == nil {
			t.Fatalf("invalid typed input accepted: %v", values)
		}
	}
	values, err := ResolveWorkflowInputs(manifest, "", map[string]string{"count": "9007199254740993", "details": `{"code":"ok-run"}`})
	if err != nil {
		t.Fatal(err)
	}
	if values["count"] != "9007199254740993" || values["enabled"] != "false" {
		t.Fatalf("typed values corrupted: %v", values)
	}
	if err := ValidateWorkflowLaunchInputs(manifest, values); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"file:///etc/passwd", "https://example.com/schema"} {
		field := Variable{Name: "private", Type: "object", Schema: map[string]interface{}{"$ref": ref}}
		if _, err := WorkflowInputProperty(field); err == nil {
			t.Fatal("external schema reference admitted")
		}
	}
}
