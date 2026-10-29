package server

import (
	"context"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The usage chip is the one place showing plan limits (owner, 2026-10-29). With nothing reported by the chat,
// the server asks the provider, but only for the chat's own provider and account: the request runs in that
// account's HOME, never for an account the chat's owner may not run on or for another provider, and is asked
// once per account within the cache time.
func TestChatUsageProbeAsksOnlyTheChatsAccount(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Alice Claude", "auth_method": "cli_login"})
	wantHome, _ := providerConnectionHome(account.ID)
	reset := func() {
		chatUsageProbes.mu.Lock()
		chatUsageProbes.results, chatUsageProbes.running = map[string]chatUsageProbeResult{}, map[string]bool{}
		chatUsageProbes.mu.Unlock()
	}
	reset()
	original := readNativeAccountUsage
	t.Cleanup(func() { readNativeAccountUsage = original; reset() })
	calls := 0
	readNativeAccountUsage = func(_ context.Context, provider string, environment []string) (llmtypes.AccountUsage, error) {
		calls++
		if provider != "claude-code" || envValue(environment, "HOME") != wantHome || envValue(environment, "AUTH_SECRET") != "" {
			t.Fatal("quota request ran outside the chat account's environment")
		}
		return llmtypes.AccountUsage{Windows: []llmtypes.AccountUsageWindow{{Label: "5-hour limit", UsedPercent: 42}, {Label: "Weekly · Opus", UsedPercent: 90}}}, nil
	}

	if limits := env.api.probedChatLimits("bob", "claude-code", account.ID); limits != nil || calls != 0 {
		t.Fatalf("asked about a private account for someone it is not shared with: %v", limits)
	}
	if limits := env.api.probedChatLimits("alice", "codex-cli", account.ID); limits != nil || calls != 0 {
		t.Fatalf("asked another provider about this account: %v", limits)
	}
	limits := env.api.probedChatLimits("alice", "claude-code", account.ID)
	if calls != 1 || len(limits) != 1 || limits[0].Label != "5h" || limits[0].UsedPercent != 42 {
		t.Fatalf("chat owner's own account: calls=%d limits=%v", calls, limits)
	}
	if again := env.api.probedChatLimits("alice", "claude-code", account.ID); calls != 1 || len(again) != 1 {
		t.Fatalf("asked the provider again within the cache time: calls=%d", calls)
	}
}
