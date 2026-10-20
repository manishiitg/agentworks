package knowledgebase

import (
	"context"
	"reflect"
	"testing"
)

// Public MCP names are canonical; domain operations stay behind Service.Call.
func TestBrainMCPNamesAndCanonicalRetries(t *testing.T) {
	s, admin, _ := fixture(t, false)
	ctx := context.Background()
	args := map[string]any{"action": "create", "folder_path": "", "filename": "note.md", "type": "note", "title": "Note", "content": "x", "request_id": "same"}
	for _, name := range []string{"browse_knowledgebase", "read_knowledgebase", "update_knowledgebase", "backup_knowledgebase", "knowledgebase_skills", "manage_knowledgebase_access", "brain_backup"} {
		if IsMCPTool(name) {
			t.Fatalf("removed name remains a public tool: %s", name)
		}
		if _, err := s.CallTool(ctx, admin, name, args); err == nil {
			t.Fatalf("removed name dispatched: %s", name)
		}
	}
	first, err := s.CallTool(ctx, admin, ToolUpdate, args)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CallTool(ctx, admin, ToolUpdate, args)
	if err != nil || !reflect.DeepEqual(asMap(first)["entry_id"], asMap(again)["entry_id"]) {
		t.Fatalf("canonical retry must replay the same request: %v %v %v", first, again, err)
	}
	var advertised []string
	for _, def := range ToolDefinitions() {
		advertised = append(advertised, def.Name)
	}
	if !reflect.DeepEqual(advertised, ToolNames()) {
		t.Fatalf("unexpected public tools: %v", advertised)
	}
}
