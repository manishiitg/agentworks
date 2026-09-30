package server

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

func TestGlobalCostsRequireReviewPermission(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"admin","username":"admin","admin":true},
		{"id":"reviewer","username":"reviewer","code_reviewer":true},
		{"id":"member","username":"member","can_create":true},
		{"id":"disabled","username":"disabled","code_reviewer":true,"disabled":true}]}`)
	for name, handler := range map[string]func(http.ResponseWriter, *http.Request){
		"overview":    env.api.handleCostOverview,
		"accounts":    env.api.handleProviderAccountCosts,
		"raw summary": env.api.handleCostSummary,
	} {
		for _, user := range []string{"member", "disabled"} {
			w := env.do(t, handler, http.MethodGet, "/", user, nil, nil)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s: %s = %d %s", name, user, w.Code, w.Body.String())
			}
		}
	}
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	env.api.costLedger = ledger
	for _, work := range []string{"_users/alice/Chats/Code/projects/visible-code", "_users/alice/Chats/Video Studio/projects/private-video"} {
		actor := "alice"
		if strings.Contains(work, "visible-code") {
			actor = "reviewer"
		}
		if err := ledger.Append(costledger.Entry{Timestamp: time.Now().UTC(), WorkflowID: work, UserID: actor, Provider: "claude-code", ModelID: "sonnet", TotalCostUSD: 1}); err != nil {
			t.Fatal(err)
		}
	}
	for name, handler := range map[string]func(http.ResponseWriter, *http.Request){"overview": env.api.handleCostOverview, "accounts": env.api.handleProviderAccountCosts} {
		w := env.do(t, handler, http.MethodGet, "/", "reviewer", nil, nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "visible-code") || strings.Contains(w.Body.String(), "private-video") {
			t.Fatalf("reviewer %s lost scoping: %d %s", name, w.Code, w.Body.String())
		}
		w = env.do(t, handler, http.MethodGet, "/", "admin", nil, nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "private-video") {
			t.Fatalf("admin %s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// The legacy summary has no work-level filtering and stays admin-only.
	if w := env.do(t, env.api.handleCostSummary, http.MethodGet, "/", "reviewer", nil, nil); w.Code != http.StatusForbidden {
		t.Fatalf("reviewer read unfiltered summary: %d %s", w.Code, w.Body.String())
	}
	if w := env.do(t, env.api.handleCostSummary, http.MethodGet, "/", "admin", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("admin summary: %d %s", w.Code, w.Body.String())
	}
}
