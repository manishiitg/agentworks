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
	f.write(t, externalCommandCrewRoot+"/db/migrations/2026-10-11-example.sql", "CREATE TABLE IF NOT EXISTS example (id INTEGER PRIMARY KEY, value TEXT);")
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
		result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "crew_execution", "arguments": map[string]any{"crew_id": "team", "action": action, "arguments": params}})
		requireRemoteSuccess(t, result, action)
	}
	call("apply_workflow_db_migration", map[string]any{"migration_file": "2026-10-11-example.sql"})
	call("mutate_workflow_db", map[string]any{"sql": "INSERT INTO example (id,value) VALUES (?,?)", "params": []any{1, "saved via MCP"}})
	call("create_workflow_database_snapshot", map[string]any{})
	live, err := sql.Open("sqlite", filepath.Join(f.docs, externalCommandCrewRoot, "db", "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	var value string
	if err = live.QueryRow("SELECT value FROM example WHERE id=1").Scan(&value); err != nil || value != "saved via MCP" {
		t.Fatalf("disk value=%q err=%v", value, err)
	}
	guarded := crewWriter("owner")
	guarded.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"code"}, WritePaths: []string{"code"}, ReadOnlyPaths: []string{"code/locked"}, BlockedPaths: []string{"code/private"}}
	crewCapabilityHTTP(t, f, guarded, "crew_execution", map[string]any{"action": "execute_shell_command", "arguments": map[string]any{"command": "pwd"}}, 200)
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
	history := crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_execution", map[string]any{"action": "history"}, 200)
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
	history = crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_execution", map[string]any{"action": "history"}, 200)
	if !strings.Contains(history, "previous-owner") || strings.Contains(history, "expired") {
		t.Fatal(history)
	}
	call("mutate_workflow_db", map[string]any{"sql": "UPDATE example SET value=? WHERE id=?", "params": []any{"saved via MCP", 1}})
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

	crewCapabilityHTTP(t, f, crewWriter("other"), "crew_execution", map[string]any{"action": "history"}, 403)
	for _, denied := range []*UserClaims{crewReader("owner"), crewWriter("other")} {
		crewCapabilityHTTP(t, f, denied, "crew_execution", map[string]any{"action": "mutate_workflow_db", "arguments": map[string]any{"sql": "DELETE FROM example"}}, 403)
	}
	crewCapabilityHTTP(t, f, guarded, "crew_execution", map[string]any{"action": "mutate_workflow_db", "arguments": map[string]any{"sql": "DELETE FROM example"}}, 403)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_execution", map[string]any{"action": "execute_shell_command", "arguments": map[string]any{"command": "pwd", "working_directory": "Workflow/other"}}, 400)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_execution", map[string]any{"action": "apply_workflow_db_migration", "arguments": map[string]any{"migration_file": "../other.sql"}}, 400)
	bounded := crewWriter("owner")
	bounded.AccessToken.AllCrews, bounded.AccessToken.CrewIDs = false, []string{"other"}
	crewCapabilityHTTP(t, f, bounded, "crew_execution", map[string]any{"action": "list"}, 403)
	list := crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_execution", map[string]any{"action": "list"}, 200)
	if !json.Valid([]byte(list)) || !strings.Contains(list, "inputSchema") || !strings.Contains(list, "agent_browser") {
		t.Fatal(list)
	}
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
	result := callRemoteTool(t, ctx, refClient, "call_tool", map[string]any{"name": "crew_execution", "arguments": map[string]any{"crew_id": "team", "action": "list_attached_workflows", "arguments": map[string]any{}}})
	requireRemoteSuccess(t, result, "attached workflow discovery")
	listed, _ := json.Marshal(result)
	if !strings.Contains(string(listed), "Workflow/visible") || strings.Contains(string(listed), "Workflow/hidden") {
		t.Fatalf("workflow token bounds lost: %s", listed)
	}
	crewCapabilityHTTP(t, f, refClaims, "crew_execution", map[string]any{"action": "run_workflow_trigger", "arguments": map[string]any{"workspace_path": "Workflow/visible"}}, 403)
	crewCapabilityHTTP(t, f, refClaims, "crew_execution", map[string]any{"action": "list_workflow_triggers", "arguments": map[string]any{"workspace_path": "Workflow/hidden"}}, 400)
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
	f.api.bgAgentRegistry.Register("another-person-session", &BackgroundAgent{ID: "private-agent", SessionID: "another-person-session", Name: "private background task", Status: BGAgentRunning})
	server := serveExternalMCP(t, f.api, crewWriter("owner"))
	cli := dialExternalMCP(t, ctx, server.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	for _, name := range []string{"list_ui_capabilities", "get_ui_state", "list_background_agents"} {
		result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "workspace", "arguments": map[string]any{"workspace_id": "work:team", "chat_id": "main", "action": "chat_tool", "tool_name": name, "arguments": map[string]any{}}})
		requireRemoteSuccess(t, result, name)
		raw, _ := json.Marshal(result)
		if strings.Contains(string(raw), "private background task") {
			t.Fatal("another session's background agent was exposed")
		}
		if name == "get_ui_state" && !strings.Contains(string(raw), "browser_disconnected") {
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
	f.api.bgAgentRegistry.Register(record.SessionID, &BackgroundAgent{ID: "own-agent", SessionID: record.SessionID, Name: "own task", Status: BGAgentRunning})
	owner := crewWriter("owner")
	owner.AccessToken.Scopes = append(owner.AccessToken.Scopes, "crews:run")
	call(owner, map[string]any{"action": "tool", "chat_id": "main", "tool_name": "query_agent", "arguments": map[string]any{"agent_id": "own-agent"}}, 200)
	call(owner, map[string]any{"action": "tool", "chat_id": "main", "tool_name": "terminate_agent", "arguments": map[string]any{"agent_id": "own-agent"}}, 200)
	audit, err := openCrewExecutionAudit()
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	var count int
	if err = audit.db.QueryRow(`SELECT COUNT(*) FROM external_crew_execution_audit WHERE crew_id='team' AND tool_group='crew_session' AND action IN ('query_agent','terminate_agent') AND status='completed'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("session actions not audited: %d, %v", count, err)
	}
	call(crewWriter("owner"), map[string]any{"action": "tool", "chat_id": "another-person-session", "tool_name": "list_background_agents"}, 404)
	call(crewWriter("owner"), map[string]any{"action": "tool", "chat_id": "main", "tool_name": "run_in_background", "arguments": map[string]any{"name": "task", "instruction": "do work"}}, 403)
	call(crewWriter("other"), map[string]any{"action": "tools", "chat_id": "own-session"}, 404)
	call(crewWriter("owner"), map[string]any{"action": "tool", "chat_id": "main", "tool_name": "get_ui_state", "arguments": map[string]any{"session_id": "another-person-session"}}, 400)
}
