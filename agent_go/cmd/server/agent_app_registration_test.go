package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// PLAT-746-890: an app chat must register privately without creating or pairing
// a Crew, and a reconnect must not take over a different recipient.
func TestAppAgentRegistrationNeedsOnlyCurrentLoginAndExactChat(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","can_create":true},{"id":"other","can_create":true}]}`)
	api := &StreamingAPI{}
	login := func(user, id string) *UserClaims {
		return &UserClaims{UserID: user, Username: user, RegisteredClaims: jwt.RegisteredClaims{ID: id, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	}
	first, second, stranger := login("owner", "first-login"), login("owner", "second-login"), login("other", "other-login")
	register := func(c *UserClaims, device, conversation string) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"action": "register", "agent_name": "notes-agent", "device_id": device, "conversation_id": conversation, "label": "Notes", "purpose": "Keep notes"})
		r := httptest.NewRequest(http.MethodPost, "/api/external/v1/agents/app", bytes.NewReader(raw))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, c))
		w := httptest.NewRecorder()
		api.handleAppAgentRegistration(w, r)
		out := map[string]any{}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return w.Code, out
	}
	code, row := register(first, "device-one", "chat-one")
	if code != 200 {
		t.Fatalf("registration without a Crew: %d %v", code, row)
	}
	agentID, attachment := row["agent_id"].(string), row["attachment_id"].(string)
	ctx := t.Context()
	if _, _, err := currentAgentAttachment(ctx, first, "notes-agent", attachment); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveNamedMessageAgent(ctx, second.UserID, "notes-agent", ""); err != nil {
		t.Fatalf("same login account cannot discover its chat: %v", err)
	}
	if _, err := resolveNamedMessageAgent(ctx, stranger.UserID, "notes-agent", ""); err == nil {
		t.Fatal("another account discovered private agent")
	}
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows := namedMessageAgentDirectory(ctx, first.UserID, store, false)["agents"].([]map[string]any)
	found := false
	for _, r := range rows {
		if r["agent_id"] == agentID {
			found = true
			if r["published"] != false || r["yours"] != true {
				t.Fatalf("private registration projection: %v", r)
			}
		}
	}
	if !found {
		t.Fatal("new app agent absent from existing directory")
	}
	for _, recipient := range [][2]string{{"device-two", "chat-one"}, {"device-one", "chat-two"}} {
		if code, _ := register(first, recipient[0], recipient[1]); code != 409 {
			t.Fatalf("different recipient took name: %d", code)
		}
	}
	code, renewed := register(second, "device-one", "chat-one")
	if code != 200 || renewed["agent_id"] != agentID || renewed["attachment_id"] == attachment {
		t.Fatalf("login renewal: %d %v", code, renewed)
	}
	if _, _, err := currentAgentAttachment(ctx, first, "notes-agent", attachment); err == nil {
		t.Fatal("old login attachment still receives")
	}
	id := renewed["attachment_id"].(string)
	if _, _, err := currentAgentAttachment(ctx, second, "notes-agent", id); err != nil {
		t.Fatal(err)
	}
	revokedSessions.Lock()
	revokedSessions.until[second.ID] = time.Now().Add(time.Hour)
	revokedSessions.Unlock()
	t.Cleanup(func() { revokedSessions.Lock(); delete(revokedSessions.until, second.ID); revokedSessions.Unlock() })
	if _, _, err := currentAgentAttachment(ctx, second, "notes-agent", id); err == nil {
		t.Fatal("signed-out login still receives")
	}
	if code, _ := register(second, "device-one", "chat-one"); code != 403 {
		t.Fatalf("signed-out registration: %d", code)
	}
	restricted := *first
	restricted.Scope = "report-preview"
	if code, _ := register(&restricted, "device-one", "chat-one"); code != 403 {
		t.Fatalf("scoped token registration: %d", code)
	}
}
