package server

import (
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	pkgevents "github.com/manishiitg/mcpagent/events"
)

// #2837: automatic progress cannot settle a request or forward another turn.
func TestAgentTaskAutoProgressNeverClosesRequestOrReadsUnrelatedFinal(t *testing.T) {
	task := agentMessage{ID: "request", MessageType: "request", Delivery: "delivered", From: 0, Sequence: 1}
	auto := agentMessage{ID: "auto", Kind: agentTaskUpdateKind, MessageType: "notification", ReplyStatus: "in_progress", From: 1, Sequence: 2}
	c := agentConversation{Messages: []agentMessage{task, auto}}
	if state, _ := agentTaskState(c, task, time.Now()); state != "open" {
		t.Fatal("automatic progress masqueraded as reply", state)
	}
	final := storeevents.Event{ID: "final", Type: "unified_completion", ReplyToAgentMessageID: "another-request", Data: pkgevents.NewAgentEvent(storeevents.NewGenericEventData("unified_completion", map[string]interface{}{"final_result": "private answer", "status": "completed"}))}
	if _, text, _ := agentTaskFinalText([]storeevents.Event{final}, task.ID); text != "" {
		t.Fatal("unrelated turn leaked", text)
	}
	final.ReplyToAgentMessageID = task.ID
	if id, text, _ := agentTaskFinalText([]storeevents.Event{final}, task.ID); id != "final" || text != "private answer" {
		t.Fatal("exact admitted reply lost", id, text)
	}
	reply := agentMessage{From: 1, ReplyStatus: "in_progress", TaskReplyTo: task.ID, Text: "still working"}
	if text := agentTaskReplySummary(c, reply); text != "" {
		t.Fatal("progress would wake sender", text)
	}
	reply.ReplyStatus = "done"
	if text := agentTaskReplySummary(c, reply); !strings.Contains(text, "done") {
		t.Fatal("terminal reply lost sender summary", text)
	}
}
