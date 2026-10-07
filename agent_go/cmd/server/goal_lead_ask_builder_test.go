package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// PLAT-697: the Pulse asks the workflow's Builder chat. A fix request is sent
// only when pulse.autonomy.change is auto; at ask it becomes one decision with
// the Pulse's recommendation, and the owner's Accept carries it to the Builder
// chat. A question goes at either level and may change nothing. The goal
// check's context names plan changes since the last check.
func TestPulseAskBuilderHonoursChangeLevelAndCheckSeesPlanChanges(t *testing.T) {
	env := newCrewFunctionEnv(t)
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	const ws = "Workflow/reports"
	if err := os.MkdirAll(filepath.Join(root, "Workflow", "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	const builderChat = "builder-sess-1"

	type turn struct {
		session, query string
		perms          stepworkflow.GoalWorkPermissions
	}
	var mu sync.Mutex
	var turns []turn
	previousFind, previousTurn, previousLimiter := findPulseBuilderChat, pulseBuilderAskTurn, pulseBuilderAsks
	findPulseBuilderChat = func(*StreamingAPI, context.Context, string, *WorkflowManifest, string, string) (string, error) {
		return builderChat, nil
	}
	pulseBuilderAskTurn = func(_ context.Context, reqMap map[string]interface{}, sessionID, _ string) (internalSessionTurnResult, error) {
		perms, _ := goalWorkTurnPermissions(sessionID)
		mu.Lock()
		turns = append(turns, turn{session: sessionID, query: fmt.Sprint(reqMap["query"]), perms: perms})
		mu.Unlock()
		return internalSessionTurnResult{FinalResponse: "Step growth summary now records the subscriber delta; validate_plan_change passed."}, nil
	}
	pulseBuilderAsks = &hourlyAskLimiter{}
	t.Cleanup(func() {
		findPulseBuilderChat, pulseBuilderAskTurn, pulseBuilderAsks = previousFind, previousTurn, previousLimiter
	})

	ctx := context.Background()
	fix := pulseBuilderAskRequest{
		UserID: "owner", WorkspacePath: ws, PulseSession: goalLeadSessionID("reports", 1), Kind: "fix",
		Message:  "Make step-growth-summary record the subscriber delta every run.",
		Evidence: "growth ran 6 times (runs 41-46) without a subscriber reading",
		Title:    "Have the growth step record subscriber numbers every run?",
		Why:      "The goal cannot be judged while growth runs record no subscriber numbers.",
		Wait:     5 * time.Second,
	}

	// change=ask: no Builder turn, one decision with the recommendation.
	out, err := env.api.askBuilder(ctx, fix)
	if err != nil || out["status"] != "decision_created" {
		t.Fatalf("fix at change=ask = %v, %v; want a decision instead", out, err)
	}
	if len(turns) != 0 {
		t.Fatalf("a fix at change=ask reached the Builder chat: %+v", turns)
	}
	pending, err := listReportHumanInputs(ctx, ws, "pending", "")
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending decisions = %+v, %v", pending, err)
	}
	decision := pending[0]
	if decision.ApplyContract.Mode != "targeted_fixer" || !strings.Contains(decision.ApplyContract.ApprovedScope, "step-growth-summary") ||
		decision.Recommendation == nil || decision.Recommendation.OptionID != "approve" {
		t.Fatalf("decision = %+v (recommendation %+v)", decision, decision.Recommendation)
	}
	answered, err := answerReportHumanInput(ctx, ws, decision.ID, ReportHumanInputAnswerRequest{SelectedOptionID: "approve", AnsweredBy: "owner", AnsweredByKind: "human_ui", AnsweredVia: "report_ui"})
	if err != nil {
		t.Fatal(err)
	}
	if apply := decisionApplyChatMessage(*answered); !strings.Contains(apply, "Apply it now, here in this chat") || !strings.Contains(apply, "step-growth-summary") {
		t.Fatalf("Accept must send the fix to the Builder chat: %q", apply)
	}

	// change=auto: the fix runs in the Builder chat, which may change but not run.
	fix.Perms = stepworkflow.GoalWorkPermissions{Change: true}
	if out, err = env.api.askBuilder(ctx, fix); err != nil || out["status"] != "completed" {
		t.Fatalf("fix at change=auto = %v, %v", out, err)
	}
	// A question goes at change=ask too, and its turn may change nothing.
	question := pulseBuilderAskRequest{UserID: "owner", WorkspacePath: ws, PulseSession: fix.PulseSession, Kind: "question",
		Message: "What changed in step-growth-summary yesterday, and why?", Wait: 5 * time.Second}
	if out, err = env.api.askBuilder(ctx, question); err != nil || out["status"] != "completed" {
		t.Fatalf("question at change=ask = %v, %v", out, err)
	}
	mu.Lock()
	if len(turns) != 2 || turns[0].session != builderChat || turns[1].session != builderChat {
		t.Fatalf("Builder turns = %+v", turns)
	}
	if !strings.Contains(turns[0].query, "asks this Builder chat for a fix") || !turns[0].perms.Change || turns[0].perms.Run || turns[0].perms.Outward {
		t.Fatalf("fix turn = %+v", turns[0])
	}
	if !strings.Contains(turns[1].query, "asks this Builder chat a question") || turns[1].perms.Change || turns[1].perms.Run {
		t.Fatalf("question turn = %+v", turns[1])
	}
	mu.Unlock()
	// A failed-run turn may only ask questions.
	fix.TurnKind = goalLeadTurnRunFailed
	if _, err := env.api.askBuilder(ctx, fix); err == nil || !strings.Contains(err.Error(), "questions only") {
		t.Fatalf("fix from a failed-run turn = %v", err)
	}

	// A plan edit since the last check appears in the check's context.
	env.mock.mu.Lock()
	env.mock.files[ws+"/planning/changelog/changelog-2026-10-08-08-00-00.json"] = fmt.Sprintf(`{"entries":[{"timestamp":%q,"tool":"update_message_sequence_step","reason":"Record the subscriber delta","step_ids":["step-growth-summary"],"origin":{"type":"builder","session_id":%q,"username":"owner"}}]}`,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), builderChat)
	env.mock.mu.Unlock()
	facts := goalLeadAgentContext(ctx, ws)
	changes, _ := facts["plan_changes"].([]goalLeadPlanChange)
	if len(changes) != 1 || changes[0].StepIDs[0] != "step-growth-summary" || changes[0].Session != builderChat || !strings.Contains(fmt.Sprint(facts["plan_changes_note"]), "ask_builder") {
		t.Fatalf("plan_changes = %+v", facts["plan_changes"])
	}
	if asks, _ := facts["builder_asks"].([]map[string]string); len(asks) != 2 || asks[0]["status"] != "completed" {
		t.Fatalf("builder_asks = %+v", facts["builder_asks"])
	}
}
