package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

const codeReviewDirectory = `{"users":[
	{"id":"owner","username":"owner","can_create":true},
	{"id":"other","username":"other","can_create":true},
	{"id":"boss","username":"boss","admin":true,"can_create":true},
	{"id":"rev","username":"rev","role":"viewer","code_reviewer":true}]}`

func codeReviewCall(api *StreamingAPI, claims *UserClaims, name string, args map[string]any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	w := httptest.NewRecorder()
	api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), claims, nil))
	return w
}

func codeReviewToken(user string, scopes ...string) *UserClaims {
	return &UserClaims{UserID: user, Username: user, AccessToken: &accesstokens.Token{ID: "tok-" + user, Scopes: scopes}}
}

// The Code review tools are read-only, audited like the web inspector (token
// named), and follow the account on every call: a reviewer's token works, the
// same token stops the moment the flag is removed, and a token that merely
// holds the scope string without a reviewer or admin behind it gets 403.
func TestCodeReviewToolsFollowTheAccountOnEveryCall(t *testing.T) {
	api, mock := newCodeAdminFixture(t, true)
	withMemoryUserDirectory(t, codeReviewDirectory)
	project := map[string]any{"owner_id": "owner", "project_id": "c0de0001-0000"}

	reviewer := codeReviewToken("rev", "code:review")
	if w := codeReviewCall(api, reviewer, "list_code_workspaces", map[string]any{}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"owner_id":"owner"`) {
		t.Fatalf("reviewer token listing = %d %s", w.Code, w.Body.String())
	}
	if w := codeReviewCall(api, reviewer, "read_code_file", map[string]any{"owner_id": "owner", "project_id": "c0de0001-0000", "path": "code/main.go"}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "package main") {
		t.Fatalf("reviewer token file = %d %s", w.Code, w.Body.String())
	}
	// The Code's HOME (git and CLI logins) stays hidden over MCP too.
	if w := codeReviewCall(api, reviewer, "read_code_file", map[string]any{"owner_id": "owner", "project_id": "c0de0001-0000", "path": ".sandbox-cache/home/.git-credentials"}); w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "token@") {
		t.Fatalf("reviewer token read credentials = %d %s", w.Code, w.Body.String())
	}
	if w := codeReviewCall(api, reviewer, "list_code_chats", project); w.Code != http.StatusOK {
		t.Fatalf("reviewer token chats = %d %s", w.Code, w.Body.String())
	}
	mock.mu.Lock()
	log := mock.files[codeAdminAuditPath(time.Now())]
	mock.mu.Unlock()
	for _, want := range []string{`"action":"list_workspaces"`, `"action":"list_chats"`, `"target":"code/main.go"`, `"role":"reviewer"`, `"via":"token:tok-rev"`} {
		if !strings.Contains(log, want) {
			t.Fatalf("MCP review not audited (%s):\n%s", want, log)
		}
	}

	// Removing the flag ends the existing token's access at once.
	withMemoryUserDirectory(t, strings.Replace(codeReviewDirectory, `"code_reviewer":true`, `"code_reviewer":false`, 1))
	if w := codeReviewCall(api, reviewer, "list_code_workspaces", map[string]any{}); w.Code != http.StatusForbidden {
		t.Fatalf("revoked reviewer's token still works: %d %s", w.Code, w.Body.String())
	}
	withMemoryUserDirectory(t, codeReviewDirectory)

	// The scope string alone is nothing; nor is a reviewer's token without it.
	for name, claims := range map[string]*UserClaims{
		"member with code:review":  codeReviewToken("other", "code:review"),
		"owner with code:review":   codeReviewToken("owner", "code:review"),
		"reviewer without scope":   codeReviewToken("rev", "workflows:read"),
		"bot route":                {UserID: "bot", Username: "bot", Provider: "bot_route", BotRouteProfileID: "code"},
		"member without any token": {UserID: "other", Username: "other"},
	} {
		w := codeReviewCall(api, claims, "list_code_workspaces", map[string]any{})
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s reached Code review: %d %s", name, w.Code, w.Body.String())
		}
		tools := httptest.NewRecorder()
		api.handleExternalTools(tools, adminRequest(http.MethodGet, "/api/external/tools", "", claims, nil))
		if strings.Contains(tools.Body.String(), "list_code_workspaces") {
			t.Fatalf("%s sees the Code review tools", name)
		}
	}
	if w := codeReviewCall(api, codeReviewToken("boss", "code:review"), "get_code_audit", map[string]any{}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "tok-rev") {
		t.Fatalf("admin audit read = %d %s", w.Code, w.Body.String())
	}
}

// Every Code review tool only reads.
func TestCodeReviewToolsAreReadOnly(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, tool := range catalog {
		if !isExternalCodeReviewTool(tool.Name) {
			continue
		}
		found++
		if tool.mutates || tool.executes || tool.plan {
			t.Fatalf("%s is not read-only", tool.Name)
		}
	}
	if found != len(externalCodeReviewTools) {
		t.Fatalf("catalog has %d of %d Code review tools", found, len(externalCodeReviewTools))
	}
}

// code:review needs no workflow or Crew bound; only admins and reviewers can
// mint it, and only they see or grant it over OAuth.
func TestCodeReviewScopeIsForReviewersOnly(t *testing.T) {
	now := time.Now()
	if err := accesstokens.Validate(accesstokens.Token{Name: "review", UserID: "rev", Scopes: []string{"code:review"}, ExpiresAt: now.Add(24 * time.Hour)}, now); err != nil {
		t.Fatalf("a review-only token was refused: %v", err)
	}
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, codeReviewDirectory)
	api := tokenTestSetup(t)
	mint := func(user string) int {
		w := httptest.NewRecorder()
		api.handleAccessTokens(w, adminRequest(http.MethodPost, "/api/auth/access-tokens", `{"name":"review","scopes":["code:review"],"expires_in_days":7}`, &UserClaims{UserID: user, Username: user}, nil))
		return w.Code
	}
	if code := mint("other"); code != http.StatusForbidden {
		t.Fatalf("a member minted code:review: %d", code)
	}
	if code := mint("rev"); code != http.StatusCreated {
		t.Fatalf("a reviewer could not mint code:review: %d", code)
	}
	all := []string{"workflows:read", "code:review"}
	if got := mcpOAuthScopesFor(&UserClaims{UserID: "other", Username: "other"}, all); len(got) != 1 || got[0] != "workflows:read" {
		t.Fatalf("a member was offered code:review over OAuth: %v", got)
	}
	if got := mcpOAuthScopesFor(&UserClaims{UserID: "rev", Username: "rev"}, all); len(got) != 2 {
		t.Fatalf("a reviewer lost code:review over OAuth: %v", got)
	}
}

// Only an admin sets the reviewer flag: a reviewer cannot grant it, not even
// to themself.
func TestOnlyAnAdminMakesCodeReviewers(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, codeReviewDirectory)
	api := &StreamingAPI{}
	for _, caller := range []string{"rev", "other"} {
		for _, target := range []string{"rev", "other"} {
			w := httptest.NewRecorder()
			requireAdmin(api.handleAdminUpdateUser)(w, adminRequest(http.MethodPut, "/api/admin/users/"+target, `{"code_reviewer":true}`, &UserClaims{UserID: caller, Username: caller}, map[string]string{"id": target}))
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s set code_reviewer on %s: %d", caller, target, w.Code)
			}
		}
	}
	w := httptest.NewRecorder()
	requireAdmin(api.handleAdminUpdateUser)(w, adminRequest(http.MethodPut, "/api/admin/users/other", `{"code_reviewer":true}`, &UserClaims{UserID: "boss", Username: "boss"}, map[string]string{"id": "other"}))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code_reviewer":true`) {
		t.Fatalf("admin could not make a reviewer: %d %s", w.Code, w.Body.String())
	}
}

// get_code_costs is every Code's cost split by person, and nothing else:
// other products and workflows are neither listed nor in the totals.
func TestCodeReviewCostsAreCodeOnly(t *testing.T) {
	api, mock := newCodeAdminFixture(t, true)
	withMemoryUserDirectory(t, codeReviewDirectory)
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	api.costLedger = ledger
	ts := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	for _, entry := range []costledger.Entry{
		{EventID: "c1", Timestamp: ts, WorkflowID: "_users/owner/Chats/Code/projects/c0de0001-0000", UserID: "owner", Scope: "chat", LLMCallCount: 1, BillingBasis: "subscription_shadow", TotalCostUSD: 2},
		{EventID: "c2", Timestamp: ts, WorkflowID: "_users/owner/Chats/Code/projects/c0de0001-0000/code", UserID: "other", Scope: "chat", LLMCallCount: 1, BillingBasis: "subscription_shadow", TotalCostUSD: 3},
		{EventID: "v1", Timestamp: ts, WorkflowID: "_users/owner/Chats/Video Studio/projects/launch", UserID: "owner", Scope: "chat", LLMCallCount: 1, BillingBasis: "subscription_shadow", TotalCostUSD: 40},
		{EventID: "w1", Timestamp: ts, WorkflowID: "Workflow/sales", UserID: "owner", Scope: "workflow_execution", LLMCallCount: 1, BillingBasis: "subscription_shadow", TotalCostUSD: 50},
	} {
		if err := ledger.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	w := codeReviewCall(api, codeReviewToken("rev", "code:review"), "get_code_costs", map[string]any{"from": "2026-09-01", "to": "2026-09-30"})
	var got struct {
		Total      costledger.Aggregate `json:"total"`
		Workspaces []costOverviewItem   `json:"workspaces"`
		ByUser     []costOverviewUser   `json:"by_user"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("get_code_costs = %d %s", w.Code, w.Body.String())
	}
	if len(got.Workspaces) != 1 || got.Workspaces[0].ID != "_users/owner/Chats/Code/projects/c0de0001-0000" || got.Total.TotalCostUSD != 5 || len(got.ByUser) != 2 {
		t.Fatalf("Code costs = %+v", got)
	}
	mock.mu.Lock()
	log := mock.files[codeAdminAuditPath(time.Now())]
	mock.mu.Unlock()
	if !strings.Contains(log, `"action":"read_costs"`) {
		t.Fatalf("cost read not audited:\n%s", log)
	}
}
