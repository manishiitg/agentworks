package server

import (
	"context"
	"testing"

	"github.com/manishiitg/mcpagent/executor"
)

// One user, one chat: after a Slack DM (or WhatsApp) turn in the owner's
// own chat, the owner's next web turn runs as a web turn — its tools work —
// while a bot-claiming turn by a non-bot principal is still refused.
func TestOwnerWebTurnAfterDMRunsAsWeb(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	newAPI := func() *StreamingAPI {
		api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{
			"chat-1": {SessionID: "chat-1", UserID: "owner", BotPlatform: "slack", TriggeredBy: "bot:slack"},
		}}
		api.botExecutionSessions.Store("chat-1", botExecutionSession{Claims: &UserClaims{UserID: "owner", Provider: slackDMProvider}})
		return api
	}
	web := &UserClaims{UserID: "owner", Provider: "local"}
	incoming := executor.WithSessionID(context.Background(), "chat-1")

	// Before the fix: the web turn inherits the DM's marks.
	stale := newAPI()
	if _, err := stale.bindToolExecutionContext(context.WithValue(context.Background(), UserContextKey, web), "chat-1", QueryRequest{}, false)(incoming, "read_skill"); err == nil {
		t.Fatal("expected the stale bot marks to block tools without the reset")
	}

	api := newAPI()
	api.clearBotOriginForOwnerTurn("chat-1", web, QueryRequest{})
	if active, _ := api.getActiveSession("chat-1"); active.BotPlatform != "" || active.TriggeredBy != "" {
		t.Fatalf("owner's web turn kept the bot marks: %+v", active)
	}
	if _, bound := api.botExecutionForSession("chat-1"); bound {
		t.Fatal("owner's web turn kept the DM's Slack binding")
	}
	if policy := resolveWorkflowChatPolicy("chat-1", QueryRequest{}, mustActive(t, api, "chat-1"), false); policy.Origin != "interactive" {
		t.Fatalf("web turn origin = %q, want interactive", policy.Origin)
	}

	// Someone else, a scheduled turn, or a bot turn never clears them.
	for name, tc := range map[string]struct {
		claims *UserClaims
		req    QueryRequest
	}{
		"other user": {&UserClaims{UserID: "reader", Provider: "local"}, QueryRequest{}},
		"schedule":   {web, QueryRequest{TriggeredBy: "cron"}},
		"bot turn":   {&UserClaims{UserID: "owner", Provider: slackDMProvider}, QueryRequest{BotPlatform: "slack"}},
	} {
		kept := newAPI()
		kept.clearBotOriginForOwnerTurn("chat-1", tc.claims, tc.req)
		if active, _ := kept.getActiveSession("chat-1"); active.BotPlatform != "slack" {
			t.Fatalf("%s cleared the bot marks", name)
		}
	}
}

func mustActive(t *testing.T, api *StreamingAPI, id string) *ActiveSessionInfo {
	t.Helper()
	active, ok := api.getActiveSession(id)
	if !ok {
		t.Fatalf("no active session %s", id)
	}
	return active
}
