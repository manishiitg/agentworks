package server

import (
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-990: quotas must follow the generating account, not the next model
// selection; unbound/revoked or another user's account may never update it.
func TestAccountLimitEventsRequireBoundAdmittedAccount(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	own := env.addAccount(t, "alice", map[string]interface{}{"provider": "codex-cli", "credential": "test-account-key", "display_name": "Own"})
	other := env.addAccount(t, "bob", map[string]interface{}{"provider": "codex-cli", "credential": "test-other-key", "display_name": "Other"})
	env.api.eventStore = storeevents.NewEventStore(20)
	env.api.eventStore.SetSessionOwner("chat", "alice")
	t.Cleanup(env.api.eventStore.Stop)
	t.Cleanup(func() {
		subscriptionLimits.Lock()
		defer subscriptionLimits.Unlock()
		for _, id := range []string{own.ID, other.ID} {
			delete(subscriptionLimits.snapshots, accountLimitKey("codex-cli", id))
		}
	})
	for _, id := range []string{"", other.ID, own.ID} {
		status := &agentevents.StreamingStatusLineEvent{Provider: "codex-cli", ConnectionID: id, Metadata: map[string]interface{}{llmtypes.RateLimitWindowsMetaKey: []llmtypes.RateLimitWindow{{Name: "primary", UsedPercent: 37}}}}
		event := storeevents.Event{Type: "status_line", Timestamp: time.Now(), Data: &agentevents.AgentEvent{Data: status}}
		env.api.observeAccountLimits("chat", event)
	}
	got, found := subscriptionLimits.get("codex-cli", own.ID)
	if !found || got.Source != "event" || len(got.Windows) != 1 || got.Windows[0].UsedPercent != 37 {
		t.Fatalf("bound account update missing: %+v", got)
	}
	if _, found := subscriptionLimits.get("codex-cli", other.ID); found {
		t.Fatal("private account event admitted for wrong owner")
	}
	if _, found := subscriptionLimits.get("codex-cli", ""); found {
		t.Fatal("unbound event guessed an account")
	}
}
