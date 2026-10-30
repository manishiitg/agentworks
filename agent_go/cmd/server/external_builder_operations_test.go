package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
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
func (f *builderOperationFixture) submit(t *testing.T, raw, key, message string) externalBuilderOperation {
	t.Helper()
	w := f.call(t, raw, "sessions", map[string]any{"action": "builder_chat", "submission_id": key, "message": message}, 200)
	var op externalBuilderOperation
	if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil {
		t.Fatal(err)
	}
	if op.ID == "" {
		t.Fatalf("missing operation: %s", w.Body)
	}
	return op
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

func TestExternalBuilderOperationsContinueMainChatAndSeparateEditor(t *testing.T) {
	f := newBuilderOperationFixture(t)
	_, owner := f.issue(t, "owner")
	_, editor := f.issue(t, "outsider")
	first := f.submit(t, owner, "first", "Update the plan")
	if first.SessionID != f.sessions["owner"] {
		t.Fatalf("owner forked main chat: %+v", first)
	}
	duplicate := f.submit(t, owner, "first", "Update the plan")
	if duplicate.ID != first.ID || len(f.api.mustReadTurnQueueForTest(t, "owner")) != 1 {
		t.Fatal("duplicate submission queued twice")
	}
	f.call(t, owner, "sessions", map[string]any{"action": "builder_chat", "submission_id": "first", "message": "Different edit"}, 409)
	second := f.submit(t, editor, "editor", "Adjust validation")
	if second.SessionID != f.sessions["outsider"] || second.SessionID == first.SessionID {
		t.Fatalf("editor did not use own chat: %+v", second)
	}
	f.call(t, editor, "sessions", map[string]any{"action": "builder_chat", "submission_id": "foreign", "message": "Read owner's chat", "session_id": first.SessionID}, 404)
	_, replacement := f.issue(t, "owner")
	f.call(t, replacement, "sessions", map[string]any{"action": "builder_status", "operation_id": first.ID}, 404)
	f.call(t, owner, "sessions", map[string]any{"action": "builder_status", "operation_id": first.ID}, 401)
}

func TestExternalBuilderReservationDoesNotCoverChatLookupOrQueue(t *testing.T) {
	f := newBuilderOperationFixture(t)
	token, raw := f.issue(t, "owner")
	claims, err := accessTokenClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), UserContextKey, claims)
	main, err := f.api.goalOwnerTabSummary(ctx, workflowBuilderScope{PresetID: "invoices", Workspace: "Workflow/invoices"}, false)
	if err != nil {
		t.Fatal(err)
	}
	reservation := productConversationRegistryMutex("external-builder:owner:Workflow/invoices")
	var nested, registryRead atomic.Bool
	checkUnlocked := func() {
		if !reservation.TryLock() {
			nested.Store(true)
			return
		}
		reservation.Unlock()
	}
	upstream, err := url.Parse(os.Getenv("WORKSPACE_API_URL"))
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/"+productConversationRegistryPath("owner")) {
			registryRead.Store(true)
			checkUnlocked()
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(probe.Close)
	t.Setenv("WORKSPACE_API_URL", probe.URL)
	writeQueue := f.api.internalTurnQueueWrite
	f.api.internalTurnQueueWrite = func(ctx context.Context, path, value string) error {
		checkUnlocked()
		return writeQueue(ctx, path, value)
	}
	first := f.submit(t, raw, "lock-scope", "Update the plan")
	retry := f.submit(t, raw, "lock-scope", "Update the plan")
	if !registryRead.Load() || nested.Load() {
		t.Fatal("chat lookup or queue persistence ran under the Builder reservation")
	}
	if first.SessionID != main.SessionID || retry.ID != first.ID || len(f.api.mustReadTurnQueueForTest(t, "owner")) != 1 {
		t.Fatal("Builder submission did not reuse the existing chat and one queued operation")
	}
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

func TestExternalBuilderPlanWriteRecordsGrantAndOperation(t *testing.T) {
	f := newBuilderOperationFixture(t)
	token, raw := f.issue(t, "owner")
	op := f.submit(t, raw, "audit-plan", "Edit the plan")
	_, ctx, _ := f.claim(t, "owner")
	reg := &chatPolicyTestDefinition{}
	called := false
	if err := externalBuilderRegistrar(reg, GetUserFromContext(ctx)).RegisterCustomTool("update_step", "", nil,
		func(context.Context, map[string]interface{}) (string, error) { called = true; return "ok", nil }, "workflow"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.tools["update_step"].exec(ctx, map[string]interface{}{"step_id": "fetch-invoices"}); err != nil || !called {
		t.Fatalf("plan edit failed: %v", err)
	}
	s, err := openExternalBuilderStore()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var operationID, grantID, path, status string
	if err := s.db.QueryRow(`SELECT operation_id,grant_id,path,status FROM external_builder_edits WHERE tool='update_step'`).Scan(&operationID, &grantID, &path, &status); err != nil {
		t.Fatal(err)
	}
	if operationID != op.ID || grantID != token.ID || path != "planning/plan.json#step=fetch-invoices" || status != "completed" {
		t.Fatalf("plan edit audit mismatch: operation=%s grant=%s path=%s status=%s", operationID, grantID, path, status)
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

func TestExternalBuilderOperationsQueuedCancelLeavesBrowserWork(t *testing.T) {
	f := newBuilderOperationFixture(t)
	_, raw := f.issue(t, "owner")
	browserCanceled := atomic.Bool{}
	f.api.agentCancelFuncs = map[string]context.CancelFunc{f.sessions["owner"]: func() { browserCanceled.Store(true) }}
	browserCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	browser, _, err := f.api.enqueueConversationTurn(browserCtx, "owner", f.sessions["owner"], QueryRequest{Query: "browser follow-up", AgentMode: "workflow_phase"})
	if err != nil {
		t.Fatal(err)
	}
	op := f.submit(t, raw, "queued", "Edit the plan")
	f.call(t, raw, "sessions", map[string]any{"action": "builder_cancel", "operation_id": op.ID}, 200)
	f.call(t, raw, "sessions", map[string]any{"action": "builder_cancel", "operation_id": op.ID}, 200)
	if browserCanceled.Load() {
		t.Fatal("queued cancel stopped unrelated browser work")
	}
	rows := f.api.mustReadTurnQueueForTest(t, "owner")
	if len(rows) != 1 || rows[0].ID != browser.ID {
		t.Fatalf("canceled operation remained queued or removed browser work: %+v", rows)
	}
}

func TestExternalBuilderOperationsRecheckGrantBeforeQueuedExecution(t *testing.T) {
	f := newBuilderOperationFixture(t)
	token, raw := f.issue(t, "owner")
	op := f.submit(t, raw, "revoked", "Edit the plan")
	turn, ctx, request := f.claim(t, "owner")
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	err = store.Revoke(context.Background(), token.ID, "owner", time.Now())
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	called := false
	f.api.internalExternalBuilderTurn = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		called = true
		return internalSessionTurnResult{}, nil
	}
	if _, err := f.api.runQueuedExternalBuilder(ctx, turn, request); err == nil || called {
		t.Fatal("revoked queued grant executed")
	}
	saved, err := readExternalBuilder(context.Background(), op.ID)
	if err != nil || saved.Status != "failed" {
		t.Fatalf("revoked operation result: %+v %v", saved, err)
	}
}

func TestExternalBuilderOperationsNeverReplayUncertainStartedTurn(t *testing.T) {
	f := newBuilderOperationFixture(t)
	_, raw := f.issue(t, "owner")
	op := f.submit(t, raw, "restart", "Edit exactly once")
	turn, ctx, request := f.claim(t, "owner")
	store, err := openExternalBuilderStore()
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`UPDATE external_builder_operations SET status='running',started_at=? WHERE id=?`, time.Now().UnixNano(), op.ID)
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	called := false
	f.api.internalExternalBuilderTurn = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		called = true
		return internalSessionTurnResult{}, nil
	}
	if _, err := f.api.runQueuedExternalBuilder(ctx, turn, request); err == nil || called {
		t.Fatal("uncertain operation was replayed")
	}
	w := f.call(t, raw, "sessions", map[string]any{"action": "builder_status", "operation_id": op.ID}, 200)
	if !strings.Contains(w.Body.String(), `"status":"interrupted"`) {
		t.Fatalf("uncertain result: %s", w.Body)
	}
	retry := f.submit(t, raw, "restart", "Edit exactly once")
	if retry.ID != op.ID || retry.Status != "interrupted" {
		t.Fatalf("retry resurrected operation: %+v", retry)
	}
}

func TestExternalBuilderOperationsFeedbackAndRunningCancelStayScoped(t *testing.T) {
	f := newBuilderOperationFixture(t)
	_, raw := f.issue(t, "owner")
	session := f.sessions["owner"]
	feedback := virtualtools.GetHumanFeedbackStore()
	previousID := uuid.NewString()
	if err := feedback.CreatePendingRequest(previousID, "Earlier browser question", "", session, nil, true, time.Minute); err != nil {
		t.Fatal(err)
	}
	op := f.submit(t, raw, "question", "Ask before changing")
	turn, ctx, request := f.claim(t, "owner")
	ready := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	defer once.Do(func() { close(finish) })
	ownID := uuid.NewString()
	foreignID := uuid.NewString()
	concurrentBrowserID := uuid.NewString()
	pendingCancelID := uuid.NewString()
	f.api.internalExternalBuilderTurn = func(runCtx context.Context, _ map[string]interface{}, _, _ string) (internalSessionTurnResult, error) {
		if err := feedback.CreatePendingRequest(ownID, "Continue?", "", session, []string{"Yes", "No"}, false, time.Minute, op.ID); err != nil {
			close(ready)
			return internalSessionTurnResult{}, err
		}
		if err := feedback.CreatePendingRequest(foreignID, "Other chat question", "", f.sessions["outsider"], nil, true, time.Minute); err != nil {
			close(ready)
			return internalSessionTurnResult{}, err
		}
		if err := feedback.CreatePendingRequest(concurrentBrowserID, "New browser background question", "", session, nil, true, time.Minute); err != nil {
			close(ready)
			return internalSessionTurnResult{}, err
		}
		if err := feedback.CreatePendingRequest(pendingCancelID, "Another operation question", "", session, nil, true, time.Minute, op.ID); err != nil {
			close(ready)
			return internalSessionTurnResult{}, err
		}
		close(ready)
		select {
		case <-runCtx.Done():
			return internalSessionTurnResult{}, runCtx.Err()
		case <-finish:
			return internalSessionTurnResult{FinalResponse: "finished"}, nil
		}
	}
	go func() { _, err := f.api.runQueuedExternalBuilder(ctx, turn, request); done <- err }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not start")
	}
	// Always join the hook worker before test state/environment is removed.
	defer func() {
		once.Do(func() { close(finish) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("runtime failed to stop")
		}
	}()
	w := f.call(t, raw, "sessions", map[string]any{"action": "builder_status", "operation_id": op.ID}, 200)
	if !strings.Contains(w.Body.String(), ownID) || strings.Contains(w.Body.String(), previousID) || strings.Contains(w.Body.String(), foreignID) || strings.Contains(w.Body.String(), concurrentBrowserID) {
		t.Fatalf("question scope: %s", w.Body)
	}
	f.call(t, raw, "sessions", map[string]any{"action": "builder_reply_input", "operation_id": op.ID, "request_id": previousID, "response": "yes"}, 409)
	f.call(t, raw, "sessions", map[string]any{"action": "builder_reply_input", "operation_id": op.ID, "request_id": foreignID, "response": "yes"}, 409)
	f.call(t, raw, "sessions", map[string]any{"action": "builder_reply_input", "operation_id": op.ID, "request_id": ownID, "response": "invalid"}, 409)
	f.call(t, raw, "sessions", map[string]any{"action": "builder_reply_input", "operation_id": op.ID, "request_id": ownID, "response": "Yes"}, 200)
	f.call(t, raw, "sessions", map[string]any{"action": "builder_cancel", "operation_id": op.ID}, 200)
	oldStillPending := false
	concurrentStillPending := false
	for _, pending := range feedback.PendingForSession(session, time.Now()) {
		if pending.UniqueID == previousID {
			oldStillPending = true
		}
		if pending.UniqueID == concurrentBrowserID {
			concurrentStillPending = true
		}
		if pending.UniqueID == pendingCancelID {
			t.Error("cancel did not withdraw this operation's unanswered question")
		}
	}
	if !oldStillPending || !concurrentStillPending {
		t.Error("cancel withdrew unrelated browser question in shared chat")
	}
	f.call(t, raw, "sessions", map[string]any{"action": "builder_reply_input", "operation_id": op.ID, "request_id": ownID, "response": "No"}, 409)
}

func TestExternalBuilderOperationsLiveRoleDowngradeBlocksExecution(t *testing.T) {
	f := newBuilderOperationFixture(t)
	_, raw := f.issue(t, "owner")
	f.submit(t, raw, "downgrade", "Update the plan")
	turn, ctx, request := f.claim(t, "owner")
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoices","access":{"owners":["outsider"],"readers":["owner"]}}`)
	called := false
	f.api.internalExternalBuilderTurn = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		called = true
		return internalSessionTurnResult{}, nil
	}
	if _, err := f.api.runQueuedExternalBuilder(ctx, turn, request); err == nil || called {
		t.Fatal("lost write access did not prevent queued Builder execution")
	}
}

func TestExternalBuilderOperationsCompletionUsesExactResultAndPrincipal(t *testing.T) {
	f := newBuilderOperationFixture(t)
	token, raw := f.issue(t, "owner")
	op := f.submit(t, raw, "complete", "Update the plan")
	turn, ctx, request := f.claim(t, "owner")
	f.api.internalExternalBuilderTurn = func(runCtx context.Context, req map[string]interface{}, session, user string) (internalSessionTurnResult, error) {
		claims := GetUserFromContext(runCtx)
		if claims == nil || claims.AccessToken == nil || claims.AccessToken.ID != token.ID || claims.ExternalBuilderOperationID != op.ID || session != f.sessions["owner"] || user != "owner" {
			t.Error("runtime lost the operation grant or main chat binding")
		}
		return internalSessionTurnResult{FinalResponse: "Updated the validation plan."}, nil
	}
	if _, err := f.api.runQueuedExternalBuilder(ctx, turn, request); err != nil {
		t.Fatal(err)
	}
	w := f.call(t, raw, "sessions", map[string]any{"action": "builder_status", "operation_id": op.ID}, 200)
	if !strings.Contains(w.Body.String(), `"status":"completed"`) || !strings.Contains(w.Body.String(), `"answer":"Updated the validation plan."`) {
		t.Fatalf("exact result missing: %s", w.Body)
	}
	f.call(t, raw, "sessions", map[string]any{"action": "builder_cancel", "operation_id": op.ID}, 200)
	saved, err := readExternalBuilder(context.Background(), op.ID)
	if err != nil || saved.Status != "completed" || saved.Answer != "Updated the validation plan." {
		t.Fatalf("late cancel rewrote completed work: %+v %v", saved, err)
	}
}

func TestExternalBuilderOperationsRealMCPTransportAndDiscovery(t *testing.T) {
	f := newBuilderOperationFixture(t)
	token, _ := f.issue(t, "owner")
	claims, err := accessTokenClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	srv := serveExternalMCP(t, f.api, claims)
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	spec := callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{})
	requireRemoteSuccess(t, spec, "Builder catalog")
	builderSpec := marshalStructured(t, callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{"names": []any{"sessions"}}))
	for _, action := range []string{"builder_chat", "builder_status", "builder_reply_input", "builder_cancel", "file_history", "restore_file"} {
		if !strings.Contains(builderSpec, `"`+action+`"`) {
			t.Fatalf("enabled scoped catalog: builder has no %s action", action)
		}
	}
	history := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "sessions", "arguments": map[string]any{"action": "file_history", "workflow_id": "invoices", "path": "code/task.py"}})
	requireRemoteSuccess(t, history, "Builder file history")
	if !strings.Contains(marshalStructured(t, history), `"edits":[]`) {
		t.Fatalf("unexpected file history: %s", marshalStructured(t, history))
	}
	submitted := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "sessions", "arguments": map[string]any{"action": "builder_chat", "workflow_id": "invoices", "submission_id": "transport", "message": "Update validation"}})
	requireRemoteSuccess(t, submitted, "Builder submit")
	var op externalBuilderOperation
	if err := json.Unmarshal([]byte(marshalStructured(t, submitted)), &op); err != nil || op.ID == "" || op.SessionID != f.sessions["owner"] {
		t.Fatalf("transport submit: %+v %v", op, err)
	}
	status := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "sessions", "arguments": map[string]any{"action": "builder_status", "workflow_id": "invoices", "operation_id": op.ID}})
	requireRemoteSuccess(t, status, "Builder status")
	if !strings.Contains(marshalStructured(t, status), `"status":"queued"`) {
		t.Fatalf("unexpected status: %s", marshalStructured(t, status))
	}
	canceled := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "sessions", "arguments": map[string]any{"action": "builder_cancel", "workflow_id": "invoices", "operation_id": op.ID}})
	requireRemoteSuccess(t, canceled, "Builder cancel must not become an MCP HTTP-status error")
	if !strings.Contains(marshalStructured(t, canceled), `"status":"canceled"`) {
		t.Fatalf("missing cancel acknowledgement: %s", marshalStructured(t, canceled))
	}
	// Personal sign-ins now follow the website role. The Run-only contract is
	// a shared project connection, not a personal token's historical scopes.
	t.Setenv("PUBLIC_URL", "https://agentworks.example.com")
	readToken := mcpOAuthTokenForGrant(mcpOAuthGrant{UserID: "reader", Username: "reader", FamilyID: "run-project", Resource: "https://agentworks.example.com" + projectMCPPrefix + "workflow/invoices/use", Scopes: []string{"workflows:read", "files:read", "runs:execute"}, Expires: time.Now().Add(time.Hour)})
	readClaims, err := accessTokenClaims(readToken)
	if err != nil {
		t.Fatal(err)
	}
	readServer := serveExternalMCP(t, f.api, readClaims)
	readClient := dialExternalMCP(t, ctx, readServer.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, readClient)
	readSpec := callRemoteTool(t, ctx, readClient, externalMCPToolSpec, map[string]any{})
	requireRemoteSuccess(t, readSpec, "read/run catalog")
	var runCatalog struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(marshalStructured(t, readSpec)), &runCatalog); err != nil {
		t.Fatal(err)
	}
	hasRuns := false
	for _, tool := range runCatalog.Tools {
		hasRuns = hasRuns || tool.Name == "runs"
	}
	if !hasRuns {
		t.Fatal("shared Run connection did not discover its allowed runs")
	}
	var sessionShape struct {
		Schemas map[string]struct {
			InputSchema struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"inputSchema"`
		} `json:"schemas"`
	}
	runSessions := callRemoteTool(t, ctx, readClient, externalMCPToolSpec, map[string]any{"names": "sessions"})
	if !runSessions.IsError {
		if err := json.Unmarshal([]byte(marshalStructured(t, runSessions)), &sessionShape); err != nil {
			t.Fatal(err)
		}
		for _, verb := range sessionShape.Schemas["sessions"].InputSchema.Properties["action"].Enum {
			if verb == "builder_chat" {
				t.Fatal("run-only token discovered Builder action")
			}
		}
	}
	denied := callRemoteTool(t, ctx, readClient, externalMCPToolCall, map[string]any{"name": "sessions", "arguments": map[string]any{"action": "builder_chat", "workflow_id": "invoices", "submission_id": "unauthorized", "message": "Edit"}})
	if !denied.IsError {
		t.Fatal("run-only client could invoke hidden Builder")
	}
}

func TestExternalBuilderOperationsRetryRepairsMissingQueueExactlyOnce(t *testing.T) {
	f := newBuilderOperationFixture(t)
	token, raw := f.issue(t, "owner")
	// Simulate a crash after committing the trusted operation reservation but
	// before writing the workspace queue. No model or queue worker ran.
	op := externalBuilderOperation{ID: uuid.NewString(), UserID: "owner", GrantID: token.ID, WorkflowID: "invoices", Workspace: "Workflow/invoices", SessionID: f.sessions["owner"], SubmissionID: "crash-gap", Message: "Repair this accepted edit", Status: "queued", CreatedAt: time.Now()}
	store, err := openExternalBuilderStore()
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`INSERT INTO external_builder_operations (id,user_id,grant_id,workflow_id,workspace,session_id,submission_id,message,status,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, op.ID, op.UserID, op.GrantID, op.WorkflowID, op.Workspace, op.SessionID, op.SubmissionID, op.Message, op.Status, op.CreatedAt.UnixNano())
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if rows := f.api.mustReadTurnQueueForTest(t, "owner"); len(rows) != 0 {
		t.Fatal("crash fixture unexpectedly queued")
	}
	for i := 0; i < 2; i++ {
		retried := f.submit(t, raw, op.SubmissionID, op.Message)
		if retried.ID != op.ID || retried.SessionID != op.SessionID {
			t.Fatalf("retry replaced reservation: %+v", retried)
		}
		rows := f.api.mustReadTurnQueueForTest(t, "owner")
		if len(rows) != 1 || rows[0].Principal.ExternalBuilderOperationID != op.ID || rows[0].Principal.AccessTokenID != token.ID || rows[0].Request.Query != op.Message {
			t.Fatalf("repair attempt %d did not retain exactly one bound turn: %+v", i, rows)
		}
	}
}

// builder_chat with wait_seconds returns the Builder's answer in the same call
// instead of making the client poll builder_status.
func TestExternalBuilderChatWaitReturnsTheAnswer(t *testing.T) {
	f := newBuilderOperationFixture(t)
	_, raw := f.issue(t, "owner")
	f.api.internalExternalBuilderTurn = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		return internalSessionTurnResult{FinalResponse: "Plan updated."}, nil
	}
	got := make(chan *httptest.ResponseRecorder, 1)
	body := `{"name":"sessions","arguments":{"action":"builder_chat","workflow_id":"invoices","submission_id":"wait","message":"Update the plan","wait_seconds":10}}`
	go func() {
		r := httptest.NewRequest("POST", "/api/external/v1/call", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		AuthMiddleware(http.HandlerFunc(f.api.handleExternalCall)).ServeHTTP(w, r)
		got <- w
	}()
	deadline := time.Now().Add(5 * time.Second)
	var turn queuedConversationTurn
	for {
		next, ok := f.api.claimNextConversationTurn(context.Background(), "owner", f.sessions["owner"])
		if ok {
			turn = next
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("builder_chat never queued its turn")
		}
		time.Sleep(50 * time.Millisecond)
	}
	request, err := queryRequestToMap(turn.Request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := f.api.queuedConversationTurnContext(turn, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.runQueuedExternalBuilder(ctx, turn, request); err != nil {
		t.Fatal(err)
	}
	w := <-got
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"completed"`) || !strings.Contains(w.Body.String(), "Plan updated.") {
		t.Fatalf("builder_chat did not return the answer: %s", w.Body)
	}
}

func TestExternalBuilderTabsShareNativeAndWebsiteControl(t *testing.T) {
	f := newBuilderOperationFixture(t)
	f.api.eventStore = storeevents.NewEventStore(1000)
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoices","created_by":"owner","access":{"owners":["owner","outsider"],"readers":["reader"]}}`)
	_, owner := f.issue(t, "owner")
	_, other := f.issue(t, "outsider")
	f.call(t, owner, "sessions", map[string]any{"action": "list"}, 200)
	w := f.call(t, owner, "sessions", map[string]any{"action": "open", "name": "Review"}, 200)
	var opened struct {
		Chat goalOwnerTab `json:"chat"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &opened); err != nil || opened.Chat.ID == "" {
		t.Fatal(w.Body.String(), err)
	}
	// PLAT-871-28: schedules return the tab suffix; MCP must also accept the
	// session_id field without bypassing the existing project/owner checks.
	for _, selector := range []map[string]any{
		{"chat_id": opened.Chat.ID},
		{"chat_id": strings.TrimPrefix(opened.Chat.ID, "invoices"+codeChatSideMarker)},
		{"session_id": opened.Chat.SessionID},
	} {
		selector["action"] = "read"
		f.call(t, other, "sessions", selector, 200)
	}
	f.call(t, other, "sessions", map[string]any{"action": "read", "session_id": "another-project-session"}, 404)
	f.call(t, other, "sessions", map[string]any{"action": "read", "chat_id": "another-project:chat:private"}, 404)
	f.call(t, other, "sessions", map[string]any{"action": "rename", "chat_id": opened.Chat.ID, "name": "Taken"}, 403)
	reg := &recordingRegistrar{}
	if err := f.api.registerGoalOwnerTabTools(reg, "outsider", "Workflow/invoices"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.tools["manage_builder_chats"].exec(context.Background(), map[string]any{"action": "read", "chat_id": opened.Chat.ID}); err != nil {
		t.Fatal(err)
	}
	// PR349: the native wrapper must forward the same advertised session selector.
	if _, err := reg.tools["manage_builder_chats"].exec(context.Background(), map[string]any{"action": "read", "session_id": opened.Chat.SessionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.tools["manage_builder_chats"].exec(context.Background(), map[string]any{"action": "read", "session_id": "another-project-session"}); err == nil {
		t.Fatal("native read admitted an unknown session")
	}
	if _, err := reg.tools["manage_builder_chats"].exec(context.Background(), map[string]any{"action": "close", "chat_id": opened.Chat.ID}); err == nil {
		t.Fatal("native Builder closed another owner's tab")
	}
	f.call(t, owner, "sessions", map[string]any{"action": "pin", "chat_id": opened.Chat.ID, "pinned": false}, 200)
	f.call(t, other, "sessions", map[string]any{"action": "order", "chat_ids": []string{opened.Chat.ID}}, 403)
	if err := f.api.registerGoalOwnerTabTools(reg, "owner", "Workflow/invoices"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.tools["manage_builder_chats"].exec(context.Background(), map[string]any{"action": "pin", "chat_id": opened.Chat.ID, "pinned": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.tools["manage_builder_chats"].exec(context.Background(), map[string]any{"action": "order", "chat_ids": []string{opened.Chat.ID}}); err != nil {
		t.Fatal(err)
	}
	f.call(t, owner, "sessions", map[string]any{"action": "rename", "chat_id": opened.Chat.ID, "name": "Review changes"}, 200)
}
