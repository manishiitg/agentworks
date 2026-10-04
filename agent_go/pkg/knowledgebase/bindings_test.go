package knowledgebase

import "testing"

func TestBindingAudienceIsRestrictionNotGrant(t *testing.T) {
	s, a, p := fixture(t, false)
	folder(t, s, a, "", "Shared")
	info := call(t, s, a, "get_knowledgebase_access", map[string]any{"folder_path": "Shared"})
	id := info["folder_id"].(string)
	b := Binding{Alias: "payments", FolderID: id, Access: "read"}
	if _, err := s.ResolveBinding(t.Context(), a, b, []string{"admin"}); err == nil {
		t.Fatal("admin audience bypassed actual grants")
	}
	grant(t, s, a, "admin", "Shared", "Owner", "owner")
	grant(t, s, a, "priya", "Shared", "Reader", "reader")
	e := create(t, s, a, "Shared", "note.md", "visible", "new")
	p.BindingPolicy = &BindingPolicy{Bindings: []Binding{b}, Audience: []string{"admin", "priya"}}
	if _, err := s.CallTool(t.Context(), p, "read_knowledgebase", map[string]any{"action": "read", "entry_id": e["entry_id"]}); err != nil {
		t.Fatal(err)
	}
	p.IsAdmin = true
	if _, err := s.CallTool(t.Context(), p, "update_knowledgebase", map[string]any{"action": "update", "entry_id": e["entry_id"], "expected_version": e["version"], "content": "blocked", "request_id": "bound_write"}); err == nil {
		t.Fatal("read binding allowed write")
	}
	p.BindingPolicy.Audience = append(p.BindingPolicy.Audience, "editor")
	if _, err := s.CallTool(t.Context(), p, "read_knowledgebase", map[string]any{"action": "read", "entry_id": e["entry_id"]}); err == nil {
		t.Fatal("ungranted audience read shared data")
	}
}

func TestIntegrationRequestIDsShareContentNamespace(t *testing.T) {
	s, a, _ := fixture(t, false)
	args := map[string]any{"action": "migration_preview", "request_id": "reuse", "workspace_path": "Workflow/demo", "folder_id": "folder_example", "alias": "demo", "access": "read"}
	if _, err := s.ReserveIntegrationRequest(t.Context(), a, "update_knowledgebase", args); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveIntegrationRequest(t.Context(), a, "update_knowledgebase", args); err != nil {
		t.Fatal(err)
	}
	_, err := s.CallTool(t.Context(), a, "update_knowledgebase", map[string]any{"action": "create", "request_id": "reuse", "folder_path": "", "filename": "note.md", "title": "Note", "type": "note", "content": "x"})
	if e, ok := err.(*Error); !ok || e.Code != "REQUEST_ID_REUSE" {
		t.Fatalf("got %v", err)
	}
}
