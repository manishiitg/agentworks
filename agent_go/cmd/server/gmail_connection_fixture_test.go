package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func gmailConnectionFixture(t *testing.T) (*StreamingAPI, context.Context, *mockWorkspaceAPI) {
	t.Helper()
	t.Setenv("LOCAL_MODE", "false")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","email":"owner@example.com","can_create":true},{"id":"reader","email":"reader@example.com"}]}`)
	dir := t.TempDir()
	t.Setenv("GMAIL_CONNECTIONS_DIR", filepath.Join(dir, "connections"))
	t.Setenv("GMAIL_OAUTH_TOKEN_DIR", filepath.Join(dir, "tokens"))
	t.Setenv("GOG_HOME", filepath.Join(dir, "store"))
	t.Setenv("GMAIL_INBOUND_TOPICS", `{"app":"projects/test-project/topics/mail"}`)
	t.Setenv("GMAIL_INBOUND_AUDIENCE", "https://example.com/api/hooks/gmail/events")
	t.Setenv("GMAIL_INBOUND_PUSH_EMAIL", "push@test-project.iam.gserviceaccount.com")
	binary := filepath.Join(dir, "gog")
	script := "#!/bin/sh\ncat <<'JSON'\n" + `{"accounts":[{"email":"owner@example.com","client":"app","valid":true,"scopes":["https://www.googleapis.com/auth/gmail.readonly","https://www.googleapis.com/auth/gmail.send"]}]}` + "\nJSON\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	manifest := NewWorkflowManifest("Gmail routing")
	manifest.CreatedBy = "owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}, Readers: []string{"reader"}}
	raw, _ := json.Marshal(manifest)
	config := services.GmailConfig{GogPath: binary, UseGogBackend: true, Connections: []services.GmailConnection{{ID: "mail", Email: "owner@example.com", OwnerID: "owner", ClientName: "app", AuthBackend: "gog", Enabled: true, AllowReadAccess: true, Scopes: []string{services.GmailReadonlyScope, "https://www.googleapis.com/auth/gmail.send"}}}}
	configRaw, _ := json.Marshal(config)
	mock := &mockWorkspaceAPI{files: map[string]string{
		"Workflow/mail/workflow.json":            string(raw),
		"Workflow/mail/planning/plan.json":       `{"steps":[{"type":"routing","id":"triage","title":"Choose work","routing_question":"Which?","routes":[{"route_id":"support","route_name":"Support","next_step_id":"work"},{"route_id":"billing","route_name":"Billing","next_step_id":"work"}]},{"type":"scripted","id":"work","title":"Work","description":"Work"}]}`,
		"Workflow/mail/variables/variables.json": `{"groups":[{"name":"prod"}]}`,
		"config/gmail-config.json":               string(configRaw),
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	old := services.GetGmailService()
	t.Cleanup(func() { services.SetGmailService(old) })
	if _, err := services.InitGmailService(); err != nil {
		t.Fatal(err)
	}
	store, err := gmailinbound.Open(filepath.Join(dir, "mail.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	api := &StreamingAPI{gmailInbound: &gmailinbound.Service{Store: store}}
	api.scheduler = NewSchedulerService(api)
	return api, sharedSecretsRequest("GET", "/", "owner", nil).Context(), mock
}
