package step_based_workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

// Plan Drift and the due flags use one objective rule for the 1.0.49 Resources section: all four lines present, no
// "none" for a reference the description names, no use claimed for a store the step's own configuration switches off.
func TestResourceIntentProblemsAreObjective(t *testing.T) {
	full := "## Goal\nDo it.\n\n## Resources\n- Knowledge base: none\n- Database: read the orders\n- Brain: none\n- Learnings: none"
	plan := func(description string, authored bool) []byte {
		raw, _ := json.Marshal(map[string]any{"steps": []any{map[string]any{"type": "agent", "id": "a", "description": description, "authored_prompt": authored,
			"prompt_contract": AgentDescriptionFirstPromptContract}}})
		return raw
	}
	for name, tc := range map[string]struct {
		description string
		authored    bool
		config      string
		want        string // substring of the reason; empty means no problem
	}{
		"all four lines":                   {description: full},
		"no Resources section":             {description: "## Goal\nDo it.", want: "no ## Resources"},
		"a line missing":                   {description: strings.Replace(full, "\n- Brain: none", "", 1), want: "Brain"},
		"none for a named reference":       {description: full + "\n\n## Guides\nknowledgebase/tone.md", want: "not used"},
		"use claimed, access switched off": {description: strings.Replace(full, "Knowledge base: none", "Knowledge base: reads the tone guide", 1), config: `{"steps":[{"id":"a","agent_configs":{"knowledgebase_access":"none"}}]}`, want: "access to none"},
		"unset access is the default read": {description: strings.Replace(full, "Knowledge base: none", "Knowledge base: reads the tone guide", 1), config: `{"steps":[{"id":"a","agent_configs":{}}]}`},
		"authored prompts are not checked": {description: "no sections", authored: true},
	} {
		problems, evaluated := ResourceIntentProblems(plan(tc.description, tc.authored), tc.config)
		got := problems["a"]
		if tc.authored && evaluated["a"] {
			t.Errorf("%s: an authored prompt was evaluated", name)
		}
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: problem = %q, want containing %q", name, got, tc.want)
		}
	}
}

// A step that tells its agent to read a file that does not exist makes Plan Drift due once even when the break is old,
// so the owner's workflow gets it repaired or a plain question (a note break at the first look still does not).
func TestOldMissingFileNamedByAStepFlagsPlanDriftOnce(t *testing.T) {
	plan := `{"steps":[{"type":"agent","id":"step-a","description":"## Goal\nDo A with the company notes in knowledgebase/context/context.md.","items":[{"id":"x","type":"user_message","message":"Do A."}]}]}`
	planDriftCandidateWorkspace(t, "Workflow/old-missing-file", plan, reviewedStepConfig)
	items, err := CollectPlanDriftDueItems("Workflow/old-missing-file")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.StepID == "step-a" && strings.Contains(item.Reason, "knowledgebase/context/context.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("an old missing file named by a step must make Plan Drift due for it, got %#v", items)
	}
}
