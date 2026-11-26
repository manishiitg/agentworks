package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-990: cached private quotas must not bypass fresh account admission;
// admin quota visibility must not grant model use or account management.
func TestAccountLimitsPrivateCacheRequiresAdmission(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "codex-cli", "display_name": "Private plan", "credential": "private-key-not-for-response"})
	// Exercise authorization over an already cached provider result, without
	// launching unrelated real-account probes from this permission regression.
	keys := []string{"codex-cli|" + account.ID}
	for _, provider := range serverAccountProviders() {
		keys = append(keys, provider+"|global:"+provider)
	}
	chatUsageProbes.mu.Lock()
	previous := map[string]chatUsageProbeResult{}
	for _, key := range keys {
		previous[key] = chatUsageProbes.results[key]
		chatUsageProbes.results[key] = chatUsageProbeResult{expires: time.Now().Add(time.Hour)}
	}
	chatUsageProbes.mu.Unlock()
	t.Cleanup(func() {
		chatUsageProbes.mu.Lock()
		defer chatUsageProbes.mu.Unlock()
		for _, key := range keys {
			if old := previous[key]; old.expires.IsZero() {
				delete(chatUsageProbes.results, key)
			} else {
				chatUsageProbes.results[key] = old
			}
		}
	})
	subscriptionLimits.put("codex-cli", account.ID, "probe", time.Now(), []llmtypes.AccountUsageWindow{{Label: "Weekly", UsedPercent: 42}})
	t.Cleanup(func() {
		subscriptionLimits.Lock()
		delete(subscriptionLimits.snapshots, accountLimitKey("codex-cli", account.ID))
		subscriptionLimits.Unlock()
	})
	for _, handler := range []http.HandlerFunc{env.api.handleAccountLimits, func(w http.ResponseWriter, r *http.Request) {
		env.api.externalTokenLimitCall(w, r, "get_subscription_limits", nil)
	}} {
		for _, tc := range []struct {
			user    string
			visible bool
		}{{"alice", true}, {"admin", true}, {"bob", false}} {
			w := env.do(t, handler, http.MethodGet, "/api/provider-accounts/limits", tc.user, nil, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("%s: status %d", tc.user, w.Code)
			}
			var response struct {
				Accounts []accountLimitsView `json:"accounts"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range response.Accounts {
				if row.AccountID == account.ID {
					found = true
					if row.Snapshot == nil || len(row.Snapshot.Windows) != 1 || row.Snapshot.Windows[0].UsedPercent != 42 {
						t.Fatalf("missing cached quota: %+v", row)
					}
				}
			}
			if found != tc.visible {
				t.Fatalf("%s private quota visible=%t", tc.user, found)
			}
			for _, forbidden := range []string{"private-key-not-for-response", "credential", "auth_method", "sharing", "identity"} {
				if strings.Contains(w.Body.String(), forbidden) {
					t.Fatalf("response includes %s", forbidden)
				}
			}
		}
	}
	// The existing management screen still hides this account from admins.
	for _, row := range env.list(t, "admin", "") {
		if row.ID == account.ID {
			t.Fatal("quota read widened account management visibility")
		}
	}
	if _, ok := env.api.chatUsageProbeAccount(sharedSecretsRequest(http.MethodGet, "/", "admin", nil).Context(), "admin", "codex-cli", account.ID); ok {
		t.Fatal("quota read granted model-use admission")
	}
}
