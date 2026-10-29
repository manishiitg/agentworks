package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

func TestAllowedModelsListLogic(t *testing.T) {
	if got := normalizeAllowedModels([]string{" gpt-5.3-codex ", "", "GPT-5.3-CODEX", "gpt-5.5"}); !reflect.DeepEqual(got, []string{"gpt-5.3-codex", "gpt-5.5"}) {
		t.Fatalf("normalize: %v", got)
	}
	if normalizeAllowedModels([]string{"", "  "}) != nil {
		t.Fatal("an empty list must stay nil (every model)")
	}
	if !modelAllowed(nil, "anything") || !modelAllowed([]string{"a"}, " A ") || modelAllowed([]string{"a"}, "b") {
		t.Fatal("modelAllowed")
	}
	if _, err := validateAllowedModels(make([]string, 0)); err != nil {
		t.Fatal(err)
	}
	long := make([]string, maxAllowedModels+1)
	for i := range long {
		long[i] = "m" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + strings.Repeat("y", i/26)
	}
	if _, err := validateAllowedModels(long); err == nil {
		t.Fatal("too many models accepted")
	}
	if _, err := validateAllowedModels([]string{strings.Repeat("x", maxAllowedModelIDSize+1)}); err == nil {
		t.Fatal("an oversized model id accepted")
	}
	err := disallowedModelError("gpt-5.5", "Team Codex", []string{"gpt-5.3-codex", "gpt-5.4"})
	if err.Error() != "gpt-5.5 is not allowed on Team Codex; allowed: gpt-5.3-codex, gpt-5.4" {
		t.Fatalf("message: %v", err)
	}
}

// A deployment that supplies its own Pi models has a key for those only, so they are the server account's list: a chat
// saved on another Pi model is settled on the deployment's model before its runtime is bound. Settled later (the
// running CLI on one model, the request still naming another), live input was refused as a runtime change and a
// steered message sat in the queue.
func TestDeploymentPiModelsAreTheServerAccountsAllowedList(t *testing.T) {
	newProviderAccountsEnv(t, "")
	dir := t.TempDir()
	models := `{"providers":{"gateway":{"baseUrl":"https://gateway.example.com/v1","api":"openai-completions","apiKey":"$GATEWAY_API_KEY","models":[{"id":"model-a","name":"Model A"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(picli.EnvPiAgentTemplateDir, dir)
	ctx := context.Background()
	if model, changed, err := resolveAccountModel(ctx, "pi-cli", "", "google/gemini-3.8-flash"); err != nil || !changed || model != "gateway/model-a" {
		t.Fatalf("a Pi model the deployment has no key for: %q changed=%v err=%v", model, changed, err)
	}
	if model, changed, _ := resolveAccountModel(ctx, "pi-cli", "", "gateway/model-a"); changed || model != "gateway/model-a" {
		t.Fatalf("the deployment's model must stay: %q %v", model, changed)
	}
	query := QueryRequest{Provider: "pi-cli", ModelID: "google/gemini-3.8-flash"}
	if err := constrainProductChatModel(ctx, &query); err != nil || query.ModelID != "gateway/model-a" {
		t.Fatalf("the chat's request must name the deployment's model: %q %v", query.ModelID, err)
	}
}

func patchAllowed(env *providerAccountsEnv, t *testing.T, user, id string, models interface{}) int {
	t.Helper()
	w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", user, map[string]interface{}{"allowed_models": models}, map[string]string{"connectionID": id})
	return w.Code
}

func TestAllowedModelsServerAccountAdminOnlyAndEnforced(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	t.Setenv("CODEX_API_KEY", "server-codex-key")
	ctx := context.Background()

	// Default: every model.
	if err := checkAccountModel(ctx, "codex-cli", "global:codex-cli", "gpt-5.5"); err != nil {
		t.Fatalf("no list must allow every model: %v", err)
	}
	// Members cannot restrict the shared account; admins can.
	if code := patchAllowed(env, t, "bob", "global:codex-cli", []string{"gpt-5.3-codex"}); code != http.StatusForbidden {
		t.Fatalf("member restricted the server account: %d", code)
	}
	if code := patchAllowed(env, t, "admin", "global:codex-cli", []string{"gpt-5.3-codex"}); code != http.StatusNoContent {
		t.Fatalf("admin restriction: %d", code)
	}
	// Everyone sees it on the account view.
	view, ok := findAccountView(env.list(t, "bob", ""), "global:codex-cli")
	if !ok || !reflect.DeepEqual(view.AllowedModels, []string{"gpt-5.3-codex"}) {
		t.Fatalf("view: %+v", view)
	}
	// The account's server setting also covers a turn that names no account.
	for _, id := range []string{"global:codex-cli", "", "server-default:codex-cli"} {
		if err := checkAccountModel(ctx, "codex-cli", id, "gpt-5.5"); err == nil || !strings.Contains(err.Error(), "gpt-5.5 is not allowed on Admin-managed account; allowed: gpt-5.3-codex") {
			t.Fatalf("id %q: disallowed model not refused: %v", id, err)
		}
		if err := checkAccountModel(ctx, "codex-cli", id, "gpt-5.3-codex"); err != nil {
			t.Fatalf("id %q: allowed model refused: %v", id, err)
		}
	}
	// Another provider's account is unaffected.
	if err := checkAccountModel(ctx, "claude-code", "global:claude-code", "sonnet"); err != nil {
		t.Fatal(err)
	}
	// A saved selection falls back to the first allowed model.
	if model, changed, err := resolveAccountModel(ctx, "codex-cli", "", "gpt-5.5"); err != nil || !changed || model != "gpt-5.3-codex" {
		t.Fatalf("fallback: %q %v %v", model, changed, err)
	}
	if model, changed, _ := resolveAccountModel(ctx, "codex-cli", "", ""); !changed || model != "gpt-5.3-codex" {
		t.Fatalf("an empty model must resolve to the first allowed: %q %v", model, changed)
	}
	if model, changed, _ := resolveAccountModel(ctx, "codex-cli", "", "gpt-5.3-codex"); changed || model != "gpt-5.3-codex" {
		t.Fatalf("an allowed model must stay: %q %v", model, changed)
	}

	// Product defaults follow the list.
	t.Setenv("AGENTWORKS_PRODUCT_DEFAULTS", `{"code":{"provider":"codex-cli","model":"gpt-5.5"}}`)
	defaults, err := effectiveProductDefaults(ctx)
	if err != nil || defaults["code"].Model != "gpt-5.3-codex" {
		t.Fatalf("product default not constrained: %+v %v", defaults, err)
	}

	// Workflow roles and delegation tiers: a saved disallowed model is replaced.
	cfg := workshopConvertAgentLLMConfig(&workflowtypes.AgentLLMConfig{Provider: "codex-cli", ModelID: "gpt-5.5", PublishedLLMID: "pub-1"})
	if cfg.ModelID != "gpt-5.3-codex" || cfg.PublishedLLMID != "" {
		t.Fatalf("workflow role: %+v", cfg)
	}
	tiers := constrainDelegationTierConfig(ctx, &virtualtools.DelegationTierConfig{
		High:   &virtualtools.TierModel{Provider: "codex-cli", ModelID: "gpt-5.5"},
		Medium: &virtualtools.TierModel{Provider: "claude-code", ModelID: "sonnet"},
		Custom: map[string]*virtualtools.CustomTierModel{"x": {Provider: "codex-cli", ModelID: "gpt-5.4"}},
	})
	if tiers.High.ModelID != "gpt-5.3-codex" || tiers.Medium.ModelID != "sonnet" || tiers.Custom["x"].ModelID != "gpt-5.3-codex" {
		t.Fatalf("tiers: %+v", tiers)
	}

	// Clearing the list restores every model.
	if code := patchAllowed(env, t, "admin", "global:codex-cli", []string{}); code != http.StatusNoContent {
		t.Fatalf("clear: %d", code)
	}
	if err := checkAccountModel(ctx, "codex-cli", "global:codex-cli", "gpt-5.5"); err != nil {
		t.Fatalf("cleared list still restricts: %v", err)
	}
	// An empty request is not an edit.
	w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "admin", map[string]interface{}{}, map[string]string{"connectionID": "global:codex-cli"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty server account patch: %d", w.Code)
	}
}

func TestAllowedModelsPersonalAccountOwnerOnly(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	ctx := context.Background()
	account := env.addAccount(t, "alice", map[string]interface{}{
		"provider": "codex-cli", "display_name": "Alice Codex", "auth_method": "cli_login",
		"allowed_models": []string{"gpt-5.4", " gpt-5.4 "},
		"sharing":        map[string]interface{}{"mode": "shared", "users": []string{"bob"}},
	})
	if !reflect.DeepEqual(account.AllowedModels, []string{"gpt-5.4"}) {
		t.Fatalf("created list: %v", account.AllowedModels)
	}
	// Another user cannot change it; neither can an unrelated admin (the
	// account is private to its owner, admins included).
	for _, user := range []string{"bob", "carol", "admin"} {
		if code := patchAllowed(env, t, user, account.ID, []string{"gpt-5.5"}); code == http.StatusNoContent {
			t.Fatalf("%s changed alice's account", user)
		}
	}
	if err := checkAccountModel(ctx, "codex-cli", account.ID, "gpt-5.5"); err == nil || !strings.Contains(err.Error(), "not allowed on Alice Codex; allowed: gpt-5.4") {
		t.Fatalf("personal account list not enforced: %v", err)
	}
	// The person it is shared with sees the list.
	view, ok := findAccountView(env.list(t, "bob", ""), account.ID)
	if !ok || !reflect.DeepEqual(view.AllowedModels, []string{"gpt-5.4"}) {
		t.Fatalf("shared view: %+v", view)
	}
	// The owner edits and clears it.
	if code := patchAllowed(env, t, "alice", account.ID, []string{"gpt-5.5", "gpt-5.4"}); code != http.StatusNoContent {
		t.Fatalf("owner edit: %d", code)
	}
	if model, changed, _ := resolveAccountModel(ctx, "codex-cli", account.ID, "gpt-5.3"); !changed || model != "gpt-5.5" {
		t.Fatalf("fallback on the personal account: %q %v", model, changed)
	}
	if code := patchAllowed(env, t, "alice", account.ID, []string{}); code != http.StatusNoContent {
		t.Fatalf("owner clear: %d", code)
	}
	if err := checkAccountModel(ctx, "codex-cli", account.ID, "gpt-5.3"); err != nil {
		t.Fatalf("cleared: %v", err)
	}
	// Editing something else keeps the list.
	patchAllowed(env, t, "alice", account.ID, []string{"gpt-5.4"})
	w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "alice", map[string]interface{}{"display_name": "Renamed"}, map[string]string{"connectionID": account.ID})
	if w.Code != http.StatusNoContent {
		t.Fatalf("rename: %d", w.Code)
	}
	if err := checkAccountModel(ctx, "codex-cli", account.ID, "gpt-5.5"); err == nil || !strings.Contains(err.Error(), "Renamed") {
		t.Fatalf("a rename dropped the list: %v", err)
	}
}

func TestAllowedModelsProductChatTurn(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	_ = env
	if code := patchAllowed(env, t, "admin", "global:codex-cli", []string{"gpt-5.3-codex"}); code != http.StatusNoContent {
		t.Fatalf("setup: %d", code)
	}
	ctx := context.Background()

	// A disallowed model, whether picked or re-sent from a saved setting, runs on the first allowed one: the turn never fails (it used to 422).
	for _, model := range []string{"gpt-5.4", "gpt-5.5"} {
		query := QueryRequest{Provider: "codex-cli", ModelID: model, LLMConfig: &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "codex-cli", ModelID: model}}}
		if err := constrainProductChatModel(ctx, &query); err != nil || query.ModelID != "gpt-5.3-codex" || query.LLMConfig.Primary.ModelID != "gpt-5.3-codex" {
			t.Fatalf("%s: %v %+v", model, err, query)
		}
	}
	// An allowed model is untouched.
	query := QueryRequest{Provider: "codex-cli", ModelID: "gpt-5.3-codex"}
	if err := constrainProductChatModel(ctx, &query); err != nil || query.ModelID != "gpt-5.3-codex" {
		t.Fatalf("allowed pick: %v %+v", err, query)
	}
	// No model named: the first allowed one.
	query = QueryRequest{Provider: "codex-cli"}
	if err := constrainProductChatModel(ctx, &query); err != nil || query.ModelID != "gpt-5.3-codex" {
		t.Fatalf("no model: %v %+v", err, query)
	}
	// An account without a list changes nothing.
	query = QueryRequest{Provider: "claude-code", ModelID: "sonnet"}
	if err := constrainProductChatModel(ctx, &query); err != nil || query.ModelID != "sonnet" {
		t.Fatalf("no list: %v %+v", err, query)
	}
}

// The picker offers only what the account allows: with the shared account limited to one model the others are flagged
// for the browser to hide (they ran on the allowed model anyway, 2026-10-15), and stay in the list because the browser
// writes the whole shared list back. A provider with no list keeps every model; the flag is never stored.
func TestPublishedLLMsFlagTheModelsTheAccountDoesNotAllow(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	t.Setenv("CODEX_API_KEY", "server-codex-key")
	if code := patchAllowed(env, t, "admin", "global:codex-cli", []string{"gpt-5.3-codex"}); code != http.StatusNoContent {
		t.Fatalf("admin restriction: %d", code)
	}
	all := []StoredPublishedLLM{
		{ID: "a", Name: "Allowed", Provider: "codex-cli", ModelID: "gpt-5.3-codex"},
		{ID: "b", Name: "Other", Provider: "codex-cli", ModelID: "gpt-5.5"},
		{ID: "c", Name: "Claude", Provider: "claude-code", ModelID: "sonnet"},
	}
	got := markPublishedLLMsNotAllowed(context.Background(), "bob", all)
	if len(got) != 3 || got[0].NotAllowed || !got[1].NotAllowed || got[2].NotAllowed {
		t.Fatalf("flags = %+v, want only the other codex model flagged and every entry kept", got)
	}
	if saved := sanitizePersistedPublishedLLMs(got); len(saved) != 3 || saved[1].NotAllowed {
		t.Fatalf("the per-person flag must never be stored: %+v", saved)
	}
}

// The model pickers read /api/llm-config/providers/{provider}/models: with the shared account limited to one model a
// member is offered just that one, an administrator asking for the full catalog (the Accounts page) still gets every
// model (2026-10-15: the picker listed eight Pi models while one was allowed).
func TestProviderModelsListOnlyTheAllowedModelsExceptForAnAdminsFullCatalog(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	t.Setenv("CODEX_API_KEY", "server-codex-key")
	if code := patchAllowed(env, t, "admin", "global:codex-cli", []string{"gpt-5.3-codex"}); code != http.StatusNoContent {
		t.Fatalf("admin restriction: %d", code)
	}
	models := func(user, query string) []string {
		w := env.do(t, env.api.handleGetProviderModels, http.MethodGet, "/api/llm-config/providers/codex-cli/models"+query, user, nil, map[string]string{"provider": "codex-cli"})
		var resp dynamicModelsResponse
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &resp) != nil {
			t.Fatalf("%s %s: %d %s", user, query, w.Code, w.Body.String())
		}
		ids := []string{}
		for _, model := range resp.Models {
			ids = append(ids, model.ModelID)
		}
		return ids
	}
	if got := models("bob", ""); len(got) != 1 || got[0] != "gpt-5.3-codex" {
		t.Fatalf("a member's picker = %v, want only the allowed model", got)
	}
	if got := models("bob", "?full=true"); len(got) != 1 || got[0] != "gpt-5.3-codex" {
		t.Fatalf("a member asking for the full catalog = %v, want only the allowed model", got)
	}
	if got := models("admin", "?full=true"); len(got) < 2 {
		t.Fatalf("an admin's full catalog = %v, want every model", got)
	}
}

// The Code/Crew model picker is built from the product profile's provider options: with the shared account limited to
// one model the profile a member reads offers just that model on the engine (2026-10-15: eight Pi models were shown
// while one was allowed), and the registry's own profile is left untouched.
func TestProfileModelsAreLimitedToTheAccountsAllowedModels(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	t.Setenv("CODEX_API_KEY", "server-codex-key")
	if code := patchAllowed(env, t, "admin", "global:codex-cli", []string{"gpt-5.3-codex"}); code != http.StatusNoContent {
		t.Fatalf("admin restriction: %d", code)
	}
	profile := agentprofiles.Profile{}
	profile.Runtime.ProviderOptions = []agentprofiles.ProviderOption{
		{ID: "codex-cli", Provider: "codex-cli", ModelID: "gpt-5.5"},
		{ID: "claude-code", Provider: "claude-code", ModelID: "sonnet"},
	}
	got := limitProfileModelsToAccount(context.Background(), "bob", profile)
	if !reflect.DeepEqual(got.Runtime.ProviderOptions[0].Models, []string{"gpt-5.3-codex"}) || len(got.Runtime.ProviderOptions[1].Models) != 0 {
		t.Fatalf("limited options = %+v, want codex narrowed to its allowed model and claude untouched", got.Runtime.ProviderOptions)
	}
	if len(profile.Runtime.ProviderOptions[0].Models) != 0 {
		t.Fatal("the shared profile was edited")
	}
}

// The model pickers list the provider manifest's catalog: with the shared account limited to one model a member's
// manifest carries just that model for the provider (2026-10-15: eight Pi models were listed), other providers are
// untouched, and the shared metadata entries are not edited.
func TestManifestModelsAreLimitedToTheAccountsAllowedModels(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	t.Setenv("CODEX_API_KEY", "server-codex-key")
	if code := patchAllowed(env, t, "admin", "global:codex-cli", []string{"gpt-5.3-codex"}); code != http.StatusNoContent {
		t.Fatalf("admin restriction: %d", code)
	}
	shared := []*llmtypes.ModelMetadata{{Provider: "codex-cli", ModelID: "gpt-5.3-codex"}, {Provider: "codex-cli", ModelID: "gpt-5.5"}}
	entries := []providerManifestEntry{
		{ID: "codex-cli", Models: shared},
		{ID: "claude-code", Models: []*llmtypes.ModelMetadata{{Provider: "claude-code", ModelID: "sonnet"}}},
	}
	got := limitManifestModelsToAccount(sharedSecretsRequest(http.MethodGet, "/api/llm-config/providers", "bob", nil), entries)
	if len(got[0].Models) != 1 || got[0].Models[0].ModelID != "gpt-5.3-codex" || len(got[1].Models) != 1 {
		t.Fatalf("manifest models = %+v / %+v, want codex narrowed to its allowed model and claude untouched", got[0].Models, got[1].Models)
	}
	if len(shared) != 2 {
		t.Fatal("the shared metadata slice was edited")
	}
}
