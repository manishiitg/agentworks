package server

import (
	"strings"
	"testing"
)

// The Builder chat applies every answered decision itself; there is no Fixer
// turn and no pre-run drain (PLAT-381). Whatever option was picked is honored.
func TestDecisionApplyChatMessageAppliesInTheBuilderForEveryStructuredMode(t *testing.T) {
	base := ReportHumanInput{ID: "goal-work-shopify-filter", WorkspacePath: "Workflow/salesoutreach", Status: "answered", SelectedOptionID: "approve", Source: "strategic_review"}
	for _, mode := range []string{"", "no_change", "direct_apply", "targeted_fixer"} {
		input := base
		input.ApplyContract.Mode = mode
		msg := decisionApplyChatMessage(input)
		for _, want := range []string{"Apply it now, here in this chat", "goal-work-shopify-filter", "get_human_input_request", "mark_human_input_consumed", "Honor the answer exactly"} {
			if !strings.Contains(msg, want) {
				t.Errorf("mode %q: message missing %q:\n%s", mode, want, msg)
			}
		}
		for _, banned := range []string{"Fixer", "FIXER", "DRAIN", "PRE-RUN", "dedicated"} {
			if strings.Contains(msg, banned) {
				t.Errorf("mode %q: message still mentions %q, a role that no longer exists:\n%s", mode, banned, msg)
			}
		}
	}
}

// The reported case: a targeted_fixer decision answered with a smaller option
// (label_only) used to be handed to a drain prompt that refused repairs.
func TestDecisionApplyChatMessageAppliesASmallerOptionOnATargetedDecision(t *testing.T) {
	input := ReportHumanInput{ID: "d-label", WorkspacePath: "Workflow/social-media", Status: "answered", SelectedOptionID: "label_only", Source: "pulse",
		ApplyContract: ReportHumanInputApplyContract{Mode: "targeted_fixer", IssueID: "issue-7", ApprovedScope: "label the post type in the plan",
			PreRunChecks: []string{"validate_plan_change passes"}, PostRunProof: "next run labels posts"}}
	msg := decisionApplyChatMessage(input)
	for _, want := range []string{"(answer: label_only)", `"label the post type in the plan"`, "validate_plan_change passes", "issue-7", "Keep it open until", "next run labels posts", "smaller option"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "only direct_apply") {
		t.Errorf("the message must not tell the Builder it cannot repair:\n%s", msg)
	}
}

func TestDecisionApplyChatMessageIsEmptyWhenThereIsNothingToApply(t *testing.T) {
	for name, input := range map[string]ReportHumanInput{
		"pending":       {ID: "d1", WorkspacePath: "Workflow/x", Status: "pending"},
		"no answer":     {ID: "d2", WorkspacePath: "Workflow/x", Status: "answered"},
		"external wait": {ID: "d3", WorkspacePath: "Workflow/x", Status: "answered", SelectedOptionID: "approve", ApplyContract: ReportHumanInputApplyContract{Mode: "external_wait"}},
	} {
		if msg := decisionApplyChatMessage(input); msg != "" {
			t.Errorf("%s: expected no chat message, got %q", name, msg)
		}
	}
}
