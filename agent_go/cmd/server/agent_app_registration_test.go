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
	t.Setenv("AGENT_PRODUCTS", "bullpen")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "bullpen")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"other","username":"other","can_create":true}]}`)
	api := &StreamingAPI{}
	login := func(user, id string) *UserClaims {
		return &UserClaims{UserID: user, Username: user, RegisteredClaims: jwt.RegisteredClaims{ID: id, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	}
	first, second, stranger := login("owner", "first-login"), login("owner", "second-login"), login("other", "other-login")
	request := func(c *UserClaims, args map[string]any) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(args)
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
	register := func(c *UserClaims, device, conversation string) (int, map[string]any) {
		return request(c, map[string]any{"action": "register", "agent_name": "notes-agent", "device_id": device, "conversation_id": conversation, "label": "Notes", "purpose": "Keep notes"})
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
	// Same routing IDs under another account create a separate agent, never
	// supersede or grant access to this person's registered inbox.
	foreignStatus, foreignCopy := register(stranger, "device-one", "chat-one")
	if foreignStatus != 200 || foreignCopy["agent_id"] == agentID {
		t.Fatalf("cross-account identity was not isolated: %d", foreignStatus)
	}
	if _, _, err := currentAgentAttachment(ctx, stranger, "notes-agent", attachment); err == nil {
		t.Fatal("another account read the original attachment using identical routing IDs")
	}
	if _, _, err := currentAgentAttachment(ctx, first, "notes-agent", attachment); err != nil {
		t.Fatal("another account replaced the owner's attachment")
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
	// A second device has no Crew either: send and read through the same
	// named-agent inbox, then prove sign-out blocks the stored message.
	status, receiver := request(first, map[string]any{"action": "register", "agent_name": "receiving-agent", "device_id": "device-two", "conversation_id": "chat-two"})
	if status != 200 {
		t.Fatalf("second device: %d %v", status, receiver)
	}
	send := map[string]any{"action": "send", "agent_name": "notes-agent", "attachment_id": id, "target": "agent:receiving-agent", "message": "Keep this note", "submission_id": "login-only-message"}
	status, sent := request(second, send)
	if status != 200 || sent["inbox_id"] == "" {
		t.Fatalf("named send: %d %v", status, sent)
	}
	// A non-owner-triggered app turn cannot borrow this private same-account route.
	send["public_only"] = true
	if status, _ := request(second, send); status != 404 {
		t.Fatalf("non-owner turn reached owner's private agent: %d", status)
	}
	delete(send, "public_only")
	inboxArgs := map[string]any{"action": "inbox", "agent_name": "receiving-agent", "attachment_id": receiver["attachment_id"]}
	status, inbox := request(first, inboxArgs)
	if status != 200 || len(inbox["messages"].([]any)) != 1 {
		t.Fatalf("stored inbox: %d %v", status, inbox)
	}
	if status, _ := request(stranger, inboxArgs); status != 403 {
		t.Fatalf("another login read a private inbox: %d", status)
	}
	// A permanent app-side refusal is a failed receipt, not a retrying inbox row.
	failedSend := map[string]any{"action": "send", "agent_name": "notes-agent", "attachment_id": id, "target": "agent:receiving-agent", "message": "Refused example", "submission_id": "permanent-refusal"}
	status, failureSent := request(second, failedSend)
	if status != 200 {
		t.Fatal("failed-receipt fixture send refused")
	}
	failureID, _ := failureSent["message_id"].(string)
	if failureID == "" {
		t.Fatal("missing message id")
	}
	ack := map[string]any{"action": "ack", "agent_name": "receiving-agent", "attachment_id": receiver["attachment_id"], "message_id": failureID, "delivery": "failed"}
	if status, _ := request(stranger, ack); status == 200 {
		t.Fatal("other account acknowledged a failure")
	}
	if status, _ := request(first, ack); status != 200 {
		t.Fatalf("failed receipt status=%d", status)
	}
	if status, _ := request(first, ack); status != 200 {
		t.Fatal("failed receipt is not idempotent")
	}
	status, remaining := request(first, inboxArgs)
	if status != 200 || len(remaining["messages"].([]any)) != 1 {
		t.Fatal("failed message kept retrying or original pending message lost")
	}
	store, err := readAgentMessageStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundFailure := false
	for _, c := range store.Conversations {
		for _, m := range c.Messages {
			if m.ID == failureID {
				state, _ := receiptState(m, nil, true)
				if state != "not_delivered" || m.Delivery != "failed" || !m.DeliveredAt.IsZero() {
					t.Fatal("sender saw a successful or pending delivery")
				}
				foundFailure = true
			}
		}
	}
	if !foundFailure {
		t.Fatal("failure receipt not persisted")
	}
	invalid := map[string]any{"action": "send", "agent_name": "notes-agent", "attachment_id": id, "target": "workspace:work:unpaired", "message": "no"}
	if status, _ := request(second, invalid); status != 400 {
		t.Fatalf("app message path admitted a Crew operation: %d", status)
	}
	// Other logins need the existing public named-agent grant: a valid old
	// person-specific grant is not a substitute in this login-only route.
	status, foreign := request(stranger, map[string]any{"action": "register", "agent_name": "public-helper", "device_id": "other-device", "conversation_id": "other-chat"})
	if status != 200 {
		t.Fatalf("other registered chat: %d %v", status, foreign)
	}
	share := agentShare{ID: "app-public-share", OwnerUserID: "other", AgentID: foreign["agent_id"].(string), SubjectUserID: "owner", Capability: shareCapabilityChat, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), RevocationVersion: 1}
	setShare := func() {
		t.Helper()
		agentMessageMu.Lock()
		state, e := readAgentMessageStore(ctx)
		if e == nil {
			state.Shares = []agentShare{share}
			e = saveAgentMessageStore(ctx, state)
		}
		agentMessageMu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
	}
	setShare()
	state, e := readAgentMessageSnapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := state.authorizeShare(share.ID, 1, time.Now()); e != nil {
		t.Fatalf("private grant fixture is not genuinely authorized: %v", e)
	}
	publicSend := map[string]any{"action": "send", "agent_name": "notes-agent", "attachment_id": id, "target": "agent:other/public-helper", "message": "A public request", "submission_id": "public-app-message", "public_only": true}
	if status, _ := request(second, publicSend); status != 404 {
		t.Fatalf("private grant used as publication: %d", status)
	}
	share.SubjectUserID = shareScopeServer
	setShare()
	status, publicReceipt := request(second, publicSend)
	if status != 200 {
		t.Fatalf("published named agent refused: %d %v", status, publicReceipt)
	}
	status, reply := request(stranger, map[string]any{"action": "send", "agent_name": "public-helper", "attachment_id": foreign["attachment_id"], "inbox_id": publicReceipt["inbox_id"], "message": "The explicit reply", "submission_id": "public-app-reply"})
	if status != 200 || reply["inbox_id"] != publicReceipt["inbox_id"] {
		t.Fatalf("reply lost its original inbox: %d %v", status, reply)
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
	status, held := request(first, inboxArgs)
	if status != 200 || len(held["messages"].([]any)) != 0 || held["held"] != float64(1) {
		t.Fatalf("revoked sender message was released: %d %v", status, held)
	}
	restricted := *first
	restricted.Scope = "report-preview"
	if code, _ := register(&restricted, "device-one", "chat-one"); code != 403 {
		t.Fatalf("scoped token registration: %d", code)
	}
}
