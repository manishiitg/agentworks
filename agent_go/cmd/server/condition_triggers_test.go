package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

func conditionCodeFixture(t *testing.T) (triggerLinkEnv, context.Context, string) {
	t.Helper()
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	root := registerTestCrew(t, "checks", "owner", false)
	env.mock.mu.Lock()
	env.mock.files[root+"/product.json"] = `{"schema_version":1,"product":"work","id":"checks","title":"Checks","session_id":"sess-condition-main"}`
	env.mock.files[root+"/code/triggers/check.py"] = "import json\nprint(json.dumps({'events': [], 'state': {}}))\n"
	env.mock.mu.Unlock()
	return env, internalBotRequestContext(context.Background(), "owner"), root
}

func mustConditionJob(t *testing.T, s *ProjectActionDispatcher, ctx context.Context, id string) productScheduleJob {
	t.Helper()
	j, err := s.Job(ctx, "owner", projectScheduleJobID("work", "checks", id))
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// A Code watcher may send to the main chat, to an existing side chat, or open a
// new chat for each event ("new"); anything that is not a chat id is refused.
func TestConditionAgentTargetChatChoices(t *testing.T) {
	env, ctx, _ := conditionCodeFixture(t)
	c := env.svc.conditionService()
	job := productScheduleJob{UserID: "owner", ProjectID: "checks"}
	for _, chatID := range []string{"", "main", "new", "NEW", "373a2ae1"} {
		cfg := conditiontrigger.Config{Target: conditiontrigger.Target{Kind: "agent", Message: "go", ChatID: chatID}}
		if err := c.validateTarget(ctx, job, cfg); err != nil {
			t.Fatalf("chat_id %q refused: %v", chatID, err)
		}
	}
	for _, chatID := range []string{"a:b", "../x", "two words"} {
		cfg := conditiontrigger.Config{Target: conditiontrigger.Target{Kind: "agent", Message: "go", ChatID: chatID}}
		if err := c.validateTarget(ctx, job, cfg); err == nil {
			t.Fatalf("chat_id %q was admitted", chatID)
		}
	}
	if name := newWatcherChatName("Tagged tickets", "ticket-123"); name != "Tagged tickets · ticket-123" {
		t.Fatalf("tab name %q", name)
	}
	if got := len([]rune(newWatcherChatName(strings.Repeat("w", 200), "e"))); got != 120 {
		t.Fatalf("tab name not capped: %d", got)
	}
}

// PLAT-861: watchers of one Crew due on the same minute failed with "watcher
// checks are at capacity". A full pool now waits for a slot, and a check that
// never gets one is a quiet skip, not a failure.
func TestConditionCheckWaitsForSlot(t *testing.T) {
	slots := make(chan struct{}, 1)
	hold := acquireConditionSlot(context.Background(), slots, time.Second)
	if hold == nil {
		t.Fatal("empty pool did not give a slot")
	}
	if acquireConditionSlot(context.Background(), slots, 20*time.Millisecond) != nil {
		t.Fatal("full pool gave a second slot")
	}
	go func() { time.Sleep(20 * time.Millisecond); hold() }()
	release := acquireConditionSlot(context.Background(), slots, time.Second)
	if release == nil {
		t.Fatal("check did not get the slot freed while it waited")
	}
	release()
	if !errors.Is(errConditionBusy{}, productschedule.ErrSkip) {
		t.Fatal("busy check must be a skip")
	}
}
