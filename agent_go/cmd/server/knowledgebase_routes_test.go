package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/knowledgebaseproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

func knowledgebaseServerTest(t *testing.T) (*StreamingAPI, *knowledgebase.Service) {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_KNOWLEDGEBASE_ROOT", filepath.Join(t.TempDir(), "knowledgebase"))
	t.Setenv("AGENTWORKS_KNOWLEDGEBASE_BACKUP_REMOTE", "")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","role":"admin","products":[]},{"id":"priya","username":"priya","role":"viewer","products":["knowledgebase"]},{"id":"outsider","username":"outsider","role":"viewer","products":["knowledgebase"]}]}`)
	service, err := knowledgebaseService()
	if err != nil {
		t.Fatal(err)
	}
	if err = knowledgebaseSyncIdentities(t.Context(), service); err != nil {
		t.Fatal(err)
	}
	admin := knowledgebase.Principal{IdentityID: "admin", IsAdmin: true}
	call := func(tool string, args map[string]any) any {
		t.Helper()
		v, err := service.Call(t.Context(), admin, tool, args)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return v
	}
	call("create_knowledgebase_folder", map[string]any{"folder_path": "", "name": "Payments", "request_id": "payments"})
	call("create_knowledgebase_folder", map[string]any{"folder_path": "Payments", "name": "Checkout", "request_id": "checkout"})
	call("create_knowledgebase_folder", map[string]any{"folder_path": "Payments", "name": "Billing", "request_id": "billing"})
	access := call("get_knowledgebase_access", map[string]any{"folder_path": "Payments/Checkout"}).(map[string]any)
	call("manage_knowledgebase_access", map[string]any{"action": "grant", "folder_path": "Payments/Checkout", "identity_id": "priya", "role": "reader", "request_id": "grant-priya", "expected_acl_version": access["acl_version"]})
	call("create_knowledgebase", map[string]any{"folder_path": "Payments/Checkout", "filename": "retries.md", "type": "skill", "title": "Retries", "content": "# Retries\nRetry failed payments safely.\n", "request_id": "entry-checkout"})
	call("create_knowledgebase", map[string]any{"folder_path": "Payments/Billing", "filename": "private.md", "type": "note", "title": "Billing private", "content": "Protected billing secret", "request_id": "entry-billing"})
	return &StreamingAPI{}, service
}

func TestKnowledgebaseReconcileRouteRequiresInteractiveAdministrator(t *testing.T) {
	api, _ := knowledgebaseServerTest(t)
	remote := filepath.Join(t.TempDir(), "backup.git")
	if output, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("remote: %v %s", err, output)
	}
	t.Setenv("AGENTWORKS_KNOWLEDGEBASE_BACKUP_REMOTE", remote)
	for _, tc := range []struct {
		name   string
		claims *UserClaims
		want   int
	}{
		{"anonymous", nil, http.StatusForbidden},
		{"reader", &UserClaims{UserID: "priya", Username: "priya"}, http.StatusForbidden},
		{"admin-token", &UserClaims{UserID: "admin", Username: "admin", AccessToken: &accesstokens.Token{Scopes: []string{"knowledgebase:read", "knowledgebase:write"}}}, http.StatusForbidden},
		{"admin-session", &UserClaims{UserID: "admin", Username: "admin"}, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/knowledgebase/maintenance/reconcile-backup", nil)
			if tc.claims != nil {
				r = r.WithContext(context.WithValue(r.Context(), UserContextKey, tc.claims))
			}
			w := httptest.NewRecorder()
			api.handleKnowledgebaseReconcileBackup(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d %s, want %d", w.Code, w.Body, tc.want)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("maintenance response cacheable")
			}
		})
	}
	service, err := knowledgebaseService()
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.Call(t.Context(), knowledgebase.Principal{IdentityID: "priya"}, "read_knowledgebase", map[string]any{"path": "Payments/Checkout/retries.md"})
	if err != nil || !strings.Contains(fmt.Sprint(value), "Retry failed payments safely") {
		t.Fatal("reconciliation changed live content or grants", value, err)
	}
	if output, err := exec.Command("git", "--git-dir="+remote, "for-each-ref", "--format=%(refname)", "refs/heads").CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "" {
		t.Fatalf("reconciliation published: %s %v", output, err)
	}
}

func TestKnowledgebaseViewerAndExternalCallSharePermissions(t *testing.T) {
	api, _ := knowledgebaseServerTest(t)
	request := func(path, body, user string, external bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, strings.NewReader(body))
		if external {
			r.Method = http.MethodPost
		}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		w := httptest.NewRecorder()
		if external {
			api.handleExternalCall(w, r)
		} else {
			api.handleKnowledgebaseViewer(w, r)
		}
		return w
	}
	read := request("/api/knowledgebase/read?path=Payments/Checkout/retries.md", "", "priya", false)
	if read.Code != 200 || !strings.Contains(read.Body.String(), "Retry failed payments safely") {
		t.Fatal(read.Code, read.Body.String())
	}
	missing := request("/api/knowledgebase/read?path=Payments/Billing/missing.md", "", "priya", false)
	protected := request("/api/knowledgebase/read?path=Payments/Billing/private.md", "", "priya", false)
	if missing.Code != 404 || protected.Code != 404 || missing.Body.String() != protected.Body.String() {
		t.Fatalf("existence leaked: %d %s / %d %s", missing.Code, missing.Body, protected.Code, protected.Body)
	}
	search := request("/api/knowledgebase/search?query=secret", "", "priya", false)
	if search.Code != 200 || strings.Contains(search.Body.String(), "Billing") || strings.Contains(search.Body.String(), "Protected billing") {
		t.Fatal(search.Code, search.Body.String())
	}
	external := request("/api/external/v1/call", `{"name":"brain_read","arguments":{"action":"read","path":"Payments/Checkout/retries.md"}}`, "priya", true)
	if external.Code != 200 || !strings.Contains(external.Body.String(), "Retry failed payments safely") {
		t.Fatal(external.Code, external.Body.String())
	}
	var readBody struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &readBody); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"name": "brain_update", "arguments": map[string]any{"action": "update", "path": "Payments/Checkout/retries.md", "content": "Bad overwrite", "expected_version": readBody.Version, "request_id": "denied-write"}})
	denied := request("/api/external/v1/call", string(args), "priya", true)
	if denied.Code != 403 {
		t.Fatal(denied.Code, denied.Body.String())
	}
}

func TestKnowledgebaseBuilderOnlyAdmitsAccessAndGitTools(t *testing.T) {
	api, service := knowledgebaseServerTest(t)
	p := knowledgebaseproduct.BuiltinAgentProfile()
	gate := newProductToolGate(&resolvedAgentProfile{Definition: p})
	for _, name := range []string{"execute_shell_command", "diff_patch_workspace_file", "brain_browse", "brain_read", "brain_update", "query_database", "agent_browser"} {
		if gate.Admit(name) {
			t.Fatalf("builder admitted %s", name)
		}
	}
	if !gate.Admit("brain_access") || !gate.Admit("brain_backup") {
		t.Fatal("builder cannot manage access")
	}
	_, err := service.Call(t.Context(), knowledgebase.Principal{IdentityID: "admin", IsAdmin: true, AccessOnly: true}, "create_knowledgebase", map[string]any{"folder_path": "", "filename": "blocked.md", "type": "note", "title": "Blocked", "content": "blocked", "request_id": "blocked"})
	if err == nil {
		t.Fatal("access purpose allowed content write")
	}
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "admin", Username: "admin"})
	if _, err = knowledgebaseAccessExecutor(ctx, agentprofiles.ToolRuntimeContext{UserID: "admin", Product: "knowledgebase"}, map[string]any{"action": "list", "folder_path": "", "request_id": "inspect"}); err != nil {
		t.Fatal(err)
	}
	_ = api
}

func TestKnowledgebaseConfigRejectsWorkspaceDataRoots(t *testing.T) {
	docs := filepath.Join(t.TempDir(), "docs")
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	for _, root := range []string{docs, filepath.Join(docs, "knowledgebase"), filepath.Dir(docs)} {
		t.Setenv("AGENTWORKS_KNOWLEDGEBASE_ROOT", root)
		if _, err := knowledgebaseConfig(); err == nil {
			t.Fatalf("accepted overlapping root %s", root)
		}
	}
}

func TestKnowledgebaseContentRuntimeUsesExecutionIdentityAndLiveGrants(t *testing.T) {
	_, service := knowledgebaseServerTest(t)
	// Unattended calls carry claims bound from the authenticated run identity,
	// with exactly the same folder checks as interactive MCP calls.
	args := map[string]any{"action": "read", "path": "Payments/Checkout/retries.md"}
	if result, err := knowledgebaseExecute(knowledgeTestCaller(t.Context(), "priya"), "priya", false, "brain_read", args); err != nil || !strings.Contains(result, "Retry failed payments safely") {
		t.Fatal("run identity could not read granted folder", result, err)
	}
	if _, err := knowledgebaseExecute(knowledgeTestCaller(t.Context(), "outsider"), "outsider", false, "brain_read", args); err == nil {
		t.Fatal("ungranted run identity read shared knowledge")
	}
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "admin", Username: "admin"})
	if _, err := knowledgebaseExecute(ctx, "priya", false, "brain_read", args); err == nil {
		t.Fatal("run identity borrowed another caller's authority")
	}
	admin := knowledgebase.Principal{IdentityID: "admin", IsAdmin: true}
	access, err := service.Call(t.Context(), admin, "get_knowledgebase_access", map[string]any{"folder_path": "Payments/Checkout"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Call(t.Context(), admin, "manage_knowledgebase_access", map[string]any{"action": "revoke", "folder_path": "Payments/Checkout", "identity_id": "priya", "request_id": "revoke-runtime", "expected_acl_version": access.(map[string]any)["acl_version"]}); err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgebaseExecute(knowledgeTestCaller(t.Context(), "priya"), "priya", false, "brain_read", args); err == nil {
		t.Fatal("running agent retained revoked folder access")
	}
}

// Production binds claims at tool ingress; fixtures must do the same.
func knowledgeTestCaller(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, UserContextKey, principalClaims(userID))
}

// Pins the owner decision: everyone may use Brain through the MCP, but only accounts holding the product (and
// administrators) get it in the app. A disabled account gets neither.
func TestBrainMCPIsOpenToEveryoneButAppNeedsTheProduct(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","role":"admin","products":[]},{"id":"matt","username":"matt","role":"viewer","products":["agentworks"]},{"id":"gone","username":"gone","role":"viewer","products":["agentworks"],"disabled":true}]}`)
	t.Setenv("AGENT_PRODUCTS", "")
	session := &UserClaims{UserID: "matt", Username: "matt"}
	connection := &UserClaims{UserID: "matt", Username: "matt", AccessToken: &accesstokens.Token{Scopes: []string{"knowledgebase:read"}}}
	if knowledgebaseProductAllowed(session) {
		t.Fatal("an account without the product must not get Brain in the app")
	}
	if !knowledgebaseMCPAllowed(session) || !knowledgebaseProductAllowed(connection) {
		t.Fatal("every active account may connect through the MCP")
	}
	if !knowledgebaseProductAllowed(&UserClaims{UserID: "admin", Username: "admin"}) {
		t.Fatal("administrators have the product")
	}
	if knowledgebaseMCPAllowed(&UserClaims{UserID: "gone", Username: "gone"}) {
		t.Fatal("a disabled account has no access")
	}
}
