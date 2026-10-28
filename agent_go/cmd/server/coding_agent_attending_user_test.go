package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// Website tabs wait for the person on a coding agent's questions, whoever
// started the turn; schedules, webhooks and bots must never wait (owner,
// 2026-10-28; PLAT-354 for the unattended half). The server decides from the
// turn's origin: a message from another agent delivered into a tab was the
// gap (its turns logged attended=false, so no cards and no "Needs you").
func TestCodingAgentTurnAttended(t *testing.T) {
	tab := "6eaa17e1-9457-42fb-aa8e-d1772cb14334"
	cases := []struct {
		name      string
		claims    *UserClaims
		req       QueryRequest
		sessionID string
		want      bool
	}{
		{"person in a website tab", &UserClaims{UserID: "u"}, QueryRequest{AgentProfileID: crewProfileID}, tab, true},
		{"queued or steered follow-up", nil, QueryRequest{TriggeredBy: "interactive"}, tab, true},
		{"another agent's message delivered into a tab", &UserClaims{UserID: "u"}, QueryRequest{TriggeredBy: "agent_message", AgentMessageInbox: "inbox-1", IsAutoNotification: true}, tab, true},
		{"schedule delivered into a Crew chat", nil, QueryRequest{TriggeredBy: "cron"}, tab, false},
		{"schedule run now", nil, QueryRequest{TriggeredBy: "manual"}, tab, false},
		{"scheduled workflow session", nil, QueryRequest{KeepNativeSessionAlive: true}, "schedule-cron--aa8361a7_1", false},
		{"schedule made interactive", nil, QueryRequest{KeepNativeSessionAlive: true, UserInteractiveContinuation: true}, "schedule-cron--x", true},
		{"webhook", nil, QueryRequest{TriggeredBy: "webhook"}, tab, false},
		{"slack or whatsapp", nil, QueryRequest{BotPlatform: "slack", AgentMessageInbox: "inbox-1"}, tab, false},
		{"background completion", nil, QueryRequest{IsAutoNotification: true}, tab, false},
		{"child session", nil, QueryRequest{ParentSessionID: "parent"}, "child", false},
		{"MCP or API caller", &UserClaims{UserID: "u", AccessToken: &accesstokens.Token{}}, QueryRequest{AgentMessageInbox: "inbox-1"}, tab, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			if got := codingAgentTurnAttended(tc.claims, &req, tc.sessionID); got != tc.want {
				t.Fatalf("codingAgentTurnAttended() = %v, want %v", got, tc.want)
			}
		})
	}
}
