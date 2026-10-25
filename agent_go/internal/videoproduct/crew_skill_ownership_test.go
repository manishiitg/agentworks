package videoproduct

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// The same public runtime/file operations serve owner chats and shared Run
// chats. A removed project skill must not reappear from the product library.
func TestCrewSkillFilesReplaceBundledVideoLibrary(t *testing.T) {
	if err := RegisterProductSkills(); err != nil {
		t.Fatal(err)
	}
	const name = "minimax-h3-video"
	const root = "Crew/example"
	body := "---\nname: minimax-h3-video\ndescription: Project instructions\n---\n\nProject version one."
	installed := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/skills/workspace/list":
			docs := []map[string]any{}
			if installed {
				docs = append(docs, map[string]any{"folder_name": name, "file_path": "skills/" + name + "/SKILL.md", "document": body})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"documents": docs, "usage": map[string][]string{name: {"Main chat"}}})
		case "/api/skills/workspace/files":
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{}})
		case "/api/skills/workspace/delete":
			installed = false
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		case "/api/skills/workspace/import":
			installed = true
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			http.Error(w, "unexpected route", 404)
		}
	}))
	defer server.Close()
	for _, definition := range profileSkills {
		if skills.IsBuiltinSkillIn(root, definition.name) {
			t.Fatalf("Crew still borrows product skill %s", definition.name)
		}
		if !skills.IsBuiltinSkillIn("Chats/Video Studio/projects/example", definition.name) {
			t.Fatalf("original product lost %s", definition.name)
		}
	}
	if !skills.IsBuiltinSkillIn(root, "agent-browser") {
		t.Fatal("core platform guide lost protection")
	}
	check := func(want string) {
		t.Helper()
		attached := skills.LoadAttachableIn(server.URL, root, []string{name})
		if len(attached) != 1 || attached[0].Source.Origin != "imported" || !strings.Contains(attached[0].Content, want) {
			t.Fatalf("runtime did not use project file: %+v", attached)
		}
		file, err := skills.NewInstalledSkillReader(server.URL, root)(name, "SKILL.md")
		if err != nil || !strings.Contains(file.Content, want) {
			t.Fatalf("read_skill did not use project file: %v %+v", err, file)
		}
	}
	check("Project version one")
	before, err := skills.WorkspaceSkillDigest(server.URL, root, []string{name})
	if err != nil {
		t.Fatal(err)
	}
	body = strings.ReplaceAll(body, "version one", "edited version")
	check("Project edited version")
	after, err := skills.WorkspaceSkillDigest(server.URL, root, []string{name})
	if err != nil || before == after {
		t.Fatal("skill edit did not change the retained runtime revision")
	}
	inventory, err := skills.DiscoverSkillsIn(server.URL, root)
	if err != nil || len(inventory.Skills) != 1 || inventory.Skills[0].Builtin || inventory.Skills[0].Managed {
		t.Fatalf("incorrect project ownership: %+v %v", inventory, err)
	}
	if err := skills.UninstallSkillIn(context.Background(), server.URL, root, name); err != nil {
		t.Fatal(err)
	}
	if got := skills.LoadAttachableIn(server.URL, root, []string{name}); len(got) != 0 {
		t.Fatal("deleted skill reappeared from bundled library")
	}
	if _, err := skills.NewInstalledSkillReader(server.URL, root)(name, ""); err == nil {
		t.Fatal("read_skill resurrected deleted project skill")
	}
	if err := skills.InstallSkillFilesIn(server.URL, root, name, map[string][]byte{"SKILL.md": []byte(body)}); err != nil {
		t.Fatal(err)
	}
	check("Project edited version")
	if err := skills.UninstallSkillIn(context.Background(), server.URL, "Chats/Video Studio/projects/example", name); err == nil {
		t.Fatal("original product's bundled library became removable")
	}
	original := skills.LoadAttachableIn("http://unused.invalid", "Chats/Video Studio/projects/example", []string{name})
	if len(original) != 1 || original[0].Source.Origin != "builtin" || strings.Contains(original[0].Content, "Project edited version") {
		t.Fatal("original product no longer uses bundled instructions")
	}
}
