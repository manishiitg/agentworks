package admin

import (
	"bytes"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/policy"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestDatabaseSQLAPIEnforcesAuthAndUpdatesEffectiveAccess(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddUser(store.User{ID: "u", WorkspaceID: "w"})
	s.AddGroup(store.Group{ID: "g", WorkspaceID: "w", Name: "Readers"})
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	tool := s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "c__read", Fingerprint: "f"})
	s.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version)
	if err = s.EnableSQLWorkspace("w"); err != nil {
		t.Fatal(err)
	}
	a := &Admin{Store: s, WorkspaceID: "w", HumanToken: "secret"}
	mux := http.NewServeMux()
	a.databaseRoutes(mux)
	request := func(path, token string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-CapLayer-Actor", "admin")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if got := request("/api/admin/database/query", "wrong", map[string]any{"sql": "SELECT name FROM groups"}); got.Code != 401 {
		t.Fatal(got.Code)
	}
	if got := request("/api/admin/database/query", "secret", map[string]any{"sql": "SELECT name FROM groups", "db_path": "other/db.sqlite"}); got.Code != 400 {
		t.Fatal("accepted caller path")
	}
	if got := request("/api/admin/database/mutate", "secret", map[string]any{"statements": []map[string]any{{"sql": "INSERT INTO group_members VALUES(?,?)", "params": []string{"g", "u"}}, {"sql": "INSERT INTO group_tool_grants VALUES(?,?)", "params": []string{"g", "c__read"}}}}); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	if _, err := policy.Authorize(s, auth.Identity{UserID: "u", WorkspaceID: "w"}, tool.PublicName); err != nil {
		t.Fatal("SQL grant not applied", err)
	}
	if got := request("/api/admin/database/mutate", "secret", map[string]any{"sql": "DELETE FROM group_tool_grants WHERE group_id=?", "params": []string{"g"}}); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	if _, err := policy.Authorize(s, auth.Identity{UserID: "u", WorkspaceID: "w"}, tool.PublicName); err == nil {
		t.Fatal("SQL revoke not applied")
	}
	if got := request("/api/admin/database/query", "secret", map[string]any{"sql": "SELECT name FROM groups"}); got.Code != 200 || !bytes.Contains(got.Body.Bytes(), []byte("Readers")) {
		t.Fatal(got.Code, got.Body.String())
	}
}
