package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestAgentMessageStateIsOwnerOnlyAndDeliveryRechecksAuthority(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	crewOwnerTabsFixture(t, env)
	t.Setenv("AUTH_SECRET", "message-admission-test-signing-secret")
	ctx := internalBotRequestContext(context.Background(), "owner")
	target, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "crew:Beta")
	if err != nil {
		t.Fatal(err)
	}
	peer := targetMessageEndpoint("owner", target)
	peer.Session = "message-session"
	peer.ChatKey = "messages:inbox-state"
	c := agentConversation{ID: "inbox-state", Endpoints: [2]agentMessageEndpoint{{UserID: "other", Kind: triggerCallerUser, ID: "other", External: true}, peer}, Messages: []agentMessage{{ID: "msg-state", Text: "private message body", Delivery: "undeliverable", Error: "receiving session was closed", SentAt: time.Now()}}}
	store := agentMessageStore{Conversations: []agentConversation{c}}
	agentMessageMu.Lock()
	err = saveAgentMessageStore(ctx, store)
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	request := func(user, crew string) (int, string) {
		body, _ := json.Marshal(map[string]any{"name": "messages", "arguments": map[string]any{"action": "state", "crew_id": "beta"}})
		claims := &UserClaims{UserID: user, Username: user, AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{crew}}}
		req := httptest.NewRequest("POST", "/api/external/v1/call", strings.NewReader(string(body)))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
		rec := httptest.NewRecorder()
		env.api.handleExternalCall(rec, req)
		return rec.Code, rec.Body.String()
	}
	status, body := request("owner", "beta")
	if status != 200 || !strings.Contains(body, "receiving session was closed") || strings.Contains(body, "private message body") {
		t.Fatalf("owner projection: %d %s", status, body)
	}
	for _, tc := range [][2]string{{"other", "beta"}, {"owner", "alpha"}} {
		if status, body := request(tc[0], tc[1]); status != 404 {
			t.Fatalf("non-owner/out-of-bound projection: %d %s", status, body)
		}
	}
	// The accepted queued submission loses authority before a receiving turn.
	// Use the current owner: another person's private Crew is not an initial
	// access grant, even when a token names its ID.
	c.Endpoints[0].UserID, c.Endpoints[0].ID = "owner", "owner"
	tokens, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer tokens.Close()
	token, _, err := tokens.Issue(ctx, accesstokens.Token{Name: "message-probe", UserID: "owner", Scopes: []string{"crews:run"}, CrewIDs: []string{"beta"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	msg := agentMessage{From: 0, SenderConnectionID: token.ID}
	if err := env.api.recheckAgentMessageAdmission(ctx, c, msg); err != nil {
		t.Fatalf("current connection refused: %v", err)
	}
	if err := tokens.Revoke(ctx, token.ID, "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.api.deliverAgentMessage(ctx, c, msg); err == nil || !strings.Contains(err.Error(), "connection") {
		t.Fatalf("revoked submission admitted: %v", err)
	}
	msg.SenderConnectionID = ""
	c.Endpoints[0].UserID, c.Endpoints[0].ID = "other", "other"
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"other","username":"other","disabled":true}]}`)
	if _, err := env.api.deliverAgentMessage(ctx, c, msg); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled participant admitted: %v", err)
	}
}

// #2881: cancellation of a registration lookup must remain retryable, not
// become a confirmed loss of admission. The corresponding live check is allowed.
func TestAgentAdmissionInterruptedLookupPreservesRetryableCause(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := internalBotRequestContext(t.Context(), "owner")
	agents := []namedMessageAgent{{ID: "sender", Name: "sender", UserID: "owner"}, {ID: "recipient", Name: "recipient", UserID: "owner"}}
	agentMessageMu.Lock()
	err := saveAgentMessageStore(ctx, agentMessageStore{Agents: agents})
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	c := agentConversation{ID: "inbox-fixture", Endpoints: [2]agentMessageEndpoint{
		{Kind: triggerCallerUser, ID: "owner", UserID: "owner", NamedAgentID: "sender", External: true},
		{Kind: triggerCallerUser, ID: "owner", UserID: "owner", NamedAgentID: "recipient", External: true},
	}}
	if err := env.api.recheckAgentMessageAdmission(ctx, c, agentMessage{From: 0}); err != nil {
		t.Fatal("valid current sender refused", err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	err = env.api.recheckAgentMessageAdmission(canceled, c, agentMessage{From: 0})
	var rule agentAdmissionRuleError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &rule) || !strings.Contains(rule.Rule, "destination.named_registration") {
		t.Fatal("lost failing rule or retryable cause", err)
	}
	if err := env.api.recheckAgentMessageAdmission(ctx, c, agentMessage{From: 0}); err != nil {
		t.Fatal("transient failure revoked sender", err)
	}
}
