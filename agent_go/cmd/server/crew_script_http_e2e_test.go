package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	handlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	mcpexecutor "github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/spf13/viper"
)

// PLAT-746-265: authenticated Run-person admission crosses HTTP, uses the live
// parent owner, and executes real Python with no model turn or saved plan.
func TestSharedCrewScriptRunPersonHTTPExecutesRealPython(t *testing.T) {
	if runtime.GOOS == "linux" {
		// Hosted storage is outside the launcher's writable private /tmp.
		// A rebound fixture beneath /tmp inherits that scratch grant.
		root, err := os.MkdirTemp(".", ".script-http-fixture-")
		if err != nil {
			t.Fatal(err)
		}
		root, err = filepath.Abs(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
		t.Setenv("TMPDIR", root)
	}
	for _, local := range []bool{false, true} {
		for _, googleScope := range []string{"shared_default", "selected_workflow", "unselected_private"} {
			t.Run(fmt.Sprintf("attached_project_%v/%s", local, googleScope), func(t *testing.T) { checkSharedCrewScriptHTTP(t, local, googleScope) })
		}
	}
}

func checkSharedCrewScriptHTTP(t *testing.T, local bool, googleScope string) {

	docs := t.TempDir()
	docs, _ = filepath.EvalSymlinks(docs)
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("AUTH_SECRET", "script-http-fixture-secret-01234567890123456789")
	common.SetBridgeTokenSecret("script-http-session-fixture")
	t.Cleanup(func() { common.SetBridgeTokenSecret("") })
	previous := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", previous) })
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"},{"id":"runner","username":"runner","role":"creator"}]}`)
	registerTestCrew(t, "script-http", "owner", true)
	sourceRoot := filepath.Join(docs, "Crew/script-http/.agentworks/code/saved")
	if local {
		project := t.TempDir()
		project, _ = filepath.EvalSymlinks(project)
		if err := os.MkdirAll(filepath.Join(project, ".agentworks"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := defaultProjectOwners().modify(func(records map[string]projectOwnerRecord) (bool, error) {
			rec := records["work/script-http"]
			rec.LocalRoot = project
			records["work/script-http"] = rec
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
		sourceRoot = filepath.Join(project, ".agentworks/code/saved")
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Any("/api/documents/*filepath", handlers.HandleDocumentRequest)
	router.GET("/api/documents", handlers.ListDocuments)
	router.POST("/api/folders", handlers.CreateFolder)
	router.DELETE("/api/folders/*folderpath", handlers.DeleteFolder)
	router.POST("/api/execute", handlers.ExecuteShellCommand)
	router.POST("/api/query", handlers.QueryWorkflowDB)
	router.POST("/api/mutate", handlers.MutateWorkflowDB)
	router.POST("/api/db/initialize", handlers.InitializeWorkflowDB)
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	binding := crewExecutableBinding{ID: "saved", CrewID: "crew-script-http", CrewRoot: "Crew/script-http", Root: "Workflow/script-http-saved", Kind: "script", RuntimeRoot: "workflows/saved"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	// Model the explicit conversion's existing database routing.
	if err := defaultProjectOwners().modify(func(records map[string]projectOwnerRecord) (bool, error) {
		record := records["work/script-http"]
		record.DatabaseRoot = "workflows/saved/db"
		records["work/script-http"] = record
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	executorHandlers := mcpexecutor.NewExecutorHandlers("", loggerv2.NewNoop())
	bridgeRouter := mux.NewRouter()
	bridgeRouter.Use(bridgeAuthMiddleware(""))
	bridgeRouter.HandleFunc("/s/{session_id}/tools/custom/{tool}", func(w http.ResponseWriter, r *http.Request) {
		executorHandlers.HandlePerToolCustomRequest(w, r, mux.Vars(r)["tool"])
	})
	bridge := httptest.NewServer(bridgeRouter)
	defer bridge.Close()
	t.Setenv("MCP_API_URL", bridge.URL)
	if _, err := workspacepkg.NewClient(workspace.URL, workspacepkg.WithUserID("owner")).InitializeWorkflowDB(t.Context(), workspacepkg.InitializeWorkflowDBParams{DBPath: binding.CrewRoot + "/db/db.sqlite"}); err != nil {
		t.Fatal(err)
	}
	if err := initializeCrewScriptSource(t.Context(), binding, "Saved Python"); err != nil {
		t.Fatalf("initial script source: %v", err)
	}
	write := func(file, content string) {
		t.Helper()
		if !filepath.IsAbs(file) {
			file = filepath.Join(docs, file)
		}
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Account selection is separate from script capabilities and tool names.
	connections := []services.GmailConnection{{ID: "fixture-google", Personal: true, OwnerID: "owner", Enabled: true, Email: "owner@example.com", AuthBackend: "gog"}}
	if googleScope == "selected_workflow" {
		connections[0].Workspaces = []string{binding.Root}
	}
	if googleScope == "shared_default" {
		connections[0].Personal = false
		connections[0].OwnerID = ""
	}
	gmailConfig, _ := json.Marshal(services.GmailConfig{DefaultConnectionID: "fixture-google", Connections: connections})
	write("config/gmail-config.json", string(gmailConfig))
	t.Setenv("GMAIL_CONNECTIONS_DIR", t.TempDir())
	t.Setenv("GMAIL_OAUTH_TOKEN_DIR", t.TempDir())
	t.Setenv("GOG_HOME", t.TempDir())
	previousGoogle := services.GetGmailService()
	t.Cleanup(func() { services.SetGmailService(previousGoogle) })
	if _, err := services.InitGmailService(); err != nil {
		t.Fatal(err)
	}
	expectedGoogle, _ := json.Marshal(map[string]bool{"selected": googleScope != "unselected_private"})
	write(filepath.Join(sourceRoot, "bridge_expected.json"), string(expectedGoogle))
	write(filepath.Join(sourceRoot, "bridge_probe.py"), `import json, os, urllib.request
from pathlib import Path
def call(name, arguments):
    request = urllib.request.Request(os.environ['MCP_API_URL']+'/tools/custom/'+name, data=json.dumps(arguments).encode(), headers={'Content-Type':'application/json','Authorization':'Bearer '+os.environ['MCP_API_TOKEN']})
    with urllib.request.urlopen(request) as response: return json.load(response)
def verify():
    google_selected = json.loads(Path(__file__).with_name('bridge_expected.json').read_text())['selected']
    # Intentional argument refusal proves the actual Google handler was
    # admitted, without contacting Google or inventing credentials.
    google = call('google_workspace_cli', {'args': []})
    text = json.dumps(google)
    if google_selected: assert 'args is required' in text, text
    else: assert 'args is required' not in text and not google.get('success') and ('tools_unavailable' in text or 'tool_not_found' in text), text
    assert call('mutate_workflow_db', {'sql':'CREATE TABLE IF NOT EXISTS bridge_probe (id INTEGER PRIMARY KEY, value TEXT)'}).get('success')
    assert call('mutate_workflow_db', {'sql':'INSERT INTO bridge_probe(id,value) VALUES (?,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value','params':[1,'bridge-probe']}).get('success')
    result = call('query_workflow_db', {'sql':'SELECT value FROM bridge_probe WHERE id=?','params':[1]})
    assert result.get('success') and 'bridge-probe' in json.dumps(result), str(result)
    assert not call('send_email', {}).get('success')
`)
	// Copy an actual CPython extension as a vendored dependency. The old
	// text-file resolver refused it before Python could start (PLAT-746-435).
	extensionPath, err := exec.Command("python3", "-c", "import _json; print(_json.__file__)").Output()
	if err != nil {
		t.Fatal(err)
	}
	extension, err := os.ReadFile(strings.TrimSpace(string(extensionPath)))
	if err != nil {
		t.Fatal(err)
	}
	vendorFile := filepath.Base(strings.TrimSpace(string(extensionPath)))
	write(filepath.Join(sourceRoot, "_vendor", vendorFile), string(extension))
	write(filepath.Join(sourceRoot, "_vendor/vendor_fixture/__init__.py"), "from pathlib import Path\nimport _json\nassert Path(_json.__file__).parent == Path(__file__).parents[1]\nassert _json.scanstring('\\\"vendored\\\"', 1)[0] == 'vendored'\n")
	write(filepath.Join(sourceRoot, "script.json"), `{"id":"saved","name":"Saved Python","inputs":[{"name":"count","type":"integer","required":true},{"name":"extended","type":"boolean","value":"false"},{"name":"rate","type":"number","value":"5"},{"name":"RDS_SECRET_ID_DEV","type":"string","value":"example/dev/app"}],"input_presets":[{"name":"Saved","values":{"extended":"false","rate":"5"}}],"outputs":["result.json"]}`)
	kbPath, err := fsutil.WorkspacePath(docs, binding.Root+"/knowledgebase/context/rules.md")
	if err != nil {
		t.Fatal(err)
	}
	write(kbPath, "fixture knowledge")
	// Retired plan settings must not participate in a converted Python run or
	// its executable revision, even if the old file is malformed.
	legacyConfig, err := fsutil.WorkspacePath(docs, binding.Root+"/planning/step_config.json")
	if err != nil {
		t.Fatal(err)
	}
	write(legacyConfig, "retired settings: not a JSON definition")
	kbJSON, _ := json.Marshal(kbPath)
	write(filepath.Join(sourceRoot, "main.py"), "from pathlib import Path\nassert Path("+string(kbJSON)+").read_text() == 'fixture knowledge'\n"+`import json, os, sys
from bridge_probe import verify
verify()
from nested.prepare import prepare
prepare()
from pathlib import Path
print('start-marker')
sys.stdout.write('Q' * 2097152)
sys.stderr.write('stderr-last-line')
assert 'VAR_GROUP_NAME' in os.environ
values = json.loads(os.environ['STEP_PARAMS_JSON'])
assert isinstance(values['count'], int)
assert values['extended'] is False and isinstance(values['rate'], (int,float))
assert values['RDS_SECRET_ID_DEV'] == 'example/dev/app'
assert not {'PUBLISH_URL','SLACK_CHANNEL_ID','EXT_DISCOVERY_TCP_SCOPE_NOTE'} & set(values)
# Persistent files use the runtime store, not a guessed parent of source code.
data_root = Path(os.environ['WORKFLOW_DATA_ROOT'])
assert data_root != Path(os.environ['WORKFLOW_CODE_ROOT']).parent
report = data_root / 'reports' / 'runtime-fixture.json'
report.parent.mkdir(parents=True, exist_ok=True)
report.write_text(json.dumps({'count': values['count']}))
assert json.loads(report.read_text())['count'] == values['count']
from agentworks import run_folder
for protected in (Path(os.environ['WORKFLOW_CODE_ROOT']) / 'unexpected.py', run_folder / 'helpers' / 'forged.json', Path(__file__)):
    try: protected.write_text('{}')
    except OSError: pass
    else: raise AssertionError('Saved source/helper write was allowed: ' + str(protected))

from agentworks_db import execute, scalar
execute('CREATE TABLE IF NOT EXISTS fixture_rows (id INTEGER PRIMARY KEY, value TEXT)')
execute('INSERT INTO fixture_rows(id,value) VALUES (?,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value', [values['count'],'through script bridge'])
assert scalar('SELECT value FROM fixture_rows WHERE id=?', [values['count']]) == 'through script bridge'
# Ordinary subprocess steps retain the same managed bridge.
import subprocess
subprocess.run([sys.executable, '-B', '-c', "import sys; sys.path.insert(0, '_vendor'); import vendor_fixture"], check=True)
subprocess.run([sys.executable,'-c',"import os; from pathlib import Path; from agentworks_db import scalar; assert scalar('SELECT COUNT(*) FROM fixture_rows') >= 1; assert (Path(os.environ['WORKFLOW_DATA_ROOT'])/'reports/runtime-fixture.json').is_file(); from bridge_probe import verify; verify()"],check=True)

Path(os.environ['STEP_OUTPUT_DIR'], 'result.json').write_text(json.dumps({'count':values['count'], 'preset':os.environ.get('VAR_GROUP_NAME'), 'python_ran':True, 'run_folder':str(__import__('agentworks').run_folder)}))
`)
	if err := writeFileToWorkspace(t.Context(), binding.CrewRoot+"/product.json", `{"schema_version":1,"product":"work","id":"crew-script-http","title":"Script HTTP","session_id":"work:project:crew-script-http"}`); err != nil {
		t.Fatal(err)
	}
	if err := writeFileToWorkspace(t.Context(), binding.CrewRoot+"/workflow.json", `{"schema_version":1,"id":"crew-script-http","label":"Script HTTP","capabilities":{}}`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	manifest, found, err := ReadWorkflowManifest(ctx, binding.Root)
	if err != nil || !found {
		t.Fatal(err)
	}
	// PLAT-746-440: source inspection uses the file editor's current Builder
	// access and never imports/executes the bundle, even in an attached folder.
	sourceAPI := &StreamingAPI{}
	graphEndpoint := httptest.NewServer(AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/crew-script-file" {
			sourceAPI.handleCrewScriptFile(w, r)
			return
		}
		sourceAPI.handleCrewScriptGraph(w, r)
	})))
	defer graphEndpoint.Close()
	// Save a real nested helper through the same owner-authorized editor that
	// rejected package paths, then import it in every actual Python entrance.
	ownerToken, err := GenerateJWT("owner", "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	for file, content := range map[string]string{"nested/__init__.py": "", "nested/prepare.py": "from agentworks import step, check\n@step('Prepare report')\ndef prepare():\n    assert check('Prepared', True, 'Fixture ready').ok\n    return True\n"} {
		body, _ := json.Marshal(map[string]string{"content": content})
		request, _ := http.NewRequestWithContext(ctx, http.MethodPut, graphEndpoint.URL+"/api/crew-script-file?workspace_path="+binding.Root+"&file="+file, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+ownerToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("nested source save HTTP%d %s", response.StatusCode, raw)
		}
	}
	for _, user := range []string{"owner", "runner"} {
		token, err := GenerateJWT(user, user, "")
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, graphEndpoint.URL+"/api/crew-script-graph?workspace_path="+binding.Root, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if user == "runner" {
			if response.StatusCode != 403 {
				t.Fatalf("Run person read source overview HTTP%d", response.StatusCode)
			}
		} else if response.StatusCode != 200 || !strings.Contains(string(body), "Prepare report") || !strings.Contains(string(body), `"errors": []`) || strings.Contains(string(body), "start-marker") {
			t.Fatalf("saved source graph did not inspect safely: HTTP%d %s", response.StatusCode, body)
		}
		if user == "owner" {
			// Large code remains runnable: outline returns its normal graph
			// envelope with a readable limit message, not a failed page request.
			padding := filepath.Join(sourceRoot, "padding.py")
			write(padding, "#"+strings.Repeat("x", 65536))
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, req.URL.String(), nil)
			request.Header.Set("Authorization", "Bearer "+token)
			oversized, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			large, _ := io.ReadAll(oversized.Body)
			oversized.Body.Close()
			if err := os.Remove(padding); err != nil {
				t.Fatal(err)
			}
			if oversized.StatusCode != 200 || !strings.Contains(string(large), "too large to outline") || !strings.Contains(string(large), "64 KiB") {
				t.Fatalf("large outline broke the page request: HTTP%d %s", oversized.StatusCode, large)
			}
		}
	}
	if _, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner", Username: "owner"}, "workflow:"+binding.ID); err != nil {
		t.Fatalf("saved script discovery by stable id: %v", err)
	}
	runAs := workflowExecutionOwnerUserID(manifest)
	if runAs != "owner" {
		t.Fatal("wrong run-as", runAs)
	}
	executionCtx := internalBotRequestContext(context.WithValue(ctx, common.UserIDKey, runAs), runAs)
	// Supply the real server database pool; the script's capability filter and
	// per-invocation registry decide whether Python can use it.
	dbTools := virtualtools.CreateWorkflowDBToolRegistry(workspace.URL, runAs, "")
	dbExecutors := map[string]interface{}{}
	for name, executor := range dbTools.Executors {
		dbExecutors[name] = executor
	}
	for _, tool := range virtualtools.CreateHumanTools() {
		dbTools.Tools = append(dbTools.Tools, tool)
	}
	for name, execute := range virtualtools.CreateHumanToolExecutors() {
		dbExecutors[name] = execute
		dbTools.Categories[name] = "human_tools"
	}

	session, err := workflow.NewWorkshopChatSession(executionCtx, &workflow.WorkshopConfig{UserID: runAs, WorkspacePath: binding.Root, Logger: loggerv2.NewNoop(), CustomTools: dbTools.Tools, CustomToolExecutors: dbExecutors, ToolCategories: dbTools.Categories, CrewResources: &workflow.CrewResources{Scripts: &crewWorkflowResources{userID: runAs}}})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	draft := &productSurfaceDraft{}
	workflow.RegisterRunFullWorkflowTool(draft, session, loggerv2.NewNoop())
	endpoint := httptest.NewServer(AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetUserFromContext(r.Context())
		if level, _ := workflowAccessForWorkspacePath(r.Context(), claims, binding.Root); level == WorkflowAccessNone {
			t.Logf("denied actor=%s role=%s parent-owner=%s", claims.UserID, roleForClaims(claims), runAs)
			http.Error(w, "Crew access required", 403)
			return
		}
		var args map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			http.Error(w, "invalid inputs", 400)
			return
		}
		result, err := draft.tools["run_full_workflow"].exec(executionCtx, args)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		t.Logf("script HTTP actor=%s run_as=%s %s", claims.UserID, runAs, result)
		json.NewEncoder(w).Encode(map[string]interface{}{"result": result})
	})))
	defer endpoint.Close()
	token, err := GenerateJWT("runner", "runner", "")
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL+"/api/test-script-run", bytes.NewBufferString(`{"input_preset":"Saved","inputs":{"count":"9","extended":"false","rate":"5"}}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("Run HTTP%d %s", response.StatusCode, body)
	}
	for {
		snapshot, found, _ := session.StepRegistry.LatestSnapshotForStep("full-workflow")
		if found && snapshot.Status != workflow.WorkshopStepRunning {
			if snapshot.Status != workflow.WorkshopStepDone {
				t.Fatalf("Python failed: %+v", snapshot)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	layout, found, err := crewroot.LookupExecutable(docs, binding.Root)
	if err != nil || !found {
		t.Fatalf("script layout: %v", err)
	}
	runtimeRoot := layout.Runtime
	folders, err := os.ReadDir(filepath.Join(runtimeRoot, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	verified := false
	for _, folder := range folders {
		if !folder.IsDir() || !strings.HasPrefix(folder.Name(), "run-") {
			continue
		}

		result, err := os.ReadFile(filepath.Join(runtimeRoot, "runs", folder.Name(), "execution/saved/result.json"))
		if err == nil && strings.Contains(string(result), `"count": 9`) && strings.Contains(string(result), `"python_ran": true`) {
			if !strings.Contains(string(result), `"preset": "Saved"`) {
				t.Fatalf("direct script lost selected preset environment: %s", result)
			}
			runRoot := filepath.Join(runtimeRoot, "runs", folder.Name())
			metadata, readErr := os.ReadFile(filepath.Join(runRoot, "run_metadata.json"))
			var runRecord RunMetadata
			if readErr != nil || json.Unmarshal(metadata, &runRecord) != nil || runRecord.ExecutionID == "" || len(runRecord.PythonSteps) != 1 || len(runRecord.PythonEvidence) != 1 {
				t.Fatalf("actual script run lost durable attempt/step/check record: %s %v", metadata, readErr)
			}
			for _, recorded := range runRecord.PythonSteps {
				start, startErr := time.Parse(time.RFC3339Nano, recorded.StartedAt)
				end, endErr := time.Parse(time.RFC3339Nano, recorded.CompletedAt)
				if recorded.ExecutionID != runRecord.ExecutionID || recorded.Name != "Prepare report" || recorded.SourceCall == "" || recorded.Status != "completed" || startErr != nil || endErr != nil || end.Before(start) || recorded.DurationMs < 0 || recorded.LogNote == "" {
					t.Fatalf("Python step timing/attempt was not persisted: %+v", recorded)
				}
			}

			snapshot, readErr := os.ReadFile(filepath.Join(runRoot, "inputs.json"))
			var inputRecord map[string]interface{}
			if readErr != nil || json.Unmarshal(snapshot, &inputRecord) != nil || inputRecord["extended"] != false || inputRecord["rate"] != float64(5) {
				t.Fatalf("untyped script input evidence: %s %v", snapshot, readErr)
			}
			if _, err := os.Stat(filepath.Join(runRoot, "execution/saved/inputs.json")); !os.IsNotExist(err) {
				t.Fatalf("duplicate direct-script input snapshot remains: %v", err)
			}

			rawLog, readErr := os.ReadFile(filepath.Join(runRoot, "logs/saved/execution/scripted_fast_path.json"))
			if readErr != nil {
				t.Fatal("script log receipt missing", readErr)
			}
			var logReceipt struct {
				LogFile         string `json:"log_file"`
				LogError        string `json:"log_error"`
				LogComplete     bool   `json:"log_complete"`
				OutputBytes     int    `json:"output_bytes"`
				OutputTruncated bool   `json:"output_truncated"`
				Output          string `json:"output"`
			}
			if err := json.Unmarshal(rawLog, &logReceipt); err != nil || logReceipt.LogFile == "" || logReceipt.LogError != "" || !logReceipt.LogComplete {
				t.Fatalf("bad script log receipt: %s %v", rawLog, err)
			}
			// A native Read of this receipt rejected the previous 2 MiB JSON.
			// Keep the metadata small and both diagnostic ends, with real size.
			if len(rawLog) > 32768 || !logReceipt.OutputTruncated || logReceipt.OutputBytes < 2097152 || !strings.Contains(logReceipt.Output, "start-marker") || !strings.Contains(logReceipt.Output, "stderr-last-line") || !strings.Contains(logReceipt.Output, logReceipt.LogFile) {
				t.Fatalf("unbounded/incomplete output receipt: bytes=%d total=%d truncated=%t", len(rawLog), logReceipt.OutputBytes, logReceipt.OutputTruncated)
			}
			full, readErr := os.ReadFile(logReceipt.LogFile)
			if readErr != nil || strings.Count(string(full), "Q") != 2097152 || !strings.Contains(string(full), "start-marker") || !strings.Contains(string(full), "stderr-last-line") {
				t.Fatalf("full stdout/stderr missing: size=%d %v", len(full), readErr)
			}
			rel, relErr := filepath.Rel(runRoot, logReceipt.LogFile)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, "../") {
				t.Fatalf("log is outside its run: %s", logReceipt.LogFile)
			}
			for _, user := range []string{"owner", "runner"} {
				request := httptest.NewRequest(http.MethodGet, "/api/workspace/run-evidence?workspace_path="+binding.Root+"&run_folder="+folder.Name(), nil)
				request = request.WithContext(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: user, Username: user}))
				reply := httptest.NewRecorder()
				(&StreamingAPI{}).handleWorkspaceRunEvidence(reply, request)
				var evidence struct {
					Items []workflow.PythonRunEvidence `json:"items"`
				}
				if reply.Code != 200 || json.Unmarshal(reply.Body.Bytes(), &evidence) != nil || len(evidence.Items) != 1 || evidence.Items[0].OK == nil || !*evidence.Items[0].OK || evidence.Items[0].SourceCall == "" {
					t.Fatalf("converted run evidence user=%s: HTTP%d %s", user, reply.Code, reply.Body)
				}
			}
			verified = true
			t.Logf("script completed run=%s run_as=%s real_python=true", folder.Name(), runAs)
		}
	}
	if _, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner", Username: "owner"}, "workflow:"+binding.ID); err != nil {
		t.Fatalf("script discovery after runtime files exist: %v", err)
	}
	if !verified {
		t.Fatal("no real Python result")
	}
	if !local {
		checkSavedScriptTriggerAndSchedule(t, docs, binding)
	}
	var sharedExtension os.FileInfo
	linkedRuns := 0
	folders, err = os.ReadDir(filepath.Join(runtimeRoot, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range folders {
		if !strings.HasPrefix(folder.Name(), "run-") {
			continue
		}
		snapshotFile := filepath.Join(runtimeRoot, "runs", folder.Name(), "execution/saved/code/_vendor", vendorFile)
		info, err := os.Stat(snapshotFile)
		if err != nil {
			t.Fatal(err)
		}
		if sharedExtension != nil && !os.SameFile(sharedExtension, info) {
			t.Fatal("unchanged vendored binary was copied instead of linking its immutable snapshot")
		}
		sharedExtension = info
		linkedRuns++
		actual, readErr := os.ReadFile(snapshotFile)
		if readErr != nil || !bytes.Equal(actual, extension) {
			t.Fatal("binary source bytes changed", readErr)
		}
	}
	if !local && linkedRuns < 3 {
		t.Fatal("manual/trigger/timer binary snapshots were not all retained")
	}
	authored, err := os.Stat(filepath.Join(sourceRoot, "_vendor", vendorFile))
	if err != nil || sharedExtension == nil || os.SameFile(authored, sharedExtension) {
		t.Fatal("run linked mutable authored source", err)
	}
}
