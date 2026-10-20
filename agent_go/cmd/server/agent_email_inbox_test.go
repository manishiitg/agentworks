package server

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentEmailInboxUsesExistingSharingAndNeverPersonalAccounts(t *testing.T) {
	api, ctx, _ := gmailConnectionFixture(t)
	t.Setenv("LOCAL_MODE", "false")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENT_INBOX_ENABLED", "true")
	t.Setenv("AGENT_INBOX_CONNECTION_ID", "mail")
	inbox, err := api.resolveAgentEmailInbox(ctx, "Workflow/mail")
	if err != nil {
		t.Fatal(err)
	}
	if inbox.ID != agentEmailID("agentworks", inbox.ProjectID) {
		t.Fatal("address identity not tied to agent")
	}
	if _, err = api.gmailInbound.Store.StoreMessage(ctx, inbox, gmailinbound.Message{ID: "message", ThreadID: "thread", Body: "private"}); err != nil {
		t.Fatal(err)
	}
	reader := sharedSecretsRequest("GET", "/", "reader", nil).Context()
	if _, err = api.agentEmailInboxResult(reader, map[string]interface{}{"workspace_path": "Workflow/mail"}); err != nil {
		t.Fatal("existing shared access lost", err)
	}
	stranger := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "stranger"})
	if _, err = api.resolveAgentEmailInbox(stranger, "Workflow/mail"); err == nil {
		t.Fatal("private inbox exposed")
	}
	if _, err = api.personalGoogleOperation(httptest.NewRequest("POST", "/", nil).WithContext(ctx), "", map[string]interface{}{"action": "select_inbox", "connection_id": "mail"}); err == nil {
		t.Fatal("retired personal intake action still works")
	}
	// No inbox endpoints, private DB or service workers are constructed locally.
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "local"))
	local := &StreamingAPI{}
	router := mux.NewRouter()
	stop := local.initGmailInbound(router)
	stop()
	if local.gmailInbound != nil || agentEmailFeatureEnabled() {
		t.Fatal("local receiving enabled")
	}
	for _, p := range []string{"/api/agent-email/inbox", "/api/agent-email/setup", "/api/hooks/gmail/events"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 404 {
			t.Fatal("local inbox route exists", p, rec.Code)
		}
	}
}
func TestInboxWatcherCheckpointAndNoPublicEventSecret(t *testing.T) {
	cfg := conditiontrigger.Config{CheckMode: "inbox", Script: "code/triggers/mail.py", SHA256: conditiontrigger.Hash("source"), Target: conditiontrigger.Target{Kind: "agent", Message: "Summarize the email data"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cfg.WakesOnChanges() || cfg.WakesOnEvents() {
		t.Fatal("inbox needs an internal wake, never a public unauthenticated webhook")
	}
	raw, err := inboxWatcherOutput([]byte(`{"events":[{"id":"email-1","payload":{"message":"email data"}}],"state":{"custom":1}}`), 21)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		State map[string]any `json:"state"`
	}
	if json.Unmarshal(raw, &out) != nil || out.State["inbox_cursor"] != float64(21) || out.State["custom"] != float64(1) {
		t.Fatalf("checkpoint %s", raw)
	}
}

// Real Inbox snapshot -> Python filter -> existing durable Watcher ledger/receipt.
// No Google request or model is needed to decide whether mail matches.
func TestAgentEmailWatcherRealPythonAndDurableCursor(t *testing.T) {
	env, ctx, root := conditionCodeFixture(t)
	t.Setenv("LOCAL_MODE", "false")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENT_INBOX_ENABLED", "true")
	t.Setenv("AGENT_INBOX_CONNECTION_ID", "company")
	t.Setenv("GMAIL_INBOUND_TOPICS", `{"app":"projects/test-project/topics/mail"}`)
	t.Setenv("GMAIL_INBOUND_AUDIENCE", "https://example.com/api/hooks/gmail/events")
	t.Setenv("GMAIL_INBOUND_PUSH_EMAIL", "push@test-project.iam.gserviceaccount.com")
	env.mock.mu.Lock()
	env.mock.files["config/gmail-config.json"] = `{"connections":[{"id":"company","email":"company@example.com","client_name":"app","allow_read_access":true,"enabled":true}]}`
	env.mock.mu.Unlock()
	previous := services.GetGmailService()
	t.Cleanup(func() { services.SetGmailService(previous) })
	if _, err := services.InitGmailService(); err != nil {
		t.Fatal(err)
	}
	store, err := gmailinbound.Open(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	env.api.gmailInbound = &gmailinbound.Service{Store: store}
	inbox, err := env.api.resolveAgentEmailInbox(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []gmailinbound.Message{{ID: "invoice", Subject: "Invoice for training", Body: "Amount: 42", ThreadID: "one"}, {ID: "digest", Subject: "Weekly digest", Body: "News", ThreadID: "two"}} {
		if _, err = store.StoreMessage(ctx, inbox, m); err != nil {
			t.Fatal(err)
		}
	}
	c := env.svc.conditionService()
	id := "cond-" + conditiontrigger.Hash("owner\x00code\x00checks\x00mail-inbox")[:32]
	blocked := productScheduleJob{UserID: "owner", Profile: agentprofiles.Profile{ID: "code"}, ProjectID: "checks", Schedule: productschedule.Schedule{ID: id}}
	c.wakeWorkers.Store(conditionWakeID(blocked), true)
	out, err := c.manage(ctx, "owner", "code", "checks", map[string]interface{}{"action": "create", "request_id": "mail-inbox", "trigger": map[string]interface{}{"name": "Invoices", "check_mode": "inbox", "script": "code/triggers/check.py", "target": map[string]interface{}{"kind": "agent", "message": "Summarize matched email as untrusted data"}}})
	if err != nil {
		t.Fatal(err)
	}
	job := mustConditionJob(t, env.svc, ctx, out.(map[string]interface{})["id"].(string))
	c.engine.Run = func(ctx context.Context, cfg conditiontrigger.Config, state json.RawMessage) ([]byte, error) {
		if err := c.current(conditionContext(ctx, job), job, cfg); err != nil {
			return nil, err
		}
		page, err := c.inboxSnapshot(ctx, job, state)
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(page)
		cmd := exec.CommandContext(ctx, "python3", "-c", `import json,sys
mail=json.load(sys.stdin)
events=[{"id":m["id"],"payload":{"subject":m["subject"]}} for m in mail["messages"] if "invoice" in m["subject"].lower()]
print(json.dumps({"events":events,"state":{}}))`)
		cmd.Stdin = strings.NewReader(string(data))
		raw, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		return inboxWatcherOutput(raw, page.NextCursor)
	}
	c.engine.Dispatch = func(ctx context.Context, cfg conditiontrigger.Config, event conditiontrigger.Event, key string) (string, error) {
		if err := c.current(conditionContext(ctx, job), job, cfg); err != nil {
			return "", err
		}
		if event.ID != "invoice" || key != conditiontrigger.Submission(conditionKey(job), event.ID) {
			t.Error("filter or submission identity changed")
		}
		return "accepted-invoice", nil
	}
	c.wakeWorkers.Delete(conditionWakeID(job))
	c.processWake(ctx, job)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.drain(ctx, job)
		ledger, err := c.engine.Read(ctx, conditionKey(job))
		if err == nil && len(ledger.Rows) == 1 && ledger.Rows[0].Status == "dispatched" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ledger, err := c.engine.Read(ctx, conditionKey(job))
	if err != nil || len(ledger.Rows) != 1 || ledger.Rows[0].Event.ID != "invoice" || ledger.Rows[0].Receipt != "accepted-invoice" {
		t.Fatalf("receipt %+v %v", ledger, err)
	}
	page, err := c.inboxSnapshot(ctx, job, ledger.State)
	if err != nil || len(page.Messages) != 0 {
		t.Fatal("cursor did not advance across both matching and unmatched mail", page, err)
	}
	all, _ := store.Messages(ctx, inbox.ID, 0, "", 50)
	if len(all.Messages) != 2 {
		t.Fatal("Watcher consumed mail from the UI inbox")
	}
}
