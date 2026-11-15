package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const restartBGUser = "owner"

func restartBGAPI(t *testing.T) (*StreamingAPI, context.Context) {
	t.Helper()
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { journal.Close() })
	api := newConversationTurnQueueTestAPI(map[string]string{})
	api.eventStore = storeevents.NewEventStore(100)
	api.eventStore.SetDurableJournal(journal)
	t.Cleanup(api.eventStore.Stop)
	return api, context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: restartBGUser, Username: restartBGUser})
}

// restartBGChat saves a chat between turns: a restart record, a background command started at startedAt and whose
// state is `state`, and a finished turn at lastActive.
func restartBGChat(t *testing.T, api *StreamingAPI, ctx context.Context, sid, state string, startedAt, lastActive time.Time) {
	t.Helper()
	if err := api.eventStore.SetSessionPersistenceClass(sid, storeevents.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	request := QueryRequest{Query: "original task", AgentMode: "multi-agent", Provider: "codex-cli", ModelID: "example-model"}
	if err := api.saveRestartContinueRequest(ctx, restartBGUser, sid, "q-"+sid, request); err != nil {
		t.Fatal(err)
	}
	add := func(id, kind string, at time.Time, data *unifiedevents.AgentEvent) {
		api.eventStore.AddEvent(sid, storeevents.Event{ID: sid + id, Type: kind, Timestamp: at, SessionID: sid, ExecutionKind: "main_agent", ExecutionID: "main:" + sid, Data: data})
	}
	add("input", "user_message", startedAt, unifiedevents.NewAgentEvent(&unifiedevents.UserMessageEvent{Content: "original task"}))
	if state != "" {
		record := llmtypes.NativeRecord{ID: sid + "work", Provider: "codex-cli", SessionID: "native-" + sid, Kind: "background_command", State: state, TaskID: "task-1", WorkID: "work-" + sid, CommandLabel: "./deploy.sh verify mine", StartedAt: startedAt, Timestamp: startedAt}
		add("work", string(unifiedevents.CodingAgentNativeActivity), startedAt, unifiedevents.NewAgentEvent(unifiedevents.NewCodingAgentNativeActivity(record)))
	}
	add("answer", "unified_completion", lastActive, nil) // the turn ended: the chat is between turns
}

func restartBGWakes(turns []queuedConversationTurn) (sessions []string) {
	for _, turn := range turns {
		if strings.HasPrefix(turn.Request.Query, "[Background command stopped]") {
			sessions = append(sessions, turn.SessionID)
		}
	}
	return sessions
}

// A chat between turns whose background command died with the app gets one wake turn, and the dead row is closed;
// a chat whose work finished, or that the person stopped, gets none (the 20:55 restart left five chats idle).
func TestRestartWakesChatWhoseBackgroundCommandDied(t *testing.T) {
	for _, scenario := range []string{"killed", "finished_before", "no_background", "stopped_by_person"} {
		t.Run(scenario, func(t *testing.T) {
			const sid = "restart-bg-chat"
			backgroundFollowUps.personWrote(sid)
			defer backgroundFollowUps.personWrote(sid)
			api, ctx := restartBGAPI(t)
			now := time.Now()
			state := "started"
			switch scenario {
			case "finished_before":
				state = "completed"
			case "no_background":
				state = ""
			}
			restartBGChat(t, api, ctx, sid, state, now.Add(-10*time.Minute), now.Add(-5*time.Minute))
			if scenario == "stopped_by_person" {
				api.stoppedSessions = map[string]bool{sid: true} // the person pressed Stop
			}
			got, err := api.prepareRestartContinues(ctx, restartBGUser, nil)
			if err != nil {
				t.Fatal(err)
			}
			wakes := restartBGWakes(got)
			if want := map[bool]int{true: 1}[scenario == "killed"]; len(wakes) != want {
				t.Fatalf("wakes=%d, want %d", len(wakes), want)
			}
			if scenario == "killed" {
				if !strings.Contains(got[0].Request.Query, "./deploy.sh verify mine") || got[0].Request.Provider != "codex-cli" {
					t.Fatalf("wake lost the command label or the chat's runtime: %q", got[0].Request.Query)
				}
				if left := api.restartKilledBackground(sid); len(left) != 0 {
					t.Fatalf("the dead command still shows as running: %+v", left)
				}
				if again, err := api.prepareRestartContinues(ctx, restartBGUser, got); err != nil || len(again) != len(got) {
					t.Fatal("a second startup queued the wake again")
				}
			}
		})
	}
}

// One restart must not start model turns in a pile of long-idle chats: only chats active in the last 6 hours with
// work started in the last 6 hours are woken, at most 10, newest first; every dead row is still closed.
func TestRestartBackgroundWakesAreRecentAndCapped(t *testing.T) {
	api, ctx := restartBGAPI(t)
	now := time.Now()
	for i := 0; i < 12; i++ {
		sid := fmt.Sprintf("recent-%02d", i)
		backgroundFollowUps.personWrote(sid)
		restartBGChat(t, api, ctx, sid, "started", now.Add(-time.Hour), now.Add(-time.Duration(i+1)*time.Minute))
	}
	restartBGChat(t, api, ctx, "idle-chat", "started", now.Add(-time.Hour), now.Add(-8*time.Hour))    // chat idle for 8 hours
	restartBGChat(t, api, ctx, "old-work", "started", now.Add(-9*time.Hour), now.Add(-2*time.Minute)) // work started 9 hours ago
	got, err := api.prepareRestartContinues(ctx, restartBGUser, nil)
	if err != nil {
		t.Fatal(err)
	}
	woken := map[string]bool{}
	for _, sid := range restartBGWakes(got) {
		woken[sid] = true
	}
	if len(woken) != restartBackgroundWakeCap || woken["idle-chat"] || woken["old-work"] || woken["recent-10"] || woken["recent-11"] || !woken["recent-00"] {
		t.Fatalf("wakes=%v: want the 10 newest recent chats only", woken)
	}
	for _, sid := range []string{"recent-11", "idle-chat", "old-work"} {
		if left := api.restartKilledBackground(sid); len(left) != 0 {
			t.Fatalf("%s: a skipped chat's dead command still shows as running", sid)
		}
	}
}

// An active Codex goal that was between turns when the app restarted resumes with one continuation; a goal the
// person paused, a chat with no goal and a chat the person stopped stay idle. A continuation that failed in an
// earlier restart (kept in the queue with an error, never dispatched) is replaced, not mistaken for a waiting one.
func TestRestartResumesActiveGoalBetweenTurns(t *testing.T) {
	for _, scenario := range []string{"active", "paused_by_person", "no_goal", "stopped_by_person", "failed_continue_in_queue"} {
		t.Run(scenario, func(t *testing.T) {
			const sid = "restart-goal-chat"
			backgroundFollowUps.personWrote(sid)
			defer backgroundFollowUps.personWrote(sid)
			api, ctx := restartBGAPI(t)
			now := time.Now()
			restartBGChat(t, api, ctx, sid, "", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
			if scenario != "no_goal" {
				status := map[bool]string{true: "paused"}[scenario == "paused_by_person"]
				if status == "" {
					status = "active"
				}
				update := llmtypes.NativeGoalUpdate{Provider: "codex-cli", NativeSessionID: "native", Goal: &llmtypes.NativeGoal{Objective: "Finish the task", Status: status}}
				if err := api.projectNativeGoal(sid, update, "test"); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "stopped_by_person" {
				api.stoppedSessions = map[string]bool{sid: true}
			}
			var queued []queuedConversationTurn
			if scenario == "failed_continue_in_queue" {
				failed := queuedConversationTurn{ID: "restart:old", UserID: restartBGUser, SessionID: sid, Request: QueryRequest{TriggeredBy: "restart_continue"}, DeliveryError: queuedTurnFailureMessage, CreatedAt: now.Add(-time.Hour)}
				queued = []queuedConversationTurn{failed}
			}
			got, err := api.prepareRestartContinues(ctx, restartBGUser, queued)
			if err != nil {
				t.Fatal(err)
			}
			continues := 0
			for _, turn := range got {
				if turn.SessionID == sid && turn.Request.TriggeredBy == "restart_continue" {
					continues++
					if turn.DeliveryError != "" {
						t.Fatal("the failed continuation was kept instead of replaced")
					}
				}
			}
			want := map[bool]int{true: 1}[scenario == "active" || scenario == "failed_continue_in_queue"]
			if continues != want {
				t.Fatalf("continuations=%d, want %d", continues, want)
			}
			if want == 1 {
				if again, err := api.prepareRestartContinues(ctx, restartBGUser, got); err != nil || len(again) != len(got) {
					t.Fatal("a second startup queued the continuation again")
				}
			}
		})
	}
}

// Shutdown cancels the turn and Codex saves the goal as paused (turn_canceled): that pause belongs to the restart, so
// the goal is active again for the continuation. A pause the person chose stays paused.
func TestRestartReopensGoalPausedByTheCancelOnly(t *testing.T) {
	for _, tc := range []struct{ reason, want string }{{"turn_canceled", "active"}, {"intent_pause", "paused"}} {
		t.Run(tc.reason, func(t *testing.T) {
			const sid = "restart-goal-cancel"
			api, ctx := restartBGAPI(t)
			if err := api.eventStore.SetSessionPersistenceClass(sid, storeevents.SessionPersistenceInteractiveChat); err != nil {
				t.Fatal(err)
			}
			update := llmtypes.NativeGoalUpdate{Provider: "codex-cli", NativeSessionID: "native", Goal: &llmtypes.NativeGoal{Objective: "Finish the task", Status: "paused"}}
			if err := api.projectNativeGoal(sid, update, tc.reason); err != nil {
				t.Fatal(err)
			}
			api.reopenGoalPausedByRestart(ctx, sid)
			got, err := api.latestNativeGoal(ctx, sid)
			if err != nil || got == nil || got.Goal == nil || got.Goal.Status != tc.want || got.Goal.Objective != "Finish the task" {
				t.Fatalf("goal after restart = %+v, want %s", got, tc.want)
			}
		})
	}
}
