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

	"github.com/gorilla/mux"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// PLAT-746: exercise the actual canonical event observer, durable restart/queue
// documents, and claim. Only provider execution is absent; original input must
// never be replayed, including when the failure came from an incoming message.
func TestUsageLimitRecoveryUsesOneSyntheticTurnAndSurvivesRestart(t *testing.T) {
	for _, scenario := range []string{"future", "due_after_restart", "fresh_past", "too_overdue", "stopped", "person_queued", "stop_after_claim", "child", "other_turn", "different_native", "missing_reset", "stopped_before_failure", "incoming_future", "incoming_due", "incoming_batch", "incoming_stopped", "unowned_native", "native_root", "foreground_observer", "stale_foreground_observer", "cancel_recovery", "child_root_parent", "foreign_root_parent"} {
		t.Run(scenario, func(t *testing.T) {
			const user, sid = "owner", "usage-limit-chat"
			files := map[string]string{}
			journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			newAPI := func() *StreamingAPI {
				api := newConversationTurnQueueTestAPI(files)
				api.conversationTurnQueueDraining[sid] = true // Observe/claim, never execute a model.
				api.stoppedSessions = map[string]bool{}
				api.eventStore = storeevents.NewEventStore(100)
				api.eventStore.SetDurableJournal(journal)
				api.eventStore.SetSessionOwner(sid, user)
				if err := api.eventStore.SetSessionPersistenceClass(sid, storeevents.SessionPersistenceInteractiveChat); err != nil {
					t.Fatal(err)
				}
				api.eventStore.SetEventAddedCallback(api.observeUsageLimitFailure)
				t.Cleanup(api.eventStore.Stop)
				return api
			}
			api := newAPI()
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user, Username: user})
			incoming := strings.HasPrefix(scenario, "incoming_")
			if incoming {
				ctx = context.WithValue(ctx, deliveredAgentMessageKey{}, map[string]interface{}{"source": "agent_message", "reply_address": "original-inbox"})
			}
			req := QueryRequest{Query: "original task with side effects", AgentMode: "multi-agent", Provider: "codex-cli", ModelID: "example-model", AgentMessageInbox: "incoming"}
			if err := api.saveRestartContinueRequest(ctx, user, sid, "failed-query", req); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			failedAt, reset := now.Add(-time.Hour), now.Add(time.Hour)
			switch scenario {
			case "due_after_restart", "stop_after_claim", "incoming_due", "incoming_batch", "incoming_stopped":
				reset = now.Add(-time.Second)
			case "fresh_past":
				failedAt = now
				reset = now.Add(-time.Second)
			case "too_overdue":
				failedAt = now.Add(-26 * time.Hour)
				reset = now.Add(-25 * time.Hour)
			}
			document, err := api.readRestartContinueRecords(ctx, user)
			if err != nil {
				t.Fatal(err)
			}
			record := document.Chats[sid]
			record.SavedAt = failedAt.Add(-time.Minute)
			document.Chats[sid] = record
			if err := api.writeRestartContinueRecords(ctx, user, document); err != nil {
				t.Fatal(err)
			}
			started := failedAt
			original := queuedConversationTurn{ID: "started-original", UserID: user, SessionID: sid, Request: req, StartedAt: &started}
			if incoming {
				original.Principal = queuedPrincipalFromContext(ctx)
				original.Principal.DeliveredMessageSession = sid
				original.Principal.DeliveredMessageUser = user
			}
			originals := []queuedConversationTurn{original}
			if scenario == "incoming_batch" {
				second := original
				second.ID = "started-original2"
				originals = append(originals, second)
			}
			if err := api.writeConversationTurnQueue(ctx, user, originals); err != nil {
				t.Fatal(err)
			}
			completion := unifiedevents.NewUnifiedCompletionEvent("coding_agent", "retained", "", "", "error", 0, 1)
			completion.Metadata["error_code"] = "quota_exhausted"
			completion.Metadata["retry_at"] = reset.Format(time.RFC3339)
			completion.Metadata["source"] = "mcpagent_session"
			event := storeevents.Event{ID: "quota-failure", Type: "unified_completion", Timestamp: failedAt, SessionID: sid, ExecutionID: "failed-query", ExecutionKind: "main_agent", Data: unifiedevents.NewAgentEvent(completion)}
			switch scenario {
			case "child":
				event.ParentExecutionID = "parent-query"
			case "child_root_parent":
				event.ParentExecutionID, event.ExecutionKind = "main:"+sid, "sub_agent"
			case "foreign_root_parent":
				event.ParentExecutionID = "session:another-chat"
			case "native_root":
				// The real observer has no host execution fields. EventStore
				// enriches it with main:<session> / session:<session> ownership.
				event.ExecutionID, event.ExecutionKind = "", ""
				event.Data.TurnID = "owned-native"
				api.retainedMainTurnExecutionIDs = map[string]string{sid: "failed-query"}
				api.retainedMainTurnCanonicalIDs = map[string]retainedTurnIdentity{"failed-query": {ID: "owned-native"}}
			case "other_turn":
				event.ExecutionID = "other-query"
				completion.Metadata["source"] = "other"
			case "different_native":
				api.retainedMainTurnExecutionIDs = map[string]string{sid: "current-execution"}
				api.retainedMainTurnCanonicalIDs = map[string]retainedTurnIdentity{"current-execution": {ID: "current-native"}}
				event.Data.TurnID = "older-native"
			case "unowned_native":
				event.ExecutionID = "older-execution"
			case "missing_reset":
				delete(completion.Metadata, "retry_at")
			case "stopped_before_failure":
				api.markSessionStopped(sid)
			}
			if scenario == "foreground_observer" || scenario == "stale_foreground_observer" {
				completion.Metadata["source"] = ""
				completion.Metadata["canonical_turn_completion"] = true
				event.Data.TurnID = "foreground-native"
				event.Data.Timestamp = failedAt
				query := "failed-query"
				if scenario == "stale_foreground_observer" {
					query = "earlier-query"
				}
				observer := storeevents.NewEventObserverWithLogger(api.eventStore, sid, nil, query)
				if err := observer.HandleEvent(ctx, event.Data); err != nil {
					t.Fatal(err)
				}
				for _, stored := range api.eventStore.GetAllEventsRaw(sid) {
					if stored.Type == "unified_completion" {
						event = stored
					}
				}
				encoded, _ := json.Marshal(event)
				var restored storeevents.Event
				if json.Unmarshal(encoded, &restored) != nil || restored.ObserverQueryID != query {
					t.Fatal("host observer query lost in journal/API encoding")
				}
			} else {
				api.eventStore.AddEvent(sid, event)
			}
			// Replay the STORED event, including its real hierarchy. Passing the
			// raw parent-less fixture here masked #3071 after the real callback refused it.
			for _, stored := range api.eventStore.GetAllEventsRaw(sid) {
				if stored.ID == event.ID {
					api.observeUsageLimitFailure(sid, stored)
				}
			}
			turns, err := api.readConversationTurnQueue(ctx, user)
			if err != nil {
				t.Fatal(err)
			}
			expected := scenario != "fresh_past" && scenario != "too_overdue" && scenario != "child" && scenario != "other_turn" && scenario != "different_native" && scenario != "missing_reset" && scenario != "stopped_before_failure" && scenario != "unowned_native" && scenario != "child_root_parent" && scenario != "foreign_root_parent" && scenario != "stale_foreground_observer"
			quotaTurns, notices := 0, 0
			groups := map[string]bool{}
			for _, turn := range turns {
				if turn.UsageLimitRecoveryID != "" {
					groups[turn.UsageLimitRecoveryID] = true
				}
			}
			quotaTurns = len(groups)
			for _, e := range api.eventStore.GetAllEventsRaw(sid) {
				if e.Type == "usage_limit_notice" {
					notices++
				}
			}
			want := 0
			if expected {
				want = 1
			}
			if quotaTurns != want || notices != want {
				t.Fatalf("queued=%d notices=%d want=%d", quotaTurns, notices, want)
			}
			status := api.usageLimitQueueStatus(ctx, user, sid, turns)
			if expected && (status.State != "scheduled" || status.EventID != event.ID || status.RetryAt == nil) {
				t.Fatalf("missing scheduled projection: %+v", status)
			}
			if !expected && status.State != "not_scheduled" {
				t.Fatalf("false recovery promise: %+v", status)
			}
			if !expected {
				return
			}
			if scenario == "cancel_recovery" {
				// The real owner endpoint exposes only durable state. Neither another
				// person nor a token/scoped caller may cancel this person's recovery.
				for _, claims := range []*UserClaims{{UserID: "other"}, {UserID: user, AccessToken: &accesstokens.Token{ID: "token"}}, {UserID: user, Scope: "restricted"}} {
					r := httptest.NewRequest(http.MethodDelete, "/queued", nil)
					r = mux.SetURLVars(r, map[string]string{"session_id": sid, "message_id": status.MessageID})
					r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
					w := httptest.NewRecorder()
					api.handleRemoveQueuedConversationMessage(w, r)
					if w.Code < 400 {
						t.Fatalf("foreign/scoped recovery cancelled: %d", w.Code)
					}
				}
				r := httptest.NewRequest(http.MethodGet, "/queued", nil).WithContext(ctx)
				r = mux.SetURLVars(r, map[string]string{"session_id": sid})
				w := httptest.NewRecorder()
				api.handleQueuedConversationMessages(w, r)
				var snapshot struct {
					Recovery usageLimitRecoveryStatus `json:"usage_limit_recovery"`
				}
				if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &snapshot) != nil || !snapshot.Recovery.CanCancel || snapshot.Recovery.State != "scheduled" {
					t.Fatalf("bad owner status: %s", w.Body)
				}
				r = httptest.NewRequest(http.MethodDelete, "/queued", nil).WithContext(ctx)
				r = mux.SetURLVars(r, map[string]string{"session_id": sid, "message_id": status.MessageID})
				w = httptest.NewRecorder()
				api.handleRemoveQueuedConversationMessage(w, r)
				if w.Code != http.StatusAccepted {
					t.Fatalf("owner cancel: %d %s", w.Code, w.Body)
				}
				remaining, err := api.readConversationTurnQueue(ctx, user)
				if err != nil || api.usageLimitQueueStatus(ctx, user, sid, remaining).State != "not_scheduled" {
					t.Fatal("cancelled recovery still promised")
				}
				return
			}
			if strings.Contains(files[restartContinuePath(user)], req.Query) {
				t.Fatal("recovery stored original prompt")
			}
			if scenario == "stopped" || scenario == "incoming_stopped" {
				api.markSessionStopped(sid)
			}
			if scenario == "person_queued" {
				personCtx := context.WithValue(ctx, chatSubmissionContextKey{}, chatSubmissionContext{ID: "person-next", ComposerInput: true})
				if _, _, err := api.enqueueConversationTurn(personCtx, user, sid, QueryRequest{Query: "new task", AgentMode: "multi-agent"}); err != nil {
					t.Fatal(err)
				}
			}
			// New API and journal replay: no runtime timer/memory survives.
			fresh := newAPI()
			turns, err = fresh.readConversationTurnQueue(ctx, user)
			if err != nil {
				t.Fatal(err)
			}
			turns, err = fresh.prepareRestartContinues(ctx, user, turns)
			if err != nil {
				t.Fatal(err)
			}
			turns, err = fresh.pruneUsageLimitContinues(ctx, user, turns)
			if err != nil {
				t.Fatal(err)
			}
			if err := fresh.writeConversationTurnQueue(ctx, user, turns); err != nil {
				t.Fatal(err)
			}
			for _, turn := range turns {
				if turn.ID == original.ID && !incoming {
					t.Fatal("failed original replayed")
				}
				if incoming && turn.UsageLimitRecoveryID != "" {
					if turn.Request.Query != req.Query || turn.Request.AgentMessageInbox != req.AgentMessageInbox || turn.Principal.DeliveredMessage["reply_address"] != "original-inbox" {
						t.Fatal("incoming retry lost its message or original reply authority")
					}
				}
				if turn.Request.TriggeredBy == usageLimitContinueTrigger {
					if turn.Request.Query != usageLimitContinueMessage || !turn.Request.IsAutoNotification || turn.Request.AgentMessageInbox != "" || turn.Request.Provider != req.Provider {
						t.Fatalf("unsafe continuation: %+v", turn.Request)
					}
				}
			}
			if scenario == "future" || scenario == "native_root" || scenario == "foreground_observer" || scenario == "incoming_future" {
				if len(dropStaleWaitingTurns(turns, now.Add(30*time.Minute))) != 1 {
					t.Fatal("waiting recovery pruned")
				}
				if _, ok := fresh.claimNextConversationTurn(ctx, user, sid); ok {
					t.Fatal("claimed before reset")
				}
				return
			}
			if scenario == "stopped" || scenario == "person_queued" || scenario == "incoming_stopped" {
				for _, turn := range turns {
					if turn.UsageLimitRecoveryID != "" {
						t.Fatal("person cancellation lost across restart")
					}
				}
				return
			}
			claimed, ok := fresh.claimNextConversationTurn(ctx, user, sid)
			wantQuery := usageLimitContinueMessage
			if incoming {
				wantQuery = req.Query
			}
			if !ok || claimed.Request.Query != wantQuery {
				t.Fatal("due continuation not claimable")
			}
			if scenario == "incoming_batch" && len(claimed.Batch) != 2 {
				t.Fatal("incoming batch split into more than one retry")
			}
			if !fresh.usageLimitContinuationCurrent(ctx, claimed) {
				t.Fatal("claimed recovery rejected")
			}
			if scenario == "stop_after_claim" {
				fresh.markSessionStopped(sid)
				if fresh.usageLimitContinuationCurrent(ctx, claimed) {
					t.Fatal("Stop after claim did not cancel")
				}
			}
			saved, err := fresh.readRestartContinueRecords(ctx, user)
			if err != nil || !saved.Chats[sid].UsageLimit.Consumed {
				t.Fatal("claim did not consume receipt")
			}
			restarted := newAPI()
			claimedRows, err := restarted.readConversationTurnQueue(ctx, user)
			if err != nil {
				t.Fatal(err)
			}
			queued, err := restarted.prepareRestartContinues(ctx, user, claimedRows)
			if err != nil {
				t.Fatal(err)
			}
			if len(queued) != 0 {
				t.Fatal("claimed recovery replayed after process loss")
			}
			if incoming {
				secondFailure := event
				secondFailure.ID = "second-quota-failure"
				restarted.observeUsageLimitFailure(sid, secondFailure)
				receipt, err := restarted.readRestartContinueRecords(ctx, user)
				if err != nil || receipt.Chats[sid].UsageLimit.EventID != event.ID {
					t.Fatal("incoming message got a second automatic retry")
				}
			}
			// JSON replay of the consumed failure cannot arm another retry.
			raw, _ := json.Marshal(event)
			var replay storeevents.Event
			if err := json.Unmarshal(raw, &replay); err != nil {
				t.Fatal(err)
			}
			restarted.observeUsageLimitFailure(sid, replay)
			after, err := restarted.readRestartContinueRecords(ctx, user)
			if err != nil || !after.Chats[sid].UsageLimit.Consumed {
				t.Fatal("replayed failure rearmed consumed recovery")
			}
		})
	}
}

// PLAT-746: a canceled reset left the shared watching flag set for hours,
// so a newer earlier reset never kicked the real queue at its due time.
func TestEarlierQuotaResetWakesQueueBeforeOldTimer(t *testing.T) {
	api := newConversationTurnQueueTestAPI(map[string]string{})
	const user, sid = "owner", "earlier-usage-reset"
	api.registerConversationQueueOwnerLocked(user, sid)
	read := make(chan struct{}, 1)
	api.internalTurnQueueRead = func(_ context.Context, path string) (string, bool, error) {
		if path == conversationTurnQueuePath(user) {
			select {
			case read <- struct{}{}:
			default:
			}
		}
		return "", false, nil
	}
	api.wakeConversationQueueAt(sid, time.Now().Add(time.Hour))
	api.wakeConversationQueueAt(sid, time.Now().Add(20*time.Millisecond))
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		t.Fatal("earlier reset did not wake the input queue")
	}
}
