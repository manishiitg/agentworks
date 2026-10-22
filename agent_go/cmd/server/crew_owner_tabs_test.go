package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// Exercise the actual website/MCP boundaries with two owners and two retained
// conversations. No provider runs: the message seam records delivery identity.
func TestCrewOwnerTabsShareViewsKeepControlAndDeliverMessages(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	registry := newTestRegistry(t)
	previous := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() {
		defaultProjectOwnersOverride = previous
		crewOwners.mu.Lock()
		crewOwners.entries = map[string]crewOwnerEntry{}
		crewOwners.mu.Unlock()
	})
	const root = "Crew/owner-tabs"
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "owner-tabs", ProjectID: "beta", OwnerID: "owner", Shared: true, Private: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.AddCoOwner("work", "owner-tabs", "other"); err != nil {
		t.Fatal(err)
	}
	crewOwners.mu.Lock()
	crewOwners.entries = map[string]crewOwnerEntry{}
	crewOwners.mu.Unlock()
	env.mock.mu.Lock()
	env.mock.files[root+"/product.json"] = env.mock.files[linkBetaPath+"/product.json"]
	delete(env.mock.files, linkBetaPath+"/product.json")
	env.mock.mu.Unlock()
	profile, err := env.svc.registry.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	sessions := map[string]ProductConversationRecord{}
	for _, person := range []string{"owner", "other"} {
		ctx := internalBotRequestContext(t.Context(), person)
		if err := ensureCrewOwnerDefaultTab(ctx, person, profile, "beta"); err != nil {
			t.Fatalf("default %s: %v", person, err)
		}
		binding, _, err := resolveConversationBindingForUser(ctx, person, profile, "beta")
		if err != nil {
			t.Fatal(err)
		}
		record, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, person, profile, binding, "")
		if err != nil {
			t.Fatal(err)
		}
		sessions[person] = record
	}
	if sessions["owner"].SessionID == sessions["other"].SessionID || sessions["owner"].ConversationKey == sessions["other"].ConversationKey {
		t.Fatal("owners share a physical tab")
	}
	request := func(person, method, path, body string) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		return r.WithContext(internalBotRequestContext(r.Context(), person))
	}
	for _, person := range []string{"owner", "other"} {
		peer := "owner"
		if person == peer {
			peer = "other"
		}
		req := request(person, "GET", "/api/agent-profiles/work/side-chats?project_id=beta", "")
		req = mux.SetURLVars(req, map[string]string{"id": "work"})
		rec := httptest.NewRecorder()
		env.api.handleListAgentProfileSideChats(rec, req)
		var listed []AgentProfileSideChat
		_ = json.Unmarshal(rec.Body.Bytes(), &listed)
		if rec.Code != 200 || len(listed) != 1 || listed[0].TabOwnerID != peer || listed[0].CanWrite {
			t.Fatalf("%s views: %d %s", person, rec.Code, rec.Body.String())
		}
		// Opening another owner's tab returns its canonical conversation without
		// creating a shadow slot; typing into that same address is refused.
		req = request(person, "POST", "/api/agent-profiles/work/conversation", `{"conversation_key":"`+sessions[peer].ConversationKey+`"}`)
		req = mux.SetURLVars(req, map[string]string{"id": "work"})
		rec = httptest.NewRecorder()
		env.api.handleResolveAgentProfileConversation(rec, req)
		var opened AgentProfileConversationResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &opened)
		if rec.Code != 200 || opened.CanWrite || opened.SessionID != sessions[peer].SessionID {
			t.Fatalf("observe: %d %s", rec.Code, rec.Body.String())
		}
		req = request(person, "POST", "/api/agent-profiles/work/query", `{"conversation_key":"`+sessions[peer].ConversationKey+`","message":"typed takeover"}`)
		req = mux.SetURLVars(req, map[string]string{"id": "work"})
		rec = httptest.NewRecorder()
		env.api.handleAgentProfileChatQuery(rec, req)
		if rec.Code != 403 {
			t.Fatalf("foreign website write: %d %s", rec.Code, rec.Body.String())
		}
		if err := crewQueryTabWriteError(req.Context(), person, "work", root, sessions[peer].SessionID); err == nil {
			t.Fatal("raw query accepted another owner's unused session ID")
		}
		chats := env.api.crewChats(req, person, "beta", root)
		if len(chats) != 2 {
			t.Fatalf("MCP list %s: %+v", person, chats)
		}
		var foreign externalCrewChat
		for _, chat := range chats {
			if chat.OwnerID == peer {
				foreign = chat
			}
		}
		for _, action := range []string{"side_rename", "side_close", "set_model", "stop"} {
			code, out := externalCrewRequest(t, env, &UserClaims{UserID: person}, "manage_crew_chats", map[string]any{"crew_id": "beta", "action": action, "chat_id": foreign.ChatID, "name": "changed"})
			if code != 403 {
				t.Fatalf("MCP %s foreign write: %d %+v", action, code, out)
			}
		}
		project, err := env.api.codeChatsFor(req.Context(), person, sessions[person].SessionID, root)
		if err != nil || len(project.Chats) != 2 {
			t.Fatalf("cross-owner agents: %+v %v", project, err)
		}
		target, err := project.find(foreign.ChatID)
		if err != nil {
			t.Fatal(err)
		}
		if got := codeChatTarget(project, target, root).CrewOwner; got != peer {
			t.Fatalf("message recipient impersonated %s as %s", peer, got)
		}
	}
	env.api.activeSessions = map[string]*ActiveSessionInfo{}
	for person, record := range sessions {
		env.api.activeSessions[record.SessionID] = &ActiveSessionInfo{SessionID: record.SessionID, UserID: person, WorkspacePath: root, Status: "completed"}
	}
	for _, person := range []string{"owner", "other"} {
		peer := "owner"
		if person == peer {
			peer = "other"
		}
		req := request(person, "GET", "/api/terminals/main", "")
		if !env.api.canAccessTerminalSession(req, sessions[peer].SessionID) {
			t.Fatal("owner cannot observe peer terminal")
		}
		req.Method = "POST"
		if env.api.canAccessTerminalSession(req, sessions[peer].SessionID) || env.api.crewSessionCanControl(req, sessions[peer].SessionID) || env.api.canUseSessionIDForQuery(req, sessions[peer].SessionID) {
			t.Fatal("peer terminal/query control allowed")
		}
		if err := env.api.verifySessionAccess(req, sessions[peer].SessionID); err == nil {
			t.Fatal("peer stop allowed")
		}
	}
	// A restart has no active sessions. Durable ownership must still allow
	// observation and refuse query/input/stop on the other person's tab.
	env.api.eventStore = storeevents.NewEventStore(50)
	for person, record := range sessions {
		env.api.eventStore.SetSessionOwner(record.SessionID, person)
	}
	env.api.activeSessions = map[string]*ActiveSessionInfo{}
	restarted := request("owner", http.MethodGet, "/api/terminals/main", "")
	if !env.api.canAccessTerminalSession(restarted, sessions["other"].SessionID) {
		t.Fatal("restart lost shared terminal view")
	}
	restarted.Method = http.MethodPost
	if env.api.canAccessTerminalSession(restarted, sessions["other"].SessionID) || env.api.canUseSessionIDForQuery(restarted, sessions["other"].SessionID) {
		t.Fatal("restart allowed foreign terminal/query input")
	}
	if err := env.api.verifySessionAccess(restarted, sessions["other"].SessionID); err == nil {
		t.Fatal("restart allowed foreign stop")
	}
	binding, _, err := resolveConversationBindingForUser(restarted.Context(), "owner", profile, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resumeProductConversation(restarted.Context(), "owner", profile, binding, sessions["other"].SessionID); err == nil {
		t.Fatal("resumed another owner's tab into own slot")
	}
	delivered := make(chan string, 1)
	oldTurn := agentMessageTurn
	agentMessageTurn = func(_ *StreamingAPI, _ context.Context, req map[string]interface{}, session, user string) (internalSessionTurnResult, error) {
		if pin, _ := req["pin_run_mode"].(bool); pin {
			return internalSessionTurnResult{}, fmt.Errorf("owner tab was demoted to Run after restart")
		}
		delivered <- user + ":" + session
		return internalSessionTurnResult{}, nil
	}
	t.Cleanup(func() { agentMessageTurn = oldTurn })
	ctx := internalBotRequestContext(t.Context(), "owner")
	project, err := env.api.codeChatsFor(ctx, "owner", sessions["owner"].SessionID, root)
	if err != nil {
		t.Fatal(err)
	}
	target, err := project.find(strings.TrimPrefix(sessions["other"].ConversationKey, "beta:chat:"))
	if err != nil {
		t.Fatal(err)
	}
	base, err := crewTriggerLinkCaller(root)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.api.sendAgentMessage(ctx, "owner", codeChatCaller(project, base), codeChatTarget(project, target, root), "Please review this shared work.", "", "owner-tabs-message")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-delivered:
		if got != "other:"+sessions["other"].SessionID {
			t.Fatalf("wrong recipient %s", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cross-owner agent message did not reach recipient")
	}
	if _, err = registry.RemoveCoOwner("work", "owner-tabs", "other"); err != nil {
		t.Fatal(err)
	}
	if env.api.crewSessionTabView(request("other", http.MethodGet, "/api/terminals/main", ""), sessions["owner"].SessionID) {
		t.Fatal("removed co-owner still views owner tab")
	}
}
