package server

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

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
