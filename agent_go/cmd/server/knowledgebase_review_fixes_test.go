package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

func TestKnowledgebaseMigrationCannotEscapeManagedSession(t *testing.T) {
	s, p, workspace, id := knowledgeIntegrationFixture(t)
	args := map[string]any{"action": "migration_preview", "workspace_path": workspace, "folder_id": id, "alias": "local", "access": "write", "request_id": "blocked-migration"}
	for _, session := range []string{"", "missing-policy", "readonly-run"} {
		ctx := context.WithValue(t.Context(), common.ChatSessionIDKey, session)
		if session == "readonly-run" {
			common.SetSessionWorkflowPath(session, workspace)
			common.GetSessionShellConfig(session).ReadOnlyAccess = true
		}
		if _, err := knowledgebaseDispatch(ctx, s, p, p.IdentityID, "update_knowledgebase", args); err == nil {
			t.Fatalf("migration admitted in session %q", session)
		}
	}
	ctx := context.WithValue(t.Context(), common.ChatSessionIDKey, "missing-policy")
	if _, err := knowledgebaseDispatch(ctx, s, p, p.IdentityID, "read_knowledgebase", map[string]any{"action": "read", "path": "Payments/Checkout/retries.md"}); err == nil {
		t.Fatal("missing session policy admitted ambient read")
	}
	for _, d := range knowledgebase.ConnectionToolDefinitions(true) {
		b, _ := json.Marshal(d.InputSchema)
		if strings.Contains(string(b), "migration_") {
			t.Fatal("migration leaked to ordinary agents")
		}
	}
}

func TestKnowledgebaseCutoverRechecksNewConsumers(t *testing.T) {
	s, p, workspace, id := knowledgeIntegrationFixture(t)
	r := knowledgeDispatchTest(t, s, p, "update_knowledgebase", map[string]any{"action": "migration_preview", "workspace_path": workspace, "folder_id": id, "alias": "local", "access": "read", "request_id": "consumer-preview"}).(*knowledgeMigrationReceipt)
	r = knowledgeDispatchTest(t, s, p, "update_knowledgebase", map[string]any{"action": "migration_import", "workspace_path": workspace, "migration_id": r.ID, "request_id": "consumer-import"}).(*knowledgeMigrationReceipt)
	root := os.Getenv("WORKSPACE_DOCS_PATH")
	path := filepath.Join(root, "Workflow/consumer")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"id":"consumer-id","created_by":"admin","knowledgebase_sources":[{"workflow_id":"payments-workflow","alias":"payments"}]}`)
	if err := os.WriteFile(filepath.Join(path, "workflow.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	project, err := knowledgeProjectLoad(t.Context(), "admin", workspace, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgeMigrationCutover(t.Context(), s, p, project, r); err == nil || !strings.Contains(err.Error(), "rebind legacy consumers") {
		t.Fatal("consumer silently disabled", err)
	}
}

func TestKnowledgebaseAccessRequiresFrozenInteractiveConfirmation(t *testing.T) {
	api, s := knowledgebaseServerTest(t)
	admin := &UserClaims{UserID: "admin", Username: "admin"}
	ctx := context.WithValue(t.Context(), UserContextKey, admin)
	acl, err := s.Call(ctx, knowledgebase.Principal{IdentityID: "admin", IsAdmin: true}, "get_knowledgebase_access", map[string]any{"folder_path": "Payments/Checkout"})
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"action": "grant", "folder_path": "Payments/Checkout", "identity_id": "outsider", "role": "Reader", "expected_acl_version": knowledgeMap(acl)["acl_version"], "request_id": "approval-grant"}
	result, err := knowledgebaseAccessExecutor(ctx, agentprofiles.ToolRuntimeContext{UserID: "admin", Product: "knowledgebase"}, args)
	if err != nil {
		t.Fatal(err)
	}
	var pending struct {
		ID string `json:"proposal_id"`
	}
	if err := json.Unmarshal([]byte(result), &pending); err != nil {
		t.Fatal(err)
	}
	outsider := knowledgebase.Principal{IdentityID: "outsider"}
	if _, err := s.Call(ctx, outsider, "read_knowledgebase", map[string]any{"path": "Payments/Checkout/retries.md"}); err == nil {
		t.Fatal("grant executed before human confirmation")
	}
	approve := func(claims *UserClaims, id string, approve bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"id": id, "approve": approve})
		req := httptest.NewRequest(http.MethodPost, "/api/knowledgebase/access-proposals", strings.NewReader(string(body)))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		api.handleKnowledgebaseAccessProposals(w, req)
		return w
	}
	if w := approve(&UserClaims{UserID: "outsider", Username: "outsider"}, pending.ID, true); w.Code != 404 {
		t.Fatal("other user confirmed proposal", w.Code, w.Body.String())
	}
	if w := approve(&UserClaims{UserID: "admin", ExecutionPrincipal: &ExecutionPrincipal{Kind: "scheduled"}}, pending.ID, true); w.Code != 403 {
		t.Fatal("unattended caller approved proposal", w.Code)
	}
	if w := approve(admin, pending.ID, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := s.Call(ctx, outsider, "read_knowledgebase", map[string]any{"path": "Payments/Checkout/retries.md"}); err != nil {
		t.Fatal("approved grant not live", err)
	}
	if w := approve(admin, pending.ID, true); w.Code != 404 {
		t.Fatal("consumed proposal replayed", w.Code)
	}
}

func TestKnowledgebaseToolsRequireBindingAndOAuthIsExplicit(t *testing.T) {
	s, p, workspace, id := knowledgeIntegrationFixture(t)
	if tools, _, _ := createKnowledgebaseTools("admin", "no-config"); len(tools) != 0 {
		t.Fatal("ambient tools registered")
	}
	project, err := knowledgeProjectLoad(t.Context(), "admin", workspace, true)
	if err != nil {
		t.Fatal(err)
	}
	p.AccessOnly = true
	knowledgeDispatchTest(t, s, p, "manage_knowledgebase_access", map[string]any{"action": "bind_project", "workspace_path": workspace, "folder_id": id, "alias": "local", "access": "read", "expected_manifest_version": project.Version, "request_id": "review-binding"})
	tools, _, _ := createKnowledgebaseTools("admin", "initial-registration", workspace)
	if len(tools) != 4 {
		t.Fatalf("bound project has %d tools", len(tools))
	}
	scopes, ok := validMCPOAuthScopes("knowledgebase:read knowledgebase:write")
	if !ok || len(scopes) != 2 {
		t.Fatal(scopes, ok)
	}
	if _, ok := validMCPOAuthScopes("knowledgebase:write"); ok {
		t.Fatal("write scope without read accepted")
	}
	if slices.Contains(mcpOAuthDefaultScopes, "knowledgebase:write") {
		t.Fatal("ambient OAuth write grant")
	}
	config, err := knowledgebaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cliSeatbeltProtectedRoots(), config.Root) {
		t.Fatal("custom root unprotected")
	}
}

func TestKnowledgebaseExternalMigrationRequiresSourceBuilderForPreview(t *testing.T) {
	_, _, workspace, id := knowledgeIntegrationFixture(t)
	t.Setenv("AUTH_SECRET", "knowledgebase-migration-test-signing-secret")
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, builder := range []bool{false, true} {
		scopes := []string{"knowledgebase:read", "knowledgebase:write", "workflows:read", "files:read", "runs:execute"}
		if builder {
			scopes = append(scopes, "builder:chat")
		}
		token, _, err := store.Issue(t.Context(), accesstokens.Token{UserID: "admin", Username: "admin", Name: "Migration", Scopes: scopes, AllWorkflows: true, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		claims := &UserClaims{UserID: "admin", Username: "admin", AccessToken: &token}
		body, _ := json.Marshal(map[string]any{"name": "update_knowledgebase", "arguments": map[string]any{"action": "migration_preview", "workspace_path": workspace, "folder_id": id, "alias": "local", "access": "read", "request_id": fmt.Sprintf("external-preview-%t", builder)}})
		req := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", strings.NewReader(string(body))).WithContext(context.WithValue(t.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		(&StreamingAPI{}).handleExternalCall(w, req)
		want := http.StatusForbidden
		if builder {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Fatalf("builder=%t: %d %s", builder, w.Code, w.Body.String())
		}
	}
}

func TestKnowledgebaseEmptyDirectoryDoesNotDisableLastSnapshot(t *testing.T) {
	_, s := knowledgebaseServerTest(t)
	withMemoryUserDirectory(t, `{"users":[]}`)
	if err := knowledgebaseSyncIdentities(t.Context(), s); err == nil {
		t.Fatal("empty authority snapshot accepted")
	}
	if _, err := s.Call(t.Context(), knowledgebase.Principal{IdentityID: "priya"}, "read_knowledgebase", map[string]any{"path": "Payments/Checkout/retries.md"}); err != nil {
		t.Fatal("last known identity snapshot erased", err)
	}
}
