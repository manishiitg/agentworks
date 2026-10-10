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

// Pulse talks to the workflow's Builder chat like a colleague (owner,
// 2026-10-08): its message arrives there as plain text from the Pulse, the
// Builder chat's tools are held to Pulse's own permission levels, and the
// reply comes back. The goal check also sees plan changes since the last check.
func TestPulseTalksToTheBuilderChatWithinItsLevels(t *testing.T) {
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
	msg := pulseBuilderAskRequest{
		UserID: "owner", WorkspacePath: ws, PulseSession: goalLeadSessionID("reports", 1),
		Message: "What changed in step-growth-summary yesterday, and why?",
		Perms:   stepworkflow.GoalWorkPermissions{Run: true},
		Wait:    5 * time.Second,
	}
	out, err := env.api.askBuilder(ctx, msg)
	if err != nil || out["status"] != "completed" || !strings.Contains(fmt.Sprint(out["result"]), "records the subscriber delta") {
		t.Fatalf("ask_builder = %v, %v", out, err)
	}
	if out["auto_notify"] == true || out["execution_id"] != nil {
		t.Fatalf("inline reply must not start a second Pulse turn: %v", out)
	}
	mu.Lock()
	if len(turns) != 1 || turns[0].session != builderChat {
		t.Fatalf("Builder turns = %+v", turns)
	}
	if turns[0].query != "Reports Pulse: What changed in step-growth-summary yesterday, and why?" && !strings.HasSuffix(turns[0].query, "Pulse: What changed in step-growth-summary yesterday, and why?") {
		t.Fatalf("the Builder chat received %q, want the plain message from Pulse", turns[0].query)
	}
	if !turns[0].perms.Run || turns[0].perms.Change || turns[0].perms.Outward {
		t.Fatalf("the Builder chat must be held to Pulse's levels: %+v", turns[0].perms)
	}
	mu.Unlock()

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
	if asks, _ := facts["builder_asks"].([]map[string]string); len(asks) != 1 || asks[0]["status"] != "completed" {
		t.Fatalf("builder_asks = %+v", facts["builder_asks"])
	}
}
