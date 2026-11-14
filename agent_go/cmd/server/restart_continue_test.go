package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// PLAT-746-919: reconstruct the API/store from durable events and queue JSON;
// nothing executes a model, and an already-started original is never replayed.
func TestRestartContinuesInterruptedChatWithoutReplayingStartedInput(t *testing.T) {
	for _, scenario := range []string{"started", "agent_message", "schedule", "stopped", "finished", "error", "person_queued", "graceful_shutdown", "stopped_then_shutdown", "finished_during_shutdown"} {
		t.Run(scenario, func(t *testing.T) {
			const user, sid = "owner", "restart-chat"
			backgroundFollowUps.personWrote(sid)
			defer backgroundFollowUps.personWrote(sid)
			files := map[string]string{}
			journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			newAPI := func() *StreamingAPI {
				api := newConversationTurnQueueTestAPI(files)
				api.eventStore = storeevents.NewEventStore(100)
				api.eventStore.SetDurableJournal(journal)
				if err := api.eventStore.SetSessionPersistenceClass(sid, storeevents.SessionPersistenceInteractiveChat); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(api.eventStore.Stop)
				return api
			}
			add := func(api *StreamingAPI, id, kind string) {
				event := storeevents.Event{ID: id, Type: kind, Timestamp: time.Now(), SessionID: sid, ExecutionKind: "main_agent", ExecutionID: "main:" + sid}
				if kind == "user_message" {
					event.Data = unifiedevents.NewAgentEvent(&unifiedevents.UserMessageEvent{Content: "original task"})
				}
				api.eventStore.AddEvent(sid, event)
			}
			api := newAPI()
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user, Username: user})
			request := QueryRequest{Query: "original task", AgentMode: "multi-agent", Provider: "codex-cli", ModelID: "example-model"}
			if scenario == "schedule" {
				request.TriggeredBy = "cron"
			}
			if scenario == "agent_message" {
				ctx = context.WithValue(ctx, deliveredAgentMessageKey{}, map[string]interface{}{"source": "agent_message"})
				request.AgentMessageInbox = "incoming"
			}
			if err := api.saveRestartContinueRequest(ctx, user, sid, "original-query", request); err != nil {
				t.Fatal(err)
			}
			add(api, "input", "user_message")
			now := time.Now()
			turns := []queuedConversationTurn{{ID: "original-queue-row", UserID: user, SessionID: sid, Request: request, StartedAt: &now}}
			if scenario == "person_queued" {
				turns = append(turns, queuedConversationTurn{ID: "person-next", UserID: user, SessionID: sid, SubmissionID: "typed-next", ComposerInput: true, Request: QueryRequest{Query: "next task", AgentMode: "multi-agent"}})
			}
			if strings.Contains(scenario, "shutdown") {
				turnCtx, cancel := context.WithCancelCause(ctx)
				api.shutdownTurnCancels.Store(sid, cancel)
				api.agentCancelFuncs = map[string]context.CancelFunc{sid: func() { cancel(context.Canceled) }}
				if scenario == "stopped_then_shutdown" {
					cancel(context.Canceled) // person's Stop wins before the app quits
				}
				api.cancelActiveWorkForShutdown()
				observer := &restartAwareEventObserver{EventObserver: storeevents.NewEventObserver(api.eventStore, sid)}
				if scenario != "finished_during_shutdown" {
					if err := observer.HandleEvent(turnCtx, unifiedevents.NewAgentEvent(unifiedevents.NewContextCancelledEvent(1, "context canceled", time.Second))); err != nil {
						t.Fatal(err)
					}
				}
				status := "error"
				if scenario == "finished_during_shutdown" {
					status = "completed"
				}
				completion := unifiedevents.NewUnifiedCompletionEvent("session", "simple", "", "", status, time.Second, 1)
				if err := observer.HandleEvent(turnCtx, unifiedevents.NewAgentEvent(completion)); err != nil {
					t.Fatal(err)
				}
				if api.eventStore.PersistedTurnOpen(sid) {
					t.Fatal("graceful shutdown terminal markers should remain closed")
				}
			}
			switch scenario {
			case "stopped":
				api.emitInterruptedTurnEnd(sid, "stop_after_restart")
			case "finished":
				add(api, "answer", "unified_completion")
			case "error":
				add(api, "error", "conversation_error")
			}
			restarted := newAPI()
			got, err := restarted.prepareRestartContinues(ctx, user, turns)
			if err != nil {
				t.Fatal(err)
			}
			expected := scenario == "started" || scenario == "agent_message" || scenario == "schedule" || scenario == "graceful_shutdown"
			notes := 0
			for _, turn := range got {
				if turn.Request.Query == "original task" || turn.ID == "original-queue-row" {
					t.Fatal("started original replayed")
				}
				if turn.Request.Query == restartContinueMessage {
					notes++
					if !turn.Request.IsAutoNotification || turn.Request.Provider != request.Provider {
						t.Fatal("continuation lost its synthetic marker or runtime")
					}
				}
			}
			if expected && notes != 1 || !expected && notes != 0 {
				t.Fatalf("continuations=%d expected=%t", notes, expected)
			}
			if scenario == "person_queued" {
				if len(got) != 1 || got[0].ID != "person-next" {
					t.Fatal("person's pending input was changed")
				}
				if err := restarted.writeConversationTurnQueue(ctx, user, got); err != nil {
					t.Fatal(err)
				}
				if _, ok := restarted.claimNextConversationTurn(ctx, user, sid); !ok {
					t.Fatal("pending person input not claimable")
				}
				if _, ok := restarted.claimNextConversationTurn(ctx, user, sid); ok {
					t.Fatal("pending person input claimed twice")
				}
			}
			again, err := restarted.prepareRestartContinues(ctx, user, got)
			if err != nil || len(again) != len(got) {
				t.Fatal("second recovery duplicated continuation")
			}
			if !expected {
				return
			}
			if _, changed := restarted.pruneRestartContinues(got); changed {
				t.Fatal("journal-replayed app_restart settlement dropped its own continuation")
			}
			// PLAT-746: the fourth restart in five minutes is refused even though
			// the native background-follow-up budget now permits more than three.
			for cycle := 2; cycle <= 5; cycle++ {
				fresh := newAPI()
				backgroundFollowUps.personWrote(sid) // simulate an empty process-local map
				request.IsAutoNotification = true
				document, err := fresh.readRestartContinueRecords(ctx, user)
				if err != nil {
					t.Fatal(err)
				}
				if cycle == 5 {
					// Expire only the restart window; background wake counts remain.
					record := document.Chats[sid]
					for i := range record.RestartContinues {
						record.RestartContinues[i] = time.Now().Add(-restartContinueWindow)
					}
					document.Chats[sid] = record
					if err := fresh.writeRestartContinueRecords(ctx, user, document); err != nil {
						t.Fatal(err)
					}
				}
				backgroundFollowUps.restore(sid, document.Chats[sid].FollowUps)
				if err := fresh.saveRestartContinueRequest(ctx, user, sid, fmt.Sprint("continued-", cycle), request); err != nil {
					t.Fatal(err)
				}
				add(fresh, fmt.Sprint("continued-input-", cycle), "user_message")
				next, err := fresh.prepareRestartContinues(ctx, user, nil)
				if err != nil {
					t.Fatal(err)
				}
				if cycle != 4 && len(next) != 1 || cycle == 4 && len(next) != 0 {
					t.Fatalf("cycle=%d continuations=%d", cycle, len(next))
				}
				if cycle == 4 {
					notices := 0
					for _, event := range fresh.eventStore.GetAllEventsRaw(sid) {
						if event.Type == "restart_continue_notice" {
							notices++
						}
					}
					if notices != 1 {
						t.Fatalf("restart limit notices=%d, want 1", notices)
					}
					if _, err := fresh.prepareRestartContinues(ctx, user, nil); err != nil {
						t.Fatal(err)
					}
					after := 0
					for _, event := range fresh.eventStore.GetAllEventsRaw(sid) {
						if event.Type == "restart_continue_notice" {
							after++
						}
					}
					if after != notices {
						t.Fatal("restart limit notice repeated")
					}
				}
				saved, err := fresh.readRestartContinueRecords(ctx, user)
				expectedAttempts := 3
				if cycle == 2 {
					expectedAttempts = 2
				}
				if cycle == 5 {
					expectedAttempts = 1
				}
				if err != nil || len(saved.Chats[sid].RestartContinues) != expectedAttempts {
					t.Fatal("restart reset/bypassed restart limit")
				}
			}
		})
	}
}

func TestRestartContinuationStorageDoesNotPersistPromptOrResolvedKey(t *testing.T) {
	files := map[string]string{}
	api := newConversationTurnQueueTestAPI(files)
	api.eventStore = storeevents.NewEventStore(10)
	defer api.eventStore.Stop()
	key := "not-a-real-key"
	req := QueryRequest{Query: "original-private-prompt", Message: "original-private-alias", AgentMode: "multi-agent", LLMConfig: &orchestrator.LLMConfig{}}
	req.LLMConfig.Primary.APIKey = &key
	if err := api.saveRestartContinueRequest(context.Background(), "owner", "chat", "query", req); err != nil {
		t.Fatal(err)
	}
	raw := files[restartContinuePath("owner")]
	for _, forbidden := range []string{key, req.Query, req.Message} {
		if strings.Contains(raw, forbidden) {
			t.Fatal("restart storage retained original content or resolved credential")
		}
	}
}
