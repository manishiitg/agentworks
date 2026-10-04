package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/policy"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

func TestBuilderGroupRemovalUsesEffectivePermissionsAndPersistsRevocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.sqlite")
	s, err := store.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddWorkspace(store.Workspace{ID: "foreign"})
	s.AddUser(store.User{ID: "excluded", WorkspaceID: "w"})
	s.AddUser(store.User{ID: "included", WorkspaceID: "w"})
	s.AddGroup(store.Group{ID: "team", WorkspaceID: "w"})
	s.AddGroup(store.Group{ID: "foreign", WorkspaceID: "foreign"})
	s.AddMember("team", "included")
	if err := s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	platform := store.PlatformGroupID("w")
	for _, c := range []store.Connector{{ID: "linear", WorkspaceID: "w", Status: store.StatusActive}, {ID: "notion", WorkspaceID: "w", Status: store.StatusActive}, {ID: "foreign", WorkspaceID: "foreign"}} {
		s.AddConnector(c)
	}
	for _, c := range []string{"linear", "notion"} {
		tool := s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: c, PublicName: c + "__read", Fingerprint: "v1", InputSchema: []byte(`{"type":"object"}`)})
		s.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version)
	}
	s.AddGroupServerGrant(platform, "linear")
	s.AddGroupServerGrant(platform, "notion")
	s.AddGroupGrant(store.GroupGrant{GroupID: "team", PublicName: "linear__read"})
	a := &Admin{Store: s, WorkspaceID: "w", HumanToken: "secret"}
	inspect := json.RawMessage(`{"group_id":"` + platform + `"}`)
	// The original bug: no individual grants, but whole-server access is live.
	if len(s.GroupGrantsFor(platform)) != 0 {
		t.Fatal("fixture has individual grants")
	}
	result, err := a.setupTool(context.Background(), "inspect_group", inspect)
	if err != nil {
		t.Fatal(err)
	}
	permissions := result.(map[string]any)["permissions"].([]groupPermission)
	if len(permissions) != 2 || !permissions[0].Allowed || !permissions[1].Allowed {
		t.Fatal("server grants were not counted", permissions)
	}
	// Exercise all removal sources, including a saved rule and a direct grant.
	s.AddGroupGrant(store.GroupGrant{GroupID: platform, PublicName: "linear__read"})
	if _, err := s.SaveAccessPackage(access.Package{ID: "rule", WorkspaceID: "w", GroupID: platform, Name: "Linear", Rules: []access.ToolRule{{PublicName: "linear__read", Fingerprint: "v1"}}}, 0, "admin"); err != nil {
		t.Fatal(err)
	}
	// Once governed, this tool needs a saved rule for each authorized group.
	if _, err := s.SaveAccessPackage(access.Package{ID: "team-rule", WorkspaceID: "w", GroupID: "team", Name: "Team Linear", Rules: []access.ToolRule{{PublicName: "linear__read", Fingerprint: "v1"}}}, 0, "admin"); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.setupRoutes(mux)
	call := func(token, group, connector string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"operation": "remove_group_mcp", "arguments": map[string]string{"group_id": group, "connector_id": connector}})
		r := httptest.NewRequest(http.MethodPost, "/api/admin/setup/tool", strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-CapLayer-Actor", "operator")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		token, group, connector string
		status                  int
	}{{"wrong", platform, "linear", 401}, {"secret", "foreign", "linear", 400}, {"secret", platform, "foreign", 400}, {"secret", platform, "missing", 400}} {
		if w := call(tc.token, tc.group, tc.connector); w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := call("secret", platform, "linear")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"allowed_tool_count":0`) || !strings.Contains(w.Body.String(), `"server_grant_active":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if s.GroupHasServer(platform, "linear") || s.GroupHasTool(platform, "linear__read") {
		t.Fatal("group grants survived")
	}
	if _, err := policy.Authorize(s, auth.Identity{UserID: "excluded", WorkspaceID: "w"}, "linear__read"); err == nil {
		t.Fatal("excluded user retained access")
	}
	if _, err := policy.Authorize(s, auth.Identity{UserID: "included", WorkspaceID: "w"}, "linear__read"); err != nil {
		t.Fatal("other group lost access", err)
	}
	if !s.GroupHasServer(platform, "notion") {
		t.Fatal("other connector lost access")
	}
	if _, ok := s.GetConnector("linear"); !ok {
		t.Fatal("connection was deleted")
	}
	events := s.ListPolicyEvents("w")
	if events[len(events)-1].Actor != "operator" || events[len(events)-1].Action != "remove_server_from_group" {
		t.Fatal("removal was not audited", events)
	}
	s.Close()
	s, err = store.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	if s.GroupHasServer(platform, "linear") || !s.GroupHasServer(platform, "notion") {
		t.Fatal("restart restored explicit removal or removed another server")
	}
}
