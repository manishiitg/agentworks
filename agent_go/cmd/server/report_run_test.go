package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestReportRunScriptPath(t *testing.T) {
	for _, ok := range []string{"code/reports/deals.py", "code/x.js", " code/a/b.mjs ", "code\\reports\\w.py"} {
		if _, allowed := reportRunScriptPath(ok); !allowed {
			t.Fatalf("rejected %q", ok)
		}
	}
	for _, bad := range []string{"", "db/x.py", "code/x.sh", "code/../workflow.py", "../code/x.py", "code/.hidden/x.py", "code/x", "/etc/x.py"} {
		if normalized, allowed := reportRunScriptPath(bad); allowed {
			t.Fatalf("accepted %q as %q", bad, normalized)
		}
	}
	if got := reportRunCommand("/docs/Workflow/it's/code/x.py"); got != `python3 '/docs/Workflow/it'"'"'s/code/x.py'` {
		t.Fatalf("command quoting: %s", got)
	}
	if got := reportRunCommand("/docs/code/x.mjs"); !strings.HasPrefix(got, "node ") {
		t.Fatalf("node script ran as %s", got)
	}
}

// A report run's bridge session answers only while its script is running,
// and an ordinary session is left to the other resolvers.
func TestReportRunMCPSessionScope(t *testing.T) {
	api := &StreamingAPI{}
	if _, isReportRun, err := api.resolveReportRunMCPServer(context.Background(), "chat-session", "notion", "search"); isReportRun || err != nil {
		t.Fatalf("ordinary session claimed by report run: %v %v", isReportRun, err)
	}
	if _, isReportRun, err := api.resolveReportRunMCPServer(context.Background(), "report-run-finished", "notion", "search"); !isReportRun || err == nil {
		t.Fatalf("finished report run still reached MCP: %v %v", isReportRun, err)
	}
}

// A crew Dashboard is runnable by its owner and by users with the Crew
// product; anyone else is refused before anything runs.
func TestReportRunCrewAccess(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"outsider","username":"outsider","can_create":true,"products":["agentworks"]}]}`)
	api := &StreamingAPI{}
	post := func(claims *UserClaims) int {
		r := httptest.NewRequest("POST", reportPreviewAPIPrefix+"run", strings.NewReader(`{"workspace":"_users/owner/Chats/Work/projects/sde","path":"code/x.py"}`))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		api.handleReportRun(w, r)
		return w.Code
	}
	if code := post(&UserClaims{UserID: "outsider", Username: "outsider"}); code != http.StatusForbidden {
		t.Fatalf("user without the Crew product: %d", code)
	}
	// The owner passes the access check (the script itself does not exist).
	if code := post(&UserClaims{UserID: "owner", Username: "owner"}); code != http.StatusNotFound {
		t.Fatalf("owner: %d", code)
	}
	// Same through the user-relative path the owner's own Dashboard sends.
	r := httptest.NewRequest("POST", reportPreviewAPIPrefix+"run", strings.NewReader(`{"workspace":"Chats/Work/projects/sde","path":"code/x.py"}`))
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"}))
	w := httptest.NewRecorder()
	api.handleReportRun(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("owner via relative path: %d %s", w.Code, w.Body.String())
	}
}

func TestReportRunRejectsBeforeRunning(t *testing.T) {
	api := &StreamingAPI{}
	post := func(body string, claims *UserClaims) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", reportPreviewAPIPrefix+"run", strings.NewReader(body))
		if claims != nil {
			r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		}
		w := httptest.NewRecorder()
		api.handleReportRun(w, r)
		return w
	}
	if w := post(`{"workspace":"Workflow/a","path":"code/x.py"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous run: %d", w.Code)
	}
	reader := &UserClaims{UserID: "reader"}
	if w := post(`{"workspace":"Workflow/a","path":"db/x.py"}`, reader); w.Code != http.StatusBadRequest {
		t.Fatalf("script outside code/: %d", w.Code)
	}
	// A shared crew nobody owns (or that does not exist) is refused.
	stubCrewLookups(t, nil, nil)
	if w := post(`{"workspace":"Crew/a","path":"code/x.py"}`, reader); w.Code != http.StatusForbidden {
		t.Fatalf("ownerless shared crew: %d", w.Code)
	}
	for _, ws := range []string{"Crew/a/sub", "Workflow/a/sub", "_users/o/Chats/Work/projects", "_users/o/Chats/Work/projects/p/sub", "_users/o/Chats/Other/projects/p"} {
		if w := post(`{"workspace":"`+ws+`","path":"code/x.py"}`, reader); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", ws, w.Code)
		}
	}
	// A preview token bound to one workflow cannot run another's scripts.
	bound := &UserClaims{UserID: "reader", Scope: reportPreviewScope, ScopeWorkspace: "Workflow/b"}
	if w := post(`{"workspace":"Workflow/a","path":"code/x.py"}`, bound); w.Code != http.StatusBadRequest {
		t.Fatalf("cross-workflow preview token: %d", w.Code)
	}
}

// The script gets the workflow's sandbox: read the workflow, write only the
// report cache, REPORT_ARGS, a bridge session scoped to this run, and its
// one JSON value comes back as data.
func TestRunReportScriptSendsSandboxedRequestAndParsesJSON(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("MCP_API_URL", "http://127.0.0.1:9/api")
	// The process-wide token never reaches the script; it gets its own
	// session's token (bridge_token.go).
	t.Setenv("MCP_API_TOKEN", "global-token")
	common.SetBridgeTokenSecret("test-signing-secret")
	t.Cleanup(func() { common.SetBridgeTokenSecret("") })
	if err := os.MkdirAll(filepath.Join(docs, "Workflow/deals/code/reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "Workflow/deals/code/reports/open.py"), []byte("print(1)"), 0o644); err != nil {
		t.Fatal(err)
	}

	var sent struct {
		Command          string            `json:"command"`
		WorkingDirectory string            `json:"working_directory"`
		Timeout          int               `json:"timeout"`
		ExtraEnv         map[string]string `json:"extra_env"`
		DBReadSnapshot   bool              `json:"db_read_snapshot"`
		FolderGuard      struct {
			Enabled    bool     `json:"enabled"`
			ReadPaths  []string `json:"read_paths"`
			WritePaths []string `json:"write_paths"`
		} `json:"folder_guard"`
	}
	var sessionLive bool
	api := &StreamingAPI{}
	stdout := `{"deals":[{"name":"Acme"}],"fetched_at":"2026-09-26"}`
	workspaceSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/workflow.json"):
			manifest, _ := json.Marshal(map[string]any{"capabilities": map[string]any{"selected_servers": []string{"notion"}, "selected_global_secret_names": []string{}}})
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"content": string(manifest)}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/variables.json"):
			vars := `{"variables":[{"name":"REGION","value":"eu"}],"groups":[{"name":"main","enabled":true,"values":{"REGION":"us"}}]}`
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"content": vars}})
		case r.URL.Path == "/api/execute":
			// json.Decoder reuses non-nil maps; capture only this request's env.
			sent.ExtraEnv = nil
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
				t.Error(err)
			}
			_, sessionLive = api.reportRunSessions.Load(sent.ExtraEnv["MCP_SESSION_ID"])
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"stdout": stdout + "\n", "exit_code": 0}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer workspaceSvc.Close()
	t.Setenv("WORKSPACE_API_URL", workspaceSvc.URL)

	absScript := filepath.Join(docs, "Workflow/deals/code/reports/open.py")
	result := api.runReportScript(context.Background(), "reader", "Workflow/deals", "code/reports/open.py", absScript, `{"days":7}`)
	if result["success"] != true {
		t.Fatalf("run failed: %v", result)
	}
	data, _ := json.Marshal(result["data"])
	if string(data) != `{"deals":[{"name":"Acme"}],"fetched_at":"2026-09-26"}` {
		t.Fatalf("data: %s", data)
	}
	if !sessionLive {
		t.Fatal("MCP bridge session was not registered while the script ran")
	}
	if _, stillLive := api.reportRunSessions.Load(sent.ExtraEnv["MCP_SESSION_ID"]); stillLive {
		t.Fatal("MCP bridge session outlived the script")
	}
	env := sent.ExtraEnv
	sid := env["MCP_SESSION_ID"]
	if !strings.HasPrefix(sid, "report-run-") || env["MCP_API_URL"] != "http://127.0.0.1:9/api/s/"+sid || env["MCP_AUTH"] != "Authorization: Bearer "+common.BridgeTokenForSession(sid) || strings.Contains(fmt.Sprint(env), "global-token") {
		t.Fatalf("bridge env: %v", env)
	}
	if env["REPORT_ARGS"] != `{"days":7}` || env["VAR_REGION"] != "us" || env["REPORT_CACHE_DIR"] != filepath.Join(docs, "Workflow/deals/.report-cache") {
		t.Fatalf("report env: %v", env)
	}
	if _, hasDB := env["DB_PATH"]; hasDB || sent.DBReadSnapshot {
		t.Fatalf("no DB yet, but DB_PATH/snapshot requested: %v %v", env["DB_PATH"], sent.DBReadSnapshot)
	}
	if !sent.FolderGuard.Enabled || strings.Join(sent.FolderGuard.ReadPaths, ",") != "Workflow/deals" || strings.Join(sent.FolderGuard.WritePaths, ",") != "Workflow/deals/.report-cache" {
		t.Fatalf("guard: %+v", sent.FolderGuard)
	}
	if sent.Timeout != 60 || sent.WorkingDirectory != "Workflow/deals/code" || !strings.HasPrefix(sent.Command, "python3 '") {
		t.Fatalf("exec: %+v", sent)
	}

	// Code uses the same real HTTP entry point, canonical owner path and sandbox
	// envelope; neither another user nor an admin inspection may execute it.
	codeRoot := "_users/reader/Chats/Code/projects/dashboard"
	codeScript := filepath.Join(docs, codeRoot, "code/reports/open.py")
	if err := os.MkdirAll(filepath.Dir(codeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codeScript, []byte("print(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{codeRoot, "Chats/Code/projects/dashboard"} {
		r := httptest.NewRequest("POST", reportPreviewAPIPrefix+"run", strings.NewReader(`{"workspace":"`+address+`","path":"code/reports/open.py","args":{"days":7}}`))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "reader"}))
		w := httptest.NewRecorder()
		api.handleReportRun(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"success":true`) {
			t.Fatalf("Code run %s: %d %s", address, w.Code, w.Body.String())
		}
		if sent.WorkingDirectory != codeRoot+"/code" || strings.Join(sent.FolderGuard.WritePaths, ",") != codeRoot+"/.report-cache" || sent.ExtraEnv["VAR_REGION"] != "" {
			t.Fatalf("Code escaped its project scope: %+v", sent)
		}
		selection, err := reportRunSelectionFor(context.Background(), codeRoot)
		if err != nil || strings.Join(selection.servers, ",") != "notion" || selection.globalSecrets == nil || len(*selection.globalSecrets) != 0 {
			t.Fatalf("Code selection: %+v %v", selection, err)
		}
	}
	postCode := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", reportPreviewAPIPrefix+"run", strings.NewReader(`{"workspace":"`+codeRoot+`","path":"code/reports/open.py"}`))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "reader"}))
		w := httptest.NewRecorder()
		api.handleReportRun(w, r)
		return w
	}
	// An owner-controlled code/ symlink must not turn another project's script
	// into an executable under this project's permissions.
	codeDir := filepath.Join(docs, codeRoot, "code")
	if err := os.Rename(codeDir, codeDir+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(docs, "Workflow/deals/code"), codeDir); err != nil {
		t.Fatal(err)
	}
	if w := postCode(); w.Code != http.StatusBadRequest {
		t.Fatalf("symlinked Code directory: %d %s", w.Code, w.Body.String())
	}
	if err := os.Remove(codeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(codeDir+"-saved", codeDir); err != nil {
		t.Fatal(err)
	}
	// A copied Code with a conflicting server-controlled owner is refused.
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "code", Folder: "dashboard", OwnerID: "registered-owner"}); err != nil {
		t.Fatal(err)
	}
	if w := postCode(); w.Code != http.StatusForbidden {
		t.Fatalf("Code owner registry conflict: %d %s", w.Code, w.Body.String())
	}

	withMemoryUserDirectory(t, `{"users":[{"id":"admin-viewer","username":"admin","admin":true,"can_create":true}]}`)
	for _, caller := range []string{"other", "admin-viewer"} {
		r := httptest.NewRequest("POST", reportPreviewAPIPrefix+"run", strings.NewReader(`{"workspace":"`+codeRoot+`","path":"code/reports/open.py"}`))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: caller}))
		w := httptest.NewRecorder()
		api.handleReportRun(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("non-owner Code run (%s): %d %s", caller, w.Code, w.Body.String())
		}
	}

	// With a DB, the script reads a snapshot, never the live store.
	if err := os.MkdirAll(filepath.Join(docs, "Workflow/deals/db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "Workflow/deals/db/db.sqlite"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout = "Fetched 3 deals"
	result = api.runReportScript(context.Background(), "reader", "Workflow/deals", "code/reports/open.py", absScript, "{}")
	if !sent.DBReadSnapshot || sent.ExtraEnv["DB_PATH"] == "" || sent.ExtraEnv["STEP_OUTPUT_DIR"] != sent.ExtraEnv["REPORT_CACHE_DIR"] {
		t.Fatalf("DB snapshot not requested: %v %v", sent.DBReadSnapshot, sent.ExtraEnv)
	}
	if result["success"] != false || !strings.Contains(result["error"].(string), "one JSON value") {
		t.Fatalf("non-JSON stdout accepted: %v", result)
	}
}
