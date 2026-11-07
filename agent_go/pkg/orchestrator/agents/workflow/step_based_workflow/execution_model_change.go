package step_based_workflow

import (
	"fmt"
	"strings"
)

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
