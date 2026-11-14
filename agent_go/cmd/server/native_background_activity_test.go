package server

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativeusage"
)

// PLAT-746-941: concurrent tasks never inherit a parent's or each other's
// totals; completed task facts contain their start/end after journal replay.
func TestNativeBackgroundTaskFactsKeepUsageAndRunsSeparate(t *testing.T) {
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	store := storeevents.NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	store.SetSessionOwner("chat", "person")
	if err = store.SetSessionPersistenceClass("chat", storeevents.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{eventStore: store}
	start := time.Now().Add(-3 * time.Minute).UTC().Truncate(time.Second)
	task := func(id string) llmtypes.NativeRecord {
		return llmtypes.NativeRecord{ID: id, Provider: "claude-code", SessionID: "native-parent", TaskID: id, TaskRunID: "1", Kind: "helper", State: "started", Timestamp: start}
	}
	queued := task("queued")
	queued.State = "proposed"
	if api.nativeBackgroundFacts("chat", queued).WorkID != "" {
		t.Fatal("a proposed task was labelled running")
	}
	typed := task("a")
	typed.HelperType, typed.HelperDescription, typed.HelperMode, typed.CurrentStep = "Explore", "Find routing code", "foreground", "Grepping coding.go"
	a := api.nativeBackgroundFacts("chat", typed)
	progress := task("a")
	progress.State = "progress"
	if got := api.nativeBackgroundFacts("chat", progress); got.HelperType != "Explore" || got.HelperDescription != "Find routing code" || got.CurrentStep != "Grepping coding.go" || got.HelperMode != "foreground" {
		t.Fatal("helper progress lost its native labels")
	}
	b := task("b")
	reported := int64(500)
	b.Tokens = &reported
	b.TokenSource = "task_report"
	b = api.nativeBackgroundFacts("chat", b)
	shell := task("shell")
	shell.Kind = "background_command"
	shell.CommandLabel = "deploy.sh verify"
	shell.BackgroundMode = "explicit"
	shell = api.nativeBackgroundFacts("chat", shell)
	shellProgress := shell
	shellProgress.CommandLabel, shellProgress.BackgroundMode = "", ""
	shellProgress.State = "progress"
	if got := api.nativeBackgroundFacts("chat", shellProgress); got.CommandLabel != shell.CommandLabel || got.BackgroundMode != shell.BackgroundMode {
		t.Fatal("task progress lost the shell classification or redacted command")
	}
	api.projectNativeBackgroundUsage("chat", "claude-code", "native-parent", "account", []nativeusage.Record{
		{ID: "one", NativeSessionID: "child-a", TaskID: "a", Timestamp: start.Add(time.Second), Input: 100, Output: 20, CacheRead: 30},
		{ID: "one", NativeSessionID: "child-a", TaskID: "a", Timestamp: start.Add(time.Second), Input: 100, Output: 20, CacheRead: 30},
		{ID: "parent", NativeSessionID: "native-parent", Timestamp: start.Add(time.Second), Input: 99999, Output: 99999},
		{ID: "b", NativeSessionID: "child-b", TaskID: "b", Timestamp: start.Add(time.Second), Input: 900, Output: 900},
	})
	facts := api.activeNativeBackground("chat")
	for _, r := range facts {
		switch r.TaskID {
		case "a":
			if r.Tokens == nil || *r.Tokens != 150 {
				t.Fatalf("a usage=%v", r.Tokens)
			}
			a = r
		case "b":
			if r.Tokens == nil || *r.Tokens != 500 {
				t.Fatal("task-reported usage replaced with another scope")
			}
		case "shell":
			if r.Tokens != nil {
				t.Fatal("shell inherited model tokens")
			}
		}
	}
	a.State = "finished"
	a.Timestamp = start.Add(182 * time.Second)
	a = api.nativeBackgroundFacts("chat", a)
	if err = api.saveNativeCodingActivity("chat", a); err != nil {
		t.Fatal(err)
	}
	page, err := journal.ReadPage("chat", storeevents.DurableEventPageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := json.Marshal(page.Events)
	if err != nil || !strings.Contains(string(persisted), `"work_id":"`+a.WorkID+`"`) || !strings.Contains(string(persisted), `"tokens":150`) || !strings.Contains(string(persisted), `"ended_at":`) {
		t.Fatalf("journal replay lost task facts: %s %v", persisted, err)
	}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var replay llmtypes.NativeRecord
	if err = json.Unmarshal(raw, &replay); err != nil {
		t.Fatal(err)
	}
	if replay.WorkID != a.WorkID || !replay.StartedAt.Equal(start) || !replay.EndedAt.Equal(a.Timestamp) || replay.Tokens == nil || *replay.Tokens != 150 {
		t.Fatalf("replay lost final task facts: %s", raw)
	}
	late := task("a")
	late.Timestamp = start.Add(time.Second)
	if got := api.nativeBackgroundFacts("chat", late); got.State != "finished" {
		t.Fatal("late start resurrected a finished task")
	}
	next := task("a")
	next.TaskRunID = "2"
	next.Timestamp = start.Add(200 * time.Second)
	if got := api.nativeBackgroundFacts("chat", next); got.WorkID == a.WorkID || got.Tokens != nil {
		t.Fatal("a new native run inherited the old run's row or tokens")
	}
	stale := b
	lower := int64(5)
	stale.TokenSource = "native_usage"
	stale.Tokens = &lower
	stale.Origin = "background_usage"
	if got := api.nativeBackgroundFacts("chat", stale); got.Tokens == nil || *got.Tokens != 500 {
		t.Fatal("a delayed ledger projection replaced the task's own report")
	}
	update := a
	latest := int64(151)
	update.Tokens = &latest
	update.State = "started"
	update.Origin = "background_usage"
	if got := api.nativeBackgroundFacts("chat", update); got.State != "finished" || got.EndedAt.IsZero() || got.Tokens == nil || *got.Tokens != 151 {
		t.Fatal("late task usage lost the real terminal state")
	}
	if b.WorkID == shell.WorkID {
		t.Fatal("concurrent task identities collided")
	}
	if len(api.activeNativeBackground("other-chat")) != 0 {
		t.Fatal("task escaped its owning chat")
	}
}

// A quiet native task outlasting the ten-minute idle tail is still work. The
// checkpoint preserves that fact; the usual idle stop returns after its end.
func TestNativeBackgroundTaskKeepsAccountantBeyondIdleTail(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", root)
	api := &StreamingAPI{costLedger: costledger.NewLedger("http://127.0.0.1:1")}
	r := llmtypes.NativeRecord{ID: "quiet", Provider: "claude-code", SessionID: "native-bg", TaskID: "quiet", Kind: "helper", State: "started", Timestamp: time.Now().Add(-11 * time.Minute)}
	r = api.nativeBackgroundFacts("bg-cost-chat", r)
	a := &nativeCostAccountant{api: api, state: nativeCostState{Current: costledger.Entry{SessionID: "bg-cost-chat", UserID: "person"}, Provider: "claude-code", Active: true}, file: filepath.Join(root, "native-cost", "bg.json"), running: true, done: make(chan struct{}), lastActive: time.Now().Add(-11 * time.Minute)}
	if !a.poll() {
		t.Fatal("known background task lost its accountant at the idle timeout")
	}
	a.save()
	saved, ok := readNativeCostCheckpoint(a.file)
	if !ok || len(saved.Background) != 1 {
		t.Fatal("running task missing from restart checkpoint")
	}
	restored := &StreamingAPI{}
	for _, task := range saved.Background {
		restored.nativeBackgroundFacts(saved.Current.SessionID, task)
	}
	if len(restored.activeNativeBackground("bg-cost-chat")) != 1 {
		t.Fatal("restart lost running task identity")
	}
	r.State = "finished"
	r.Timestamp = time.Now()
	api.nativeBackgroundFacts("bg-cost-chat", r)
	a.lastActive = time.Now().Add(-11 * time.Minute)
	if a.poll() || a.running {
		t.Fatal("accountant did not return to the normal idle stop after the task ended")
	}
}

// A provider-owned command gets a line only after its reply ends. Its real
// native completion (not another chat, another tool or another reply) ends it.
func TestNativeBackgroundCommandFollowsActualProcessBookends(t *testing.T) {
	store := storeevents.NewEventStore(100)
	defer store.Stop()
	api := &StreamingAPI{eventStore: store}
	old := nativeBackgroundRuntime
	nativeBackgroundRuntime = func(string) (string, string) { return "codex-cli", "native-command-runtime" }
	t.Cleanup(func() { nativeBackgroundRuntime = old })
	storeevents.SetToolCompletionAuthority(func(session, id, name string) bool {
		return session == "command-chat" && id == "exec-bg" && name == "exec_command"
	})
	t.Cleanup(func() { storeevents.SetToolCompletionAuthority(nil) })
	store.SetEventAddedCallback(api.observeNativeBackgroundCommands)
	at := time.Now().Add(-time.Minute)
	store.AddEvent("command-chat", storeevents.Event{ID: "start-command", Type: "tool_call_start", Timestamp: at, Data: agentevents.NewAgentEvent(&agentevents.ToolCallStartEvent{ToolName: "exec_command", ToolCallID: "exec-bg"})})
	if len(api.activeNativeBackground("command-chat")) != 0 {
		t.Fatal("ordinary foreground command was labelled background")
	}
	store.AddEvent("command-chat", storeevents.Event{ID: "reply-end", Type: "unified_completion", Timestamp: time.Now(), Data: &agentevents.AgentEvent{Type: "unified_completion"}})
	tasks := api.activeNativeBackground("command-chat")
	if len(tasks) != 1 || tasks[0].Tokens != nil {
		t.Fatalf("background command facts: %+v", tasks)
	}
	store.CompleteNativeTool("other-chat", "exec-bg", time.Now(), false, "")
	if len(api.activeNativeBackground("command-chat")) != 1 {
		t.Fatal("foreign chat ended the command")
	}
	if !store.CompleteNativeTool("command-chat", "exec-bg", time.Now(), false, "done") {
		t.Fatal("native completion did not close the command")
	}
	if len(api.activeNativeBackground("command-chat")) != 0 {
		t.Fatal("native completion left the background line running")
	}
}
