package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
)

func conditionCodeFixture(t *testing.T) (triggerLinkEnv, context.Context, string) {
	t.Helper()
	env := newTriggerLinkEnv(t)
	profile := agentprofiles.Profile{ID: "code", Name: "Code", Product: "code", Version: 1, BuiltIn: true, SystemPromptTemplate: "hi", UIPanels: agentprofiles.UIPanels{Schedules: true}, Runtime: agentprofiles.RuntimePolicy{Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}, Workspace: agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: "Chats/Code/projects"}}}
	if err := env.svc.registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	env.api.agentProfiles = env.svc.registry
	root := "_users/owner/Chats/Code/projects/checks"
	env.mock.mu.Lock()
	env.mock.files[root+"/product.json"] = `{"schema_version":1,"product":"code","id":"checks","title":"Checks","session_id":"sess-condition-main"}`
	env.mock.files[root+"/code/triggers/check.py"] = "import json\nprint(json.dumps({'events': [], 'state': {}}))\n"
	env.mock.mu.Unlock()
	return env, internalBotRequestContext(context.Background(), "owner"), root
}

func TestConditionCodeManagementAndRuntimeAccess(t *testing.T) {
	env, ctx, root := conditionCodeFixture(t)
	c := env.svc.conditionService()
	args := map[string]interface{}{"action": "create", "request_id": "create-check", "trigger": map[string]interface{}{"name": "Tagged tickets", "cron_expression": "*/5 * * * *", "timezone": "Asia/Kolkata", "script": "code/triggers/check.py", "target": map[string]interface{}{"kind": "agent", "message": "Process this ticket"}}}
	out, err := c.manage(ctx, "owner", "code", "checks", args)
	if err != nil {
		t.Fatal(err)
	}
	id := out.(map[string]interface{})["id"].(string)
	duplicate, err := c.manage(ctx, "owner", "code", "checks", args)
	if err != nil || duplicate.(map[string]interface{})["id"] != id {
		t.Fatal("create retry duplicated trigger", duplicate, err)
	}

	job, err := env.svc.Job(ctx, "owner", projectScheduleJobID("code", "checks", id))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.current(ctx, job, *job.Schedule.Condition); err != nil {
		t.Fatal(err)
	}
	if _, err := c.manage(ctx, "other", "code", "checks", map[string]interface{}{"action": "list"}); err == nil {
		t.Fatal("another account read private condition triggers")
	}
	if _, err := c.manage(ctx, "owner", "work", "alpha", args); err == nil {
		t.Fatal("unsupported source product was admitted")
	}
	if _, err := c.manage(ctx, "owner", "code", "checks", map[string]interface{}{"action": "update", "expected_revision": conditionView(mustConditionJob(t, env.svc, ctx, id).Schedule)["revision"], "id": id, "trigger": map[string]interface{}{"cron_expression": "* * * * *"}}); err == nil {
		t.Fatal("one minute poll was admitted")
	}
	if _, err := c.manage(ctx, "owner", "code", "checks", map[string]interface{}{"action": "update", "expected_revision": conditionView(mustConditionJob(t, env.svc, ctx, id).Schedule)["revision"], "id": id, "trigger": map[string]interface{}{"enabled": false}}); err != nil {
		t.Fatal(err)
	}
	if err := c.current(ctx, job, *job.Schedule.Condition); err == nil {
		t.Fatal("paused source still admitted")
	}
	if _, err := c.manage(ctx, "owner", "code", "checks", map[string]interface{}{"action": "update", "expected_revision": conditionView(mustConditionJob(t, env.svc, ctx, id).Schedule)["revision"], "id": id, "trigger": map[string]interface{}{"enabled": true}}); err != nil {
		t.Fatal(err)
	}
	job, _ = env.svc.Job(ctx, "owner", job.ID())
	if job.Schedule.Condition.Script != "code/triggers/check.py" || job.Schedule.Condition.Target.Message != "Process this ticket" {
		t.Fatal("partial update erased definition")
	}
	env.mock.mu.Lock()
	env.mock.files[root+"/code/triggers/check.py"] = "print('changed')"
	env.mock.mu.Unlock()
	if _, err := c.runScript(conditionContext(ctx, job), *job.Schedule.Condition, json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "script changed") {
		t.Fatal("unpinned source ran", err)
	}
	// Saved route dispatch doesn't require mutating the receiving workflow.
	cfg := *job.Schedule.Condition
	cfg.Target = conditiontrigger.Target{Kind: "route", Name: "Workflow/reports", Groups: []string{"default"}, Routes: map[string]string{}}
	if err := c.validateTarget(ctx, job, cfg); err != nil {
		t.Fatal(err)
	}
	if len(conditionRouteSchedule(job, cfg).GroupNames) != 1 {
		t.Fatal("route groups lost")
	}
}

func TestConditionBackpressureDoesNotPauseButScriptFailuresNotify(t *testing.T) {
	env, ctx, _ := conditionCodeFixture(t)
	c := env.svc.conditionService()
	out, err := c.manage(ctx, "owner", "code", "checks", map[string]interface{}{"action": "create", "request_id": "create-check", "trigger": map[string]interface{}{"name": "Check", "cron_expression": "*/5 * * * *", "timezone": "UTC", "script": "code/triggers/check.py", "target": map[string]interface{}{"kind": "agent", "message": "Handle"}}})
	if err != nil {
		t.Fatal(err)
	}
	id := out.(map[string]interface{})["id"].(string)
	job, _ := env.svc.Job(ctx, "owner", projectScheduleJobID("code", "checks", id))
	c.engine.Run = func(context.Context, conditiontrigger.Config, json.RawMessage) ([]byte, error) {
		return nil, conditiontrigger.ErrBacklog
	}
	c.engine.Dispatch = func(context.Context, conditiontrigger.Config, conditiontrigger.Event, string) (string, error) {
		return "receipt", nil
	}
	notices := 0
	env.api.eventStore = openClientIdentityTestStore(t, "condition-pause")
	c.notifyPause = func(ctx context.Context, j productScheduleJob, detail string) {
		notices++
		c.pauseNotification(ctx, j, detail)
	}
	for i := 0; i < 6; i++ {
		_, _ = c.poll(ctx, job, "test", time.Time{})
	}
	job, _ = env.svc.Job(ctx, "owner", job.ID())
	if !job.Effective().Enabled || job.State.ConsecutiveFailures != 0 || notices != 0 {
		t.Fatal("backpressure paused source", job.State, notices)
	}
	c.engine.Run = func(context.Context, conditiontrigger.Config, json.RawMessage) ([]byte, error) {
		return nil, errors.New("source unavailable")
	}
	for i := 0; i < 30; i++ {
		_, _ = c.poll(ctx, job, "test", time.Time{})
		job, _ = env.svc.Job(ctx, "owner", job.ID())
		if job.State.ConsecutiveFailures == 5 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	job, _ = env.svc.Job(ctx, "owner", job.ID())
	if job.Effective().Enabled || job.State.ConsecutiveFailures != 5 || notices != 1 {
		t.Fatal("script failures did not pause/notify", job.State, notices)
	}
	if job.State.LastError != "source unavailable" {
		t.Fatal(job.State.LastError)
	}
	binding, err := resolveProductConversationBinding(ctx, "owner", job.Profile, job.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, "owner", job.Profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	visible := env.api.eventStore.GetEvents(chat.SessionID, internalevents.GetEventsOptions{})
	b, _ := json.Marshal(visible.Events)
	if !strings.Contains(string(b), "Condition trigger paused") || !strings.Contains(string(b), "source unavailable") {
		t.Fatalf("pause wasn't visible in main Code chat: %s", b)
	}

}

func mustConditionJob(t *testing.T, s *ProductScheduleService, ctx context.Context, id string) productScheduleJob {
	t.Helper()
	j, err := s.Job(ctx, "owner", projectScheduleJobID("code", "checks", id))
	if err != nil {
		t.Fatal(err)
	}
	return j
}
