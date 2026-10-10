package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Drive durable admission and real delivery, replacing only the model. A busy
// Builder receives the message later, with current Pulse authority; its final
// chat text must not be converted into a reply.
func TestPulseMessagesBuilderUnderCurrentAuthorityWithoutCapturedReply(t *testing.T) {
	env := newCrewFunctionEnv(t)
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	const ws = "Workflow/reports"
	if err := os.MkdirAll(filepath.Join(root, ws), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	manifest, _, err := ReadWorkflowManifest(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	env.mock.mu.Lock()
	env.mock.files[manifestPath(ws)] = fmt.Sprintf(`{"id":%q,"label":"Reports","access":{"owners":["owner"]},"pulse":{"enabled":true,"autonomy":{"level":0}}}`, manifest.ID)
	env.mock.files[ws+"/soul/soul.md"] = "Goal: improve measured outcomes"
	env.mock.mu.Unlock()
	conv, err := ensureGoalLeadConversation(ctx, ws, manifest.ID, time.Now().UTC(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldFind, oldTurn := findPulseBuilderChat, agentMessageTurn
	findPulseBuilderChat = func(*StreamingAPI, context.Context, string, *WorkflowManifest, string, string) (string, error) {
		return "builder-message-test", nil
	}
	finished := make(chan error, 1)
	agentMessageTurn = func(_ *StreamingAPI, turnCtx context.Context, request map[string]interface{}, session, user string) (internalSessionTurnResult, error) {
		if _, held := goalWorkTurnPermissions(session); held {
			finished <- fmt.Errorf("Pulse authority applied before receiving turn admission")
			return internalSessionTurnResult{}, nil
		}
		releaseAuthority, admissionErr := acquireAgentMessageAuthority(turnCtx, session)
		if admissionErr != nil {
			finished <- admissionErr
			return internalSessionTurnResult{}, admissionErr
		}
		defer releaseAuthority()
		perms, held := goalWorkTurnPermissions(session)
		if session != "builder-message-test" || user != "owner" || !held || perms.Run || perms.Change || !strings.Contains(fmt.Sprint(request["query"]), "Explicit reply requested") {
			finished <- fmt.Errorf("wrong receiving identity/authority/message: %s %s %+v held=%v request=%v", session, user, perms, held, request)
		} else {
			finished <- nil
		}
		return internalSessionTurnResult{FinalResponse: "This final chat answer is not a message"}, nil
	}
	t.Cleanup(func() { findPulseBuilderChat, agentMessageTurn = oldFind, oldTurn })
	release := env.api.lockSessionInputLane("builder-message-test")
	out, err := env.api.askBuilder(ctx, pulseBuilderAskRequest{UserID: "owner", WorkspacePath: ws, PulseSession: conv.SessionID, Message: "Explicit reply requested", Perms: stepworkflow.GoalWorkPermissions{Run: true}, SubmissionID: "builder-message"})
	release()
	if err != nil || out["status"] != "accepted" || out["inbox_id"] == nil || out["call_id"] != nil || out["auto_notify"] != nil {
		t.Fatalf("acknowledgement=%v error=%v", out, err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("message did not reach Builder")
	}
	inbox := fmt.Sprint(out["inbox_id"])
	deadline := time.Now().Add(time.Second)
	for {
		agentMessageMu.Lock()
		stored, err := readAgentMessageStore(ctx)
		done := false
		for _, c := range stored.Conversations {
			if c.ID == inbox && len(c.Messages) == 1 && c.Messages[0].Delivery == "delivered" {
				done = true
			}
		}
		agentMessageMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delivery not recorded or final answer was captured")
		}
		time.Sleep(5 * time.Millisecond)
	}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerWorkflow, ID: manifest.ID}, Path: ws, Chat: &codeChat{Key: pulseBuilderChatKey, SessionID: conv.SessionID}}
	received, err := env.api.readAgentMessages(ctx, "owner", caller, inbox, 0, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(received), "This final chat answer") {
		t.Fatalf("captured final chat text: %v", received)
	}
}
