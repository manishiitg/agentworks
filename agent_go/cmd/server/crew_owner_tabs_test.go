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
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

// Exercise the actual website/MCP boundaries with two owners and two retained
// conversations. No provider runs: the message seam records delivery identity.
func TestCrewOwnerNavigationBeforeFirstChat(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	crewOwnerTabsFixture(t, env, "other")
	list := func(navigation bool) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
		req = req.WithContext(internalBotRequestContext(req.Context(), "other"))
		out := httptest.NewRecorder()
		env.api.listExternalWorkspaces(out, req, map[string]any{"action": "list"}, navigation)
		if out.Code != 200 {
			t.Fatalf("list: %d %s", out.Code, out.Body.String())
		}
		return out.Body.String()
	}
	if !strings.Contains(list(true), `"workspace_id":"work:beta"`) {
		t.Fatal("co-owned Crew missing before first chat")
	}
	if _, err := defaultProjectOwners().RemoveCoOwner("work", "owner-tabs", "other"); err != nil {
		t.Fatal(err)
	}
	if err := defaultProjectOwners().SetPrivate("work", "owner-tabs", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(list(true), `"workspace_id":"work:beta"`) {
		t.Fatal("Run-only share leaked into Builder navigation")
	}
	if !strings.Contains(list(false), `"workspace_id":"work:beta"`) {
		t.Fatal("ordinary MCP discovery lost Run access")
	}
}

func TestCrewOwnersShareScheduleAndWatcherConfiguration(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	crewOwnerTabsFixture(t, env, "other")
	env.svc.users = func(string) []string { return []string{"owner", "other"} }
	ctx := internalBotRequestContext(t.Context(), "other")
	job, err := env.svc.CreateProjectSchedule(ctx, "other", "work", "beta", productschedule.Schedule{ID: "daily", Name: "Daily", Enabled: true, CronExpression: "0 8 * * *", Timezone: "UTC", Messages: []string{"review"}})
	if err != nil || job.UserID != "owner" {
		t.Fatalf("shared create: %+v %v", job, err)
	}
	if _, err = env.svc.SetEnabled(ctx, "other", job.ID(), false); err != nil {
		t.Fatal(err)
	}
	for _, person := range []string{"owner", "other"} {
		found, err := env.svc.Job(ctx, person, job.ID())
		if err != nil || found.UserID != "owner" || found.Effective().Enabled {
			t.Fatalf("shared override %s: %+v %v", person, found, err)
		}
	}
	if jobs, err := env.svc.JobsForUser(ctx, "other"); err != nil || len(jobs) != 0 {
		t.Fatalf("co-owner scheduled duplicate executions: %+v %v", jobs, err)
	}
	if jobs, err := env.svc.JobsForBuilder(ctx, "other"); err != nil || len(jobs) != 1 {
		t.Fatalf("Builder did not see schedule: %+v %v", jobs, err)
	}
	c := env.svc.conditionService()
	base, err := c.source(ctx, "owner", "work", "beta")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.saveCaller(ctx, base, productschedule.Schedule{ID: "watch", Name: "Watch", Enabled: true, CronExpression: "* * * * *", Timezone: "UTC", Condition: &conditiontrigger.Config{Script: "watch.py"}})
	if err != nil {
		t.Fatal(err)
	}
	view, err := c.manage(ctx, "other", "work", "beta", map[string]interface{}{"action": "list"})
	if err != nil || len(view.(map[string]interface{})["triggers"].([]interface{})) != 1 {
		t.Fatalf("shared Watchers: %+v %v", view, err)
	}
	if _, err := defaultProjectOwners().RemoveCoOwner("work", "owner-tabs", "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.SetEnabled(ctx, "other", job.ID(), true); err == nil {
		t.Fatal("removed co-owner changed the shared schedule")
	}
	if _, err := c.manage(ctx, "other", "work", "beta", map[string]interface{}{"action": "delete", "id": "watch"}); err == nil {
		t.Fatal("removed co-owner changed the shared Watcher")
	}
}

func TestCrewOwnerTabsShareViewsKeepControlAndDeliverMessages(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	root := crewOwnerTabsFixture(t, env, "other")
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
	// PLAT-750: dashboard-only Run chat remains stored and usable, but is not
	// advertised as a Builder tab to any owner of the Crew.
	viewContext := internalBotRequestContext(t.Context(), "owner")
	viewBinding, _, err := resolveConversationBindingForUser(viewContext, "owner", profile, "beta:chat:asuser-main")
	if err != nil {
		t.Fatal(err)
	}
	userView, err := defaultProductConversationRegistryStore().resolveOrCreate(viewContext, "owner", profile, viewBinding, "")
	if err != nil {
		t.Fatal(err)
	}
	request := func(person, method, path, body string) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		return r.WithContext(internalBotRequestContext(r.Context(), person))
	}
	// PLAT-825: grouping the tab model action must preserve both the chat
	// tool's read/run consent and its setter's write consent.
	for _, consent := range []struct {
		scopes []string
		status int
	}{
		{[]string{"crews:read", "crews:write"}, 200},
		{[]string{"crews:read"}, 403},
		{[]string{"crews:write"}, 403},
	} {
		claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: consent.scopes, CrewIDs: []string{"beta"}}}
		args := map[string]any{"workspace_id": "work:beta", "action": "set", "scope": "tab", "chat_id": sessions["owner"].SessionID, "engine": ""}
		if code, out := workspaceOwnerTabRequest(t, env, claims, "models", args); code != consent.status {
			t.Fatalf("tab model consent %v: %d %+v", consent.scopes, code, out)
		}
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
		currentView, foundView, _, viewErr := defaultProductConversationRegistryStore().history(viewContext, "owner", profile, viewBinding)
		if viewErr != nil || !foundView || currentView.SessionID != userView.SessionID {
			t.Fatalf("listing Builder tabs changed the dashboard conversation: %v", viewErr)
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
		if code, out := workspaceOwnerTabRequest(t, env, &UserClaims{UserID: person}, "workspace", map[string]any{"workspace_id": "work:beta", "action": "chats"}); code != 200 || len(out["chats"].([]any)) != 2 {
			t.Fatalf("public tab list: %d %+v", code, out)
		}
		if code, out := workspaceOwnerTabRequest(t, env, &UserClaims{UserID: person}, "workspace", map[string]any{"workspace_id": "work:beta", "action": "read_chat", "chat_id": foreign.ChatID}); code != 200 {
			t.Fatalf("public peer read: %d %+v", code, out)
		}
		if code, out := workspaceOwnerTabRequest(t, env, &UserClaims{UserID: person}, "workspace", map[string]any{"workspace_id": "work:beta", "action": "ask", "chat_id": foreign.ChatID, "message": "typed takeover"}); code != 403 {
			t.Fatalf("public peer ask: %d %+v", code, out)
		}
		for _, action := range []string{"side_rename", "side_close", "set_model", "stop"} {
			tool := "workspace"
			args := map[string]any{"workspace_id": "work:beta", "action": map[string]string{"side_rename": "rename_chat", "side_close": "close_chat", "stop": "stop"}[action], "chat_id": foreign.ChatID, "name": "changed"}
			if action == "set_model" {
				tool = "models"
				args = map[string]any{"workspace_id": "work:beta", "action": "set", "scope": "tab", "chat_id": foreign.ChatID, "engine": ""}
			}
			code, out := workspaceOwnerTabRequest(t, env, &UserClaims{UserID: person}, tool, args)
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
	// PLAT-856: a co-owner's registry kept a tab from before they were
	// added that holds the main owner's session, and the co-owner was that session's first
	// recorded writer. The main owner must keep their tab, and it must not show twice.
	otherDoc, err := defaultProductConversationRegistryStore().loadDocument(restarted.Context(), productConversationRegistryPath("other"))
	if err != nil {
		t.Fatal(err)
	}
	stale := sessions["owner"]
	stale.ConversationKey = "beta" + codeChatSideMarker + "root-stale"
	otherDoc.Entries[productConversationRegistryEntryKey(stale.ProfileID, stale.ConversationKey)] = stale
	if err := defaultProductConversationRegistryStore().writeDocument(restarted.Context(), productConversationRegistryPath("other"), otherDoc); err != nil {
		t.Fatal(err)
	}
	env.api.eventStore.SetSessionOwner(sessions["owner"].SessionID, "other")
	if !env.api.crewSessionCanControl(restarted, sessions["owner"].SessionID) || !env.api.canUseSessionIDForQuery(restarted, sessions["owner"].SessionID) {
		t.Fatal("main owner lost control of their own tab to a co-owner's older record")
	}
	if chats := env.api.crewChats(restarted, "owner", "beta", root); len(chats) != 2 {
		t.Fatalf("a shared session showed as a second tab: %+v", chats)
	}
	// Mirrored: the co-owner's own list holds that record too, but the Crew-wide tab owner decides.
	coOwner := request("other", http.MethodPost, "/api/terminals/main", "")
	if env.api.crewSessionCanControl(coOwner, sessions["owner"].SessionID) || env.api.canUseSessionIDForQuery(coOwner, sessions["owner"].SessionID) {
		t.Fatal("co-owner's older record took control of the main owner's tab")
	}
	if !env.api.crewSessionTabView(coOwner, sessions["owner"].SessionID) {
		t.Fatal("co-owner lost the view of the main owner's tab")
	}
	if chats := env.api.crewChats(coOwner, "other", "beta", root); len(chats) != 2 {
		t.Fatalf("co-owner sees the stale record as a tab: %+v", chats)
	}
	delete(otherDoc.Entries, productConversationRegistryEntryKey(stale.ProfileID, stale.ConversationKey))
	if err := defaultProductConversationRegistryStore().writeDocument(restarted.Context(), productConversationRegistryPath("other"), otherDoc); err != nil {
		t.Fatal(err)
	}
	env.api.eventStore.SetSessionOwner(sessions["owner"].SessionID, "owner")
	binding, _, err := resolveConversationBindingForUser(restarted.Context(), "owner", profile, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resumeProductConversation(restarted.Context(), "owner", profile, binding, sessions["other"].SessionID); err == nil {
		t.Fatal("resumed another owner's tab into own slot")
	}
	readClaims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"beta"}}}
	peer := agentMessageEndpoint{Kind: triggerCallerCrew, ID: "beta", Profile: "work", UserID: "other", Path: root, Session: sessions["other"].SessionID, ChatKey: sessions["other"].ConversationKey}
	if e := env.api.externalAuthorizeMessagePeer(internalBotRequestContext(t.Context(), "owner"), readClaims, peer, true); e == nil {
		t.Fatal("read connection gained existing Builder send authority")
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
	receipt, err := env.api.sendAgentMessage(ctx, "owner", codeChatCaller(project, base), codeChatTarget(project, target, root), "Please review this shared work.", "", "owner-tabs-message")
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
	if _, err = defaultProjectOwners().RemoveCoOwner("work", "owner-tabs", "other"); err != nil {
		t.Fatal(err)
	}
	removed := env.api.agentMessageCallerRole(agentMessageEndpoint{Kind: triggerCallerCrew, ID: "beta", Profile: "work", UserID: "other", Path: root, Session: sessions["other"].SessionID, ChatKey: sessions["other"].ConversationKey})
	if removed.Mode == "builder" {
		t.Fatal("removed owner retained inferred Builder role")
	}
	stored, e := readAgentMessageStore(ctx)
	if e != nil {
		t.Fatal(e)
	}
	checked := false
	for _, conversation := range stored.Conversations {
		if conversation.ID == receipt["inbox_id"] {
			checked = true
			message := conversation.Messages[len(conversation.Messages)-1]
			if e := env.api.recheckAgentMessageAdmission(ctx, conversation, message); e == nil {
				t.Fatal("queued message still admitted to removed co-owner")
			}
		}
	}
	if !checked {
		t.Fatal("message receipt was not stored")
	}
	if env.api.crewSessionTabView(request("other", http.MethodGet, "/api/terminals/main", ""), sessions["owner"].SessionID) {
		t.Fatal("removed co-owner still views owner tab")
	}
}

func crewOwnerTabsFixture(t *testing.T, env triggerLinkEnv, coOwners ...string) string {
	t.Helper()
	registry := newTestRegistry(t)
	previous := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	clear := func() { crewOwners.mu.Lock(); crewOwners.entries = map[string]crewOwnerEntry{}; crewOwners.mu.Unlock() }
	t.Cleanup(func() { defaultProjectOwnersOverride = previous; clear() })
	const root = "Crew/owner-tabs"
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "owner-tabs", ProjectID: "beta", OwnerID: "owner", Shared: true, Private: true}); err != nil {
		t.Fatal(err)
	}
	for _, person := range coOwners {
		if _, err := registry.AddCoOwner("work", "owner-tabs", person); err != nil {
			t.Fatal(err)
		}
	}
	clear()
	env.mock.mu.Lock()
	env.mock.files[root+"/product.json"] = env.mock.files[linkBetaPath+"/product.json"]
	delete(env.mock.files, linkBetaPath+"/product.json")
	env.mock.mu.Unlock()
	return root
}

func workspaceOwnerTabRequest(t *testing.T, env triggerLinkEnv, claims *UserClaims, name string, args map[string]any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	req := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", strings.NewReader(string(body)))
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
	rec := httptest.NewRecorder()
	env.api.handleExternalCall(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}
