package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/knowledgebaseproduct"
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
	external := request("/api/external/v1/call", `{"name":"read_knowledgebase","arguments":{"action":"read","path":"Payments/Checkout/retries.md"}}`, "priya", true)
	if external.Code != 200 || !strings.Contains(external.Body.String(), "Retry failed payments safely") {
		t.Fatal(external.Code, external.Body.String())
	}
	var readBody struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &readBody); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"name": "update_knowledgebase", "arguments": map[string]any{"action": "update", "path": "Payments/Checkout/retries.md", "content": "Bad overwrite", "expected_version": readBody.Version, "request_id": "denied-write"}})
	denied := request("/api/external/v1/call", string(args), "priya", true)
	if denied.Code != 403 {
		t.Fatal(denied.Code, denied.Body.String())
	}
}

func TestKnowledgebaseBuilderOnlyAdmitsAccessTool(t *testing.T) {
	api, service := knowledgebaseServerTest(t)
	p := knowledgebaseproduct.BuiltinAgentProfile()
	gate := newProductToolGate(&resolvedAgentProfile{Definition: p})
	for _, name := range []string{"execute_shell_command", "diff_patch_workspace_file", "browse_knowledgebase", "read_knowledgebase", "update_knowledgebase", "backup_knowledgebase", "query_database", "agent_browser"} {
		if gate.Admit(name) {
			t.Fatalf("builder admitted %s", name)
		}
	}
	if !gate.Admit("manage_knowledgebase_access") {
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
