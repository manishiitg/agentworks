package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/spf13/viper"
)

// Real document handlers, observer events, SQLite, issued grants and a
// Streamable HTTP MCP client. No model, CLI, or paid provider is launched.
func TestMCPCostsAttributionAndAccess(t *testing.T) {
	t.Setenv("AUTH_SECRET", "costs-test-signing-secret")
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks,relays,work,code,knowledgebase,mcp-gateway")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"Admin","role":"admin"},{"id":"owner","username":"Owner","role":"editor","products":["agentworks","code","work","relays"]},{"id":"member","username":"Member","role":"viewer","products":["agentworks","code","work","relays"]}]}`)
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	write := func(relative, content string) {
		t.Helper()
		p := filepath.Join(docs, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	const shared = "Workflow/shared"
	write(shared+"/workflow.json", `{"schema_version":1,"version":"1.0.0","id":"wf_shared","label":"Shared workflow","created_by":"owner","access":{"owners":["owner"],"readers":["member"]}}`)
	const privateCode = "_users/owner/Chats/Code/projects/private"
	write(privateCode+"/product.json", `{"schema_version":1,"product":"code","id":"private","owner_id":"owner","title":"Private Code"}`)
	router := gin.New()
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	api := &StreamingAPI{costLedger: ledger}
	ownerCtx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner"})
	// ask_crew's validated message sender stays the actor when the receiver
	// is the owner's turn. A guest label in client JSON is not that proof.
	askCtx := costAgentMessageContext(ownerCtx, agentMessageEndpoint{UserID: "member", Kind: triggerCallerUser})
	askPrincipal := queuedPrincipalFromContext(askCtx)
	askQueued, askErr := api.queuedConversationTurnContext(queuedConversationTurn{UserID: "owner", Principal: askPrincipal}, map[string]interface{}{})
	if askErr != nil {
		t.Fatal(askErr)
	}
	guestAsk := QueryRequest{AgentProfileID: "work", CrewGuestCaller: "member", CrewAskReadOnly: true, PinRunMode: true}
	if got := api.costTurnAttribution(askQueued, guestAsk, "owner", shared, "chat"); got.ActorID != "member" || got.Activity != "person_chat" {
		t.Fatal("owner-runtime Crew ask lost authenticated caller", got)
	}
	if got := api.costTurnAttribution(ownerCtx, guestAsk, "owner", shared, "chat"); got.ActorID != "owner" {
		t.Fatal("untrusted guest label reassigned costs", got)
	}
	fnCtx := costActivityContext(ownerCtx, "member", "function_call", "fn-public")
	// Native queued principal propagation preserves the caller while the turn
	// executes in the owner's namespace. Client JSON cannot supply it.
	principal := queuedPrincipalFromContext(fnCtx)
	b, _ := json.Marshal(principal)
	var saved queuedConversationPrincipal
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	queuedCtx, err := api.queuedConversationTurnContext(queuedConversationTurn{UserID: "owner", Principal: saved}, map[string]interface{}{})
	if err != nil || costobserver.AttributionFromContext(queuedCtx).ActorID != "member" {
		t.Fatal("queued function lost origin actor", err)
	}
	a := api.costTurnAttribution(queuedCtx, QueryRequest{}, "owner", shared, "schedule")
	if a.ActorID != "member" || a.ProductID != "wf_shared" || a.Activity != "function_call" {
		t.Fatal("admitted native attribution incorrect", a)
	}
	a.OriginDashboardID, a.OriginDashboardName = "dashboard-shared", "Shared dashboard"
	emit := func(session, user, root string, attr costledger.Attribution, input, output, cache int, usd float64, includes bool, hour int) {
		t.Helper()
		observer := newCostObserver(ledger, session, user, "multi-agent", withCostAttribution("chat", root, "", "query-"+session), withCostModel("openai", "test-model"), costobserver.WithProductAttribution(attr), withCostAccount("personal-account-canary"))
		err := observer.HandleEvent(t.Context(), &unifiedevents.AgentEvent{Type: unifiedevents.TokenUsage, Timestamp: time.Date(2026, 10, 13, hour, 0, 0, 0, time.UTC), SpanID: "span-" + session, Data: &unifiedevents.TokenUsageEvent{Provider: "openai", ModelID: "test-model", PromptTokens: input, CompletionTokens: output, GenerationInfo: map[string]interface{}{"cost_usd": usd, "cache_read_input_tokens": cache, "prompt_tokens_include_cache": includes}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	emit("function-session", "owner", shared, a, 100, 10, 20, 1, true, 1)
	ownerA := api.costTurnAttribution(ownerCtx, QueryRequest{}, "owner", shared, "chat")
	emit("private-owner-chat", "owner", shared, ownerA, 40, 5, 10, 0.2, false, 2)
	codeA := api.costTurnAttribution(ownerCtx, QueryRequest{}, "owner", privateCode, "chat")
	emit("private-code-chat", "owner", privateCode, codeA, 1000, 0, 0, 10, false, 3)
	emit("member-personal", "member", "", costledger.Attribution{AttributionVersion: 1, ActorID: "member", Activity: "person_chat"}, 7, 0, 0, 0.3, false, 4)
	emit("owner-personal", "owner", "", costledger.Attribution{AttributionVersion: 1, ActorID: "owner", Activity: "person_chat"}, 11, 0, 0, 0.3, false, 5)
	if err := ledger.Append(costledger.Entry{EventID: "legacy", Timestamp: time.Date(2026, 10, 13, 6, 0, 0, 0, time.UTC), UserID: "owner", WorkflowID: shared, Scope: "workflow_execution", Provider: "openai", ModelID: "test-model", LLMCallCount: 1, PromptTokens: 9, TotalCostUSD: 0.4, BillingBasis: "provider_actual", OperationMetadata: map[string]interface{}{"secret": "private-prompt-canary", "account_name": "private-account-name-canary"}}); err != nil {
		t.Fatal(err)
	}
	memberCtx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "member"})
	const ownCode = "_users/member/Chats/Code/projects/private"
	write(ownCode+"/product.json", `{"schema_version":1,"product":"code","id":"private","owner_id":"member","title":"Own Code"}`)
	ownA := api.costTurnAttribution(memberCtx, QueryRequest{}, "member", ownCode, "chat")
	emit("own-code-chat", "member", ownCode, ownA, 13, 0, 0, 0.1, false, 7)
	if err := os.Remove(filepath.Join(docs, filepath.FromSlash(ownCode), "product.json")); err != nil {
		t.Fatal(err)
	}
	var spoof QueryRequest
	_ = json.Unmarshal([]byte(`{"actor_id":"admin","product_id":"private","activity":"pulse","pulse_lifecycle_turn":true,"cost_source_id":"fn-fake"}`), &spoof)
	if got := api.costTurnAttribution(memberCtx, spoof, "member", shared, "pulse"); got.ActorID != "member" || got.Activity != "person_chat" || got.ProductID != "wf_shared" {
		t.Fatal("client JSON forged attribution", got)
	}
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	token, _, err := store.Issue(t.Context(), accesstokens.Token{UserID: "member", Username: "Member", Name: "costs-test", Scopes: []string{"workflows:read", "code:run", "llm:use"}, AllWorkflows: true, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: "member", Username: "Member", AccessToken: &token}
	srv := serveExternalMCP(t, api, claims)
	cli := dialExternalMCP(t, t.Context(), srv.URL+externalMCPPath)
	initializeExternalMCP(t, t.Context(), cli)
	remote := func(args map[string]any) map[string]any {
		t.Helper()
		args["from"], args["to"] = "2026-10-13", "2026-10-13"
		result := callRemoteTool(t, t.Context(), cli, externalMCPToolCall, map[string]any{"name": "costs", "arguments": args})
		requireRemoteSuccess(t, result, "costs")
		raw := marshalStructured(t, result)
		for _, canary := range []string{"personal-account-canary", "private-prompt-canary", "private-account-name-canary", "Private Code", "private-code-chat", "owner-personal", "private-owner-chat"} {
			if strings.Contains(raw, canary) {
				t.Fatalf("cost response leaked %s", canary)
			}
		}
		var response map[string]any
		if err := json.Unmarshal([]byte(raw), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	breakdown := remote(map[string]any{"action": "breakdown", "group_by": "product", "top": 1})
	total := breakdown["total"].(map[string]any)
	if total["input_tokens"] != float64(179) || math.Abs(total["cost_usd"].(float64)-2.0) > 1e-8 || len(breakdown["rows"].([]any)) != 1 || breakdown["omitted"].(map[string]any)["input_tokens"] != float64(20) {
		t.Fatal("visible total/top remainder incorrect", breakdown)
	}
	people := remote(map[string]any{"action": "breakdown", "group_by": "person"})
	for _, raw := range people["rows"].([]any) {
		row := raw.(map[string]any)
		if row["id"] == "member" && row["usage"].(map[string]any)["input_tokens"] != float64(120) {
			t.Fatal("runtime owner replaced original function caller", row)
		}
	}
	page := remote(map[string]any{"action": "detail", "product_id": "wf_shared", "limit": 1})
	if page["total"].(map[string]any)["input_tokens"] != float64(159) || page["next_cursor"] == nil {
		t.Fatal("shared detail total/pagination incorrect", page)
	}
	seen := map[string]bool{}
	summed := float64(0)
	for {
		for _, raw := range page["rows"].([]any) {
			row := raw.(map[string]any)
			id := row["id"].(string)
			if seen[id] {
				t.Fatal("duplicate detail execution", id)
			}
			seen[id] = true
			summed += row["usage"].(map[string]any)["input_tokens"].(float64)
		}
		cursor, _ := page["next_cursor"].(string)
		if cursor == "" {
			break
		}
		page = remote(map[string]any{"action": "detail", "product_id": "wf_shared", "limit": 1, "cursor": cursor})
	}
	if len(seen) != 3 || summed != 159 {
		t.Fatal("detail pages failed conservation", seen, summed)
	}
	origin := remote(map[string]any{"action": "detail", "origin_dashboard_id": "dashboard-shared"})
	if origin["total"].(map[string]any)["input_tokens"] != float64(100) || len(origin["rows"].([]any)) != 1 {
		t.Fatal("dashboard target counted more than once", origin)
	}
	// Sharing is checked on every query; the caller's own past function usage
	// cannot reopen a product after its sharing is revoked.
	write(shared+"/workflow.json", `{"schema_version":1,"version":"1.0.0","id":"wf_shared","label":"Shared workflow","created_by":"owner","access":{"owners":["owner"],"readers":[]}}`)
	revoked := remote(map[string]any{"action": "breakdown"})
	if revoked["total"].(map[string]any)["input_tokens"] != float64(20) {
		t.Fatal("revoked sharing retained product costs", revoked)
	}
	admin := &UserClaims{UserID: "admin"}
	all, err := api.queryCosts(t.Context(), admin, costsQuery{From: "2026-10-13", To: "2026-10-13"})
	if err != nil || all.Total.InputTokens != 1190 {
		t.Fatal("admin total lost private/unattributed rows", all, err)
	}
}
