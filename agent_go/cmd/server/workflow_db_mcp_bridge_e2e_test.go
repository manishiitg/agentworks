package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
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

// TestWorkflowStepDatabaseToolsThroughMCPBridge exercises the production
// workflow-step transport seam end to end:
//
//	MCP stdio bridge -> custom tool executor -> workspace DB HTTP handler -> SQLite.
//
// It proves a WAL-resident committed row is queryable and an explicitly
// authorized mutation reaches the live workflow database. A direct executor
// query also supplies the canonical payload for transport-equivalence checks.
// The custom HTTP facade below models the executor service's envelope.
func TestWorkflowStepDatabaseToolsThroughMCPBridge(t *testing.T) {
	root := t.TempDir()
	workspacePath := "Workflow/db-bridge-e2e"
	dbDir := filepath.Join(root, filepath.FromSlash(workspacePath), "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "db.sqlite")
	live, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: dbPath}).String()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	live.SetMaxOpenConns(1)
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA wal_autocheckpoint=0",
		"CREATE TABLE facts (id INTEGER PRIMARY KEY, value TEXT NOT NULL)",
		"INSERT INTO facts(value) VALUES ('committed-in-wal')",
	} {
		if _, err := live.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if _, err := os.Stat(dbPath + "-wal"); err != nil {
		t.Fatalf("expected WAL sidecar before bridge query: %v", err)
	}

	previousDocsDir := viper.Get("docs-dir")
	viper.Set("docs-dir", root)
	t.Cleanup(func() { viper.Set("docs-dir", previousDocsDir) })
	gin.SetMode(gin.TestMode)
	workspaceRouter := gin.New()
	workspaceRouter.POST("/api/query", workspacehandlers.QueryWorkflowDB)
	workspaceRouter.POST("/api/mutate", workspacehandlers.MutateWorkflowDB)
	workspaceAPI := httptest.NewServer(workspaceRouter)
	defer workspaceAPI.Close()

	const sessionID = "workflow-step-db-bridge-session"
	common.SetSessionFolderGuard(sessionID, []string{workspacePath}, []string{workspacePath + "/db"})
	common.SetSessionShellEnv(sessionID, map[string]string{"WORKFLOW_DB_ACCESS": "read-write"})
	defer common.ClearSessionShellConfig(sessionID)

	registry := virtualtools.CreateWorkflowDBToolRegistry(workspaceAPI.URL, "", sessionID)
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
			"name": "query_workflow_db", "description": "Query the current workflow database.", "type": "custom",
			"input_schema": map[string]any{
				"type": "object", "properties": map[string]any{
					"action": map[string]any{"type": "string"}, "sql": map[string]any{"type": "string"},
				}, "required": []string{"action"},
			},
		},
		{
			"name": "mutate_workflow_db", "description": "Mutate the current workflow database.", "type": "custom",
			"input_schema": map[string]any{
				"type": "object", "properties": map[string]any{
					"statements": map[string]any{"type": "array"},
				}, "required": []string{"statements"},
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
		ClientInfo:      mcp.Implementation{Name: "workflow-db-e2e", Version: "1"},
	}}); err != nil {
		t.Fatalf("initialize mcpbridge: %v", err)
	}

	query := mcp.CallToolRequest{}
	query.Params.Name = "query_workflow_db"
	query.Params.Arguments = map[string]any{"action": "query", "sql": "SELECT id, value FROM facts ORDER BY id"}
	queryResult, err := bridge.CallTool(ctx, query)
	if err != nil {
		t.Fatalf("query through bridge: %v", err)
	}
	if !strings.Contains(fmt.Sprint(queryResult.Content), "committed-in-wal") {
		t.Fatalf("bridge query did not see committed WAL row: %#v", queryResult.Content)
	}
	// Pin the exact payload, not just a substring that would also pass for a
	// double-encoded transport envelope. Raw HTTP has an envelope; native and
	// MCP callers receive the same decoded tool result.
	decode := func(value string) map[string]any {
		t.Helper()
		var payload map[string]any
		if err := json.Unmarshal([]byte(value), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["columns"] == nil || payload["rows"] == nil || payload["success"] != nil {
			t.Fatalf("not a canonical DB payload: %s", value)
		}
		return payload
	}
	arguments := map[string]any{"action": "query", "sql": "SELECT id, value FROM facts ORDER BY id"}
	native, err := registry.Executors["query_workflow_db"](context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID), arguments)
	if err != nil {
		t.Fatal(err)
	}
	wantPayload := decode(native)
	if len(queryResult.Content) != 1 {
		t.Fatalf("unexpected MCP content: %#v", queryResult.Content)
	}
	content, ok := queryResult.Content[0].(mcp.TextContent)
	if !ok || !reflect.DeepEqual(decode(content.Text), wantPayload) {
		t.Fatalf("MCP result differs from native: %#v", queryResult.Content)
	}
	body, _ := json.Marshal(arguments)
	request, err := http.NewRequest(http.MethodPost, customAPI.URL+"/tools/custom/query_workflow_db", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Session-ID", sessionID)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Success bool   `json:"success"`
		Result  string `json:"result"`
		Error   string `json:"error"`
	}
	err = json.NewDecoder(response.Body).Decode(&envelope)
	response.Body.Close()
	if err != nil || !envelope.Success || envelope.Error != "" {
		t.Fatalf("HTTP envelope: %+v, %v", envelope, err)
	}
	if !reflect.DeepEqual(decode(envelope.Result), wantPayload) {
		t.Fatalf("decoded HTTP result differs: %+v", envelope)
	}

	mutation := mcp.CallToolRequest{}
	mutation.Params.Name = "mutate_workflow_db"
	mutation.Params.Arguments = map[string]any{"statements": []any{
		map[string]any{
			"sql":    "WITH incoming(value) AS (VALUES (?)) INSERT INTO facts(value) SELECT value FROM incoming",
			"params": []any{"written-through-bridge"},
		},
	}}
	mutationResult, err := bridge.CallTool(ctx, mutation)
	if err != nil {
		t.Fatalf("mutation through bridge: %v", err)
	}
	if !strings.Contains(fmt.Sprint(mutationResult.Content), "rows_affected") {
		t.Fatalf("bridge mutation missing receipt: %#v", mutationResult.Content)
	}

	var count int
	if err := live.QueryRow("SELECT COUNT(*) FROM facts WHERE value='written-through-bridge'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("live workflow DB row count=%d, want 1", count)
	}
}
