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

// #3038: a Codex login whose refresh token was reused failed every turn while
// the account and its agent still looked fine. The rejected turn must mark the
// account it ran on, and a later completed turn on it clears the mark.
func TestAccountSignInFollowsRejectedTurns(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	own := env.addAccount(t, "alice", map[string]interface{}{"provider": "codex-cli", "credential": "test-account-key", "display_name": "Own"})
	env.api.eventStore = storeevents.NewEventStore(20)
	env.api.eventStore.SetSessionOwner("chat", "alice")
	t.Cleanup(env.api.eventStore.Stop)
	t.Cleanup(func() {
		accountSignIns.Lock()
		defer accountSignIns.Unlock()
		delete(accountSignIns.accounts, accountLimitKey("codex-cli", own.ID))
		delete(accountSignIns.lastByOwner, "alice")
	})
	env.api.lastQueryMu.Lock()
	if env.api.lastQueryRequests == nil {
		env.api.lastQueryRequests = map[string]QueryRequest{}
	}
	env.api.lastQueryRequests["chat"] = QueryRequest{Provider: "codex-cli", ConnectionID: own.ID}
	env.api.lastQueryMu.Unlock()
	completion := func(data *agentevents.UnifiedCompletionEvent, at time.Time) storeevents.Event {
		// Stored as a native chat turn's events are (#3038: these were dropped as child runs).
		return storeevents.Event{ID: at.String(), Type: "unified_completion", Timestamp: at, ParentExecutionID: "session:chat", ExecutionKind: "main_agent", Data: &agentevents.AgentEvent{Data: data}}
	}
	failedAt := time.Now()
	failed := agentevents.NewUnifiedCompletionEventWithError("coding_agent", "retained", "", "codex-cli turn failed: Your access token could not be refreshed because your refresh token was already used. Please log out and sign in again.", time.Second, 1)
	env.api.observeAccountHealth("chat", completion(failed, failedAt))
	if _, needed := accountSignIns.get("codex-cli", own.ID); !needed || !accountSignIns.ownerNeedsSignIn("alice") {
		t.Fatal("rejected login did not mark the account the turn ran on")
	}
	// #3126: a restart keeps the mark (read back from the saved file).
	accountSignIns.Lock()
	accountSignIns.accounts, accountSignIns.lastByOwner, accountSignIns.loadedFrom = map[string]accountSignIn{}, map[string]string{}, ""
	accountSignIns.Unlock()
	if _, needed := accountSignIns.get("codex-cli", own.ID); !needed || !accountSignIns.ownerNeedsSignIn("alice") {
		t.Fatal("the sign-in mark was lost on restart")
	}
	ok := agentevents.NewUnifiedCompletionEvent("coding_agent", "retained", "", "done", "completed", time.Second, 1)
	env.api.observeAccountHealth("chat", completion(ok, failedAt.Add(-time.Minute)))
	if _, needed := accountSignIns.get("codex-cli", own.ID); !needed {
		t.Fatal("an older success cleared a newer sign-in failure")
	}
	env.api.observeAccountHealth("chat", completion(ok, failedAt.Add(time.Minute)))
	if _, needed := accountSignIns.get("codex-cli", own.ID); needed || accountSignIns.ownerNeedsSignIn("alice") {
		t.Fatal("a completed turn did not clear the sign-in mark")
	}
}
