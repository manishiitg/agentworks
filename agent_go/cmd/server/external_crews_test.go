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

func TestOAuthGrantCrewAccessFollowsApprovedScopes(t *testing.T) {
	withCrews := mcpOAuthTokenForGrant(mcpOAuthGrant{UserID: "u", Scopes: []string{"workflows:read", "crews:run"}})
	if !withCrews.AllCrews || !withCrews.AllowsCrew("any") {
		t.Fatal("an OAuth grant approving a Crew permission must reach the user's Crews")
	}
	legacy := mcpOAuthTokenForGrant(mcpOAuthGrant{UserID: "u", Scopes: []string{"workflows:read", "files:read", "runs:execute"}})
	if legacy.AllCrews || legacy.AllowsCrew("any") {
		t.Fatal("an OAuth grant without Crew permissions must not reach Crews")
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

	bounded := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Name: "laptop", Scopes: []string{"crews:read", "crews:run"}, CrewIDs: []string{"beta"}}}
	if code, _ := chats(bounded, map[string]any{"crew_id": "alpha", "action": "list"}); code != 403 {
		t.Fatalf("Crew-bounded connection listed another Crew's chats: %d", code)
	}
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

// Turning off programmatic agent messaging preserves the declared function
// catalog, including a function named ask, and refuses conversational sends.
func TestCrewOwnerCanTurnOffFreeTextAsk(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	owner := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read", "crews:run"}, AllCrews: true}}
	names := func() []string {
		_, out := externalCrewRequest(t, env, owner, "list_crew_functions", map[string]any{"crew_id": "beta"})
		got := []string{}
		for _, fn := range out["functions"].([]any) {
			got = append(got, fn.(map[string]any)["name"].(string))
		}
		return got
	}
	if got := names(); len(got) != 0 {
		t.Fatalf("a Crew must not offer an implicit conversational function, got %v", got)
	}
	env.mock.mu.Lock()
	env.mock.files[linkBetaPath+"/functions.json"] = `{"version":1,"functions":[{"name":"ask","description":"A declared structured function"}]}`
	env.mock.files[linkBetaPath+"/workflow.json"] = `{"schema_version":1,"capabilities":{"free_text_ask":false}}`
	env.mock.mu.Unlock()
	if got := names(); len(got) != 1 || got[0] != "ask" {
		t.Fatalf("turning messaging off hid a declared function: %v", got)
	}
	code, out := externalCrewRequest(t, env, owner, "ask_crew", map[string]any{"crew_id": "beta", "message": "hello"})
	errBody, _ := out["error"].(map[string]any)
	if message, _ := errBody["message"].(string); code != 400 || !strings.Contains(message, "agent messaging is disabled") {
		t.Fatalf("ask_crew on a Crew with ask off = %d %v", code, out)
	}
}
