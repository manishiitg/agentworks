package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	_ "modernc.org/sqlite"
)

// The actual MCP transport, native registry and disk-backed database handlers
// share one Crew context. A shell transport probe separately checks the exact
// policy that reaches the existing kernel sandbox rather than mocking it in Go.
func TestExternalCrewExecutionOverMCP(t *testing.T) {
	f, upstream := newExternalCrewCommandFixture(t)
	t.Setenv("AUTH_SECRET", "crew-execution-test-signing-secret")
	// PLAT-746-448: conversion points the Crew at its existing workflow DB.
	if err := defaultProjectOwners().modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		record := projects["work/test-team"]
		record.DatabaseRoot = "workflows/saved/db"
		projects["work/test-team"] = record
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/api/query", workspacehandlers.QueryWorkflowDB)
	router.POST("/api/mutate", workspacehandlers.MutateWorkflowDB)
	router.POST("/api/db/initialize", workspacehandlers.InitializeWorkflowDB)
	router.POST("/api/db/backup-snapshot", workspacehandlers.CreateWorkflowDatabaseBackupSnapshot)
	parsed, _ := url.Parse(upstream)
	proxy := httputil.NewSingleHostReverseProxy(parsed)
	router.NoRoute(gin.WrapH(proxy))
	var shellPolicy workspace.ExecuteShellCommandParams
	var shellSession string
	router.POST("/api/execute", func(c *gin.Context) {
		if err := c.ShouldBindJSON(&shellPolicy); err != nil {
			t.Error(err)
			c.AbortWithStatus(400)
			return
		}
		shellSession = c.GetHeader("X-Session-ID")
		c.JSON(200, gin.H{"success": true, "data": gin.H{"stdout": "sandbox request accepted", "stderr": "", "exit_code": 0}})
	})
	server := httptest.NewServer(router)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mcp := serveExternalMCP(t, f.api, crewWriter("owner"))
	cli := dialExternalMCP(t, ctx, mcp.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	call := func(action string, params map[string]any) {
		t.Helper()
		args := map[string]any{"crew_id": "team", "action": action}
		for key, value := range params {
			args[key] = value
		}
		result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "database", "arguments": args})
		requireRemoteSuccess(t, result, action)
	}
	// Normal Crew creation initializes an empty database; SQL setup is a mutation.
	_, err := workspace.NewClient(server.URL, workspace.WithUserID("owner")).InitializeWorkflowDB(ctx, workspace.InitializeWorkflowDBParams{DBPath: externalCommandCrewRoot + "/db/db.sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	call("mutate", map[string]any{"statements": []any{
		map[string]any{"sql": "CREATE TABLE IF NOT EXISTS example (id INTEGER PRIMARY KEY, value TEXT)"},
		map[string]any{"sql": "CREATE INDEX IF NOT EXISTS example_value ON example(value)"},
	}})
	call("mutate", map[string]any{"sql": "CREATE TABLE IF NOT EXISTS example (id INTEGER PRIMARY KEY, value TEXT)"})
	call("mutate", map[string]any{"sql": "INSERT INTO example (id,value) VALUES (?,?)", "params": []any{1, "saved via MCP"}})
	call("snapshot", map[string]any{})
	// PLAT-746-428: an unconverted workflow bound to this Crew uses its
	// chosen database too. No second database or transport fallback is needed.
	legacy := crewExecutableBinding{ID: "legacy", CrewID: "team", CrewRoot: externalCommandCrewRoot, Root: "Workflow/legacy", Kind: "script", RuntimeRoot: "workflows/legacy"}
	if err := registerCrewExecutable(legacy); err != nil {
		t.Fatal(err)
	}
	const legacySession = "legacy-shared-database"
	common.SetSessionFolderGuard(legacySession, []string{legacy.Root}, []string{legacy.Root + "/db"})
	common.SetSessionShellEnv(legacySession, map[string]string{"DB_PATH": legacy.Root + "/db/db.sqlite", "WORKFLOW_DB_ACCESS": "read-write"})
	t.Cleanup(func() { common.ClearSessionShellConfig(legacySession) })
	legacyTools := virtualtools.CreateWorkflowDBToolRegistry(server.URL, "owner", legacySession)
	if _, err := legacyTools.Executors["mutate_workflow_db"](ctx, map[string]any{"sql": "INSERT INTO example(id,value) VALUES (?,?)", "params": []any{2, "same Crew database"}}); err != nil {
		t.Fatal(err)
	}
	legacyResult, err := legacyTools.Executors["query_workflow_db"](ctx, map[string]any{"sql": "SELECT value FROM example WHERE id=1"})
	if err != nil || !strings.Contains(legacyResult, "saved via MCP") {
		t.Fatalf("old workflow did not share Crew database: %s %v", legacyResult, err)
	}
	call("query", map[string]any{"sql": "SELECT value FROM example WHERE id=2"})

	live, err := sql.Open("sqlite", filepath.Join(f.docs, externalCommandCrewState, "workflows", "saved", "db", "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	var value string
	if err = live.QueryRow("SELECT value FROM example WHERE id=1").Scan(&value); err != nil || value != "saved via MCP" {
		t.Fatalf("disk value=%q err=%v", value, err)
	}
	if err = live.QueryRow("SELECT value FROM example WHERE id=2").Scan(&value); err != nil || value != "same Crew database" {
		t.Fatalf("old workflow opened a different database: %q %v", value, err)
	}
	// PLAT-825: the same real database is readable by a shared Run caller,
	// while its existing write and unrestricted-file rules remain unchanged.
	shared := crewReader("other")
	shared.AccessToken.Scopes = append(shared.AccessToken.Scopes, "crews:run")
	read := crewCapabilityHTTP(t, f, shared, "database", map[string]any{"action": "query", "sql": "SELECT id,value FROM example"}, 200)
	if !strings.Contains(read, "saved via MCP") {
		t.Fatalf("shared database read lost its row: %s", read)
	}
	crewCapabilityHTTP(t, f, shared, "database", map[string]any{"action": "mutate", "sql": "DELETE FROM example"}, 403)
	guarded := crewWriter("owner")
	guarded.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"code"}, WritePaths: []string{"code"}, ReadOnlyPaths: []string{"code/locked"}, BlockedPaths: []string{"code/private"}}
	crewCapabilityHTTP(t, f, guarded, "terminal", map[string]any{"action": "execute", "command": "pwd"}, 200)
	guard := shellPolicy.FolderGuard
	if guard == nil || !guard.Enabled || !guard.StrictAllowlist || shellPolicy.WorkingDirectory != externalCommandCrewRoot || shellPolicy.Timeout == nil || *shellPolicy.Timeout != 25 {
		t.Fatalf("wrong shell policy: %+v", shellPolicy)
	}
	if strings.Join(guard.WritePaths, ",") != externalCommandCrewRoot+"/code" || !containsExactString(guard.BlockedWritePaths, externalCommandCrewRoot+"/code/locked") || !containsExactString(guard.BlockedPaths, externalCommandCrewRoot+"/code/private") {
		t.Fatalf("connection guard lost: %+v", guard)
	}
	if shellSession != "" && common.GetSessionShellConfig(shellSession) != nil {
		t.Fatal("temporary shell policy leaked")
	}
	history := crewCapabilityHTTP(t, f, crewWriter("owner"), "terminal", map[string]any{"action": "history"}, 200)
	if !strings.Contains(history, "mutate_workflow_db") || !strings.Contains(history, "connection_id") || strings.Contains(history, "saved via MCP") || strings.Contains(history, "INSERT INTO") {
		t.Fatalf("audit missing or exposed data: %s", history)
	}
	// History belongs to the project, not the previous owner's user identity.
	audit, oldID, err := startCrewExecutionAudit(ctx, "previous-owner", "team", "old-connection", "crew_execution", "execute_shell_command", map[string]any{"command": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err = finishCrewExecutionAudit(audit, oldID, `{"exit_code":0}`, nil); err != nil {
		t.Fatal(err)
	}
	_, err = audit.db.Exec(`INSERT INTO external_crew_execution_audit (id,user_id,crew_id,connection_id,tool_group,action,operation,started_at,status) VALUES ('expired','old','team','old','crew_execution','execute_shell_command','redacted',?,'completed')`, time.Now().Add(-31*24*time.Hour).UnixMilli())
	audit.Close()
	if err != nil {
		t.Fatal(err)
	}
	history = crewCapabilityHTTP(t, f, crewWriter("owner"), "terminal", map[string]any{"action": "history"}, 200)
	if !strings.Contains(history, "previous-owner") || strings.Contains(history, "expired") {
		t.Fatal(history)
	}
	call("mutate", map[string]any{"sql": "UPDATE example SET value=? WHERE id=?", "params": []any{"saved via MCP", 1}})
	audit, err = openCrewExecutionAudit()
	if err != nil {
		t.Fatal(err)
	}
	var expired int
	err = audit.db.QueryRow(`SELECT COUNT(*) FROM external_crew_execution_audit WHERE id='expired'`).Scan(&expired)
	audit.Close()
	if err != nil || expired != 0 {
		t.Fatalf("expired records=%d err=%v", expired, err)
	}
	first, err := lockCrewExecution(ctx, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := lockCrewExecution(ctx, "session-b")
	if err != nil {
		t.Fatal(err)
	}
	other()
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if release, err := lockCrewExecution(cancelled, "session-a"); err == nil {
		release()
		t.Fatal("cancelled waiter acquired active session")
	}
	first()
	crewExecutionLocks.Lock()
	left := len(crewExecutionLocks.entries)
	crewExecutionLocks.Unlock()
	if left != 0 {
		t.Fatalf("lock entries leaked: %d", left)
	}

	crewCapabilityHTTP(t, f, crewWriter("other"), "terminal", map[string]any{"action": "history"}, 403)
	for _, denied := range []*UserClaims{crewReader("owner"), crewWriter("other")} {
		crewCapabilityHTTP(t, f, denied, "database", map[string]any{"action": "mutate", "sql": "DELETE FROM example"}, 403)
	}
	crewCapabilityHTTP(t, f, guarded, "database", map[string]any{"action": "mutate", "sql": "DELETE FROM example"}, 403)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "terminal", map[string]any{"action": "execute", "command": "pwd", "working_directory": "Workflow/other"}, 400)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "database", map[string]any{"action": "mutate", "sql": "DROP TABLE IF EXISTS example"}, 400)
	bounded := crewWriter("owner")
	bounded.AccessToken.AllCrews, bounded.AccessToken.CrewIDs = false, []string{"other"}
	crewCapabilityHTTP(t, f, bounded, "terminal", map[string]any{"action": "history"}, 403)
	// One name per job: the retired grouped name is refused, not aliased.
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_execution", map[string]any{"action": "list"}, 404)
	// Attached workflow dispatch keeps the external connection's target ceilings.
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work","agentworks"]}]}`)
	f.api.scheduler = NewSchedulerService(f.api)
	f.write(t, "Workflow/visible/workflow.json", `{"id":"visible","label":"Visible","created_by":"owner","access":{"owners":["owner"]}}`)
	f.write(t, "Workflow/hidden/workflow.json", `{"id":"hidden","label":"Hidden","created_by":"owner","access":{"owners":["owner"]}}`)
	f.write(t, externalCommandCrewRoot+"/workflow.json", `{"id":"team","created_by":"owner","workflow_context_paths":["Workflow/visible","Workflow/hidden"]}`)
	refClaims := crewWriter("owner")
	refClaims.AccessToken.Scopes = append(refClaims.AccessToken.Scopes, "workflows:read", "files:read")
	refClaims.AccessToken.WorkflowIDs = []string{"visible"}
	refClaims.AccessToken.AllWorkflows = false
	refServer := serveExternalMCP(t, f.api, refClaims)
	refClient := dialExternalMCP(t, ctx, refServer.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, refClient)
	result := callRemoteTool(t, ctx, refClient, "call_tool", map[string]any{"name": "workspace", "arguments": map[string]any{"crew_id": "team", "action": "references_list"}})
	requireRemoteSuccess(t, result, "attached workflow discovery")
	listed, _ := json.Marshal(result)
	if !strings.Contains(string(listed), "Workflow/visible") || strings.Contains(string(listed), "Workflow/hidden") {
		t.Fatalf("workflow token bounds lost: %s", listed)
	}
	crewCapabilityHTTP(t, f, refClaims, "functions", map[string]any{"action": "run_trigger", "workspace_path": "Workflow/visible"}, 403)
	crewCapabilityHTTP(t, f, refClaims, "functions", map[string]any{"action": "triggers", "workspace_path": "Workflow/hidden"}, 400)
}

// The native session registry, transport and presentation broker enforce the
// same per-person boundary; unavailable browsers never report a successful UI edit.
func TestExternalCrewSessionToolsOverMCP(t *testing.T) {
	t.Setenv("AUTH_SECRET", "crew-session-test-signing-secret")
	f, _ := newExternalCrewCommandFixture(t)
	f.api.bgAgentRegistry = NewBackgroundAgentRegistry()
	profile, err := f.api.agentProfiles.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	record, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, "owner", profile, productConversationBinding{ConversationKey: "team", WorkspacePath: externalCommandCrewRoot, ResourceID: "team", Title: "Test Team"}, "own-session")
	if err != nil {
		t.Fatal(err)
	}
	f.api.activeSessions = map[string]*ActiveSessionInfo{record.SessionID: {UserID: "owner"}}
	server := serveExternalMCP(t, f.api, crewWriter("owner"))
	cli := dialExternalMCP(t, ctx, server.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	// Session tools are grouped by family (ui, sessions); the family takes the old tool's name as its action.
	for _, tc := range []struct{ name, tool, action string }{{"capabilities", "ui", "capabilities"}, {"state", "ui", "get"}} {
		name := tc.name
		result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "workspace", "arguments": map[string]any{"workspace_id": "work:team", "chat_id": "main", "action": "chat_tool", "tool_name": tc.tool, "arguments": map[string]any{"action": tc.action}}})
		requireRemoteSuccess(t, result, name)
		raw, _ := json.Marshal(result)
		if name == "state" && !strings.Contains(string(raw), "browser_disconnected") {
			t.Fatalf("unattached browser result: %s", raw)
		}
	}
	call := func(claims *UserClaims, args map[string]any, want int) {
		t.Helper()
		args["crew_id"] = "team"
		req := httptest.NewRequest("POST", "/api/external/call", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
		rec := httptest.NewRecorder()
		f.api.externalCrewChatsCall(rec, req, args)
		if rec.Code != want {
			t.Fatalf("status %d want %d: %s", rec.Code, want, rec.Body)
		}
	}
	// The two MCP session-tool calls above are audited.
	audit, err := openCrewExecutionAudit()
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	var count int
	if err = audit.db.QueryRow(`SELECT COUNT(*) FROM external_crew_execution_audit WHERE crew_id='team' AND tool_group='crew_session' AND action IN ('ui.capabilities','ui.get') AND status='completed'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("session actions not audited: %d, %v", count, err)
	}
	call(crewWriter("owner"), map[string]any{"action": "tool", "chat_id": "another-person-session", "tool_name": "ui", "arguments": map[string]any{"action": "capabilities"}}, 404)
	call(crewWriter("other"), map[string]any{"action": "tools", "chat_id": "own-session"}, 404)
	call(crewWriter("owner"), map[string]any{"action": "tool", "chat_id": "main", "tool_name": "ui", "arguments": map[string]any{"action": "get", "session_id": "another-person-session"}}, 400)
}
