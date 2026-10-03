package server

import (
	"strings"
	"testing"
)

// An answered decision carries its Builder chat apply message in lists, so
// Needs you can offer "Apply in chat" until it is applied; a pending or
// consumed one carries none.
func TestDecisionListsCarryTheApplyMessageForAnsweredDecisions(t *testing.T) {
	inputs := []ReportHumanInput{
		{ID: "answered", WorkspacePath: "Workflow/w", Status: "answered", SelectedOptionID: "approve", ApplyContract: ReportHumanInputApplyContract{Mode: "direct_apply"}},
		{ID: "pending", WorkspacePath: "Workflow/w", Status: "pending"},
		{ID: "consumed", WorkspacePath: "Workflow/w", Status: "consumed", SelectedOptionID: "approve"},
	}
	withDecisionApplyMessages(inputs)
	if msg := inputs[0].ApplyMessage; !strings.Contains(msg, `"answered"`) || !strings.Contains(msg, "Apply it now") {
		t.Fatalf("answered decision has no usable apply message: %q", msg)
	}
	if inputs[1].ApplyMessage != "" || inputs[2].ApplyMessage != "" {
		t.Fatalf("only answered decisions get an apply message: %q / %q", inputs[1].ApplyMessage, inputs[2].ApplyMessage)
	}
}
