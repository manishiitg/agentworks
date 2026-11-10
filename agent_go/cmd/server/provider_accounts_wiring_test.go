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

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	unifiedevents "github.com/manishiitg/mcpagent/events"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// Wiring of provider-account rules into the query, retained-CLI, live-input,
// scheduled-run and cost paths.

// setWorkflowW rewrites W's manifest: Bob is an editor and W's Builder model
// runs on connectionID.
func (e *providerAccountsEnv) setWorkflowW(t *testing.T, connectionID string) {
	t.Helper()
	manifest := map[string]interface{}{
		"id": "wf-w", "label": "Weekly report", "created_by": "alice",
		"access": map[string]interface{}{"owners": []string{"alice", "bob"}, "readers": []string{}},
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
func (e *providerAccountsEnv) queryToolsMode(t *testing.T, user, sessionID, root, projectID, accountID string) (string, int) {
	t.Helper()
	decided := ""
	e.api.internalAgentToolsModeDecided = func(_ string, mode string) bool {
		decided = normalizeAgentToolsMode(mode)
		return true
	}
	req := QueryRequest{
		Query: "Continue", AgentMode: "multi-agent", AgentProfileID: crewProfileID, AgentProfileConversationKey: projectID, SelectedFolder: root, ConnectionID: accountID, Provider: "claude-code", ModelID: "sonnet", AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Fixture Crew"},
		// A current Crew turn selects the account explicitly.
		LLMConfig: &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "claude-code", ModelID: "sonnet"}},
	}
	w := httptest.NewRecorder()
	httpReq := sharedSecretsRequest(http.MethodPost, "/api/query", user, req)
	httpReq.Header.Set("X-Session-ID", sessionID)
	e.api.handleQuery(w, httpReq)
	if w.Code != http.StatusNoContent && w.Code != http.StatusForbidden {
		t.Logf("current Crew query HTTP%d: %s", w.Code, w.Body.String())
	}
	return decided, w.Code
}

// A shared account must keep native tools, and its current sharing still
// decides admission. Use current owned Crews, not retired workflow/Code chats.
func TestProviderAccountsSharedAccountAndCrewTurnsAreFull(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"other","username":"other","can_create":true,"products":["work"]}]}`)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "provider-query-fixture")
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "claude-code")
	t.Setenv("ALLOW_PERSONAL_PROVIDER_CONNECTIONS", "")
	t.Setenv("LLM_CONFIG_LOCKED", "")
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", "")
	var err error
	f.api.chatStore, err = chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := &providerAccountsEnv{api: f.api}
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "other-team", ProjectID: "other-team", OwnerID: "other", Shared: true}); err != nil {
		t.Fatal(err)
	}
	f.write(t, "Crew/other-team/.fixture", "")
	f.write(t, "_system/crew-data/other-team/product.json", `{"schema_version":1,"product":"work","id":"other-team","title":"Other team","session_id":"other-main"}`)
	f.write(t, "_system/crew-data/other-team/workflow.json", `{"id":"other-team","created_by":"other"}`)
	account := env.addAccount(t, "owner", map[string]interface{}{"provider": "claude-code", "display_name": "Shared coding account", "auth_method": "cli_login", "sharing": map[string]interface{}{"mode": "shared", "users": []string{"other"}}})
	for _, turn := range []struct{ user, root, project string }{{"owner", externalCommandCrewRoot, "team"}, {"other", "Crew/other-team", "other-team"}} {
		if mode, code := env.queryToolsMode(t, turn.user, turn.user+"-builder", turn.root, turn.project, account.ID); mode != "full" {
			t.Fatalf("current Crew turn as %s decided %q (HTTP%d)", turn.user, mode, code)
		}
	}
	if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "owner", map[string]interface{}{"sharing": map[string]interface{}{"mode": "private"}}, map[string]string{"connectionID": account.ID}); w.Code != http.StatusNoContent {
		t.Fatalf("unshare: %d %s", w.Code, w.Body.String())
	}
	if mode, code := env.queryToolsMode(t, "other", "other-after-revoke", "Crew/other-team", "other-team", account.ID); code != http.StatusForbidden || mode != "" {
		t.Fatalf("revoked account admitted: HTTP%d mode=%q", code, mode)
	}
}

func TestProviderAccountsScheduledScopes(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	setDirectoryAdmin(t, "alice", true) // workflow shares are admin-only (PLAT-715)
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Alice Claude", "auth_method": "cli_login", "sharing": map[string]interface{}{"mode": "shared", "workflows": []string{"wf-w"}}})
	env.setWorkflowW(t, account.ID)

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
        {"id":"alice","username":"alice","can_create":true,"crew_reviewer":true},
        {"id":"bob","username":"bob","can_create":true,"crew_reviewer":true},
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
