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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	handlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/spf13/viper"
)

// PLAT-746-265: authenticated Run-person admission crosses HTTP, uses the live
// parent owner, and executes real Python with no model turn or saved plan.
func TestSharedCrewScriptRunPersonHTTPExecutesRealPython(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprintf("attached_project_%v", local), func(t *testing.T) { checkSharedCrewScriptHTTP(t, local) })
	}
}

func checkSharedCrewScriptHTTP(t *testing.T, local bool) {

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
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	binding := crewExecutableBinding{ID: "saved", CrewID: "crew-script-http", CrewRoot: "Crew/script-http", Root: "Workflow/script-http-saved", Kind: "script"}
	if err := registerCrewExecutable(binding); err != nil {
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
	write(filepath.Join(sourceRoot, "script.json"), `{"id":"saved","name":"Saved Python","inputs":[{"name":"count","type":"integer","required":true}],"outputs":["result.json"]}`)
	write(filepath.Join(sourceRoot, "main.py"), `import json, os, sys
from pathlib import Path
print('start-marker')
sys.stdout.write('Q' * 2097152)
sys.stderr.write('stderr-last-line')
values = json.loads(os.environ['STEP_PARAMS_JSON'])
assert isinstance(values['count'], int)
Path(os.environ['STEP_OUTPUT_DIR'], 'result.json').write_text(json.dumps({'count':values['count'], 'python_ran':True}))
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
	if _, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner", Username: "owner"}, "workflow:"+binding.ID); err != nil {
		t.Fatalf("saved script discovery by stable id: %v", err)
	}
	runAs := workflowExecutionOwnerUserID(manifest)
	if runAs != "owner" {
		t.Fatal("wrong run-as", runAs)
	}
	executionCtx := internalBotRequestContext(context.WithValue(ctx, common.UserIDKey, runAs), runAs)
	session, err := workflow.NewWorkshopChatSession(executionCtx, &workflow.WorkshopConfig{UserID: runAs, WorkspacePath: binding.Root, Logger: loggerv2.NewNoop(), CrewResources: &workflow.CrewResources{Scripts: &crewWorkflowResources{userID: runAs}}})
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
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL+"/api/test-script-run", bytes.NewBufferString(`{"inputs":{"count":"9"}}`))
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
			runRoot := filepath.Join(runtimeRoot, "runs", folder.Name())
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
}
