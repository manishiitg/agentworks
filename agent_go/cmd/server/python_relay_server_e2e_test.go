package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

// Real workspace file and shell handlers pin Python authoring admission,
// immutable publishing and JSON return collection without a workflow plan.
func TestPythonRelayPublishAndReturnRealWorkspace(t *testing.T) {
	if os.Getenv("RUN_PYTHON_RELAY_E2E") == "" {
		t.Skip("set RUN_PYTHON_RELAY_E2E=1 for real workspace/Python checks")
	}
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("NATIVE_WORKSPACE", "true")
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Any("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.POST("/api/folders", workspacehandlers.CreateFolder)
	router.POST("/api/execute", workspacehandlers.ExecuteShellCommand)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	const workspacePath = "Workflow/python-publish"
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(docs, workspacePath, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := NewWorkflowManifest("Python publish")
	manifest.Kind, manifest.RelayRuntime = "relay", "python"
	manifest.CodeLayoutVersion, manifest.Version = 0, "1.0.1"
	manifest.Schedules = []WorkflowSchedule{{ID: "process", Name: "Process", ScheduleType: "webhook", Kind: triggerKindFunction, Enabled: true, WorkshopMode: "run", Function: &WorkflowFunctionSpec{Name: "process", Inputs: []WorkflowFunctionInput{{Name: "INPUT", Type: "object", Required: true}}}}}
	raw, _ := json.Marshal(manifest)
	write("workflow.json", string(raw))
	write("variables/variables.json", `{"variables":[{"name":"INPUT","type":"object","value":"{}"}]}`)
	// Validation must compile only: importing this source would raise.
	write("relay.py", "raise RuntimeError('validation executed source')\nasync def run(INPUT, ctx):\n    return {'value': 1}\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := directWebhookPreflight(manifest); err != nil {
		t.Fatalf("Python Relay was gated by workflow contract: %v", err)
	}
	write("ast.py", "raise RuntimeError('workspace module executed during validation')\n")
	if err := validatePythonRelaySource(ctx, workspacePath); err != nil {
		t.Fatalf("validator imported workspace code: %v", err)
	}
	if err := os.Remove(filepath.Join(docs, workspacePath, "ast.py")); err != nil {
		t.Fatal(err)
	}
	v1, err := publishRelayRelease(ctx, workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Version != "v1" || v1.OutputStep != "" || len(v1.Files) != 3 {
		t.Fatalf("Python release includes workflow plan/output contract: %+v", v1)
	}
	write("relay.py", defaultPythonRelaySource)
	v2, err := publishRelayRelease(ctx, workspacePath)
	if err != nil || v2.Version != "v2" {
		t.Fatalf("publish new draft: %+v %v", v2, err)
	}
	frozen, err := os.ReadFile(filepath.Join(docs, relayReleaseWorkspace(workspacePath, "v1"), "relay.py"))
	if err != nil || !strings.Contains(string(frozen), "'value': 1") {
		t.Fatalf("published source was rewritten: %s %v", frozen, err)
	}
	releaseWorkspace := relayReleaseWorkspace(workspacePath, "v2")
	starterFolder, err := allocateWebhookRunFolder(releaseWorkspace, "python-starter")
	if err != nil {
		t.Fatal(err)
	}
	starterContext := buildScheduleContext(releaseWorkspace, manifest, manifest.Schedules[0])
	starterContext.WebhookInput = &WorkflowWebhookDelivery{RunID: "python-starter", Variables: map[string]string{"INPUT": `{"name":"Ada"}`}}
	if err := (&SchedulerService{api: &StreamingAPI{}}).executePythonRelay(ctx, starterContext, "python-starter", starterFolder, "relay-python-real-starter"); err != nil {
		t.Fatalf("execute published Python starter: %v", err)
	}
	starterResult := webhookRunResult{Terminal: true, Status: "completed"}
	applyRelayResult(manifest, &starterResult, releaseWorkspace, schedulerstate.Run{RunID: "python-starter", RunFolder: starterFolder})
	var greeting map[string]string
	if err := json.Unmarshal(starterResult.Result, &greeting); err != nil || greeting["hello"] != "Ada" {
		t.Fatalf("starter result: %s (%v)", starterResult.Result, err)
	}
	var trace struct {
		Status string        `json:"status"`
		Calls  []interface{} `json:"calls"`
	}
	if err := json.Unmarshal(starterResult.Trace, &trace); err != nil || trace.Status != "completed" || len(trace.Calls) != 0 {
		t.Fatalf("starter trace: %s (%v)", starterResult.Trace, err)
	}
	if err := verifyRelayRelease(ctx, v2, releaseWorkspace); err != nil {
		t.Fatalf("execution changed published source: %v", err)
	}
	write("relay.py", "async def run(INPUT):\n    pass\n")
	if _, err := publishRelayRelease(ctx, workspacePath); err == nil {
		t.Fatal("invalid entrypoint was published")
	}
	folder, err := allocateWebhookRunFolder(workspacePath, "python-result")
	if err != nil {
		t.Fatal(err)
	}
	write("runs/"+folder+"/relay_result.json", `[1,{"value":2}]`)
	run := schedulerstate.Run{RunID: "python-result", RunFolder: folder}
	result := webhookRunResult{Terminal: true, Status: "completed"}
	applyRelayResult(manifest, &result, workspacePath, run)
	if string(result.Result) != `[1,{"value":2}]` || result.Error != "" {
		t.Fatalf("Python return was not the API result: %+v", result)
	}
	sctx := buildScheduleContext(workspacePath, manifest, manifest.Schedules[0])
	if _, waiting := (&SchedulerService{}).turnLevelCapacityWait(ctx, sctx, errors.New(quotaExhaustedMarker+" test"), folder, time.Now()); waiting {
		t.Fatal("Python failure armed execution recovery")
	}
}

// Opt-in live acceptance: the platform adapter invokes a real logged-in CLI,
// whose tool crosses the signed HTTP bridge back into the live Python closure.
func TestPythonRelayLiveAgentToolAndMessages(t *testing.T) {
	provider := os.Getenv("RUN_PYTHON_RELAY_AGENT_E2E")
	if provider == "" {
		t.Skip("set RUN_PYTHON_RELAY_AGENT_E2E to a logged-in coding CLI provider")
	}
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("NATIVE_WORKSPACE", "true")
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	gin.SetMode(gin.TestMode)
	ws := gin.New()
	ws.Any("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	ws.GET("/api/documents", workspacehandlers.ListDocuments)
	ws.POST("/api/folders", workspacehandlers.CreateFolder)
	ws.POST("/api/execute", workspacehandlers.ExecuteShellCommand)
	files := httptest.NewServer(ws)
	t.Cleanup(files.Close)
	t.Setenv("WORKSPACE_API_URL", files.URL)
	config := filepath.Join(docs, "mcp.json")
	if err := os.WriteFile(config, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	common.SetBridgeTokenSecret("python-relay-e2e-signing")
	t.Cleanup(func() { common.SetBridgeTokenSecret("") })
	handlers := executor.NewExecutorHandlers(config, loggerv2.NewNoop())
	routes := mux.NewRouter()
	for _, prefix := range []string{"/tools", "/s/{session_id}/tools"} {
		tools := routes.PathPrefix(prefix).Subrouter()
		tools.Use(bridgeAuthMiddleware("python-relay-e2e-signing"))
		tools.HandleFunc("/custom/{tool}", func(w http.ResponseWriter, r *http.Request) {
			handlers.HandlePerToolCustomRequest(w, r, mux.Vars(r)["tool"])
		})
	}
	bridge := httptest.NewServer(routes)
	t.Cleanup(bridge.Close)
	u, _ := url.Parse(bridge.URL)
	port, _ := strconv.Atoi(u.Port())
	t.Setenv("MCP_API_URL", bridge.URL)
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{config: ServerConfig{Host: u.Hostname(), Port: port}, mcpConfigPath: config, chatStore: store, logger: loggerv2.NewNoop()}
	const workspacePath = "Workflow/python-live"
	if err := os.MkdirAll(filepath.Join(docs, workspacePath), 0700); err != nil {
		t.Fatal(err)
	}
	source := `import json
from relay_sdk import tool
async def run(INPUT, ctx):
    @tool
    async def lookup(customer_id: str):
        """Fetch the customer's private verification code."""
        return {"customer_id": customer_id, "code": INPUT["code"]}
    value = await ctx.call_agent(
        model="` + provider + `:", name="lookup",
        system_prompt="Call the lookup tool to fetch the code. Never guess it. Follow the user's next message using the tool result you already obtained. Return only JSON.",
        messages=["Call lookup with customer_id c1. Reply with the code as JSON.", "Using that prior tool result, return exactly an object with code and customer_id."],
        tools=[lookup], output_schema={"type":"object", "properties":{"code":{"type":"string"},"customer_id":{"type":"string"}}, "required":["code", "customer_id"]})
    return {"customer": value}
`
	if err := os.WriteFile(filepath.Join(docs, workspacePath, "relay.py"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := NewWorkflowManifest("Live Python")
	manifest.Kind, manifest.RelayRuntime = "relay", "python"
	bytes, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(docs, workspacePath, "workflow.json"), bytes, 0600); err != nil {
		t.Fatal(err)
	}
	folder, err := allocateWebhookRunFolder(workspacePath, "python-live")
	if err != nil {
		t.Fatal(err)
	}
	sctx := buildScheduleContext(workspacePath, manifest, WorkflowSchedule{ID: "live", Name: "Live", ScheduleType: "webhook", Kind: triggerKindFunction})
	sctx.WebhookInput = &WorkflowWebhookDelivery{RunID: "python-live", Variables: map[string]string{"INPUT": `{"code":"relay-secret-725194"}`}}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if err := (&SchedulerService{api: api}).executePythonRelay(ctx, sctx, "python-live", folder, "python-relay-live-root"); err != nil {
		t.Fatal(err)
	}
	result := webhookRunResult{Terminal: true, Status: "completed"}
	applyRelayResult(manifest, &result, workspacePath, schedulerstate.Run{RunID: "python-live", RunFolder: folder})
	if !strings.Contains(string(result.Result), "relay-secret-725194") {
		t.Fatalf("tool value not returned: %s", result.Result)
	}
	if !strings.Contains(string(result.Trace), `"name": "lookup"`) && !strings.Contains(string(result.Trace), `"name":"lookup"`) {
		t.Fatalf("Python tool absent from trace: %s", result.Trace)
	}
	t.Logf("real %s returned %s", provider, result.Result)
}
