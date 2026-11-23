package mcpagent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type retainedCompletionCapture struct {
	mu     sync.Mutex
	events []*events.AgentEvent
	ready  chan struct{}
}

func (c *retainedCompletionCapture) Name() string { return "retained-completion-capture" }

func (c *retainedCompletionCapture) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	if event == nil || event.Type != events.EventTypeUnifiedCompletion {
		return nil
	}
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
	select {
	case c.ready <- struct{}{}:
	default:
	}
	return nil
}

func TestRetainedSessionOwnsCompletionAfterDirectDelivery(t *testing.T) {
	capture := &retainedCompletionCapture{ready: make(chan struct{}, 1)}
	agent := &Agent{sessionID: "retained-pi-session", provider: llm.ProviderPiCLI, listeners: []AgentEventListener{capture}}
	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	session := &Session{
		agent:       agent,
		watchCtx:    watchCtx,
		watchCancel: watchCancel,
		retainedFinalResponse: func(provider llm.Provider, sessionID string, _ time.Time) string {
			if provider != llm.ProviderPiCLI || sessionID != "retained-pi-session" {
				t.Fatalf("reader identity = %s/%s", provider, sessionID)
			}
			return "upgrade complete"
		},
	}

	lifecycle := newCanonicalTurnLifecycle("")
	session.startRetainedCompletionWatch(lifecycle, "upgrade the report", llm.ProviderPiCLI, llm.CodingAgentTransportTmux)
	select {
	case <-capture.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("retained turn did not emit canonical completion")
	}

	session.stateMu.Lock()
	active := session.retainedActive
	history := append([]llmtypes.MessageContent(nil), session.history...)
	session.stateMu.Unlock()
	if active {
		t.Fatal("retained turn remained active after final response")
	}
	if len(history) != 2 {
		t.Fatalf("retained history = %d messages, want user + assistant", len(history))
	}

	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.events) != 1 {
		t.Fatalf("completion events = %d, want exactly one", len(capture.events))
	}
	completion, ok := capture.events[0].Data.(*events.UnifiedCompletionEvent)
	if !ok {
		t.Fatalf("completion data = %T", capture.events[0].Data)
	}
	if completion.FinalResult != "upgrade complete" || completion.Metadata["source"] != "mcpagent_session" {
		t.Fatalf("completion = %#v", completion)
	}
	if completion.Metadata["turn_id"] != lifecycle.id || capture.events[0].TurnID != lifecycle.id {
		t.Fatalf("completion turn identity = metadata:%v wrapper:%q want %q", completion.Metadata["turn_id"], capture.events[0].TurnID, lifecycle.id)
	}
}

func TestSessionRunMarksAgentTurnInFlightForDirectReceiptSuppression(t *testing.T) {
	agent := &Agent{}
	session := &Session{agent: agent}

	// We only need to prove the state transition used by bridge receipt
	// suppression; invalid policy exits after the turn has been claimed and the
	// deferred cleanup must release it again.
	_, err := session.Run(context.Background(), Turn{
		Input:      "hello",
		ToolPolicy: ToolPolicy{AllowedTools: []string{" invalid "}},
	})
	if err == nil {
		t.Fatal("expected invalid tool policy")
	}
	if agent.isTurnInFlight() {
		t.Fatal("Session.Run left agent turn marked in flight")
	}
}

// PLAT-180. A retained delivery never calls Session.Run again -- it mints its
// own lifecycle via startRetainedCompletionWatch. A caller that cached a turn
// ID from an earlier Run (as agent_go's tool-call hook used to) would still
// see that stale ID; ActiveTurnID must reflect the retained turn's own,
// different ID for as long as it is in flight, then "" once it completes.
func TestActiveTurnIDReflectsTheRetainedTurnNotAnEarlierCachedTurn(t *testing.T) {
	agent := &Agent{sessionID: "retained-turn-id-session", provider: llm.ProviderPiCLI}
	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()

	release := make(chan string, 1)
	session := &Session{
		agent:       agent,
		watchCtx:    watchCtx,
		watchCancel: watchCancel,
		retainedFinalResponse: func(llm.Provider, string, time.Time) string {
			select {
			case result := <-release:
				return result
			default:
				return ""
			}
		},
	}

	staleTurnID := "turn_from_an_earlier_run"
	if got := session.ActiveTurnID(); got != "" {
		t.Fatalf("ActiveTurnID() before any turn = %q, want empty", got)
	}

	retainedLifecycle := newCanonicalTurnLifecycle("")
	if retainedLifecycle.id == staleTurnID {
		t.Fatal("test setup: retained lifecycle must not coincidentally match the stale ID")
	}
	session.startRetainedCompletionWatch(retainedLifecycle, "steer the running agent", llm.ProviderPiCLI, llm.CodingAgentTransportTmux)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := session.ActiveTurnID(); got == retainedLifecycle.id {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ActiveTurnID() never reported the retained turn's own ID %q", retainedLifecycle.id)
		}
		time.Sleep(5 * time.Millisecond)
	}

	release <- "the real final answer"
	deadline = time.Now().Add(2 * time.Second)
	for {
		if got := session.ActiveTurnID(); got == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("ActiveTurnID() did not clear after the retained turn completed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSessionRunRejectsWhileRetainedTurnIsActive(t *testing.T) {
	agent := &Agent{}
	session := &Session{agent: agent, retainedActive: true}

	_, err := session.Run(context.Background(), Turn{Input: "overlapping turn"})
	if !errors.Is(err, ErrTurnAlreadyInFlight) {
		t.Fatalf("Run error = %v, want ErrTurnAlreadyInFlight", err)
	}
}

func TestRetainedFollowupReplacesInFlightCompletionRead(t *testing.T) {
	for _, provider := range []llm.Provider{llm.ProviderClaudeCode, llm.ProviderCursorCLI, llm.ProviderMuseCLI} {
		t.Run(string(provider), func(t *testing.T) {
			testRetainedFollowupReplacesInFlightCompletionRead(t, provider)
		})
	}
}

func testRetainedFollowupReplacesInFlightCompletionRead(t *testing.T, provider llm.Provider) {
	capture := &retainedCompletionCapture{ready: make(chan struct{}, 2)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	oldReply := make(chan struct{})
	newReply := make(chan struct{})
	var once sync.Once
	var firstStart time.Time
	var mu sync.Mutex
	session := &Session{
		agent:    &Agent{sessionID: "cursor-followup", listeners: []AgentEventListener{capture}},
		watchCtx: ctx,
		retainedFinalResponse: func(_ llm.Provider, _ string, start time.Time) string {
			mu.Lock()
			if firstStart.IsZero() {
				firstStart = start
			}
			first := firstStart.Equal(start)
			mu.Unlock()
			if first {
				once.Do(func() { close(entered) })
				<-oldReply
				return "Latency now runs every other day."
			}
			select {
			case <-newReply:
				return "The Slack card is easier to scan."
			default:
				return ""
			}
		},
	}
	lifecycle := newCanonicalTurnLifecycle("")
	session.startRetainedCompletionWatch(lifecycle, "change schedule", provider, llm.CodingAgentTransportTmux)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not start")
	}
	// Match Send's critical section: an old read finishes while a new input is
	// being delivered, and must lose ownership before it can emit completion.
	session.sendMu.Lock()
	close(oldReply)
	session.startRetainedCompletionWatch(lifecycle, "fix Slack readability", provider, llm.CodingAgentTransportTmux)
	session.sendMu.Unlock()
	select {
	case <-capture.ready:
		t.Fatal("previous response completed the new request")
	case <-time.After(3 * retainedCompletionPollInterval):
	}
	close(newReply)
	select {
	case <-capture.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("follow-up final reply was lost")
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.events) != 1 {
		t.Fatalf("completions = %d", len(capture.events))
	}
	completion := capture.events[0].Data.(*events.UnifiedCompletionEvent)
	if completion.FinalResult != "The Slack card is easier to scan." {
		t.Fatalf("final = %q", completion.FinalResult)
	}
}

// PLAT-872: an acknowledgement can arrive after native work has committed.
// Every supported CLI must replace stale in-flight reads and retain the
// before-send boundary, including Pi/Agy/Codex which previously did not refresh.
func TestRetainedFollowupKeepsTheBeforeDeliveryBoundary(t *testing.T) {
	for _, provider := range []llm.Provider{llm.ProviderClaudeCode, llm.ProviderCodexCLI, llm.ProviderCursorCLI, llm.ProviderMuseCLI, llm.ProviderPiCLI, llm.ProviderAgyCLI} {
		t.Run(string(provider), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			beforeSend := time.Now().Add(-time.Second)
			read := make(chan time.Time, 1)
			s := &Session{agent: &Agent{sessionID: t.Name()}, watchCtx: ctx,
				retainedProgressMessages: (&transcriptFixture{}).read,
				retainedFinalResponse: func(_ llm.Provider, _ string, start time.Time) string {
					select {
					case read <- start:
					default:
					}
					return ""
				},
			}
			if !providerRefreshesRetainedWatcher(provider) {
				t.Fatal("follow-up kept a stale watcher")
			}
			s.startRetainedCompletionWatchFor(newCanonicalTurnLifecycle(""), "follow-up", provider, llm.CodingAgentTransportTmux, true, beforeSend)
			select {
			case got := <-read:
				if !got.Equal(beforeSend) {
					t.Fatal("native work before the acknowledgement was excluded")
				}
			case <-time.After(time.Second):
				t.Fatal("follow-up has no reader")
			}
		})
	}
}
