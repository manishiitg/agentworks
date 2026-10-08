package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	unifiedevents "github.com/manishiitg/mcpagent/events"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// Wiring of provider-account rules into the query, retained-CLI, live-input,
// delegation, scheduled-run and cost paths.

// setWorkflowW rewrites W's manifest: Bob is an editor and W's Builder model
// runs on connectionID.
func (e *providerAccountsEnv) setWorkflowW(t *testing.T, connectionID string) {
	t.Helper()
	manifest := map[string]interface{}{
		"id": "wf-w", "label": "Weekly report", "created_by": "alice",
		"access": map[string]interface{}{"owners": []string{"alice"}, "editors": []string{"bob"}, "readers": []string{}},
		"capabilities": map[string]interface{}{"llm_config": map[string]interface{}{
			"schema_version": 2, "mode": "explicit",
			"builder_llm": map[string]interface{}{"provider": "claude-code", "model_id": "sonnet", "connection_id": connectionID},
		}},
	}
	data, _ := json.Marshal(manifest)
	e.mock.mu.Lock()
	e.mock.files["Workflow/w/workflow.json"] = string(data)
	e.mock.mu.Unlock()
}

// queryToolsMode sends a Builder message through handleQuery and returns
// the agent-tools mode the turn decided.
func (e *providerAccountsEnv) queryToolsMode(t *testing.T, user, sessionID string) (string, int) {
	t.Helper()
	decided := ""
	e.api.internalAgentToolsModeDecided = func(_ string, mode string) bool {
		decided = normalizeAgentToolsMode(mode)
		return true
	}
	req := QueryRequest{
		Query: "tidy the plan", AgentMode: "workflow_phase", SelectedFolder: "Workflow/w",
		// The Builder names no account; the workflow's saved model does.
		LLMConfig: &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "claude-code", ModelID: "sonnet"}},
	}
	w := httptest.NewRecorder()
	httpReq := sharedSecretsRequest(http.MethodPost, "/api/query", user, req)
	httpReq.Header.Set("X-Session-ID", sessionID)
	e.api.handleQuery(w, httpReq)
	return decided, w.Code
}

// Owner decision 2026-09-28: native tools are on by default everywhere. A
// Builder turn on someone else's shared account keeps the workflow's
// configured mode (hybrid), and so does a Code turn.
func TestProviderAccountsSharedAccountAndCodeTurnsAreFull(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	setDirectoryAdmin(t, "alice", true) // workflow shares are admin-only (PLAT-715)
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Alice Claude", "auth_method": "cli_login", "sharing": map[string]interface{}{"mode": "shared", "workflows": []string{"wf-w"}}})
	env.setWorkflowW(t, account.ID)
	if mode, code := env.queryToolsMode(t, "bob", "bob-builder"); mode != "full" {
		t.Fatalf("bob's Builder turn on Alice's shared account decided %q (status %d), want full", mode, code)
	}
	if mode, _ := env.queryToolsMode(t, "alice", "alice-builder"); mode != "full" {
		t.Fatalf("alice's own Builder turn decided %q, want full", mode)
	}
	// The final account still decides admission.
	if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "alice", map[string]interface{}{"sharing": map[string]interface{}{"mode": "private"}}, map[string]string{"connectionID": account.ID}); w.Code != http.StatusNoContent {
		t.Fatalf("unshare: %d", w.Code)
	}
	if mode, code := env.queryToolsMode(t, "bob", "bob-builder-2"); code != http.StatusForbidden || mode != "" {
		t.Fatalf("bob's turn after unsharing: status %d mode %q", code, mode)
	}

	// A Code turn: the real Code profile, a Code with no switch set.
	const codeRoot = "_users/alice/Chats/Code/projects/site"
	env.mock.mu.Lock()
	env.mock.files[codeRoot+"/product.json"] = `{"schema_version":1,"product":"code","id":"site","title":"Site","session_id":"code:site"}`
	env.mock.files[codeRoot+"/workflow.json"] = `{"capabilities":{}}`
	env.mock.mu.Unlock()
	registry := agentprofiles.NewRegistry()
	for _, profile := range codeproduct.BuiltinAgentProfiles() {
		profile.Product = codeproduct.ProfileID
		if err := registry.RegisterProfile(profile); err != nil {
			t.Fatal(err)
		}
	}
	env.api.agentProfiles = registry
	codeTurn := func() string {
		req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: codeproduct.ProfileID, AgentProfileConversationKey: "site", SelectedFolder: "Chats/Code/projects/site", AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Site"}}
		resolved, err := env.api.resolveAgentProfileForQuery(context.Background(), &req, "alice", "code-session")
		if err != nil {
			t.Fatalf("resolve Code turn: %v", err)
		}
		return normalizeAgentToolsMode(resolved.Definition.Runtime.AgentTools.Mode)
	}
	if mode := codeTurn(); mode != "full" {
		t.Fatalf("Code turn with no switch set decided %q, want full", mode)
	}
	env.mock.mu.Lock()
	env.mock.files[codeRoot+"/workflow.json"] = `{"capabilities":{"native_agent_tools":false}}`
	env.mock.mu.Unlock()
	// No switch any more (2026-09-29): an older Code's stored "off" is ignored.
	if mode := codeTurn(); mode != "full" {
		t.Fatalf("a Code with a stored off decided %q, want full: native tools are always on", mode)
	}
}

// Delegation runs as the parent turn's principal in the parent's
// workspace; a scheduled run uses the workflow owner.
func TestProviderAccountsDelegationAndScheduledScopes(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	setDirectoryAdmin(t, "alice", true) // workflow shares are admin-only (PLAT-715)
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Alice Claude", "auth_method": "cli_login", "sharing": map[string]interface{}{"mode": "shared", "workflows": []string{"wf-w"}}})
	env.setWorkflowW(t, account.ID)
	ctx := context.WithValue(context.Background(), common.UserIDKey, "bob")
	parent := QueryRequest{SelectedFolder: "Workflow/w", userID: "someone-else"}
	principal := delegationPrincipal(ctx, parent)
	scope := delegationProviderAccountScope(principal, parent)
	if principal != "bob" || scope.Principal != "bob" || scope.WorkspacePath != "Workflow/w" {
		t.Fatalf("delegation scope: %+v", scope)
	}
	keys := env.api.withConnectionResolver(nil, scope)
	if _, err := keys.ResolveConnection(ctx, "claude-code", account.ID); err != nil {
		t.Fatalf("bob's sub-agent in W: %v", err)
	}
	outside := env.api.withConnectionResolver(nil, delegationProviderAccountScope("bob", QueryRequest{SelectedFolder: "Workflow/v"}))
	if _, err := outside.ResolveConnection(ctx, "claude-code", account.ID); err == nil {
		t.Fatal("bob's sub-agent outside W used the account")
	}

	var capabilities WorkflowCapabilities
	capabilities.LLMConfig = &workflowtypes.PresetLLMConfig{SchemaVersion: 2, Mode: workflowtypes.LLMConfigModeExplicit, BuilderLLM: &workflowtypes.AgentLLMConfig{Provider: "claude-code", ModelID: "sonnet", ConnectionID: account.ID}}
	scheduled := &ScheduleContext{OwnerUserID: "alice", WorkspacePath: "Workflow/w", Capabilities: capabilities}
	if _, err := env.api.scheduleProviderAPIKeys(context.Background(), scheduled); err != nil {
		t.Fatalf("scheduled run of W as its owner: %v", err)
	}
	scheduled.OwnerUserID, scheduled.WorkspacePath = "bob", "Workflow/v"
	if _, err := env.api.scheduleProviderAPIKeys(context.Background(), scheduled); err == nil {
		t.Fatal("scheduled run of V used an account shared only with W")
	}
}

// MEDIUM 6: an account owner sees who used the account, but not the name
// of a workflow she cannot open.
func TestProviderAccountsCostSplitMasksHiddenWork(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	withMemoryUserDirectory(t, `{"users":[
        {"id":"admin","username":"admin","admin":true,"can_create":true},
        {"id":"alice","username":"alice","can_create":true,"code_reviewer":true},
        {"id":"bob","username":"bob","can_create":true,"code_reviewer":true},
        {"id":"carol","username":"carol","can_create":true}]}`)
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Alice Claude", "auth_method": "cli_login", "sharing": map[string]interface{}{"mode": "shared", "users": []string{"bob"}}})
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	env.api.costLedger = ledger
	obs := newCostObserver(ledger, "sess-v", "bob", "workflow_phase", withCostModel("claude-code", "sonnet"),
		withCostAccount(costAccountIDFor("claude-code", account.ID)), withCostAttribution("workflow_execution", "Workflow/v/runs/1", "run-1", "exec-1"))
	if err := obs.HandleEvent(context.Background(), &unifiedevents.AgentEvent{Type: unifiedevents.LLMGenerationEnd, Timestamp: time.Now().UTC(), SpanID: "v1", Component: "llm",
		Data: &unifiedevents.LLMGenerationEndEvent{UsageMetrics: unifiedevents.UsageMetrics{PromptTokens: 10}, BaseEventData: unifiedevents.BaseEventData{Metadata: map[string]interface{}{"provider": "claude-code", "cost_usd": 0.3}}}}); err != nil {
		t.Fatal(err)
	}
	split := func(user string) *providerAccountCostSplit {
		w := env.do(t, env.api.handleProviderAccountCosts, http.MethodGet, "/", user, nil, nil)
		var resp providerAccountCostsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		for _, provider := range resp.Providers {
			for _, row := range provider.Accounts {
				if row.AccountID == account.ID && len(row.Split) == 1 {
					return row.Split[0]
				}
			}
		}
		t.Fatalf("%s: no split for the account: %s", user, w.Body.String())
		return nil
	}
	alice := split("alice")
	if alice.WorkName != "a workflow you can't see" || strings.Contains(alice.WorkID, "Workflow/v") || alice.UserID != "bob" || alice.TotalCostUSD < 0.29 {
		t.Fatalf("owner's split of hidden work: %+v", alice)
	}
	if bob := split("bob"); bob.WorkName != "v" {
		t.Fatalf("bob's own share names his workflow: %+v", bob)
	}
	if admin := split("admin"); admin.WorkID != "Workflow/v" {
		t.Fatalf("admin view: %+v", admin)
	}
}

// LOW 7: share targets list people the caller already sees in share
// dialogs, and read-only accounts only for admins.
func TestProviderAccountsShareTargetsHideReadOnlyPeople(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"admin","username":"admin","admin":true,"can_create":true,"products":[]},
		{"id":"alice","username":"alice","can_create":true,"products":[]},
		{"id":"bob","username":"bob","can_create":true,"products":[]},
		{"id":"viewer","username":"viewer","role":"viewer","products":[]},
		{"id":"gone","username":"gone","can_create":true,"disabled":true,"products":[]}]}`)
	names := func(user string) string {
		w := env.do(t, env.api.handleProviderShareTargets, http.MethodGet, "/", user, nil, nil)
		var body struct {
			Users []struct {
				ID   string
				Self bool
			} `json:"users"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		ids := []string{}
		for _, u := range body.Users {
			// The caller is listed as self (for the server account's
			// availability); sharing pickers leave it out.
			if u.Self != (u.ID == user) {
				t.Fatalf("%s listed with self=%v for caller %s", u.ID, u.Self, user)
			}
			if !u.Self {
				ids = append(ids, u.ID)
			}
		}
		return strings.Join(ids, ",")
	}
	if got := names("alice"); strings.Contains(got, "viewer") || strings.Contains(got, "gone") || strings.Contains(got, "alice") || !strings.Contains(got, "bob") {
		t.Fatalf("member share targets: %s", got)
	}
	if got := names("admin"); !strings.Contains(got, "viewer") {
		t.Fatalf("admin share targets: %s", got)
	}
}

// LOW 8: a new workflow copies the workflows default, and the server's
// fallback model is that default too.
func TestProviderAccountsNewWorkflowCopiesProductDefault(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	t.Setenv("AGENTWORKS_PRODUCT_DEFAULTS", `{"agentworks":{"provider":"codex-cli","model":"gpt-5"}}`)
	if provider, model := getPrimaryProviderAndModelFromDefaults(); provider != "codex-cli" || model != "gpt-5" {
		t.Fatalf("server fallback ignores the workflows default: %s/%s", provider, model)
	}
	config := productDefaultWorkflowLLMConfig(context.Background())
	if config == nil || config.BuilderLLM == nil || config.BuilderLLM.Provider != "codex-cli" || config.BuilderLLM.ModelID != "gpt-5" {
		t.Fatalf("new workflow settings: %+v", config)
	}
	_ = env
}

// MEDIUM 4: settings and the account registry are read once and then served
// from memory; saves update the cache.
func TestProviderAccountsSettingsAndRegistryAreCached(t *testing.T) {
	env := newProviderAccountsEnv(t, `{"cursor-cli":{"available_to":"admins"}}`)
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "codex-cli", "display_name": "A", "credential": "k"})
	if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "admin", map[string]interface{}{"available_to": "all"}, map[string]string{"connectionID": "global:cursor-cli"}); w.Code != http.StatusNoContent {
		t.Fatalf("admin edit: %d", w.Code)
	}
	// The workspace goes away: turns still admit from memory.
	t.Setenv("WORKSPACE_API_URL", "http://127.0.0.1:1")
	providerConnectionsCache.Lock()
	providerConnectionsCache.url = "http://127.0.0.1:1"
	providerConnectionsCache.Unlock()
	providerAccountSettingsCache.Lock()
	providerAccountSettingsCache.url = "http://127.0.0.1:1"
	providerAccountSettingsCache.Unlock()
	if _, err := env.api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: "bob", WorkspacePath: "_users/bob/Chats/Code/projects/p"}, "cursor-cli", "global:cursor-cli"); err != nil {
		t.Fatalf("cached admin setting not used: %v", err)
	}
	if _, err := env.api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: "alice"}, "codex-cli", account.ID); err != nil {
		t.Fatalf("cached registry not used: %v", err)
	}
	// With no policy and no settings ever loaded, the server account needs no read.
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", "")
	providerAccountSettingsCache.Lock()
	providerAccountSettingsCache.loaded = false
	providerAccountSettingsCache.Unlock()
	if _, err := env.api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: "bob"}, "claude-code", "global:claude-code"); err != nil {
		t.Fatalf("no-config deployment failed a turn on an unreadable workspace: %v", err)
	}
}
