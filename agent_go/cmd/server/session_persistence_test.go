package server

import (
	"testing"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

func TestSessionPersistenceClassForRequest(t *testing.T) {
	tests := []struct {
		name string
		req  QueryRequest
		want storeevents.SessionPersistenceClass
	}{
		{name: "direct chat", req: QueryRequest{AgentMode: "multi-agent"}, want: storeevents.SessionPersistenceInteractiveChat},
		{name: "Crew chat", req: QueryRequest{AgentMode: "multi-agent", AgentProfileID: "work"}, want: storeevents.SessionPersistenceInteractiveChat},
		{name: "product chat", req: QueryRequest{AgentMode: "multi-agent", AgentProfileID: "example-product"}, want: storeevents.SessionPersistenceInteractiveChat},
		{name: "Crew chat whose first turn is a trigger", req: QueryRequest{AgentMode: "multi-agent", AgentProfileID: "work", TriggeredBy: "webhook"}, want: storeevents.SessionPersistenceInteractiveChat},
		{name: "Crew chat whose first turn is a schedule", req: QueryRequest{AgentMode: "multi-agent", AgentProfileID: "work", TriggeredBy: "cron"}, want: storeevents.SessionPersistenceInteractiveChat},
		{name: "bot conversation", req: QueryRequest{AgentMode: "multi-agent", TriggeredBy: "bot:slack", BotPlatform: "slack"}, want: storeevents.SessionPersistenceInteractiveChat},
		{name: "workflow builder", req: QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "manual"}, want: storeevents.SessionPersistenceInteractiveChat},
		{name: "scheduled workflow", req: QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "cron"}, want: storeevents.SessionPersistenceExecution},
		{name: "queued workflow", req: QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "queued"}, want: storeevents.SessionPersistenceExecution},
		{name: "webhook workflow", req: QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "webhook"}, want: storeevents.SessionPersistenceExecution},
		{name: "headless workflow", req: QueryRequest{AgentMode: "workflow"}, want: storeevents.SessionPersistenceExecution},
		{name: "child session", req: QueryRequest{AgentMode: "multi-agent", ParentSessionID: "chat-1"}, want: storeevents.SessionPersistenceExecution},
		{name: "typed runtime session", req: QueryRequest{AgentMode: "workflow_phase", SessionKind: "pulse_reviewer"}, want: storeevents.SessionPersistenceExecution},
		{name: "legacy empty mode is direct chat", req: QueryRequest{}, want: storeevents.SessionPersistenceInteractiveChat},
		{name: "unknown mode", req: QueryRequest{AgentMode: "mystery"}, want: storeevents.SessionPersistenceEphemeral},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sessionPersistenceClassForRequest(test.req); got != test.want {
				t.Fatalf("class = %q, want %q", got, test.want)
			}
		})
	}
}
