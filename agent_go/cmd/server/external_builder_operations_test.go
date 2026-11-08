package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
func (f *builderOperationFixture) claim(t *testing.T, user string) (queuedConversationTurn, context.Context, map[string]interface{}) {
	t.Helper()
	turn, ok := f.api.claimNextConversationTurn(context.Background(), user, f.sessions[user])
	if !ok {
		t.Fatal("missing queued turn")
	}
	request, err := queryRequestToMap(turn.Request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := f.api.queuedConversationTurnContext(turn, request)
	if err != nil {
		t.Fatal(err)
	}
	return turn, ctx, request
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

func TestExternalBuilderOperationsBrowserRetryQueuesOnce(t *testing.T) {
	f := newBuilderOperationFixture(t)
	session := f.sessions["owner"]
	f.api.internalChatSubmissionStore = newTestChatSubmissionStore()
	canceled := atomic.Bool{}
	f.api.externalBuilderRuntime.sessions = map[string]*externalBuilderActive{
		session: {id: "active-builder", cancel: func() { canceled.Store(true) }},
	}
	body, err := json.Marshal(QueryRequest{Query: "Browser follow-up", AgentMode: "workflow_phase", PhaseID: "workflow-builder", PresetQueryID: "invoices", SelectedFolder: "Workflow/invoices"})
	if err != nil {
		t.Fatal(err)
	}
	var firstID string
	for range 2 {
		r := httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(string(body)))
		r.Header.Set("X-Session-ID", session)
		r.Header.Set("Idempotency-Key", "browser-retry")
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"}))
		w := httptest.NewRecorder()
		f.api.handleQuery(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("browser admission: %d %s", w.Code, w.Body)
		}
		var response QueryResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.QueryID == "" || response.DeliveryStatus != "queued_for_turn" {
			t.Fatalf("missing queue receipt: %+v", response)
		}
		if firstID != "" && firstID != response.QueryID {
			t.Fatal("browser retry created a second turn")
		}
		firstID = response.QueryID
	}
	rows := f.api.mustReadTurnQueueForTest(t, "owner")
	if len(rows) != 1 || rows[0].SubmissionID != "browser-retry" || rows[0].Principal.AccessTokenID != "" || rows[0].Principal.ExternalBuilderOperationID != "" {
		t.Fatalf("browser retry lost its submission or authority: %+v", rows)
	}
	if canceled.Load() || !f.api.externalBuilderOwnsSession(session) {
		t.Fatal("browser follow-up interrupted the Builder operation")
	}
}
