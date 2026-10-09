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

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
	_ "modernc.org/sqlite"
)

// Both app sessions and scoped previews reach the real workspace HTTP handler
// and SQLite. Quoted input remains a binding; validation prepares without it.
func TestDashboardQueryBindingsRealWorkspace(t *testing.T) {
	docs := t.TempDir()
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","admin":true,"can_create":true,"products":["work"]}]}`)
	stubCrewLookups(t, map[string]string{"Crew/dashboard": "owner"}, nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/query", workspacehandlers.QueryWorkflowDB)
	server := httptest.NewServer(router)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	const value = "a' OR 1=1 --"
	for _, root := range []string{"Workflow/dashboard", "Workflow/relay-dashboard", "Crew/dashboard", "_users/owner/Chats/Code/projects/dashboard", "_users/crew-owner/Chats/Work/projects/dashboard"} {
		dbPath := filepath.Join(docs, root, "db/db.sqlite")
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("CREATE TABLE items(name TEXT); INSERT INTO items VALUES (?), ('other')", value); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		for _, scope := range []string{"", reportPreviewScope} {
			claims := &UserClaims{UserID: "owner", Scope: scope}
			if scope != "" {
				claims.ScopeWorkspace = root
			}
			body, _ := json.Marshal(map[string]any{"workspace": root, "sql": "SELECT name FROM items WHERE name = ?", "params": []any{value}})
			r := httptest.NewRequest("POST", reportPreviewAPIPrefix+"query", strings.NewReader(string(body)))
			r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
			w := httptest.NewRecorder()
			(&StreamingAPI{}).handleReportPreviewQuery(w, r)
			var out struct {
				Success bool                            `json:"success"`
				Data    workspace.QueryWorkflowDBResult `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK || !out.Success || len(out.Data.Rows) != 1 || out.Data.Rows[0]["name"] != value {
				t.Fatalf("%s scope %q: %d %s (%v)", root, scope, w.Code, w.Body.String(), err)
			}
		}
		client := (&StreamingAPI{}).reportPreviewWorkspaceClient(context.Background(), &UserClaims{UserID: "owner"}, root)
		for query, valid := range map[string]bool{
			"EXPLAIN SELECT name FROM items WHERE name = ?":    true,
			"EXPLAIN SELECT missing FROM items WHERE name = ?": false,
			"DELETE FROM items WHERE name = ?":                 false,
		} {
			_, err := client.QueryAuthorizedWorkflowDB(context.Background(), workspace.QueryWorkflowDBParams{DBPath: root + "/db/db.sqlite", SQL: query, PrepareOnly: true})
			if (err == nil) != valid {
				t.Fatalf("prepare %q: %v", query, err)
			}
		}
	}
}
