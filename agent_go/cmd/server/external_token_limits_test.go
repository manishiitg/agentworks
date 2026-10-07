package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// An admin with users:manage sets limits through the admin write path and
// reads them back; a Code reviewer may only read; anyone else gets 403 and
// never sees the tools. Usage counts only server-account (global:) tokens.
// Every call is in the Code review audit log.
func TestTokenLimitToolsAdminSetsReviewerReadsOthersRefused(t *testing.T) {
	api, mock := newCodeAdminFixture(t, true)
	withMemoryUserDirectory(t, `{"users":[
		{"id":"boss","username":"boss","admin":true,"can_create":true},
		{"id":"rev","username":"rev","role":"viewer","code_reviewer":true},
		{"id":"tl-alice","username":"tl-alice","email":"alice@example.com","can_create":true}]}`)
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	api.costLedger = ledger
	now := time.Now().UTC()
	for i, e := range []struct {
		account          string
		prompt, complete int
	}{{"global:claude-code", 600, 400}, {"personal:alice-claude", 9000, 9000}} {
		if err := ledger.Append(costledger.Entry{EventID: "tl" + string(rune('a'+i)), Timestamp: now, UserID: "tl-alice", Scope: "chat", Provider: "claude-code", ModelID: "sonnet", AccountID: e.account, LLMCallCount: 1, PromptTokens: e.prompt, CompletionTokens: e.complete}); err != nil {
			t.Fatal(err)
		}
	}
	sharedTokenUsageCache.Lock()
	delete(sharedTokenUsageCache.byUser, "tl-alice")
	sharedTokenUsageCache.Unlock()

	admin := codeReviewToken("boss", "users:manage")
	w := codeReviewCall(api, admin, "set_token_limits", map[string]any{"email": "alice@example.com", "daily": 1200})
	var set tokenLimitPersonUsage
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &set) != nil || set.DailyLimit != 1200 || set.WeeklyLimit != 0 || set.DailyUsed != 1000 || set.State != "warning" {
		t.Fatalf("admin set = %d %s", w.Code, w.Body.String())
	}
	// weekly set, daily omitted = unchanged; then null clears daily.
	codeReviewCall(api, admin, "set_token_limits", map[string]any{"user_id": "tl-alice", "weekly": 900})
	if w = codeReviewCall(api, admin, "set_token_limits", map[string]any{"user_id": "tl-alice", "daily": nil}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"daily_limit":0,"weekly_limit":900`) || !strings.Contains(w.Body.String(), `"state":"over"`) {
		t.Fatalf("partial updates = %d %s", w.Code, w.Body.String())
	}

	// Per shared account (PLAT-693): a default for everyone on Codex, and
	// Alice's own override on top of it; her overall cap is untouched.
	if w = codeReviewCall(api, admin, "set_token_limits", map[string]any{"account": "codex-cli", "daily": 2000}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"default_limits":{"daily":2000}`) {
		t.Fatalf("account default = %d %s", w.Code, w.Body.String())
	}
	if w = codeReviewCall(api, admin, "set_token_limits", map[string]any{"user_id": "tl-alice", "account": "codex-cli", "weekly": 7000}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"codex-cli":{"label":"Codex","daily_used":0,"weekly_used":0,"daily_limit":2000,"weekly_limit":7000,"default_limits":{"daily":2000},"override":{"weekly":7000}`) {
		t.Fatalf("account override = %d %s", w.Code, w.Body.String())
	}

	// A reviewer reads (only global: tokens count) but cannot write.
	reviewer := codeReviewToken("rev", "code:review")
	w = codeReviewCall(api, reviewer, "get_token_usage", map[string]any{})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"user_id":"tl-alice","username":"tl-alice","email":"alice@example.com","daily_limit":0,"weekly_limit":900,"daily_used":1000,"weekly_used":1000`) {
		t.Fatalf("reviewer usage = %d %s", w.Code, w.Body.String())
	}
	day := now.Format("2006-01-02")
	if w = codeReviewCall(api, reviewer, "get_token_usage", map[string]any{"user_id": "tl-alice", "from": day, "to": day}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"tokens":1000`) || strings.Contains(w.Body.String(), "personal:") {
		t.Fatalf("range usage = %d %s", w.Code, w.Body.String())
	}
	for name, claims := range map[string]*UserClaims{
		"reviewer with users:manage": codeReviewToken("rev", "users:manage", "code:review"),
		"member with users:manage":   codeReviewToken("tl-alice", "users:manage", "code:review"),
		"admin without users:manage": codeReviewToken("boss", "code:review"),
	} {
		if w = codeReviewCall(api, claims, "set_token_limits", map[string]any{"user_id": "tl-alice", "daily": 1}); w.Code != http.StatusForbidden {
			t.Fatalf("%s set limits: %d %s", name, w.Code, w.Body.String())
		}
	}
	member := codeReviewToken("tl-alice", "users:manage", "code:review")
	if w = codeReviewCall(api, member, "get_token_usage", map[string]any{}); w.Code != http.StatusForbidden {
		t.Fatalf("member read usage: %d %s", w.Code, w.Body.String())
	}
	tools := httptest.NewRecorder()
	api.handleExternalTools(tools, adminRequest(http.MethodGet, "/api/external/tools", "", member, nil))
	if strings.Contains(tools.Body.String(), "token_usage") || strings.Contains(tools.Body.String(), "set_token_limits") {
		t.Fatal("a member sees the token limit tools")
	}
	if rec := directoryUserFor("tl-alice", "", ""); rec == nil || rec.TokenLimits == nil || rec.TokenLimits.Daily != 0 || rec.TokenLimits.Weekly != 900 {
		t.Fatalf("refused writes changed the record: %+v", rec)
	}

	mock.mu.Lock()
	log := mock.files[codeAdminAuditPath(time.Now())]
	mock.mu.Unlock()
	for _, want := range []string{`"action":"set_token_limits"`, `"target":"tl-alice daily=1200 weekly=0"`, `"action":"read_token_usage"`, `"via":"token:tok-rev"`} {
		if !strings.Contains(log, want) {
			t.Fatalf("not audited (%s):\n%s", want, log)
		}
	}
}
