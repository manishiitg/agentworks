package server

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The message that applies an answered decision in the workflow's Builder
// chat, where the user can watch. The answering chat turn applies it at once;
// a decision left answered but not applied keeps this message, and Needs you
// offers it as "Apply in chat". Scheduled runs never apply decisions.
func decisionApplyChatMessage(input ReportHumanInput) string {
	if !strings.EqualFold(strings.TrimSpace(input.Status), "answered") {
		return ""
	}
	id := strings.TrimSpace(input.ID)
	answer := strings.TrimSpace(input.SelectedOptionID)
	if answer == "" && strings.TrimSpace(input.Note) != "" {
		answer = "a written answer"
	}
	if id == "" || answer == "" {
		return ""
	}
	intro := fmt.Sprintf("I just answered the decision %q in %s (answer: %s). Apply it now, here in this chat.", id, input.WorkspacePath, answer)
	switch scheduledDecisionApplyMode(input) {
	case "external_wait":
		return ""
	default:
		// Every other decision, structured or older, is applied here by the
		// Builder: the user answered in person and is watching. There is no
		// separate Fixer or pre-run turn; the Builder makes the repair itself.
		return intro + decisionApplyInstructions(input)
	}
}

// decisionApplyInstructions tells the Builder how to apply one answered decision.
// A structured decision's saved scope, checks and linked issue bound the repair;
// whatever option the user picked (approve, a smaller option such as
// label_only, a rejection, a deferral) is honored as written.
func decisionApplyInstructions(input ReportHumanInput) string {
	id := strings.TrimSpace(input.ID)
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nRead it with get_human_input_request(workspace_path=%q, input_id=%q). Its context says what happens for each answer. Honor the answer exactly, including a rejection, a deferral or a smaller option than the one proposed.\n", input.WorkspacePath, id)
	contract := input.ApplyContract
	if scope := strings.TrimSpace(contract.ApprovedScope); scope != "" {
		fmt.Fprintf(&b, "- Scope: apply ONLY this approved scope: %q. Make the smallest coherent change and do not broaden it.\n", scope)
	}
	if len(contract.PreRunChecks) > 0 {
		checks, _ := json.Marshal(contract.PreRunChecks)
		fmt.Fprintf(&b, "- Checks: run every required static or side-effect-free check before you finish (through the real consumer where possible), and call validate_plan_change whenever the plan changed: %s\n", checks)
	}
	if proof := strings.TrimSpace(contract.PostRunProof); proof != "" {
		fmt.Fprintf(&b, "- Proof that needs a later run: %q. Say plainly what remains to be proven; do not claim it.\n", proof)
	}
	if issueID := strings.TrimSpace(contract.IssueID); issueID != "" {
		fmt.Fprintf(&b, "- Linked issue %q: read it with get_pulse_state(view=\"backlog\", detail=\"full\") before changing anything. Keep it open until the change is applied, then record what was done and close it.\n", issueID)
	}
	b.WriteString(`- Approved or a smaller option chosen: make the change with the normal typed Builder tools, re-read the changed artifact to confirm it landed, then call mark_human_input_consumed with an outcome_summary that says in plain words what changed.
- Rejected: call mark_human_input_consumed with an outcome_summary that the user declined and nothing changed.
- Deferred, or anything you cannot apply safely right now: change nothing, leave it answered, and tell me in one or two sentences why.
Do not run the workflow, back up, publish or notify. Keep your reply short and plain.`)
	return b.String()
}

// withDecisionApplyMessages fills ApplyMessage on every answered decision, so
// Needs you can offer "Apply in chat" until it is applied.
func withDecisionApplyMessages(inputs []ReportHumanInput) {
	for i := range inputs {
		inputs[i].ApplyMessage = decisionApplyChatMessage(inputs[i])
	}
}
