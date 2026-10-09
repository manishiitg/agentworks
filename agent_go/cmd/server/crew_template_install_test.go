package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// Installing a template into an existing Crew keeps a file the owner edited,
// writes the missing ones and records the template; creation stays strict
// (2026-10-09 review of PLAT-738).
func TestInstallTemplateKeepsEditedFiles(t *testing.T) {
	files := func() map[string]string {
		return map[string]string{
			"Crew/demo/product.json":         `{"id":"demo","product":"work","schema_version":1}`,
			"Crew/demo/skills/tmpl/SKILL.md": "edited by the owner",
		}
	}
	item := &crewAgentTemplate{ID: "tmpl", Version: 1, SelectedSkills: []string{"tmpl"}, Files: map[string]string{
		"skills/tmpl/SKILL.md":    "template original",
		"templates/tmpl/setup.md": "setup",
	}}
	mock := &mockWorkspaceAPI{files: files()}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	if err := applyCrewAgentTemplateFiles(context.Background(), "Crew/demo", item, true); err != nil {
		t.Fatalf("install into an edited Crew: %v", err)
	}
	mock.mu.Lock()
	skill, setup, manifest := mock.files["Crew/demo/skills/tmpl/SKILL.md"], mock.files["Crew/demo/templates/tmpl/setup.md"], mock.files["Crew/demo/product.json"]
	mock.mu.Unlock()
	if skill != "edited by the owner" || setup != "setup" || !strings.Contains(manifest, `"tmpl"`) {
		t.Fatalf("skill=%q setup=%q manifest=%s", skill, setup, manifest)
	}
	strict := &mockWorkspaceAPI{files: files()}
	ws2 := httptest.NewServer(strict)
	t.Cleanup(ws2.Close)
	t.Setenv("WORKSPACE_API_URL", ws2.URL)
	if err := applyCrewAgentTemplate(context.Background(), "Crew/demo", item); err == nil {
		t.Fatal("creation path accepted a differing file")
	}
}
