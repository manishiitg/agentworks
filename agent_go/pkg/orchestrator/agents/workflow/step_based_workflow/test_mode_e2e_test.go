package step_based_workflow

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/viper"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/testmode"
	wshandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// TestStepTestModeContainsExternalEffects is the PLAT-562 end-to-end check on
// the real bridge path every CLI coding agent and script uses: mcpagent's
// executor HTTP handlers, a real MCP server over streamable HTTP, the real
// workflow DB tools against the real workspace query/mutate handlers and a
// real SQLite file. A test session reads for real; its MCP write, browser click
// and DB write never reach the server, the browser or the real DB, and the
// run records each one. The same calls from a normal session do reach them.
func TestStepTestModeContainsExternalEffects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", "") })

	const workflow = "Workflow/tm-demo"
	wfAbs := filepath.Join(docs, workflow)
	realDB := filepath.Join(wfAbs, "db", "db.sqlite")
	mustMkdir(t, filepath.Dir(realDB), filepath.Join(wfAbs, "learnings"), filepath.Join(wfAbs, "runs", "iteration-0", "default", "execution", "pick-job"))
	db, err := sql.Open("sqlite", realDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE proposals (job_id TEXT PRIMARY KEY, status TEXT); INSERT INTO proposals VALUES ('job-1', 'open')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err := os.WriteFile(filepath.Join(wfAbs, "runs", "iteration-0", "default", "execution", "pick-job", "picked.json"), []byte(`{"job_id":"job-2"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// A fake external system: one read-only tool, one that would submit.
	var mu sync.Mutex
	calls := map[string]int{}
	mcpServer := server.NewMCPServer("fake-upwork", "1.0.0")
	count := func(name string) server.ToolHandlerFunc {
		return func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			mu.Lock()
			calls[name]++
			mu.Unlock()
			return mcp.NewToolResultText(name + " done"), nil
		}
	}
	mcpServer.AddTool(mcp.NewTool("read_job", mcp.WithReadOnlyHintAnnotation(true), mcp.WithDestructiveHintAnnotation(false)), count("read_job"))
	mcpServer.AddTool(mcp.NewTool("submit_bid"), count("submit_bid"))
	fake := httptest.NewServer(server.NewStreamableHTTPServer(mcpServer))
	defer fake.Close()
	callCount := func(name string) int {
		mu.Lock()
		defer mu.Unlock()
		return calls[name]
	}

	// The real workspace DB handlers.
	ws := gin.New()
	ws.POST("/api/query", wshandlers.QueryWorkflowDB)
	ws.POST("/api/mutate", wshandlers.MutateWorkflowDB)
	wsServer := httptest.NewServer(ws)
	defer wsServer.Close()

	// The test run, set up by the controller exactly as execute_step does.
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, workflow,
		0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath(workflow)
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
	hcpo.selectedRunFolder = "test-1/default"
	run, err := hcpo.beginTestRun(context.Background(), "test-1", "iteration-0/default")
	if err != nil {
		t.Fatal(err)
	}
	testSession := "sub-exec-bid-submit-test"
	hcpo.configureSubAgentSessionGuard(testSession, "exec", "bid-submit", nil,
		[]string{workflow + "/runs/test-1/default/execution/bid-submit", workflow + "/learnings", workflow + "/db"})
	configureWorkflowDBSession(testSession, workflow, DBAccessReadWrite, false)
	realSession := "sub-exec-bid-submit-real"
	common.SetSessionFolderGuard(realSession, nil, []string{workflow + "/runs/iteration-0/default/execution/bid-submit"})
	configureWorkflowDBSession(realSession, workflow, DBAccessReadWrite, false)
	defer common.ClearSessionShellConfig(testSession)
	defer common.ClearSessionShellConfig(realSession)

	// Tools as a step session has them: the real DB tools and a browser whose
	// executor records which commands reached it.
	var browserMu sync.Mutex
	var browserCommands []string
	dbTools := virtualtools.CreateWorkflowDBToolRegistry(wsServer.URL, "", "")
	tools := map[string]func(context.Context, map[string]interface{}) (string, error){
		"query_workflow_db":  dbTools.Executors["query_workflow_db"],
		"mutate_workflow_db": dbTools.Executors["mutate_workflow_db"],
		"agent_browser": func(_ context.Context, args map[string]interface{}) (string, error) {
			browserMu.Lock()
			defer browserMu.Unlock()
			command, _ := args["command"].(string)
			browserCommands = append(browserCommands, command)
			return "ok", nil
		},
	}
	for _, sid := range []string{testSession, realSession} {
		codeexec.InitRegistryForSession(sid, tools, loggerv2.NewNoop())
	}

	handlers := executor.NewExecutorHandlers("", loggerv2.NewNoop())
	handlers.SetMCPServerResolver(func(_ context.Context, sessionID, serverName, _ string) (*executor.ResolvedMCPServer, error) {
		return &executor.ResolvedMCPServer{Name: serverName, ConnectionSessionID: sessionID,
			Config: mcpclient.MCPServerConfig{Protocol: mcpclient.ProtocolHTTP, URL: fake.URL}}, nil
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mcp/execute", handlers.HandleMCPExecute)
	// As the server's /tools/custom routes do: the trusted session travels in
	// X-Session-ID and the request context.
	mux.HandleFunc("/api/custom/execute", func(w http.ResponseWriter, r *http.Request) {
		sid := r.Header.Get("X-Session-ID")
		handlers.HandleCustomExecute(w, r.WithContext(context.WithValue(r.Context(), common.ChatSessionIDKey, sid)))
	})
	bridge := httptest.NewServer(mux)
	defer bridge.Close()
	post := func(path string, body map[string]interface{}) map[string]interface{} {
		t.Helper()
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, bridge.URL+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if sid, _ := body["session_id"].(string); sid != "" {
			req.Header.Set("X-Session-ID", sid)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	mcpCall := func(session, tool string) map[string]interface{} {
		return post("/api/mcp/execute", map[string]interface{}{"server": "fake-upwork", "tool": tool, "args": map[string]interface{}{"job_id": "job-2"}, "session_id": session})
	}
	customCall := func(session, tool string, args map[string]interface{}) map[string]interface{} {
		return post("/api/custom/execute", map[string]interface{}{"tool": tool, "args": args, "session_id": session})
	}
	claim := map[string]interface{}{"sql": "INSERT INTO proposals (job_id, status) VALUES ('job-2', 'claimed')"}
	countRows := func(path string) int {
		t.Helper()
		d, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM proposals`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// --- The test session.
	if out := mcpCall(testSession, "read_job"); out["success"] != true || !strings.Contains(out["result"].(string), "read_job done") {
		t.Fatalf("a read-only MCP tool must run in test mode: %v", out)
	}
	if out := mcpCall(testSession, "submit_bid"); !strings.Contains(out["result"].(string), "TEST MODE") {
		t.Fatalf("a write MCP tool must be stubbed in test mode: %v", out)
	}
	if out := customCall(testSession, "agent_browser", map[string]interface{}{"command": "snapshot"}); out["result"] != "ok" {
		t.Fatalf("browser snapshot must run in test mode: %v", out)
	}
	if out := customCall(testSession, "agent_browser", map[string]interface{}{"command": "click", "args": []interface{}{"@submit"}}); !strings.Contains(out["result"].(string), "TEST MODE") {
		t.Fatalf("browser click must be stubbed in test mode: %v", out)
	}
	if out := customCall(testSession, "mutate_workflow_db", claim); out["success"] != true {
		t.Fatalf("a DB write in test mode goes to the copy: %v", out)
	}
	if out := customCall(testSession, "query_workflow_db", map[string]interface{}{"sql": "SELECT status FROM proposals WHERE job_id = 'job-2'"}); !strings.Contains(out["result"].(string), "claimed") {
		t.Fatalf("the test session reads its own write from the copy: %v", out)
	}
	if out := customCall(testSession, "notify_user", map[string]interface{}{"message": "bid sent"}); !strings.Contains(out["result"].(string), "TEST MODE") {
		t.Fatalf("a tool not on the test-mode list must be stubbed: %v", out)
	}

	if got := callCount("submit_bid"); got != 0 {
		t.Fatalf("the external system saw %d submit_bid calls from a test run", got)
	}
	if got := callCount("read_job"); got != 1 {
		t.Fatalf("read_job reached the server %d times, want 1", got)
	}
	browserMu.Lock()
	if strings.Join(browserCommands, ",") != "snapshot" {
		t.Fatalf("browser executor saw %v, want only the snapshot", browserCommands)
	}
	browserMu.Unlock()
	if n := countRows(realDB); n != 1 {
		t.Fatalf("the real DB has %d proposals after the test run, want 1", n)
	}
	if n := countRows(run.DBAbsPath); n != 2 {
		t.Fatalf("the test DB copy has %d proposals, want 2", n)
	}
	if _, err := os.Stat(filepath.Join(docs, workflow, "runs", "test-1", "default", "execution", "pick-job", "picked.json")); err != nil {
		t.Fatalf("upstream output was not copied into the test run: %v", err)
	}
	cfg := common.GetSessionShellConfig(testSession)
	for _, denied := range []string{workflow + "/db", workflow + "/learnings", workflow + "/runs/iteration-0"} {
		if !slices.Contains(cfg.BlockedWritePaths, denied) {
			t.Fatalf("test session may write %s: blocked=%v", denied, cfg.BlockedWritePaths)
		}
	}
	if cfg.Env[testmode.EnvFlag] != "1" {
		t.Fatalf("test session shell lacks %s", testmode.EnvFlag)
	}

	hcpo.endTestRun(run, "bid-submit", nil)
	record, err := os.ReadFile(filepath.Join(docs, workflow, "runs", "test-1", "test_mode.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"tool": "submit_bid"`, `"tool": "agent_browser"`, `"tool": "notify_user"`, `"tool": "mutate_workflow_db"`, `"decision": "redirected"`} {
		if !strings.Contains(string(record), want) {
			t.Fatalf("test_mode.json lacks %s:\n%s", want, record)
		}
	}
	if testmode.ForSession(testSession) != nil {
		t.Fatal("the test session must leave test mode when the run ends")
	}

	// --- The same calls from a normal session reach the real systems.
	if out := mcpCall(realSession, "submit_bid"); out["success"] != true {
		t.Fatalf("normal submit_bid: %v", out)
	}
	if got := callCount("submit_bid"); got != 1 {
		t.Fatalf("a normal session's submit_bid reached the server %d times, want 1", got)
	}
	if out := customCall(realSession, "mutate_workflow_db", claim); out["success"] != true {
		t.Fatalf("normal DB write: %v", out)
	}
	if n := countRows(realDB); n != 2 {
		t.Fatalf("the real DB has %d proposals after a normal write, want 2", n)
	}
}

func mustMkdir(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
