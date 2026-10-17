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

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// PLAT-683: only server-account tokens count toward a person's limit, and
// at the limit a new turn is refused on the server account (manual or
// scheduled) while the person's own account keeps working.
func TestTokenLimitsCountAndCapServerAccountsOnly(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	env.api.costLedger = ledger
	resetUsage := func() {
		sharedTokenUsageCache.Lock()
		sharedTokenUsageCache.byUser = map[string]cachedSharedTokenUsage{}
		sharedTokenUsageCache.Unlock()
	}
	resetUsage()
	t.Cleanup(resetUsage)

	// The admin sets Alice's daily limit through the user-update API.
	if w := env.do(t, env.api.handleAdminUpdateUser, http.MethodPut, "/api/admin/users/alice", "admin", map[string]interface{}{"token_limits": map[string]int64{"daily": 1000}}, map[string]string{"id": "alice"}); w.Code != http.StatusOK {
		t.Fatalf("set limit: %d %s", w.Code, w.Body.String())
	}
	own := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Alice Claude", "auth_method": "oauth_token", "credential": "dummy-private-token"})
	scope := providerAccountScope{Principal: "alice"}
	record := func(accountID string, prompt, completion int) {
		t.Helper()
		if err := ledger.Append(costledger.Entry{Timestamp: time.Now().UTC(), UserID: "alice", Scope: "chat", Provider: "claude-code", ModelID: "sonnet", AccountID: accountID, LLMCallCount: 1, PromptTokens: prompt, CompletionTokens: completion}); err != nil {
			t.Fatal(err)
		}
		resetUsage()
	}

	// Heavy use of her own account does not count.
	record(own.ID, 50000, 50000)
	if _, err := env.api.admitProviderAccount(context.Background(), scope, "claude-code", "global:claude-code"); err != nil {
		t.Fatalf("own-account usage counted toward the limit: %v", err)
	}

	// Server-account use past the limit refuses the next server turn...
	record("global:claude-code", 600, 500)
	_, err = env.api.admitProviderAccount(context.Background(), scope, "claude-code", serverDefaultConnectionID("claude-code"))
	if err == nil || !strings.Contains(err.Error(), "daily limit of 1,000 tokens") {
		t.Fatalf("server turn over the limit: %v", err)
	}
	// ...and a scheduled run on the server account, but never her own account.
	if err := env.api.scheduledRunTokenLimitRefusal(context.Background(), &ScheduleContext{OwnerUserID: "alice"}); err == nil {
		t.Fatal("scheduled run on the server account was not refused")
	}
	var ownCapabilities WorkflowCapabilities
	ownCapabilities.LLMConfig = &workflowtypes.PresetLLMConfig{SchemaVersion: 2, Mode: workflowtypes.LLMConfigModeExplicit, BuilderLLM: &workflowtypes.AgentLLMConfig{Provider: "claude-code", ModelID: "sonnet", ConnectionID: own.ID}}
	if err := env.api.scheduledRunTokenLimitRefusal(context.Background(), &ScheduleContext{OwnerUserID: "alice", Capabilities: ownCapabilities}); err != nil {
		t.Fatalf("scheduled run on her own account refused: %v", err)
	}
	if _, err := env.api.admitProviderAccount(context.Background(), scope, "claude-code", own.ID); err != nil {
		t.Fatalf("own account refused over the shared limit: %v", err)
	}
	// Bob has no limit.
	if _, err := env.api.admitProviderAccount(context.Background(), providerAccountScope{Principal: "bob"}, "claude-code", "global:claude-code"); err != nil {
		t.Fatalf("unlimited user refused: %v", err)
	}
	if usage := env.api.sharedAccountTokenUsageFor(directoryUserFor("alice", "", "")); usage.DailyUsed != 1100 || usage.State != "over" {
		t.Fatalf("usage shown: %+v", usage)
	}
}

// PLAT-693: each shared account has its own per-person default limit
// (Providers -> account -> Limits) that a person's override replaces; only
// that account's tokens count and only that account is refused, while the
// person's overall cap across all shared accounts still applies.
func TestTokenLimitsPerSharedAccount(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	env.api.costLedger = ledger
	resetUsage := func() {
		sharedTokenUsageCache.Lock()
		sharedTokenUsageCache.byUser = map[string]cachedSharedTokenUsage{}
		sharedTokenUsageCache.Unlock()
	}
	resetUsage()
	t.Cleanup(resetUsage)
	record := func(user, provider string, tokens int) {
		t.Helper()
		if err := ledger.Append(costledger.Entry{Timestamp: time.Now().UTC(), UserID: user, Scope: "chat", Provider: provider, ModelID: "m", AccountID: "global:" + provider, LLMCallCount: 1, PromptTokens: tokens}); err != nil {
			t.Fatal(err)
		}
		resetUsage()
	}
	admit := func(user, provider string) error {
		_, err := env.api.admitProviderAccount(context.Background(), providerAccountScope{Principal: user}, provider, "global:"+provider)
		return err
	}

	// Codex default: 1,000 a day per person; Alice's override: 5,000.
	if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/api/provider-connections/global:codex-cli", "admin", map[string]interface{}{"token_limits": map[string]int64{"daily": 1000}}, map[string]string{"connectionID": "global:codex-cli"}); w.Code != http.StatusNoContent {
		t.Fatalf("set account default: %d %s", w.Code, w.Body.String())
	}
	if w := env.do(t, env.api.handleAdminUpdateUser, http.MethodPut, "/api/admin/users/alice", "admin", map[string]interface{}{"account_token_limits": map[string]interface{}{"codex-cli": map[string]int64{"daily": 5000}}}, map[string]string{"id": "alice"}); w.Code != http.StatusOK {
		t.Fatalf("set override: %d %s", w.Code, w.Body.String())
	}

	// Bob over the Codex default: Codex is refused and named, Claude is not.
	record("bob", "codex-cli", 1200)
	if err := admit("bob", "codex-cli"); err == nil || !strings.Contains(err.Error(), "today's 1k tokens on the shared Codex account") {
		t.Fatalf("bob codex over the default: %v", err)
	}
	if err := admit("bob", "claude-code"); err != nil {
		t.Fatalf("another shared account refused: %v", err)
	}
	// Alice's override beats the default.
	record("alice", "codex-cli", 1200)
	if err := admit("alice", "codex-cli"); err != nil {
		t.Fatalf("override not applied: %v", err)
	}
	// Her overall cap still counts every shared account.
	if w := env.do(t, env.api.handleAdminUpdateUser, http.MethodPut, "/api/admin/users/alice", "admin", map[string]interface{}{"token_limits": map[string]int64{"daily": 2000}}, map[string]string{"id": "alice"}); w.Code != http.StatusOK {
		t.Fatalf("set overall cap: %d %s", w.Code, w.Body.String())
	}
	record("alice", "claude-code", 900)
	if err := admit("alice", "claude-code"); err == nil || !strings.Contains(err.Error(), "daily limit of 2,000 tokens on the shared accounts") {
		t.Fatalf("overall cap: %v", err)
	}
	usage := env.api.sharedAccountTokenUsageFor(directoryUserFor("alice", "", ""))
	codex := usage.Accounts["codex-cli"]
	if codex == nil || codex.DailyUsed != 1200 || codex.DailyLimit != 5000 || codex.Default == nil || codex.Default.Daily != 1000 || usage.DailyUsed != 2100 || usage.State != "over" {
		t.Fatalf("usage shown: %+v codex=%+v", usage, codex)
	}
}

// PLAT-693 (owner decision 2026-10-07): a person's account override may be
// unlimited (-1) even when the account has a default, and the admin write path
// keeps it; an empty (0) override field still falls back to the default.
func TestAccountTokenOverrideUnlimitedBeatsDefault(t *testing.T) {
	rec := &UserRecord{ID: "alice"}
	if err := applyAccountTokenLimits(rec, map[string]*UserTokenLimits{"codex-cli": {Daily: TokenLimitUnlimited}}); err != nil {
		t.Fatal(err)
	}
	override := rec.AccountTokenLimits["codex-cli"]
	if override == nil || override.Daily != TokenLimitUnlimited {
		t.Fatalf("unlimited override not saved: %+v", rec.AccountTokenLimits)
	}
	defaults := &UserTokenLimits{Daily: 1000, Weekly: 5000}
	if got := effectiveAccountTokenLimits(defaults, override); got == nil || got.Daily != 0 || got.Weekly != 5000 {
		t.Fatalf("unlimited daily + empty weekly: %+v, want daily unlimited and weekly 5000 from the default", got)
	}
	if got := effectiveAccountTokenLimits(defaults, &UserTokenLimits{Daily: TokenLimitUnlimited, Weekly: TokenLimitUnlimited}); got != nil {
		t.Fatalf("fully unlimited override: %+v, want no limit", got)
	}
}

// PLAT-698: a Slack channel turn runs as a bot identity with no user record.
// Its shared-account tokens count toward the owner of the workflow it answers
// for, and it is refused (with a message for the channel) when that owner is
// over a limit. A DM turn still counts to the DM person; a personal account
// never counts.
func TestSlackChannelBotTurnCountsTowardTargetOwner(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	env.api.costLedger = ledger
	resetUsage := func() {
		sharedTokenUsageCache.Lock()
		sharedTokenUsageCache.byUser = map[string]cachedSharedTokenUsage{}
		sharedTokenUsageCache.Unlock()
	}
	resetUsage()
	t.Cleanup(resetUsage)
	if w := env.do(t, env.api.handleAdminUpdateUser, http.MethodPut, "/api/admin/users/alice", "admin", map[string]interface{}{"token_limits": map[string]int64{"daily": 1000}}, map[string]string{"id": "alice"}); w.Code != http.StatusOK {
		t.Fatalf("set limit: %d %s", w.Code, w.Body.String())
	}
	own := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Alice Claude", "auth_method": "oauth_token", "credential": "dummy-private-token"})

	// A channel turn on Alice's workflow W, as the route's bot identity.
	const bot = "bot-slack-0123abcd"
	botCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: bot, Username: bot, Provider: "bot_route", BotRouteGrant: "run", BotRouteWorkflowID: "wf-w", BotRouteWorkspacePath: "Workflow/w"})
	owner := botRouteTokenOwner(botCtx, bot, "Workflow/w")
	if owner != "alice" {
		t.Fatalf("bot turn owner = %q, want alice", owner)
	}
	record := func(user, billing, accountID string, tokens int) {
		t.Helper()
		if err := ledger.Append(costledger.Entry{Timestamp: time.Now().UTC(), UserID: user, BillingUserID: billing, Scope: "chat", SourcePlatform: "slack", Provider: "claude-code", ModelID: "sonnet", AccountID: accountID, LLMCallCount: 1, PromptTokens: tokens}); err != nil {
			t.Fatal(err)
		}
		resetUsage()
	}
	// The bot's tokens on her personal account never count; on the shared
	// account they count to her (the bot identity stays on the row).
	record(bot, owner, own.ID, 50000)
	record(bot, owner, "global:claude-code", 1100)
	usage := env.api.sharedAccountTokenUsageFor(directoryUserFor("alice", "", ""))
	if usage.DailyUsed != 1100 || usage.DailyViaBot != 1100 || usage.State != "over" {
		t.Fatalf("alice usage: %+v", usage)
	}
	// The next channel turn is refused with the owner's limit, named for the channel.
	scope := providerAccountScope{Principal: bot, WorkspacePath: "Workflow/w"}
	_, err = env.api.admitProviderAccount(botCtx, scope, "claude-code", serverDefaultConnectionID("claude-code"))
	if err == nil || !strings.Contains(err.Error(), "The owner of workflow Weekly report has used their daily limit of 1,000 tokens") || !strings.Contains(err.Error(), "resets 00:00 UTC") {
		t.Fatalf("channel turn over the owner's limit: %v", err)
	}
	// A Slack DM runs as the person (Bob) and counts to Bob, not to the owner.
	record("bob", "", "global:claude-code", 300)
	if bob := env.api.sharedAccountTokenUsageFor(directoryUserFor("bob", "", "")); bob.DailyUsed != 300 || bob.DailyViaBot != 0 {
		t.Fatalf("bob usage: %+v", bob)
	}
	if again := env.api.sharedAccountTokenUsageFor(directoryUserFor("alice", "", "")); again.DailyUsed != 1100 {
		t.Fatalf("a DM turn counted to the owner: %+v", again)
	}
}

// Cached context counts at 10% for limits while the cost ledger retains its
// actual tokens. Cover both inclusive/exclusive input reporting and a bot turn
// billed to the same person, through admission and both usage surfaces.
func TestTokenLimitsDiscountCacheReads(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	env.api.costLedger = ledger
	resetUsage := func() {
		sharedTokenUsageCache.Lock()
		sharedTokenUsageCache.byUser = map[string]cachedSharedTokenUsage{}
		sharedTokenUsageCache.Unlock()
	}
	resetUsage()
	t.Cleanup(resetUsage)
	if w := env.do(t, env.api.handleAdminUpdateUser, http.MethodPut, "/api/admin/users/alice", "admin", map[string]any{"token_limits": map[string]int64{"daily": 1500, "weekly": 1500}}, map[string]string{"id": "alice"}); w.Code != http.StatusOK {
		t.Fatalf("set overall limit: %d %s", w.Code, w.Body.String())
	}
	for _, provider := range []string{"claude-code", "codex-cli"} {
		if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/api/provider-connections/global:"+provider, "admin", map[string]any{"token_limits": map[string]int64{"daily": 600, "weekly": 600}}, map[string]string{"connectionID": "global:" + provider}); w.Code != http.StatusNoContent {
			t.Fatalf("set account limit: %d %s", w.Code, w.Body.String())
		}
		entry := costledger.Entry{EventID: provider, Timestamp: time.Now().UTC(), UserID: "alice", Scope: "chat", Provider: provider, ModelID: "cached-model", AccountID: "global:" + provider, LLMCallCount: 1, PromptTokens: 100, CacheReadTokens: 1000, CacheWriteTokens: 200, CompletionTokens: 100, TotalCostUSD: 1.25, BillingBasis: "provider_actual", OperationMetadata: map[string]any{"prompt_tokens_include_cache": false}}
		if provider == "codex-cli" {
			entry.PromptTokens = 1300 // fresh 100 + cached read 1000 + cache write 200
			entry.OperationMetadata["prompt_tokens_include_cache"] = true
		} else {
			entry.UserID, entry.BillingUserID = "channel-bot", "alice"
		}
		if err := ledger.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	person := directoryUserFor("alice", "", "")
	usage := env.api.sharedAccountTokenUsageFor(person)
	if usage.DailyUsed != 1000 || usage.WeeklyUsed != 1000 || usage.DailyViaBot != 500 || usage.WeeklyViaBot != 500 || usage.DailyCacheReadTokens != 2000 || usage.WeeklyCacheReadTokens != 2000 || !strings.Contains(usage.CountingNote, "10%") {
		t.Fatalf("discounted usage/bot/cache breakdown: %+v", usage)
	}
	for _, provider := range []string{"claude-code", "codex-cli"} {
		account := usage.Accounts[provider]
		if account.DailyUsed != 500 || account.WeeklyUsed != 500 || account.DailyCacheReadTokens != 1000 || account.WeeklyCacheReadTokens != 1000 {
			t.Fatalf("account usage: %+v", account)
		}
		if _, err := env.api.admitProviderAccount(context.Background(), providerAccountScope{Principal: "alice"}, provider, "global:"+provider); err != nil {
			t.Fatalf("cache reads wrongly exhausted %s: %v", provider, err)
		}
	}
	if external := env.api.tokenLimitUsageOf(person, ""); external.DailyUsed != usage.DailyUsed || external.DailyCacheReadTokens != 2000 || external.CountingNote != usage.CountingNote {
		t.Fatalf("MCP/website usage differ: %+v", external)
	}
	day, _, week, _ := tokenLimitWindows(time.Now())
	raw, err := ledger.AccountTokens("alice", "global:", day, week)
	if err != nil || raw.Day != 2800 || raw.Week != 2800 || raw.ForTokenLimits().Day != 1000 {
		t.Fatalf("raw ledger usage changed: %+v, %v", raw, err)
	}
	models, err := ledger.AccountModelTokens("alice", "global:", day, week)
	if err != nil || models["global:codex-cli"]["cached-model"].ForTokenLimits().Day != 500 {
		t.Fatalf("model limit counting differs: %+v, %v", models, err)
	}
	summary, err := ledger.Summarize("", "")
	if err != nil || summary.Total.InputTokens != 2600 || summary.Total.CompletionTokens != 200 || summary.Total.CacheReadTokens != 2000 || summary.Total.CacheWriteTokens != 400 || summary.Total.TotalCostUSD != 2.5 {
		t.Fatalf("discount changed actual costs/tokens: %+v, %v", summary, err)
	}
	w := httptest.NewRecorder()
	env.api.externalTokenUsageRange(w, []*UserRecord{person}, day.Format("2006-01-02"), day.Format("2006-01-02"))
	var ranged struct {
		People []struct {
			Tokens          int64            `json:"tokens"`
			CacheReadTokens int64            `json:"cache_read_tokens"`
			ByAccount       map[string]int64 `json:"by_account"`
		} `json:"people"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ranged); err != nil || len(ranged.People) != 1 || ranged.People[0].Tokens != usage.DailyUsed || ranged.People[0].CacheReadTokens != 2000 || ranged.People[0].ByAccount["global:codex-cli"] != 500 {
		t.Fatalf("date-range usage differs: %s (%v)", w.Body.String(), err)
	}
}
