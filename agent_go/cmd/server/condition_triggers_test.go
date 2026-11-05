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

// RTS: a completed one-time Crew watcher was "switched off" through the
// per-person override, which Effective ignores for project jobs, so the
// scheduler drained it again every tick. One drain must switch it off in the
// watcher's own definition; the next drain does nothing.
func TestConditionCompletedOnceCrewWatcherSwitchesOff(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	env, ctx, _ := agentEmailCrewFixture(t)
	c := env.svc.conditionService()
	base, err := c.source(ctx, "owner", "work", "mail-checks")
	if err != nil {
		t.Fatal(err)
	}
	cfg := conditiontrigger.Config{Once: true, Script: "code/triggers/cond-once.py", Target: conditiontrigger.Target{Kind: "agent", Message: "go"}}
	if _, err := c.saveCaller(ctx, base, productschedule.Schedule{ID: "cond-once", Name: "Once", Enabled: true, CronExpression: "*/5 * * * *", Timezone: "UTC", Condition: &cfg}); err != nil {
		t.Fatal(err)
	}
	read := func() productScheduleJob {
		j, err := env.svc.Job(ctx, "owner", projectScheduleJobID("work", "mail-checks", "cond-once"))
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	job := read()
	if job.WatcherSource == nil || !job.Effective().Enabled || job.Schedule.Condition == nil || !job.Schedule.Condition.Once {
		t.Fatalf("fixture watcher not enabled one-time: %+v", job.Schedule)
	}
	if err := c.Save(ctx, conditionKey(job), conditiontrigger.Ledger{OnceEventID: "ticket", OnceCompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	c.drain(ctx, job)
	if read().Effective().Enabled {
		t.Fatal("completed one-time Crew watcher still enabled after drain")
	}
	env.mock.mu.Lock()
	before := env.mock.files[productScheduleStatePath("owner")]
	env.mock.mu.Unlock()
	c.drain(ctx, job)
	env.mock.mu.Lock()
	after := env.mock.files[productScheduleStatePath("owner")]
	env.mock.mu.Unlock()
	if before != after {
		t.Fatal("second drain rewrote the switched-off watcher")
	}
}
