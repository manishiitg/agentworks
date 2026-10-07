package server

import (
	"context"
	"net/http"
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
