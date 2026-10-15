package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

// Real authenticated HTTP notification -> durable wake -> service restart ->
// real Python detector -> target receipt. Bodies cannot choose events/targets;
// secret removal and completion are enforced on subsequent notifications.
func TestConditionWebhookWakeRealPythonRecovery(t *testing.T) {
	env, ctx, root := conditionCodeFixture(t)
	_, env.api.chatStore = productSecretsMigrationTestDocs(t)
	t.Setenv("CAPLAYER_SERVICE_URL", "")
	managedGlobalsMu.Lock()
	oldGlobals, oldManaged := globalSecrets, managedGlobals
	globalSecrets = []globalSecretEntry{{Name: "WAKE_KEY", Value: "example-wake-credential"}}
	managedGlobals = map[string]string{}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() {
		managedGlobalsMu.Lock()
		globalSecrets, managedGlobals = oldGlobals, oldManaged
		managedGlobalsMu.Unlock()
	})
	env.mock.mu.Lock()
	env.mock.files[root+"/product.json"] = `{"schema_version":1,"product":"code","id":"checks","title":"Checks","session_id":"sess-condition-main","capabilities":{"selected_global_secret_names":["WAKE_KEY"]}}`
	env.mock.mu.Unlock()
	c := env.svc.conditionService()
	out, err := c.manage(ctx, "owner", "code", "checks", map[string]interface{}{"action": "create", "request_id": "event-wait", "trigger": map[string]interface{}{
		"name": "Ticket ready", "check_mode": "event", "once": true, "event_secret_name": "WAKE_KEY", "secrets": []string{"WAKE_KEY"},
		"script": "code/triggers/check.py", "target": map[string]interface{}{"kind": "agent", "message": "Review the matching ticket"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	view := out.(map[string]interface{})
	job := mustConditionJob(t, env.svc, ctx, view["id"].(string))
	if job.Schedule.CronExpression != "" || productschedule.Decide(job.Effective(), productschedule.Inputs{Now: time.Now().Add(24 * time.Hour)}).Run {
		t.Fatal("event-only watcher was put on a timer")
	}
	encoded, _ := json.Marshal(view)
	if strings.Contains(string(encoded), "example-wake-credential") {
		t.Fatal("management returned secret value")
	}
	id := conditionWakeID(job)
	// Hold the worker to prove HTTP acceptance is durable before checking,
	// then simulate losing the process and its in-memory worker bookkeeping.
	c.wakeWorkers.Store(id, true)
	router := mux.NewRouter()
	ProductWebhookRoutes(router, env.svc)
	post := func(path, credential string) int {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"events":[{"id":"forged","target":"other"}]}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+credential)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w.Code
	}
	eventPath := view["event_path"].(string)
	if code := post(eventPath, "wrong"); code != 404 {
		t.Fatal("bad credential", code)
	}
	if code := post("/api/hooks/product/00000000-0000-0000-0000-000000000001", "wrong"); code != 404 {
		t.Fatal("unknown receiver", code)
	}
	write := env.svc.writeFile
	env.svc.writeFile = func(ctx context.Context, path, raw string) error {
		if path == conditionWakePath(id) {
			return errors.New("storage interrupted")
		}
		return write(ctx, path, raw)
	}
	if code := post(eventPath, "example-wake-credential"); code != 503 {
		t.Fatal("unpersisted wake accepted", code)
	}
	env.svc.writeFile = write
	for range 3 {
		if code := post(eventPath, "example-wake-credential"); code != 202 {
			t.Fatal("wake refused", code)
		}
	}
	wake, found, err := c.readWake(ctx, id)
	if err != nil || !found || wake.Received != 3 || wake.Checked != 0 {
		t.Fatal("wake not durable", wake, err)
	}
	// Changing the selected credential is effective without changing a Watcher.
	managedGlobalsMu.Lock()
	globalSecrets = nil
	managedGlobalsMu.Unlock()
	if code := post(eventPath, "example-wake-credential"); code != 404 {
		t.Fatal("removed secret still admitted", code)
	}
	managedGlobalsMu.Lock()
	globalSecrets = []globalSecretEntry{{Name: "WAKE_KEY", Value: "example-wake-credential"}}
	managedGlobalsMu.Unlock()

	env.svc.conditions = nil
	c = env.svc.conditionService()
	var checks, deliveries atomic.Int32
	c.engine.Run = func(ctx context.Context, cfg conditiontrigger.Config, state json.RawMessage) ([]byte, error) {
		if err := c.current(ctx, job, cfg); err != nil {
			return nil, err
		}
		checks.Add(1)
		if len(state) == 0 {
			state = json.RawMessage(`{}`)
		}
		cmd := exec.CommandContext(ctx, "python3", "-c", `import json,sys
state=json.load(sys.stdin)
events=[] if state.get("last")=="ticket-123" else [{"id":"ticket-123","payload":{"ticket_id":"123"}}]
print(json.dumps({"events":events,"state":{"last":"ticket-123"}}))`)
		cmd.Stdin = strings.NewReader(string(state))
		return cmd.Output()
	}
	c.engine.Dispatch = func(_ context.Context, _ conditiontrigger.Config, event conditiontrigger.Event, key string) (string, error) {
		if event.ID != "ticket-123" || key != conditiontrigger.Submission(conditionKey(job), event.ID) {
			t.Error("notification chose event or submission")
		}
		deliveries.Add(1)
		return "accepted-ticket", nil
	}
	c.processWake(ctx, job)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.drain(ctx, job)
		fresh, err := env.svc.Job(ctx, "owner", job.ID())
		if err == nil && !fresh.Effective().Enabled {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ledger, err := c.engine.Read(ctx, conditionKey(job))
	if err != nil || ledger.OnceCompletedAt.IsZero() || len(ledger.Rows) != 1 || ledger.Rows[0].Event.ID != "ticket-123" || checks.Load() != 1 || deliveries.Load() != 1 {
		t.Fatal("restart/coalescing/once delivery", ledger, err, checks.Load(), deliveries.Load())
	}
	wake, _, err = c.readWake(ctx, id)
	if err != nil || wake.Checked != 3 {
		t.Fatal("accepted wakes not checkpointed", wake, err)
	}
	if code := post(eventPath, "example-wake-credential"); code != 404 {
		t.Fatal("completed watcher still admitted", code)
	}
	// A running recurring check absorbs a burst, then performs one follow-up.
	recurring, err := c.manage(ctx, "owner", "code", "checks", map[string]interface{}{"action": "create", "request_id": "event-recurring", "trigger": map[string]interface{}{
		"name": "Keep watching", "check_mode": "both", "cron_expression": "*/5 * * * *", "event_secret_name": "WAKE_KEY", "secrets": []string{"WAKE_KEY"},
		"script": "code/triggers/check.py", "target": map[string]interface{}{"kind": "agent", "message": "Review matches"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	job = mustConditionJob(t, env.svc, ctx, recurring.(map[string]interface{})["id"].(string))
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	c.engine.Run = func(context.Context, conditiontrigger.Config, json.RawMessage) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return []byte(`{"events":[],"state":{}}`), nil
	}
	if _, err := c.wake(ctx, job); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("event did not wake check")
	}
	for range 20 {
		if coalesced, err := c.wake(ctx, job); err != nil || !coalesced {
			close(release)
			t.Fatal("burst did not coalesce", err)
		}
	}
	close(release)
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		m := c.wakeLock(conditionWakeID(job))
		m.Lock()
		wake, _, err = c.readWake(ctx, conditionWakeID(job))
		m.Unlock()
		if err == nil && wake.Received == wake.Checked {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if wake.Received != 21 || wake.Checked != 21 || calls.Load() != 2 {
		t.Fatal("in-flight burst lost or repeated checks", wake, calls.Load())
	}

}
