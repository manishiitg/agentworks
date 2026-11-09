package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

type builderOperationFixture struct {
	*externalToolsFixture
	sessions map[string]string
}

func newBuilderOperationFixture(t *testing.T) *builderOperationFixture {
	t.Helper()
	tokenTestSetup(t)
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "true")
	f := newExternalToolsFixture(t)
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoices","created_by":"owner","access":{"owners":["owner","outsider"],"readers":["reader"]}}`)
	f.api = newConversationTurnQueueTestAPI(map[string]string{})
	// These tests drive the durable queue explicitly so no unobserved worker
	// reaches a model, and every test owns the lifetime of its runtime hook.
	f.api.activeSessions = map[string]*ActiveSessionInfo{}
	fixture := &builderOperationFixture{externalToolsFixture: f, sessions: map[string]string{"owner": uuid.NewString(), "outsider": uuid.NewString()}}
	for user, session := range fixture.sessions {
		f.api.activeSessions[session] = &ActiveSessionInfo{SessionID: session, UserID: user, AgentMode: "workflow_phase", PhaseID: "workflow-builder", WorkspacePath: "Workflow/invoices", PresetQueryID: "invoices", Status: "completed", LastActivity: time.Now()}
		f.api.conversationTurnQueueDraining[session] = true
	}
	return fixture
}
func (f *builderOperationFixture) issue(t *testing.T, user string) (accesstokens.Token, string) {
	t.Helper()
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	token, raw, err := store.Issue(context.Background(), accesstokens.Token{Name: "Builder", UserID: user, Username: user, Scopes: builderConsentScopes, WorkflowIDs: []string{"invoices"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return token, raw
}
func (f *builderOperationFixture) call(t *testing.T, raw, name string, args map[string]any, want int) *httptest.ResponseRecorder {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	args["workflow_id"] = "invoices"
	name, args = canonicalTestCall(name, args)
	data, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/external/v1/call", strings.NewReader(string(data)))
	r.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	AuthMiddleware(http.HandlerFunc(f.api.handleExternalCall)).ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s status %d want %d: %s", name, w.Code, want, w.Body)
	}
	return w
}
func TestExternalBuilderLiveGrantHonorsWorkflowAllowlist(t *testing.T) {
	f := newBuilderOperationFixture(t)
	token, _ := f.issue(t, "owner")
	claims, err := accessTokenClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateBuilderGrant(t.Context(), claims, "invoices", "Workflow/invoices"); err != nil {
		t.Fatal(err)
	}
	f.write(t, userProductAccessFilePath(), `{"owner":{"workflow_ids":["secret"]}}`)
	if _, err := validateBuilderGrant(t.Context(), claims, "invoices", "Workflow/invoices"); err == nil {
		t.Fatal("removed allow-list entry still authorized Builder")
	}
}
