package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/admin"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/policy"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

// localAdminMux wires the admin API + UI on an empty workspace for auth tests.
func localAdminMux() *http.ServeMux {
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1", Name: "local"})
	adm := &admin.Admin{Store: st, WorkspaceID: "w1", HumanToken: "tok"}
	mux := http.NewServeMux()
	adm.APIRoutes(mux)
	adm.UIRoutes(mux)
	return mux
}

// TestLoopbackBypassesAdminToken: in local runs the user is admin, so
// loopback requests need no token on either the JSON API or the UI.
func TestLoopbackBypassesAdminToken(t *testing.T) {
	srv := httptest.NewServer(admin.LocalhostCORS(localAdminMux()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/admin/users")
	if err != nil {
		t.Fatalf("api users: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("api users without token: got %d, want 200", resp.StatusCode)
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err = client.Get(srv.URL + "/admin/")
	if err != nil {
		t.Fatalf("ui dashboard: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ui dashboard without cookie: got %d, want 200", resp.StatusCode)
	}
}

// TestGroupMembersRoundTrip: add/remove members and list them.
func TestGroupMembersRoundTrip(t *testing.T) {
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1", Name: "local"})
	st.AddUser(store.User{ID: "u1", WorkspaceID: "w1", Email: "u1@example.com"})
	st.AddGroup(store.Group{ID: "g1", WorkspaceID: "w1", Name: "G1"})
	adm := &admin.Admin{Store: st, WorkspaceID: "w1", HumanToken: "tok"}
	mux := http.NewServeMux()
	adm.APIRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	post := func(path, body string) (int, []byte) {
		t.Helper()
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, data
	}
	get := func(path string) (int, []byte) {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, data
	}

	if code, data := post("/api/admin/groups/g1/members", `{"user_id":"u1"}`); code != 200 {
		t.Fatalf("add member: %d %s", code, data)
	}
	code, data := get("/api/admin/groups/g1/members")
	if code != 200 {
		t.Fatalf("list members: %d %s", code, data)
	}
	var out struct {
		Members []string `json:"members"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Members) != 1 || out.Members[0] != "u1" {
		t.Fatalf("members decode: %v %s", err, data)
	}
	if code, data := get("/api/admin/groups/nope/members"); code != 400 {
		t.Fatalf("unknown group: got %d %s, want 400", code, data)
	}
}

// TestGroupServerGrantRoundTrip: attaching a whole connector to a group
// authorizes its tools (current and later-discovered) for members.
func TestGroupServerGrantRoundTrip(t *testing.T) {
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1", Name: "local"})
	st.AddUser(store.User{ID: "u1", WorkspaceID: "w1", Email: "u1@example.com"})
	st.AddGroup(store.Group{ID: "g1", WorkspaceID: "w1", Name: "G1"})
	st.AddMember("g1", "u1")
	st.AddConnector(store.Connector{ID: "c1", WorkspaceID: "w1", Provider: "fake", Label: "fake", Status: store.StatusActive})
	st.UpsertToolSnapshot(store.ToolSnapshot{ConnectorID: "c1", WorkspaceID: "w1", PublicName: "fake__tool", Status: store.StatusActive})
	adm := &admin.Admin{Store: st, WorkspaceID: "w1", HumanToken: "tok"}
	mux := http.NewServeMux()
	adm.APIRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	id := auth.Identity{UserID: "u1", WorkspaceID: "w1"}
	if _, err := policy.Authorize(st, id, "fake__tool"); err == nil {
		t.Fatal("tool allowed before any grant")
	}

	resp, err := http.Post(srv.URL+"/api/admin/groups/g1/servers", "application/json",
		strings.NewReader(`{"connector_id":"c1"}`))
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("attach: got %d, want 200", resp.StatusCode)
	}
	if _, err := policy.Authorize(st, id, "fake__tool"); err != nil {
		t.Fatalf("tool denied after server attach: %v", err)
	}
	// A tool discovered later is covered by the same ongoing attach.
	st.UpsertToolSnapshot(store.ToolSnapshot{ConnectorID: "c1", WorkspaceID: "w1", PublicName: "fake__new", Status: store.StatusActive})
	if _, err := policy.Authorize(st, id, "fake__new"); err != nil {
		t.Fatalf("later tool denied after server attach: %v", err)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/admin/groups/g1/servers/c1", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("detach: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("detach: got %d, want 204", resp.StatusCode)
	}
	if _, err := policy.Authorize(st, id, "fake__tool"); err == nil {
		t.Fatal("tool allowed after detach")
	}
}

// TestEmptyListsMarshalAsArrays: list endpoints emit [] (never null) so
// clients can iterate without nil checks.
func TestEmptyListsMarshalAsArrays(t *testing.T) {
	srv := httptest.NewServer(localAdminMux())
	defer srv.Close()

	for _, path := range []string{
		"/api/admin/users", "/api/admin/groups", "/api/admin/connectors",
		"/api/admin/tools", "/api/admin/audit",
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: got %d", path, resp.StatusCode)
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("GET %s decode: %v", path, err)
		}
		for key, value := range decoded {
			arr, ok := value.([]any)
			if !ok || arr == nil {
				t.Fatalf("GET %s: field %q = %v, want []", path, key, value)
			}
		}
	}
}

// TestNonLoopbackStillRequiresToken: off-loopback peers must present the
// token; the local bypass never applies to them.
func TestNonLoopbackStillRequiresToken(t *testing.T) {
	mux := localAdminMux()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.RemoteAddr = "203.0.113.7:1234"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token off loopback: got %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.RemoteAddr = "203.0.113.7:1234"
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("token off loopback: got %d, want 200", rec.Code)
	}

	// A spoofed forwarding header must not grant admin.
	req = httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.RemoteAddr = "203.0.113.7:1234"
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("forwarded loopback: got %d, want 401", rec.Code)
	}
}

// TestLocalhostCORS: loopback origins (the local AgentWorks UI) get CORS
// headers and preflight answers; anything else passes through untouched.
func TestLocalhostCORS(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := admin.LocalhostCORS(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.Header.Set("Origin", "http://127.0.0.1:51733")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:51733" {
		t.Fatalf("loopback origin header: got %q", got)
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("loopback GET passthrough: got %d, want 418", rec.Code)
	}

	req = httptest.NewRequest(http.MethodOptions, "/api/admin/users", nil)
	req.Header.Set("Origin", "http://localhost:51733")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight: got %d, want 204", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("foreign origin header: got %q, want empty", got)
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("foreign GET passthrough: got %d, want 418", rec.Code)
	}
}
