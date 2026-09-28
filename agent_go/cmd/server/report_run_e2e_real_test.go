package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	_ "modernc.org/sqlite"
)

// Real window.report.run against a running workspace service: the real
// sandbox, a real Python script, the read-only DB snapshot, the cache folder,
// and MCP bridge calls resolved through the report-run scope.
//
//	REPORT_RUN_E2E_DOCS=/path/to/workspace-docs \
//	WORKSPACE_API_URL=http://127.0.0.1:18911 WORKSPACE_API_TOKEN=... \
//	go test ./cmd/server/ -run TestReportRunRealE2E -v -count=1
//
// REPORT_RUN_E2E_DOCS must be the service's own --docs-dir. Skipped unless set.
func TestReportRunRealE2E(t *testing.T) {
	docs := strings.TrimSpace(os.Getenv("REPORT_RUN_E2E_DOCS"))
	if docs == "" {
		t.Skip("set REPORT_RUN_E2E_DOCS to a running workspace service's docs dir")
	}
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	withMemoryUserDirectory(t, `{"users":[{"id":"report-viewer","username":"viewer","admin":true,"can_create":true,"products":[]}]}`)

	workflow := "Workflow/report-run-e2e"
	root := filepath.Join(docs, workflow)
	_ = os.RemoveAll(root)
	for _, dir := range []string{"code/reports", "db"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("workflow.json", `{"id":"report-run-e2e","name":"report-run-e2e","capabilities":{"selected_servers":["notion"],"selected_global_secret_names":[]}}`)
	write("code/reports/live.py", `import json, os, sqlite3, urllib.request, pathlib
args = json.loads(os.environ["REPORT_ARGS"])
deals = [r[0] for r in sqlite3.connect(os.environ["DB_PATH"]).execute("select name from deals order by name")]
def call(server):
    req = urllib.request.Request(os.environ["MCP_MCP"] + "/" + server + "/search", data=b"{}",
        headers={"Authorization": os.environ["MCP_AUTH"].split(": ", 1)[1], "Content-Type": "application/json"})
    return json.load(urllib.request.urlopen(req, timeout=10))
cache = pathlib.Path(os.environ["REPORT_CACHE_DIR"]) / "live.json"
cache.write_text(json.dumps(deals))
def try_write(path):
    try:
        with open(path, "w") as f:
            f.write("x")
        return True
    except OSError:
        return False
print("fetched", len(deals), "deals", file=__import__("sys").stderr)
print(json.dumps({
    "days": args["days"], "deals": deals,
    "notion": call("notion"), "github": call("github"),
    "cached": cache.exists(),
    "wrote_db_folder": try_write(os.path.join(os.environ["WORKFLOW_CODE_ROOT"], "..", "db", "evil.txt")),
    "wrote_code_folder": try_write(os.path.join(os.environ["WORKFLOW_CODE_ROOT"], "evil.txt")),
    "db_is_snapshot": os.environ["DB_PATH"] != os.path.join(os.environ["WORKFLOW_CODE_ROOT"], "..", "db", "db.sqlite"),
}))
`)
	write("code/reports/broken.py", "import sys\nprint('token is', 'not json')\n")
	db, err := sql.Open("sqlite", filepath.Join(root, "db", "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table deals(name text); insert into deals values ('Acme'), ('Globex')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	mcpConfig := filepath.Join(t.TempDir(), "mcp_servers.json")
	if err := os.WriteFile(mcpConfig, []byte(`{"mcpServers":{"notion":{"command":"true"},"github":{"command":"true"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{mcpConfigPath: mcpConfig}

	// The bridge stands in for the executor: it answers with the report-run
	// scope decision for the session id baked into MCP_API_URL.
	var bridgeAuth []string
	bridgeRouter := mux.NewRouter()
	bridgeRouter.HandleFunc("/api/s/{sid}/tools/mcp/{server}/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		bridgeAuth = append(bridgeAuth, r.Header.Get("Authorization"))
		resolved, isReportRun, err := api.resolveReportRunMCPServer(r.Context(), vars["sid"], vars["server"], vars["tool"])
		out := map[string]any{"report_run": isReportRun}
		if err != nil {
			out["error"] = err.Error()
		} else if resolved != nil {
			out["server"] = resolved.Name
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	bridge := httptest.NewServer(bridgeRouter)
	defer bridge.Close()
	t.Setenv("MCP_API_URL", bridge.URL+"/api")
	t.Setenv("MCP_API_TOKEN", "bridge-e2e-token")

	runIn := func(workspacePath, path string) (int, map[string]any) {
		r := httptest.NewRequest("POST", reportPreviewAPIPrefix+"run", strings.NewReader(`{"workspace":"`+workspacePath+`","path":"`+path+`","args":{"days":7}}`))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "report-viewer"}))
		w := httptest.NewRecorder()
		api.handleReportRun(w, r)
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Code, body
	}
	run := func(path string) (int, map[string]any) { return runIn(workflow, path) }

	code, body := run("code/reports/live.py")
	pretty, _ := json.MarshalIndent(body, "", "  ")
	t.Logf("live.py -> %d\n%s", code, pretty)
	if code != http.StatusOK || body["success"] != true {
		t.Fatalf("live run failed")
	}
	data := body["data"].(map[string]any)
	if data["days"] != float64(7) || strings.Join(toStrings(data["deals"]), ",") != "Acme,Globex" {
		t.Fatalf("args/DB not delivered: %v", data)
	}
	if notion := data["notion"].(map[string]any); notion["server"] != "notion" || notion["report_run"] != true {
		t.Fatalf("selected server not reachable: %v", notion)
	}
	if github := data["github"].(map[string]any); !strings.Contains(github["error"].(string), "not available") {
		t.Fatalf("unselected server reachable: %v", github)
	}
	if data["cached"] != true || data["db_is_snapshot"] != true {
		t.Fatalf("cache/snapshot: %v", data)
	}
	if data["wrote_db_folder"] == true || data["wrote_code_folder"] == true {
		t.Fatalf("report script wrote outside its cache folder: %v", data)
	}
	for _, header := range bridgeAuth {
		if header != "Bearer bridge-e2e-token" {
			t.Fatalf("bridge auth header: %q", header)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".report-cache", "live.json")); err != nil {
		t.Fatalf("cache file: %v", err)
	}

	code, body = run("code/reports/broken.py")
	t.Logf("broken.py -> %d %v", code, body)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body["error"].(string), "one JSON value") {
		t.Fatalf("non-JSON output accepted")
	}

	// A crew project's Dashboard, opened by someone who is not its owner
	// (Crew Run mode): same sandbox, the crew's own server selection.
	crew := "_users/crew-owner/Chats/Work/projects/sde"
	crewRoot := filepath.Join(docs, crew)
	_ = os.RemoveAll(filepath.Join(docs, "_users/crew-owner"))
	for _, dir := range []string{"code/reports", "db"} {
		if err := os.MkdirAll(filepath.Join(crewRoot, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	liveScript, _ := os.ReadFile(filepath.Join(root, "code/reports/live.py"))
	for rel, content := range map[string]string{
		"product.json":         `{"schema_version":1,"product":"work","id":"sde","title":"SDE"}`,
		"workflow.json":        `{"schema_version":1,"id":"sde","capabilities":{"selected_servers":["notion"],"selected_global_secret_names":[]}}`,
		"code/reports/live.py": string(liveScript),
	} {
		if err := os.WriteFile(filepath.Join(crewRoot, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	crewDB, err := sql.Open("sqlite", filepath.Join(crewRoot, "db", "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crewDB.Exec(`create table deals(name text); insert into deals values ('Initech')`); err != nil {
		t.Fatal(err)
	}
	crewDB.Close()
	code, body = runIn(crew, "code/reports/live.py")
	pretty, _ = json.MarshalIndent(body, "", "  ")
	t.Logf("crew live.py -> %d\n%s", code, pretty)
	if code != http.StatusOK || body["success"] != true {
		t.Fatalf("crew run failed")
	}
	data = body["data"].(map[string]any)
	if strings.Join(toStrings(data["deals"]), ",") != "Initech" || data["notion"].(map[string]any)["server"] != "notion" ||
		!strings.Contains(data["github"].(map[string]any)["error"].(string), "not available") ||
		data["wrote_db_folder"] == true || data["wrote_code_folder"] == true {
		t.Fatalf("crew scope: %v", data)
	}

	// The owner's own Dashboard sends the user-relative project path.
	own := "_users/report-viewer/Chats/Work/projects/sde"
	_ = os.RemoveAll(filepath.Join(docs, "_users/report-viewer"))
	if err := os.MkdirAll(filepath.Dir(filepath.Join(docs, own)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(crewRoot, filepath.Join(docs, own)); err != nil {
		t.Fatal(err)
	}
	code, body = runIn("Chats/Work/projects/sde", "code/reports/live.py")
	t.Logf("owner crew live.py (relative path) -> %d %v", code, body["error"])
	if code != http.StatusOK || body["success"] != true {
		t.Fatalf("owner crew run via relative path failed")
	}
}

func toStrings(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}
