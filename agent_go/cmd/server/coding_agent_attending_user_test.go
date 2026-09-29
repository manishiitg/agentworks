package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// PLAT-354: only a chat a person is attending may make a native CLI question
// wait for an answer. A scheduled run keeps its native session alive, but a
// question there must be auto-answered, or the run waits forever.
func TestCodingAgentRequestHasAttendingUser(t *testing.T) {
	cases := []struct {
		name      string
		req       QueryRequest
		sessionID string
		want      bool
	}{
		{"interactive chat", QueryRequest{}, "6eaa17e1-9457-42fb-aa8e-d1772cb14334", true},
		{"scheduler keeps native session alive", QueryRequest{KeepNativeSessionAlive: true, TriggeredBy: "cron"}, "schedule-cron--aa8361a7_1", false},
		{"keep-alive with ordinary id", QueryRequest{KeepNativeSessionAlive: true}, "6eaa17e1-x", false},
		{"cron trigger", QueryRequest{TriggeredBy: "cron"}, "abc", false},
		{"webhook trigger", QueryRequest{TriggeredBy: "webhook"}, "abc", false},
		{"schedule made interactive", QueryRequest{KeepNativeSessionAlive: true, UserInteractiveContinuation: true}, "schedule-cron--x", true},
		{"slack bot", QueryRequest{BotPlatform: "slack"}, "abc", false},
		{"bot trigger", QueryRequest{TriggeredBy: "bot:whatsapp"}, "abc", false},
		{"child session", QueryRequest{ParentSessionID: "parent"}, "child", false},
		{"typed runtime stage", QueryRequest{SessionKind: "pulse_reviewer"}, "abc", false},
		{"auto notification turn", QueryRequest{IsAutoNotification: true}, "abc", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			if got := codingAgentRequestHasAttendingUser(&req, tc.sessionID); got != tc.want {
				t.Fatalf("codingAgentRequestHasAttendingUser() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Only the chat composer's header opts in. Internal callers (bots, Crew
// calls, webhooks, queued turns built from them) and API clients build their
// own requests without it, so they auto-answer even if the body looks like a
// plain chat.
func TestRequestFromAttendedChatIsAllowListed(t *testing.T) {
	plain := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	if requestFromAttendedChat(plain) || requestFromAttendedChat(nil) {
		t.Fatal("a request without the composer header must auto-answer")
	}
	composer := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	composer.Header.Set(attendedChatHeader, "1")
	if !requestFromAttendedChat(composer) {
		t.Fatal("the composer header must opt in")
	}
	composer.Header.Set(attendedChatHeader, "true")
	if requestFromAttendedChat(composer) {
		t.Fatal("only the exact value opts in")
	}
}
