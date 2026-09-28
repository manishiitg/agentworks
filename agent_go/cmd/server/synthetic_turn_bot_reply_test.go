package server

import "testing"

// A background result (function call, background agent, workflow) that
// comes back into a chat whose latest turn was the owner's Slack DM or
// WhatsApp message is relayed to that thread; the same chat after a web turn
// (bot marks cleared) is not.
func TestSyntheticTurnRepliesToTheBotThreadThatStartedTheWork(t *testing.T) {
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{
		"work:project:dm":  {SessionID: "work:project:dm", BotPlatform: "slack", TriggeredBy: "bot:slack"},
		"work:project:wa":  {SessionID: "work:project:wa", BotPlatform: "whatsapp", TriggeredBy: "bot:whatsapp"},
		"work:project:web": {SessionID: "work:project:web"},
	}}
	for session, want := range map[string]bool{
		"bot-slack--abc":   true,
		"work:project:dm":  true,
		"work:project:wa":  true,
		"work:project:web": false,
		"unknown":          false,
	} {
		if got := api.syntheticTurnRepliesToBot(session); got != want {
			t.Errorf("%s: relays to bot = %v, want %v", session, got, want)
		}
	}
}
