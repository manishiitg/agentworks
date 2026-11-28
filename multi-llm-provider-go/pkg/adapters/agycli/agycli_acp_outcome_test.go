package agycli

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/acpcli"
)

// PLAT-746-987: an ACP refusal must not turn streamed text into a finished
// reply; with no native error step it reads as the account's quota.
func TestAgyACPRefusalIsQuota(t *testing.T) {
	check := agyACPOutcome{home: t.TempDir()}.BeginTurn("quota-test")
	if err := check("example-model", "refusal"); llmerrors.KindOf(err) != llmerrors.KindQuotaExhausted {
		t.Fatalf("refusal = %v, want quota", err)
	}
	if err := check("example-model", "end_turn"); err != nil {
		t.Fatalf("finished turn = %v", err)
	}
}

// Only the current session's newest post-prompt step is an outcome carrier.
func TestAgyACPOutcomeScopesNativeErrorAndReset(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "native.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,step_payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	insert := func(idx, typ int, payload string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO steps VALUES(?,?,3,?)`, idx, typ, []byte(payload)); err != nil {
			t.Fatal(err)
		}
	}
	insert(0, 17, "reason:QUOTA_EXHAUSTED quotaResetTimeStamp:2030-01-01T01:00:00Z")
	watch := beginAgyACPOutcome(home, "native")
	if !watch.enabled || watch.floor != 0 {
		t.Fatal("failed to establish native floor")
	}
	if err := watch.failure("example-model", "end_turn"); err != nil {
		t.Fatalf("old quota leaked: %v", err)
	}
	insert(1, 17, "Error 429, Message: exhausted, Status: RESOURCE_EXHAUSTED reason:QUOTA_EXHAUSTED quotaResetTimeStamp:2030-01-02T01:00:00Z")
	failure := watch.failure("example-model", "end_turn")
	if llmerrors.KindOf(failure) != llmerrors.KindQuotaExhausted || llmerrors.RetryAtOrZero(failure).UTC().Format(time.RFC3339) != "2030-01-02T01:00:00Z" {
		t.Fatalf("lost error or reset: %v", failure)
	}
	insert(2, 101, "system notice")
	insert(3, agyStepCheckpoint, "checkpoint")
	if llmerrors.KindOf(watch.failure("example-model", "end_turn")) != llmerrors.KindQuotaExhausted {
		t.Fatal("trailing bookkeeping hid executor error")
	}
	insert(4, agyStepAssistant, "normal final answer")
	if err := watch.failure("example-model", "end_turn"); err != nil {
		t.Fatalf("non-final error was treated as outcome: %v", err)
	}
	if _, _, err := agyACPConversationFile(home, "../native"); err == nil {
		t.Fatal("native session escaped directory")
	}
	if err := os.Symlink(path, filepath.Join(dir, "linked.db")); err != nil {
		t.Fatal(err)
	}
	if beginAgyACPOutcome(home, "linked").enabled {
		t.Fatal("linked native log accepted")
	}
	if err := os.Symlink(path, path+"-wal"); err != nil {
		t.Fatal(err)
	}
	if beginAgyACPOutcome(home, "native").enabled {
		t.Fatal("linked WAL accepted")
	}
}

func TestAgyACPTypedErrorsClassifyQuotaRetryAndUnavailableModel(t *testing.T) {
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		carrier string
		kind    llmerrors.Kind
		retry   bool
	}{
		{"Error 429, Message: wait, Status: RESOURCE_EXHAUSTED", llmerrors.KindRateLimit, true},
		{"Error 503, Message: busy, Status: UNAVAILABLE", llmerrors.KindServerError, true},
		{"native type-17 error", llmerrors.KindUnknown, false},
		// Seen live: a company account's bad Gemini key showed only "Antigravity stopped this turn because of an error".
		{"Error 400, Message: API key not valid. Please pass a valid API key., Status: INVALID_ARGUMENT, Details: [reason:API_KEY_INVALID]", llmerrors.KindAuth, false},
		{"reason:QUOTA_EXHAUSTED quotaResetDelay:1h2m", llmerrors.KindQuotaExhausted, false},
	} {
		err := agyACPOutcomeError("example-model", "", tc.carrier, now)
		if llmerrors.KindOf(err) != tc.kind || llmerrors.IsRetryable(err) != tc.retry {
			t.Fatalf("wrong native outcome %q: %v", tc.carrier, err)
		}
		if tc.kind == llmerrors.KindQuotaExhausted && !llmerrors.RetryAtOrZero(err).Equal(now.Add(62*time.Minute)) {
			t.Fatal("lost relative reset")
		}
	}
	err := agyACPRPCFailure("example-model", "native", &acpcli.RPCError{Code: -32602, Message: "Model not available"})
	if llmerrors.KindOf(err) != llmerrors.KindModelNotFound {
		t.Fatalf("raw unknown model: %v", err)
	}
	quota := agyACPRPCFailure("example-model", "native", &acpcli.RPCError{Code: 429, Message: "request rejected", Data: json.RawMessage(`{"reason":"QUOTA_EXHAUSTED","detail":"quotaResetTimeStamp:2030-01-02T01:00:00Z"}`)})
	if llmerrors.KindOf(quota) != llmerrors.KindQuotaExhausted || llmerrors.IsRetryable(quota) || llmerrors.RetryAtOrZero(quota).UTC().Format(time.RFC3339) != "2030-01-02T01:00:00Z" {
		t.Fatalf("429 lost explicit quota/reset: %v", quota)
	}
	for _, code := range []int{429, 503} {
		if !llmerrors.IsRetryable(agyACPRPCFailure("example-model", "native", &acpcli.RPCError{Code: code, Message: "request failed"})) {
			t.Fatalf("RPC %d not retryable", code)
		}
	}
}

// T3 Code #44294: a silent prompt counts as finished only when agy's newest step after the turn's floor is its reply,
// marked done. An older reply, a reply still being written, or a step after the reply does not count.
func TestAgyACPReplyFinishedReadsTheNewestStep(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "native.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,step_payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	insert := func(idx, typ, status int) {
		t.Helper()
		if _, err := db.Exec(`INSERT OR REPLACE INTO steps VALUES(?,?,?,x'')`, idx, typ, status); err != nil {
			t.Fatal(err)
		}
	}
	insert(0, agyStepAssistant, agyStepStatusDone)
	finished := agyACPOutcome{home: home}.BeginFinished("native")
	if finished() {
		t.Fatal("the previous turn's reply counted as this turn's")
	}
	insert(1, agyStepUser, agyStepStatusDone)
	insert(2, agyStepAssistant, 2)
	if finished() {
		t.Fatal("a reply still being written counted as finished")
	}
	insert(2, agyStepAssistant, agyStepStatusDone)
	if !finished() {
		t.Fatal("a done reply did not count as finished")
	}
	insert(3, agyStepToolCall, 2)
	if finished() {
		t.Fatal("a step after the reply was ignored")
	}
	if (agyACPOutcome{home: home}).BeginFinished("missing")() {
		t.Fatal("a missing conversation counted as finished")
	}
}
