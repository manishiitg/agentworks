package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #4604: an Antigravity chat's /goal uses agy's own /goal. AgentWorks answers show, pause and clear itself (agy has
// no pause, resume or goal read) and sends set and Start again (resume, with the stored objective) to agy. The
// chat's Stop records a working goal as stopped.
func TestAgyGoalCommandsAnsweredByAgentWorks(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]}]}`)
	sid := "agy-goal-commands"
	api, store := newDurableChatAPI(t, sid, "owner")
	store.SetEventPreparer(api.prepareNativeGoalEvent)
	if !nativeGoalSupported("agy-cli") || !nativeGoalSupported("codex-cli") || !nativeGoalSupported("claude-code") || nativeGoalSupported("cursor-cli") {
		t.Fatal("goal programs changed")
	}
	run := func(action string) (bool, string, llmtypes.NativeGoalCommand) {
		t.Helper()
		command := llmtypes.NativeGoalCommand{SessionID: sid, Action: action}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		handled := api.prepareAgyGoalCommand(rec, req, sid, agyGoalProvider, &command, true)
		var body struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return handled, body.Message, command
	}
	latest := func() *llmtypes.NativeGoalUpdate {
		t.Helper()
		update, err := api.latestNativeGoal(context.Background(), sid)
		if err != nil {
			t.Fatal(err)
		}
		return update
	}

	if handled, message, _ := run("show"); !handled || !strings.Contains(message, "No goal is set") {
		t.Fatalf("show with no goal: %t %q", handled, message)
	}
	if handled, message, _ := run("resume"); !handled || !strings.Contains(message, "no goal to start again") {
		t.Fatalf("resume with no goal: %t %q", handled, message)
	}
	if handled, _, _ := run("set"); handled {
		t.Fatal("set did not go to agy")
	}

	working := llmtypes.NativeGoalUpdate{Provider: "agy-cli", NativeSessionID: "agy_sess", Goal: &llmtypes.NativeGoal{Objective: "make the tests pass", Status: "active"}}
	if err := api.projectNativeGoal(sid, working, "intent_set"); err != nil {
		t.Fatal(err)
	}
	if handled, message, _ := run("pause"); !handled || !strings.Contains(message, "can't pause") {
		t.Fatalf("pause with no running turn: %t %q", handled, message)
	}
	api.pauseNativeGoalForStop(sid)
	if update := latest(); update == nil || update.Goal == nil || update.Goal.Status != "paused" || update.Reason != "person_stop" {
		t.Fatalf("after Stop = %+v", update)
	}
	if handled, message, _ := run("show"); !handled || !strings.Contains(message, "stopped") || !strings.Contains(message, "make the tests pass") {
		t.Fatalf("show: %t %q", handled, message)
	}
	if handled, _, command := run("resume"); handled || command.Objective != "make the tests pass" {
		t.Fatalf("Start again: handled=%t objective=%q", handled, command.Objective)
	}

	done := working
	done.Goal = &llmtypes.NativeGoal{Objective: "make the tests pass", Status: "complete"}
	if err := api.projectNativeGoal(sid, done, "intent_set"); err != nil {
		t.Fatal(err)
	}
	if handled, message, _ := run("resume"); !handled || !strings.Contains(message, "already done") {
		t.Fatalf("resume a done goal: %t %q", handled, message)
	}
	if handled, message, _ := run("clear"); !handled || message != "Goal cleared." {
		t.Fatalf("clear: %t %q", handled, message)
	}
	if update := latest(); update == nil || update.Goal != nil {
		t.Fatalf("after clear = %+v", update)
	}
}

// #4626: a Claude Code chat's goal is kept here the same way: Stop records it stopped, and pause is answered.
func TestClaudeGoalCommandsAnsweredByAgentWorks(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]}]}`)
	sid := "claude-goal-commands"
	api, store := newDurableChatAPI(t, sid, "owner")
	store.SetEventPreparer(api.prepareNativeGoalEvent)
	run := func(action string) (bool, string, llmtypes.NativeGoalCommand) {
		t.Helper()
		command := llmtypes.NativeGoalCommand{SessionID: sid, Action: action}
		rec := httptest.NewRecorder()
		handled := api.prepareAgyGoalCommand(rec, httptest.NewRequest(http.MethodPost, "/", nil), sid, claudeGoalProvider, &command, true)
		var body struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return handled, body.Message, command
	}
	working := llmtypes.NativeGoalUpdate{Provider: claudeGoalProvider, NativeSessionID: "claude-native", Goal: &llmtypes.NativeGoal{Objective: "ship the fix", Status: "active"}}
	if err := api.projectNativeGoal(sid, working, "intent_set"); err != nil {
		t.Fatal(err)
	}
	if handled, message, _ := run("pause"); !handled || message != "Claude Code can't pause a goal. Stop ends it; Start again picks it up later." {
		t.Fatalf("pause: %t %q", handled, message)
	}
	api.pauseNativeGoalForStop(sid)
	update, err := api.latestNativeGoal(context.Background(), sid)
	if err != nil || update == nil || update.Goal == nil || update.Goal.Status != "paused" || update.Provider != claudeGoalProvider {
		t.Fatalf("after Stop = %+v %v", update, err)
	}
	if handled, _, command := run("resume"); handled || command.Objective != "ship the fix" {
		t.Fatalf("Start again: handled=%t objective=%q", handled, command.Objective)
	}
}
