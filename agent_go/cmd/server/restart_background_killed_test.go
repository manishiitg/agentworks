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
