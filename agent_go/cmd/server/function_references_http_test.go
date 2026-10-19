package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func functionReferenceRequest(svc *ProductScheduleService, claims *UserClaims, target, query string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/function-references?"+url.Values{"target": {target}, "q": {query}}.Encode(), nil)
	if claims != nil {
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
	}
	w := httptest.NewRecorder()
	muxRouterForCrewFunctions(svc).ServeHTTP(w, r)
	return w
}

func TestFunctionReferencesRequireAppIdentity(t *testing.T) {
	for _, claims := range []*UserClaims{nil, {}, {UserID: " "}, {UserID: "owner", Scope: "crew"}, {UserID: "owner", AccessToken: &accesstokens.Token{}}} {
		w := functionReferenceRequest(&ProductScheduleService{}, claims, "crew:beta", "")
		if w.Code != http.StatusForbidden || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("non-app identity admitted: %d", w.Code)
		}
	}
}

func TestFunctionReferencesListSearchableMetadataWithoutManagementData(t *testing.T) {
	env := newCrewFunctionEnv(t)
	if _, err := env.alpha["define_function"].exec(context.Background(), loginFlowArgs); err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: "owner"}
	w := functionReferenceRequest(env.svc, claims, "crew:beta", "BUILD")
	if w.Code != http.StatusOK {
		t.Fatalf("owner metadata unavailable: %d %s", w.Code, w.Body)
	}
	var out struct {
		Functions []map[string]interface{} `json:"functions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Functions) != 1 || out.Functions[0]["name"] != "run_login_flow" {
		t.Fatalf("metadata search result: %s %v", w.Body, err)
	}
	for _, private := range []string{"instructions", "created_by", "calls", "Run tests/login.py"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("management-only data leaked: %s", private)
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("metadata discovery allowed a stale permission cache")
	}
	if w := functionReferenceRequest(env.svc, claims, "crew:beta", "no matching function"); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"functions":[]}` {
		t.Fatalf("nonmatching functions returned: %d %s", w.Code, w.Body)
	}
}

func TestFunctionReferencesPreserveWorkflowAccessAndRevocation(t *testing.T) {
	env := newCrewFunctionEnv(t)
	path := "Workflow/reports"
	env.mock.mu.Lock()
	var manifest WorkflowManifest
	if err := json.Unmarshal([]byte(env.mock.files[manifestPath(path)]), &manifest); err != nil {
		env.mock.mu.Unlock()
		t.Fatal(err)
	}
	manifest.Schedules = []WorkflowSchedule{reviewPRTrigger()}
	raw, _ := json.Marshal(manifest)
	env.mock.files[manifestPath(path)] = string(raw)
	env.mock.mu.Unlock()
	noteWorkspaceMutation(manifestPath(path), false)
	owner := &UserClaims{UserID: "owner"}
	if w := functionReferenceRequest(env.svc, owner, "workflow:Reports", "pull request"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"review_pr"`) {
		t.Fatalf("workflow function not listed: %d %s", w.Code, w.Body)
	}
	// A View-only workflow grant is not permission to call its functions.
	reader := functionReferenceRequest(env.svc, owner, "workflow:Shared", "")
	foreign := functionReferenceRequest(env.svc, &UserClaims{UserID: "other"}, "workflow:Reports", "")
	missing := functionReferenceRequest(env.svc, owner, "workflow:not-present", "")
	for _, denied := range []*httptest.ResponseRecorder{reader, foreign, missing} {
		if denied.Code != http.StatusNotFound || denied.Body.String() != missing.Body.String() {
			t.Fatalf("missing/denied metadata disclosed target authority: %d %s", denied.Code, denied.Body)
		}
	}
	// Revoke the previously valid owner. A second listing cannot reuse it.
	env.mock.mu.Lock()
	manifest.Access = &WorkflowAccess{Owners: []string{"other"}}
	raw, _ = json.Marshal(manifest)
	env.mock.files[manifestPath(path)] = string(raw)
	env.mock.mu.Unlock()
	noteWorkspaceMutation(manifestPath(path), false)
	if w := functionReferenceRequest(env.svc, owner, "workflow:Reports", ""); w.Code != http.StatusNotFound || w.Body.String() != missing.Body.String() {
		t.Fatalf("revoked function metadata remained available: %d %s", w.Code, w.Body)
	}
}

func TestFunctionReferencesRespectSharedCodeOriginAudience(t *testing.T) {
	env := newCrewFunctionEnv(t)
	if err := env.svc.registry.RegisterProfile(codeproduct.BuiltinAgentProfile()); err != nil {
		t.Fatal(err)
	}
	const root = "_users/owner/Chats/Code/projects/target"
	env.mock.mu.Lock()
	env.mock.files[root+"/product.json"] = `{"schema_version":1,"product":"code","id":"target","title":"Target","session_id":"code-target"}`
	env.mock.files[root+"/functions.json"] = `{"version":1,"functions":[{"name":"export_report","description":"Export a report","instructions":"private implementation"}]}`
	env.mock.mu.Unlock()
	registry := withProjectOwnerRegistry(t)
	withProjectSharing(t, true)
	if err := registry.Register(projectOwnerRecord{Product: "code", Folder: "target", OwnerID: "owner", ProjectID: "target"}); err != nil {
		t.Fatal(err)
	}
	owner, other := &UserClaims{UserID: "owner"}, &UserClaims{UserID: "other"}
	const target = "workspace:code:target"
	if w := functionReferenceRequest(env.svc, owner, target, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"export_report"`) {
		t.Fatalf("owner Code-origin metadata: %d %s", w.Code, w.Body)
	}
	if w := functionReferenceRequest(env.svc, other, target, ""); w.Code != http.StatusNotFound {
		t.Fatalf("private metadata exposed: %d", w.Code)
	}
	if err := registry.SetAudience("code", "target", true, []string{"other"}); err != nil {
		t.Fatal(err)
	}
	if w := functionReferenceRequest(env.svc, other, target, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"export_report"`) || strings.Contains(w.Body.String(), "private implementation") {
		t.Fatalf("shared metadata boundary: %d %s", w.Code, w.Body)
	}
	if err := registry.SetAudience("code", "target", true, nil); err != nil {
		t.Fatal(err)
	}
	if w := functionReferenceRequest(env.svc, other, target, ""); w.Code != http.StatusNotFound {
		t.Fatalf("revoked caller kept metadata: %d", w.Code)
	}
}
