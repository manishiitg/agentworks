package main

import (
	"bytes"
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
