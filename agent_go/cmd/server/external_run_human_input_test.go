package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	mcpagentevents "github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

type discardEvents struct{}

func (discardEvents) HandleEvent(context.Context, *mcpagentevents.AgentEvent) error { return nil }
func (discardEvents) Name() string                                                  { return "discard" }

// PLAT-365, end to end over the external API: a question a workflow asks
// through the real orchestrator helper shows up in run_status.pending_inputs
// of the session that asked it, and run_reply_input answers it. Before the
// fix the helper stored no session, so run_status listed nothing and the
// reply was refused.
func TestExternalRunAnswersHelperQuestion(t *testing.T) {
	f := newExternalToolsFixture(t)
	const session = "run-plat365"
	f.api.activeSessions = map[string]*ActiveSessionInfo{session: {
		SessionID: session, UserID: "owner", WorkspacePath: "Workflow/invoices",
		AgentMode: "workflow_phase", PhaseID: "workflow-builder",
	}}
	bo, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), discardEvents{}, "test", "", 0, "", nil, nil, false, nil, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestID := "plat365-external-" + time.Now().Format("150405.000000000")
	done := make(chan string, 1)
	go func() {
		choice, _ := bo.RequestMultipleChoiceFeedback(context.Background(), requestID, "Which region?", []string{"EU", "US"}, "", session, "invoices")
		done <- choice
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		body := externalTestBody(t, f.call(t, "owner", "run_status", map[string]any{"workflow_id": "invoices", "session_id": session}), 200)
		if pending, _ := body["pending_inputs"].([]any); len(pending) > 0 {
			if !strings.Contains(stringOf(pending), requestID) || body["needs_user_input"] != true {
				t.Fatalf("pending_inputs = %v", pending)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper's question never appeared in run_status")
		}
		time.Sleep(20 * time.Millisecond)
	}
	externalTestBody(t, f.call(t, "owner", "run_reply_input", map[string]any{"workflow_id": "invoices", "session_id": session, "request_id": requestID, "response": "Antarctica"}), 400)
	externalTestBody(t, f.call(t, "owner", "run_reply_input", map[string]any{"workflow_id": "invoices", "session_id": session, "request_id": requestID, "response": "US"}), 200)
	select {
	case choice := <-done:
		if choice != "option1" {
			t.Fatalf("helper got %q, want option1", choice)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the answer never reached the waiting helper")
	}
}

func stringOf(v any) string {
	var b strings.Builder
	for _, item := range v.([]any) {
		if m, ok := item.(map[string]any); ok {
			for _, value := range m {
				if s, ok := value.(string); ok {
					b.WriteString(s + " ")
				}
			}
		}
	}
	return b.String()
}
