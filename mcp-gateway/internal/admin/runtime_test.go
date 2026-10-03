package admin

import (
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRuntimeInventoryIsServiceOnlyAndGrantFiltered(t *testing.T) {
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	st.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	st.AddUser(store.User{ID: "bob", WorkspaceID: "w"})
	st.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Label: "Shared", Provider: "p", Status: store.StatusActive, UpstreamURL: "https://private.example/mcp"})
	t1 := st.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "p__read", Fingerprint: "f", InputSchema: []byte(`{"type":"object"}`)})
	st.ApproveTool("w", t1.PublicName, t1.Fingerprint, t1.Version)
	st.AddGrant(store.Grant{UserID: "alice", PublicName: t1.PublicName})
	a := &Admin{Store: st, Gateway: mcpserver.New(st, nil, nil, nil), WorkspaceID: "w", HumanToken: "secret"}
	mux := http.NewServeMux()
	a.runtimeRoutes(mux)
	for _, tc := range []struct {
		token, actor, origin string
		status               int
		visible              bool
	}{{"secret", "alice", "", 200, true}, {"secret", "bob", "", 200, false}, {"wrong", "alice", "", 401, false}, {"secret", "alice", "https://app.example", 401, false}, {"secret", "", "", 401, false}} {
		r := httptest.NewRequest("GET", "/api/admin/runtime/servers", nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("X-CapLayer-Actor", tc.actor)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "p__read") != tc.visible || strings.Contains(w.Body.String(), "private.example") {
			t.Fatalf("inventory wrong or leaked URL: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestRuntimeInventoryRejectsBrowserCookieAuthentication(t *testing.T) {
	st := store.NewMemoryStore()
	a := &Admin{Store: st, Gateway: mcpserver.New(st, nil, nil, nil), WorkspaceID: "w", HumanToken: "secret"}
	mux := http.NewServeMux()
	a.runtimeRoutes(mux)
	r := httptest.NewRequest("GET", "/api/admin/runtime/servers", nil)
	r.Header.Set("Cookie", "gateway_admin=secret")
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("X-CapLayer-Actor", "alice")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("browser cookie admitted to service-only inventory")
	}
}

func TestPlatformRuntimeBindingRequiresTrustedServiceAndUsesExplicitDefaultGrants(t *testing.T) {
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	if err := st.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	st.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	tool := st.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "c__read", Fingerprint: "f", InputSchema: []byte(`{"type":"object"}`)})
	st.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version)
	if err := st.RegisterSecrets("w", []store.SecretResource{{Name: "TEAM_KEY", Managed: true}}); err != nil {
		t.Fatal(err)
	}
	a := &Admin{Store: st, WorkspaceID: "w", HumanToken: "secret"}
	mux := http.NewServeMux()
	a.APIRoutes(mux)
	request := func(path, actor, token, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-CapLayer-Actor", actor)
		r.Header.Set("X-Vault-Platform-User", "1")
		r.Header.Set("Origin", origin)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		return out
	}
	for _, path := range []string{"/api/admin/runtime/servers", "/api/admin/runtime/secrets"} {
		if out := request(path, "attacker", "wrong", ""); out.Code != 401 {
			t.Fatal(out.Code)
		}
		if out := request(path, "attacker", "secret", "https://app.example"); out.Code != 401 {
			t.Fatal(out.Code)
		}
	}
	if _, exists := st.GetUser("attacker"); exists {
		t.Fatal("untrusted identity provisioned")
	}
	if out := request("/api/admin/runtime/servers", "new-user", "secret", ""); out.Code != 200 || strings.Contains(out.Body.String(), "c__read") {
		t.Fatal(out.Code, out.Body.String())
	}
	if _, exists := st.GetUser("new-user"); !exists {
		t.Fatal("trusted platform user not bound")
	}
	group := store.PlatformGroupID("w")
	st.AddGroupGrant(store.GroupGrant{GroupID: group, PublicName: "c__read"})
	if err := st.SetSecretGrant("w", group, "TEAM_KEY", "admin", true); err != nil {
		t.Fatal(err)
	}
	if out := request("/api/admin/runtime/servers", "later-user", "secret", ""); out.Code != 200 || !strings.Contains(out.Body.String(), "c__read") {
		t.Fatal(out.Code, out.Body.String())
	}
	if out := request("/api/admin/runtime/secrets", "later-user", "secret", ""); out.Code != 200 || !strings.Contains(out.Body.String(), "TEAM_KEY") {
		t.Fatal(out.Code, out.Body.String())
	}
	if err := a.SetMember(group, "later-user", false); err == nil {
		t.Fatal("automatic membership edited through API")
	}
	if err := a.RenameGroup(group, "Other name"); err == nil {
		t.Fatal("built-in group renamed")
	}
}
