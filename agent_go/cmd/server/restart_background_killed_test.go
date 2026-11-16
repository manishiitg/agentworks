package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

// A background wake goes to a chat whose newest event is a normal turn end; that end must not drop it
// (the 22:22 restart lost a wake this way and the chat sat idle). A Stop or a waiting person message still does.
func TestRestartBackgroundWakeSurvivesTheTurnEndItFollows(t *testing.T) {
	for _, scenario := range []string{"normal_end", "stopped_by_person"} {
		t.Run(scenario, func(t *testing.T) {
			const sid = "restart-bg-prune"
			api, _ := restartBGAPI(t)
			now := time.Now()
			restartBGChat(t, api, context.Background(), sid, "", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
			if scenario == "stopped_by_person" {
				api.stoppedSessions = map[string]bool{sid: true}
			}
			wake := queuedConversationTurn{ID: "restart-bg:q-" + sid, UserID: restartBGUser, SessionID: sid, Request: QueryRequest{TriggeredBy: "restart_continue"}}
			kept, _ := api.pruneRestartContinues([]queuedConversationTurn{wake})
			if want := map[bool]int{true: 1}[scenario == "normal_end"]; len(kept) != want {
				t.Fatalf("kept=%d, want %d", len(kept), want)
			}
		})
	}
}

// A normal turn that ends with plan steps still open gets one continuation per distinct open plan; a plan that is
// finished, a turn that failed or was stopped, and a plan from an earlier turn get none.
func TestOpenPlanAtTurnEnd(t *testing.T) {
	plan := func(statuses ...string) *unifiedevents.AgentEvent {
		items := []map[string]string{}
		for i, status := range statuses {
			items = append(items, map[string]string{"id": fmt.Sprint(i), "text": "step " + fmt.Sprint(i), "status": status})
		}
		return unifiedevents.NewAgentEvent(&unifiedevents.GenericEventData{Data: map[string]interface{}{"plan_id": "p", "items": items}})
	}
	for _, tc := range []struct {
		name   string
		events [][2]interface{} // type, data
		want   int
	}{
		{"open_plan_normal_end", [][2]interface{}{{"user_message", nil}, {"coding_agent_plan", plan("completed", "running", "pending")}, {"unified_completion", nil}}, 2},
		// A real turn saves unified_completion and then conversation_end (the 02:05 log: every check said ended=false).
		{"open_plan_with_trailing_conversation_end", [][2]interface{}{{"user_message", nil}, {"coding_agent_plan", plan("pending")}, {"unified_completion", nil}, {"conversation_end", nil}}, 1},
		{"cancelled_after_completion", [][2]interface{}{{"user_message", nil}, {"coding_agent_plan", plan("pending")}, {"unified_completion", nil}, {"context_cancelled", nil}}, 0},
		{"all_done", [][2]interface{}{{"user_message", nil}, {"coding_agent_plan", plan("completed")}, {"unified_completion", nil}}, 0},
		{"turn_failed", [][2]interface{}{{"user_message", nil}, {"coding_agent_plan", plan("pending")}, {"conversation_error", nil}}, 0},
		{"plan_from_earlier_turn", [][2]interface{}{{"user_message", nil}, {"coding_agent_plan", plan("pending")}, {"unified_completion", nil}, {"user_message", nil}, {"unified_completion", nil}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const sid = "open-plan-chat"
			api, _ := restartBGAPI(t)
			if err := api.eventStore.SetSessionPersistenceClass(sid, storeevents.SessionPersistenceInteractiveChat); err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(-time.Minute)
			for i, e := range tc.events {
				var data *unifiedevents.AgentEvent
				if d, ok := e[1].(*unifiedevents.AgentEvent); ok {
					data = d
				}
				api.eventStore.AddEvent(sid, storeevents.Event{ID: fmt.Sprintf("%s-%d", sid, i), Type: e[0].(string), Timestamp: at.Add(time.Duration(i) * time.Second), SessionID: sid, ExecutionKind: "main_agent", ExecutionID: "main:" + sid, Data: data})
			}
			open, ended := api.latestTurnOpenPlan(sid)
			if len(open) != tc.want || (tc.want > 0 && !ended) {
				t.Fatalf("open=%d ended=%v, want %d", len(open), ended, tc.want)
			}
		})
	}
}

// A chat that was already idle at restart with plan steps still open (its turn end ran before this build, so the
// turn-end check never sees it) gets one continuation at startup; a finished plan, a Stop and an old chat get none.
func TestRestartContinuesIdleChatWithOpenPlan(t *testing.T) {
	for _, scenario := range []string{"open_plan", "plan_done", "stopped_by_person", "idle_for_a_day"} {
		t.Run(scenario, func(t *testing.T) {
			const sid = "restart-plan-chat"
			backgroundFollowUps.personWrote(sid)
			defer backgroundFollowUps.personWrote(sid)
			openPlanContinued.mu.Lock()
			delete(openPlanContinued.seen, sid)
			openPlanContinued.mu.Unlock()
			api, ctx := restartBGAPI(t)
			if err := api.eventStore.SetSessionPersistenceClass(sid, storeevents.SessionPersistenceInteractiveChat); err != nil {
				t.Fatal(err)
			}
			if err := api.saveRestartContinueRequest(ctx, restartBGUser, sid, "q-"+sid, QueryRequest{Query: "task", AgentMode: "multi-agent", Provider: "codex-cli", ModelID: "example-model"}); err != nil {
				t.Fatal(err)
			}
			age := 5 * time.Minute
			if scenario == "idle_for_a_day" {
				age = 30 * time.Hour
			}
			status := map[bool]string{true: "completed"}[scenario == "plan_done"]
			if status == "" {
				status = "pending"
			}
			start := time.Now().Add(-age - 5*time.Minute)
			plan := unifiedevents.NewAgentEvent(&unifiedevents.GenericEventData{Data: map[string]interface{}{"plan_id": "p", "items": []map[string]string{{"id": "1", "text": "finish the report", "status": status}}}})
			for i, e := range []struct {
				typ  string
				data *unifiedevents.AgentEvent
			}{{"user_message", unifiedevents.NewAgentEvent(&unifiedevents.UserMessageEvent{Content: "task"})}, {"coding_agent_plan", plan}, {"unified_completion", nil}} {
				api.eventStore.AddEvent(sid, storeevents.Event{ID: fmt.Sprintf("%s-%d", sid, i), Type: e.typ, Timestamp: start.Add(time.Duration(i) * time.Minute), SessionID: sid, ExecutionKind: "main_agent", ExecutionID: "main:" + sid, Data: e.data})
			}
			if scenario == "stopped_by_person" {
				api.stoppedSessions = map[string]bool{sid: true}
			}
			got, err := api.prepareRestartContinues(ctx, restartBGUser, nil)
			if err != nil {
				t.Fatal(err)
			}
			queued := 0
			for _, turn := range got {
				if turn.SessionID == sid && strings.Contains(turn.Request.Query, "finish the report") {
					queued++
				}
			}
			if want := map[bool]int{true: 1}[scenario == "open_plan"]; queued != want {
				t.Fatalf("continuations=%d, want %d", queued, want)
			}
		})
	}
}

// An idle chat with an open AgentBridge request when the app restarts (the turn-end wake cannot run for it, and its
// caller memory is gone) gets one restart continuation; no request, or a Stop, gets none.
func TestRestartContinuesIdleChatWithOpenRequest(t *testing.T) {
	for _, scenario := range []string{"open_request", "network_checkpoint", "no_request", "stopped_by_person"} {
		t.Run(scenario, func(t *testing.T) {
			const sid = "restart-task-chat"
			newTriggerLinkEnv(t)
			t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
			backgroundFollowUps.personWrote(sid)
			defer backgroundFollowUps.personWrote(sid)
			api, ctx := restartBGAPI(t)
			now := time.Now()
			restartBGChat(t, api, ctx, sid, "", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
			conversations := []agentConversation{}
			if scenario == "network_checkpoint" {
				// A request from the saved network lives only in the chat's private checkpoint file (local mode),
				// and the startup scan must find it before any turn has filled the chat's owner in memory.
				t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
				t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
				path, err := desktopTaskPathFor(restartBGUser, sid)
				if err != nil {
					t.Fatal(err)
				}
				task := agentMessage{ID: "task", From: 0, Sequence: 1, MessageType: "request", Delivery: "delivered", TaskProgress: agentTaskProgress{Session: sid}}
				if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(agentMessageStore{Conversations: []agentConversation{{ID: "network:conn:inbox-1", Messages: []agentMessage{task}}}})
				if err = os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			} else if scenario != "no_request" {
				task := agentMessage{ID: "task", From: 0, Sequence: 1, MessageType: "request", Delivery: "delivered", TaskProgress: agentTaskProgress{Session: sid}}
				conversations = append(conversations, agentConversation{ID: "thread", Messages: []agentMessage{task}})
			}
			agentMessageMu.Lock()
			err := saveAgentMessageStore(context.Background(), agentMessageStore{Conversations: conversations})
			agentMessageMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "stopped_by_person" {
				api.stoppedSessions = map[string]bool{sid: true}
			}
			got, err := api.prepareRestartContinues(ctx, restartBGUser, nil)
			if err != nil {
				t.Fatal(err)
			}
			queued := 0
			for _, turn := range got {
				if turn.SessionID == sid && strings.Contains(turn.Request.Query, "still had an open request") {
					queued++
				}
			}
			if want := map[bool]int{true: 1}[scenario == "open_request" || scenario == "network_checkpoint"]; queued != want {
				t.Fatalf("continuations=%d, want %d", queued, want)
			}
		})
	}
}
