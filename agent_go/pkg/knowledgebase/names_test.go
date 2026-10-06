package knowledgebase

import (
	"context"
	"reflect"
	"testing"
)

// The Brain tools were renamed to brain_* (PLAT-608). A pre-rename name still works and is the same tool: a retried
// request_id under either name is one request, and only the new names are advertised.
func TestLegacyBrainToolNamesAreAliasesNotSecondTools(t *testing.T) {
	s, admin, _ := fixture(t, false)
	ctx := context.Background()
	args := func() map[string]any {
		return map[string]any{"action": "create", "folder_path": "", "filename": "note.md", "type": "note", "title": "Note", "content": "x", "request_id": "same"}
	}
	first, err := s.CallTool(ctx, admin, "update_knowledgebase", args())
	if err != nil {
		t.Fatalf("legacy name must still work: %v", err)
	}
	again, err := s.CallTool(ctx, admin, ToolUpdate, args())
	if err != nil || !reflect.DeepEqual(asMap(first)["entry_id"], asMap(again)["entry_id"]) {
		t.Fatalf("a retry under the new name must replay the same request: %v %v %v", first, again, err)
	}
	if !IsMCPTool("browse_knowledgebase") || CanonicalToolName("manage_knowledgebase_access") != ToolAccess || CanonicalToolName("list_workflows") != "list_workflows" {
		t.Fatal("legacy names must resolve to the renamed tools and nothing else")
	}
	var advertised []string
	for _, def := range ToolDefinitions() {
		advertised = append(advertised, def.Name)
	}
	if !reflect.DeepEqual(advertised, ToolNames()) {
		t.Fatalf("only the brain_* names are advertised: %v", advertised)
	}
}
