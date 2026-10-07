package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpexecutor "github.com/manishiitg/mcpagent/executor"
)

// PLAT-697 phase 3 (owner, 2026-10-07): the Goal Lead recommends, the owner
// decides. A Pulse turn may attach a recommendation to a decision but cannot
// answer it; the owner's Accept answers with the recommended option, marks the
// recommendation accepted in the decision log and copies the answer into goal
// memory (memory/goal.md).
func TestGoalLeadRecommendsAndOnlyTheOwnerAnswers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	const ws = "Workflow/substack"
	const id = "goal-check-2026-10-07"
	if err := os.MkdirAll(filepath.Join(root, "Workflow", "substack"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := createReportHumanInput(ctx, ws, ReportHumanInputCreateRequest{
		InputID: id, Source: "strategic_review", Question: "Resume the growth runs?",
		Options: []ReportHumanInputOption{{ID: "resume", Title: "Resume growth runs"}, {ID: "wait", Title: "Keep them paused"}},
	}); err != nil {
		t.Fatalf("create decision: %v", err)
	}

	pulseTurn := mcpexecutor.WithSessionID(ctx, "schedule-cron--goal-check")
	_, humanExecutors, _ := createReportHumanInputTools()
	answer := humanExecutors["answer_human_input_request"].(func(context.Context, map[string]interface{}) (string, error))
	if _, err := answer(pulseTurn, map[string]interface{}{"workspace_path": ws, "input_id": id, "selected_option_id": "resume"}); err == nil {
		t.Fatal("a Pulse turn answered a decision; only the owner may")
	}

	_, pulseExecutors, _ := createPulseWorklistTools()
	recommend := pulseExecutors["record_pulse_recommendation"].(func(context.Context, map[string]interface{}) (string, error))
	if _, err := recommend(pulseTurn, map[string]interface{}{
		"workspace_path": ws, "input_id": id, "option_id": "resume", "confidence": "high",
		"why":    "Growth has not run for 11 days and the goal has gone unmeasured.",
		"blocks": "Friday's growth run",
	}); err != nil {
		t.Fatalf("record_pulse_recommendation: %v", err)
	}
	inputs, err := listReportHumanInputs(ctx, ws, "", "")
	if err != nil || len(inputs) != 1 {
		t.Fatalf("list = %d, err=%v", len(inputs), err)
	}
	got := inputs[0]
	if got.Status != "pending" || got.AnsweredBy != "" || got.SelectedOptionID != "" {
		t.Fatalf("a recommendation must not answer: status=%s answered_by=%q selected=%q", got.Status, got.AnsweredBy, got.SelectedOptionID)
	}
	if got.Recommendation == nil || got.Recommendation.OptionID != "resume" || got.Recommendation.RecommendedBy != "pulse" || got.Recommendation.Blocks == "" {
		t.Fatalf("recommendation not attached as Pulse's: %+v", got.Recommendation)
	}

	// The owner's Accept: the answer route with the recommended option.
	if _, err := answerReportHumanInput(ctx, ws, id, ReportHumanInputAnswerRequest{
		SelectedOptionID: "resume", AnsweredBy: "alice", AnsweredByKind: "human_ui", AnsweredVia: "report_ui",
	}); err != nil {
		t.Fatalf("owner answer: %v", err)
	}
	log, err := listPulseDecisionLog(ctx, ws, 10)
	if err != nil || len(log) != 1 || log[0].OwnerResponse != "accepted" || log[0].Recommended != "Resume growth runs" {
		t.Fatalf("decision log = %+v, err=%v; want one accepted entry", log, err)
	}
	memory, err := readGoalMemory(ws)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(memory, `[owner answer] Asked "Resume the growth runs?", the owner chose "Resume growth runs" (the Goal Lead's recommendation).`) {
		t.Fatalf("goal memory has no owner-answer line:\n%s", memory)
	}
}
