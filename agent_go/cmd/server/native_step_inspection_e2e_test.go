package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
)

// The CLI protocol driver performs real native file writes in the real kernel
// sandbox, including a child process. It makes no model request. The rest is
// the real structured adapter, SDK events, durable recorder and inspector HTTP.
func TestFullNativeStepJSONInspection(t *testing.T) {
	runner := os.Getenv("AGENTWORKS_LANDLOCK_RUNNER")
	if runtime.GOOS != "linux" || runner == "" {
		t.Skip("GitHub verification provides the Linux Landlock launcher")
	}
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	output := filepath.Join(docs, "Workflow/example/runs/iteration-0/default/execution/native-step")
	outside := filepath.Join(root, "outside", "forbidden.txt")
	bin := filepath.Join(root, "bin")
	home := filepath.Join(root, "cli-home")
	for _, dir := range []string{output, filepath.Dir(outside), bin, home} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://127.0.0.1:8080")
	t.Setenv("MCP_API_TOKEN", "fixture-token")
	childCode := `import os,json,datetime
root=os.environ.get('CODEX_HOME',os.path.expanduser('~/.codex'))
parent='11111111-1111-1111-1111-111111111111'; child='22222222-2222-2222-2222-222222222222'
path=os.path.join(root,'sessions','2026','10','14','rollout-'+child+'.jsonl');os.makedirs(os.path.dirname(path),exist_ok=True)
now=datetime.datetime.now(datetime.timezone.utc).isoformat()
def row(t,p): return json.dumps({'type':t,'timestamp':now,'payload':p})
with open(path,'w') as f:
 f.write(row('session_meta',{'id':child,'cwd':os.getcwd(),'base_instructions':{'text':'Verify the recorded artifact'},'source':{'subagent':{'thread_spawn':{'parent_thread_id':parent}}}})+'\n')
 f.write(row('response_item',{'type':'message','role':'user','content':[{'type':'input_text','text':'Read witness.txt'}]})+'\n')
 f.write(row('response_item',{'type':'function_call','name':'read_file','call_id':'child-read','arguments':json.dumps({'path':OUT})})+'\n')
 with open(OUT) as evidence: value=evidence.read()
 f.write(row('response_item',{'type':'function_call_output','call_id':'child-read','output':value})+'\n')
 f.write(row('response_item',{'type':'message','role':'assistant','phase':'final_answer','content':[{'type':'output_text','text':'Native child verified '+value}]})+'\n')
`
	childCode = "OUT=" + fmt.Sprintf("%q", filepath.Join(output, "witness.txt")) + "\n" + childCode
	script := fmt.Sprintf(`#!/usr/bin/python3
import sys,os,json,subprocess
if '--json' not in sys.argv or 'read-only' in sys.argv: raise RuntimeError('expected Full CLI structured transport')
def emit(kind,item=None,**fields):
 row={'type':kind,**fields}
 if item is not None: row['item']=item
 print(json.dumps(row),flush=True)
parent='11111111-1111-1111-1111-111111111111'
emit('thread.started',thread_id=parent);emit('turn.started')
with open(%q,'w') as f: f.write('native evidence')
emit('item.completed',{'id':'native-write','type':'file_change','changes':[{'path':%q,'kind':'add'}],'status':'completed'})
try:
 with open(%q,'w') as f: f.write('must be denied')
 raise RuntimeError('outside write escaped the step grant')
except PermissionError:
 emit('item.completed',{'id':'outside-denied','type':'file_change','changes':[{'path':%q,'kind':'add'}],'status':'failed'})
emit('item.started',{'id':'native-spawn','type':'collab_tool_call','tool':'spawn_agent','sender_thread_id':parent,'receiver_thread_ids':[],'prompt':'Read witness.txt','agents_states':{},'status':'in_progress'})
subprocess.run([sys.executable,'-c',%q],check=True)
emit('item.completed',{'id':'native-spawn','type':'collab_tool_call','tool':'spawn_agent','sender_thread_id':parent,'receiver_thread_ids':['22222222-2222-2222-2222-222222222222'],'prompt':'Read witness.txt','agents_states':{'22222222-2222-2222-2222-222222222222':{'status':'completed','message':'verified'}},'status':'completed'})
emit('item.completed',{'id':'answer','type':'agent_message','text':'Native step completed'})
emit('turn.completed',usage={'input_tokens':1,'output_tokens':1,'total_tokens':2})
`, filepath.Join(output, "witness.txt"), filepath.Join(output, "witness.txt"), outside, outside, childCode)
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "mcp.json")
	if err := os.WriteFile(config, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	service := httptest.NewServer(workspace)
	defer service.Close()
	t.Setenv("WORKSPACE_API_URL", service.URL)
	api := lifecycleTestAPI()
	api.activeSessions = map[string]*ActiveSessionInfo{"chat": {SessionID: "chat", WorkspacePath: "Workflow/example", UserID: GetDefaultUserID()}}
	api.bgAgentRegistry.Register("chat", &BackgroundAgent{ID: "native-step", Name: "Native step", Kind: "workflow_step", Status: BGAgentRunning, CreatedAt: time.Now(), Metadata: map[string]string{"step_id": "native-step", "run_folder": "iteration-0/default"}})
	api.emitBackgroundAgentStarted("chat", "native-step", "Native step", "", "", orchEvents.ExecutionKindWorkflowStep)
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), discardEvents{}, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath("Workflow/example")
	bridge := base.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge)
	ctx := orchestrator.WithBackgroundTranscriptSession(t.Context(), "chat")
	ctx = context.WithValue(ctx, orchEvents.ParentExecutionIDKey, "native-step")
	ctx = orchestrator.WithEventContextOverride(ctx, "execution", 0, "native-step", "Native step", orchestrator.RichStepContext{})
	policy := llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, LandlockRunner: runner, PrivateHome: home, WorkspaceReadPaths: []string{output, bin}, WorkspaceWritePaths: []string{output}}
	agent, err := mcpagent.NewAgentFromDefinition(ctx, mcpagent.AgentDefinition{Instructions: "Write evidence and delegate verification.", Tools: mcpagent.ToolSet{MCP: []mcpagent.MCPToolSource{{Name: mcpclient.NoServers}}}}, mcpagent.RuntimeConfig{
		Model: codexcli.NewCodexCLIAdapter("", "gpt-5.5", nil), MCPConfigPath: config,
		Generation: mcpagent.GenerationRuntimeConfig{Provider: llm.Provider("codex-cli"), MaxTurns: 1},
		Coding:     mcpagent.CodingRuntimeConfig{AgentToolsMode: "full", Transport: llm.CodingAgentTransportStructured, CLISecurityPolicy: &policy},
		Workspace:  mcpagent.WorkspaceRuntimeConfig{IsolatedSession: true, CodingAgentWorkingDir: output, OutputDir: output}, MCP: mcpagent.MCPRuntimeConfig{SessionID: "native-runtime"},
		Observability: mcpagent.ObservabilityRuntimeConfig{Logger: loggerv2.NewNoop(), Streaming: true, Observers: []mcpagent.AgentEventListener{bridge}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	session, err := agent.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := session.Run(ctx, mcpagent.Turn{Input: "Write the native evidence and verify it with a child."}); err != nil {
		t.Fatal(err)
	}
	if err := bridge.WaitForBackgroundTranscriptPersistence(ctx); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(output, "witness.txt")); err != nil || string(raw) != "native evidence" {
		t.Fatal("native artifact missing", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatal("native write escaped the step folder", err)
	}
	router := mux.NewRouter()
	router.HandleFunc("/inspect/{session_id}/{execution_id}", api.handleInspectSessionExecution)
	inspect := func(id string) struct {
		Transcript *orchEvents.BackgroundAgentTranscript `json:"transcript"`
		Children   []BackgroundAgentLogEntry             `json:"children"`
	} {
		var result struct {
			Transcript *orchEvents.BackgroundAgentTranscript `json:"transcript"`
			Children   []BackgroundAgentLogEntry             `json:"children"`
		}
		request := httptest.NewRequest(http.MethodGet, "/inspect/chat/"+id+"?workspace_path=Workflow/example", nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("inspection failed: %d %s", response.Code, response.Body.String())
		}
		return result
	}
	api.emitBackgroundAgentCompleted("chat", "native-step", "Native step", "completed", "Native step completed", "", "1s")
	parent := inspect("native-step")
	if len(parent.Children) != 1 {
		t.Fatalf("native child not linked: %+v", parent.Children)
	}
	tools := map[string]bool{}
	for _, event := range parent.Transcript.Events {
		if event.ToolCall != nil {
			tools[event.ToolCall.ToolName] = true
		}
	}
	if !tools["apply_patch"] || !tools["spawn_agent"] {
		t.Fatalf("native calls missing from inspector: %+v", tools)
	}
	child := inspect(parent.Children[0].AgentID)
	encoded, _ := json.Marshal(child.Transcript)
	for _, want := range []string{"Verify the recorded artifact", "Read witness.txt", "child-read", "native evidence", "Native child verified"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("native child conversation missing %q", want)
		}
	}
}
