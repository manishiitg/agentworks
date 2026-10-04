package admin

import (
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRuntimeSecretPermissionsRequireHostAndLiveMembership(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	s.AddGroup(store.Group{ID: "g", WorkspaceID: "w"})
	s.AddMember("g", "alice")
	s.RegisterSecrets("w", []store.SecretResource{{Name: "KEY", Managed: true}})
	s.SetSecretGrant("w", "g", "KEY", "admin", true)
	a := &Admin{Store: s, WorkspaceID: "w", HumanToken: strings.Repeat("s", 32)}
	mux := http.NewServeMux()
	a.APIRoutes(mux)
	for _, test := range []struct {
		actor, auth, origin string
		want                int
	}{{"alice", "Bearer " + a.HumanToken, "", 200}, {"alice", "bad", "", 401}, {"alice", "Bearer " + a.HumanToken, "http://browser", 401}, {"", "Bearer " + a.HumanToken, "", 401}} {
		req := httptest.NewRequest("GET", "/api/admin/runtime/secrets", nil)
		req.Header.Set("Authorization", test.auth)
		req.Header.Set("X-CapLayer-Actor", test.actor)
		req.Header.Set("Origin", test.origin)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != test.want {
			t.Fatalf("status %d want %d", w.Code, test.want)
		}
		if strings.Contains(w.Body.String(), "value") {
			t.Fatal("value exposed")
		}
	}
	s.RemoveMember("g", "alice")
	req := httptest.NewRequest("GET", "/api/admin/runtime/secrets", nil)
	req.Header.Set("Authorization", "Bearer "+a.HumanToken)
	req.Header.Set("X-CapLayer-Actor", "alice")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), "KEY") {
		t.Fatal("revoked resource still discoverable")
	}
}
