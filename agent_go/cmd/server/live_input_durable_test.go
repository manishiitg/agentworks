package server

import (
	"context"

	"testing"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	pkgevents "github.com/manishiitg/mcpagent/events"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func waitForLiveInputConfirmed(t *testing.T, store *internalevents.EventStore, sessionID string) internalevents.Event {
	t.Helper()
	sub := store.Subscribe(sessionID)
	defer store.Unsubscribe(sessionID, sub)
	deadline := time.Now().Add(5 * time.Second)
	for {
		timeout := time.Until(deadline)
		if timeout <= 0 {
			t.Fatalf("timed out waiting for live_input_confirmed event for session %s", sessionID)
		}
		select {
		case event := <-sub.Ch:
			if event.Type == string(pkgevents.LiveInputConfirmed) {
				return event
			}
		case <-time.After(timeout):
			t.Fatalf("timed out waiting for live_input_confirmed event for session %s", sessionID)
		}
	}
}

func TestWatchLiveInputDurableSkipsProvidersWithoutFlag(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()
	called := false
	api := &StreamingAPI{eventStore: store,
		internalDurableAckHandler: func(context.Context, llmproviders.Provider, string, string) (llmtypes.DurableAck, error) {
			called = true
			return llmtypes.DurableAck{Outcome: llmtypes.DurableAckConfirmed}, nil
		},
	}

	// Unknown providers have no durable-ack contract: no watch, no event.
	// (All six coding CLIs now carry SupportsDurableAck.)
	api.watchLiveInputDurable("durable-watch-skipped", "unknown-provider", "steer-message-2", "steer into the turn")
	time.Sleep(200 * time.Millisecond)
	if called {
		t.Fatal("durable watch must not run for providers without SupportsDurableAck")
	}
	if events := store.GetAllEventsRaw("durable-watch-skipped"); len(events) != 0 {
		t.Fatalf("recorded events = %d, want 0", len(events))
	}
}

func TestDurableAckDispatchCoversAckContracts(t *testing.T) {
	// PLAT-872-55: SDK defaults may not need durable-ack polling, while their
	// explicit terminal transports still must have an awaiter.
	for _, mode := range []string{"", "tmux"} {
		t.Run("transport_"+mode, func(t *testing.T) {
			t.Setenv("CLAUDE_CODE_TRANSPORT", mode)
			t.Setenv("CODEX_CLI_TRANSPORT", mode)
			t.Setenv("CURSOR_CLI_TRANSPORT", mode)
			for _, contract := range llmproviders.CodingAgentProviderContracts() {
				if contract.SupportsDurableAck {
					if _, ok := durableAckAwaiters[contract.Provider]; !ok {
						t.Errorf("contract claims durable ack for %s but no dispatch entry is registered", contract.Provider)
					}
				}
			}
		})
	}
}
