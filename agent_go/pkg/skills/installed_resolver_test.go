package skills

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func skillServer(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/skills/workspace/list" {
			var req map[string]string
			_ = json.NewDecoder(r.Body).Decode(&req)
			documents := []map[string]interface{}{}
			for file, body := range files {
				rel := strings.TrimPrefix(file, req["workspace_path"]+"/")
				if rel != file && strings.HasSuffix(rel, "/SKILL.md") {
					documents = append(documents, map[string]interface{}{"folder_name": strings.Split(rel, "/")[1], "file_path": rel, "document": body})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"documents": documents})
			return
		}
		requested := strings.TrimPrefix(r.URL.Path, "/api/documents/")
		for path, body := range files {
			if requested == path || requested == strings.ReplaceAll(path, "/", "%2F") {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
					"data":    map[string]interface{}{"filepath": path, "content": body},
				})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true, "message": "File does not exist",
			"error": "File not found: " + requested,
			"data":  map[string]interface{}{"filepath": "", "content": ""},
		})
	}))
}

func TestInstalledSkillReaderServesAWorkspaceSkill(t *testing.T) {
	server := skillServer(t, map[string]string{
		"proj/skills/hyperframes-core/SKILL.md": "---\nname: hyperframes-core\ndescription: core rules\n---\n\nThe body.\n",
	})
	defer server.Close()

	file, err := NewInstalledSkillReader(server.URL, "proj")("hyperframes-core", "")
	if err != nil {
		t.Fatalf("installed skill should resolve: %v", err)
	}
	if !strings.Contains(file.Content, "The body.") {
		t.Fatalf("content = %q", file.Content)
	}
	// The description is what a router needs to confirm it asked for the right
	// skill, so it must survive the frontmatter parse.
	if file.Description != "core rules" {
		t.Fatalf("description = %q", file.Description)
	}
}

func TestInstalledSkillReaderRejectsEscapingPaths(t *testing.T) {
	server := skillServer(t, map[string]string{})
	defer server.Close()
	read := NewInstalledSkillReader(server.URL, "proj")

	for _, bad := range []string{"../../etc/passwd", "/etc/passwd", "refs/../../secret"} {
		if _, err := read("skill", bad); err == nil {
			t.Fatalf("path %q must be rejected at the host boundary", bad)
		}
	}
}

func TestInstalledSkillReaderReportsAMissingSkillPlainly(t *testing.T) {
	server := skillServer(t, map[string]string{})
	defer server.Close()
	_, err := NewInstalledSkillReader(server.URL, "proj")("ghost", "")
	if err == nil {
		t.Fatal("a skill that is not installed must error")
	}
	// It must not claim the file is malformed — that was the false diagnosis
	// this whole thread started from.
	if strings.Contains(err.Error(), "frontmatter") {
		t.Fatalf("missing skill reported as a parse defect: %v", err)
	}
}
