package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/mux"
	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestChatSubmissionDurableAcceptanceAndReplay(t *testing.T) {
	files := map[string]string{}
	store := &chatSubmissionStore{
		read:  func(_ context.Context, p string) (string, bool, error) { v, ok := files[p]; return v, ok, nil },
		write: func(_ context.Context, p, v string) error { files[p] = v; return nil },
	}
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "/api/query", nil)
		r.Header.Set("Idempotency-Key", "turn-1")
		return r
	}
	// Short adopt wait: the in-flight retry below must still 409 once the
	// wait expires without a resolution; the adopt path is covered below.
	api := &StreamingAPI{
		internalChatSubmissionStore:                  store,
		internalUncertainSubmissionAdoptTimeout:      50 * time.Millisecond,
		internalUncertainSubmissionAdoptPollInterval: 5 * time.Millisecond,
	}
	result := httptest.NewRecorder()
	w, _, finish, ok := api.beginChatSubmission(result, request(), "session-a", "project-a", "hello")
	if !ok || len(files) != 1 {
		t.Fatal("must durably accept before dispatch")
	}
	for _, raw := range files {
		var record chatSubmissionRecord
		_ = json.Unmarshal([]byte(raw), &record)
		if record.State != "delivery_uncertain" || record.Message != "hello" {
			t.Fatalf("invalid predispatch record: %s", raw)
		}
	}
	pending := httptest.NewRecorder()
	if _, _, _, ok := api.beginChatSubmission(pending, request(), "session-a", "project-a", "hello"); ok || pending.Code != 409 {
		t.Fatal("in-flight retry must not dispatch")
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"delivery_status": "sent_to_cli"})
	finish()
	restarted := &StreamingAPI{internalChatSubmissionStore: store}
	replay := httptest.NewRecorder()
	if _, _, _, ok := restarted.beginChatSubmission(replay, request(), "session-a", "project-a", "hello"); ok {
		t.Fatal("restarted retry must not dispatch")
	}
	if replay.Code != 200 || replay.Body.String() != result.Body.String() {
		t.Fatalf("response was not replayed: %s", replay.Body.String())
	}
	conflict := httptest.NewRecorder()
	if _, _, _, ok := restarted.beginChatSubmission(conflict, request(), "session-b", "project-a", "hello"); ok || conflict.Code != 409 {
		t.Fatal("key cannot move between conversations")
	}
}

// turn_running proves the message was never sent or queued, so a same-key
// retry dispatches instead of replaying the rejection forever. Any other
// rejection keeps its replay semantics.
func TestChatSubmissionRedispatchesAfterTurnRunningRejection(t *testing.T) {
	files := map[string]string{}
	store := &chatSubmissionStore{
		read:  func(_ context.Context, p string) (string, bool, error) { v, ok := files[p]; return v, ok, nil },
		write: func(_ context.Context, p, v string) error { files[p] = v; return nil },
	}
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "/api/query", nil)
		r.Header.Set("Idempotency-Key", "lane-1")
		return r
	}
	api := &StreamingAPI{internalChatSubmissionStore: store}

	w, _, finish, ok := api.beginChatSubmission(httptest.NewRecorder(), request(), "session-a", "project-a", "hello")
	if !ok {
		t.Fatal("must durably accept before dispatch")
	}
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "turn_running", "status": "turn_running"})
	finish()

	w2, _, finish2, ok := api.beginChatSubmission(httptest.NewRecorder(), request(), "session-a", "project-a", "hello")
	if !ok {
		t.Fatal("same-key retry after turn_running must dispatch again")
	}
	_ = json.NewEncoder(w2).Encode(map[string]string{"delivery_status": "sent_to_cli"})
	finish2()

	replay := httptest.NewRecorder()
	if _, _, _, ok := api.beginChatSubmission(replay, request(), "session-a", "project-a", "hello"); ok {
		t.Fatal("retry after a confirmed dispatch must not dispatch again")
	}
	if !bytes.Contains(replay.Body.Bytes(), []byte("sent_to_cli")) {
		t.Fatalf("confirmed outcome was not replayed: %s", replay.Body.String())
	}
}

func TestChatSubmissionStillReplaysOtherRejections(t *testing.T) {
	files := map[string]string{}
	store := &chatSubmissionStore{
		read:  func(_ context.Context, p string) (string, bool, error) { v, ok := files[p]; return v, ok, nil },
		write: func(_ context.Context, p, v string) error { files[p] = v; return nil },
	}
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "/api/query", nil)
		r.Header.Set("Idempotency-Key", "lane-2")
		return r
	}
	api := &StreamingAPI{internalChatSubmissionStore: store}

	w, _, finish, ok := api.beginChatSubmission(httptest.NewRecorder(), request(), "session-a", "project-a", "hello")
	if !ok {
		t.Fatal("must durably accept before dispatch")
	}
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "workflow_busy"})
	finish()

	replay := httptest.NewRecorder()
	if _, _, _, ok := api.beginChatSubmission(replay, request(), "session-a", "project-a", "hello"); ok {
		t.Fatal("retry after a non-turn_running rejection must not dispatch again")
	}
	if replay.Code != http.StatusConflict || !bytes.Contains(replay.Body.Bytes(), []byte("workflow_busy")) {
		t.Fatalf("rejection was not replayed: %d %s", replay.Code, replay.Body.String())
	}
}

func TestChatSubmissionRetriesUncertainReceiptOnlyAfterNotDeliveredProof(t *testing.T) {
	store := newTestChatSubmissionStore()
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/query", nil)
		r.Header.Set("Idempotency-Key", "uncertain-after-reaper")
		return r
	}
	api := &StreamingAPI{
		internalChatSubmissionStore: store,
		internalUncertainSubmissionRetryChecker: func(_ context.Context, record chatSubmissionRecord) bool {
			return record.Session == "session-a" && record.Message == "resume me"
		},
	}

	first := httptest.NewRecorder()
	w, _, finish, ok := api.beginChatSubmission(first, request(), "session-a", "project-a", "resume me")
	if !ok {
		t.Fatal("initial submission was not accepted")
	}
	writeSubmissionUncertain(w, "uncertain-after-reaper")
	finish()

	retry := httptest.NewRecorder()
	if _, _, _, ok := api.beginChatSubmission(retry, request(), "session-a", "project-a", "resume me"); !ok {
		t.Fatalf("proven-undelivered receipt did not reopen: status=%d body=%s", retry.Code, retry.Body.String())
	}
}

func TestChatSubmissionKeepsUncertainReceiptWithoutNotDeliveredProof(t *testing.T) {
	store := newTestChatSubmissionStore()
	request := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	request.Header.Set("Idempotency-Key", "still-uncertain")
	api := &StreamingAPI{
		internalChatSubmissionStore: store,
		internalUncertainSubmissionRetryChecker: func(context.Context, chatSubmissionRecord) bool {
			return false
		},
		internalUncertainSubmissionAdoptTimeout:      50 * time.Millisecond,
		internalUncertainSubmissionAdoptPollInterval: 5 * time.Millisecond,
	}

	first := httptest.NewRecorder()
	w, _, finish, ok := api.beginChatSubmission(first, request, "session-a", "project-a", "maybe delivered")
	if !ok {
		t.Fatal("initial submission was not accepted")
	}
	writeSubmissionUncertain(w, "still-uncertain")
	finish()

	retry := httptest.NewRecorder()
	if _, _, _, ok := api.beginChatSubmission(retry, request, "session-a", "project-a", "maybe delivered"); ok || retry.Code != http.StatusConflict {
		t.Fatalf("unproven receipt was reopened: status=%d body=%s", retry.Code, retry.Body.String())
	}
}

func TestChatSubmissionUncertainRetryAdoptsResolvedOutcome(t *testing.T) {
	store := newTestChatSubmissionStore()
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/query", nil)
		r.Header.Set("Idempotency-Key", "adopt-me")
		return r
	}
	api := &StreamingAPI{
		internalChatSubmissionStore: store,
		internalUncertainSubmissionRetryChecker: func(context.Context, chatSubmissionRecord) bool {
			return false
		},
		internalUncertainSubmissionAdoptTimeout:      5 * time.Second,
		internalUncertainSubmissionAdoptPollInterval: 5 * time.Millisecond,
	}
	first := httptest.NewRecorder()
	w, _, finish, ok := api.beginChatSubmission(first, request(), "session-a", "project-a", "hello again")
	if !ok {
		t.Fatal("initial submission was not accepted")
	}
	retryDone := make(chan struct{})
	retry := httptest.NewRecorder()
	var retryOK bool
	go func() {
		defer close(retryDone)
		_, _, _, retryOK = api.beginChatSubmission(retry, request(), "session-a", "project-a", "hello again")
	}()
	// Let the retry block in the adopt wait, then resolve the first attempt.
	time.Sleep(50 * time.Millisecond)
	_ = json.NewEncoder(w).Encode(map[string]string{"delivery_status": "sent_to_cli", "query_id": "q-adopt"})
	finish()
	select {
	case <-retryDone:
	case <-time.After(10 * time.Second):
		t.Fatal("same-key retry did not adopt the resolved outcome")
	}
	if retryOK {
		t.Fatal("adopted retry dispatched a second time")
	}
	if retry.Code != http.StatusOK || retry.Body.String() != first.Body.String() {
		t.Fatalf("retry did not adopt first outcome: %d %s", retry.Code, retry.Body.String())
	}
}

func TestChatSubmissionUncertainRetryStopsWhenRequestEnds(t *testing.T) {
	store := newTestChatSubmissionStore()
	api := &StreamingAPI{
		internalChatSubmissionStore: store,
		internalUncertainSubmissionRetryChecker: func(context.Context, chatSubmissionRecord) bool {
			return false
		},
		internalUncertainSubmissionAdoptTimeout:      5 * time.Second,
		internalUncertainSubmissionAdoptPollInterval: 5 * time.Millisecond,
	}
	firstReq := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	firstReq.Header.Set("Idempotency-Key", "cancel-me")
	if _, _, _, ok := api.beginChatSubmission(httptest.NewRecorder(), firstReq, "session-a", "project-a", "hello"); !ok {
		t.Fatal("initial submission was not accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	retryReq := httptest.NewRequest(http.MethodPost, "/api/query", nil).WithContext(ctx)
	retryReq.Header.Set("Idempotency-Key", "cancel-me")
	cancel()
	retry := httptest.NewRecorder()
	start := time.Now()
	if _, _, _, ok := api.beginChatSubmission(retry, retryReq, "session-a", "project-a", "hello"); ok || retry.Code != http.StatusConflict {
		t.Fatalf("cancelled retry dispatched or missed 409: ok=%v code=%d", ok, retry.Code)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("cancelled adopt wait took %v", elapsed)
	}
}

func TestCanRetryUncertainChatSubmissionUsesClosedNativeTranscriptProof(t *testing.T) {
	home := t.TempDir()
	docs := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	const owner = "default"
	const sessionID = "closed-native-session"
	const project = "Workflow/retry-proof"
	const nativeSessionID = "native-before-receipt"
	workingDir := filepath.Join(home, "runtime", "retry-proof")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcriptDir := filepath.Join(home, ".claude", "projects", claudeNativeTranscriptProjectSlug(workingDir))
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(transcriptDir, nativeSessionID+".jsonl")
	writeTranscriptFixture(t, transcriptPath, []string{
		`{"type":"user","timestamp":"2026-09-19T08:00:00Z","message":{"role":"user","content":"earlier message"}}`,
		`{"type":"assistant","timestamp":"2026-09-19T08:01:00Z","message":{"role":"assistant","content":"earlier answer"}}`,
	})
	conversationPath := filepath.Join(docs, filepath.FromSlash(project), "builder", "conversation", "users", owner, "2026-09-19", "session-"+sessionID+"-conversation.json")
	if err := os.MkdirAll(filepath.Dir(conversationPath), 0o755); err != nil {
		t.Fatal(err)
	}
	conversation := fmt.Sprintf(`{"session_id":%q,"user_id":%q,"runtime":{"provider":"claude-code","external_session_id":%q,"workspace_path":%q,"agent_session_handle":{"provider":{"provider":"claude-code","native_session_id":%q,"working_dir":%q}}},"conversation_history":[]}`, sessionID, owner, nativeSessionID, project, nativeSessionID, workingDir)
	if err := os.WriteFile(conversationPath, []byte(conversation), 0o600); err != nil {
		t.Fatal(err)
	}

	record := chatSubmissionRecord{Owner: owner, Session: sessionID, Project: project, Message: "message after pane closed", UpdatedAt: "2026-09-19T09:00:00Z"}
	api := &StreamingAPI{}
	if !api.canRetryUncertainChatSubmission(context.Background(), record) {
		t.Fatal("final native transcript predating the receipt did not prove non-delivery")
	}

	writeTranscriptFixture(t, transcriptPath, []string{
		`{"type":"user","timestamp":"2026-09-19T09:00:01Z","message":{"role":"user","content":"another prompt after the failed paste"}}`,
		`{"type":"assistant","timestamp":"2026-09-19T09:00:02Z","message":{"role":"assistant","content":"later turn completed"}}`,
	})
	if !api.canRetryUncertainChatSubmission(context.Background(), record) {
		t.Fatal("final native transcript continuing beyond the receipt without the exact prompt did not prove non-delivery")
	}

	writeTranscriptFixture(t, transcriptPath, []string{
		`{"type":"user","timestamp":"2026-09-19T09:00:01Z","message":{"role":"user","content":"message after pane closed"}}`,
	})
	if api.canRetryUncertainChatSubmission(context.Background(), record) {
		t.Fatal("receipt was reopened after the native transcript contained the message")
	}
}

// A live terminal that started before the submission may hold it (uncertain); one that started after it cannot, whatever CLI it is, so the transcript proof can
// reconcile the old submission (Code on Excellence, 2026-10-04: an old uncertain message answered 409 forever once the chat ran on another provider).
func TestCanRetryUncertainChatSubmissionIgnoresALiveTerminalThatStartedLater(t *testing.T) {
	home := t.TempDir()
	docs := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	const owner = "default"
	const sessionID = "closed-native-session-later-terminal"
	const project = "Workflow/retry-proof-later"
	const nativeSessionID = "native-before-receipt-later"
	workingDir := filepath.Join(home, "runtime", "retry-proof-later")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcriptDir := filepath.Join(home, ".claude", "projects", claudeNativeTranscriptProjectSlug(workingDir))
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTranscriptFixture(t, filepath.Join(transcriptDir, nativeSessionID+".jsonl"), []string{
		`{"type":"user","timestamp":"2026-09-19T08:00:00Z","message":{"role":"user","content":"earlier message"}}`,
		`{"type":"assistant","timestamp":"2026-09-19T08:01:00Z","message":{"role":"assistant","content":"earlier answer"}}`,
	})
	conversationPath := filepath.Join(docs, filepath.FromSlash(project), "builder", "conversation", "users", owner, "2026-09-19", "session-"+sessionID+"-conversation.json")
	if err := os.MkdirAll(filepath.Dir(conversationPath), 0o755); err != nil {
		t.Fatal(err)
	}
	conversation := fmt.Sprintf(`{"session_id":%q,"user_id":%q,"runtime":{"provider":"claude-code","external_session_id":%q,"workspace_path":%q,"agent_session_handle":{"provider":{"provider":"claude-code","native_session_id":%q,"working_dir":%q}}}}`,
		sessionID, owner, nativeSessionID, workingDir, nativeSessionID, workingDir)
	if err := os.WriteFile(conversationPath, []byte(conversation), 0o600); err != nil {
		t.Fatal(err)
	}
	record := chatSubmissionRecord{Owner: owner, Session: sessionID, Project: project, Message: "message sent to the old provider", UpdatedAt: "2026-09-19T09:00:00Z"}

	for name, tc := range map[string]struct {
		tmuxStartedAt string
		want          bool
	}{
		"live terminal started before the submission may hold it": {"2026-09-19T08:30:00Z", false},
		"live terminal started after the submission cannot":       {"2026-09-19T09:05:00Z", true},
	} {
		started, err := time.Parse(time.RFC3339, tc.tmuxStartedAt)
		if err != nil {
			t.Fatal(err)
		}
		store := terminals.NewStore()
		store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(started, sessionID, "main:"+sessionID, "mlp-codex-cli-int-later"))
		api := &StreamingAPI{terminalStore: store}
		if got := api.canRetryUncertainChatSubmission(context.Background(), record); got != tc.want {
			t.Errorf("%s: canRetry = %v, want %v", name, got, tc.want)
		}
	}
}

func TestQueuedChatSubmissionDeduplicatesAcrossClientReceipts(t *testing.T) {
	files := map[string]string{}
	store := &chatSubmissionStore{
		read:  func(_ context.Context, p string) (string, bool, error) { v, ok := files[p]; return v, ok, nil },
		write: func(_ context.Context, p, v string) error { files[p] = v; return nil },
	}
	request := func(id string, queued bool) *http.Request {
		r := httptest.NewRequest("POST", "/api/query", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
		r.Header.Set("Idempotency-Key", id)
		if queued {
			r.Header.Set("X-Queued-Chat-Delivery", "true")
		}
		return r
	}
	api := &StreamingAPI{internalChatSubmissionStore: store}
	first := httptest.NewRecorder()
	w, _, finish, ok := api.beginChatSubmission(first, request("tab-one", true), "session-a", "project-a", "#1: run architecture review")
	if !ok {
		t.Fatal("first queued delivery was not accepted")
	}
	_, _ = w.Write([]byte(`{"delivery_status":"sent_to_cli"}`))
	finish()

	second := httptest.NewRecorder()
	if _, _, _, ok := api.beginChatSubmission(second, request("tab-two", true), "session-a", "project-a", "run architecture review"); ok {
		t.Fatal("another tab dispatched the same queued action")
	}
	if second.Code != http.StatusOK || second.Body.String() != first.Body.String() {
		t.Fatalf("queued result was not replayed: %d %s", second.Code, second.Body.String())
	}
	if len(files) != 1 {
		t.Fatalf("queued duplicates created %d durable receipts", len(files))
	}

	// Typed messages retain ordinary idempotency semantics and may deliberately
	// repeat the same text.
	ordinary := httptest.NewRecorder()
	if _, _, _, ok := api.beginChatSubmission(ordinary, request("typed-turn", false), "session-a", "project-a", "run architecture review"); !ok {
		t.Fatal("queued semantic dedupe leaked into ordinary chat input")
	}
}

func TestChatSubmissionPersistenceFailuresNeverInviteResend(t *testing.T) {
	files := map[string]string{}
	fail := true
	store := &chatSubmissionStore{
		read: func(_ context.Context, p string) (string, bool, error) { v, ok := files[p]; return v, ok, nil },
		write: func(_ context.Context, p, v string) error {
			if fail {
				return errors.New("disk unavailable")
			}
			files[p] = v
			return nil
		},
	}
	api := &StreamingAPI{internalChatSubmissionStore: store}
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "/api/query", nil)
		r.Header.Set("Idempotency-Key", "turn-2")
		return r
	}
	rejected := httptest.NewRecorder()
	if _, _, _, ok := api.beginChatSubmission(rejected, request(), "s", "p", "hello"); ok || rejected.Code != 503 {
		t.Fatal("failed acceptance must prevent dispatch")
	}
	fail = false
	response := httptest.NewRecorder()
	w, _, finish, ok := api.beginChatSubmission(response, request(), "s", "p", "hello")
	if !ok {
		t.Fatal("acceptance failed")
	}
	_, _ = w.Write([]byte(`{"delivery_status":"sent_to_cli"}`))
	fail = true
	finish()
	if response.Code != 409 {
		t.Fatalf("lost outcome must be uncertain, got %d", response.Code)
	}
	fail = false
	replay := httptest.NewRecorder()
	if _, _, _, ok := api.beginChatSubmission(replay, request(), "s", "p", "hello"); ok || replay.Code != 409 {
		t.Fatal("lost acknowledgement cannot cause resend")
	}
}

func newTestChatSubmissionStore() *chatSubmissionStore {
	var mu sync.Mutex
	files := map[string]string{}
	return &chatSubmissionStore{
		resolveProject: func(owner, session string) (string, error) { return "", nil },
		read: func(_ context.Context, p string) (string, bool, error) {
			mu.Lock()
			defer mu.Unlock()
			v, ok := files[p]
			return v, ok, nil
		},
		write: func(_ context.Context, p, v string) error { mu.Lock(); defer mu.Unlock(); files[p] = v; return nil },
	}
}

func TestChatSubmissionSameKeyIsIsolatedByOwner(t *testing.T) {
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore()}
	for _, owner := range []string{"alice", "bob"} {
		r := httptest.NewRequest("POST", "/api/query", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: owner}))
		r.Header.Set("Idempotency-Key", "same-client-key")
		w, _, finish, ok := api.beginChatSubmission(httptest.NewRecorder(), r, "session-"+owner, "p", "hello")
		if !ok {
			t.Fatalf("another owner consumed key for %s", owner)
		}
		_, _ = w.Write([]byte(`{"status":"started"}`))
		finish()
	}
}

func TestChatSubmissionColdProjectUsesDurableReceipt(t *testing.T) {
	store := newTestChatSubmissionStore()
	store.resolveProject = func(owner, session string) (string, error) { return "project-a", nil }
	api := &StreamingAPI{internalChatSubmissionStore: store}
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "/api/sessions/s/live-input", nil)
		r.Header.Set("Idempotency-Key", "cold-project")
		return r.WithContext(context.WithValue(r.Context(), chatSubmissionUnknownProjectKey{}, true))
	}
	first := httptest.NewRecorder()
	w, _, finish, ok := api.beginChatSubmission(first, request(), "s", "", "hello")
	if !ok {
		t.Fatal("durable project resolution failed")
	}
	_, _ = w.Write([]byte(`{"delivery_status":"sent_to_cli"}`))
	finish()
	store.resolveProject = func(owner, session string) (string, error) { return "", errors.New("runtime temporarily unavailable") }
	restarted := &StreamingAPI{internalChatSubmissionStore: store}
	replay := httptest.NewRecorder()
	if _, _, _, ok := restarted.beginChatSubmission(replay, request(), "s", "", "hello"); ok || replay.Code != 200 {
		t.Fatalf("cold replay lost durable project: %d %s", replay.Code, replay.Body.String())
	}
}

func TestChatSubmissionPanicPreservesUncertainReceipt(t *testing.T) {
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore()}
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "/api/query", nil)
		r.Header.Set("Idempotency-Key", "panic-boundary")
		return r
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("handler panic was swallowed")
			}
		}()
		w, _, finish, ok := api.beginChatSubmission(httptest.NewRecorder(), request(), "s", "p", "hello")
		if !ok {
			t.Fatal("acceptance failed")
		}
		defer finish()
		_, _ = w.Write([]byte(`{"status":"started"}`))
		panic("crash before dispatch")
	}()
	replay := httptest.NewRecorder()
	if _, _, _, ok := api.beginChatSubmission(replay, request(), "s", "p", "hello"); ok || replay.Code != 409 {
		t.Fatalf("panic produced false acknowledgement: %d", replay.Code)
	}
}

func TestChatSubmissionInheritedTokenCannotAuthorizeAnotherConversation(t *testing.T) {
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore()}
	r := httptest.NewRequest("POST", "/api/query", nil)
	r.Header.Set("Idempotency-Key", "nested")
	_, nested, _, ok := api.beginChatSubmission(httptest.NewRecorder(), r, "first", "p", "hello")
	if !ok {
		t.Fatal("acceptance failed")
	}
	rejected := httptest.NewRecorder()
	if _, _, _, ok := api.beginChatSubmission(rejected, nested, "other", "p", "hello"); ok || rejected.Code != 409 {
		t.Fatal("inherited token bypassed another conversation's durable acceptance")
	}
}

func TestChatSubmissionLiveInputDiscoversSavedWorkflowAfterRestart(t *testing.T) {
	t.Chdir(t.TempDir())
	const owner, session, project = "alice", "cold-builder-chat", "Workflow/cold-workflow"
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	path := project + "/builder/conversation/users/alice/2026-09-17/session-" + session + "-conversation.json"
	// Legacy CLI runtime omitted workspace_path. The workflow path is durable
	// evidence after both process-local project maps have disappeared.
	raw := `{"session_id":"cold-builder-chat","user_id":"alice","agent_mode":"workflow","runtime":{"provider":"claude-code","external_session_id":"native-chat"},"conversation_history":[{"Role":"human","Parts":[{"Text":"old message"}]}]}`
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	workspace := &mockWorkspaceAPI{files: map[string]string{path: raw}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	events := internalevents.NewEventStore(10)
	defer events.Stop()
	events.SetSessionOwner(session, owner)
	api := &StreamingAPI{eventStore: events, runningAgents: map[string]*mcpagent.Agent{session: testCodingAgent(llm.ProviderOpenAI, "gpt-5")}, agentCancelFuncs: map[string]context.CancelFunc{session: func() {}},
		// This test is about the durable project after a restart; delivery
		// into the retained CLI is simulated.
		internalUserMessageDeliveryHandler: func(context.Context, *mcpagent.Agent, mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
			return mcpagent.UserMessageDeliveryResult{DeliveryStatus: mcpagent.UserMessageDeliveryStatusQueuedForInjection}, nil
		}}
	r := httptest.NewRequest(http.MethodPost, "/api/sessions/"+session+"/live-input", bytes.NewBufferString(`{"message":"follow up after restart"}`))
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: owner}))
	r = mux.SetURLVars(r, map[string]string{"session_id": session})
	r.Header.Set("Idempotency-Key", "cold-real-workflow")
	result := httptest.NewRecorder()
	api.handleLiveInputMessage(result, r)
	if result.Code != http.StatusOK {
		t.Fatalf("legitimate retained chat rejected after restart: %d %s", result.Code, result.Body.String())
	}
	if api.sessionWorkspaceFolders[session] != project {
		t.Fatalf("durable project was not restored: %v", api.sessionWorkspaceFolders)
	}
	journalPath := filepath.ToSlash(filepath.Join(chatHistoryRoot(owner), "submissions", submissionDigest("cold-real-workflow")+".json"))
	var receipt chatSubmissionRecord
	if err := json.Unmarshal([]byte(workspace.files[journalPath]), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Owner != owner || receipt.Session != session || receipt.Project != project {
		t.Fatalf("wrong durable acceptance identity: %+v", receipt)
	}
	if _, err := durableSubmissionProject("bob", session); err == nil {
		t.Fatal("another user discovered alice's chat as their own")
	}
}

func TestChatSubmissionDiscoversWorkflowThroughWorkspaceAPI(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	const project = "Workflow/remote-cold-project"
	path := project + "/builder/conversation/2026-09-17/session-remote-saved-conversation.json"
	workspace := &mockWorkspaceAPI{files: map[string]string{path: `{"session_id":"remote-saved","user_id":"alice","runtime":{"provider":"codex-cli"}}`}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	got, err := durableSubmissionProject("alice", "remote-saved")
	if err != nil || got != project {
		t.Fatalf("remote durable discovery = %q, %v", got, err)
	}
}

func TestLegacyEmptyProjectReceiptRequiresVerifiedWorkflowBinding(t *testing.T) {
	store := newTestChatSubmissionStore()
	store.resolveProject = func(string, string) (string, error) { return "Workflow/test", nil }
	api := &StreamingAPI{internalChatSubmissionStore: store}
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/query", nil)
		r.Header.Set("Idempotency-Key", "legacy-project")
		return r
	}
	result := httptest.NewRecorder()
	w, _, finish, ok := api.beginChatSubmission(result, request(), "chat", "", "hello")
	if !ok {
		t.Fatal("initial acceptance failed")
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"delivery_status": "sent_to_cli"})
	finish()
	for _, tc := range []struct {
		project, message string
		status           int
	}{
		{"Workflow/test", "hello", http.StatusOK},
		{"Workflow/another", "hello", http.StatusConflict},
		{"Workflow/test", "different", http.StatusConflict},
	} {
		replay := httptest.NewRecorder()
		if _, _, _, dispatch := api.beginChatSubmission(replay, request(), "chat", tc.project, tc.message); dispatch || replay.Code != tc.status {
			t.Fatalf("project=%s dispatch=%v code=%d want=%d", tc.project, dispatch, replay.Code, tc.status)
		}
	}
	store.resolveProject = func(string, string) (string, error) { return "", errors.New("cannot verify") }
	rejected := httptest.NewRecorder()
	if _, _, _, dispatch := api.beginChatSubmission(rejected, request(), "chat", "Workflow/test", "hello"); dispatch || rejected.Code != http.StatusConflict {
		t.Fatal("unverified legacy receipt was replayed")
	}
}
