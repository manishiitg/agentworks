package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestNativeRecoveryDemandDuringWindowRunsAnotherWindow(t *testing.T) {
	api := &StreamingAPI{nativeTranscriptSyncInFlight: map[string]bool{"chat": true}, nativeTranscriptSyncPending: map[string]bool{"chat": true}}
	windows := 0
	api.runNativeTranscriptSyncWorker("chat", func() {
		windows++
		if windows == 1 {
			api.nativeTranscriptSyncMu.Lock()
			api.nativeTranscriptSyncPending["chat"] = true
			api.nativeTranscriptSyncMu.Unlock()
		}
	})
	if windows != 2 || len(api.nativeTranscriptSyncInFlight) != 0 || len(api.nativeTranscriptSyncPending) != 0 {
		t.Fatalf("lost demand or leaked worker: windows=%d", windows)
	}
}

func TestNativeRecoveryMissingTranscriptIsRetryable(t *testing.T) {
	server := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{}})
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	_, retryable := (&StreamingAPI{}).syncWorkflowBuilderConversationFromNativeTranscript(context.Background(), "alice", "missing", "Workflow/test")
	if !retryable {
		t.Fatal("a missing initial record prematurely ended recovery")
	}
}

func TestFullConversationWriterPreservesConcurrentRecoveredAnswer(t *testing.T) {
	const path = "Chats/alice/2026-09-17/session-chat-conversation.json"
	previous := `{"session_id":"chat","user_id":"alice","pending_submissions":{"one":true},"conversation_history":[{"Role":"human","Parts":[{"Text":"first"}]},{"Role":"ai","Parts":[{"Text":"recovered answer"}]}]}`
	workspace := &mockWorkspaceAPI{files: map[string]string{path: previous}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	msg := func(text string) llmtypes.MessageContent {
		return llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}}}
	}
	(&StreamingAPI{}).persistChatConversationToPathWithTerminalSession("chat", "", "simple", "alice", []llmtypes.MessageContent{msg("first"), msg("next")}, nil, nil, path)
	var got builderConversationLog
	if err := json.Unmarshal([]byte(workspace.files[path]), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.ConversationHistory) != 3 || got.ConversationHistory[1].Parts[0].Text != "recovered answer" {
		t.Fatalf("lost canonical answer: %+v", got)
	}
	var raw map[string]interface{}
	_ = json.Unmarshal([]byte(workspace.files[path]), &raw)
	if raw["pending_submissions"] == nil {
		t.Fatal("opaque acceptance metadata erased")
	}
}

func setupNativeRecoveryCanonicalFixture(t *testing.T, path, raw string) {
	t.Helper()
	server := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{path: raw}})
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
}

func TestNativeRecoveryRefreshRejectsStaleCallerSnapshot(t *testing.T) {
	const path = "Workflow/test/builder/conversation/test.json"
	setupNativeRecoveryCanonicalFixture(t, path, `{"session_id":"test","revision":9,"conversation_history":[{"Role":"human","Parts":[{"Text":"hello"}]},{"Role":"ai","Parts":[{"Text":"canonical answer"}]}]}`)
	stale := builderConversationLog{SessionID: "test"}
	got := (&StreamingAPI{}).refreshLatestBuilderConversationFromNativeTranscript(context.Background(), path, `{"session_id":"test"}`, stale)
	if len(got.ConversationHistory) != 2 || got.Revision != 9 {
		t.Fatal("stale caller snapshot replaced canonical state")
	}
}

func TestNativeRecoveryDemandDurableAndOwnerScoped(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", root)
	for _, owner := range []string{"alice", "bob"} {
		if err := persistNativeTranscriptRecoveryDemand(nativeTranscriptRecoveryDemand{UserID: owner, SessionID: "same", WorkspacePath: "Workflow/same", State: "unresolved"}); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := filepath.Glob(filepath.Join(root, "chat-native-recovery", "*.json"))
	if err != nil || len(paths) != 2 {
		t.Fatalf("owner demand collision: %v %v", paths, err)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var demand nativeTranscriptRecoveryDemand
		if json.Unmarshal(raw, &demand) != nil || demand.State != "unresolved" || demand.UserID == "" {
			t.Fatal("invalid durable recovery marker")
		}
	}
}

func TestNativeRecoveryDemandUsesBoundedBackoffAndExpires(t *testing.T) {
	now := time.Date(2026, 9, 21, 7, 0, 0, 0, time.UTC)
	demand := nativeTranscriptRecoveryDemand{
		UserID: "alice", SessionID: "chat", WorkspacePath: "Workflow/test",
		RequestedAt: now.Add(-time.Minute), State: "unresolved",
	}
	if !nativeTranscriptRecoveryDue(demand, now) {
		t.Fatal("fresh demand should be due")
	}
	demand = advanceNativeTranscriptRecoveryDemand(demand, now, true)
	if demand.AttemptCount != 1 || demand.NextAttemptAt.Sub(now) != time.Minute || demand.State != "unresolved" {
		t.Fatalf("unexpected first retry state: %+v", demand)
	}
	if nativeTranscriptRecoveryDue(demand, now.Add(30*time.Second)) {
		t.Fatal("demand ignored its retry backoff")
	}
	if !nativeTranscriptRecoveryDue(demand, now.Add(time.Minute)) {
		t.Fatal("demand did not become due after backoff")
	}

	old := nativeTranscriptRecoveryDemand{
		UserID: "alice", SessionID: "old", WorkspacePath: "Workflow/test",
		RequestedAt: now.Add(-nativeTranscriptRecoveryMaxAge), State: "unresolved",
	}
	if nativeTranscriptRecoveryDue(old, now) {
		t.Fatal("expired demand must not replay")
	}

	unsupported := advanceNativeTranscriptRecoveryDemand(demand, now, false)
	if unsupported.State != "unsupported" || nativeTranscriptRecoveryDue(unsupported, now.Add(24*time.Hour)) {
		t.Fatalf("unsupported provider remained replayable: %+v", unsupported)
	}

	capped := nativeTranscriptRecoveryDemand{
		UserID: "alice", SessionID: "capped", WorkspacePath: "Workflow/test",
		RequestedAt: now, State: "unresolved",
	}
	for capped.State == "unresolved" {
		capped = advanceNativeTranscriptRecoveryDemand(capped, now.Add(time.Duration(capped.AttemptCount)*time.Hour), true)
	}
	if capped.State != "exhausted" || capped.AttemptCount != nativeTranscriptRecoveryMaxAttempts {
		t.Fatalf("recovery did not stop at attempt cap: %+v", capped)
	}
}

func TestNativeRecoveryAttemptDoesNotOverwriteNewerDemand(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", root)
	requestedAt := time.Date(2026, 9, 21, 7, 0, 0, 0, time.UTC)
	original := nativeTranscriptRecoveryDemand{
		UserID: "alice", SessionID: "chat", WorkspacePath: "Workflow/test",
		RequestedAt: requestedAt, State: "unresolved",
	}
	newer := original
	newer.RequestedAt = requestedAt.Add(time.Minute)
	if err := persistNativeTranscriptRecoveryDemand(newer); err != nil {
		t.Fatal(err)
	}
	advanced := advanceNativeTranscriptRecoveryDemand(original, requestedAt.Add(2*time.Minute), true)
	if err := persistNativeTranscriptRecoveryAttempt(original, advanced); err != nil {
		t.Fatal(err)
	}
	path, err := nativeTranscriptRecoveryDemandPath(original)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got nativeTranscriptRecoveryDemand
	if json.Unmarshal(raw, &got) != nil || !got.RequestedAt.Equal(newer.RequestedAt) || got.AttemptCount != 0 {
		t.Fatalf("older attempt overwrote newer demand: %+v", got)
	}
}

func TestRawConversationSnapshotPreservesRecoveredStructuredRows(t *testing.T) {
	const path = "Workflow/test/builder/conversation/raw.json"
	workspace := &mockWorkspaceAPI{files: map[string]string{path: `{"session_id":"chat","user_id":"alice","revision":8,"conversation_history":[{"Role":"human","Parts":[{"Text":"first"}]},{"Role":"ai","Parts":[{"Text":"answer","ToolCall":{"ID":"keep"}}]}]}`}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	incoming := `{"session_id":"chat","user_id":"alice","conversation_history":[{"Role":"human","Parts":[{"Text":"first"}]},{"Role":"human","Parts":[{"Text":"next"}]}]}`
	if err := persistRawConversationSnapshot(context.Background(), path, incoming); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(workspace.files[path]), &got); err != nil {
		t.Fatal(err)
	}
	if got["revision"] != float64(9) || !strings.Contains(workspace.files[path], `"keep"`) {
		t.Fatalf("revision or structure lost: %s", workspace.files[path])
	}
	var conv builderConversationLog
	_ = json.Unmarshal([]byte(workspace.files[path]), &conv)
	if len(conv.ConversationHistory) != 3 || conv.ConversationHistory[1].Parts[0].Text != "answer" {
		t.Fatal("raw snapshot lost or reordered answer")
	}
}

func TestProductConversationContinuationNeverSilentlySubstitutesSession(t *testing.T) {
	for _, tc := range []struct {
		name                string
		continuation        bool
		requested, resolved string
		fail                bool
	}{
		{"fresh provisional ID", false, "local-uuid", "product-new", false},
		{"verified same session", true, "established", "established", false},
		{"missing indexed session", true, "open-tab", "registry-other", true},
		{"continuation without ID", true, "", "registry-other", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProductConversationContinuation(tc.continuation, tc.requested, tc.resolved)
			if (err != nil) != tc.fail {
				t.Fatalf("unexpected continuation result: %v", err)
			}
		})
	}
}

func TestNativeRecoveryDoesNotInventDefaultOwner(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", root)
	api := &StreamingAPI{sessionWorkspaceFolders: map[string]string{"unknown": "Workflow/shared"}}
	api.scheduleWorkflowBuilderNativeTranscriptSync("unknown")
	paths, _ := filepath.Glob(filepath.Join(root, "chat-native-recovery", "*.json"))
	if len(paths) != 0 || len(api.nativeTranscriptSyncInFlight) != 0 {
		t.Fatal("ownerless recovery was scheduled under a fallback identity")
	}
}

func TestNativeRecoveryRefreshCannotSubstituteOwnerAfterRead(t *testing.T) {
	const path = "Workflow/test/builder/conversation/owner.json"
	setupNativeRecoveryCanonicalFixture(t, path, `{"session_id":"chat","user_id":"bob","conversation_history":[{"Role":"ai","Parts":[{"Text":"bob private answer"}]}]}`)
	original := builderConversationLog{SessionID: "chat", UserID: "alice"}
	got := (&StreamingAPI{}).refreshLatestBuilderConversationFromNativeTranscript(context.Background(), path, `{"session_id":"chat","user_id":"alice"}`, original)
	if got.UserID != "alice" || len(got.ConversationHistory) != 0 {
		t.Fatal("canonical reread substituted another owner's transcript")
	}
}

func TestNativeRecoveryPublishesOnlyToRegisteredMatchingOwner(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()
	api := &StreamingAPI{eventStore: store}
	messages := []builderConversationMessage{{Role: "ai", Parts: []builderConversationPart{{Text: "private"}}}}
	if api.publishOwnedNativeTranscriptRecoveredMessages("alice", "s", nil, messages, nil) != 0 {
		t.Fatal("published before ownership registration")
	}
	store.SetSessionOwner("s", "bob")
	if api.publishOwnedNativeTranscriptRecoveredMessages("alice", "s", nil, messages, nil) != 0 {
		t.Fatal("published into another owner's live session")
	}
}

func TestNativeRecoveryBatchBoundsConcurrencyAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	demands := make([]nativeTranscriptRecoveryDemand, 20)
	started := make(chan struct{}, 20)
	var total atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runNativeRecoveryBatch(ctx, demands, func(ctx context.Context, _ nativeTranscriptRecoveryDemand) {
			total.Add(1)
			started <- struct{}{}
			<-ctx.Done()
		})
	}()
	for i := 0; i < nativeTranscriptRecoveryWorkers; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("worker did not start")
		}
	}
	if total.Load() != nativeTranscriptRecoveryWorkers {
		t.Fatal("recovery exceeded its bounded worker count")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recovery workers did not stop")
	}
	if total.Load() != nativeTranscriptRecoveryWorkers {
		t.Fatal("cancellation dispatched historical recovery work")
	}
}

func TestNativeRecoveryPeriodicallyRetriesAfterInitialWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	passes := make(chan struct{}, 10)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPeriodicNativeRecovery(ctx, time.Millisecond, func(ctx context.Context) {
			select {
			case passes <- struct{}{}:
			case <-ctx.Done():
			}
		})
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-passes:
		case <-time.After(time.Second):
			t.Fatal("late flush demand was not retried without restart")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("periodic replay ignored shutdown")
	}
}
