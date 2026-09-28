package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
)

// End-to-end tests for provider accounts (docs/design/provider_accounts.md,
// "Tests"). They drive the real HTTP handlers, the encrypted account
// registry on a workspace API, the run-time resolver every model init goes
// through, the provider setup terminal and the cost ledger. Only a live
// CLI login is faked: tests that need one are skipped without it.

type providerAccountsEnv struct {
	api  *StreamingAPI
	mock *mockWorkspaceAPI
	home string
}

func newProviderAccountsEnv(t *testing.T, policy string) *providerAccountsEnv {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AUTH_SECRET", "provider-accounts-test-secret")
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("LLM_CONFIG_LOCKED", "")
	t.Setenv("ALLOW_PERSONAL_PROVIDER_CONNECTIONS", "")
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "claude-code,codex-cli,cursor-cli,muse-cli")
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", policy)
	t.Setenv("AGENTWORKS_PRODUCT_DEFAULTS", "")
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	for _, name := range []string{"CURSOR_API_KEY", "CODEX_API_KEY", "META_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		t.Setenv(name, "")
	}
	withMemoryUserDirectory(t, `{"users":[
		{"id":"admin","username":"admin","email":"admin@x.com","admin":true,"can_create":true,"products":[]},
		{"id":"alice","username":"alice","email":"alice@x.com","can_create":true,"products":[]},
		{"id":"bob","username":"bob","email":"bob@x.com","can_create":true,"products":[]},
		{"id":"carol","username":"carol","email":"carol@x.com","can_create":true,"products":[]}]}`)
	mock := &mockWorkspaceAPI{files: map[string]string{
		// Alice owns W and Bob may run it; Carol has no access. Bob owns V.
		"Workflow/w/workflow.json": `{"id":"wf-w","label":"Weekly report","created_by":"alice","access":{"owners":["alice"],"readers":["bob"]}}`,
		"Workflow/v/workflow.json": `{"id":"wf-v","label":"Bob's own","created_by":"bob","access":{"owners":["bob"],"readers":[]}}`,
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &providerAccountsEnv{api: &StreamingAPI{chatStore: store}, mock: mock, home: home}
}

func (e *providerAccountsEnv) do(t *testing.T, handler http.HandlerFunc, method, target, user string, body interface{}, vars map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := sharedSecretsRequest(method, target, user, body)
	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func (e *providerAccountsEnv) addAccount(t *testing.T, user string, body map[string]interface{}) ProviderConnection {
	t.Helper()
	w := e.do(t, e.api.handleProviderConnections, http.MethodPost, "/api/provider-connections", user, body, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("add account: %d %s", w.Code, w.Body.String())
	}
	var created ProviderConnection
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created
}

func (e *providerAccountsEnv) list(t *testing.T, user, query string) []providerAccountView {
	t.Helper()
	w := e.do(t, e.api.handleProviderConnections, http.MethodGet, "/api/provider-connections"+query, user, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list accounts: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Connections []providerAccountView `json:"connections"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Connections
}

func findAccountView(views []providerAccountView, id string) (providerAccountView, bool) {
	for _, view := range views {
		if view.ID == id {
			return view, true
		}
	}
	return providerAccountView{}, false
}

// resolveForRun goes through the resolver a run's model init uses.
func (e *providerAccountsEnv) resolveForRun(principal, workspace, provider, id string) (*llm.ProviderAPIKeys, error) {
	keys, err := e.api.resolveEffectiveAPIKeys(context.Background(), principal, workspace, nil)
	if err != nil {
		return nil, err
	}
	return keys.ResolveConnection(context.Background(), llm.Provider(provider), id)
}

// Test 1: an installed Cursor account available to admins only.
func TestProviderAccountsInstalledAccountAdminsOnly(t *testing.T) {
	env := newProviderAccountsEnv(t, `{"cursor-cli":{"available_to":"admins"}}`)
	t.Setenv("CURSOR_API_KEY", "server-cursor-key")

	memberView, ok := findAccountView(env.list(t, "bob", "?workspace_path=Workflow/v"), "global:cursor-cli")
	if !ok || memberView.Usable || memberView.Availability == nil || memberView.Availability.Text != "Admins only" || memberView.Kind != "installed" || memberView.Source != "Installation (.env: CURSOR_API_KEY)" {
		t.Fatalf("member view of the admins-only account: %+v", memberView)
	}
	if strings.Contains(mustJSON(t, env.list(t, "bob", "")), "server-cursor-key") {
		t.Fatal("server credential exposed in the account list")
	}
	adminView, _ := findAccountView(env.list(t, "admin", "?workspace_path=Workflow/v"), "global:cursor-cli")
	if !adminView.Usable {
		t.Fatalf("admin cannot use the admins-only account: %+v", adminView)
	}
	// A run naming it, and a run naming no account (the server account by
	// default), are both refused for a member.
	if _, err := env.resolveForRun("bob", "Workflow/v", "cursor-cli", "global:cursor-cli"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("member run on the admins-only account: %v", err)
	}
	keys, err := env.api.resolveEffectiveAPIKeys(context.Background(), "bob", "Workflow/v", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := llm.InitializeLLM(llmguard.WithServerAccountAdmission(llm.Config{Provider: "cursor-cli", ModelID: "auto", APIKeys: keys, Context: context.Background()})); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("member model init without an account used the admins-only server account: %v", err)
	}
	if _, err := env.resolveForRun("admin", "Workflow/v", "cursor-cli", llmguard.ServerDefaultConnectionPrefix+"cursor-cli"); err != nil {
		t.Fatalf("admin run refused: %v", err)
	}
	// Providers the policy does not name keep today's behaviour.
	if _, err := env.resolveForRun("bob", "Workflow/v", "codex-cli", "global:codex-cli"); err != nil {
		t.Fatalf("unlisted provider refused: %v", err)
	}

	// Admins change "Available to" from the UI; members cannot.
	patch := map[string]interface{}{"available_to": map[string]interface{}{"users": []string{"bob@x.com"}}}
	if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "bob", patch, map[string]string{"connectionID": "global:cursor-cli"}); w.Code != http.StatusForbidden {
		t.Fatalf("member edited the server account: %d", w.Code)
	}
	if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "admin", patch, map[string]string{"connectionID": "global:cursor-cli"}); w.Code != http.StatusNoContent {
		t.Fatalf("admin edit: %d %s", w.Code, w.Body.String())
	}
	if _, err := env.resolveForRun("bob", "Workflow/v", "cursor-cli", "global:cursor-cli"); err != nil {
		t.Fatalf("bob named in Available to but refused: %v", err)
	}
	if _, err := env.resolveForRun("carol", "Workflow/v", "cursor-cli", "global:cursor-cli"); err == nil {
		t.Fatal("carol admitted although only bob is named")
	}

	// An installation-pinned account stays read-only.
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", `{"cursor-cli":{"available_to":"admins","pinned":true}}`)
	if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "admin", patch, map[string]string{"connectionID": "global:cursor-cli"}); w.Code != http.StatusConflict {
		t.Fatalf("pinned account edited: %d", w.Code)
	}
	if _, err := env.resolveForRun("bob", "Workflow/v", "cursor-cli", "global:cursor-cli"); err == nil {
		t.Fatal("pinned policy ignored in favour of the admin setting")
	}
}

// Test 2: an account shared with a workflow.
func TestProviderAccountsSharedWithWorkflow(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Alice Claude", "auth_method": "cli_login", "sharing": map[string]interface{}{"mode": "shared", "workflows": []string{"wf-w"}}})

	keys, err := env.resolveForRun("bob", "Workflow/w", "claude-code", account.ID)
	if err != nil {
		t.Fatalf("bob running W on Alice's shared account: %v", err)
	}
	if home := keys.RuntimeEnvironment["HOME"]; !strings.HasSuffix(home, filepath.Join("provider-connections", account.ID, "home")) {
		t.Fatalf("shared account did not run in its own HOME: %q", home)
	}
	// Scheduled runs of W use its owner (Alice) as principal.
	if _, err := env.api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: "alice", WorkspacePath: "Workflow/w"}, "claude-code", account.ID); err != nil {
		t.Fatalf("scheduled W run: %v", err)
	}
	// Copying the connection ID into Bob's own workflow grants nothing.
	if _, err := env.resolveForRun("bob", "Workflow/v", "claude-code", account.ID); err == nil || err.Error() != "this account is no longer available to workflow Bob's own" {
		t.Fatalf("bob used the account outside W: %v", err)
	}
	// Carol cannot run W at all, so W's share does not reach her.
	if _, err := env.resolveForRun("carol", "Workflow/w", "claude-code", account.ID); err == nil {
		t.Fatal("carol admitted through a workflow she cannot open")
	}

	// Bob sees the account in W's picker (metadata only), not elsewhere.
	view, ok := findAccountView(env.list(t, "bob", "?workspace_path=Workflow/w"), account.ID)
	if !ok || view.Relation != "shared_with_workflow" || !view.Usable || view.Sharing != nil || view.OwnerUserID != "" || view.OwnerName != "alice" || view.CanManage {
		t.Fatalf("bob's view in W: %+v", view)
	}
	if _, ok := findAccountView(env.list(t, "bob", "?workspace_path=Workflow/v"), account.ID); ok {
		t.Fatal("account listed in a workflow it is not shared with")
	}
	// Only the owner (or an admin) edits sharing or removes the account.
	if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "bob", map[string]interface{}{"sharing": map[string]interface{}{"mode": "shared", "workflows": []string{"wf-v"}}}, map[string]string{"connectionID": account.ID}); w.Code != http.StatusNotFound {
		t.Fatalf("bob edited Alice's sharing: %d", w.Code)
	}
	if w := env.do(t, env.api.handleProviderConnection, http.MethodDelete, "/", "bob", nil, map[string]string{"connectionID": account.ID}); w.Code != http.StatusNotFound {
		t.Fatalf("bob removed Alice's account: %d", w.Code)
	}
	// Alice may only share with workflows she can see.
	if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "alice", map[string]interface{}{"sharing": map[string]interface{}{"mode": "shared", "workflows": []string{"wf-v"}}}, map[string]string{"connectionID": account.ID}); w.Code != http.StatusBadRequest {
		t.Fatalf("alice shared with a workflow she cannot see: %d", w.Code)
	}

	// Alice removes W: Bob's next W turn is refused with the clear error.
	if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "alice", map[string]interface{}{"sharing": map[string]interface{}{"mode": "private"}}, map[string]string{"connectionID": account.ID}); w.Code != http.StatusNoContent {
		t.Fatalf("alice made the account private: %d %s", w.Code, w.Body.String())
	}
	if _, err := env.resolveForRun("bob", "Workflow/w", "claude-code", account.ID); err == nil || err.Error() != "this account is no longer available to workflow Weekly report" {
		t.Fatalf("bob's next W turn after the share was removed: %v", err)
	}
	if _, err := env.resolveForRun("alice", "Workflow/w", "claude-code", account.ID); err != nil {
		t.Fatalf("owner lost her own account: %v", err)
	}
	// An admin sees it but cannot run on it.
	adminView, ok := findAccountView(env.list(t, "admin", ""), account.ID)
	if !ok || adminView.Relation != "admin_view" || adminView.Usable || !adminView.CanManage || adminView.Sharing == nil {
		t.Fatalf("admin view: %+v", adminView)
	}
	if _, err := env.resolveForRun("admin", "Workflow/w", "claude-code", account.ID); err == nil {
		t.Fatal("admin ran on someone's private account")
	}
	// Removal deletes the login files too.
	home, _ := providerConnectionHome(account.ID)
	if w := env.do(t, env.api.handleProviderConnection, http.MethodDelete, "/", "alice", nil, map[string]string{"connectionID": account.ID}); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("account HOME left behind after removal: %v", err)
	}
}

// Test 3: an account shared with a person, used in Bob's Code; plus a Crew share.
func TestProviderAccountsSharedWithPersonAndCrew(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "codex-cli", "display_name": "Alice Codex", "credential": "alice-codex-key", "sharing": map[string]interface{}{"mode": "shared", "users": []string{"bob@x.com"}}})
	bobCode := "_users/bob/Chats/Code/projects/p1"
	keys, err := env.resolveForRun("bob", bobCode, "codex-cli", account.ID)
	if err != nil || keys.CodexCLI == nil || *keys.CodexCLI != "alice-codex-key" {
		t.Fatalf("bob in his Code: %v", err)
	}
	if _, err := env.resolveForRun("carol", "_users/carol/Chats/Code/projects/p2", "codex-cli", account.ID); err == nil || err.Error() != "this account is no longer available to this Code" {
		t.Fatalf("carol used an account not shared with her: %v", err)
	}
	view, ok := findAccountView(env.list(t, "bob", ""), account.ID)
	if !ok || view.Relation != "shared_with_you" || !view.Usable || !view.CanViewUsage {
		t.Fatalf("bob's view: %+v", view)
	}
	if strings.Contains(mustJSON(t, env.list(t, "bob", "")), "alice-codex-key") {
		t.Fatal("credential exposed to the person it is shared with")
	}
	if _, ok := findAccountView(env.list(t, "carol", ""), account.ID); ok {
		t.Fatal("carol sees an account not shared with her")
	}
	// Sharing only names known people.
	if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "alice", map[string]interface{}{"sharing": map[string]interface{}{"mode": "shared", "users": []string{"mallory"}}}, map[string]string{"connectionID": account.ID}); w.Code != http.StatusBadRequest {
		t.Fatalf("shared with an unknown person: %d", w.Code)
	}

	// Crew share: every chat of Alice's Crew, by anyone with access, may use it.
	crew := "_users/alice/Chats/Work/projects/c1"
	crewAccount := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Crew login", "auth_method": "cli_login", "sharing": map[string]interface{}{"mode": "shared", "crews": []string{crew}}})
	if _, err := env.resolveForRun("bob", crew+"/notes", "claude-code", crewAccount.ID); err != nil {
		t.Fatalf("bob chatting with Alice's Crew: %v", err)
	}
	if _, err := env.resolveForRun("bob", "_users/bob/Chats/Work/projects/c2", "claude-code", crewAccount.ID); err == nil {
		t.Fatal("crew share leaked to another Crew")
	}
	if view, ok := findAccountView(env.list(t, "bob", "?workspace_path="+crew), crewAccount.ID); !ok || view.Relation != "shared_with_crew" {
		t.Fatalf("bob's view in the Crew: %+v", view)
	}
}

// Test 4 (flipped by the owner decision of 2026-09-28: native tools on by
// default everywhere): a turn on someone else's shared account keeps the
// configured tool mode, see TestProviderAccountsSharedAccountAndCodeTurnsAreHybrid.
// The query path still derives the account a lightweight follow-up runs on.
func TestProviderAccountsFollowUpKeepsItsAccount(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Alice Claude", "auth_method": "cli_login", "sharing": map[string]interface{}{"mode": "shared", "users": []string{"bob"}}})
	env.api.lastQueryRequests = map[string]QueryRequest{"s1": {Provider: "claude-code", ConnectionID: account.ID}}
	if provider, id := env.api.queryTurnConnectionForSession(QueryRequest{Query: "next"}, "s1"); provider != "claude-code" || id != account.ID {
		t.Fatalf("follow-up turn lost its account: %s %s", provider, id)
	}
}

// Test 5: signing in the server account is admin-only and logged as the
// server account; a private browser login never touches the service HOME.
func TestProviderAccountsServerSignInAdminOnly(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	original := providerSetupCommands
	providerSetupCommands = map[string]map[string]providerSetupCommand{
		"claude-code": {"authenticate": {command: "/bin/sh", args: []string{"-c", `printf 'login-home:%s\n' "$HOME"; sleep 1`}}},
	}
	t.Cleanup(func() { providerSetupCommands = original })
	start := func(user, connectionID string) *httptest.ResponseRecorder {
		return env.do(t, env.api.handleStartProviderSetup, http.MethodPost, "/", user, map[string]interface{}{"provider": "claude-code", "action": "authenticate", "connection_id": connectionID}, nil)
	}
	if w := start("bob", ""); w.Code != http.StatusForbidden {
		t.Fatalf("member signed in the server account: %d", w.Code)
	}
	if w := start("bob", "global:claude-code"); w.Code != http.StatusForbidden {
		t.Fatalf("member signed in the server account by its id: %d", w.Code)
	}
	logs := captureLogs(t)
	if w := start("admin", ""); w.Code != http.StatusCreated {
		t.Fatalf("admin sign-in: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(logs.String(), "claude-code authenticate for server account (HOME service HOME) by admin") {
		t.Fatalf("server sign-in not logged as the server account: %s", logs.String())
	}
	closeProviderSetups(env.api)

	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Alice Claude", "auth_method": "cli_login"})
	if w := start("bob", account.ID); w.Code != http.StatusForbidden {
		t.Fatalf("bob signed in Alice's account: %d", w.Code)
	}
	w := start("alice", account.ID)
	if w.Code != http.StatusCreated {
		t.Fatalf("alice sign-in: %d %s", w.Code, w.Body.String())
	}
	output := waitSetupOutput(t, env.api, w, "login-home:")
	accountHome, _ := providerConnectionHome(account.ID)
	if !strings.Contains(output, "login-home:"+accountHome) || strings.Contains(output, "login-home:"+env.home+"\n") {
		t.Fatalf("private browser login ran outside the account HOME: %q", output)
	}
}

// Test 7: "Usage" on a private Muse account runs /usage in that account's
// HOME, not the server's. The CLI is faked; see
// TestProviderAccountsLiveMuseUsage for the live CLI.
func TestProviderAccountsUsageRunsInAccountHome(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	original := providerSetupCommands
	providerSetupCommands = map[string]map[string]providerSetupCommand{
		"muse-cli":    {"usage": {command: "/bin/sh", args: []string{"-c", `printf '❯\n'; IFS= read -r command; printf 'submitted:%s home:%s\n' "$command" "$HOME"; sleep 1`}}},
		"claude-code": {"usage": {command: "/bin/sh", args: []string{"-c", `printf 'server-usage\n'; sleep 1`}}},
	}
	t.Cleanup(func() { providerSetupCommands = original })
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "muse-cli", "display_name": "Alice Muse", "auth_method": "cli_login"})
	usage := func(user, provider, connectionID string) *httptest.ResponseRecorder {
		return env.do(t, env.api.handleStartProviderSetup, http.MethodPost, "/", user, map[string]interface{}{"provider": provider, "action": "usage", "connection_id": connectionID}, nil)
	}
	w := usage("alice", "muse-cli", account.ID)
	if w.Code != http.StatusCreated {
		t.Fatalf("usage: %d %s", w.Code, w.Body.String())
	}
	output := waitSetupOutput(t, env.api, w, "submitted:")
	accountHome, _ := providerConnectionHome(account.ID)
	if !strings.Contains(output, "submitted:/usage home:"+accountHome) {
		t.Fatalf("usage did not run /usage in the account HOME: %q", output)
	}
	// A private account's usage is the owner's (and admins') only.
	if w := usage("bob", "muse-cli", account.ID); w.Code != http.StatusForbidden {
		t.Fatalf("bob saw Alice's private usage: %d", w.Code)
	}
	// A member sees server-account usage as text the server collected; they
	// never get a terminal.
	w = usage("bob", "claude-code", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"usage_output":"server-usage"`) {
		t.Fatalf("member server usage: %d %s", w.Code, w.Body.String())
	}
	if w := env.do(t, env.api.handleStartProviderSetup, http.MethodPost, "/", "bob", map[string]interface{}{"provider": "muse-cli", "action": "inspect"}, nil); w.Code != http.StatusForbidden {
		t.Fatalf("member inspected the server account: %d", w.Code)
	}
	closeProviderSetups(env.api)
}

// Usage for someone who does not manage the account runs without a
// terminal: the server sends /usage, returns the text, ends the session,
// and drops every input.
func TestProviderAccountsUsageForNonManagersIsNotInteractive(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	original, settle := providerSetupCommands, providerUsageSettle
	providerUsageSettle = 300 * time.Millisecond
	providerSetupCommands = map[string]map[string]providerSetupCommand{
		"muse-cli": {"usage": {command: "/bin/sh", args: []string{"-c", `printf '\033[1m❯\033[0m\n'; while IFS= read -r line; do printf 'got:%s home:%s\n' "$line" "$HOME"; done`}}},
	}
	t.Cleanup(func() { providerSetupCommands, providerUsageSettle = original, settle })
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "muse-cli", "display_name": "Alice Muse", "auth_method": "cli_login", "sharing": map[string]interface{}{"mode": "shared", "users": []string{"bob"}}})
	for _, action := range []string{"authenticate", "inspect"} {
		if w := env.do(t, env.api.handleStartProviderSetup, http.MethodPost, "/", "bob", map[string]interface{}{"provider": "muse-cli", "action": action, "connection_id": account.ID}, nil); w.Code != http.StatusForbidden {
			t.Fatalf("non-owner %s: %d", action, w.Code)
		}
	}
	w := env.do(t, env.api.handleStartProviderSetup, http.MethodPost, "/", "bob", map[string]interface{}{"provider": "muse-cli", "action": "usage", "connection_id": account.ID}, nil)
	var body struct {
		UsageOutput string                 `json:"usage_output"`
		Session     *providerSetupSnapshot `json:"session"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Session != nil {
		t.Fatalf("non-owner usage: %d %s", w.Code, w.Body.String())
	}
	accountHome, _ := providerConnectionHome(account.ID)
	if !strings.Contains(body.UsageOutput, "got:/usage home:"+accountHome) || strings.Contains(body.UsageOutput, "\x1b") {
		t.Fatalf("usage text: %q", body.UsageOutput)
	}
	manager := env.api.providerSetupManager()
	manager.mu.Lock()
	remaining := len(manager.sessions)
	manager.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("the usage session was left running: %d", remaining)
	}

	// A read-only session drops browser input (what the stream handler sends).
	session, err := manager.start("bob", "muse-cli", "usage", 80, 24, nil, nil, false, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	session.mu.Lock()
	session.readOnly = true
	session.mu.Unlock()
	_ = session.userInput("cat ~/.muse/credentials\r")
	time.Sleep(600 * time.Millisecond)
	if strings.Contains(session.outputText(), "got:cat") {
		t.Fatalf("read-only session accepted input: %q", session.outputText())
	}
	closeProviderSetups(env.api)
}

// Test 7, live: needs a signed-in Muse CLI account. Set
// AGENTWORKS_LIVE_MUSE_ACCOUNT_HOME to a HOME that holds a `muse login`.
func TestProviderAccountsLiveMuseUsage(t *testing.T) {
	liveHome := os.Getenv("AGENTWORKS_LIVE_MUSE_ACCOUNT_HOME")
	if liveHome == "" {
		t.Skip("needs AGENTWORKS_LIVE_MUSE_ACCOUNT_HOME (a HOME with a muse login)")
	}
	if _, err := exec.LookPath("muse"); err != nil {
		t.Skip("muse CLI not installed")
	}
	env := newProviderAccountsEnv(t, "")
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "muse-cli", "display_name": "Live Muse", "auth_method": "cli_login"})
	home, _ := providerConnectionHome(account.ID)
	_ = os.MkdirAll(filepath.Dir(home), 0o700)
	if err := os.Symlink(liveHome, home); err != nil {
		t.Fatal(err)
	}
	w := env.do(t, env.api.handleStartProviderSetup, http.MethodPost, "/", "alice", map[string]interface{}{"provider": "muse-cli", "action": "usage", "connection_id": account.ID}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("usage: %d %s", w.Code, w.Body.String())
	}
	output := waitSetupOutputFor(t, env.api, w, "/usage", 60*time.Second)
	t.Logf("live muse usage output: %s", output)
	closeProviderSetups(env.api)
}

// Test 6: a turn on Alice's shared account, run by Bob in W, lands under
// Alice's account with Bob and W in the split; Alice and admins see it,
// Bob sees only his share.
func TestProviderAccountsCostSplitVisibility(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Alice Claude", "auth_method": "cli_login", "sharing": map[string]interface{}{"mode": "shared", "workflows": []string{"wf-w"}}})
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	env.api.costLedger = ledger
	turn := func(user, workspace, connectionID string, span string, cost float64) {
		obs := newCostObserver(ledger, "sess-"+span, user, "workflow_phase",
			withCostModel("claude-code", "sonnet"),
			withCostAccount(costAccountIDFor("claude-code", connectionID)),
			withCostAttribution("workflow_execution", workspace, "run-1", "exec-"+span))
		if err := obs.HandleEvent(context.Background(), &unifiedevents.AgentEvent{
			Type: unifiedevents.LLMGenerationEnd, Timestamp: time.Now().UTC(), SpanID: span, Component: "llm",
			Data: &unifiedevents.LLMGenerationEndEvent{
				UsageMetrics:  unifiedevents.UsageMetrics{PromptTokens: 100, CompletionTokens: 10},
				BaseEventData: unifiedevents.BaseEventData{Metadata: map[string]interface{}{"provider": "claude-code", "cost_usd": cost}},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	turn("bob", "Workflow/w", account.ID, "bob-w", 0.5)
	turn("alice", "Workflow/w", account.ID, "alice-w", 0.25)
	turn("carol", "Workflow/v", "", "carol-server", 1)
	// A row written before accounts were recorded.
	if err := ledger.Append(costledger.Entry{Timestamp: time.Now().UTC(), UserID: "bob", WorkflowID: "Workflow/w", Scope: "chat", Provider: "claude-code", ModelID: "sonnet", PromptTokens: 5, TotalCostUSD: 0.1}); err != nil {
		t.Fatal(err)
	}
	costs := func(user string) providerAccountCostsResponse {
		w := env.do(t, env.api.handleProviderAccountCosts, http.MethodGet, "/api/provider-accounts/costs", user, nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("costs: %d %s", w.Code, w.Body.String())
		}
		var resp providerAccountCostsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	accountRow := func(resp providerAccountCostsResponse, id string) *providerAccountCost {
		for _, provider := range resp.Providers {
			for _, row := range provider.Accounts {
				if row.AccountID == id {
					return row
				}
			}
		}
		return nil
	}
	hasSplit := func(row *providerAccountCost, work, user string) bool {
		for _, split := range row.Split {
			if split.WorkID == work && split.UserID == user {
				return true
			}
		}
		return false
	}
	alice := accountRow(costs("alice"), account.ID)
	if alice == nil || !alice.FullSplit || alice.Name != "Alice Claude" || !hasSplit(alice, "Workflow/w", "bob") || !hasSplit(alice, "Workflow/w", "alice") || alice.Total.TotalCostUSD < 0.74 {
		t.Fatalf("owner's view of her shared account: %+v", alice)
	}
	admin := costs("admin")
	if row := accountRow(admin, account.ID); row == nil || !row.FullSplit || !hasSplit(row, "Workflow/w", "bob") {
		t.Fatalf("admin's view: %+v", row)
	}
	if row := accountRow(admin, "global:claude-code"); row == nil || row.Kind != "server" || !hasSplit(row, "Workflow/v", "carol") {
		t.Fatalf("admin's server account row: %+v", row)
	}
	if row := accountRow(admin, ""); row == nil || row.Kind != "unrecorded" || row.Name != "Unrecorded account" {
		t.Fatalf("older rows not shown as unrecorded: %+v", row)
	}
	bob := costs("bob")
	bobRow := accountRow(bob, account.ID)
	if bobRow == nil || bobRow.FullSplit || len(bobRow.Split) != 1 || bobRow.Split[0].UserID != "bob" {
		t.Fatalf("bob's share: %+v", bobRow)
	}
	if accountRow(bob, "global:claude-code") != nil {
		t.Fatal("bob sees carol's server-account spend")
	}
}

// Product defaults: validated at start, served to the UI, admin-editable.
func TestProviderAccountsProductDefaults(t *testing.T) {
	env := newProviderAccountsEnv(t, `{"muse-cli":{"available_to":{"products":["code"]}}}`)
	t.Setenv("AGENTWORKS_PRODUCT_DEFAULTS", `{"code":{"provider":"muse-cli","model":"muse-1"},"agentworks":{"provider":"claude-code","model":"sonnet"}}`)
	if err := validateProviderAccountInstallation(); err != nil {
		t.Fatalf("valid defaults refused: %v", err)
	}
	t.Setenv("AGENTWORKS_PRODUCT_DEFAULTS", `{"work":{"provider":"muse-cli","model":"muse-1"}}`)
	if err := validateProviderAccountInstallation(); err == nil || !strings.Contains(err.Error(), "not available to Crews") {
		t.Fatalf("a default its policy does not admit must stop the server: %v", err)
	}
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", `{"muse-cli":{"available_to":"sometimes"}}`)
	if err := validateProviderAccountInstallation(); err == nil {
		t.Fatal("an invalid policy must stop the server")
	}
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", `{"muse-cli":{"available_to":{"products":["code"]}}}`)
	t.Setenv("AGENTWORKS_PRODUCT_DEFAULTS", `{"code":{"provider":"muse-cli","model":"muse-1","pinned":true}}`)

	// The Code profile starts with the installation default.
	profile := agentprofiles.Profile{Product: "code", Runtime: agentprofiles.RuntimePolicy{ProviderOptions: []agentprofiles.ProviderOption{{ID: "claude", Provider: "claude-code", ModelID: "sonnet", Default: true}}}}
	applyInstallationProductDefault(&profile)
	defaults := 0
	for _, option := range profile.Runtime.ProviderOptions {
		if option.Default {
			defaults++
			if option.Provider != "muse-cli" || option.ModelID != "muse-1" {
				t.Fatalf("wrong default engine: %+v", option)
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("want exactly one default engine: %+v", profile.Runtime.ProviderOptions)
	}
	// The real Code and Crew profiles still register with a default engine
	// they did not list.
	for _, builtin := range append(codeproduct.BuiltinAgentProfiles(), workproduct.BuiltinAgentProfiles()...) {
		builtin.Product = builtin.ID
		value := productDefault{Provider: "muse-cli", Model: "muse-1"}
		applyProductDefaultToProfile(&builtin, value, true)
		if err := agentprofiles.NewRegistry().RegisterProfile(builtin); err != nil {
			t.Fatalf("%s with an installation default does not register: %v", builtin.ID, err)
		}
	}

	// Pinned products are read-only; members cannot edit; a default must be
	// admitted for its product.
	put := func(user string, body map[string]interface{}) *httptest.ResponseRecorder {
		return env.do(t, env.api.handleProductDefaults, http.MethodPut, "/", user, body, nil)
	}
	if w := put("admin", map[string]interface{}{"product_defaults": map[string]interface{}{"code": map[string]string{"provider": "claude-code", "model": "sonnet"}}}); w.Code != http.StatusConflict {
		t.Fatalf("pinned default edited: %d", w.Code)
	}
	if w := put("bob", map[string]interface{}{"product_defaults": map[string]interface{}{"work": map[string]string{"provider": "claude-code", "model": "sonnet"}}}); w.Code != http.StatusForbidden {
		t.Fatalf("member edited defaults: %d", w.Code)
	}
	if w := put("admin", map[string]interface{}{"product_defaults": map[string]interface{}{"work": map[string]string{"provider": "muse-cli", "model": "muse-1"}}}); w.Code != http.StatusBadRequest {
		t.Fatalf("default not admitted for its product accepted: %d", w.Code)
	}
	if w := put("admin", map[string]interface{}{"product_defaults": map[string]interface{}{"work": map[string]string{"provider": "claude-code", "model": "sonnet"}}}); w.Code != http.StatusOK {
		t.Fatalf("admin default: %d %s", w.Code, w.Body.String())
	}
	// The workflows default shows up where new workflows read their model.
	t.Setenv("AGENTWORKS_PRODUCT_DEFAULTS", `{"agentworks":{"provider":"codex-cli","model":"gpt-5"}}`)
	w := env.do(t, env.api.handleGetLLMDefaults, http.MethodGet, "/api/llm-config/defaults", "bob", nil, nil)
	var body struct {
		PrimaryConfig   map[string]interface{}            `json:"primary_config"`
		ProductDefaults map[string]map[string]interface{} `json:"product_defaults"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.PrimaryConfig["provider"] != "codex-cli" || body.ProductDefaults["work"]["provider"] != "claude-code" || body.ProductDefaults["agentworks"]["connection_id"] != "global:codex-cli" {
		t.Fatalf("defaults response: %+v", body)
	}
	// Taking a provider away from a product whose default uses it is refused.
	if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "admin", map[string]interface{}{"available_to": "admins"}, map[string]string{"connectionID": "global:claude-code"}); w.Code != http.StatusBadRequest {
		t.Fatalf("availability change broke the Crews default: %d", w.Code)
	}
}

// Without the new settings, today's behaviour holds: the server account is
// everyone's and a private account is the owner's only.
func TestProviderAccountsDefaultsKeepTodaysBehaviour(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	for _, user := range []string{"alice", "bob", ""} {
		if _, err := env.api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: user}, "claude-code", "global:claude-code"); err != nil {
			t.Fatalf("server account refused for %q: %v", user, err)
		}
	}
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "cursor-cli", "display_name": "Mine", "credential": "k"})
	if view, _ := findAccountView(env.list(t, "alice", ""), account.ID); view.Sharing == nil || view.Sharing.Mode != providerSharingPrivate {
		t.Fatalf("a new account must be private: %+v", view)
	}
	if _, err := env.api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: "bob"}, "cursor-cli", account.ID); err == nil {
		t.Fatal("private account used by someone else")
	}
}

func mustJSON(t *testing.T, value interface{}) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func waitSetupOutput(t *testing.T, api *StreamingAPI, w *httptest.ResponseRecorder, want string) string {
	return waitSetupOutputFor(t, api, w, want, 5*time.Second)
}

func waitSetupOutputFor(t *testing.T, api *StreamingAPI, w *httptest.ResponseRecorder, want string, timeout time.Duration) string {
	t.Helper()
	var body struct {
		Session providerSetupSnapshot `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	session, ok := api.providerSetupManager().get(body.Session.ID)
	if !ok {
		t.Fatalf("setup session %q not found", body.Session.ID)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if output := session.outputText(); strings.Contains(output, want) {
			time.Sleep(100 * time.Millisecond)
			return session.outputText()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("setup output never contained %q: %q", want, session.outputText())
	return ""
}

func closeProviderSetups(api *StreamingAPI) {
	manager := api.providerSetupManager()
	manager.mu.Lock()
	ids := make([]string, 0, len(manager.sessions))
	for id := range manager.sessions {
		ids = append(ids, id)
	}
	manager.mu.Unlock()
	for _, id := range ids {
		manager.remove(id, true)
	}
}

type lockedLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureLogs(t *testing.T) *lockedLogBuffer {
	t.Helper()
	buffer := &lockedLogBuffer{}
	previous := log.Writer()
	log.SetOutput(buffer)
	t.Cleanup(func() { log.SetOutput(previous) })
	return buffer
}
