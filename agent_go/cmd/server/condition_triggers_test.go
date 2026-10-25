package server

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
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

func mustConditionJob(t *testing.T, s *ProductScheduleService, ctx context.Context, id string) productScheduleJob {
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
