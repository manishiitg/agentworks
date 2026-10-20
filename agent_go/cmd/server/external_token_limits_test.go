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
)

// An admin with users:manage sets limits through the admin write path and
// reads them back; a Code reviewer may only read; anyone else gets 403 and
// never sees the tools. Usage counts only server-account (global:) tokens.
// Every call is in the Code review audit log.
func TestTokenLimitToolsAdminSetsReviewerReadsOthersRefused(t *testing.T) {
	api, mock := newCodeAdminFixture(t, true)
	withMemoryUserDirectory(t, `{"users":[
		{"id":"boss","username":"boss","admin":true,"can_create":true},
		{"id":"rev","username":"rev","role":"viewer","crew_reviewer":true},
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
	w = codeReviewCall(api, admin, "set_token_limits", map[string]any{"user_id": "tl-alice", "daily": nil})
	set = tokenLimitPersonUsage{}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &set) != nil || set.DailyLimit != 0 || set.WeeklyLimit != 900 || set.State != "over" {
		t.Fatalf("partial updates = %d %s", w.Code, w.Body.String())
	}

	// Per shared account (PLAT-693): a default for everyone on Codex, and
	// Alice's own override on top of it; her overall cap is untouched.
	if w = codeReviewCall(api, admin, "set_token_limits", map[string]any{"account": "codex-cli", "daily": 2000}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"default_limits":{"daily":2000}`) {
		t.Fatalf("account default = %d %s", w.Code, w.Body.String())
	}
	w = codeReviewCall(api, admin, "set_token_limits", map[string]any{"user_id": "tl-alice", "account": "codex-cli", "weekly": 7000})
	set = tokenLimitPersonUsage{}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &set) != nil {
		t.Fatalf("account override = %d %s", w.Code, w.Body.String())
	}
	codex := set.Accounts["codex-cli"]
	if codex == nil || codex.Label != "Codex" || codex.DailyUsed != 0 || codex.WeeklyUsed != 0 || codex.DailyLimit != 2000 || codex.WeeklyLimit != 7000 || codex.Default == nil || codex.Default.Daily != 2000 || codex.Override == nil || codex.Override.Weekly != 7000 {
		t.Fatalf("account override = %d %s", w.Code, w.Body.String())
	}

	// A reviewer reads (only global: tokens count) but cannot write.
	reviewer := codeReviewToken("rev", "crews:review")
	w = codeReviewCall(api, reviewer, "get_token_usage", map[string]any{})
	var response struct {
		People []tokenLimitPersonUsage `json:"people"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatalf("reviewer usage = %d %s", w.Code, w.Body.String())
	}
	var alice *tokenLimitPersonUsage
	for i := range response.People {
		if response.People[i].UserID == "tl-alice" {
			alice = &response.People[i]
		}
	}
	if alice == nil || alice.Username != "tl-alice" || alice.Email != "alice@example.com" || alice.DailyLimit != 0 || alice.WeeklyLimit != 900 || alice.DailyUsed != 1000 || alice.WeeklyUsed != 1000 {
		t.Fatalf("reviewer usage = %d %s", w.Code, w.Body.String())
	}
	day := now.Format("2006-01-02")
	if w = codeReviewCall(api, reviewer, "get_token_usage", map[string]any{"user_id": "tl-alice", "from": day, "to": day}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"tokens":1000`) || strings.Contains(w.Body.String(), "personal:") {
		t.Fatalf("range usage = %d %s", w.Code, w.Body.String())
	}
	for name, claims := range map[string]*UserClaims{
		"reviewer with users:manage": codeReviewToken("rev", "users:manage", "crews:review"),
		"member with users:manage":   codeReviewToken("tl-alice", "users:manage", "crews:review"),
		"admin without users:manage": codeReviewToken("boss", "crews:review"),
	} {
		if w = codeReviewCall(api, claims, "set_token_limits", map[string]any{"user_id": "tl-alice", "daily": 1}); w.Code != http.StatusForbidden {
			t.Fatalf("%s set limits: %d %s", name, w.Code, w.Body.String())
		}
	}
	member := codeReviewToken("tl-alice", "users:manage", "crews:review")
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

// Allowed models per person sit next to the token limits (PLAT-714): the
// admin limits the shared Codex account to one model and gives Alice her own
// list. Her turns resolve against her list, Bob's against the account's;
// all_models gives her every model and null puts her back on the account's.
// An id the provider's catalog does not list is refused; a reviewer cannot
// set models; every write is audited.
func TestAllowedModelsPersonOverrideDecidesTheTurnModel(t *testing.T) {
	api, mock := newCodeAdminFixture(t, true)
	withMemoryUserDirectory(t, `{"users":[
		{"id":"boss","username":"boss","admin":true,"can_create":true},
		{"id":"rev","username":"rev","role":"viewer","crew_reviewer":true},
		{"id":"am-alice","username":"am-alice","email":"am-alice@example.com","can_create":true},
		{"id":"am-bob","username":"am-bob","can_create":true}]}`)
	ids := providerModelIDs("codex-cli")
	if len(ids) < 2 {
		t.Fatalf("codex-cli catalog = %v, want two models", ids)
	}
	only, other := ids[0], ids[1]
	admin := codeReviewToken("boss", "users:manage")
	if w := codeReviewCall(api, admin, "set_allowed_models", map[string]any{"account": "codex-cli", "models": []any{only}}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"allowed_models":["`+only+`"]`) {
		t.Fatalf("account list = %d %s", w.Code, w.Body.String())
	}
	if w := codeReviewCall(api, admin, "set_allowed_models", map[string]any{"account": "codex-cli", "user_id": "am-alice", "models": []any{"no-such-model"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown model = %d %s", w.Code, w.Body.String())
	}
	if w := codeReviewCall(api, admin, "set_allowed_models", map[string]any{"account": "codex-cli", "user_id": "am-alice", "models": []any{other}}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"allowed_models":{"codex-cli":{"models":["`+other+`"],"source":"person"}}`) {
		t.Fatalf("person override = %d %s", w.Code, w.Body.String())
	}
	resolve := func(person, model, want string) {
		t.Helper()
		got, _, err := resolveAccountModel(withModelLimitPerson(context.Background(), person), "codex-cli", "global:codex-cli", model)
		if err != nil || got != want {
			t.Fatalf("%s on %s runs %q (%v), want %q", person, model, got, err, want)
		}
	}
	resolve("am-alice", only, other) // not on her list: her first model, never a failed turn
	resolve("am-alice", other, other)
	resolve("am-bob", other, only)
	codeReviewCall(api, admin, "set_allowed_models", map[string]any{"account": "codex-cli", "email": "am-alice@example.com", "all_models": true})
	resolve("am-alice", only, only)
	resolve("am-alice", other, other)
	codeReviewCall(api, admin, "set_allowed_models", map[string]any{"account": "codex-cli", "user_id": "am-alice", "models": nil})
	resolve("am-alice", other, only)

	w := codeReviewCall(api, admin, "get_token_usage", map[string]any{"account": "codex-cli"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"account_allowed_models":{"codex-cli":["`+only+`"]}`) || !strings.Contains(w.Body.String(), `"allowed_models":{"codex-cli":{"models":["`+only+`"],"source":"account"}}`) {
		t.Fatalf("usage models = %d %s", w.Code, w.Body.String())
	}
	if w = codeReviewCall(api, codeReviewToken("rev", "users:manage", "crews:review"), "set_allowed_models", map[string]any{"account": "codex-cli", "models": nil}); w.Code != http.StatusForbidden {
		t.Fatalf("reviewer set models = %d %s", w.Code, w.Body.String())
	}
	mock.mu.Lock()
	log := mock.files[codeAdminAuditPath(time.Now())]
	mock.mu.Unlock()
	if !strings.Contains(log, `"action":"set_allowed_models"`) || !strings.Contains(log, `"target":"am-alice account=codex-cli models=`+other+`"`) {
		t.Fatalf("not audited:\n%s", log)
	}
}
