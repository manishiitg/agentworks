package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

// A company skill published to Brain is found by search_skills and installed into a workspace by
// install_skill(source="brain:<folder>"): the server reads it through the caller's Brain access and sends every file,
// binary assets byte for byte, to the workspace's skill import (PLAT-576).
func TestBrainSkillSearchAndInstallIntoWorkspace(t *testing.T) {
	_, service := knowledgebaseServerTest(t)
	admin := knowledgebase.Principal{IdentityID: "admin", IsAdmin: true}
	for _, f := range []map[string]any{{"folder_path": "", "name": "Company", "request_id": "company"}, {"folder_path": "Company", "name": "Skills", "request_id": "skills"}} {
		if _, err := service.Call(t.Context(), admin, "create_knowledgebase_folder", f); err != nil {
			t.Fatal(err)
		}
	}
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 7}
	files := []any{
		map[string]any{"path": "SKILL.md", "content": "---\nname: release-notes\ndescription: Release notes from merged PRs\n---\nSteps.\n"},
		map[string]any{"path": "assets/logo.png", "content_base64": base64.StdEncoding.EncodeToString(png)},
	}
	if _, err := service.CallTool(t.Context(), admin, "brain_skills", map[string]any{"action": "publish", "folder_path": "Company/Skills/release-notes", "files": files, "request_id": "publish"}); err != nil {
		t.Fatal(err)
	}
	var imported struct {
		WorkspacePath string            `json:"workspace_path"`
		Name          string            `json:"name"`
		Files         map[string][]byte `json:"files"`
	}
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/skills/workspace/import" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&imported)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer workspace.Close()
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "admin", Username: "admin"})
	if found := brainSkillSearch(ctx, "release"); !strings.Contains(found, "brain:Company/Skills/release-notes") {
		t.Fatalf("search_skills must list the company skill: %q", found)
	}
	message, err := installBrainSkill(ctx, workspace.URL, "Workflow/w", "Company/Skills/release-notes", "")
	if err != nil {
		t.Fatal(err)
	}
	if imported.WorkspacePath != "Workflow/w" || imported.Name != "release-notes" || !strings.Contains(string(imported.Files["SKILL.md"]), "Release notes from merged PRs") || string(imported.Files["assets/logo.png"]) != string(png) {
		t.Fatalf("install sent %+v (%s)", imported, message)
	}
	if source, ok := brainSkillFolder("brain:/Company/Skills/release-notes/"); !ok || source != "Company/Skills/release-notes" {
		t.Fatalf("brain source = %q %v", source, ok)
	}
}
