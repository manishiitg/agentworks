package step_based_workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestClearStepConfigField verifies that clearStepConfigField resets the named
// field to its zero value and, combined with `omitempty`, makes the JSON encoder
// drop the key entirely — which is what agents rely on to "remove" a prior
// override so a step falls back to preset/default behavior.
func TestClearStepConfigField(t *testing.T) {
	cases := []struct {
		name           string
		field          string
		prep           func(*StepConfig)
		assertJSONGone string // JSON key that must disappear after clearing
	}{
		{
			name:  "clears execution tier override",
			field: "execution_tier",
			prep: func(sc *StepConfig) {
				sc.AgentConfigs = &AgentConfigs{ExecutionTier: "medium"}
			},
			assertJSONGone: "execution_tier",
		},
		{
			name:  "clears slice selection",
			field: "servers",
			prep: func(sc *StepConfig) {
				sc.AgentConfigs = &AgentConfigs{SelectedServers: []string{"gmail", "slack"}}
			},
			assertJSONGone: "selected_servers",
		},
		{
			name:  "clears additional read paths",
			field: "additional_read_paths",
			prep: func(sc *StepConfig) {
				sc.AgentConfigs = &AgentConfigs{AdditionalReadPaths: []string{"variables"}}
			},
			assertJSONGone: "additional_read_paths",
		},
		{
			name:  "clears string field (knowledgebase_contribution)",
			field: "knowledgebase_contribution",
			prep: func(sc *StepConfig) {
				sc.AgentConfigs = &AgentConfigs{KnowledgebaseContribution: "extract companies"}
			},
			assertJSONGone: "knowledgebase_contribution",
		},
		{
			name:  "clears validation_schema at StepConfig level",
			field: "validation_schema",
			prep: func(sc *StepConfig) {
				sc.ValidationSchema = &ValidationSchema{}
			},
			assertJSONGone: "validation_schema",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := &StepConfig{ID: "step-test"}
			tc.prep(sc)

			ok := clearStepConfigField(sc, tc.field)
			if !ok {
				t.Fatalf("clearStepConfigField(%q) returned false — name was not recognized", tc.field)
			}

			encoded, err := json.Marshal(sc)
			if err != nil {
				t.Fatalf("marshal StepConfig: %v", err)
			}
			s := string(encoded)
			if strings.Contains(s, `"`+tc.assertJSONGone+`"`) {
				t.Errorf("expected JSON key %q to be omitted after clear; got %s", tc.assertJSONGone, s)
			}
		})
	}
}

// TestClearStepConfigField_UnknownName asserts the helper reports unrecognized
// names so the tool handler can surface a clear error to the agent instead of
// silently ignoring typos.
//
// Fields that exist on the struct but have no setter in update_step_config
// (e.g. enable_context_offloading, successful_runs, *_max_turns) are also
// treated as unknown — the tool only exposes clearing of fields an agent could
// have set in the first place. Dead/removed fields (conditional_llm,
// keep_learning_full) are unknown because they no longer exist on the struct.
func TestClearStepConfigField_UnknownName(t *testing.T) {
	sc := &StepConfig{ID: "step-test", AgentConfigs: &AgentConfigs{}}
	unknowns := []string{
		"",
		"nonexistent_field",
		// Removed fields:
		"conditional_llm",
		"keep_learning_full",
		// No-setter fields (system-managed or global-default):
		"successful_runs",
		"enable_context_offloading",
		"enable_dynamic_tier_selection",
		"disable_tier_optimization",
		"execution_max_turns",
		"learning_max_turns",
		"orchestration_max_iterations",
		"delegating_agent_tier",
		"learn_code_max_fix_iterations",
		"learning_llm",
	}
	for _, bad := range unknowns {
		if clearStepConfigField(sc, bad) {
			t.Errorf("clearStepConfigField(%q) returned true — expected unknown", bad)
		}
	}
}

// TestClearStepConfigField_NilAgentConfigs — even when AgentConfigs hasn't been
// allocated, the helper should still recognize valid field names (return true)
// rather than falsely reporting them as unknown. Validation_schema lives on
// StepConfig itself, so clearing it must work regardless of AgentConfigs presence.
func TestClearStepConfigField_NilAgentConfigs(t *testing.T) {
	sc := &StepConfig{ID: "step-test"} // AgentConfigs intentionally nil

	if !clearStepConfigField(sc, "validation_schema") {
		t.Error("expected validation_schema to be clearable even with nil AgentConfigs")
	}
	if clearStepConfigField(sc, "not_a_real_field") {
		t.Error("expected nonexistent field to return false")
	}
}
