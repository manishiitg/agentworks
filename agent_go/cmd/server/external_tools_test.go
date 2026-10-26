package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	jwt "github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

type externalToolsFixture struct {
	api                   *StreamingAPI
	docs                  string
	upstreamCalls         atomic.Int64
	questionRegistryReads atomic.Int64
}

func newExternalToolsFixture(t *testing.T) *externalToolsFixture {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	const workspaceToken = "external-tools-fixture-server-only-token"
	t.Setenv("WORKSPACE_API_TOKEN", workspaceToken)
	withMemoryUserDirectory(t, `{"users":[
 {"id":"owner","username":"owner","can_create":true,"products":["agentworks"]},
 {"id":"reader","username":"reader","can_create":false,"products":["agentworks"]},
 {"id":"readonly-owner","username":"readonly-owner","can_create":false,"products":["agentworks"]},
 {"id":"outsider","username":"outsider","can_create":true,"products":["agentworks"]}
 ]}`)
	f := &externalToolsFixture{api: &StreamingAPI{}, docs: t.TempDir()}
	t.Setenv("WORKSPACE_DOCS_PATH", f.docs)
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", f.docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoice processing","created_by":"owner","access":{"owners":["owner","readonly-owner"],"readers":["reader"]},"capabilities":{"selected_servers":["accounting"]}}`)
	f.write(t, "Workflow/secret/workflow.json", `{"id":"secret","label":"Private research","created_by":"outsider","access":{"owners":["outsider"]}}`)
	f.write(t, "Workflow/invoices/planning/plan.json", `{"steps":[{"id":"fetch-invoices","type":"scripted","title":"Fetch invoices","description":"Fetch invoices from accounting."}]}`)
	f.write(t, "Workflow/invoices/planning/step_config.json", `{"steps":[]}`)
	f.write(t, "Workflow/invoices/docs/process.md", "Invoices are reviewed weekly.\n")
	router := gin.New()
	router.POST("/api/dashboards", workspacehandlers.DashboardOperation)
	router.POST("/api/shared-file-write", func(c *gin.Context) {
		if c.GetHeader("X-Workspace-Token") != workspaceToken {
			c.AbortWithStatus(401)
			return
		}
		workspacehandlers.SharedFileWrite(c)
	})
	router.POST("/api/shared-assets", func(c *gin.Context) {
		if c.GetHeader("X-Workspace-Token") != workspaceToken {
			c.AbortWithStatus(401)
			return
		}
		workspacehandlers.SharedAssets(c)
	})
	router.GET("/api/documents", func(c *gin.Context) {
		c.JSON(200, gin.H{"success": true, "data": []any{gin.H{"filepath": "Workflow", "type": "folder", "children": []any{gin.H{"filepath": "Workflow/invoices", "type": "folder"}, gin.H{"filepath": "Workflow/secret", "type": "folder"}}}}})
	})
	router.GET("/api/documents/*file", func(c *gin.Context) {
		rel := strings.TrimPrefix(c.Param("file"), "/")
		if strings.HasPrefix(rel, "_system/dashboard_questions/") {
			if rel == dashboardQuestionRegistryPath {
				f.questionRegistryReads.Add(1)
			}
			c.Params = append(c.Params, gin.Param{Key: "filepath", Value: "/" + rel})
			workspacehandlers.GetDocument(c)
			return
		}
		if (strings.HasPrefix(rel, "Workflow/invoices/") || strings.HasPrefix(rel, "Crew/dashboard-team/") || strings.HasPrefix(rel, "_users/owner/Chats/Code/projects/dashboard-code/")) && strings.HasSuffix(rel, "/raw") {
			if c.Query("report_media") == "true" {
				c.Params = append(c.Params, gin.Param{Key: "filepath", Value: "/" + strings.TrimSuffix(rel, "/raw")})
				workspacehandlers.GetRawDocument(c)
				return
			}
			data, err := os.ReadFile(filepath.Join(f.docs, strings.TrimSuffix(rel, "/raw")))
			if err != nil {
				c.Status(404)
				return
			}
			c.Data(200, "text/html", data)
			return
		}

		if strings.HasPrefix(rel, "_users/") && strings.HasSuffix(rel, "/chat_history/product-conversations.json") {
			c.Params = append(c.Params, gin.Param{Key: "filepath", Value: "/" + rel})
			workspacehandlers.GetDocument(c)
			return
		}
		if strings.HasSuffix(rel, "/preferences/workspace-state.json") || strings.HasSuffix(rel, "/preferences/chat-display.json") || strings.Contains(rel, "/preferences/context-shortcuts/") {
			c.Params = append(c.Params, gin.Param{Key: "filepath", Value: "/" + rel})
			workspacehandlers.GetDocument(c)
			return
		}
		if rel != "Crew/dashboard-team/product.json" && rel != "Crew/dashboard-team/workflow.json" && rel != "_users/owner/Chats/Code/projects/dashboard-code/product.json" && rel != "_users/owner/Chats/Code/projects/dashboard-code/workflow.json" && rel != "Workflow/invoices/workflow.json" && rel != "Workflow/secret/workflow.json" && rel != "Workflow/invoices/schedule-runs.json" && rel != userProductAccessFilePath() {
			c.Status(404)
			return
		}
		data, err := os.ReadFile(filepath.Join(f.docs, filepath.FromSlash(rel)))
		if err != nil {
			c.Status(404)
			return
		}
		c.JSON(200, gin.H{"success": true, "data": gin.H{"content": string(data)}})
	})
	// Settings saves rewrite this one manifest through the workspace API.
	router.PUT("/api/documents/*file", func(c *gin.Context) {
		rel := strings.TrimPrefix(c.Param("file"), "/")
		if strings.HasPrefix(rel, "_system/dashboard_questions/") {
			c.Params = append(c.Params, gin.Param{Key: "filepath", Value: "/" + rel})
			workspacehandlers.UpdateDocument(c)
			return
		}
		if strings.HasPrefix(rel, "_users/") && strings.HasSuffix(rel, "/chat_history/product-conversations.json") {
			c.Params = append(c.Params, gin.Param{Key: "filepath", Value: "/" + rel})
			workspacehandlers.UpdateDocument(c)
			return
		}
		if strings.HasSuffix(rel, "/preferences/workspace-state.json") || strings.HasSuffix(rel, "/preferences/chat-display.json") || strings.Contains(rel, "/preferences/context-shortcuts/") {
			c.Params = append(c.Params, gin.Param{Key: "filepath", Value: "/" + rel})
			workspacehandlers.UpdateDocument(c)
			return
		}
		if strings.TrimPrefix(c.Param("file"), "/") != "Workflow/invoices/workflow.json" {
			c.Status(404)
			return
		}
		var body struct {
			Content string `json:"content"`
		}
		if c.BindJSON(&body) != nil || os.WriteFile(filepath.Join(f.docs, "Workflow", "invoices", "workflow.json"), []byte(body.Content), 0600) != nil {
			c.Status(500)
			return
		}
		c.JSON(200, gin.H{"success": true})
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.upstreamCalls.Add(1); router.ServeHTTP(w, r) }))
	t.Cleanup(upstream.Close)
	t.Setenv("WORKSPACE_API_URL", upstream.URL)
	return f
}
func (f *externalToolsFixture) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(f.docs, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *externalToolsFixture) read(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.docs, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func (f *externalToolsFixture) call(t *testing.T, user, name string, args map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	name, args = canonicalFileTestCall(name, args)
	data, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	var claims *UserClaims
	if user != "" {
		claims = &UserClaims{UserID: user, Username: user}
	}
	w := httptest.NewRecorder()
	f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(data), claims, nil))
	return w
}
func externalTestBody(t *testing.T, w *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}
func (f *externalToolsFixture) planRevision(t *testing.T, user string) string {
	t.Helper()
	body := externalTestBody(t, f.call(t, user, "get_plan", map[string]any{"workflow_id": "invoices"}), 200)
	revision, _ := body["revision"].(string)
	if revision == "" {
		t.Fatal("missing plan revision")
	}
	return revision
}

func TestExternalDirectoryListingsReportExistingPaths(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.write(t, "Workflow/invoices/runs/iteration-1-sched/default/logs/output.txt", "finished")
	for _, call := range []struct {
		name string
		args map[string]any
		path string
	}{
		{"list_files", map[string]any{"workflow_id": "invoices", "path": "docs"}, "docs"},
		{"search_files", map[string]any{"workflow_id": "invoices", "path": "docs", "query": "Invoices"}, "docs"},
		{"list_runs", map[string]any{"workflow_id": "invoices"}, "runs"},
		{"get_run", map[string]any{"workflow_id": "invoices", "run_folder": "iteration-1-sched/default"}, "runs/iteration-1-sched/default"},
		{"get_logs", map[string]any{"workflow_id": "invoices", "run_folder": "iteration-1-sched/default"}, "runs/iteration-1-sched/default/logs"},
	} {
		body := externalTestBody(t, f.call(t, "owner", call.name, call.args), 200)
		if body["exists"] != true || body["path"] != call.path {
			t.Fatalf("%s incorrectly reports directory missing: %v", call.name, body)
		}
	}
}

func TestExternalToolsHTTPCatalogCompilesSchemasAndRequiresIdentity(t *testing.T) {
	api := &StreamingAPI{}
	for _, authenticated := range []bool{false, true} {
		var claims *UserClaims
		if authenticated {
			claims = &UserClaims{UserID: "catalog-reader"}
		}
		w := httptest.NewRecorder()
		api.handleExternalTools(w, adminRequest(http.MethodGet, "/api/external/tools", "", claims, nil))
		if !authenticated {
			externalTestBody(t, w, 401)
			continue
		}
		body := externalTestBody(t, w, 200)
		definitions, ok := body["tools"].([]any)
		if !ok || len(definitions) == 0 {
			t.Fatal("authenticated catalog is missing")
		}
		names := map[string]bool{}
		for _, raw := range definitions {
			tool := raw.(map[string]any)
			name := tool["name"].(string)
			if names[name] {
				t.Fatalf("duplicate %s", name)
			}
			names[name] = true

			if _, leaked := tool["validator"]; leaked {
				t.Fatal("private implementation field leaked")
			}
		}

		// Public action families replace private source names; do not advertise
		// those source names as a second authoring interface.
		for _, name := range []string{"write_file", "patch_file", "update_scripted_step", "update_step_config", "builder_chat", "builder_status", "builder_reply_input", "builder_cancel"} {
			if names[name] {
				t.Fatalf("write tool exposed: %s", name)
			}
		}
		// The run surface proxies run-mode tools, including execution.
		for _, name := range []string{"run_full_workflow", "execute_step", "runs", "trigger_schedule"} {
			if !names[name] {
				t.Fatalf("run tool missing: %s", name)
			}
		}
	}
}

// Source admission is validated by externalTools at startup; authorization is
// exercised through the real MCP/HTTP cases, not intermediate member counts.

func TestExternalRunAuthorityBoundary(t *testing.T) {
	// Pins the runs:execute authority statement in product.yaml: only
	// account-scoped tools and internal chat/bridge tools are withheld; workflow-bound side effects stay
	// because they are the workflow running as configured, not the token
	// acting beyond it. Changing either list is a deliberate API change.
	denied := agentworksproduct.RunExternalDenylist()
	wantDenied := []string{"execute_shell_command", "diff_patch_workspace_file", "read_image", "list_llm_capabilities", "get_goal_metrics", "query_workflow_db", "query_workflow_costs", "google_workspace_cli", "list_mcp_servers", "send_email"}
	if strings.Join(denied, ",") != strings.Join(wantDenied, ",") {
		t.Fatalf("external denylist = %v, want %v", denied, wantDenied)
	}
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]externalTool{}
	for _, tool := range catalog {
		byName[tool.Name] = tool
	}
	for _, name := range denied {
		if _, exposed := byName[name]; exposed {
			t.Fatalf("internal tool %s leaked into the external API", name)
		}
	}
	for _, name := range []string{"send_slack_message", "trigger_schedule"} {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("deliberately kept run tool %s is not exposed", name)
		}
		if !tool.executes {
			t.Fatalf("kept run tool %s is not marked executes", name)
		}
	}
	// Denied tools are unknown to the API, not merely scope-gated.
	f := newExternalToolsFixture(t)
	retired := externalTestBody(t, f.call(t, "owner", "notify_user", map[string]any{"workflow_id": "invoices"}), 404)
	if retired["error"].(map[string]any)["code"] != "unknown_tool" {
		t.Fatalf("retired notification tool remains externally callable: %v", retired)
	}
	body := externalTestBody(t, f.call(t, "owner", "google_workspace_cli", map[string]any{"workflow_id": "invoices"}), 404)
	if body["error"].(map[string]any)["code"] != "unknown_tool" {
		t.Fatalf("wrong error for denylisted tool: %v", body)
	}
}

func TestExternalToolsHTTPMutationsAreNotExposed(t *testing.T) {
	f := newExternalToolsFixture(t)
	before := f.read(t, "Workflow/invoices/planning/plan.json")
	revision := f.planRevision(t, "owner")
	calls := map[string]map[string]any{
		"update_scripted_step": {"workflow_id": "invoices", "expected_revision": revision, "existing_step_id": "fetch-invoices", "title": "Denied", "reason": "Attempt change"},
		"update_step_config":   {"workflow_id": "invoices", "expected_revision": revision, "step_id": "fetch-invoices", "reason": "Attempt change"},
		"write_file":           {"workflow_id": "invoices", "path": "docs/new.md", "expected_revision": "missing", "content": "Denied"},
		"patch_file":           {"workflow_id": "invoices", "path": "docs/process.md", "expected_revision": "anything", "diff": "@@ -1 +1 @@\n-old\n+new\n"},
		"builder_chat":         {"workflow_id": "invoices", "message": "Denied"},
		"builder_status":       {"workflow_id": "invoices", "session_id": "anything"},
		"builder_cancel":       {"workflow_id": "invoices", "session_id": "anything"},
	}
	for name, args := range calls {
		wantStatus, wantCode := 404, "unknown_tool"
		if strings.HasPrefix(name, "builder_") || name == "write_file" {
			wantStatus, wantCode = 403, "insufficient_scope"
		}
		body := externalTestBody(t, f.call(t, "readonly-owner", name, args), wantStatus)
		if body["error"].(map[string]any)["code"] != wantCode {
			t.Fatalf("wrong error for %s: %v", name, body)
		}
	}
	if f.read(t, "Workflow/invoices/planning/plan.json") != before {
		t.Fatal("unexposed mutation changed plan")
	}
}

func TestExternalToolsHTTPWorkflowVisibilityAndReadonlyOwnerGuard(t *testing.T) {
	f := newExternalToolsFixture(t)
	body := externalTestBody(t, f.call(t, "owner", "list_workflows", nil), 200)
	workflows := body["workflows"].([]any)
	// Compact is the default; compact: false returns the full manifest.
	full := externalTestBody(t, f.call(t, "owner", "list_workflows", map[string]any{"compact": false}), 200)["workflows"].([]any)
	if len(full) != 1 || full[0].(map[string]any)["manifest"].(map[string]any)["id"] != "invoices" {
		t.Fatalf("list_workflows with compact:false must carry the manifest: %v", full)
	}
	if len(workflows) != 1 || workflows[0].(map[string]any)["id"] != "invoices" || workflows[0].(map[string]any)["manifest"] != nil {
		t.Fatalf("unauthorized workflow visible: %v", body)
	}
	for _, tool := range []string{"get_workflow", "get_plan"} {
		externalTestBody(t, f.call(t, "owner", tool, map[string]any{"workflow_id": "secret"}), 404)
	}
	for _, user := range []string{"reader", "readonly-owner"} {
		externalTestBody(t, f.call(t, user, "get_plan", map[string]any{"workflow_id": "invoices"}), 200)
		externalTestBody(t, f.call(t, user, "read_file", map[string]any{"workflow_id": "invoices", "path": "docs/process.md"}), 200)
	}
	externalTestBody(t, f.call(t, "outsider", "get_plan", map[string]any{"workflow_id": "invoices"}), 404)
	externalTestBody(t, f.call(t, "", "list_workflows", nil), 401)
	if strings.Contains(f.read(t, "Workflow/invoices/planning/plan.json"), "Denied") {
		t.Fatal("unauthorized request changed plan")
	}
}

func TestExternalToolsHTTPRejectsUnknownArguments(t *testing.T) {
	f := newExternalToolsFixture(t)
	before := f.read(t, "Workflow/invoices/planning/plan.json")
	externalTestBody(t, f.call(t, "owner", "get_plan", map[string]any{"workflow_id": "invoices", "titel": "Misspelled"}), 400)
	externalTestBody(t, f.call(t, "owner", "read_file", map[string]any{"workflow_id": "invoices", "path": "docs/process.md", "managed": true}), 400)
	// Reads reach plan files; there is no write path to protect in v1.
	externalTestBody(t, f.call(t, "owner", "read_file", map[string]any{"workflow_id": "invoices", "path": "planning/plan.json"}), 200)
	// Denylisted run tools answer unknown_tool: the name never enters the
	// catalog, so dispatch cannot reach the proxy.
	denied := f.call(t, "owner", "google_workspace_cli", map[string]any{"workflow_id": "invoices"})
	if denied.Code != http.StatusNotFound || !strings.Contains(denied.Body.String(), "unknown_tool") {
		t.Fatalf("denylisted call status %d: %s", denied.Code, denied.Body.String())
	}
	if f.read(t, "Workflow/invoices/planning/plan.json") != before {
		t.Fatal("invalid request changed plan")
	}
}

func TestExternalToolsHTTPFileGlobAndRuntimeExclusions(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.write(t, "Workflow/invoices/code/run-basic-smoke/main.py", "def test_login(): pass\n")
	f.write(t, "Workflow/invoices/code/.local/lib/installed.py", "def test_hidden(): pass\n")
	args := map[string]any{"workflow_id": "invoices", "path": "code", "glob": "**/*.py", "depth": 8}
	listed := externalTestBody(t, f.call(t, "owner", "list_files", args), 200)
	entries := listed["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["path"] != "code/run-basic-smoke/main.py" {
		t.Fatalf("glob list = %v", listed)
	}
	for _, path := range []string{"code/.local/lib/installed.py", "code/.local"} {
		body := externalTestBody(t, f.call(t, "owner", "read_file", map[string]any{"workflow_id": "invoices", "path": path}), 403)
		if body["error"].(map[string]any)["code"] != "protected_path" {
			t.Fatalf("private read = %v", body)
		}
	}
}

func TestExternalToolsHTTPListStepCode(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoice processing","created_by":"owner","access":{"owners":["owner"],"readers":["reader"]},"code_layout_version":1}`)
	f.write(t, "Workflow/invoices/code/fetch-invoices/main.py", "def main(): pass\n")
	f.write(t, "Workflow/invoices/code/orphan/main.py", "def old(): pass\n")
	f.write(t, "Workflow/invoices/code/fetch-invoices/.cache/installed.py", "def hidden(): pass\n")
	body := externalTestBody(t, f.call(t, "owner", "list_step_code", map[string]any{"workflow_id": "invoices"}), 200)
	entries := body["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("code inventory = %v", body)
	}
	first := entries[0].(map[string]any)
	if first["path"] != "code/fetch-invoices/main.py" || first["step_id"] != "fetch-invoices" || first["step_title"] != "Fetch invoices" || first["in_plan"] != true {
		t.Fatalf("plan code entry = %v", first)
	}
	second := entries[1].(map[string]any)
	if second["step_id"] != "orphan" || second["in_plan"] != false {
		t.Fatalf("orphan code entry = %v", second)
	}
	selected := externalTestBody(t, f.call(t, "reader", "list_step_code", map[string]any{"workflow_id": "invoices", "step_id": "fetch-invoices"}), 200)
	if len(selected["entries"].([]any)) != 1 {
		t.Fatalf("selected code = %v", selected)
	}
	externalTestBody(t, f.call(t, "owner", "list_step_code", map[string]any{"workflow_id": "invoices", "step_id": "../secret"}), 400)
}

func TestExternalStopStepDoesNotCancelSession(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.api.stoppedSessions = map[string]bool{}
	f.api.trackedWorkflowExecutions = map[string]*TrackedWorkflowExecution{
		"exec-1": {ExecutionID: "exec-1", SessionID: "sess-1", WorkspacePath: "Workflow/invoices", Status: trackedExecutionStatusRunning, Source: trackedExecutionSourceWorkshopBackground, Kind: "step"},
		"exec-2": {ExecutionID: "exec-2", SessionID: "sess-1", WorkspacePath: "Workflow/invoices", Status: trackedExecutionStatusRunning, Source: trackedExecutionSourceWorkshopBackground, Kind: "step"},
	}
	registry := stepworkflow.NewWorkshopStepRegistry()
	registry.Register(&stepworkflow.WorkshopStepExecution{ID: "exec-1", StepID: "fetch-invoices", Status: stepworkflow.WorkshopStepRunning})
	registry.Register(&stepworkflow.WorkshopStepExecution{ID: "exec-2", StepID: "notify", Status: stepworkflow.WorkshopStepRunning})
	f.api.workshopChatSessions.Store("sess-1", &stepworkflow.WorkshopChatSession{StepRegistry: registry})
	// Missing execution_id fails schema validation before dispatch.
	externalTestBody(t, f.call(t, "owner", "stop_step", map[string]any{"workflow_id": "invoices"}), 400)
	// Unknown execution resolves to nothing.
	missing := f.call(t, "owner", "stop_step", map[string]any{"workflow_id": "invoices", "execution_id": "nope"})
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "execution_not_found") {
		t.Fatalf("unknown execution status %d: %s", missing.Code, missing.Body.String())
	}
	// A session filter that does not own the execution rejects.
	mismatch := f.call(t, "owner", "stop_step", map[string]any{"workflow_id": "invoices", "execution_id": "exec-1", "session_id": "sess-9"})
	if mismatch.Code != http.StatusNotFound {
		t.Fatalf("session mismatch status %d, want 404", mismatch.Code)
	}
	// A tracked step without a cancel function must not cancel its entire session.
	stopped := f.call(t, "owner", "stop_step", map[string]any{"workflow_id": "invoices", "execution_id": "exec-1"})
	if stopped.Code != http.StatusConflict || !strings.Contains(stopped.Body.String(), "execution_not_cancelable") {
		t.Fatalf("non-cancelable step status %d: %s", stopped.Code, stopped.Body.String())
	}
	if f.api.stoppedSessions["sess-1"] {
		t.Fatal("stop_step stopped the whole session")
	}
	if got := f.api.trackedWorkflowExecutions["exec-2"].Status; got != trackedExecutionStatusRunning {
		t.Fatalf("sibling execution status %q, want running", got)
	}
}

func TestExternalStopOwnershipAcrossTokens(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.api.stoppedSessions = map[string]bool{}
	f.api.trackedWorkflowExecutions = map[string]*TrackedWorkflowExecution{
		"exec-1": {ExecutionID: "exec-1", SessionID: "pat-other-s1", WorkspacePath: "Workflow/invoices", Status: trackedExecutionStatusRunning, Source: trackedExecutionSourceWorkflowRun},
	}
	claims := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "t1", Scopes: []string{"workflows:read", "files:read", "runs:execute"}, AllWorkflows: true}}
	data, _ := json.Marshal(map[string]any{"name": "stop_step", "arguments": map[string]any{"workflow_id": "invoices", "execution_id": "exec-1"}})
	w := httptest.NewRecorder()
	f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(data), claims, nil))
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "execution_not_found") {
		t.Fatalf("foreign execution status %d: %s", w.Code, w.Body.String())
	}
	if f.api.stoppedSessions["pat-other-s1"] {
		t.Fatal("foreign session was stopped")
	}
}

func TestExternalStopAllExecutions(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.api.stoppedSessions = map[string]bool{}
	// App callers must scope to one session.
	scopeless := f.call(t, "owner", "stop_all_executions", map[string]any{"workflow_id": "invoices"})
	if scopeless.Code != http.StatusBadRequest {
		t.Fatalf("unscoped app call status %d, want 400", scopeless.Code)
	}
	// Token callers stop exactly their own sessions in the workflow.
	f.api.accessTokenSessions.sessions = map[string]accessTokenSession{
		"pat-t1-s1": {tokenID: "t1", sessionID: "pat-t1-s1", workspace: "Workflow/invoices"},
		"pat-t1-s2": {tokenID: "t1", sessionID: "pat-t1-s2", workspace: "Workflow/invoices"},
		"pat-t1-x":  {tokenID: "t1", sessionID: "pat-t1-x", workspace: "Workflow/secret"},
		"pat-t2-s1": {tokenID: "t2", sessionID: "pat-t2-s1", workspace: "Workflow/invoices"},
	}
	claims := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "t1", Scopes: []string{"workflows:read", "files:read", "runs:execute"}, AllWorkflows: true}}
	data, _ := json.Marshal(map[string]any{"name": "stop_all_executions", "arguments": map[string]any{"workflow_id": "invoices"}})
	w := httptest.NewRecorder()
	f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(data), claims, nil))
	body := externalTestBody(t, w, 200)
	raw, _ := body["stopped_sessions"].([]any)
	stopped := make([]string, 0, len(raw))
	for _, item := range raw {
		if sid, ok := item.(string); ok {
			stopped = append(stopped, sid)
		}
	}
	if len(stopped) != 2 || stopped[0] != "pat-t1-s1" || stopped[1] != "pat-t1-s2" {
		t.Fatalf("stopped %v, want exactly the token's invoices sessions", stopped)
	}
	for _, sid := range []string{"pat-t1-s1", "pat-t1-s2"} {
		if !f.api.stoppedSessions[sid] {
			t.Fatalf("session %s was not marked stopped", sid)
		}
	}
	for _, sid := range []string{"pat-t1-x", "pat-t2-s1"} {
		if f.api.stoppedSessions[sid] {
			t.Fatalf("out-of-scope session %s was stopped", sid)
		}
	}
	// A named session outside the token's ownership rejects before any lookup.
	foreign, _ := json.Marshal(map[string]any{"name": "stop_all_executions", "arguments": map[string]any{"workflow_id": "invoices", "session_id": "pat-t2-s1"}})
	fw := httptest.NewRecorder()
	f.api.handleExternalCall(fw, adminRequest(http.MethodPost, "/api/external/call", string(foreign), claims, nil))
	if fw.Code != http.StatusNotFound {
		t.Fatalf("foreign session status %d, want 404", fw.Code)
	}
}

func TestExternalToolsInternalFileEndpointCannotBeProxied(t *testing.T) {
	f := newExternalToolsFixture(t)
	proxy := workspaceProxyHandler()
	for _, target := range []string{"/api/wp/api/dashboards", "/api/wp/api/nested/../dashboards", "/api/wp/api/workflow-files", "/api/wp/api/workflow-files/", "/api/wp/api/nested/../workflow-files", "/api/wp//api//workflow-files", "/api/wp/api/nested/%2e%2e/workflow-files", "/api/wp/api/%77orkflow-files"} {
		t.Run(fmt.Sprintf("path=%s", target), func(t *testing.T) {
			before := f.upstreamCalls.Load()
			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, adminRequest(http.MethodPost, target, `{"root":"Workflow/secret","operation":"commit","managed":true}`, &UserClaims{UserID: "owner"}, nil))
			if w.Code != 404 {
				t.Fatalf("internal endpoint proxy status %d: %s", w.Code, w.Body.String())
			}
			if f.upstreamCalls.Load() != before {
				t.Fatal("internal endpoint reached workspace service")
			}
		})
	}
}

// This uses the same transport client consumed by CLI and MCP against real
// authentication, external HTTP handlers, and disk-backed workspace
// operations. Only workflow discovery is stubbed by the fixture.
func TestExternalToolsClientTransportThroughJWTAndWorkspace(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AUTH_SECRET", "external-tools-transport-auth-secret-for-tests")
	router := http.NewServeMux()
	router.HandleFunc("GET /api/external/v1/tools", f.api.handleExternalTools)
	router.HandleFunc("POST /api/external/v1/call", f.api.handleExternalCall)
	server := httptest.NewServer(AuthMiddleware(router))
	t.Cleanup(server.Close)

	token, err := GenerateJWT("owner", "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	client, err := agentworksclient.New(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	catalog, err := client.Tools(ctx)
	if err != nil {
		t.Fatalf("authenticated catalog: %v", err)
	}
	found := map[string]bool{}
	for _, tool := range catalog {
		found[tool.Name] = true
	}
	for _, name := range []string{"workflow", "files", "help"} {
		if !found[name] {
			t.Fatalf("read tool %s missing from client catalog", name)
		}
	}
	for _, name := range []string{"update_scripted_step", "write_file", "builder_chat"} {
		if found[name] {
			t.Fatalf("write tool %s exposed in client catalog", name)
		}
	}
	raw, err := client.Call(ctx, "get_plan", map[string]any{"workflow_id": "invoices"})
	if err != nil {
		t.Fatalf("get_plan: %v", err)
	}
	var plan struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil || plan.Revision == "" {
		t.Fatalf("invalid get_plan result %s: %v", raw, err)
	}
	raw, err = client.Call(ctx, "files", map[string]any{"action": "read", "workflow_id": "invoices", "path": "docs/process.md"})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	var file struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || file.Content == "" {
		t.Fatalf("invalid read result %s: %v", raw, err)
	}

	// Missing and expired credentials must fail before workflow discovery or
	// any workspace operation. The client sends a real bearer JWT when present.
	workspaceCalls := f.upstreamCalls.Load()
	missing, err := http.Get(server.URL + "/api/external/v1/tools")
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing JWT status = %d", missing.StatusCode)
	}
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &UserClaims{
		UserID: "owner", Username: "owner", RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	}).SignedString(GetAuthSecret())
	if err != nil {
		t.Fatal(err)
	}
	expiredClient, err := agentworksclient.New(server.URL, expired)
	if err != nil {
		t.Fatal(err)
	}
	_, err = expiredClient.Tools(ctx)
	var apiErr *agentworksclient.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("expired JWT must return client APIError 401, got %v", err)
	}
	_, err = expiredClient.Call(ctx, "get_plan", map[string]any{"workflow_id": "invoices"})
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("expired JWT call must return client APIError 401, got %v", err)
	}
	if f.upstreamCalls.Load() != workspaceCalls {
		t.Fatal("unauthenticated requests reached the workspace")
	}
}

// A large plan is read in pieces: the outline lists steps and section sizes, step_id reads one step, and the plan is
// no longer repeated inside artifacts (PLAT-837).
func TestExternalPlanCanBeReadInPieces(t *testing.T) {
	f := newExternalToolsFixture(t)
	full := externalTestBody(t, f.call(t, "owner", "get_plan", map[string]any{"workflow_id": "invoices"}), 200)
	if _, repeated := full["artifacts"].(map[string]any)["planning/plan.json"]; repeated || full["plan"] == nil {
		t.Fatalf("the plan must be returned once, as plan: %v", full)
	}
	outline := externalTestBody(t, f.call(t, "owner", "get_plan", map[string]any{"workflow_id": "invoices", "view": "outline"}), 200)
	steps := outline["outline"].(map[string]any)["steps"].([]any)
	if len(steps) != 1 || steps[0].(map[string]any)["id"] != "fetch-invoices" || outline["revision"] != full["revision"] {
		t.Fatalf("outline = %v", outline)
	}
	one := externalTestBody(t, f.call(t, "owner", "get_plan", map[string]any{"workflow_id": "invoices", "step_id": "fetch-invoices"}), 200)
	if one["step"].(map[string]any)["title"] != "Fetch invoices" {
		t.Fatalf("step = %v", one)
	}
	externalTestBody(t, f.call(t, "owner", "get_plan", map[string]any{"workflow_id": "invoices", "step_id": "nope"}), 404)
}

// The Crew root can be listed: an empty path is not refused by the schema (PLAT-837).
func TestListCrewFilesAcceptsAnEmptyPath(t *testing.T) {
	tools, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.Name != "list_crew_files" && tool.Name != "search_crew_files" {
			continue
		}
		properties, _ := tool.InputSchema["properties"].(map[string]any)
		path, _ := properties["path"].(map[string]any)
		if _, restricted := path["minLength"]; restricted || path == nil {
			t.Fatalf("%s must accept an empty path, got %v", tool.Name, path)
		}
	}
}
