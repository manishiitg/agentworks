package server

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// Website and MCP use the same serialized service, including Code-origin
// Crews. Concurrent removals cannot restore one another's receipt or skills.
func TestCrewTemplateWebsiteMCPPreservesWork(t *testing.T) {
	f := newMultiUserFixture(t, legacyIdentityLayout())
	root := f.Layout.CodePhysical(fixtureUserA, fixtureCodeFolder)
	ctx := f.Ctx(fixtureUserA)
	finance, err := loadCrewAgentTemplate("finance-analyst")
	if err != nil {
		t.Fatal(err)
	}
	tax, err := loadCrewAgentTemplate("tax-export")
	if err != nil {
		t.Fatal(err)
	}
	call := func(action, id string, website bool) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/templates", strings.NewReader(`{"action":"`+action+`","template_id":"`+id+`"}`)).WithContext(ctx)
		if website {
			req = mux.SetURLVars(req, map[string]string{"id": "code", "project_id": fixtureCodeID})
			f.API.handleAgentProfileProjectTemplate(rec, req)
		} else {
			f.API.externalCrewProjectCall(rec, req, map[string]any{"action": action, "template_id": id}, "code:"+fixtureCodeID)
		}
		return rec
	}
	rec := call("install_template", finance.ID, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("website install: %d %s", rec.Code, rec.Body.String())
	}
	edited := root + "/skills/finance-analyst/SKILL.md"
	f.put(edited, "Owner edited skill")
	// Earlier releases stored a singleton receipt; adding another keeps it.
	raw, _, _ := readFileFromWorkspace(ctx, root+"/product.json")
	var manifest map[string]interface{}
	_ = json.Unmarshal([]byte(raw), &manifest)
	manifest["template"] = map[string]interface{}{"id": finance.ID, "version": finance.Version}
	delete(manifest, "templates")
	encoded, _ := json.Marshal(manifest)
	f.put(root+"/product.json", string(encoded))
	if err := updateProductSelectedSkills(ctx, "code", root, func(names []string) []string { return append(names, "custom-skill") }); err != nil {
		t.Fatal(err)
	}
	rec = call("install_template", tax.ID, false)
	if rec.Code != http.StatusOK || len(crewInstalledTemplates(ctx, root)) != 2 {
		t.Fatalf("MCP install lost legacy receipt: %d %s", rec.Code, rec.Body.String())
	}
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for _, id := range []string{finance.ID, tax.ID} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); results <- call("remove_template", id, true) }(id)
	}
	wg.Wait()
	close(results)
	for rec := range results {
		if rec.Code != http.StatusOK {
			t.Fatalf("remove: %d %s", rec.Code, rec.Body.String())
		}
	}
	selected, err := productSelectedSkills(ctx, "code", root)
	if err != nil || len(selected) != 1 || selected[0] != "custom-skill" || len(crewInstalledTemplates(ctx, root)) != 0 {
		t.Fatalf("removal state: %v %v", selected, err)
	}
	rec = call("install_template", finance.ID, false)
	saved, _, err := readFileFromWorkspace(ctx, edited)
	if rec.Code != http.StatusOK || err != nil || saved != "Owner edited skill" {
		t.Fatalf("reattach overwrote work: %d %s %v", rec.Code, saved, err)
	}
	// Run access never admits template changes.
	req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/templates", strings.NewReader(`{"action":"remove_template","template_id":"finance-analyst"}`)).WithContext(f.Ctx(fixtureUserB)), map[string]string{"id": "code", "project_id": fixtureCodeID})
	denied := httptest.NewRecorder()
	f.API.handleAgentProfileProjectTemplate(denied, req)
	if denied.Code == http.StatusOK {
		t.Fatal("another person's Run access changed templates")
	}
}
