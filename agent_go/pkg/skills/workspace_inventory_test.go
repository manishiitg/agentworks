package skills

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoverSkillsPreservesInstalledFilesAndRecordedProvenance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/skills/workspace/list" {
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"documents": []map[string]interface{}{
				{"folder_name": "unselected", "file_path": ".agents/skills/unselected/SKILL.md", "document": "---\nname: unselected\ndescription: Imported guide\n---\nBody", "source": "example/repo", "source_type": "github"},
				{"folder_name": "broken", "file_path": "skills/broken/SKILL.md", "document": "---\nname: [invalid yaml\n---\nKeep this visible"},
			},
			"usage": map[string][]string{"broken": {"Step: Check"}},
		})
	}))
	defer server.Close()
	inventory, err := DiscoverSkillsIn(server.URL, "Workflow/test")
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Skills) != 2 {
		t.Fatalf("installed file was hidden: %+v", inventory)
	}
	imported, broken := inventory.Skills[0], inventory.Skills[1]
	if imported.Source != "example/repo" || imported.Managed || imported.SourceType != "github" {
		t.Fatalf("incorrect provenance: %+v", imported)
	}
	if broken.ValidationError == "" || broken.FolderName != "broken" || broken.Content == "" {
		t.Fatalf("malformed file disappeared: %+v", broken)
	}
	if inventory.Usage["broken"][0] != "Step: Check" {
		t.Fatal("lost saved usage")
	}
}
