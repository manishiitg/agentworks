package server

import (
	"context"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	pkgevents "github.com/manishiitg/mcpagent/events"
)

// #2837: only actual wake completions count as no-progress; Stop beats timers.
func TestAgentTaskWakeStopsAfterTwoToolFreeTurnsAndHonorsOwnerStop(t *testing.T) {
	env := newTriggerLinkEnv(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	api := env.api
	api.eventStore = openClientIdentityTestStore(t, "task-wake")
	old := agentTaskAfter
	agentTaskAfter = func(time.Duration, func()) {}
	t.Cleanup(func() { agentTaskAfter = old })
	task := agentMessage{ID: "task", From: 0, Sequence: 1, MessageType: "request", Delivery: "delivered", TaskProgress: agentTaskProgress{Session: "recipient", Attempts: 1, PendingAttempt: 1}}
	c := agentConversation{ID: "thread", Messages: []agentMessage{task}}
	agentMessageMu.Lock()
	err := saveAgentMessageStore(context.Background(), agentMessageStore{Conversations: []agentConversation{c}})
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		api.eventStore.AddEvent("recipient", testUserMessage("wake", "next step"))
		api.eventStore.AddEvent("recipient", storeevents.Event{ID: "end", Type: "unified_completion", Data: pkgevents.NewAgentEvent(storeevents.NewGenericEventData("unified_completion", map[string]interface{}{"status": "completed"}))})
		api.finishAgentTaskWake("recipient", "thread", "task", attempt, nil)
		agentMessageMu.Lock()
		saved, e := readAgentMessageStore(context.Background())
		agentMessageMu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		got := saved.Conversations[0].Messages[0]
		if got.TaskProgress.NoProgress != attempt {
			t.Fatal("wrong empty-wake count", got.TaskProgress)
		}
		if attempt == 1 {
			saved.Conversations[0].Messages[0].TaskProgress.PendingAttempt = 2
			saved.Conversations[0].Messages[0].TaskProgress.Attempts = 2
			agentMessageMu.Lock()
			e = saveAgentMessageStore(context.Background(), saved)
			agentMessageMu.Unlock()
			if e != nil {
				t.Fatal(e)
			}
		} else if state, _ := agentTaskState(saved.Conversations[0], got, time.Now()); state != "stalled" {
			t.Fatal("runaway not stalled", state)
		}
	}
	if api.stoppedSessions == nil {
		api.stoppedSessions = map[string]bool{}
	}
	api.markSessionStopped("recipient")
	if api.continueAgentBridgeTask("recipient") {
		t.Fatal("owner stop was ignored")
	}
}
