package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestAgentMessageStateIsOwnerOnlyAndDeliveryRechecksAuthority(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
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
	store := agentMessageStore{Conversations: []agentConversation{c}, Wakeups: []agentWakeup{{ID: "wake-state", Endpoint: peer, Status: "scheduled", Due: time.Now().Add(time.Hour), Text: "private wakeup text"}}}
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
	if status != 200 || !strings.Contains(body, "receiving session was closed") || !strings.Contains(body, "wake-state") || strings.Contains(body, "private message body") || strings.Contains(body, "private wakeup text") {
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
