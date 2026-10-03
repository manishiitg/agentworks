package admin

import (
	"bytes"
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

func TestPackageDraftPublishAndRevokeAPI(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddUser(store.User{ID: "u", WorkspaceID: "w"})
	s.AddGroup(store.Group{ID: "g", WorkspaceID: "w"})
	s.AddMember("g", "u")
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	tool := s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "p__read", Fingerprint: "v1", InputSchema: []byte(`{"type":"object","properties":{"project":{"type":"string"}}}`)})
	tool, _ = s.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version)
	a := &Admin{Store: s, WorkspaceID: "w", HumanToken: "secret"}
	mux := http.NewServeMux()
	a.accessRoutes(mux)
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		encoded, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodGet, "/api/admin/access/history", nil); got.Code != http.StatusOK || !bytes.Contains(got.Body.Bytes(), []byte(`"events":[]`)) {
		t.Fatalf("empty history must be an array: %d %s", got.Code, got.Body.String())
	}
	p := access.Package{ID: "pkg", GroupID: "g", Name: "one project", Rules: []access.ToolRule{{PublicName: "p__read", Fingerprint: "v1", Conditions: []access.Condition{{Path: "/project", Op: "equals", Value: "one"}}}}}
	if got := request(http.MethodPost, "/api/admin/access/packages", p); got.Code != http.StatusOK {
		t.Fatalf("save draft: %d %s", got.Code, got.Body.String())
	}
	if _, err := policy.Authorize(s, auth.Identity{UserID: "u", WorkspaceID: "w"}, "p__read"); err == nil {
		t.Fatal("draft affected live authorization")
	}
	if got := request(http.MethodPost, "/api/admin/access/packages/pkg/publish", map[string]any{"version": 999}); got.Code != http.StatusConflict {
		t.Fatalf("stale review published: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/api/admin/access/packages/pkg/publish", map[string]any{"version": 1}); got.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", got.Code, got.Body.String())
	}
	if _, err := policy.Authorize(s, auth.Identity{UserID: "u", WorkspaceID: "w"}, "p__read"); err != nil {
		t.Fatalf("published package denied: %v", err)
	}
	if got := request(http.MethodPost, "/api/admin/access/packages/pkg/revoke", map[string]any{}); got.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", got.Code, got.Body.String())
	}
	if _, err := policy.Authorize(s, auth.Identity{UserID: "u", WorkspaceID: "w"}, "p__read"); err == nil {
		t.Fatal("revoked package still allows")
	}
	p.Rules[0].Conditions[0].Path = "/unknown"
	if got := request(http.MethodPost, "/api/admin/access/packages", p); got.Code != http.StatusBadRequest {
		t.Fatalf("unknown schema path accepted: %d", got.Code)
	}
}

func TestRemoveGroupServerAccess(t *testing.T) {
	s := store.NewMemoryStore()
	for _, g := range []store.Group{{ID: "g", WorkspaceID: "w"}, {ID: "other", WorkspaceID: "w"}, {ID: "foreign", WorkspaceID: "elsewhere"}} {
		s.AddGroup(g)
	}
	for _, c := range []store.Connector{{ID: "a", WorkspaceID: "w", Status: store.StatusActive}, {ID: "b", WorkspaceID: "w", Status: store.StatusActive}, {ID: "foreign", WorkspaceID: "elsewhere"}} {
		s.AddConnector(c)
	}
	for _, tool := range []store.ToolSnapshot{
		{WorkspaceID: "w", ConnectorID: "a", PublicName: "a__read", Fingerprint: "f1", InputSchema: []byte(`{"type":"object"}`)},
		{WorkspaceID: "w", ConnectorID: "a", PublicName: "a__write", Fingerprint: "f1", InputSchema: []byte(`{"type":"object"}`)},
		{WorkspaceID: "w", ConnectorID: "b", PublicName: "b__read", Fingerprint: "f1", InputSchema: []byte(`{"type":"object"}`)},
	} {
		t1 := s.UpsertToolSnapshot(tool)
		s.ApproveTool("w", t1.PublicName, t1.Fingerprint, t1.Version)
	}
	s.AddGroupServerGrant("g", "a")
	s.AddGroupServerGrant("other", "a")
	s.AddGroupGrant(store.GroupGrant{GroupID: "g", PublicName: "a__read"})
	s.AddGroupGrant(store.GroupGrant{GroupID: "g", PublicName: "a__write"})
	s.AddGroupGrant(store.GroupGrant{GroupID: "g", PublicName: "b__read"})
	rules := []access.ToolRule{{PublicName: "a__read", Fingerprint: "f1"}, {PublicName: "b__read", Fingerprint: "f1"}}
	p, _ := s.SavePackageDraft(access.Package{ID: "mixed", WorkspaceID: "w", GroupID: "g", Name: "Mixed", Rules: rules}, 0)
	s.PublishPackage("w", p.ID, p.Version)
	oldDraft, _ := s.SavePackageDraft(p, p.Version)
	only, _ := s.SavePackageDraft(access.Package{ID: "only", WorkspaceID: "w", GroupID: "g", Name: "Only A", Rules: rules[:1]}, 0)
	s.PublishPackage("w", only.ID, only.Version)
	other, _ := s.SavePackageDraft(access.Package{ID: "other", WorkspaceID: "w", GroupID: "other", Name: "Other group", Rules: rules[:1]}, 0)
	s.PublishPackage("w", other.ID, other.Version)
	a := &Admin{Store: s, WorkspaceID: "w", HumanToken: "secret"}
	mux := http.NewServeMux()
	a.accessRoutes(mux)
	request := func(method, path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-CapLayer-Actor", "admin")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	path := "/api/admin/groups/g/servers/a/access"
	for _, tc := range []struct {
		method, path, token string
		status              int
	}{
		{http.MethodDelete, path, "wrong", 401}, {http.MethodPost, path, "secret", 405},
		{http.MethodDelete, "/api/admin/groups/foreign/servers/a/access", "secret", 404},
		{http.MethodDelete, "/api/admin/groups/g/servers/foreign/access", "secret", 404},
		{http.MethodDelete, path, "secret", 204},
	} {
		if got := request(tc.method, tc.path, tc.token); got.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, got.Code, got.Body.String())
		}
	}
	if s.GroupHasServer("g", "a") || s.GroupHasTool("g", "a__read") || s.GroupHasTool("g", "a__write") {
		t.Fatal("legacy grants survived removal")
	}
	if !s.GroupHasServer("other", "a") || !s.GroupHasTool("g", "b__read") {
		t.Fatal("unrelated grants removed")
	}
	for _, p := range s.ListPackages("w") {
		if p.GroupID == "g" {
			for _, rule := range p.Rules {
				if rule.PublicName == "a__read" {
					t.Fatal("removed server survived in policy")
				}
			}
		}
		if p.ID == "only" && p.Status != "revoked" {
			t.Fatal("empty live policy was not revoked")
		}
	}
	if _, ok := s.PublishPackage("w", "mixed", oldDraft.Version); ok {
		t.Fatal("stale draft republished removed access")
	}
	if _, ok := s.SavePackageDraft(oldDraft, oldDraft.Version); ok {
		t.Fatal("stale edit restored removed access")
	}
	for _, tc := range []struct {
		group, tool string
		allowed     bool
	}{{"g", "a__read", false}, {"g", "a__write", false}, {"g", "b__read", true}, {"other", "a__read", true}} {
		_, err := policy.Authorize(s, auth.Identity{WorkspaceID: "w", ViaGroup: tc.group}, tc.tool)
		if (err == nil) != tc.allowed {
			t.Fatalf("%s/%s: %v", tc.group, tc.tool, err)
		}
	}
	if c, ok := s.GetConnector("a"); !ok || c.Status != store.StatusActive {
		t.Fatal("server disconnected")
	}
	if got := request(http.MethodDelete, path, "secret"); got.Code != 204 {
		t.Fatal("removal not idempotent")
	}
	if events := s.ListPolicyEvents("w"); len(events) == 0 || events[0].Actor != "admin" || events[0].GroupID != "g" || events[0].ConnectorID != "a" {
		t.Fatalf("missing removal audit: %+v", events)
	}
}

func TestGroupPermissionInventoryUsesRuntimePolicy(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddGroup(store.Group{ID: "g", WorkspaceID: "w"})
	s.AddGroup(store.Group{ID: "foreign", WorkspaceID: "other"})
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	tool := s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "p__read", Fingerprint: "v1", InputSchema: []byte(`{"type":"object","properties":{"project":{"type":"string"}}}`)})
	s.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version)
	s.AddGroupServerGrant("g", "c")
	a := &Admin{Store: s, WorkspaceID: "w", HumanToken: "secret"}
	mux := http.NewServeMux()
	a.accessRoutes(mux)
	request := func(group, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/admin/groups/"+group+"/permissions", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	check := func(allowed, governed bool, source string) {
		t.Helper()
		rec := request("g", "secret")
		var out struct {
			Permissions []struct {
				PublicName string `json:"public_name"`
				Allowed    bool   `json:"allowed"`
				Governed   bool   `json:"governed"`
				Source     string `json:"source"`
			}
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || len(out.Permissions) != 1 {
			t.Fatal("bad inventory", rec.Code, rec.Body)
		}
		p := out.Permissions[0]
		if p.PublicName != tool.PublicName || p.Allowed != allowed || p.Governed != governed || p.Source != source {
			t.Fatalf("wrong effective permission %+v", p)
		}
	}
	check(true, false, "server")
	p := access.Package{ID: "p", WorkspaceID: "w", GroupID: "g", Name: "Scoped", Rules: []access.ToolRule{{PublicName: tool.PublicName, Fingerprint: tool.Fingerprint, Conditions: []access.Condition{{Path: "/project", Op: "equals", Value: "one"}}}}}
	saved, ok := s.SavePackageDraft(p, 0)
	if !ok {
		t.Fatal("draft failed")
	}
	check(true, false, "server")
	if _, ok := s.PublishPackage("w", p.ID, saved.Version); !ok {
		t.Fatal("publish failed")
	}
	check(true, true, "policy")
	s.RevokePackage("w", p.ID)
	check(false, true, "policy")
	if request("foreign", "secret").Code != 404 || request("g", "wrong").Code != 401 {
		t.Fatal("inventory leaked across authorization or workspace")
	}
}

func TestConfigurationFailureReturns503AndDeniesCalls(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "gateway.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddUser(store.User{ID: "u", WorkspaceID: "w"})
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	tool := s.UpsertToolSnapshot(store.ToolSnapshot{ConnectorID: "c", WorkspaceID: "w", PublicName: "c__read", Fingerprint: "f"})
	s.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version)
	s.AddGrant(store.Grant{UserID: "u", PublicName: tool.PublicName})
	identity := auth.Identity{UserID: "u", WorkspaceID: "w"}
	if _, err := policy.Authorize(s, identity, tool.PublicName); err != nil {
		t.Fatal(err)
	}
	a := &Admin{Store: s, WorkspaceID: "w", HumanToken: "secret"}
	// Close storage to force the next write to fail after auth has passed.
	s.Close()
	handler := a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		s.AddGroup(store.Group{ID: "lost", WorkspaceID: "w"})
		writeJSON(w, http.StatusCreated, map[string]any{"id": "lost"})
	})
	r := httptest.NewRequest(http.MethodPost, "/api/admin/groups", nil)
	r.Header.Set("Authorization", "Bearer secret")
	out := httptest.NewRecorder()
	handler(out, r)
	if out.Code != http.StatusServiceUnavailable {
		t.Fatalf("false success: %d %s", out.Code, out.Body.String())
	}
	if _, ok := s.GetGroup("lost"); ok {
		t.Fatal("unsaved group visible")
	}
	if _, err := policy.Authorize(s, identity, tool.PublicName); err == nil {
		t.Fatal("storage failure did not deny calls")
	}
}

func TestGroupDescriptionsAPI(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddGroup(store.Group{ID: "foreign", WorkspaceID: "other", Name: "Other", Description: "Private"})
	a := &Admin{Store: s, WorkspaceID: "w", HumanToken: "secret"}
	mux := http.NewServeMux()
	a.APIRoutes(mux)
	request := func(method, path, token string, body any) *httptest.ResponseRecorder {
		t.Helper()
		encoded, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		r.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec
	}
	if got := request("POST", "/api/admin/groups", "secret", map[string]string{"ID": "g", "Name": "Readers", "Description": "  Support read access  "}); got.Code != 201 {
		t.Fatal(got.Code, got.Body.String())
	}
	s.AddUser(store.User{ID: "u", WorkspaceID: "w"})
	s.AddMember("g", "u")
	s.AddGroupGrant(store.GroupGrant{GroupID: "g", PublicName: "read"})
	for _, change := range []struct {
		body       map[string]string
		name, desc string
	}{
		{map[string]string{"Name": "Support"}, "Support", "Support read access"},
		{map[string]string{"Description": "New purpose"}, "Support", "New purpose"},
		{map[string]string{"Description": ""}, "Support", ""},
	} {
		if got := request("POST", "/api/admin/groups/g", "secret", change.body); got.Code != 200 {
			t.Fatal(got.Code, got.Body.String())
		}
		group, _ := s.GetGroup("g")
		if group.Name != change.name || group.Description != change.desc || !s.GroupHasTool("g", "read") || len(s.MembersOf("g")) != 1 {
			t.Fatal("metadata update changed unrelated group state", group)
		}
	}
	for _, check := range []struct {
		path, token string
		body        map[string]string
		status      int
	}{
		{"/api/admin/groups/g", "wrong", map[string]string{"Description": "Unauthorized"}, 401},
		{"/api/admin/groups/foreign", "secret", map[string]string{"Description": "Cross workspace"}, 400},
		{"/api/admin/groups/g", "secret", map[string]string{"Description": strings.Repeat("x", store.MaxGroupDescriptionLength+1)}, 400},
		{"/api/admin/groups", "secret", map[string]string{"ID": "too-long", "Name": "Long", "Description": strings.Repeat("x", store.MaxGroupDescriptionLength+1)}, 400},
	} {
		if got := request("POST", check.path, check.token, check.body); got.Code != check.status {
			t.Fatal(got.Code, got.Body.String())
		}
	}
	group, _ := s.GetGroup("foreign")
	if group.Description != "Private" {
		t.Fatal("cross workspace edit succeeded")
	}
	got := request("GET", "/api/admin/groups", "secret", nil)
	if got.Code != 200 || !bytes.Contains(got.Body.Bytes(), []byte(`"Description":""`)) || bytes.Contains(got.Body.Bytes(), []byte("Private")) {
		t.Fatal(got.Code, got.Body.String())
	}
}
