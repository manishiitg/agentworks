package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"testing"
)

func TestLaptopConnectionKeepsCrewOnServer(t *testing.T) {
	target := &laptopConnectionTarget{DeviceID: "offline-laptop", ResourceID: "project"}
	api := &StreamingAPI{}
	if err := api.validateLaptopConnection(&UserClaims{UserID: "owner"}, target); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"work"} {
		profile := routeTestProfile(origin, true, "")
		conversation := ProductConversationRecord{SessionID: "same-chat", WorkspacePath: "Crew/example"}
		req, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hello", LaptopConnection: target}, conversation)
		if err != nil || req.SelectedFolder != "Crew/example" || req.RestoredConversationSessionID != "same-chat" {
			t.Fatalf("connection changed runtime: %+v %v", req, err)
		}
		resolved := &resolvedAgentProfile{Definition: agentprofiles.Profile{ID: origin}}
		if !laptopConnectionTurn(req, resolved) {
			t.Fatal("own interactive turn lost connection")
		}
		// The person's own automatic turns keep the laptop (owner, 5 Nov), so the tools never change between turns.
		for _, trigger := range []string{"cron", "code_chat", "auto_notification", "agent_message"} {
			own := req
			own.TriggeredBy = trigger
			own.IsAutoNotification = trigger == "auto_notification"
			if !laptopConnectionTurn(own, resolved) {
				t.Fatalf("own %s turn lost the laptop", trigger)
			}
		}
		// Turns that work for other people never get it.
		for name, other := range map[string]func(*QueryRequest){
			"run":                  func(r *QueryRequest) { r.PinRunMode = true },
			"slack":                func(r *QueryRequest) { r.BotPlatform = "slack" },
			"other person's agent": func(r *QueryRequest) { r.AgentMessageFromOtherPerson = true },
		} {
			denied := req
			other(&denied)
			if laptopConnectionTurn(denied, resolved) {
				t.Fatalf("%s inherited laptop", name)
			}
		}
	}
}
