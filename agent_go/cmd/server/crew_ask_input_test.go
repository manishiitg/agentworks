package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestCrewAskInputIsPrivateTurnAndConnectionScoped(t *testing.T) {
	api := &StreamingAPI{eventStore: events.NewEventStore(100), sessionBusy: map[string]bool{"chat-session": true}, trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
		"query": {ExecutionID: "query", SessionID: "chat-session", Source: trackedExecutionSourceConversationTurn, Status: trackedExecutionStatusRunning, StartedAt: time.Now()},
	}}
	scope := &crewAskInputScope{User: "person", Connection: "family", Workspace: "work:project", Chat: "my-tab", Session: "chat-session", Query: "query"}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), crewAskInputKey{}, scope), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := api.waitForCodingAgentClarification(ctx, scope.Session, "codex-cli", clarificationTestArgs())
		done <- err
	}()
	p := pendingClarificationForTest(t, api, scope.Session)
	caller := func(user, family string) *UserClaims {
		return &UserClaims{UserID: user, AccessToken: &accesstokens.Token{ID: family}}
	}
	req := httptest.NewRequest("POST", "/api/external/v1/call", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, caller(scope.User, scope.Connection)))
	if got := api.externalCrewAskPending(req, scope.Workspace, scope.Chat, scope.Session, scope.Query); len(got) != 1 {
		t.Fatal("own question missing", got)
	}
	if len(api.eventStore.GetAllEventsRaw(scope.Session)) != 0 {
		t.Fatal("private Ask broadcast a shared question card")
	}
	answers := []codingAgentQuestionAnswerInput{{ID: "scope", SelectedLabels: []string{"Gmail"}}, {ID: "purpose", SelectedLabels: []string{"Read"}}}
	args := map[string]any{"request_id": p.promptID, "answers": answers}
	for _, tc := range []struct{ user, family, workspace, chat, session, query string }{
		{"colleague", "family", scope.Workspace, scope.Chat, scope.Session, scope.Query},
		{"person", "another-family", scope.Workspace, scope.Chat, scope.Session, scope.Query},
		{"person", "family", "work:other", scope.Chat, scope.Session, scope.Query},
		{"person", "family", scope.Workspace, "owner-tab", scope.Session, scope.Query},
		{"person", "family", scope.Workspace, scope.Chat, "other-session", scope.Query},
		{"person", "family", scope.Workspace, scope.Chat, scope.Session, "old-query"},
	} {
		other := req.WithContext(context.WithValue(req.Context(), UserContextKey, caller(tc.user, tc.family)))
		if got := api.externalCrewAskPending(other, tc.workspace, tc.chat, tc.session, tc.query); len(got) != 0 {
			t.Fatal("foreign question exposed", tc)
		}
		rec := httptest.NewRecorder()
		api.replyCrewAskInput(rec, other, args, tc.workspace, tc.chat, tc.session, tc.query)
		if rec.Code != 409 {
			t.Fatalf("foreign answer accepted: %+v %d %s", tc, rec.Code, rec.Body.String())
		}
	}
	if err := api.submitCodingAgentClarification(scope.Session, "codex-cli", p.promptID, false, answers); err == nil {
		t.Fatal("website family answered external prompt")
	}
	args["request_id"] = "old-question"
	rec := httptest.NewRecorder()
	api.replyCrewAskInput(rec, req, args, scope.Workspace, scope.Chat, scope.Session, scope.Query)
	if rec.Code != 409 {
		t.Fatal("stale question answered")
	}
	args["request_id"] = p.promptID
	api.trackedWorkflowExecutions["query"].Status = trackedExecutionStatusCompleted
	rec = httptest.NewRecorder()
	api.replyCrewAskInput(rec, req, args, scope.Workspace, scope.Chat, scope.Session, scope.Query)
	if rec.Code != 409 {
		t.Fatal("ended turn answered")
	}
	api.trackedWorkflowExecutions["query"].Status = trackedExecutionStatusRunning
	rec = httptest.NewRecorder()
	api.replyCrewAskInput(rec, req, args, scope.Workspace, scope.Chat, scope.Session, scope.Query)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	api.replyCrewAskInput(rec, req, args, scope.Workspace, scope.Chat, scope.Session, scope.Query)
	if rec.Code != 409 {
		t.Fatal("replay answered twice")
	}
	if len(api.eventStore.GetAllEventsRaw(scope.Session)) != 0 {
		t.Fatal("answer broadcast to shared chat")
	}
	for _, text := range []string{"Enter your password", "Choose an access token"} {
		secret := clarificationTestArgs()
		secret["questions"].([]map[string]interface{})[0]["question"] = text
		if err := validateCrewAskQuestions(secret); err == nil || !strings.Contains(err.Error(), "Vault from chat") {
			t.Fatal("secret form admitted", err)
		}
	}
}
