package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
)

// Exercise caller creation through the real management path and shared
// scheduler state, then restart discovery and use the same function dispatcher.
// Creating the caller must not alter the product it watches.
func TestConditionProductCallerOwnsDetectorAndCallsOwnCrew(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	env, ctx, root := agentEmailCrewFixture(t)
	target := triggerTarget{Kind: triggerCallerCrew, Path: root, CrewID: "mail-checks", CrewProfile: "work", CrewOwner: "owner"}
	if err := writeCrewFunctions(ctx, target, []crewFunction{{Name: "review", Instructions: "Review the supplied ticket."}}); err != nil {
		t.Fatal(err)
	}
	env.mock.mu.Lock()
	before := env.mock.files[root+"/product.json"]
	env.mock.mu.Unlock()
	c := env.svc.conditionService()
	args := map[string]interface{}{"action": "create", "request_id": "crew-watch", "trigger": map[string]interface{}{
		"name": "One ticket", "once": true, "cron_expression": "*/5 * * * *", "timezone": "UTC",
		"script_content": "import json\nprint(json.dumps({'events': [], 'state': {}}))\n",
		"target":         map[string]interface{}{"kind": "function", "function": "review"},
	}}
	out, err := c.manage(ctx, "owner", "work", "mail-checks", args)
	if err != nil {
		t.Fatal(err)
	}
	id := out.(map[string]interface{})["id"].(string)
	job, err := env.svc.Job(ctx, "owner", projectScheduleJobID("work", "mail-checks", id))
	if err != nil {
		t.Fatal(err)
	}
	if job.WatcherSource == nil || !strings.HasPrefix(job.WorkspacePath, "_users/owner/watchers/") || !workspacePathsMatchForUser("owner", job.Schedule.Condition.Target.Name, root) {
		t.Fatal(job)
	}
	if c.caller(job).Stamp.Type != triggerCallerUser {
		t.Fatal("watcher impersonates its associated Crew")
	}
	env.mock.mu.Lock()
	unchanged := sameProductIgnoringMainChatKey(t, before, env.mock.files[root+"/product.json"])
	_, sourceInTarget := env.mock.files[root+"/"+job.Schedule.Condition.Script]
	env.mock.mu.Unlock()
	if !unchanged || sourceInTarget {
		t.Fatal("caller wrote into associated product")
	}
	if _, err := c.manage(ctx, "other", "work", "mail-checks", map[string]interface{}{"action": "read_script", "id": id}); err == nil {
		t.Fatal("another person read private detector")
	}
	// Restart the service; discovery comes from the shared schedule state.
	env.svc.conditions = nil
	c = env.svc.conditionService()
	jobs, err := env.svc.JobsForUser(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, saved := range jobs {
		if saved.ID() == job.ID() {
			found = true
		}
	}
	if !found {
		t.Fatal("caller lost after restart")
	}
	c.engine.Run = func(context.Context, conditiontrigger.Config, json.RawMessage) ([]byte, error) {
		return []byte(`{"events":[{"id":"ticket","payload":{}}],"state":{}}`), nil
	}
	if _, err := c.engine.Poll(ctx, conditionKey(job), *job.Schedule.Condition, time.Now()); err != nil {
		t.Fatal(err)
	}
	pendingList, err := c.manage(ctx, "owner", "work", "mail-checks", map[string]interface{}{"action": "list"})
	if err != nil || len(pendingList.(map[string]interface{})["triggers"].([]interface{})) != 1 {
		t.Fatal("undelivered one-time wait disappeared", pendingList, err)
	}
	c.drain(ctx, job)
	listed, err := c.manage(ctx, "owner", "work", "mail-checks", map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	active := listed.(map[string]interface{})
	if len(active["triggers"].([]interface{})) != 0 || len(active["callers"].([]map[string]interface{})) != 0 {
		t.Fatal("completed wait remains in active Watchers", listed)
	}
	archived, err := c.manage(ctx, "owner", "work", "mail-checks", map[string]interface{}{"action": "list", "include_completed": true})
	if err != nil {
		t.Fatal(err)
	}
	archive := archived.(map[string]interface{})
	if len(archive["triggers"].([]interface{})) != 1 || len(archive["callers"].([]map[string]interface{})) != 1 {
		t.Fatal("completed watcher history lost", archived)
	}
	history, err := c.manage(ctx, "owner", "work", "mail-checks", map[string]interface{}{"action": "history", "id": id})
	if err != nil || len(history.(map[string]interface{})["events"].([]conditiontrigger.Row)) != 1 {
		t.Fatal("completed receipt history lost", history, err)
	}
	ledger, err := c.engine.Read(ctx, conditionKey(job))
	if err != nil || len(ledger.Rows) != 1 || ledger.Rows[0].Status != "dispatched" {
		t.Fatalf("%+v %v", ledger, err)
	}
}

func TestConditionBuilderToolsInCrewWorkflowAndRelay(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	policy := workflowChatPolicy{Mode: "builder", Origin: "interactive", Capabilities: map[string]bool{"plan_authoring": true}}
	for _, relay := range []bool{false, true} {
		manifest, _, err := ReadWorkflowManifest(context.Background(), "Workflow/reports")
		if err != nil {
			t.Fatal(err)
		}
		if relay {
			manifest.Kind = "relay"
		} else {
			manifest.Kind = ""
		}
		raw, _ := json.Marshal(manifest)
		env.mock.mu.Lock()
		env.mock.files[manifestPath("Workflow/reports")] = string(raw)
		env.mock.mu.Unlock()
		reg := &recordingRegistrar{}
		if err := env.api.registerWebhookTools(reg, "owner", "Workflow/reports", policy); err != nil {
			t.Fatal(err)
		}
		if _, ok := reg.tools["manage_condition_triggers"]; !ok {
			t.Fatalf("Builder missing watcher tool relay=%v", relay)
		}
		run := &recordingRegistrar{}
		if err := env.api.registerWebhookTools(run, "owner", "Workflow/reports", workflowChatPolicy{Mode: "run", Origin: "interactive"}); err != nil {
			t.Fatal(err)
		}
		if _, ok := run.tools["manage_condition_triggers"]; ok {
			t.Fatal("Run chat can configure watcher")
		}
	}
	reg := &recordingRegistrar{}
	if err := env.api.registerWorkScheduleTools(reg, "work", "owner", "", linkAlphaPath, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.tools["manage_condition_triggers"]; !ok {
		t.Fatal("Crew Builder missing watcher tool")
	}
}

// sameProductIgnoringMainChatKey compares two product.json documents by content. Reading a Crew records its main chat
// key once (the main-chat model), which rewrites the file; that is not a Watcher writing into the product.
func sameProductIgnoringMainChatKey(t *testing.T, before, after string) bool {
	t.Helper()
	var a, b map[string]interface{}
	if err := json.Unmarshal([]byte(before), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(after), &b); err != nil {
		t.Fatal(err)
	}
	delete(a, "main_chat_key")
	delete(b, "main_chat_key")
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
