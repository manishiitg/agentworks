package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/viper"
	_ "modernc.org/sqlite"
)

// TestApplyWorkflowDBMigrationThroughMCPBridge exercises the same production
// seam as TestWorkflowStepDatabaseToolsThroughMCPBridge, but for the
// apply_workflow_db_migration tool: an agent authors a migration file with
// ordinary file access (simulated here with a direct write, since file
// authoring is not this tool's job), then applies it through the bridge, and
// the resulting table must be both queryable through the same session and
// visible to a fresh direct connection to the live database.
func TestApplyWorkflowDBMigrationThroughMCPBridge(t *testing.T) {
	root := t.TempDir()
	const userID = "migration-user"
	workspacePath := "Chats/Work/projects/db-migration-e2e"
	physicalWorkspacePath := filepath.ToSlash(filepath.Join("_users", userID, workspacePath))
	dbDir := filepath.Join(root, filepath.FromSlash(physicalWorkspacePath), "db")
	migrationsDir := filepath.Join(dbDir, "migrations")
	if err := os.MkdirAll(migrationsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "db.sqlite")
	live, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: dbPath}).String()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	live.SetMaxOpenConns(1)
	if _, err := live.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}

	migrationSQL := `BEGIN IMMEDIATE;

CREATE TABLE IF NOT EXISTS action_outcome_bindings (
  binding_id TEXT PRIMARY KEY,
  outcome_name TEXT NOT NULL,
  outcome_status TEXT NOT NULL DEFAULT 'pending'
);

CREATE INDEX IF NOT EXISTS idx_action_outcome_status
  ON action_outcome_bindings(outcome_status);

COMMIT;
`
	migrationFile := "2026-08-06-action-outcome-measurement.sql"
	if err := os.WriteFile(filepath.Join(migrationsDir, migrationFile), []byte(migrationSQL), 0o644); err != nil {
		t.Fatal(err)
	}

	var tableExistsBefore int
	if err := live.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='action_outcome_bindings'").Scan(&tableExistsBefore); err != nil {
		t.Fatal(err)
	}
	if tableExistsBefore != 0 {
		t.Fatalf("table already exists before migration; test setup is invalid")
	}

	previousDocsDir := viper.Get("docs-dir")
	viper.Set("docs-dir", root)
	t.Cleanup(func() { viper.Set("docs-dir", previousDocsDir) })
	gin.SetMode(gin.TestMode)
	workspaceRouter := gin.New()
	workspaceRouter.POST("/api/query", workspacehandlers.QueryWorkflowDB)
	workspaceRouter.POST("/api/mutate", workspacehandlers.MutateWorkflowDB)
	workspaceRouter.POST("/api/db/initialize", workspacehandlers.InitializeWorkflowDB)
	workspaceRouter.Any("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	workspaceAPI := httptest.NewServer(workspaceRouter)
	defer workspaceAPI.Close()

	const sessionID = "workflow-step-db-migration-bridge-session"
	common.SetSessionFolderGuard(sessionID, []string{physicalWorkspacePath}, []string{physicalWorkspacePath + "/db"})
	common.SetSessionShellEnv(sessionID, map[string]string{"WORKFLOW_DB_ACCESS": "read-write"})
	defer common.ClearSessionShellConfig(sessionID)

	registry := virtualtools.CreateWorkflowDBToolRegistry(workspaceAPI.URL, userID, sessionID)
	customAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/tools/custom/"
		name := strings.TrimPrefix(r.URL.Path, prefix)
		executor := registry.Executors[name]
		if !strings.HasPrefix(r.URL.Path, prefix) || executor == nil {
			http.Error(w, "unknown tool", http.StatusNotFound)
			return
		}
		var args map[string]any
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		execCtx := context.WithValue(r.Context(), common.ChatSessionIDKey, r.Header.Get("X-Session-ID"))
		result, execErr := executor(execCtx, args)
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"success": execErr == nil, "result": result}
		if execErr != nil {
			response["error"] = execErr.Error()
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer customAPI.Close()

	toolSchemas := []map[string]any{
		{
			"name": "apply_workflow_db_migration", "description": "Apply a workflow DB migration.", "type": "custom",
			"input_schema": map[string]any{
				"type": "object", "properties": map[string]any{
					"migration_file": map[string]any{"type": "string"},
				}, "required": []string{"migration_file"},
			},
		},
		{
			"name": "query_workflow_db", "description": "Query the current workflow database.", "type": "custom",
			"input_schema": map[string]any{
				"type": "object", "properties": map[string]any{
					"action": map[string]any{"type": "string"}, "sql": map[string]any{"type": "string"},
				}, "required": []string{"action"},
			},
		},
	}
	toolsJSON, err := json.Marshal(toolSchemas)
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := client.NewStdioMCPClient(buildPulseTestMCPBridge(t), append(os.Environ(),
		"MCP_API_URL="+customAPI.URL,
		"MCP_API_TOKEN=bridge-test-token",
		"MCP_SESSION_ID="+sessionID,
		"MCP_TOOLS_FILE="+writeMCPToolsFileForTest(t, toolsJSON),
	))
	if err != nil {
		t.Fatalf("start mcpbridge: %v", err)
	}
	defer bridge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := bridge.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "workflow-db-migration-e2e", Version: "1"},
	}}); err != nil {
		t.Fatalf("initialize mcpbridge: %v", err)
	}

	migrate := mcp.CallToolRequest{}
	migrate.Params.Name = "apply_workflow_db_migration"
	migrate.Params.Arguments = map[string]any{"migration_file": migrationFile}
	migrateResult, err := bridge.CallTool(ctx, migrate)
	if err != nil {
		t.Fatalf("migrate through bridge: %v", err)
	}
	if !strings.Contains(fmt.Sprint(migrateResult.Content), "statements_applied") {
		t.Fatalf("bridge migration missing receipt: %#v", migrateResult.Content)
	}

	// The table must be visible to a fresh direct connection to the live
	// database, not merely to the connection the handler used.
	var tableExistsAfter int
	if err := live.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='action_outcome_bindings'").Scan(&tableExistsAfter); err != nil {
		t.Fatal(err)
	}
	if tableExistsAfter != 1 {
		t.Fatalf("action_outcome_bindings not created by migration")
	}

	// The durable ledger must have recorded the real source filename, threaded
	// end to end from the tool call through the HTTP request to the
	// transaction that applied it -- not just when called directly in a unit
	// test.
	var ledgerFile string
	if err := live.QueryRow("SELECT migration_file FROM schema_migration_log ORDER BY id DESC LIMIT 1").Scan(&ledgerFile); err != nil {
		t.Fatalf("ledger row missing after bridge migration: %v", err)
	}
	if ledgerFile != migrationFile {
		t.Fatalf("ledger migration_file = %q, want %q", ledgerFile, migrationFile)
	}

	// Re-applying the same migration must be a safe no-op, not an error.
	reapply, err := bridge.CallTool(ctx, migrate)
	if err != nil {
		t.Fatalf("reapply through bridge: %v", err)
	}
	if !strings.Contains(fmt.Sprint(reapply.Content), "statements_applied") {
		t.Fatalf("bridge migration reapply missing receipt: %#v", reapply.Content)
	}

	query := mcp.CallToolRequest{}
	query.Params.Name = "query_workflow_db"
	query.Params.Arguments = map[string]any{"action": "describe", "table": "action_outcome_bindings"}
	queryResult, err := bridge.CallTool(ctx, query)
	if err != nil {
		t.Fatalf("describe through bridge: %v", err)
	}
	if !strings.Contains(fmt.Sprint(queryResult.Content), "outcome_status") {
		t.Fatalf("describe did not see migrated columns: %#v", queryResult.Content)
	}
}

// TestApplyWorkflowDBMigrationExecutorDeniesReadOnlySession mirrors the
// mutate_workflow_db read-only denial: schema-migration authority is gated by
// the same explicit db_access=read-write trust boundary, not a separate role.
func TestApplyWorkflowDBMigrationExecutorDeniesReadOnlySession(t *testing.T) {
	sessionID := "workflow-db-migration-read-only"
	defer common.ClearSessionShellConfig(sessionID)
	common.SetSessionFolderGuard(sessionID, []string{"Workflow/demo/db"}, nil)
	common.SetSessionShellEnv(sessionID, map[string]string{"WORKFLOW_DB_ACCESS": "read"})

	registry := virtualtools.CreateWorkflowDBToolRegistry("http://127.0.0.1:1", "", sessionID)
	_, err := registry.Executors["apply_workflow_db_migration"](context.Background(), map[string]any{
		"migration_file": "2026-08-06-action-outcome-measurement.sql",
	})
	if err == nil || !strings.Contains(err.Error(), "migration denied") {
		t.Fatalf("read-only migration error=%v", err)
	}
}

// TestApplyWorkflowDBMigrationExecutorRejectsPathTraversal proves the model
// cannot escape db/migrations/ by naming a file with a path separator, even
// though db_access is otherwise read-write.
func TestApplyWorkflowDBMigrationExecutorRejectsPathTraversal(t *testing.T) {
	sessionID := "workflow-db-migration-traversal"
	defer common.ClearSessionShellConfig(sessionID)
	common.SetSessionFolderGuard(sessionID, []string{"Workflow/demo/db"}, []string{"Workflow/demo/db"})
	common.SetSessionShellEnv(sessionID, map[string]string{"WORKFLOW_DB_ACCESS": "read-write"})

	registry := virtualtools.CreateWorkflowDBToolRegistry("http://127.0.0.1:1", "", sessionID)
	for _, filename := range []string{"../../etc/passwd", "sub/dir.sql", "../escape.sql", "no-suffix"} {
		_, err := registry.Executors["apply_workflow_db_migration"](context.Background(), map[string]any{
			"migration_file": filename,
		})
		if err == nil || !strings.Contains(err.Error(), "bare filename") {
			t.Fatalf("filename %q: error=%v, want a bare-filename rejection", filename, err)
		}
	}
}
