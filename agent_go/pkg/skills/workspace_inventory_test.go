package skills

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
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

func TestDiscoverSkillsKeepsWorkspaceCopiesVisibleWhenBuiltinNamesMatch(t *testing.T) {
	const name = "test-workspace-inventory-builtin-collision"
	if err := RegisterBuiltin(&llmtypes.Skill{Name: name, Description: "Product procedure", Content: "Platform body"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		builtinSkillsMu.Lock()
		delete(builtinSkills, name)
		builtinSkillsMu.Unlock()
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"documents": []map[string]interface{}{
				{"folder_name": name, "file_path": "skills/" + name + "/SKILL.md", "document": "---\nname: " + name + "\ndescription: Imported project copy\n---\nProject body"},
				{"folder_name": "agent-browser", "file_path": ".agents/skills/agent-browser/SKILL.md", "document": "---\nname: agent-browser\ndescription: Platform projection\n---\nManaged body", "managed": true},
			},
			"usage": map[string][]string{name: {"Main chat"}},
		})
	}))
	defer server.Close()
	inventory, err := DiscoverSkillsIn(server.URL, "Crew/example")
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Skills) != 2 {
		t.Fatalf("inventory = %+v", inventory)
	}
	copy, platform := inventory.Skills[0], inventory.Skills[1]
	if !copy.IsCustomWorkspaceSkill("Crew/example") || copy.Managed || !copy.Builtin || copy.FileManaged || copy.Content == "Platform body" {
		t.Fatalf("project copy was hidden as a platform definition: %+v", copy)
	}
	if platform.IsCustomWorkspaceSkill("Crew/example") || !platform.Managed || !platform.Builtin || !platform.FileManaged {
		t.Fatalf("platform projection lost its ownership: %+v", platform)
	}
}
