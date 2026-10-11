package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestPulseResultDoesNotReviveDisabledOrRotatedConversation(t *testing.T) {
	env := newCrewFunctionEnv(t)
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	const ws = "Workflow/reports"
	if err := os.MkdirAll(filepath.Join(root, ws), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := ReadWorkflowManifest(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := ensureGoalLeadConversation(context.Background(), ws, manifest.ID, time.Now().UTC(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	env.api.lastQueryRequests = map[string]QueryRequest{conv.SessionID: {SelectedFolder: ws}}
	previous := goalLeadTurnRunner
	var turns atomic.Int32
	goalLeadTurnRunner = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		turns.Add(1)
		return internalSessionTurnResult{}, nil
	}
	t.Cleanup(func() { goalLeadTurnRunner = previous })
	for _, enabled := range []bool{false, true} {
		env.mock.mu.Lock()
		env.mock.files[manifestPath(ws)] = fmt.Sprintf(`{"id":%q,"access":{"owners":["owner"]},"pulse":{"enabled":%t}}`, manifest.ID, enabled)
		env.mock.mu.Unlock()
		expected := conv.SessionID
		if enabled {
			expected = goalLeadSessionID(manifest.ID, conv.Generation+1)
		}
		_, _, err := env.api.runGoalLeadTurn(context.Background(), ws, goalLeadTurn{Kind: goalLeadTurnFunctionResult, Body: "saved result", ExpectedSessionID: expected})
		if err != errPulseResultIneligible {
			t.Fatalf("enabled=%v: got %v", enabled, err)
		}
	}
	if turns.Load() != 0 {
		t.Fatal("ineligible completion ran Pulse")
	}
	env.api.stoppedSessions = map[string]bool{conv.SessionID: true}
	if env.api.executeSyntheticTurnWithOutcome(conv.SessionID, "saved result", "", nil) {
		t.Fatal("result revived a stopped Pulse")
	}
}
