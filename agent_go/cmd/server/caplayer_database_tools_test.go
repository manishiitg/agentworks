package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCapLayerSQLToolsUseCurrentAdminAndFixedGateway(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","role":"admin","products":[]},{"id":"member","role":"creator","products":["mcp-gateway"]}]}`)
	calls := 0
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/admin/database/query" && r.URL.Path != "/api/admin/database/mutate" {
			t.Error(r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer service-secret-at-least-32-characters" || r.Header.Get("X-CapLayer-Actor") != "admin" {
			t.Error("incorrect service identity")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["sql"] == nil {
			t.Error("SQL dropped")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"columns":["name"],"rows":[{"name":"Readers"}]}`))
	}))
	defer service.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", service.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", "service-secret-at-least-32-characters")
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	reg := agentprofiles.NewRegistry()
	if err := registerCapLayerDatabaseTools(reg); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"query", "mutate"} {
		for _, id := range []string{"admin", "member"} {
			tool, err := reg.BuildTool(agentprofiles.ToolBinding{ID: "caplayer.database." + operation}, agentprofiles.ToolRuntimeContext{UserID: id, WorkspacePath: "Chats/CapLayer"})
			if err != nil {
				t.Fatal(err)
			}
			if tool.Name != operation+"_workflow_db" {
				t.Fatal(tool.Name)
			}
			_, err = tool.Execute(context.Background(), map[string]any{"sql": "SELECT name FROM groups"})
			if (err == nil) != (id == "admin") {
				t.Fatalf("%s %s: %v", id, operation, err)
			}
		}
	}
	tool, _ := reg.BuildTool(agentprofiles.ToolBinding{ID: "caplayer.database.query"}, agentprofiles.ToolRuntimeContext{UserID: "admin", WorkspacePath: "Chats/Work/projects/other"})
	if _, err := tool.Execute(context.Background(), map[string]any{"sql": "SELECT 1"}); err == nil {
		t.Fatal("foreign project reached SQL")
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
