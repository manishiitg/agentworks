package mcpagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestMuseBackgroundWatcherEmitsAfterForegroundTerminal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	const nativeID = "native-background-test"
	path := filepath.Join(root, "muse", "sessions", "2026", "09", "23", nativeID, "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	appendRow := func(sequence int, scope, kind string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600) //nolint:gosec // path is under this test's temporary directory.
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		_, err = fmt.Fprintf(f, `{"sequence":%d,"payload_type":"runtime.session","payload":{"kind":%q,"run_id":"run-1","event":{"kind":%q,"task_id":"task-1"}}}`+"\n", sequence, scope, kind)
		if err != nil {
			t.Fatal(err)
		}
	}
	appendRow(1, "run", "task_backgrounded")
	appendRow(2, "run", "terminal")
	tracer := NewStreamingTracer(observability.NoopTracer{}, 16)
	agent := &Agent{sessionID: "owner-background-test", logger: loggerv2.NewDefault(), tracers: []observability.Tracer{tracer}}
	session, err := agent.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ch, unsubscribe := tracer.SubscribeToEvents(context.Background())
	defer unsubscribe()
	session.startMuseBackgroundWatcher(nativeID, 0)
	select {
	case event := <-ch:
		if event.Type != events.CodingAgentBackgroundTask {
			t.Fatalf("first event=%s", event.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("background marker not emitted")
	}
	appendRow(3, "task", "completed")
	select {
	case event := <-ch:
		data, ok := event.Data.(*events.CodingAgentBackgroundTaskEvent)
		if !ok || data.Kind != "completed" || data.NativeSequence != 3 {
			t.Fatalf("late event=%+v", event)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("late completion not emitted")
	}
	deadline := time.Now().Add(4 * time.Second)
	for {
		session.stateMu.Lock()
		watching := session.museBackgroundWatching
		session.stateMu.Unlock()
		if !watching {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completed background task left its watcher running")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Exercise the real retained-turn start used by SendIdentified and
	// ObserveNativeInput, not another manual background-watcher call.
	agent.provider = llm.ProviderMuseCLI
	agent.codingProviderSessionHandle = llmtypes.CodingProviderSessionHandle{Provider: "muse-cli", NativeSessionID: nativeID}
	var finalReady atomic.Bool
	session.retainedFinalResponse = func(llm.Provider, string, time.Time) string {
		if finalReady.Load() {
			return "done"
		}
		return ""
	}
	session.retainedEndedWithoutReply = func(llm.Provider, string, time.Time) (string, bool) { return "", false }
	session.retainedProgressMessages = func(llm.Provider, string) []llmtypes.MessageContent { return nil }
	session.startRetainedCompletionWatchFor(newCanonicalTurnLifecycle(""), "next native turn", llm.ProviderMuseCLI, llm.CodingAgentTransportTmux, false, time.Now())
	appendRow(4, "run", "task_backgrounded")
	waitTask := func(sequence int64) {
		t.Helper()
		deadline := time.After(4 * time.Second)
		for {
			select {
			case event := <-ch:
				if event.Type != events.CodingAgentBackgroundTask {
					continue
				}
				data, ok := event.Data.(*events.CodingAgentBackgroundTaskEvent)
				if !ok {
					t.Fatal("unexpected task event")
				}
				if data.NativeSequence <= 3 {
					t.Fatal("idle restart replayed previously published task rows")
				}
				if data.NativeSequence == sequence {
					return
				}
			case <-deadline:
				t.Fatal("retained turn did not restart native background observation")
			}
		}
	}
	waitTask(4)
	finalReady.Store(true)
	deadline = time.Now().Add(4 * time.Second)
	for {
		session.stateMu.Lock()
		active := session.retainedActive
		session.stateMu.Unlock()
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retained foreground did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	appendRow(5, "task", "completed")
	waitTask(5)
}

func TestMuseRestoredSessionReplaysNativeTaskRows(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	const nativeID = "restored-native-background"
	path := filepath.Join(root, "muse", "sessions", "2026", "09", "23", nativeID, "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	rows := `{"sequence":1,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"task_backgrounded","task_id":"task-1"}}}` + "\n" +
		`{"sequence":2,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"terminal","terminal":"completed"}}}` + "\n" +
		`{"sequence":3,"payload_type":"runtime.session","payload":{"kind":"task","run_id":"run-1","event":{"kind":"completed","task_id":"task-1"}}}` + "\n"
	if err := os.WriteFile(path, []byte(rows), 0600); err != nil {
		t.Fatal(err)
	}
	tracer := NewStreamingTracer(observability.NoopTracer{}, 16)
	agent := &Agent{sessionID: "restored-owner", provider: llm.ProviderMuseCLI, logger: loggerv2.NewDefault(), tracers: []observability.Tracer{tracer},
		codingProviderSessionHandle: llmtypes.CodingProviderSessionHandle{Provider: "muse-cli", NativeSessionID: nativeID}}
	ch, unsubscribe := tracer.SubscribeToEvents(context.Background())
	defer unsubscribe()
	session, err := agent.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	kinds := make([]string, 0, 2)
	for len(kinds) < 2 {
		select {
		case event := <-ch:
			if event.Type == events.CodingAgentBackgroundTask {
				kinds = append(kinds, event.Data.(*events.CodingAgentBackgroundTaskEvent).Kind)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("restored kinds=%v", kinds)
		}
	}
	if kinds[0] != "task_backgrounded" || kinds[1] != "completed" {
		t.Fatalf("restored kinds=%v", kinds)
	}
}

// PLAT-746-961: an idle restored session with no native journal polled/globbed forever.
func TestMuseMissingLogWatcherStopsWhenTurnEndsAndRestarts(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := &Agent{sessionID: "missing-log-idle", logger: loggerv2.NewDefault()}
	s, err := a.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	wait := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for {
			s.stateMu.Lock()
			got := s.museBackgroundWatching
			s.stateMu.Unlock()
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("watching=%t want=%t", got, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	s.stateMu.Lock()
	s.runActive = true
	s.stateMu.Unlock()
	s.startMuseBackgroundWatcher("missing-native", 0)
	wait(true)
	// End the real foreground lane: no known background work remains.
	s.stateMu.Lock()
	s.runActive = false
	s.stateMu.Unlock()
	wait(false)
	// A later turn may start its observer again; idle stopping is not permanent.
	s.stateMu.Lock()
	s.runActive = true
	s.stateMu.Unlock()
	s.startMuseBackgroundWatcher("missing-native", 0)
	wait(true)
	s.stateMu.Lock()
	s.runActive = false
	s.stateMu.Unlock()
	wait(false)
}
