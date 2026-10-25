package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

func externalCrewRequest(t *testing.T, env triggerLinkEnv, claims *UserClaims, name string, args map[string]any) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/external/call", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
	rec := httptest.NewRecorder()
	env.api.externalCrewCall(rec, req, name, args)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestExternalCrewToolsRespectTokenCrewBounds(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	env.mock.mu.Lock()
	env.mock.files[linkBetaPath+"/notes/plan.md"] = "beta plan"
	env.mock.files[linkBetaPath+"/builder/conversation/2026-09-24/session.json"] = "{}"
	env.mock.mu.Unlock()

	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	folder := filepath.Join(docs, filepath.FromSlash(linkBetaPath), "notes")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "plan.md"), []byte("beta plan"), 0600); err != nil {
		t.Fatal(err)
	}
	binary := []byte{0, 1, 2, 255, 128, 3}
	if err := os.WriteFile(filepath.Join(folder, "report.pdf"), binary, 0600); err != nil {
		t.Fatal(err)
	}
	bounded := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"beta"}}}
	code, out := externalCrewRequest(t, env, bounded, "list_crews", map[string]any{})
	crews, _ := out["crews"].([]any)
	if code != 200 || len(crews) != 1 || crews[0].(map[string]any)["crew_id"] != "beta" {
		t.Fatalf("bounded list_crews = %d %v", code, out)
	}
	if code, _ := externalCrewRequest(t, env, bounded, "get_crew", map[string]any{"crew_id": "alpha"}); code != 404 {
		t.Fatalf("crew outside the token bound must be not-found, got %d", code)
	}
	code, out = externalCrewRequest(t, env, bounded, "get_crew", map[string]any{"crew_id": "beta"})
	if code != 200 || out["crew_id"] != "beta" {
		t.Fatalf("get_crew beta = %d %v", code, out)
	}
	for _, fn := range out["functions"].([]any) {
		if fn.(map[string]any)["name"] == "ask" {
			t.Fatalf("conversational ask is not a declared function: %v", out["functions"])
		}
	}
	code, out = externalCrewRequest(t, env, bounded, "read_crew_file", map[string]any{"crew_id": "beta", "path": "notes/plan.md"})
	if code != 200 || out["content"] != "beta plan" {
		t.Fatalf("read_crew_file = %d %v", code, out)
	}
	code, out = externalCrewRequest(t, env, bounded, "read_crew_file", map[string]any{"crew_id": "beta", "path": "notes/report.pdf"})
	if code != 200 || out["content_base64"] != base64.StdEncoding.EncodeToString(binary) {
		t.Fatalf("binary read = %d %v", code, out)
	}
	for _, refused := range []struct {
		path, errorCode string
		status          int
	}{
		{"builder/conversation/2026-09-24/session.json", "not_found", 404},
		{"product.json", "not_found", 404},
		{"../alpha/product.json", "invalid_arguments", 400},
	} {
		code, out := externalCrewRequest(t, env, bounded, "read_crew_file", map[string]any{"crew_id": "beta", "path": refused.path})
		errBody, _ := out["error"].(map[string]any)
		if code != refused.status || errBody["code"] != refused.errorCode || out["content"] != nil || out["content_base64"] != nil {
			t.Fatalf("refused path %q = %d %v", refused.path, code, out)
		}
	}

	all := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	if _, out := externalCrewRequest(t, env, all, "list_crews", map[string]any{}); len(out["crews"].([]any)) != 3 {
		t.Fatalf("all_crews token must see every accessible Crew (incl. other users'): %v", out)
	}
}

func TestExternalTokenScopeForCrewTools(t *testing.T) {
	tool := externalTool{Name: "list_crews"}
	if externalTokenAllows(&UserClaims{AccessToken: &accesstokens.Token{Scopes: []string{"workflows:read"}, AllWorkflows: true}}, tool) {
		t.Fatal("a workflow-only token must not reach Crew tools")
	}
	if !externalTokenAllows(&UserClaims{AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}, tool) {
		t.Fatal("crews:read must allow list_crews")
	}
}

// A send receipt is not a captured answer. Explicit replies are inbox messages,
// and both fresh external conversations and current token bounds stay separate.
func TestExternalAgentMessagesKeepOptionalRepliesAndCurrentBounds(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	turns := make(chan string, 4)
	agentMessageTurn = func(_ *StreamingAPI, _ context.Context, _ map[string]interface{}, sessionID, _ string) (internalSessionTurnResult, error) {
		turns <- sessionID
		return internalSessionTurnResult{FinalResponse: "This ordinary final text must not become an inbox reply."}, nil
	}
	t.Cleanup(func() { agentMessageTurn = nil })
	request := func(claims *UserClaims, args map[string]any) (int, map[string]any) {
		body, _ := json.Marshal(map[string]any{"name": "messages", "arguments": args})
		req := httptest.NewRequest("POST", "/api/external/v1/call", strings.NewReader(string(body)))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
		rec := httptest.NewRecorder()
		env.api.handleExternalCall(rec, req)
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	runner := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, CrewIDs: []string{"beta"}}}
	code, receipt := request(runner, map[string]any{"action": "send", "crew_id": "beta", "message": "Please review", "submission_id": "message-one"})
	inbox, _ := receipt["inbox_id"].(string)
	if code != 200 || inbox == "" || receipt["call_id"] != nil {
		t.Fatalf("send receipt = %d %v", code, receipt)
	}
	select {
	case <-turns:
	case <-time.After(5 * time.Second):
		t.Fatal("message never reached agent turn")
	}
	code, page := request(runner, map[string]any{"action": "read", "inbox_id": inbox})
	messages, _ := page["messages"].([]any)
	if code != 200 || len(messages) != 0 {
		t.Fatalf("ordinary final text was forwarded: %d %v", code, page)
	}
	code, next := request(runner, map[string]any{"action": "send", "crew_id": "beta", "message": "An independent conversation", "submission_id": "message-two"})
	if code != 200 || next["inbox_id"] == inbox {
		t.Fatalf("fresh callers share an inbox: %d %v", code, next)
	}
	select {
	case <-turns:
	case <-time.After(5 * time.Second):
		t.Fatal("second message never reached agent")
	}
	agentMessageMu.Lock()
	store, err := readAgentMessageStore(context.Background())
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var peer agentMessageEndpoint
	for _, conversation := range store.Conversations {
		if conversation.ID == inbox {
			peer = conversation.Endpoints[1]
		}
	}
	env.api.eventStore = storeevents.NewEventStore(50)
	env.api.eventStore.AddEvent(peer.Session, terminalRouteToolStartEvent(peer.Session, "exec", "message-tool", "execute_shell_command", `{"command":"ls -la && echo API_KEY=supersecretvalue123"}`, nil))
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: peer.Kind, ID: peer.ID, ProfileID: peer.Profile}, Label: peer.Label, Path: peer.Path, Chat: &codeChat{Key: peer.ChatKey, SessionID: peer.Session}}
	if _, err := env.api.sendAgentMessage(context.Background(), peer.UserID, caller, triggerTarget{}, "My explicit reply", inbox, "reply-one"); err != nil {
		t.Fatal(err)
	}
	code, page = request(runner, map[string]any{"action": "read", "inbox_id": inbox})
	messages, _ = page["messages"].([]any)
	if code != 200 || len(messages) != 1 || messages[0].(map[string]any)["message"] != "My explicit reply" {
		t.Fatalf("explicit reply = %d %v", code, page)
	}
	commands, _ := page["commands_run"].([]any)
	if len(commands) != 1 || strings.Contains(fmt.Sprint(commands), "supersecretvalue123") {
		t.Fatalf("owner command evidence missing or unmasked: %v", commands)
	}
	if code, empty := request(runner, map[string]any{"action": "read", "inbox_id": inbox, "after": page["next_cursor"]}); code != 200 || len(empty["messages"].([]any)) != 0 {
		t.Fatalf("cursor read = %d %v", code, empty)
	}
	bounded := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, CrewIDs: []string{"alpha"}}}
	other := &UserClaims{UserID: "other", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, AllCrews: true}}
	for _, claims := range []*UserClaims{bounded, other} {
		if code, _ := request(claims, map[string]any{"action": "read", "inbox_id": inbox}); code != 404 {
			t.Fatalf("inbox escaped current caller/token bound: %d", code)
		}
	}
	if code, _ := request(runner, map[string]any{"action": "read"}); code != 400 {
		t.Fatalf("missing inbox was accepted: %d", code)
	}
	readOnly := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"beta"}}}
	if code, _ := request(readOnly, map[string]any{"action": "send", "inbox_id": inbox, "message": "Not authorized"}); code != 404 {
		t.Fatalf("read-only inbox owner sent a message: %d", code)
	}
}

// manage_crew_chats lists only the caller's own app chats, and a Crew-bounded
// connection stays bounded. Agent messaging uses separate inboxes.
func TestExternalCrewChatsAreYourOwn(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	chats := func(claims *UserClaims, args map[string]any) (int, map[string]any) {
		req := httptest.NewRequest("POST", "/api/external/call", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
		rec := httptest.NewRecorder()
		env.api.externalCrewChatsCall(rec, req, args)
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	if code, out := chats(owner, map[string]any{"crew_id": "beta", "action": "list"}); code != 200 || out["chats"] == nil {
		t.Fatalf("list own chats = %d %v", code, out)
	}
	// The migrated main is listed by physical ID, with its role separate.
	mainRows := env.api.crewChats(httptest.NewRequest("GET", "/", nil), owner.UserID, "beta", linkBetaPath)
	mainChat, found := findCrewChat(mainRows, "main")
	if !found || !mainChat.IsMain || !strings.HasPrefix(mainChat.ChatID, "root-") || crewSideChatCount(mainRows) != 0 {
		t.Fatalf("migrated main discovery = %+v, found=%v", mainRows, found)
	}
	if code, out := chats(owner, map[string]any{"crew_id": "beta", "action": "set_model", "chat_id": "main", "engine": ""}); code != 200 {
		t.Fatalf("MCP main model alias = %d %v", code, out)
	}
	for _, action := range []string{"side_close", "side_rename"} {
		if code, _ := chats(owner, map[string]any{"crew_id": "beta", "action": action, "chat_id": mainChat.ChatID, "name": "Renamed"}); code != 404 {
			t.Fatalf("main physical ID allowed %s: %d", action, code)
		}
	}

	bounded := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Name: "laptop", Scopes: []string{"crews:read", "crews:run"}, CrewIDs: []string{"beta"}}}
	if code, _ := chats(bounded, map[string]any{"crew_id": "alpha", "action": "list"}); code != 403 {
		t.Fatalf("Crew-bounded connection listed another Crew's chats: %d", code)
	}
	// Every new tab is a named agent (owner, 2026-10-19): the server refuses an unnamed one, whoever opens it.
	if code, _ := chats(owner, map[string]any{"crew_id": "beta", "action": "side_open"}); code == 200 {
		t.Fatal("an unnamed tab was opened over MCP")
	}
	unnamed := httptest.NewRequest("POST", "/", nil)
	unnamed = unnamed.WithContext(context.WithValue(unnamed.Context(), UserContextKey, owner))
	if status, _ := scheduleHandler(unnamed, env.api.handleResolveAgentProfileConversation, "POST", "/api/agent-profiles/work/conversation", map[string]string{"id": "work"}, nil, AgentProfileConversationRequest{ConversationKey: "beta" + codeChatSideMarker + "unnamed1"}); status != 422 {
		t.Fatalf("the conversation route opened an unnamed tab: %d", status)
	}
	// A named empty tab must be discoverable before it has a transcript.
	code, opened := chats(owner, map[string]any{"crew_id": "beta", "action": "side_open", "name": "Research"})
	if code != 200 || opened["name_error"] != nil {
		t.Fatalf("named empty tab: %d %v", code, opened)
	}
	id, _ := opened["chat_id"].(string)
	if id == "" {
		t.Fatal("new side tab has no identity")
	}
	checkName := func(want string) externalCrewChat {
		t.Helper()
		req := httptest.NewRequest("GET", "/", nil)
		for _, row := range env.api.crewChats(req, owner.UserID, "beta", linkBetaPath) {
			if row.ChatID == id {
				if row.Name != want {
					t.Fatalf("side tab name = %q, want %q", row.Name, want)
				}
				return row
			}
		}
		t.Fatal("side tab missing from the server list")
		return externalCrewChat{}
	}
	checkName("Research")
	if code, out := chats(owner, map[string]any{"crew_id": "beta", "action": "side_rename", "chat_id": id, "name": "Analysis"}); code != 200 {
		t.Fatalf("rename: %d %v", code, out)
	}
	row := checkName("Analysis")
	// The website's ordinary rename uses the same current identity, while
	// keeping any purpose and other people's names untouched.
	req := httptest.NewRequest("PATCH", "/api/chat-history/sessions/"+row.SessionID, strings.NewReader(`{"title":"Review","workspace_path":"`+linkBetaPath+`"}`))
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, owner))
	req = mux.SetURLVars(req, map[string]string{"session_id": row.SessionID})
	recorder := httptest.NewRecorder()
	renameChatHistorySessionHandler(env.api)(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("website rename: %d %s", recorder.Code, recorder.Body.String())
	}
	checkName("Review")
}

// A Crew's owner reads its costs from the ledger (the app's Costs tab data);
// nobody else does, and a token bound to other Crews does not see it either.
func TestExternalCrewCostsAreOwnerOnly(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	server := costledger.NewTestServer(t)
	previous := costledger.DefaultLedger()
	ledger := costledger.NewLedger(server.URL)
	costledger.SetDefaultLedger(ledger)
	t.Cleanup(func() { costledger.SetDefaultLedger(previous) })
	if err := ledger.Append(costledger.Entry{EventID: "e1", IdempotencyKey: "e1", Timestamp: time.Now().UTC(), UserID: "owner", WorkflowID: linkAlphaPath, Scope: "chat",
		Provider: "claude-cli", ModelID: "sonnet", LLMCallCount: 2, PromptTokens: 1000, CompletionTokens: 200, TotalCostUSD: 1.5}); err != nil {
		t.Fatal(err)
	}

	owner := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	code, out := externalCrewRequest(t, env, owner, "get_crew_costs", map[string]any{"crew_id": "alpha"})
	total, _ := out["total"].(map[string]any)
	if code != 200 || total["total_cost_usd"] != 1.5 || out["by_model"] == nil || out["window"] == nil {
		t.Fatalf("owner's own Crew = %d %v", code, out)
	}
	code, out = externalCrewRequest(t, env, owner, "get_crew_costs", map[string]any{})
	rows, _ := out["crews"].([]any)
	if code != 200 || len(rows) == 0 || rows[0].(map[string]any)["crew_id"] != "alpha" || out["total_cost_usd"] != 1.5 {
		t.Fatalf("one row per owned Crew, dearest first = %d %v", code, out)
	}
	for _, row := range rows {
		if row.(map[string]any)["crew_id"] == "gamma" {
			t.Fatalf("another person's Crew is in the owner's cost list: %v", out)
		}
	}
	if code, _ := externalCrewRequest(t, env, owner, "get_crew_costs", map[string]any{"crew_id": "gamma"}); code != 403 {
		t.Fatalf("a Crew someone else owns must be refused, got %d", code)
	}
	bounded := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"beta"}}}
	if code, _ := externalCrewRequest(t, env, bounded, "get_crew_costs", map[string]any{"crew_id": "alpha"}); code != 404 {
		t.Fatalf("a Crew outside the token's bound must be not-found, got %d", code)
	}
}

// An account without the Crew product lists no Crews, whatever its token says (PLAT-820): a Code-only member
// listed every Crew with its owner's email and "write" access, while asking any of them was refused.
func TestExternalCrewListIsEmptyWithoutTheCrewProduct(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"owner","username":"owner","admin":true,"can_create":true,"products":[]},
		{"id":"codeonly","username":"codeonly","products":["code"]}
	]}`)
	token := &accesstokens.Token{Scopes: []string{"crews:read", "crews:run"}, AllCrews: true}

	code, out := externalCrewRequest(t, env, &UserClaims{UserID: "codeonly", Username: "codeonly", AccessToken: token}, "list_crews", map[string]any{})
	if crews, _ := out["crews"].([]any); code != 200 || len(crews) != 0 {
		t.Fatalf("a Code-only account must list no Crews, got %d %v", code, out)
	}
	_, out = externalCrewRequest(t, env, &UserClaims{UserID: "owner", Username: "owner", AccessToken: token}, "list_crews", map[string]any{})
	if crews, _ := out["crews"].([]any); len(crews) == 0 {
		t.Fatalf("an account with the Crew product must still list Crews: %v", out)
	}
}
