package server

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	"net/http/httptest"
	"path/filepath"
	"testing"
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
