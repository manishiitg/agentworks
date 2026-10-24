package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
)

// Real workspace HTTP and persisted registry/history, with only the paid model
// turn replaced. Pin the shared-tab decision across native chat, website and MCP.
func TestCrewSharedAgentsKeepExistingConversationAndAuthority(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("AUTH_SECRET", "shared-agent-fixture-signing-key")
	ctx := internalBotRequestContext(t.Context(), "owner")
	profile, err := f.api.agentProfiles.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	create := func(key, name, purpose string) ProductConversationRecord {
		t.Helper()
		binding, _, err := resolveConversationBindingForUser(ctx, "owner", profile, key)
		if err != nil {
			t.Fatal(err)
		}
		record, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, "owner", profile, binding, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = defaultProductConversationRegistryStore().setAgentIdentity(ctx, "owner", "work", key, &name, &purpose, "owner"); err != nil {
			t.Fatal(err)
		}
		return record
	}
	coach := create("team:chat:coach", "Coach", "Help the team prepare useful feedback.")
	private := create("team:chat:private", "Private planning", "Owner planning only.")
	reg := &recordingRegistrar{}
	owner := &UserClaims{UserID: "owner", Username: "Owner"}
	if err := f.api.registerDashboardTools(reg, "owner", externalCommandCrewRoot, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.tools["share_dashboard"].exec(ctx, map[string]any{"mode": "private", "shared_agents": []string{coach.ConversationKey}, "agent_purposes": map[string]string{coach.ConversationKey: "Review a situation and suggest practical next steps."}}); err != nil {
		t.Fatal(err)
	}
	if err := defaultProjectOwners().SetRunPeople("work", "test-team", []string{"other"}); err != nil {
		t.Fatal(err)
	}
	if got := crewSharedAgentKeys(externalCommandCrewRoot); len(got) != 1 || got[0] != coach.ConversationKey {
		t.Fatalf("native selection: %v", got)
	}
	reader := &UserClaims{UserID: "other", Username: "Colleague", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read", "crews:run", "dashboards:read"}, CrewIDs: []string{"team"}}}
	call := func(action string, extra map[string]any, website bool) *httptest.ResponseRecorder {
		t.Helper()
		args := map[string]any{"action": action, "workspace_id": "work:team"}
		for k, v := range extra {
			args[k] = v
		}
		w := httptest.NewRecorder()
		if website {
			body, _ := json.Marshal(args)
			req := httptest.NewRequest(http.MethodPost, "/api/crew-agents", strings.NewReader(string(body)))
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "other", Username: "Colleague"}))
			f.api.handleCrewSharedAgents(w, req)
		} else {
			body, _ := json.Marshal(map[string]any{"name": "workspace", "arguments": args})
			req := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", strings.NewReader(string(body)))
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey, reader))
			f.api.handleExternalCall(w, req)
		}
		return w
	}
	for _, web := range []bool{false, true} {
		w := call("chats", nil, web)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Coach") || !strings.Contains(w.Body.String(), "practical next steps") || strings.Contains(w.Body.String(), "Private planning") || strings.Contains(w.Body.String(), "provider_connections") {
			t.Fatalf("filtered list web=%t: %d %s", web, w.Code, w.Body.String())
		}
	}
	// Both surfaces read the same pre-existing selected transcript.
	f.write(t, externalCommandCrewRoot+"/builder/conversation/2026-10-24/session-"+coach.SessionID+"-conversation.json", `{"ui_events":[{"sequence":1,"type":"user_message","data":{"data":{"content":"Earlier shared context"}}},{"sequence":2,"type":"user_message","data":{"data":{"content":"internal envelope","metadata":{"source":"agent_message","sender_label":"Colleague","display_content":"Please review this"}}}}]}`)
	for _, web := range []bool{false, true} {
		w := call("read_chat", map[string]any{"chat_id": coach.ConversationKey}, web)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Earlier shared context") || !strings.Contains(w.Body.String(), `"sender":"Colleague"`) || strings.Contains(w.Body.String(), "internal envelope") {
			t.Fatalf("shared transcript web=%t: %d %s", web, w.Code, w.Body.String())
		}
		w = call("read_chat", map[string]any{"chat_id": private.ConversationKey}, web)
		if w.Code < 400 {
			t.Fatalf("unselected history web=%t: %s", web, w.Body.String())
		}
	}
	for _, action := range []string{"stop", "close_chat", "rename_chat", "set_chat_model"} {
		w := call(action, map[string]any{"chat_id": coach.ConversationKey, "name": "Takeover"}, false)
		if w.Code < 400 {
			t.Fatalf("shared recipient acquired %s: %s", action, w.Body.String())
		}
	}
	delivered := make(chan string, 2)
	old := agentMessageTurn
	agentMessageTurn = func(_ *StreamingAPI, ctx context.Context, req map[string]interface{}, session, user string) (internalSessionTurnResult, error) {
		if session != coach.SessionID || user != "owner" {
			return internalSessionTurnResult{}, fmt.Errorf("wrong existing tab: %s %s", session, user)
		}
		if restricted, _ := req["crew_ask_read_only"].(bool); restricted {
			return internalSessionTurnResult{}, fmt.Errorf("selected Builder agent demoted")
		}
		actor := costobserver.AttributionFromContext(ctx).ActorID
		if actor != "other" {
			return internalSessionTurnResult{}, fmt.Errorf("wrong caller cost attribution %q", actor)
		}
		delivered <- session
		return internalSessionTurnResult{}, nil
	}
	t.Cleanup(func() { agentMessageTurn = old })
	var receipt map[string]any
	for _, web := range []bool{true, false} {
		w := call("ask", map[string]any{"chat_id": coach.ConversationKey, "message": "Please help with this feedback.", "submission_id": fmt.Sprintf("shared-agent-message-%t", web)}, web)
		if w.Code != 200 {
			t.Fatalf("send web=%t: %d %s", web, w.Code, w.Body.String())
		}
		select {
		case <-delivered:
		case <-time.After(5 * time.Second):
			t.Fatal("message did not reach existing owner tab")
		}
		_ = json.Unmarshal(w.Body.Bytes(), &receipt)
	}
	var contact agentConversation
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		agentMessageMu.Lock()
		store, err := readAgentMessageStore(ctx)
		agentMessageMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range store.Conversations {
			if c.ID == receipt["inbox_id"] {
				contact = c
			}
		}
		if len(contact.Messages) > 0 && contact.Messages[0].Delivery == "delivered" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(contact.Messages) == 0 || contact.Endpoints[0].UserID != "other" || contact.Endpoints[1].Session != coach.SessionID {
		t.Fatalf("wrong attributed contact: %+v", contact)
	}
	// Removing the selected agent invalidates old reply addresses and pending delivery.
	if err := defaultProjectOwners().SetAudience("work", "test-team", true, []string{"other"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.api.recheckAgentMessageAdmission(ctx, contact, contact.Messages[0]); err == nil {
		t.Fatal("queued work retained revoked agent grant")
	}
	for _, action := range []string{"ask", "read_chat"} {
		if w := call(action, map[string]any{"chat_id": coach.ConversationKey, "message": "old contact"}, false); w.Code < 400 {
			t.Fatalf("revoked %s: %s", action, w.Body.String())
		}
	}
	if _, err := f.api.sendAgentMessage(internalBotRequestContext(ctx, "other"), "other", externalCrewCaller(&UserClaims{UserID: "other", Username: "Colleague"}), triggerTarget{}, "old inbox", contact.ID, "revoked-reply"); err == nil {
		t.Fatal("old inbox retained selected-tab authority")
	}
}
