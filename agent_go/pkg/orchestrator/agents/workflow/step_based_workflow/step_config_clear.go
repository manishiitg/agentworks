package step_based_workflow

// clearStepConfigField clears a named field on the given StepConfig so the step
// falls back to preset/default behavior on next execution. All struct fields
// that back the update_step_config tool use `omitempty` + pointer-types (or
// strings / nil-able slices), so resetting to the zero value makes the JSON
// marshaler drop the key entirely — which is what agents observe as "removed".
//
// Returns true when the name matched a known field, false otherwise.
// Keep the switch in lockstep with update_step_config's JSON schema: every name
// listed in the clear_fields description must have a case here, and vice versa.
func clearStepConfigField(sc *StepConfig, name string) bool {
	if sc == nil {
		return false
	}
	// validation_schema lives on StepConfig itself, not AgentConfigs.
	if name == "validation_schema" {
		sc.ValidationSchema = nil
		return true
	}
	if sc.AgentConfigs == nil {
		// Nothing to clear below this point; agent-scoped config doesn't exist.
		// Still report the name as recognized so callers don't see a false negative.
		return isKnownAgentConfigClearField(name)
	}
	ac := sc.AgentConfigs
	switch name {
	// LLM overrides
	// PLAT-060: clearing a decision clears its justification too — a reason must
	// not outlive the change it justified, or it reads as pre-approval for a
	// future re-pin nobody reviewed.
	case "execution_llm":
		ac.ExecutionLLM = nil
		ac.ExecutionLLMReason = ""
	case "execution_llm_reason":
		ac.ExecutionLLMReason = ""
	case "execution_tier":
		ac.ExecutionTier = ""
		ac.ExecutionTierReason = ""
	case "execution_tier_reason":
		ac.ExecutionTierReason = ""

	// Slice selections
	case "servers":
		ac.SelectedServers = nil
	case "tools":
		ac.SelectedTools = nil
	case "enabled_custom_tools":
		ac.EnabledCustomTools = nil
	case "enabled_skills":
		ac.EnabledSkills = nil
	case "additional_read_paths":
		ac.AdditionalReadPaths = nil

	// Pointer-bool flags — only the ones with a corresponding setter in update_step_config.
	// The tool intentionally omits fields without a setter (e.g. enable_context_offloading,
	// keep_learning_full) since clearing what can't be set is a no-op from the agent's side.
	case "learning_objective":
		ac.LearningObjective = ""
	case "lock_code":
		ac.LockCode = nil
	case "use_code_execution_mode":
		ac.UseCodeExecutionMode = nil
	case "disable_parallel_tool_execution":
		ac.DisableParallelToolExecution = nil
	case "description_reviewed":
		ac.DescriptionReviewed = nil
	case "drift_review":
		ac.DriftReview = nil

	// String fields (empty string + omitempty drops the key)
	case "learnings_access":
		ac.LearningsAccess = ""
	case "knowledgebase_access":
		ac.KnowledgebaseAccess = ""
	case "knowledgebase_contribution":
		ac.KnowledgebaseContribution = ""
	case "review_notes":
		ac.ReviewNotes = ""
	case "coding_agent_tmux_lifecycle":
		ac.CodingAgentTmuxLifecycle = ""
	default:
		return false
	}
	return true
}

// isKnownAgentConfigClearField mirrors the switch in clearStepConfigField and is
// used when AgentConfigs is nil — callers still get a truthful "is this name
// valid" answer without needing to allocate the config just to clear fields on
// an already-empty struct.
func isKnownAgentConfigClearField(name string) bool {
	switch name {
	case "execution_llm", "execution_tier",
		"servers", "tools", "enabled_custom_tools", "enabled_skills", "additional_read_paths",
		"learning_objective", "lock_code",
		"execution_llm_reason", "execution_tier_reason",
		"use_code_execution_mode",
		"disable_parallel_tool_execution",
		"description_reviewed", "drift_review",
		"knowledgebase_access", "knowledgebase_contribution",
		"review_notes",
		"coding_agent_tmux_lifecycle":
		return true
	}
	return false
}

// retiredStepConfigClearFields are names that used to address a real field and
// no longer do. PLAT-061.
//
// They cannot simply become unknown-field errors: three workflows still carry
// these keys in step_config.json, and guidance referenced some of them, so a
// caller following stale instructions would fail its whole update call — nothing
// else in that call is applied. Nor can they stay silently "successful", which
// is what `case "transport":` with an empty body did: the agent believed it had
// cleared something it had not. That is the same failure shape as db_access.
//
// So they are acknowledged and reported as no-ops, which is the only honest
// answer: the field is gone, its stored value is already ignored, and there is
// nothing left to clear.
var retiredStepConfigClearFields = map[string]string{
	"transport":                     "never existed as a step config field",
	"learning_mode":                 "retired with the per-step learning system",
	"learnings_write_method":        "retired — direct write is the only mode",
	"knowledgebase_write_method":    "retired — direct write is the only mode",
	"db_access":                     "retired in PLAT-061 — every step gets managed read-write access",
	"disable_tier_optimization":     "retired in PLAT-061 — pin execution_tier (with its reason) instead",
	"enable_context_offloading":     "retired in PLAT-061 — never settable, never used",
	"delegating_agent_tier":         "retired in PLAT-061 — use execution_llm to override",
	"learn_code_max_fix_iterations": "retired in PLAT-061 — every stored value was a migration artifact; use lock_code to skip script repair",
	"lock_learnings":                "retired in PLAT-263 — use learnings_access=\"read\" to allow reads without writes",
	"lock_learnings_reason":         "retired with lock_learnings in PLAT-263",
}

// isRetiredStepConfigClearField reports whether a clear_fields name refers to a
// field that no longer exists, along with why. Callers should report these as
// no-ops rather than failing the call or claiming a change.
func isRetiredStepConfigClearField(name string) (string, bool) {
	reason, ok := retiredStepConfigClearFields[name]
	return reason, ok
}
