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

func TestPlatformRuntimeBindingRequiresTrustedServiceAndGrantsPlatformResourcesAutomatically(t *testing.T) {
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
	// The first request of an unbound user binds it to Platform, which already
	// has the server and the secret registered before it.
	if _, exists := st.GetUser("new-user"); exists {
		t.Fatal("user bound before any request")
	}
	if out := request("/api/admin/runtime/servers", "new-user", "secret", ""); out.Code != 200 || !strings.Contains(out.Body.String(), "c__read") {
		t.Fatal(out.Code, out.Body.String())
	}
	if _, exists := st.GetUser("new-user"); !exists {
		t.Fatal("trusted platform user not bound")
	}
	if out := request("/api/admin/runtime/secrets", "second-user", "secret", ""); out.Code != 200 || !strings.Contains(out.Body.String(), "TEAM_KEY") {
		t.Fatal(out.Code, out.Body.String())
	}
	group := store.PlatformGroupID("w")
	if out := request("/api/admin/runtime/servers", "later-user", "secret", ""); out.Code != 200 || !strings.Contains(out.Body.String(), "c__read") {
		t.Fatal(out.Code, out.Body.String())
	}
	if out := request("/api/admin/runtime/secrets", "later-user", "secret", ""); out.Code != 200 || !strings.Contains(out.Body.String(), "TEAM_KEY") {
		t.Fatal(out.Code, out.Body.String())
	}
	if err := a.SetMember(group, "later-user", false, ""); err == nil {
		t.Fatal("automatic membership edited through API")
	}
	if err := a.RenameGroup(group, "Other name"); err == nil {
		t.Fatal("built-in group renamed")
	}
}

func TestRuntimeInventoryGroupsAreCallerScopedAndRevocationIsLive(t *testing.T) {
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	st.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	st.AddUser(store.User{ID: "bob", WorkspaceID: "w"})
	for _, gid := range []string{"engineering", "finance", "empty"} {
		st.AddGroup(store.Group{ID: gid, WorkspaceID: "w", Name: gid})
	}
	st.AddMember("engineering", "alice")
	st.AddMember("empty", "alice")
	st.AddMember("finance", "bob")
	st.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Label: "Shared Notion", Provider: "notion", Status: store.StatusActive, UpstreamURL: "https://credential.example/mcp"})
	for _, name := range []string{"notion__read", "notion__write"} {
		t := st.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: name, Fingerprint: "f", InputSchema: []byte(`{"type":"object"}`)})
		st.ApproveTool("w", t.PublicName, t.Fingerprint, t.Version)
	}
	st.AddGroupGrant(store.GroupGrant{GroupID: "engineering", PublicName: "notion__read"})
	st.AddGroupGrant(store.GroupGrant{GroupID: "finance", PublicName: "notion__write"})
	if err := st.RegisterSecrets("w", []store.SecretResource{{Name: "TEAM_KEY"}, {Name: "FINANCE_KEY"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSecretGrant("w", "engineering", "TEAM_KEY", "admin", true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSecretGrant("w", "finance", "FINANCE_KEY", "admin", true); err != nil {
		t.Fatal(err)
	}
	a := &Admin{Store: st, WorkspaceID: "w", HumanToken: "service-secret"}
	mux := http.NewServeMux()
	a.runtimeRoutes(mux)
	request := func(actor string) string {
		r := httptest.NewRequest("GET", "/api/admin/runtime/servers?user=bob", nil)
		r.Header.Set("Authorization", "Bearer service-secret")
		r.Header.Set("X-CapLayer-Actor", actor)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	body := request("alice")
	for _, want := range []string{"engineering", "empty", "notion__read", "TEAM_KEY"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing authorized metadata %s: %s", want, body)
		}
	}
	for _, deny := range []string{"finance", "FINANCE_KEY", "notion__write", "credential.example", "service-secret"} {
		if strings.Contains(body, deny) {
			t.Fatalf("leaked %s: %s", deny, body)
		}
	}
	st.RevokeGroupGrant("engineering", "notion__read")
	if err := st.SetSecretGrant("w", "engineering", "TEAM_KEY", "admin", false); err != nil {
		t.Fatal(err)
	}
	body = request("alice")
	if strings.Contains(body, "notion__read") || strings.Contains(body, "TEAM_KEY") {
		t.Fatalf("revoked resources remain visible: %s", body)
	}
	if body = request("bob"); !strings.Contains(body, "FINANCE_KEY") || !strings.Contains(body, "notion__write") || strings.Contains(body, "engineering") {
		t.Fatal(body)
	}
}

func TestBuilderInventoryAllConnectionsAndSecretMetadataIsServiceOnly(t *testing.T) {
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	st.AddUser(store.User{ID: "admin", WorkspaceID: "w"})
	st.AddGroup(store.Group{ID: "empty", WorkspaceID: "w", Name: "No members"})
	st.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive, UpstreamURL: "https://credential.example/mcp"})
	snap := st.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "c__read", Fingerprint: "f", InputSchema: []byte(`{"type":"object"}`)})
	st.ApproveTool("w", snap.PublicName, snap.Fingerprint, snap.Version)
	st.RegisterSecrets("w", []store.SecretResource{{Name: "TEST2"}})
	mux := http.NewServeMux()
	a := &Admin{Store: st, WorkspaceID: "w", HumanToken: "service"}
	a.runtimeRoutes(mux)
	for _, tc := range []struct {
		path, token, builder, origin, cookie string
		status                               int
		visible                              bool
	}{
		{"/api/admin/runtime/builder/servers", "service", "1", "", "", 200, true},
		{"/api/admin/runtime/servers", "service", "1", "", "", 200, false},
		{"/api/admin/runtime/builder/servers", "wrong", "1", "", "", 401, false},
		{"/api/admin/runtime/builder/servers", "service", "", "", "", 401, false},
		{"/api/admin/runtime/builder/servers", "service", "1", "https://app.example", "", 401, false},
		{"/api/admin/runtime/builder/servers", "service", "1", "", "login=x", 401, false},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		req.Header.Set("X-CapLayer-Actor", "admin")
		req.Header.Set("X-Vault-Builder", tc.builder)
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Cookie", tc.cookie)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		body := w.Body.String()
		if w.Code != tc.status || strings.Contains(body, "c__read") != tc.visible || strings.Contains(body, "TEST2") != tc.visible || strings.Contains(body, "credential.example") {
			t.Fatalf("inventory leak or missing setup authority: %d %s", w.Code, body)
		}
		if tc.visible && !strings.Contains(body, "No members") {
			t.Fatal("group membership incorrectly required")
		}
	}
}
