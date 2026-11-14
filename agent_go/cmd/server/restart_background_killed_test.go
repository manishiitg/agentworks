package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A chat between turns whose background command died with the app gets one wake turn, and the dead row is closed;
// a chat whose work finished, or that the person stopped, gets none (the 20:55 restart left five chats idle).
func TestRestartWakesChatWhoseBackgroundCommandDied(t *testing.T) {
	for _, scenario := range []string{"killed", "finished_before", "no_background", "stopped_by_person"} {
		t.Run(scenario, func(t *testing.T) {
			const user, sid = "owner", "restart-bg-chat"
			backgroundFollowUps.personWrote(sid)
			defer backgroundFollowUps.personWrote(sid)
			files := map[string]string{}
			journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			api := newConversationTurnQueueTestAPI(files)
			api.eventStore = storeevents.NewEventStore(100)
			api.eventStore.SetDurableJournal(journal)
			if err := api.eventStore.SetSessionPersistenceClass(sid, storeevents.SessionPersistenceInteractiveChat); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(api.eventStore.Stop)
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user, Username: user})
			request := QueryRequest{Query: "original task", AgentMode: "multi-agent", Provider: "codex-cli", ModelID: "example-model"}
			if err := api.saveRestartContinueRequest(ctx, user, sid, "original-query", request); err != nil {
				t.Fatal(err)
			}
			add := func(id, kind string, data *unifiedevents.AgentEvent) {
				api.eventStore.AddEvent(sid, storeevents.Event{ID: id, Type: kind, Timestamp: time.Now(), SessionID: sid, ExecutionKind: "main_agent", ExecutionID: "main:" + sid, Data: data})
			}
			activity := func(id, state string) {
				record := llmtypes.NativeRecord{ID: id, Provider: "codex-cli", SessionID: "native", Kind: "background_command", State: state, TaskID: "task-1", WorkID: "work-1", CommandLabel: "./deploy.sh verify mine"}
				add(id, string(unifiedevents.CodingAgentNativeActivity), unifiedevents.NewAgentEvent(unifiedevents.NewCodingAgentNativeActivity(record)))
			}
			add("input", "user_message", unifiedevents.NewAgentEvent(&unifiedevents.UserMessageEvent{Content: "original task"}))
			if scenario != "no_background" {
				activity("started", "started")
			}
			if scenario == "finished_before" {
				activity("done", "completed")
			}
			add("answer", "unified_completion", nil) // the turn ended: the chat is between turns
			if scenario == "stopped_by_person" {
				api.stoppedSessions = map[string]bool{sid: true} // the person pressed Stop
			}
			got, err := api.prepareRestartContinues(ctx, user, nil)
			if err != nil {
				t.Fatal(err)
			}
			wakes := 0
			for _, turn := range got {
				if strings.HasPrefix(turn.Request.Query, "[Background command stopped]") {
					wakes++
					if !strings.Contains(turn.Request.Query, "./deploy.sh verify mine") || turn.Request.Provider != request.Provider {
						t.Fatalf("wake lost the command label or the chat's runtime: %q", turn.Request.Query)
					}
				}
			}
			want := 0
			if scenario == "killed" {
				want = 1
			}
			if wakes != want {
				t.Fatalf("wakes=%d, want %d", wakes, want)
			}
			if scenario == "killed" {
				if left := api.restartKilledBackground(sid); len(left) != 0 {
					t.Fatalf("the dead command still shows as running: %+v", left)
				}
				again, err := api.prepareRestartContinues(ctx, user, got)
				if err != nil || len(again) != len(got) {
					t.Fatal("a second startup queued the wake again")
				}
			}
		})
	}
}
