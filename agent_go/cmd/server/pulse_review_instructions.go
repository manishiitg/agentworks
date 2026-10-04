package server

import (
	"fmt"
	"strings"
)

// pulseReviewerInstruction is the reviewer's task, the part of the old
// dispatch turn that was meant for the reviewer rather than the dispatcher.
func pulseReviewerInstruction(pulseRunID, module string) string {
	if module == pulseModulePlanDriftReview {
		return fmt.Sprintf(`PULSE PLAN DRIFT REVIEW. pulse_run_id=%q, module=%q. The Gate marked this module due. Load read_skill(skills=[{"name":"builder-reference","path":"references/plan-drift-review.md"}]) and follow it exactly. Establish ground truth per due step, apply and verify safe workflow-owned fixes directly, and only route what you cannot safely fix yourself: a genuine human decision, a platform-owned boundary, or (rarely, as a last resort) a fixer_handoff for technical_review. Finish with the terminal result for plan_drift_review. Do not render a dashboard, back up, publish or notify.`, pulseRunID, module)
	}
	_, reference, contract := pulseModuleReviewParts(module)
	return fmt.Sprintf(`PULSE %s. pulse_run_id=%q, module=%q. The Gate marked this module due. Read get_pulse_state(view="review_notes", module=%q) once for relevant prior reasoning. Load read_skill(skills=[{"name":"builder-reference","path":"references/%s.md"}]). %s
%sDo not render a dashboard, back up, publish or notify.`, strings.ToUpper(strings.ReplaceAll(module, "_", " ")), pulseRunID, module, module, reference, contract, pulseReviewerRecordRules)
}
