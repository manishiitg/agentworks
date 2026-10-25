package step_based_workflow

import (
	"fmt"
	"strings"
)

// Learnings access levels (mirror of knowledgebase_access).
const (
	LearningsAccessRead      = "read"
	LearningsAccessReadWrite = "read-write"
	LearningsAccessNone      = "none"
)

// resolveLearningsAccess returns the effective learnings_access for a step.
// Explicit value wins; empty falls back to auto-migration:
//   - learning_objective non-empty → "read-write" (preserves legacy behavior)
//   - learning_objective empty     → "read"       (legacy per-step guides)
//
// An explicit bad value is normalized to "read" with a warning at validation time.
func resolveLearningsAccess(agentConfigs *AgentConfigs) string {
	if agentConfigs == nil {
		return LearningsAccessRead
	}
	v := strings.TrimSpace(agentConfigs.LearningsAccess)
	switch v {
	case LearningsAccessRead, LearningsAccessReadWrite, LearningsAccessNone:
		return v
	case "":
		if strings.TrimSpace(agentConfigs.LearningObjective) != "" {
			return LearningsAccessReadWrite
		}
		return LearningsAccessRead
	default:
		return LearningsAccessRead
	}
}

// canReadLearnings reports whether this step's execution prompt should include
// legacy per-step guides. Read is the default unless explicitly set to
// "none"; routing steps always skip to keep their prompts lean.
func canReadLearnings(agentConfigs *AgentConfigs, step PlanStepInterface) bool {
	if step != nil && isRoutingStep(step) {
		return false
	}
	return resolveLearningsAccess(agentConfigs) != LearningsAccessNone
}

// resolveExecutionLearningsAccess is the single capability decision used by
// both prompt injection and filesystem guards. Deterministic routing steps
// intentionally consume no workflow learnings; returning none here prevents
// their shell access from being broader than their prompt.
func resolveExecutionLearningsAccess(agentConfigs *AgentConfigs, step PlanStepInterface) string {
	if !canReadLearnings(agentConfigs, step) {
		return LearningsAccessNone
	}
	return resolveLearningsAccess(agentConfigs)
}

// canWriteLearnings reports whether the step agent should run its direct
// post-completion learnings turn. Requires learnings_access == "read-write"
// AND a non-empty learning_objective (the extraction target for the writer).
// Routing steps always skip.
func canWriteLearnings(agentConfigs *AgentConfigs, step PlanStepInterface) bool {
	if step != nil && isRoutingStep(step) {
		return false
	}
	if agentConfigs == nil {
		return false
	}
	if resolveLearningsAccess(agentConfigs) != LearningsAccessReadWrite {
		return false
	}
	return strings.TrimSpace(agentConfigs.LearningObjective) != ""
}

// shouldDirectWriteLearnings reports whether the step is configured for
// learnings writes. Since direct is now the only mode, this collapses to
// "is the step access+objective gate satisfied?". Kept as a named helper so
// the call sites still read intuitively.
func shouldDirectWriteLearnings(agentConfigs *AgentConfigs, step PlanStepInterface) bool {
	return canWriteLearnings(agentConfigs, step)
}

const reasonEscapeHatch = " If the evidence does not settle it, do not make the change: raise a decision with create_human_input_request and park the finding awaiting_user. An invented reason is worse than no change."

// validateExecutionTierChange rejects a tier override that states no reason.
// Configuration stays stable until explicitly changed; record the evidence.
func validateExecutionTierChange(tier string, reason string) error {
	if strings.TrimSpace(tier) == "" || strings.TrimSpace(reason) != "" {
		return nil
	}
	return fmt.Errorf("execution_tier=%q requires execution_tier_reason: the tier stays configured until explicitly changed; successful-run counts do not change it. Cite the owning architecture_review finding (and the human_input_id if it was approved), the current state, and the evidence.%s",
		strings.TrimSpace(tier), reasonEscapeHatch)
}

// validateExecutionLLMChange rejects a model pin that states no reason. A pin
// outranks tier entirely, so it silently overrides every tier decision above it.
func validateExecutionLLMChange(pinned bool, reason string) error {
	if !pinned || strings.TrimSpace(reason) != "" {
		return nil
	}
	return fmt.Errorf("execution_llm requires execution_llm_reason: a model pin outranks execution_tier entirely, so it silently overrides every tier decision above it and will not follow provider-profile updates. Cite the owning architecture_review finding (and the human_input_id if it was approved), the current model, and the capability/cost comparison that justified the pin.%s",
		reasonEscapeHatch)
}
