package server

import (
	"context"
	"errors"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestScheduledCrewChatUsesNormalLiveInputPolicy(t *testing.T) {
	request := QueryRequest{TriggeredBy: "cron"}
	ordinary := context.Background()
	if shouldTryRetainedDeliveryBeforeQueue(ordinary, request, "crew-chat") {
		t.Fatal("ordinary cron request unexpectedly steers")
	}
	trusted := context.WithValue(ordinary, scheduledCrewChatDeliveryKey{}, true)
	if !shouldTryRetainedDeliveryBeforeQueue(trusted, request, "crew-chat") {
		t.Fatal("trusted schedule did not use chat delivery")
	}
	request.DisableLiveInputDelivery = true
	if shouldTryRetainedDeliveryBeforeQueue(trusted, request, "crew-chat") {
		t.Fatal("explicit no-live-input guard bypassed")
	}
}

func TestInternalQueuedResultWaiterIsInstalledBeforeTurnCanBeClaimed(t *testing.T) {
	api := newConversationTurnQueueTestAPI(map[string]string{})
	var admittedID string
	var waiter <-chan queuedConversationTurnResult
	queueLock := productConversationRegistryMutex(conversationTurnQueuePath("owner"))
	ctx := context.WithValue(context.Background(), internalQueuedTurnAdmissionKey{}, func(turnID string) {
		if queueLock.TryLock() {
			queueLock.Unlock()
			t.Fatal("turn became claimable before its internal result waiter was installed")
		}
		admittedID = turnID
		waiter = api.registerQueuedTurnWaiter(turnID, nil)
	})
	turn, _, err := api.enqueueConversationTurn(ctx, "owner", "chat", QueryRequest{Query: "scheduled message"})
	if err != nil {
		t.Fatal(err)
	}
	if admittedID != turn.ID || waiter == nil {
		t.Fatal("queued admission did not register an exact result waiter")
	}
	// Failed persistence must not create a waiter for a turn that cannot run.
	api.internalTurnQueueWrite = func(context.Context, string, string) error { return errors.New("storage unavailable") }
	admittedID = ""
	if _, _, err := api.enqueueConversationTurn(ctx, "owner", "chat", QueryRequest{Query: "another message"}); err == nil {
		t.Fatal("expected persistence failure")
	}
	if admittedID != "" {
		t.Fatal("failed queue persistence admitted an internal waiter")
	}
}

func TestOnlyOwnedNonisolatedCrewSchedulesUseChatDelivery(t *testing.T) {
	job := productScheduleJob{ProjectID: "project", Profile: agentprofiles.Profile{ID: "work"}}
	if !scheduleUsesCrewChatDelivery(job) {
		t.Fatal("Crew schedule excluded")
	}
	for _, variant := range []string{"isolated", "function", "trigger", "guest", "run-mode", "brain"} {
		candidate := job
		switch variant {
		case "isolated":
			candidate.Schedule.Isolated = true
		case "function", "trigger":
			candidate.AutomationKind = variant
		case "guest":
			candidate.GuestCallerID = "guest"
		case "run-mode":
			candidate.PinRunMode = true
		case "brain":
			candidate.Profile.ID = "knowledgebase"
		}
		if scheduleUsesCrewChatDelivery(candidate) {
			t.Fatalf("%s changed routing", variant)
		}
	}
}
