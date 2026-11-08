package services

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
)

type startedTurn struct {
	sessionID, userID string
	req               map[string]interface{}
}

func whatsappManagerForTest(t *testing.T) (*BotConversationManager, chan startedTurn) {
	t.Helper()
	manager := NewBotConversationManager(nil, "", "")
	manager.RegisterConnector(&testBotConnector{name: "whatsapp"})
	started := make(chan startedTurn, 1)
	manager.SetStartSessionFunc(func(_ context.Context, req map[string]interface{}, sessionID, userID string, _ func(*events.AgentEvent)) error {
		started <- startedTurn{sessionID, userID, req}
		return nil
	})
	return manager, started
}

func waitStarted(t *testing.T, started chan startedTurn) startedTurn {
	t.Helper()
	select {
	case got := <-started:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("session never started")
	}
	return startedTurn{}
}

// Crews are read-only for everyone but their owner. A WhatsApp message to
// someone else's crew runs as the paired user — never as the crew's owner.
func TestWhatsAppOtherOwnersCrewRunsAsThePairedUser(t *testing.T) {
	manager, started := whatsappManagerForTest(t)
	profileUser := make(chan string, 1)
	manager.SetProfileTurnFunc(func(_ context.Context, userID string, msg BotIncomingMessage, _ ThreadID) (map[string]interface{}, string, bool, error) {
		profileUser <- userID
		return map[string]interface{}{"agent_profile_id": "work", "query": msg.Text}, "reader-crew-chat", true, nil
	})
	manager.HandleIncomingMessage(BotIncomingMessage{
		Platform: "whatsapp", UserID: "phone", WorkspaceUserID: "reader", ChannelID: "dm", Text: "status?", IsMention: true,
		PresetWorkflow: &ChannelRoute{ProfileID: "work", ConversationKey: "crew-aaa", WorkspacePath: "_users/owner/Chats/Work/projects/alpha", BotGrant: "run", WorkshopMode: "run"},
	})
	if got := <-profileUser; got != "reader" {
		t.Fatalf("crew turn built for %q, want the paired user", got)
	}
	if got := waitStarted(t, started); got.userID != "reader" {
		t.Fatalf("session runs as %q, want the paired user (not the crew owner)", got.userID)
	}
}

// PLAT-746-477: channel adapters cannot recreate the removed workflow assistant.
func TestWorkflowBotMessagesRequireACrewAgent(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	for _, platform := range []string{"whatsapp", "slack"} {
		route := &ChannelRoute{WorkflowID: "example", WorkspacePath: "Workflow/example", WorkshopMode: "run"}
		req := manager.buildQueryRequest("hello", "owner", "channel", route, platform, "owner")
		if req["_bot_prepare_error"] == nil || req["agent_mode"] != nil {
			t.Fatalf("%s workflow message constructed a turn: %v", platform, req)
		}
		req = manager.buildQueryRequestForActive(&activeBotSession{AgentMode: "workflow_phase", WorkspacePath: route.WorkspacePath}, "continue", "owner", platform, ThreadID{Platform: platform})
		if req["_bot_prepare_error"] == nil || req["agent_mode"] != nil {
			t.Fatalf("%s resumed a workflow assistant: %v", platform, req)
		}
	}
}
