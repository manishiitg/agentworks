package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	step_based_workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	mcpexecutor "github.com/manishiitg/mcpagent/executor"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestPulseAppliedFixClosesThroughMCPBridge is the transport-level regression
// for the issue-register lifecycle. It uses the production mcpbridge stdio
// binary, the production record_pulse_result executor, and SQLite.
func TestPulseAppliedFixClosesThroughMCPBridge(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/pulse-bridge-verification"
	priorRunID := "pulse-prior-fix"
	currentRunID := "pulse-review-verification"

	if _, err := recordPulseWorklist(ctx, workspacePath, priorRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleTechnicalReview: {Module: pulseModuleTechnicalReview, Due: true, Reason: "Repair the filed defect."},
	})); err != nil {
		t.Fatalf("record prior worklist: %v", err)
	}
	recordTestReviewFinding(t, ctx, workspacePath, priorRunID, pulseModuleTechnicalReview, "collector omits the populated latency value after a producing run")
	backlog, err := step_based_workflow.LoadPulseFindingLifecycles(ctx, workspacePath, pulseModuleTechnicalReview, -1)
	if err != nil || len(backlog) != 1 {
		t.Fatalf("load filed concern: count=%d err=%v", len(backlog), err)
	}
	finding := backlog[0]
	if _, err := recordPulseWorklist(ctx, workspacePath, currentRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleTechnicalReview: {Module: pulseModuleTechnicalReview, Due: true, Reason: "Apply the repair."},
	})); err != nil {
		t.Fatalf("record current worklist: %v", err)
	}
	_, executors, _ := createPulseWorklistTools()
	executor, ok := executors["record_pulse_result"].(func(context.Context, map[string]interface{}) (string, error))
	if !ok {
		t.Fatal("record_pulse_result executor has unexpected type")
	}
	const sessionID = "pulse-fixer-bridge-session"

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tools/custom/record_pulse_result" {
			http.Error(w, "unknown tool", http.StatusNotFound)
			return
		}
		var args map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requestCtx := mcpexecutor.WithSessionID(r.Context(), r.Header.Get("X-Session-ID"))
		result, execErr := executor(requestCtx, args)
		w.Header().Set("Content-Type", "application/json")
		response := map[string]interface{}{"success": execErr == nil, "result": result}
		if execErr != nil {
			response["error"] = execErr.Error()
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer api.Close()

	bridgeBinary := buildPulseTestMCPBridge(t)
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"workspace_path":       map[string]interface{}{"type": "string"},
			"pulse_run_id":         map[string]interface{}{"type": "string"},
			"module":               map[string]interface{}{"type": "string"},
			"result":               map[string]interface{}{"type": "string"},
			"reason":               map[string]interface{}{"type": "string"},
			"evidence":             map[string]interface{}{"type": "array"},
			"changed_files":        map[string]interface{}{"type": "array"},
			"verification":         map[string]interface{}{"type": "array"},
			"finding_dispositions": map[string]interface{}{"type": "array"},
		},
		"required": []string{"workspace_path", "pulse_run_id", "module", "result", "reason"},
	}
	schemaJSON, _ := json.Marshal(schema)
	toolsJSON, _ := json.Marshal([]map[string]interface{}{{
		"name": "record_pulse_result", "description": "Apply a verified Pulse lifecycle result.",
		"input_schema": json.RawMessage(schemaJSON), "type": "custom",
	}})
	bridgeClient, err := client.NewStdioMCPClient(bridgeBinary, append(os.Environ(),
		"MCP_API_URL="+api.URL,
		"MCP_API_TOKEN=bridge-test-token",
		"MCP_SESSION_ID="+sessionID,
		"MCP_TOOLS="+string(toolsJSON),
	))
	if err != nil {
		t.Fatalf("start mcpbridge: %v", err)
	}
	defer bridgeClient.Close()
	bridgeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := bridgeClient.Initialize(bridgeCtx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "pulse-e2e", Version: "1"},
	}}); err != nil {
		t.Fatalf("initialize mcpbridge: %v", err)
	}
	request := mcp.CallToolRequest{}
	request.Params.Name = "record_pulse_result"
	request.Params.Arguments = map[string]interface{}{
		"workspace_path": workspacePath, "pulse_run_id": currentRunID, "module": pulseModuleTechnicalReview,
		"result": "changed", "reason": "Fixer applied the bounded collector repair.",
		"evidence":      []interface{}{"planning/step_config.json"},
		"changed_files": []interface{}{"planning/step_config.json"},
		"verification":  []interface{}{"updated configuration parses"},
		"finding_dispositions": []interface{}{map[string]interface{}{
			"issue_id":    finding.Issue.ID,
			"disposition": step_based_workflow.FindingDispositionChangedUnverified, "summary": "Applied collector repair",
			"changed_files": []interface{}{"planning/step_config.json"},
			"before_refs":   []interface{}{"sha256:before"}, "after_refs": []interface{}{"sha256:after"},
		}},
	}
	result, err := bridgeClient.CallTool(bridgeCtx, request)
	if err != nil {
		t.Fatalf("bridge tool call: %v", err)
	}
	if len(result.Content) != 1 || !strings.Contains(fmt.Sprint(result.Content[0]), `"status":"updated"`) {
		t.Fatalf("unexpected bridge result: %#v", result.Content)
	}

	closed, err := step_based_workflow.LoadPulseFindingLifecycles(ctx, workspacePath, pulseModuleTechnicalReview, -1)
	if err != nil || len(closed) != 1 || closed[0].Status != step_based_workflow.ConcernStatusResolved {
		t.Fatalf("applied fix did not close through bridge: %#v err=%v", closed, err)
	}
	if len(closed[0].Attempts) != 1 || closed[0].Attempts[0].Status != "applied" {
		t.Fatalf("applied fix retained verification work: %#v", closed[0].Attempts)
	}
}

func buildPulseTestMCPBridge(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	// Building a dependency directly from the module cache does not resolve its
	// local replace directives relative to this repository. Use the same
	// provider module selected by agent_go in a temporary workspace instead.
	resolveModule := func(name string) string {
		t.Helper()
		cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", name)
		cmd.Dir = moduleRoot
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("resolve %s module: %v\n%s", name, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	moduleDir := resolveModule("github.com/manishiitg/mcpagent")
	providerDir := resolveModule("github.com/manishiitg/multi-llm-provider-go")
	workDir := t.TempDir()
	initWork := exec.Command("go", "work", "init", moduleDir)
	initWork.Dir = workDir
	if output, err := initWork.CombinedOutput(); err != nil {
		t.Fatalf("init mcpbridge workspace: %v\n%s", err, output)
	}
	workFile := filepath.Join(workDir, "go.work")
	workData, err := os.ReadFile(workFile)
	if err != nil {
		t.Fatalf("read mcpbridge workspace: %v", err)
	}
	// go work edit -replace parses @ in a module-cache directory as a version
	// separator. A quoted directory in the workspace file preserves the path.
	workData = append(workData, []byte(fmt.Sprintf("\nreplace github.com/manishiitg/multi-llm-provider-go => %q\n", providerDir))...)
	if err := os.WriteFile(workFile, workData, 0600); err != nil {
		t.Fatalf("replace mcpbridge provider module: %v", err)
	}
	source := filepath.Join(moduleDir, "cmd", "mcpbridge")
	binary := filepath.Join(workDir, "mcpbridge")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = source
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(workDir, "go.work"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mcpbridge: %v\n%s", err, output)
	}
	return binary
}
