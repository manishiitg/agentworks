package server

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/goalcheck"
)

// PLAT-869: real Goal Work and check tools share one persisted clock. Disabling
// producing schedules must not hide a dated approach question behind a repair.
func TestSinglePulsePreservesDatedQuestionsWhileProductionPaused(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	const ws = "Workflow/results"
	manifest := `{"id":"results","label":"Results","pulse":{"enabled":true,"pace":"steady"}}`
	host := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{ws + "/workflow.json": manifest}})
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	if err := os.MkdirAll(filepath.Join(root, ws), 0755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	due := now.Add(12 * time.Hour)
	_, executors, _ := createPulseTools()
	call := func(name string, args map[string]interface{}) string {
		t.Helper()
		args["workspace_path"] = ws
		out, err := executors[name].(func(context.Context, map[string]interface{}) (string, error))(ctx, args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return out
	}
	call("record_pulse_goal_work", map[string]interface{}{
		"pulse_run_id": "review", "title": "Would a simpler approach get more useful replies?",
		"status": "idea", "check_at": due.Format(time.RFC3339),
	})
	call("record_pulse_goal_work", map[string]interface{}{
		"pulse_run_id": "review", "title": "A resolved old question", "status": "done",
		"action_taken": "Compared replies", "effect": "worked", "check_at": now.Add(time.Hour).Format(time.RFC3339),
	})
	out := call("record_pulse_goal_check", map[string]interface{}{
		"status": "on_track", "summary": "Replies are improving; compare the simpler approach tomorrow.",
		"next_check_in_hours": float64(72), "next_check_reason": "Repair should finish later.",
	})
	check, _, err := latestPulseGoalCheck(ctx, ws)
	if err != nil || check == nil {
		t.Fatalf("check: %+v %v", check, err)
	}
	if got := parseStoredTime(check.NextCheckAt); !got.Equal(due) {
		t.Fatalf("repair postponed question: got %s want %s", got, due)
	}
	if strings.Contains(out, "stop here") {
		t.Fatal("recording on-track must not terminate useful work")
	}
	paused := goalcheck.Facts{SchedulesPaused: true, PauseAlreadyReported: true}
	if ok, _ := decideGoalCheckDue(paused, due, time.Time{}, due.Add(-time.Minute)); ok {
		t.Fatal("paused check ignored its due time")
	}
	if ok, _ := decideGoalCheckDue(paused, due, time.Time{}, due); !ok {
		t.Fatal("reported pause suppressed a due question")
	}
	if ok, _ := decideGoalCheckDue(paused, due, due, due.Add(time.Minute)); ok {
		t.Fatal("retry guard disappeared")
	}
	if next, err := nextPulseGoalWorkQuestion(ctx, ws, due); err != nil || !next.IsZero() {
		t.Fatalf("old question would create a tight loop: %s %v", next, err)
	}
	call("record_pulse_goal_memory", map[string]interface{}{
		"action": "add", "section": "Lessons", "source": "pulse_inference",
		"text":     "More drafts did not improve replies. Compare a smaller audience; the owner has not decided.",
		"judgment": map[string]interface{}{"evidence_type": "measurement", "outcome": "pending"},
	})
	memory, err := readGoalMemory(ws)
	if err != nil || !strings.Contains(memory, "the owner has not decided") {
		t.Fatalf("concern not retained: %s %v", memory, err)
	}
}

// Check the actual active pack and turn, not the retired Gate/reviewer texts.
// This pins routing/authority contracts; it does not claim model acceptance.
func TestSinglePulseActiveInstructionsOwnGoalJudgment(t *testing.T) {
	pack, err := guidance.MaterializePulseSkill(agentworksproduct.PulseSkills())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	host := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{}})
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	text := goalLeadCharter("Results", "Workflow/results") +
		pulseLifecycleGoalCheckStep(context.Background(), "Workflow/results", "check", workflowNotificationContentInstructions{}).query
	for _, file := range pack.SupportingFiles {
		text += string(file.Content)
	}
	for _, want := range []string{"Would you build this approach today", "technical handoff alone", "No person reads this conversation", "No confirmation question", "check_at", "Needs you"} {
		if !strings.Contains(text, want) {
			t.Errorf("active Pulse loses %q", want)
		}
	}
	for _, obsolete := range []string{"Measurement comes first", "smallest useful step first", "structure, not product direction", "Do 1-3 bounded items now", "On track: stop here"} {
		if strings.Contains(text, obsolete) {
			t.Errorf("active Pulse still prescribes %q", obsolete)
		}
	}
	gate := newWorkflowConversationToolGate("schedule-goallead--results-g1", QueryRequest{}, false)
	if !gate.Admit("brain_read") || gate.Admit("brain_update") || gate.Admit("execute_step") || gate.Admit("record_summary") {
		t.Fatal("Pulse must read granted company facts and leave changes/delivery to Builder")
	}
}
