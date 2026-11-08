package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relaypython"
	workspaceclient "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
)

// PLAT-746-265: an admitted Run person may execute a script, but cannot edit
// shared source. A workflow reference cannot select another Crew's source.
func TestCrewScriptSourceUsesParentAuthorityAndReferencesStayInCrew(t *testing.T) {
	docs := t.TempDir()
	docs, _ = filepath.EvalSymlinks(docs)
	previousDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", previousDocs) })
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	resetCrewOwnerCache()
	t.Cleanup(resetCrewOwnerCache)
	t.Setenv("AUTH_SECRET", "crew-script-test-auth-secret-01234567890123456789")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"},{"id":"runner","username":"runner","role":"creator"}]}`)
	for _, record := range []projectOwnerRecord{{Product: "work", Folder: "lab", ProjectID: "crew-lab", OwnerID: "owner", Shared: true, Private: true, AudienceConfigured: true, RunPeople: []string{"runner"}}, {Product: "work", Folder: "other", ProjectID: "crew-other", OwnerID: "owner", Shared: true, Private: true, AudienceConfigured: true}} {
		if err := defaultProjectOwners().Register(record); err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{filepath.Join(docs, "Crew", record.Folder), filepath.Join(docs, "_system/crew-data", record.ProjectID)} {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	script := crewExecutableBinding{ID: "qualify", CrewID: "crew-lab", CrewRoot: "Crew/lab", Root: "Workflow/qualify", Kind: "script"}
	other := crewExecutableBinding{ID: "other-script", CrewID: "crew-other", CrewRoot: "Crew/other", Root: "Workflow/other-script", Kind: "script"}
	parent := crewExecutableBinding{ID: "sequence", CrewID: "crew-lab", CrewRoot: "Crew/lab", Root: "Workflow/sequence", Kind: "workflow"}
	for _, binding := range []crewExecutableBinding{script, other, parent} {
		if err := registerCrewExecutable(binding); err != nil {
			t.Fatal(err)
		}
	}
	files := &mockWorkspaceAPI{files: map[string]string{"Workflow/qualify/code/script.json": `{"id":"qualify","name":"Qualify","inputs":[{"name":"count","type":"integer","value":"2"}]}`, "Workflow/qualify/code/main.py": "print('saved')\n", "Workflow/other-script/code/script.json": `{"id":"other-script","name":"Other"}`, "Workflow/other-script/code/main.py": "print('private')\n"}}
	files.files["Crew/lab/product.json"] = `{"product":"work","id":"crew-lab","capabilities":{"browser_mode":"headless","llm_config":{"schema_version":1,"mode":"provider_profile","provider":"codex-cli"},"selected_servers":["example-app"],"selected_tools":["example-app:read"],"selected_global_secret_names":["EXAMPLE_CREDENTIAL"]}}`
	// Managed input saves use the same anchored staged writer as production.
	for file := range files.files {
		materializeWorkspaceFixtureFile(t, files, docs, file)
	}
	// The external checker is the only executable boundary fixture. Source
	// writes, runtime routing, SDK source, inspection and notes are production.
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	checkerDir := t.TempDir()
	checker := filepath.Join(checkerDir, "pyright")
	fixture := "#!" + python + "\n" + `import json,os,sys
from pathlib import Path
assert not any(k.startswith(('SECRET_', 'VLT_', 'MCP_')) for k in os.environ)
config_file=Path(sys.argv[sys.argv.index('--project')+1])
config=json.loads(config_file.read_text())
assert len(config['extraPaths'])==2 and str(Path.cwd()) in config['extraPaths']
assert sum((Path(p)/'agentworks/__init__.py').is_file() for p in config['extraPaths'])==1
assert all(not Path(p).is_absolute() and not (config_file.parent/p).is_symlink() and (config_file.parent/p).resolve().is_relative_to(Path.cwd()) for p in config['include'])
source=Path.cwd()/'main.py'
print(json.dumps({'summary':{'filesAnalyzed':len(config['include'])},'generalDiagnostics':[{'file':str(source),'message':'Use an integer; MCP_API_TOKEN=fixture-credential','range':{'start':{'line':2}}}]}))
sys.exit(1)
`
	if err := os.WriteFile(checker, []byte(fixture), 0755); err != nil {
		t.Fatal(err)
	}
	sdk, err := filepath.Abs("../../../workspace/security/runtime")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Any("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.POST("/api/execute", func(c *gin.Context) {
		var params workspaceclient.ExecuteShellCommandParams
		if err := c.ShouldBindJSON(&params); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		provided, _ := base64.StdEncoding.DecodeString(params.ExtraEnv["VAR_AUTOMATION_CHECK_FILES"])
		if strings.Contains(string(provided), `"outside.py"`) {
			t.Error("source inspection followed a link into another Crew")
			c.JSON(400, gin.H{"error": "source link refused"})
			return
		}
		guard := params.FolderGuard
		if guard == nil || !guard.StrictAllowlist || !guard.DenyNetwork || len(guard.ReadPaths) != 1 || guard.ReadPaths[0] != script.Root+"/code" || len(guard.WritePaths) != 0 {
			c.JSON(400, gin.H{"error": "inspection lost its source-only no-network policy"})
			return
		}
		layout, found, err := crewroot.LookupExecutable(docs, script.Root)
		if err != nil || !found {
			c.JSON(500, gin.H{"error": "source unavailable"})
			return
		}
		cmd := exec.CommandContext(c.Request.Context(), python, "-I", "-c", relaypython.SourceInspector)
		cmd.Dir = layout.Code
		cmd.Env = []string{"PATH=" + checkerDir, "PYTHONPATH=" + sdk, "SECRET_FIXTURE=must-not-reach-checker", "MCP_AUTH=fixture-only"}
		for key, value := range params.ExtraEnv {
			if strings.HasPrefix(key, "VAR_") {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		runErr := cmd.Run()
		exit := 0
		if runErr != nil {
			exit = 1
		}
		c.JSON(200, gin.H{"success": true, "data": workspaceclient.ShellCommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exit}})
	})
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	api := &StreamingAPI{}
	endpoint := httptest.NewServer(AuthMiddleware(http.HandlerFunc(api.handleCrewScriptFile)))
	defer endpoint.Close()
	lastReply := ""
	request := func(user, content string, names ...string) int {
		t.Helper()
		token, err := GenerateJWT(user, user, "")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]string{"content": content})
		file := "main.py"
		if len(names) > 0 {
			file = names[0]
		}
		req, _ := http.NewRequest(http.MethodPut, endpoint.URL+"/api/crew-script-file?workspace_path="+script.Root+"&file="+file, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		bodyText, _ := io.ReadAll(response.Body)
		lastReply = string(bodyText)
		t.Logf("source edit user=%s HTTP%d %s", user, response.StatusCode, strings.TrimSpace(string(bodyText)))
		return response.StatusCode
	}
	if code := request("runner", "print('forged')"); code != 403 {
		t.Fatalf("Run-person edit HTTP%d", code)
	}
	if code := request("owner", "print('owner update')\n"); code != 200 {
		t.Fatalf("Builder edit HTTP%d", code)
	}
	var savedWithNotes struct {
		Success   bool     `json:"success"`
		Notes     []string `json:"notes"`
		TypeNotes []struct {
			File, Message string
			Line          int
		} `json:"type_notes"`
	}
	if err := json.Unmarshal([]byte(lastReply), &savedWithNotes); err != nil || !savedWithNotes.Success || len(savedWithNotes.TypeNotes) != 1 || savedWithNotes.TypeNotes[0].File != "main.py" || savedWithNotes.TypeNotes[0].Line != 3 || strings.Contains(lastReply, "fixture-credential") {
		t.Fatalf("checker diagnostics changed save success or lost normalized/redacted notes: %s %v", lastReply, err)
	}
	if saved, _, err := readFileFromWorkspace(t.Context(), script.Root+"/code/main.py"); err != nil || saved != "print('owner update')\n" {
		t.Fatal("checker errors prevented source save", err)
	}
	// A planted source link must not give inspection a read into another
	// Crew. The source resolver must refuse or omit it, and checker includes
	// must never follow it; inspecting the remaining safe files is allowed.
	layout, foundLayout, layoutErr := crewroot.LookupExecutable(docs, script.Root)
	otherLayout, otherFound, otherErr := crewroot.LookupExecutable(docs, other.Root)
	if layoutErr != nil || otherErr != nil || !foundLayout || !otherFound {
		t.Fatalf("source layouts unavailable: %v %v", layoutErr, otherErr)
	}
	link := filepath.Join(layout.Code, "outside.py")
	if err := os.Symlink(filepath.Join(otherLayout.Code, "main.py"), link); err != nil {
		t.Fatal(err)
	}
	if code := request("owner", "print('owner update')\n"); code != 200 || strings.Contains(lastReply, "outside.py") {
		t.Fatalf("source symlink reached inspection or prevented save: HTTP%d %s", code, lastReply)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	zeroFiles := strings.Replace(fixture, "len(config['include'])", "0", 1)
	if err := os.WriteFile(checker, []byte(zeroFiles), 0755); err != nil {
		t.Fatal(err)
	}
	if code := request("owner", "print('owner update')\n"); code != 200 || !strings.Contains(lastReply, "did not analyze any files") {
		t.Fatalf("zero analyzed files silently reported clean or blocked save: HTTP%d %s", code, lastReply)
	}
	if err := os.Remove(checker); err != nil {
		t.Fatal(err)
	}
	if code := request("owner", "print('owner update')\n"); code != 200 || !strings.Contains(lastReply, "checker is not installed") {
		t.Fatalf("missing checker blocked save or prompted installation: HTTP%d %s", code, lastReply)
	}

	// PLAT-746-265 / PR498: ownership alone cannot restore revoked account rights.
	originalDirectory := *directory
	for _, record := range []string{
		`{"id":"owner","username":"owner","role":"viewer"}`,
		`{"id":"owner","username":"owner","role":"creator","disabled":true}`,
		`{"id":"owner","username":"owner","role":"creator","products":["agentworks"]}`,
	} {
		*directory = `{"users":[` + record + `]}`
		invalidateUserDirectoryCache()
		if code := request("owner", "print('revoked')"); code != 403 {
			t.Errorf("revoked account source edit HTTP%d: %s", code, record)
		}
	}
	*directory = originalDirectory
	invalidateUserDirectoryCache()
	if code := request("owner", "print('owner update')\n"); code != 200 {
		t.Fatalf("restored Builder edit HTTP%d", code)
	}
	// PLAT-746-459: helper packages use the same source writer and authority.
	if code := request("runner", "print('forged')", "helper/values.py"); code != 403 {
		t.Fatalf("Run person edited nested source HTTP%d", code)
	}
	if code := request("owner", "VALUE = 7\n", "helper/values.py"); code != 200 {
		t.Fatalf("Builder nested source HTTP%d", code)
	}
	// PLAT-746-479 / PR1567: packages use the same native source writer.
	const requirements = "# saved packages\nhttpx==0.28.1\n"
	if code := request("runner", requirements, "requirements.txt"); code != 403 {
		t.Fatalf("Run person edited requirements HTTP%d", code)
	}
	if code := request("owner", requirements, "requirements.txt"); code != 200 {
		t.Fatalf("Builder requirements edit HTTP%d", code)
	}
	for _, content := range []string{"-r extra.txt\n", " -c constraints.txt\n", "\ufeff--requirement=extra.txt\n", "\\\n-r extra.txt\n", "--index-url https://example.com/simple\n", "bad\x00text", strings.Repeat("x", (64<<10)+1)} {
		if code := request("owner", content, "requirements.txt"); code != 400 {
			t.Fatalf("invalid requirements admitted HTTP%d", code)
		}
	}
	if saved, _, err := readFileFromWorkspace(t.Context(), script.Root+"/code/requirements.txt"); err != nil || saved != requirements {
		t.Fatal("refused requirements changed saved source", err)
	}
	for _, file := range []string{"../outside.py", "/outside.py", "helper/../../outside.py", "helper/../outside.py", "helper/.private.py", "helper\\outside.py", "helper/script.json", "helper/requirements.txt"} {
		if code := request("owner", "refused", file); code != 400 {
			t.Fatalf("source path %q admitted HTTP%d", file, code)
		}
	}
	manifest, found, err := ReadWorkflowManifest(context.Background(), script.Root)
	if err != nil || !found {
		t.Fatal(err)
	}
	if manifest.Capabilities.BrowserMode != "headless" || manifest.Capabilities.LLMConfig == nil || manifest.Capabilities.LLMConfig.Provider != "codex-cli" || len(manifest.Capabilities.SelectedServers) != 1 || manifest.Capabilities.SelectedGlobalSecretNames == nil || len(*manifest.Capabilities.SelectedGlobalSecretNames) != 1 {
		t.Fatal("saved script lost the Crew's model/browser/MCP/secret-name selections")
	}
	// PLAT-746-500: retiring upgrade metadata must keep the real manifest
	// read and missing-secret notice for admitted owners and Run people.
	manifestEndpoint := httptest.NewServer(AuthMiddleware(http.HandlerFunc(api.handleGetWorkflowManifest)))
	defer manifestEndpoint.Close()
	for _, user := range []string{"owner", "runner"} {
		token, err := GenerateJWT(user, user, "")
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodGet, manifestEndpoint.URL+"?workspace_path="+script.Root, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var reply map[string]json.RawMessage
		err = json.NewDecoder(response.Body).Decode(&reply)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || err != nil {
			t.Fatalf("manifest read user=%s HTTP%d: %v", user, response.StatusCode, err)
		}
		if _, exists := reply["contract_upgrade"]; exists {
			t.Fatal("retired contract_upgrade exposed by manifest read")
		}
		var missing []string
		if err := json.Unmarshal(reply["missing_secrets"], &missing); err != nil || len(missing) != 1 || missing[0] != "EXAMPLE_CREDENTIAL" {
			t.Fatalf("manifest read lost selected missing-secret names: %s %v", reply["missing_secrets"], err)
		}
	}
	changed := `{"id":"qualify","name":"Qualify","secrets":["FIXTURE_KEY"],"inputs":[{"name":"count","type":"integer","value":"2"}],"capabilities":{"browser_mode":"extension"}}`
	if code := request("runner", changed, "script.json"); code != 403 {
		t.Fatalf("Run person changed script settings HTTP%d", code)
	}
	if code := request("owner", changed, "script.json"); code != 200 {
		t.Fatalf("Builder setting HTTP%d", code)
	}
	manifest, _, err = ReadWorkflowManifest(context.Background(), script.Root)
	if err != nil || len(manifest.DeclaredSecrets) != 1 || manifest.DeclaredSecrets[0] != "FIXTURE_KEY" {
		t.Fatalf("saved script declaration names missing from public manifest: %v", err)
	}
	if err != nil || manifest.Capabilities.BrowserMode != "extension" || manifest.Capabilities.LLMConfig == nil || manifest.Capabilities.LLMConfig.Provider != "codex-cli" || manifest.Capabilities.SelectedGlobalSecretNames == nil || len(*manifest.Capabilities.SelectedGlobalSecretNames) != 1 {
		t.Fatalf("browser override erased other settings: %v", err)
	}
	if code := request("owner", strings.Replace(changed, "extension", "auto", 1), "script.json"); code != 400 {
		t.Fatalf("invalid browser setting saved HTTP%d", code)
	}
	if workflowAccessForManifest(&UserClaims{UserID: "runner", Username: "runner"}, manifest) != WorkflowAccessRead {
		t.Fatal("Run person lost admitted execution access")
	}
	resolver := &crewWorkflowResources{userID: "owner"}
	resolved, err := resolver.ResolveCrewScript(context.Background(), parent.Root, "script:qualify")
	if err != nil || string(resolved.Files["main.py"]) != "print('owner update')\n" || string(resolved.Files["helper/values.py"]) != "VALUE = 7\n" || string(resolved.Files["requirements.txt"]) != requirements {
		t.Fatalf("same-Crew script: %+v %v", resolved, err)
	}
	if _, err := resolver.ResolveCrewScript(context.Background(), parent.Root, "other-script"); err == nil || !strings.Contains(err.Error(), "shared function") {
		t.Fatal("cross-Crew source reference accepted", err)
	}
	if _, err := resolver.ResolveCrewScript(context.Background(), parent.Root, "../../other"); err == nil {
		t.Fatal("script id selected a filesystem path")
	}
	if _, present, err := readFileFromWorkspace(t.Context(), script.Root+"/workflow.json"); err != nil || present {
		t.Fatal("script projection persisted a duplicate manifest")
	}
	// All existing entry types persist against the one script definition.
	functionRecord := projectFunctionDefinition{ID: "call", Name: "qualify", Enabled: true, Action: projectactions.Action{Type: "run", Tab: "main"}, Input: json.RawMessage(`{}`), Inputs: []string{"count"}}
	if err := writeProjectFunctionDefinitions(context.Background(), script.Root, []projectFunctionDefinition{functionRecord}); err != nil {
		t.Fatal(err)
	}
	projected, found, err := ReadWorkflowManifest(context.Background(), script.Root)
	if err != nil || !found || len(projected.Schedules) != 1 {
		t.Fatal("script function projection", err)
	}
	values, _, err := workflowFunctionArgs(projected.Schedules[0], map[string]interface{}{"count": 3})
	if err != nil || values["count"] != "3" {
		t.Fatal("script function input", err)
	}
	if err := WriteWorkflowManifest(context.Background(), script.Root, projected); err != nil {
		t.Fatal(err)
	}
	if _, present, err := readFileFromWorkspace(t.Context(), script.Root+"/workflow.json"); err != nil || present {
		t.Fatal("entry save wrote a duplicate script manifest")
	}
	definition, err := readWorkflowInputDefinitions(context.Background(), script.Root)
	if err != nil || len(definition.Variables) != 1 {
		t.Fatal("script lost its input contract", err)
	}
	if err := writeProjectFunctionDefinitions(context.Background(), script.Root, nil); err != nil {
		t.Fatal(err)
	}
	files.mu.Lock()
	files.files[parent.Root+"/planning/plan.json"] = `{"steps":[{"id":"use","type":"scripted","title":"Qualify step","description":"Use saved code","script":"qualify","script_inputs":{"count":{"value":3}}}]}`
	files.mu.Unlock()
	materializeWorkspaceFixtureFile(t, files, docs, parent.Root+"/planning/plan.json")
	if err := saveCrewScriptInputs(context.Background(), script.Root, &workflow.VariablesManifest{Variables: []workflow.Variable{{Name: "count", Type: "string"}}}); err == nil || !strings.Contains(err.Error(), "Qualify step") {
		t.Fatal("breaking script input edit did not identify the referencing step", err)
	}
	// Referenced specialist definitions live outside the ordinary step list.
	files.mu.Lock()
	files.files[parent.Root+"/planning/plan.json"] = `{"steps":[],"orphan_steps":[{"id":"specialist","type":"scripted","title":"Saved specialist","description":"Uses shared code","script":"qualify","script_inputs":{"count":{"value":3}}}]}`
	files.mu.Unlock()
	materializeWorkspaceFixtureFile(t, files, docs, parent.Root+"/planning/plan.json")
	if err := saveCrewScriptInputs(context.Background(), script.Root, &workflow.VariablesManifest{Variables: []workflow.Variable{{Name: "count", Type: "string"}}}); err == nil || !strings.Contains(err.Error(), "Saved specialist") {
		t.Errorf("specialist input edit lost consumer validation: %v", err)
	}
	if err := saveCrewScriptInputs(context.Background(), script.Root, &workflow.VariablesManifest{Variables: []workflow.Variable{{Name: "count", Type: "integer", Value: "4"}}}); err != nil {
		t.Fatal(err)
	}
	if err := saveCrewScriptInputs(context.Background(), script.Root, &workflow.VariablesManifest{Variables: []workflow.Variable{{Name: "count", Type: "integer", Required: true}}}); err != nil {
		t.Fatal(err)
	}
	projected, _, err = ReadWorkflowManifest(context.Background(), script.Root)
	if err != nil {
		t.Fatal(err)
	}
	projected.Schedules = []WorkflowSchedule{{ID: "timer", Name: "Required input timer", ScheduleType: "cron", CronExpression: "0 0 * * *", Timezone: "UTC", Enabled: true}}
	if err := WriteWorkflowManifest(context.Background(), script.Root, projected); err == nil || !strings.Contains(strings.ToLower(err.Error()), "count") {
		t.Errorf("enabled script timer accepted missing required input: %v", err)
	} else {
		response := httptest.NewRecorder()
		writeScheduleSaveError(response, err)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("missing input looked like server failure: HTTP%d", response.Code)
		}
	}
	// Converted timers keep the selected named input set using the same
	// script source contract; a preset is not a separate workflow.
	if err := saveCrewScriptInputs(context.Background(), script.Root, &workflow.VariablesManifest{Variables: []workflow.Variable{{Name: "count", Type: "integer", Required: true}}, Groups: []workflow.VariableGroup{{Name: "batch", Values: map[string]string{"count": "6"}}}}); err != nil {
		t.Fatal(err)
	}
	projected.Schedules[0].InputPreset = "batch"
	if err := WriteWorkflowManifest(context.Background(), script.Root, projected); err != nil {
		t.Fatalf("saved script preset timer refused: %v", err)
	}
	projected.Schedules[0].InputPreset = ""
	projected.Schedules[0].Inputs = map[string]string{"count": "3"}
	if err := WriteWorkflowManifest(context.Background(), script.Root, projected); err != nil {
		t.Fatalf("complete timer inputs refused: %v", err)
	}
	if _, present, err := readFileFromWorkspace(t.Context(), script.Root+"/variables/variables.json"); err != nil || present {
		t.Fatal("script wrote a duplicate input definition")
	}
	// PLAT-746: a closed destination must block running, but never block Pause.
	projected.Schedules[0].ChatID = "closed-chat"
	if err := WriteWorkflowManifest(context.Background(), script.Root, projected); err == nil {
		t.Fatal("enabled timer accepted an unavailable chat")
	}
	projected.Schedules[0].Enabled = false
	if err := WriteWorkflowManifest(context.Background(), script.Root, projected); err != nil {
		t.Fatalf("could not pause timer after its chat closed: %v", err)
	}
}
