package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
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
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoices","created_by":"owner","access":{"owners":["owner"],"editors":["outsider"],"readers":["reader"]}}`)
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
	w := f.call(t, raw, "builder_chat", map[string]any{"submission_id": key, "message": message}, 200)
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
	f.call(t, owner, "builder_chat", map[string]any{"submission_id": "first", "message": "Different edit"}, 409)
	second := f.submit(t, editor, "editor", "Adjust validation")
	if second.SessionID != f.sessions["outsider"] || second.SessionID == first.SessionID {
		t.Fatalf("editor did not use own chat: %+v", second)
	}
	f.call(t, editor, "builder_chat", map[string]any{"submission_id": "foreign", "message": "Read owner's chat", "session_id": first.SessionID}, 404)
	_, replacement := f.issue(t, "owner")
	f.call(t, replacement, "builder_status", map[string]any{"operation_id": first.ID}, 404)
	f.call(t, owner, "builder_status", map[string]any{"operation_id": first.ID}, 401)
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
	f.call(t, raw, "builder_cancel", map[string]any{"operation_id": op.ID}, 200)
	f.call(t, raw, "builder_cancel", map[string]any{"operation_id": op.ID}, 200)
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
	w := f.call(t, raw, "builder_status", map[string]any{"operation_id": op.ID}, 200)
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
	w := f.call(t, raw, "builder_status", map[string]any{"operation_id": op.ID}, 200)
	if !strings.Contains(w.Body.String(), ownID) || strings.Contains(w.Body.String(), previousID) || strings.Contains(w.Body.String(), foreignID) || strings.Contains(w.Body.String(), concurrentBrowserID) {
		t.Fatalf("question scope: %s", w.Body)
	}
	f.call(t, raw, "builder_reply_input", map[string]any{"operation_id": op.ID, "request_id": previousID, "response": "yes"}, 409)
	f.call(t, raw, "builder_reply_input", map[string]any{"operation_id": op.ID, "request_id": foreignID, "response": "yes"}, 409)
	f.call(t, raw, "builder_reply_input", map[string]any{"operation_id": op.ID, "request_id": ownID, "response": "invalid"}, 409)
	f.call(t, raw, "builder_reply_input", map[string]any{"operation_id": op.ID, "request_id": ownID, "response": "Yes"}, 200)
	f.call(t, raw, "builder_cancel", map[string]any{"operation_id": op.ID}, 200)
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
	f.call(t, raw, "builder_reply_input", map[string]any{"operation_id": op.ID, "request_id": ownID, "response": "No"}, 409)
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
	w := f.call(t, raw, "builder_status", map[string]any{"operation_id": op.ID}, 200)
	if !strings.Contains(w.Body.String(), `"status":"completed"`) || !strings.Contains(w.Body.String(), `"answer":"Updated the validation plan."`) {
		t.Fatalf("exact result missing: %s", w.Body)
	}
	f.call(t, raw, "builder_cancel", map[string]any{"operation_id": op.ID}, 200)
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
	for _, name := range []string{"builder_chat", "builder_status", "builder_reply_input", "builder_cancel", "builder_file_history", "builder_restore_file"} {
		if !strings.Contains(marshalStructured(t, spec), name) {
			t.Fatalf("enabled scoped catalog missing %s", name)
		}
	}
	history := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "builder_file_history", "arguments": map[string]any{"workflow_id": "invoices", "path": "code/task.py"}})
	requireRemoteSuccess(t, history, "Builder file history")
	if !strings.Contains(marshalStructured(t, history), `"edits":[]`) {
		t.Fatalf("unexpected file history: %s", marshalStructured(t, history))
	}
	submitted := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "builder_chat", "arguments": map[string]any{"workflow_id": "invoices", "submission_id": "transport", "message": "Update validation"}})
	requireRemoteSuccess(t, submitted, "Builder submit")
	var op externalBuilderOperation
	if err := json.Unmarshal([]byte(marshalStructured(t, submitted)), &op); err != nil || op.ID == "" || op.SessionID != f.sessions["owner"] {
		t.Fatalf("transport submit: %+v %v", op, err)
	}
	status := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "builder_status", "arguments": map[string]any{"workflow_id": "invoices", "operation_id": op.ID}})
	requireRemoteSuccess(t, status, "Builder status")
	if !strings.Contains(marshalStructured(t, status), `"status":"queued"`) {
		t.Fatalf("unexpected status: %s", marshalStructured(t, status))
	}
	canceled := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "builder_cancel", "arguments": map[string]any{"workflow_id": "invoices", "operation_id": op.ID}})
	requireRemoteSuccess(t, canceled, "Builder cancel must not become an MCP HTTP-status error")
	if !strings.Contains(marshalStructured(t, canceled), `"status":"canceled"`) {
		t.Fatalf("missing cancel acknowledgement: %s", marshalStructured(t, canceled))
	}
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "false")
	hidden := callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{})
	requireRemoteSuccess(t, hidden, "disabled catalog")
	if strings.Contains(marshalStructured(t, hidden), "builder_chat") {
		t.Fatal("disabled Builder remained discoverable")
	}
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "true")
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	readToken, _, err := store.Issue(ctx, accesstokens.Token{Name: "Run only", UserID: "outsider", Username: "outsider", Scopes: []string{"workflows:read", "files:read", "runs:execute"}, WorkflowIDs: []string{"invoices"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	readClaims, err := accessTokenClaims(readToken)
	if err != nil {
		t.Fatal(err)
	}
	readServer := serveExternalMCP(t, f.api, readClaims)
	readClient := dialExternalMCP(t, ctx, readServer.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, readClient)
	readSpec := callRemoteTool(t, ctx, readClient, externalMCPToolSpec, map[string]any{})
	requireRemoteSuccess(t, readSpec, "read/run catalog")
	if strings.Contains(marshalStructured(t, readSpec), "builder_chat") {
		t.Fatal("run-only token discovered Builder")
	}
	denied := callRemoteTool(t, ctx, readClient, externalMCPToolCall, map[string]any{"name": "builder_chat", "arguments": map[string]any{"workflow_id": "invoices", "submission_id": "unauthorized", "message": "Edit"}})
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
