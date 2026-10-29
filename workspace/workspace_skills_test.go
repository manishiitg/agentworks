package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// Drive the real HTTP/filesystem path: migrate only saved attachments, discover
// native installs, report step usage, then uninstall without touching another
// workflow or allowing an old library copy to resurrect the skill.
func TestWorkspaceSkillsLifecycle(t *testing.T) {
	docs := t.TempDir()
	old := viper.GetString("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", old) })
	write := func(rel string, data []byte) {
		t.Helper()
		dest := filepath.Join(docs, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	skill := func(name string) []byte {
		return []byte("---\nname: " + name + "\ndescription: test skill\n---\nBody.\n")
	}
	for _, workflow := range []string{"Workflow/mine", "Workflow/other"} {
		write(workflow+"/workflow.json", []byte(`{"capabilities":{"selected_skills":["report"],"selected_servers":["keep"]},"unrelated":"keep"}`))
		write(workflow+"/planning/step_config.json", []byte(`{"steps":[{"id":"step-1","title":"Publish","agent_configs":{"enabled_skills":["report","native"],"temperature":0.5}}]}`))
	}
	write("skills/report/SKILL.md", skill("report"))
	asset := []byte{0, 255, 128, 42}
	write("skills/report/assets/chart.bin", asset)
	write("skills/unused/SKILL.md", skill("unused"))
	write("Workflow/mine/.agents/skills/native/SKILL.md", skill("native"))
	write("Workflow/mine/skills-lock.json", []byte(`{"skills":{"native":{"source":"example/recorded-repo","sourceType":"github"}}}`))
	write("Workflow/mine/.claude/skills/report/SKILL.md", skill("report"))
	write("Workflow/mine/.claude/skills/report/.agentworks-managed", []byte("managed"))
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/list", handleWorkspaceSkillList)
	router.POST("/delete", handleWorkspaceSkillDelete)
	router.POST("/import", handleWorkspaceSkillImport)
	router.POST("/files", handleWorkspaceSkillFiles)
	server := httptest.NewServer(router)
	defer server.Close()
	call := func(route, body string, want int) []byte {
		t.Helper()
		resp, err := http.Post(server.URL+route, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s: %d %s", route, resp.StatusCode, data)
		}
		return data
	}
	list := call("/list", `{"workspace_path":"Workflow/mine"}`, 200)
	var inventory struct {
		Documents []workspaceSkillDocument `json:"documents"`
		Usage     map[string][]string      `json:"usage"`
	}
	if err := json.Unmarshal(list, &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory.Documents) != 2 || len(inventory.Usage["report"]) != 2 || inventory.Usage["native"][0] != "Step: Publish" {
		t.Fatalf("inventory: %s", list)
	}
	for _, doc := range inventory.Documents {
		if doc.Name == "native" && (doc.Source != "example/recorded-repo" || doc.SourceType != "github") {
			t.Fatalf("recorded provenance lost: %+v", doc)
		}
		if doc.Name == "report" && doc.Source != "" {
			t.Fatal("invented a source for a file with no installation record")
		}
	}
	filesPayload := call("/files", `{"workspace_path":"Workflow/mine","name":"report"}`, 200)
	var bundle struct {
		Files []struct {
			RelPath string
			Content []byte
		}
	}
	_ = json.Unmarshal(filesPayload, &bundle)
	if len(bundle.Files) != 1 || bundle.Files[0].RelPath != "assets/chart.bin" || !bytes.Equal(bundle.Files[0].Content, asset) {
		t.Fatalf("runtime bundle lost binary assets: %s", filesPayload)
	}
	got, err := os.ReadFile(filepath.Join(docs, "Workflow/mine/skills/report/assets/chart.bin"))
	if err != nil || !bytes.Equal(got, asset) {
		t.Fatalf("migration lost binary assets: %v %v", got, err)
	}
	// Step/global how-to guides can themselves be called SKILL.md. Only the
	// root guide is supplied separately; nested guides must survive packaging.
	stepGuide := []byte("# Login procedure\nFollow the saved login steps.\n")
	globalGuide := []byte("# Shared procedure\nUse the shared references.\n")
	write("Workflow/mine/skills/report/references/login/SKILL.md", stepGuide)
	write("Workflow/mine/skills/report/references/_global/SKILL.md", globalGuide)
	filesPayload = call("/files", `{"workspace_path":"Workflow/mine","name":"report"}`, 200)
	if err := json.Unmarshal(filesPayload, &bundle); err != nil {
		t.Fatal(err)
	}
	packaged := map[string][]byte{}
	for _, file := range bundle.Files {
		packaged[file.RelPath] = file.Content
	}
	if len(packaged) != 3 || !bytes.Equal(packaged["references/login/SKILL.md"], stepGuide) || !bytes.Equal(packaged["references/_global/SKILL.md"], globalGuide) || !bytes.Equal(packaged["assets/chart.bin"], asset) {
		t.Fatal("runtime bundle omitted nested skill guides or included the root guide")
	}
	write("Workflow/mine/.claude/skills/report/SKILL.md", skill("report"))
	write("Workflow/mine/.claude/skills/report/.agentworks-managed", []byte("managed"))
	call("/list", `{"workspace_path":"Workflow/other"}`, 200)
	call("/delete", `{"workspace_path":"Workflow/mine","name":"report"}`, 200)
	for _, rel := range []string{"skills/report", ".claude/skills/report"} {
		if _, err := os.Stat(filepath.Join(docs, "Workflow/mine", rel)); !os.IsNotExist(err) {
			t.Fatalf("uninstall left %s", rel)
		}
	}
	manifest, _ := skillConfig(filepath.Join(docs, "Workflow/mine"), "workflow.json")
	steps, _ := skillConfig(filepath.Join(docs, "Workflow/mine"), "planning/step_config.json")
	if len(skillUsage(manifest, steps)["report"]) != 0 || manifest["unrelated"] != "keep" {
		t.Fatal("uninstall did not clear only its own references")
	}
	if _, err := os.Stat(filepath.Join(docs, "Workflow/other/skills/report/assets/chart.bin")); err != nil {
		t.Fatal("another workflow lost its skill")
	}
	if strings.Contains(string(call("/list", `{"workspace_path":"Workflow/mine"}`, 200)), `"folder_name":"report"`) {
		t.Fatal("legacy library resurrected uninstalled skill")
	}
	// Scoped imports use staged writes and preserve binary files as well.
	payload, _ := json.Marshal(map[string]interface{}{"workspace_path": "Workflow/mine", "name": "new", "files": map[string][]byte{"SKILL.md": skill("new"), "assets/image.bin": asset}})
	call("/import", string(payload), 200)
	got, err = os.ReadFile(filepath.Join(docs, "Workflow/mine/skills/new/assets/image.bin"))
	if err != nil || !bytes.Equal(got, asset) {
		t.Fatal("import lost binary assets")
	}

	// Crew external tools resolve shared Crew roots rather than workflow paths.
	// The same package handler must keep that install/list/uninstall local.
	write("Crew/team/product.json", []byte(`{"product":"work","id":"team"}`))
	payload, _ = json.Marshal(map[string]interface{}{"workspace_path": "Crew/team", "name": "crew-local", "files": map[string][]byte{"SKILL.md": skill("crew-local"), "assets/image.bin": asset}})
	call("/import", string(payload), 200)
	if !strings.Contains(string(call("/list", `{"workspace_path":"Crew/team"}`, 200)), `"folder_name":"crew-local"`) {
		t.Fatal("Crew install missing from its inventory")
	}
	call("/delete", `{"workspace_path":"Crew/team","name":"crew-local"}`, 200)
	if _, err := os.Stat(filepath.Join(docs, "Crew/team/skills/crew-local")); !os.IsNotExist(err) {
		t.Fatal("Crew uninstall left its files")
	}
	if _, err := os.Stat(filepath.Join(docs, "Workflow/mine/skills/new/assets/image.bin")); err != nil {
		t.Fatal("Crew uninstall changed a workflow")
	}
}

func TestWorkspaceSkillsRefuseEscapesAndManagedUninstall(t *testing.T) {
	docs := t.TempDir()
	old := viper.GetString("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", old) })
	project := filepath.Join(docs, "_users/bob/Chats/Code/projects/mine")
	victim := filepath.Join(docs, "_users/alice/Chats/Code/projects/private")
	for _, folder := range []string{project, victim, filepath.Join(project, ".agents/skills/managed")} {
		if err := os.MkdirAll(folder, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(project, ".agents/skills/managed/SKILL.md"), []byte("---\nname: managed\ndescription: managed\n---\nBody"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".agents/skills/managed/.agentworks-managed"), []byte("managed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(project, "skills")); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/delete", handleWorkspaceSkillDelete)
	router.POST("/import", handleWorkspaceSkillImport)
	router.POST("/files", handleWorkspaceSkillFiles)
	call := func(route, body string, want int) {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("POST", route, strings.NewReader(body)))
		if rec.Code != want {
			t.Fatalf("%s %d %s", route, rec.Code, rec.Body.String())
		}
	}
	const scope = "_users/bob/Chats/Code/projects/mine"
	call("/delete", `{"workspace_path":"`+scope+`","name":"../private"}`, 400)
	call("/delete", `{"workspace_path":"`+scope+`","name":"managed"}`, 409)
	payload, _ := json.Marshal(map[string]interface{}{"workspace_path": scope, "name": "x", "files": map[string][]byte{"SKILL.md": []byte("body")}})
	call("/import", string(payload), 400)
	if entries, _ := os.ReadDir(victim); len(entries) != 0 {
		t.Fatal("a workspace link wrote another user's files")
	}
}

// A legacy skill holding a link is skipped, not fatal: an error here failed every skill call for the workspace,
// the runtime's included, on every call, so steps silently ran without any of their skills.
func TestWorkspaceSkillMigrationSkipsALinkedLegacySkill(t *testing.T) {
	docs := t.TempDir()
	old := viper.GetString("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", old) })
	write := func(rel, data string) {
		t.Helper()
		dest := filepath.Join(docs, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("Workflow/w/workflow.json", `{"capabilities":{"selected_skills":["good","linked"]}}`)
	write("skills/good/SKILL.md", "---\nname: good\ndescription: d\n---\nBody.\n")
	write("skills/linked/SKILL.md", "---\nname: linked\ndescription: d\n---\nBody.\n")
	if err := os.Symlink("/etc/hostname", filepath.Join(docs, "skills/linked/escape")); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/list", handleWorkspaceSkillList)
	server := httptest.NewServer(router)
	defer server.Close()
	resp, err := http.Post(server.URL+"/list", "application/json", strings.NewReader(`{"workspace_path":"Workflow/w"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("one linked legacy skill must not fail the workspace: %d %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(docs, "Workflow/w/skills/good/SKILL.md")); err != nil {
		t.Fatalf("the clean skill was not migrated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(docs, "Workflow/w/skills/linked")); !os.IsNotExist(err) {
		t.Fatalf("the linked skill must not be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(docs, "Workflow/w/.workspace-skills-v1")); err != nil {
		t.Fatalf("the migration must complete: %v", err)
	}
}

func TestBrainSkillSourceReportsEditsSinceInstall(t *testing.T) {
	dir := t.TempDir()
	body := []byte("# skill\n")
	sum := sha256.Sum256(body)
	marker := `{"folder":"Shared/Skills/x","version":"v_1","files":{"SKILL.md":"` + hex.EncodeToString(sum[:]) + `"}}`
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), body, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, brainSkillMarker), []byte(marker), 0644); err != nil {
		t.Fatal(err)
	}
	if got, edited := brainSkillSource(dir); got == "" || edited {
		t.Fatalf("untouched install: source %q edited %v", got, edited)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, edited := brainSkillSource(dir); !edited {
		t.Fatal("a changed file must count as edited")
	}
}

// A Crew bound to a laptop folder keeps its skills in <folder>/.agentworks/skills
// and its settings in private storage. The inventory read only the docs folder,
// so a saved skill there could not be selected (skills action=select refused it).
func TestWorkspaceSkillsReadABoundCrewsResourceRoot(t *testing.T) {
	base := t.TempDir()
	base, _ = filepath.EvalSymlinks(base)
	docs, project, state := filepath.Join(base, "docs"), filepath.Join(base, "project"), filepath.Join(base, "state")
	private := filepath.Join(docs, "_system", "crew-data", "stable-id")
	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	skill := func(name string) string { return "---\nname: " + name + "\ndescription: test skill\n---\nBody.\n" }
	write(filepath.Join(project, ".agentworks", "skills", "review", "SKILL.md"), skill("review"))
	write(filepath.Join(project, ".agentworks", "skills", "review", "notes.md"), "notes")
	write(filepath.Join(project, ".claude", "skills", "native", "SKILL.md"), skill("native"))
	write(filepath.Join(project, "skills", "unrelated", "SKILL.md"), skill("unrelated"))
	write(filepath.Join(private, "workflow.json"), `{"capabilities":{"selected_skills":["review"]}}`)
	raw, _ := json.Marshal(map[string]any{"projects": map[string]any{"work/example": map[string]any{"product": "work", "folder": "example", "owner_id": "owner", "project_id": "stable-id", "local_root": project}}})
	write(filepath.Join(state, "ownership", "projects.json"), string(raw))
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	old := viper.GetString("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", old) })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/list", handleWorkspaceSkillList)
	router.POST("/files", handleWorkspaceSkillFiles)
	call := func(route, body string) []byte {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("POST", route, strings.NewReader(body)))
		if rec.Code != 200 {
			t.Fatalf("%s %d %s", route, rec.Code, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	var inventory struct {
		Documents []workspaceSkillDocument `json:"documents"`
	}
	if err := json.Unmarshal(call("/list", `{"workspace_path":"Crew/example"}`), &inventory); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, doc := range inventory.Documents {
		paths[doc.Name] = doc.FilePath
		if doc.Name == "review" && (len(doc.UsedBy) != 1 || doc.UsedBy[0] != "Main chat") {
			t.Fatalf("selection in private workflow.json not reported: %+v", doc)
		}
	}
	if len(paths) != 2 || paths["review"] != "skills/review/SKILL.md" || paths["native"] != "_files/.claude/skills/native/SKILL.md" {
		t.Fatalf("bound Crew inventory: %v", paths)
	}
	if !strings.Contains(string(call("/files", `{"workspace_path":"Crew/example","name":"review"}`)), "notes.md") {
		t.Fatal("bound Crew skill bundle missing its files")
	}
}
