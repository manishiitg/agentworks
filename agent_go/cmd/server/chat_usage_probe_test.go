package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

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
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	readNativeAccountUsage = func(_ context.Context, provider string, environment []string) (llmtypes.AccountUsage, error) {
		calls.Add(1)
		close(started)
		<-release
		if provider != "claude-code" || envValue(environment, "HOME") != wantHome || envValue(environment, "AUTH_SECRET") != "" {
			t.Error("quota request ran outside the chat account's environment")
		}
		return llmtypes.AccountUsage{Windows: []llmtypes.AccountUsageWindow{{Label: "5-hour limit", UsedPercent: 42}, {Label: "Weekly · Opus", UsedPercent: 90}}}, nil
	}

	if limits := env.api.probedChatLimits("bob", "claude-code", account.ID); limits != nil || calls.Load() != 0 {
		t.Fatalf("asked about a private account for someone it is not shared with: %v", limits)
	}
	if limits := env.api.probedChatLimits("alice", "codex-cli", account.ID); limits != nil || calls.Load() != 0 {
		t.Fatalf("asked another provider about this account: %v", limits)
	}
	// A blocked provider must not block the GET, and another GET must reuse its in-flight probe.
	returned := make(chan []chatUsageLimit, 1)
	go func() { returned <- env.api.probedChatLimits("alice", "claude-code", account.ID) }()
	select {
	case limits := <-returned:
		if limits != nil {
			t.Fatalf("first read should have no cached usage: %v", limits)
		}
	case <-time.After(time.Second):
		t.Fatal("chat-context waited for the provider probe")
	}
	<-started
	if limits := env.api.probedChatLimits("alice", "claude-code", account.ID); limits != nil || calls.Load() != 1 {
		t.Fatalf("in-flight probe was not reused: %v", limits)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		chatUsageProbes.mu.Lock()
		running := chatUsageProbes.running["claude-code|"+account.ID]
		chatUsageProbes.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("probe did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	limits := env.api.probedChatLimits("alice", "claude-code", account.ID)
	if calls.Load() != 1 || len(limits) != 1 || limits[0].Label != "5h" || limits[0].UsedPercent != 42 {
		t.Fatalf("next read did not receive refreshed usage: %v", limits)
	}
	if limits := env.api.probedChatLimits("bob", "claude-code", account.ID); limits != nil {
		t.Fatalf("cached usage bypassed account authorization: %v", limits)
	}
}
