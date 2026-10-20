package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"testing"
)

func TestLaptopConnectionIsAdditiveForBothCrewOrigins(t *testing.T) {
	target := &laptopConnectionTarget{DeviceID: "offline-laptop", ResourceID: "project"}
	api := &StreamingAPI{}
	if err := api.validateLaptopConnection(&UserClaims{UserID: "owner"}, target); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"work", "code"} {
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
		for _, trigger := range []string{"cron", "code_chat", "auto_notification", "bot:slack"} {
			denied := req
			denied.TriggeredBy = trigger
			if laptopConnectionTurn(denied, resolved) {
				t.Fatalf("%s inherited laptop", trigger)
			}
		}
		denied := req
		denied.PinRunMode = true
		if laptopConnectionTurn(denied, resolved) {
			t.Fatal("Run inherited laptop")
		}
	}
}
