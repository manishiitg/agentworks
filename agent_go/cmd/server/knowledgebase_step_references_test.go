package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-556: a Brain note a step's Guides name is read as the run's person under
// the project's Brain access, never past either. Pins the security boundary.
func TestReferencedBrainNoteRespectsPersonAndProjectAccess(t *testing.T) {
	s, admin, workspace, _ := knowledgeIntegrationFixture(t)
	if _, err := s.Call(t.Context(), admin, "create_knowledgebase", map[string]any{"folder_path": "Imported", "filename": "pricing.md", "type": "note", "title": "Pricing", "content": "Minimum bid $500.\n", "request_id": "pricing"}); err != nil {
		t.Fatal(err)
	}

	content, err := knowledgebaseReadReferencedNote(knowledgeTestCaller(t.Context(), "priya"), workspace, "Imported/pricing.md")
	if err != nil || !strings.Contains(content, "Minimum bid $500.") {
		t.Fatalf("folder reader could not read the named note: %q, %v", content, err)
	}
	if _, err := knowledgebaseReadReferencedNote(knowledgeTestCaller(t.Context(), "bob"), workspace, "Imported/pricing.md"); err == nil {
		t.Fatal("a person without a role on the folder read the note")
	}

	manifest := filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), workspace, "workflow.json")
	raw := map[string]any{}
	data, _ := os.ReadFile(manifest)
	_ = json.Unmarshal(data, &raw)
	raw["brain_access"] = "off"
	data, _ = json.Marshal(raw)
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgebaseReadReferencedNote(knowledgeTestCaller(t.Context(), "priya"), workspace, "Imported/pricing.md"); err == nil || !strings.Contains(err.Error(), "off") {
		t.Fatalf("Brain off for the project still delivered the note: %v", err)
	}
}
