package virtualtools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
	_ "modernc.org/sqlite"
)

// offset wraps a SELECT/WITH as a subquery; anything else has no rows to page.
func TestWorkflowDBPagedSQL(t *testing.T) {
	got, err := workflowDBPagedSQL("  SELECT id, v FROM facts ORDER BY id;  ", 1000)
	if err != nil || got != "SELECT * FROM (SELECT id, v FROM facts ORDER BY id) LIMIT -1 OFFSET 1000" {
		t.Fatalf("paged = %q, %v", got, err)
	}
	got, err = workflowDBPagedSQL("WITH recent AS (SELECT * FROM facts) SELECT * FROM recent ORDER BY id", 5)
	if err != nil || !strings.HasPrefix(got, "SELECT * FROM (WITH recent AS") || !strings.HasSuffix(got, "OFFSET 5") {
		t.Fatalf("a WITH statement must page too: %q, %v", got, err)
	}
	for _, notRows := range []string{"PRAGMA integrity_check", "EXPLAIN SELECT 1"} {
		if _, err := workflowDBPagedSQL(notRows, 10); err == nil {
			t.Errorf("%q must not accept an offset", notRows)
		}
	}
}

// The schemas advertise the new, larger limits and the new arguments.
func TestWorkflowDBToolSchemasAdvertiseBulkArguments(t *testing.T) {
	raw := func(tool any) string {
		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	query, mutate := raw(workflowDBQueryToolDefinition()), raw(workflowDBMutateToolDefinition())
	for _, want := range []string{`"offset"`, `"maximum":10000`, "next_offset"} {
		if !strings.Contains(query, want) {
			t.Errorf("query_workflow_db schema misses %s", want)
		}
	}
	for _, want := range []string{`"param_sets"`, `"maxItems":200`, "5000"} {
		if !strings.Contains(mutate, want) {
			t.Errorf("mutate_workflow_db schema misses %s", want)
		}
	}
	if strings.Contains(mutate, `"maxItems":20,`) || strings.Contains(mutate, "1-20 entries") {
		t.Error("the old statement cap of 20 is still advertised")
	}
}

// The real tool executors against the real workspace handlers over HTTP: a
// bulk insert with param_sets in one call, a rolled-back bad batch, and the
// rows read back page by page with offset/next_offset.
func TestWorkflowDBBulkWriteAndPagedReadEndToEnd(t *testing.T) {
	root := t.TempDir()
	const workspacePath = "Workflow/bulk"
	dbDir := filepath.Join(root, filepath.FromSlash(workspacePath), "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed, err := sql.Open("sqlite", filepath.Join(dbDir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec("CREATE TABLE facts (id INTEGER PRIMARY KEY, label TEXT UNIQUE, n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	_ = seed.Close()
	previous := viper.Get("docs-dir")
	viper.Set("docs-dir", root)
	t.Cleanup(func() { viper.Set("docs-dir", previous) })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/query", workspacehandlers.QueryWorkflowDB)
	router.POST("/api/mutate", workspacehandlers.MutateWorkflowDB)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	sessionID := "workflow-db-bulk-" + t.Name()
	common.SetSessionFolderGuard(sessionID, []string{workspacePath + "/db"}, []string{workspacePath + "/db"})
	common.SetSessionShellEnv(sessionID, map[string]string{workflowDBAccessEnv: "read-write"})
	t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })
	registry := CreateWorkflowDBToolRegistry(server.URL, "", sessionID)
	ctx := context.Background()

	sets := make([]any, 0, 3000)
	for i := 0; i < 3000; i++ {
		sets = append(sets, []any{fmt.Sprintf("row-%04d", i), i})
	}
	out, err := registry.Executors["mutate_workflow_db"](ctx, map[string]any{
		"sql": "INSERT INTO facts(label, n) VALUES (?, ?)", "param_sets": sets,
	})
	if err != nil || !strings.Contains(out, `"total_rows_affected":3000`) {
		t.Fatalf("bulk insert: %v %s", err, out)
	}
	// A duplicate label rolls the whole call back; the 3000 rows stay 3000.
	if _, err := registry.Executors["mutate_workflow_db"](ctx, map[string]any{
		"sql": "INSERT INTO facts(label, n) VALUES (?, ?)", "param_sets": []any{[]any{"fresh", 1}, []any{"row-0001", 2}},
	}); err == nil || !strings.Contains(err.Error(), "param_sets row 2") {
		t.Fatalf("a duplicate row must fail naming its row, got %v", err)
	}
	// Read back in pages of 1000 with a stable ORDER BY.
	seen, offset := 0, 0
	for page := 0; page < 5; page++ {
		args := map[string]any{"sql": "SELECT id, label FROM facts ORDER BY id", "max_rows": float64(1000)}
		if offset > 0 {
			args["offset"] = float64(offset)
		}
		raw, err := registry.Executors["query_workflow_db"](ctx, args)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		var result struct {
			Rows       []map[string]any `json:"rows"`
			Truncated  bool             `json:"truncated"`
			NextOffset int              `json:"next_offset"`
		}
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		seen += len(result.Rows)
		if !result.Truncated {
			break
		}
		if result.NextOffset != offset+1000 {
			t.Fatalf("page %d next_offset = %d, want %d", page, result.NextOffset, offset+1000)
		}
		offset = result.NextOffset
	}
	if seen != 3000 {
		t.Fatalf("paged %d rows, want exactly 3000 (no duplicates, none lost)", seen)
	}
	// A page request on a non-SELECT is refused with a clear message.
	if _, err := registry.Executors["query_workflow_db"](ctx, map[string]any{"sql": "PRAGMA integrity_check", "offset": float64(5)}); err == nil {
		t.Error("offset on a PRAGMA must be refused")
	}
}
