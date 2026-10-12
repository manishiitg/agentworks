package step_based_workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/executioninspection"
	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

// Real Python, production script runner/client, real document handlers and
// durable readback. A later edit must not replace the dispatched source.
func TestScriptInspectionFreezesExecutedSourceAndOutput(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	previous := viper.Get("docs-dir")
	viper.Set("docs-dir", root)
	t.Cleanup(func() { viper.Set("docs-dir", previous) })
	workspace := "Workflow/test-flow"
	sourceRel := workspace + "/code/task/main.py"
	sourcePath := filepath.Join(root, sourceRel)
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0755); err != nil {
		t.Fatal(err)
	}
	source := "import sys\nMCP_API_TOKEN = 'fixture-script-secret'\nprint('first source')\nprint('diagnostic', file=sys.stderr)\n"
	if err := os.WriteFile(sourcePath, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.POST("/api/folders", workspacehandlers.CreateFolder)
	router.GET("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	router.PUT("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	router.POST("/api/execute", func(c *gin.Context) {
		var request struct {
			Command string `json:"command"`
		}
		if err := json.NewDecoder(c.Request.Body).Decode(&request); err != nil {
			t.Error(err)
			c.Status(400)
			return
		}
		if !strings.Contains(request.Command, sourcePath) {
			t.Errorf("unexpected command %s", request.Command)
		}
		cmd := exec.CommandContext(c.Request.Context(), "python3", sourcePath)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Error(err)
			c.Status(500)
			return
		}
		if err := os.WriteFile(sourcePath, []byte("print('later edit')\n"), 0600); err != nil {
			t.Error(err)
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"stdout": stdout.String(), "stderr": stderr.String(), "exit_code": 0}})
	})
	server := httptest.NewServer(router)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	controller := newAgentClosingTestOrchestrator(t)
	controller.httpSessionID = "chat"
	controller.selectedRunFolder = "iteration-0/default"
	controller.codeLayoutVersion.Store(1)
	owner := "exec-task-1"
	header := orchevents.NewBackgroundAgentTranscript("chat", owner, "", "Task", "workflow_step", time.Now())
	content, err := header.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	path := orchevents.BackgroundAgentTranscriptPath(workspace, "chat", owner)
	if err := controller.WriteWorkspaceFile(context.Background(), path, content); err != nil {
		t.Fatal(err)
	}
	ctx := virtualtools.WithBackgroundAgentID(context.Background(), owner)
	controller.recordInspectionMode(ctx, "script")
	step := &ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "task", Title: "Task"}}
	output, code, err := controller.execScriptedScript(ctx, step, 0, "step-1", sourcePath, nil, filepath.Join(root, workspace, "runs/iteration-0/default/execution/task"), filepath.Dir(sourcePath), "runs/iteration-0/default/execution/task", DBAccessNone)
	if err != nil || code != 0 || !strings.Contains(output, "first source") {
		t.Fatalf("execution code=%d output=%q err=%v", code, output, err)
	}
	saved, err := controller.ReadWorkspaceFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := orchevents.ParseBackgroundAgentTranscript(saved)
	if err != nil {
		t.Fatal(err)
	}
	mode, scripts := executioninspection.Records(transcript)
	if mode != "script" || len(scripts) != 1 {
		t.Fatalf("mode=%s scripts=%+v", mode, scripts)
	}
	script := scripts[0]
	if !strings.Contains(script.Source, "first source") || strings.Contains(script.Source, "later edit") || script.Stdout != "first source\n" || script.Stderr != "diagnostic\n" || script.ExitCode == nil || *script.ExitCode != 0 || script.Status != "completed" {
		t.Fatalf("wrong script evidence: %+v", script)
	}
	if strings.Contains(saved, "fixture-script-secret") || !strings.Contains(saved, "[redacted]") {
		t.Fatal("script source secret was not redacted")
	}
}
