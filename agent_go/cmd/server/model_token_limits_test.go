package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// A model with its own per-person limit on a shared account: a person who
// has used it up is moved to the next allowed model, a person who has not is
// unaffected, and with every allowed model used up the turn is refused.
func TestModelTokenLimitMovesPersonToAnotherAllowedModel(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	t.Setenv("CODEX_API_KEY", "server-codex-key")
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	env.api.costLedger = ledger
	costledger.SetDefaultLedger(ledger)
	t.Cleanup(func() { costledger.SetDefaultLedger(nil) })
	resetUsage := func() {
		modelTokenUsageCache.Lock()
		modelTokenUsageCache.byKey = map[string]modelUsageCacheEntry{}
		modelTokenUsageCache.Unlock()
	}
	resetUsage()
	t.Cleanup(resetUsage)

	if code := patchAllowed(env, t, "admin", "global:codex-cli", []string{"model-small", "model-large"}); code != http.StatusNoContent {
		t.Fatalf("allowed models: %d", code)
	}
	setLimit := func(model string, daily int64) {
		t.Helper()
		w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "admin", map[string]interface{}{"model_token_limits": map[string]interface{}{model: map[string]int64{"daily": daily}}}, map[string]string{"connectionID": "global:codex-cli"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("set %s limit: %d %s", model, w.Code, w.Body.String())
		}
	}
	// The first allowed model would be the fallback, so cap the large one only.
	setLimit("Model-Large", 1000)
	use := func(user, model string, tokens int) {
		t.Helper()
		if err := ledger.Append(costledger.Entry{Timestamp: time.Now().UTC(), UserID: user, Scope: "chat", Provider: "codex-cli", ModelID: model, AccountID: "global:codex-cli", LLMCallCount: 1, PromptTokens: tokens}); err != nil {
			t.Fatal(err)
		}
		resetUsage()
	}
	alice := withModelLimitPerson(context.Background(), "alice")
	bob := withModelLimitPerson(context.Background(), "bob")

	use("alice", "model-large", 900)
	if model, changed, err := resolveAccountModel(alice, "codex-cli", "", "model-large"); err != nil || changed || model != "model-large" {
		t.Fatalf("under the model's limit: %q %v %v", model, changed, err)
	}
	use("alice", "model-large", 200)
	if model, changed, err := resolveAccountModel(alice, "codex-cli", "", "model-large"); err != nil || !changed || model != "model-small" {
		t.Fatalf("at the model's limit must move to the other allowed model: %q %v %v", model, changed, err)
	}
	// Use of the small model never counts toward the large one's limit, and others are unaffected.
	if model, changed, _ := resolveAccountModel(bob, "codex-cli", "", "model-large"); changed || model != "model-large" {
		t.Fatalf("another person was moved: %q %v", model, changed)
	}
	// With the small model limited too, nothing is left: refused.
	setLimit("model-small", 500)
	use("alice", "model-small", 600)
	if _, _, err := resolveAccountModel(alice, "codex-cli", "", "model-large"); err == nil || !strings.Contains(err.Error(), "no other allowed model") {
		t.Fatalf("every allowed model used up must refuse: %v", err)
	}
	// A limit of zeros removes the model's limit.
	setLimit("model-large", 0)
	setLimit("model-small", 0)
	if model, changed, err := resolveAccountModel(alice, "codex-cli", "", "model-large"); err != nil || changed || model != "model-large" {
		t.Fatalf("limit removed: %q %v %v", model, changed, err)
	}
}
