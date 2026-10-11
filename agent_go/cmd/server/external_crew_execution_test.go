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
}
