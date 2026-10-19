package server

import (
	"context"
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

func TestOnlyOwnedNonisolatedCrewSchedulesUseChatDelivery(t *testing.T) {
	job := productScheduleJob{ProjectID: "project", Profile: agentprofiles.Profile{ID: "crew"}}
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
