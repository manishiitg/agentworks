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

func TestWatchLiveInputDurableEmitsConfirmedEvent(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()
	const sessionID = "durable-watch-confirmed"
	const messageID = "steer-message-1"
	api := &StreamingAPI{eventStore: store,
		internalDurableAckHandler: func(_ context.Context, provider llmproviders.Provider, owner, message string) (llmtypes.DurableAck, error) {
			if provider != llmproviders.ProviderCodexCLI || owner != sessionID || message != "steer into the turn" {
				t.Errorf("durable watch request = (%s %s %q)", provider, owner, message)
			}
			return llmtypes.DurableAck{Outcome: llmtypes.DurableAckConfirmed, Latency: 1200 * time.Millisecond, ProofSource: "/tmp/rollout.jsonl"}, nil
		},
	}

	api.watchLiveInputDurable(sessionID, string(llmproviders.ProviderCodexCLI), messageID, "steer into the turn")

	event := waitForLiveInputConfirmed(t, store, sessionID)
	if event.ID != messageID+":confirmed" {
		t.Fatalf("event ID = %q, want deterministic %q", event.ID, messageID+":confirmed")
	}
	payload, ok := event.Data.Data.(*pkgevents.LiveInputConfirmedEvent)
	if !ok {
		t.Fatalf("event payload = %T, want *LiveInputConfirmedEvent", event.Data.Data)
	}
	if payload.MessageID != messageID || payload.Outcome != "confirmed" || payload.Provider != string(llmproviders.ProviderCodexCLI) {
		t.Fatalf("confirm payload = %#v", payload)
	}
	if payload.LatencyMs != 1200 || payload.ProofSource != "/tmp/rollout.jsonl" {
		t.Fatalf("confirm proof = %#v", payload)
	}
	if payload.Metadata["message_id"] != messageID || payload.Metadata["confirmation"] != "confirmed" {
		t.Fatalf("confirm metadata = %#v", payload.Metadata)
	}
}

func TestWatchLiveInputDurableEmitsConfirmedEventForPi(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()
	const sessionID = "durable-watch-pi"
	const messageID = "steer-message-pi-1"
	api := &StreamingAPI{eventStore: store,
		internalDurableAckHandler: func(_ context.Context, provider llmproviders.Provider, owner, message string) (llmtypes.DurableAck, error) {
			if provider != llmproviders.ProviderPiCLI || owner != sessionID || message != "steer into the Pi turn" {
				t.Errorf("durable watch request = (%s %s %q)", provider, owner, message)
			}
			return llmtypes.DurableAck{Outcome: llmtypes.DurableAckConfirmed, Latency: 8100 * time.Millisecond, ProofSource: "/tmp/markers.jsonl"}, nil
		},
	}

	api.watchLiveInputDurable(sessionID, string(llmproviders.ProviderPiCLI), messageID, "steer into the Pi turn")

	event := waitForLiveInputConfirmed(t, store, sessionID)
	payload, ok := event.Data.Data.(*pkgevents.LiveInputConfirmedEvent)
	if !ok {
		t.Fatalf("event payload = %T, want *LiveInputConfirmedEvent", event.Data.Data)
	}
	if payload.MessageID != messageID || payload.Outcome != "confirmed" || payload.Provider != string(llmproviders.ProviderPiCLI) || payload.LatencyMs != 8100 {
		t.Fatalf("confirm payload = %#v", payload)
	}
}

func TestWatchLiveInputDurableEmitsConfirmedEventForMuse(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()
	const sessionID = "durable-watch-muse"
	const messageID = "steer-message-muse-1"
	api := &StreamingAPI{eventStore: store,
		internalDurableAckHandler: func(_ context.Context, provider llmproviders.Provider, owner, message string) (llmtypes.DurableAck, error) {
			if provider != llmproviders.ProviderMuseCLI || owner != sessionID || message != "steer into the Muse turn" {
				t.Errorf("durable watch request = (%s %s %q)", provider, owner, message)
			}
			return llmtypes.DurableAck{Outcome: llmtypes.DurableAckConfirmed, Latency: 1200 * time.Millisecond, ProofSource: "/tmp/session.jsonl"}, nil
		},
	}

	api.watchLiveInputDurable(sessionID, string(llmproviders.ProviderMuseCLI), messageID, "steer into the Muse turn")

	event := waitForLiveInputConfirmed(t, store, sessionID)
	payload, ok := event.Data.Data.(*pkgevents.LiveInputConfirmedEvent)
	if !ok {
		t.Fatalf("event payload = %T, want *LiveInputConfirmedEvent", event.Data.Data)
	}
	if payload.MessageID != messageID || payload.Outcome != "confirmed" || payload.Provider != string(llmproviders.ProviderMuseCLI) || payload.LatencyMs != 1200 {
		t.Fatalf("confirm payload = %#v", payload)
	}
}

func TestWatchLiveInputDurableEmitsConfirmedEventForClaude(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()
	const sessionID = "durable-watch-claude"
	const messageID = "steer-message-claude-1"
	api := &StreamingAPI{eventStore: store,
		internalDurableAckHandler: func(_ context.Context, provider llmproviders.Provider, owner, message string) (llmtypes.DurableAck, error) {
			if provider != llmproviders.ProviderClaudeCode || owner != sessionID || message != "steer into the Claude turn" {
				t.Errorf("durable watch request = (%s %s %q)", provider, owner, message)
			}
			return llmtypes.DurableAck{Outcome: llmtypes.DurableAckConfirmed, Latency: 7000 * time.Millisecond, ProofSource: "/tmp/session.jsonl"}, nil
		},
	}

	api.watchLiveInputDurable(sessionID, string(llmproviders.ProviderClaudeCode), messageID, "steer into the Claude turn")

	event := waitForLiveInputConfirmed(t, store, sessionID)
	payload, ok := event.Data.Data.(*pkgevents.LiveInputConfirmedEvent)
	if !ok {
		t.Fatalf("event payload = %T, want *LiveInputConfirmedEvent", event.Data.Data)
	}
	if payload.MessageID != messageID || payload.Outcome != "confirmed" || payload.Provider != string(llmproviders.ProviderClaudeCode) || payload.LatencyMs != 7000 {
		t.Fatalf("confirm payload = %#v", payload)
	}
}

func TestWatchLiveInputDurableEmitsConfirmedEventForCursor(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()
	const sessionID = "durable-watch-cursor"
	const messageID = "steer-message-cursor-1"
	api := &StreamingAPI{eventStore: store,
		internalDurableAckHandler: func(_ context.Context, provider llmproviders.Provider, owner, message string) (llmtypes.DurableAck, error) {
			if provider != llmproviders.ProviderCursorCLI || owner != sessionID || message != "steer into the Cursor turn" {
				t.Errorf("durable watch request = (%s %s %q)", provider, owner, message)
			}
			return llmtypes.DurableAck{Outcome: llmtypes.DurableAckConfirmed, Latency: 9500 * time.Millisecond, ProofSource: "/tmp/store.db"}, nil
		},
	}

	api.watchLiveInputDurable(sessionID, string(llmproviders.ProviderCursorCLI), messageID, "steer into the Cursor turn")

	event := waitForLiveInputConfirmed(t, store, sessionID)
	payload, ok := event.Data.Data.(*pkgevents.LiveInputConfirmedEvent)
	if !ok {
		t.Fatalf("event payload = %T, want *LiveInputConfirmedEvent", event.Data.Data)
	}
	if payload.MessageID != messageID || payload.Outcome != "confirmed" || payload.Provider != string(llmproviders.ProviderCursorCLI) || payload.LatencyMs != 9500 {
		t.Fatalf("confirm payload = %#v", payload)
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

func TestWatchLiveInputDurableMapsAwaitErrorToFailed(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()
	const sessionID = "durable-watch-failed"
	api := &StreamingAPI{eventStore: store,
		internalDurableAckHandler: func(context.Context, llmproviders.Provider, string, string) (llmtypes.DurableAck, error) {
			return llmtypes.DurableAck{Outcome: llmtypes.DurableAckFailed}, context.DeadlineExceeded
		},
	}

	api.watchLiveInputDurable(sessionID, string(llmproviders.ProviderCodexCLI), "steer-message-3", "steer into the turn")

	event := waitForLiveInputConfirmed(t, store, sessionID)
	payload, ok := event.Data.Data.(*pkgevents.LiveInputConfirmedEvent)
	if !ok {
		t.Fatalf("event payload = %T, want *LiveInputConfirmedEvent", event.Data.Data)
	}
	if payload.Outcome != "failed" {
		t.Fatalf("confirm outcome = %q, want failed", payload.Outcome)
	}
}

func TestDurableAckDispatchCoversAckContracts(t *testing.T) {
	// Every SupportsDurableAck contract must have a dispatch entry: the
	// watch only runs for flag-holders, so a missing entry would
	// silently report failed for a provider that proves durability.
	for _, contract := range llmproviders.CodingAgentProviderContracts() {
		if !contract.SupportsDurableAck {
			continue
		}
		if _, ok := durableAckAwaiters[contract.Provider]; !ok {
			t.Errorf("contract claims durable ack for %s but no dispatch entry is registered", contract.Provider)
		}
	}
	for provider := range durableAckAwaiters {
		contract, ok := llmproviders.GetCodingAgentProviderContract(provider, "")
		if !ok || !contract.SupportsDurableAck {
			t.Errorf("dispatch entry %s has no durable-ack contract", provider)
		}
	}
}

func TestWatchLiveInputDurablePassesThroughUnflushed(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()
	const sessionID = "durable-watch-unflushed"
	api := &StreamingAPI{eventStore: store,
		internalDurableAckHandler: func(context.Context, llmproviders.Provider, string, string) (llmtypes.DurableAck, error) {
			return llmtypes.DurableAck{Outcome: llmtypes.DurableAckUnflushed, Latency: 60 * time.Second}, nil
		},
	}

	api.watchLiveInputDurable(sessionID, string(llmproviders.ProviderCodexCLI), "steer-message-4", "steer into the turn")

	event := waitForLiveInputConfirmed(t, store, sessionID)
	payload, ok := event.Data.Data.(*pkgevents.LiveInputConfirmedEvent)
	if !ok {
		t.Fatalf("event payload = %T, want *LiveInputConfirmedEvent", event.Data.Data)
	}
	if payload.Outcome != "accepted_but_unflushed" {
		t.Fatalf("confirm outcome = %q, want accepted_but_unflushed", payload.Outcome)
	}
}
