package step_based_workflow

import (
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-060. architecture_review already owns tier, mode and model selection, is
// read-only, and already produces "current state, exact suggestion, expected
// benefit, risk, and evidence" for every recommendation. That rationale lived
// only in the Pulse finding: the Fixer applied it through a tool call with no
// reason parameter, so step_config.json — what the *next* reviewer reads —
// recorded the change with no trace of why.
//
// Each rejection must do three things: name the field, name the hidden
// consequence the caller is least likely to know, and name the escape hatch.
// The escape hatch is not decoration — a required field invites a confabulated
// answer from an agent that has already decided to act, and an invented
// justification is harder to challenge later than a missing one.

func TestExecutionTierPinRequiresAReason(t *testing.T) {
	err := validateExecutionTierChange("low", "")
	if err == nil {
		t.Fatal("a tier was pinned with no stated reason")
	}
	for _, want := range []string{
		"execution_tier_reason",
		"successful-run counts do not change it", // configuration remains stable
		"architecture_review",                    // where the evidence must come from
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("tier rejection missing %q: %v", want, err)
		}
	}

	if err := validateExecutionTierChange("low", "   "); err == nil {
		t.Error("whitespace accepted as a justification")
	}
	if err := validateExecutionTierChange("low", "PUL-1234: deterministic file-shape check, 12 stable runs"); err != nil {
		t.Errorf("a real reason was rejected: %v", err)
	}
	// Not setting a tier is not a change and must never be gated.
	if err := validateExecutionTierChange("", ""); err != nil {
		t.Errorf("leaving the tier unset demanded a reason: %v", err)
	}
}

func TestExecutionLLMPinRequiresAReason(t *testing.T) {
	err := validateExecutionLLMChange(true, "")
	if err == nil {
		t.Fatal("a model was pinned with no stated reason")
	}
	// A pin outranks tier entirely — that is the consequence worth surfacing,
	// because it silently voids every tier decision above it.
	for _, want := range []string{"execution_llm_reason", "outranks execution_tier"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("pin rejection missing %q: %v", want, err)
		}
	}
	if err := validateExecutionLLMChange(true, "PUL-9: sonnet-5 matched opus on 20 samples at 1/5 cost"); err != nil {
		t.Errorf("a real reason was rejected: %v", err)
	}
	if err := validateExecutionLLMChange(false, ""); err != nil {
		t.Errorf("not pinning demanded a reason: %v", err)
	}
}

func TestClearingAnOpsDecisionClearsItsReason(t *testing.T) {
	// A reason that outlives its decision reads as pre-approval for a future
	// re-pin nobody reviewed.
	for _, tc := range []struct {
		field    string
		seed     func(*AgentConfigs)
		stillSet func(*AgentConfigs) bool
	}{
		{
			field:    "execution_tier",
			seed:     func(ac *AgentConfigs) { ac.ExecutionTier = "low"; ac.ExecutionTierReason = "PUL-1: stable" },
			stillSet: func(ac *AgentConfigs) bool { return ac.ExecutionTier != "" || ac.ExecutionTierReason != "" },
		},
		{
			field: "execution_llm",
			seed: func(ac *AgentConfigs) {
				ac.ExecutionLLM = &AgentLLMConfig{Provider: "anthropic", ModelID: "x"}
				ac.ExecutionLLMReason = "PUL-2: cheaper"
			},
			stillSet: func(ac *AgentConfigs) bool { return ac.ExecutionLLM != nil || ac.ExecutionLLMReason != "" },
		},
	} {
		sc := &StepConfig{AgentConfigs: &AgentConfigs{}}
		tc.seed(sc.AgentConfigs)
		if !clearStepConfigField(sc, tc.field) {
			t.Fatalf("clearStepConfigField did not recognize %q", tc.field)
		}
		if tc.stillSet(sc.AgentConfigs) {
			t.Errorf("clearing %q left the decision or its reason behind", tc.field)
		}
	}
}

func TestOpsReasonsSurviveConfigMerge(t *testing.T) {
	// The reason has to travel with the decision, or a later reviewer sees a
	// pinned tier with no justification and reports a gap that does not exist.
	source := &AgentConfigs{
		ExecutionTier:       "medium",
		ExecutionTierReason: "PUL-77: quality-equivalent on 20 samples",
		ExecutionLLMReason:  "PUL-78: pin required for tool-calling fidelity",
	}
	target := &AgentConfigs{}

	MergeAgentConfigFields(target, source, "step-x", loggerv2.NewNoop())

	if target.ExecutionTierReason != source.ExecutionTierReason {
		t.Errorf("tier reason did not merge: %q", target.ExecutionTierReason)
	}
	if target.ExecutionLLMReason != source.ExecutionLLMReason {
		t.Errorf("llm reason did not merge: %q", target.ExecutionLLMReason)
	}
}
