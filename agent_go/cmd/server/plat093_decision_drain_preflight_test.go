package server

import (
	"strings"
	"testing"
)

func TestPendingDecisionNoticeDoesNotAddATurnOrInferAnAnswer(t *testing.T) {
	turns := []scheduledWorkshopTurn{
		{label: "upgrade", upgradeTarget: "1.0.99", query: "upgrade"},
		{label: "decision-drain-preflight", decisionDrain: true, query: "drain"},
		{label: "schedule-message-1", query: "run the workflow"},
		{label: "schedule-message-2", query: "summarize"},
	}
	got := attachScheduledPendingDecisionNotice(turns, []ReportHumanInput{
		{ID: "ops-decision-step-execution-pipeline", Source: "ops_review", Status: "pending"},
	})
	if len(got) != len(turns) {
		t.Fatalf("pending decision context added a turn: got %d, want %d", len(got), len(turns))
	}
	if got[0].query != "upgrade" || got[1].query != "drain" || got[3].query != "summarize" {
		t.Fatalf("notice must affect only the first normal schedule turn: %+v", got)
	}
	for _, want := range []string{
		"ops-decision-step-execution-pipeline (source: ops_review)",
		"Do not infer an answer",
		"do not block unrelated safe work",
		"get_human_input_request",
		"run the workflow",
	} {
		if !strings.Contains(got[2].query, want) {
			t.Fatalf("pending decision notice missing %q:\n%s", want, got[2].query)
		}
	}
}
