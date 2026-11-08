package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/clisecurity"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	mcpllm "github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Exercise the real coding-provider factory and ordinary turn executor. Only
// the Codex app-server executable is a protocol fixture; no API-only provider
// bypass or replacement agent engine is added. Real CLI proof stays separate.
func TestPythonAgentContinuesTurnsBoundsReplyAndPreservesTranscript(t *testing.T) {
	docs, files := workflowHTTPTestWorkspace(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTWORKS_SLOTS", "off")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CODEX_CLI_TRANSPORT", "app-server")
	const root = "Workflow/python-agent"
	runRoot := filepath.Join(docs, root, "runs/run-one")
	for name, content := range map[string]string{"workflow.json": `{"code_layout_version":1}`, "soul/soul.md": "# Task\nDo the requested work.", "planning/plan.json": `{"steps":[]}`, "runs/run-one/marker": "run"} {
		file := filepath.Join(docs, root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	fixture := "#!" + python + "\n" + fmt.Sprintf(`import json,sys,uuid
from pathlib import Path
root=Path(%q)
if '--version' in sys.argv:
    print('codex-cli 1.0.0');raise SystemExit(0)
thread=''
state=None
def send(value):
    print(json.dumps(value),flush=True)
for line in sys.stdin:
    request=json.loads(line)
    method=request.get('method','')
    params=request.get('params',{})
    with (root/'fixture-requests.jsonl').open('a') as out:
        out.write(json.dumps({'method':method,'thread':params.get('threadId'),'input':params.get('input'),'cwd':str(Path.cwd())})+'\n')
    result={}
    if method=='thread/start':
        thread=str(uuid.uuid4())
        state=root/(thread+'.json')
        state.write_text('[]')
        result={'thread':{'id':thread},'model':'fixture'}
    elif method=='thread/resume':
        thread=params['threadId']
        state=root/(thread+'.json')
        assert state.exists()
        result={'thread':{'id':thread},'model':'fixture'}
    elif method=='turn/start':
        turn='turn-'+str(uuid.uuid4())
        result={'turn':{'id':turn,'status':'inProgress'}}
    if 'id' in request:send({'id':request['id'],'result':result})
    if method=='turn/start':
        prompt=json.dumps(params.get('input'))
        if 'cancel-this-call' in prompt:continue
        history=json.loads(state.read_text())
        answer='continued' if 'follow-up' in prompt and history else 'answer-start:'+'evidence'*3000+':answer-end'
        history.append(prompt);state.write_text(json.dumps(history))
        send({'method':'item/agentMessage/delta','params':{'threadId':thread,'turnId':turn,'itemId':'answer','delta':answer}})
        send({'method':'item/completed','params':{'threadId':thread,'turnId':turn,'item':{'id':'answer','type':'agentMessage','text':answer,'phase':'final_answer'}}})
        send({'method':'turn/completed','params':{'threadId':thread,'turn':{'id':turn,'status':'completed'}}})
`, runRoot)
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(fixture), 0700); err != nil {
		t.Fatal(err)
	}
	// This Codex protocol fixture does not launch MCP subprocesses. Keep the
	// required executable lookup local, and fail visibly if that changes.
	bridge := filepath.Join(bin, "mcpbridge")
	if err := os.WriteFile(bridge, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_BRIDGE_BINARY", bridge)
	t.Setenv("MCP_API_URL", files.URL)
	t.Setenv("MCP_API_TOKEN", "fixture-bridge-token")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	key := "fixture-only"
	newController := func() *StepBasedWorkflowOrchestrator {
		t.Helper()
		base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{APIKeys: &orchestrator.APIKeys{CodexCLI: &key}}, 10, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		c := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
		c.SetMCPSessionID("python-provider-fixture")
		c.SetWorkspacePath(root)
		c.selectedRunFolder = "run-one"
		c.WorkspaceClient = workspace.NewClient(files.URL)
		return c
	}
	c := newController()
	// The executable fixture has no real model or tools. Actual enforcing shell
	// guards are tested independently below and through the real Python runner.
	previousResolver := clisecurity.ResolveWorkflowPolicy
	clisecurity.ResolveWorkflowPolicy = func(context.Context, string, string, string) (*llmtypes.CLISecurityPolicy, error) {
		return &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeCompatibility}, nil
	}
	t.Cleanup(func() { clisecurity.ResolveWorkflowPolicy = previousResolver })
	parent := &ScriptedPlanStep{CommonStepFields: CommonStepFields{ID: "parent"}, AgentConfigs: &AgentConfigs{ExecutionLLM: &AgentLLMConfig{Provider: "codex-cli", ModelID: "fixture"}, KnowledgebaseAccess: "none"}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first := c.runPythonAgent(ctx, parent, map[string]interface{}{"name": "Research", "messages": []string{"first task"}}, "first")
	large := "answer-start:" + strings.Repeat("evidence", 3000) + ":answer-end"
	if !first.OK || !first.LogComplete || first.LogKind != "provider_transcript" || len(first.Text) >= len(large) {
		t.Fatalf("first result is not a bounded completed transcript receipt: %+v", first)
	}
	log, err := os.ReadFile(first.LogFile)
	if err != nil || !strings.Contains(string(log), large) {
		t.Fatalf("full answer missing from transcript: %v", err)
	}
	// Simulate a fresh server-side controller, retaining only disk state,
	// plus stale registry/env entries left by a crashed prior call.
	c = newController()
	ownedID := "python-runtime-" + first.ConversationID
	unrelatedID := "unrelated-python-runtime"
	stale, unrelated := &runtimeCleanupClient{}, &runtimeCleanupClient{}
	registry := mcpclient.GetSessionRegistry()
	registry.StoreConnection(ownedID, "stale", stale)
	registry.StoreConnection(unrelatedID, "unrelated", unrelated)
	common.SetSessionShellEnv(ownedID, map[string]string{"STALE_FIXTURE": "must not survive"})
	mcpagent.MarkSessionsStopped([]string{ownedID, unrelatedID})
	t.Cleanup(func() { mcpagent.CloseSession(unrelatedID); mcpagent.ClearSessionsStopped([]string{unrelatedID}) })
	second := c.runPythonAgent(ctx, parent, map[string]interface{}{"name": "Research", "messages": []string{"follow-up"}, "conversation_id": first.ConversationID}, "second")
	if !second.OK || second.ConversationID != first.ConversationID || !strings.Contains(second.Text, "continued") {
		t.Fatalf("continuation failed: %+v", second)
	}
	if stale.closes != 1 || unrelated.closes != 0 || !registry.IsSessionStopped(unrelatedID) || !registry.HasSession(unrelatedID) {
		t.Fatal("reconstruction retired or cleared an unrelated runtime, or kept stale connections")
	}
	fresh := c.runPythonAgent(ctx, parent, map[string]interface{}{"name": "Another conversation", "messages": []string{"separate task"}}, "separate")
	if !fresh.OK || fresh.ConversationID == first.ConversationID || fresh.ConversationID == "" {
		t.Fatalf("two conversations shared an identity: %+v", fresh)
	}
	var identities []string
	for _, conversation := range []string{first.ConversationID, fresh.ConversationID} {
		file, _ := c.pythonAgentConversationPath(conversation)
		raw, err := c.ReadWorkspaceFile(ctx, file)
		var saved pythonAgentConversation
		if err != nil || json.Unmarshal([]byte(raw), &saved) != nil || saved.AgentSessionHandle == nil {
			t.Fatalf("missing native continuation state: %v", err)
		}
		identities = append(identities, saved.AgentSessionHandle.SessionID)
	}
	if identities[0] == "" || identities[1] == "" || identities[0] == identities[1] {
		t.Fatal("different conversations shared the native tool-session identity")
	}
	requests, err := os.ReadFile(filepath.Join(runRoot, "fixture-requests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(requests), `"method": "thread/resume"`) {
		t.Fatal("follow-up did not restore the native thread")
	}
	changed := c.runPythonAgent(ctx, parent, map[string]interface{}{"name": "Research", "messages": []string{"change"}, "conversation_id": first.ConversationID, "model": "codex-cli/another"}, "changed")
	if changed.OK || !strings.Contains(changed.Error, "original model") {
		t.Fatalf("changed settings resumed silently: %+v", changed)
	}
	foreign := *parent
	foreign.ID = "another-parent"
	refused := c.runPythonAgent(ctx, &foreign, map[string]interface{}{"name": "Research", "messages": []string{"read"}, "conversation_id": first.ConversationID}, "foreign")
	if refused.OK || !strings.Contains(refused.Error, "does not belong") {
		t.Fatalf("another script imported the conversation: %+v", refused)
	}
	timed, cancelTimed := context.WithTimeout(ctx, 500*time.Millisecond)
	cancelled := c.runPythonAgent(timed, parent, map[string]interface{}{"name": "Research", "messages": []string{"cancel-this-call"}}, "cancelled")
	cancelTimed()
	if cancelled.OK || cancelled.Error == "" || !cancelled.LogComplete {
		t.Fatalf("cancellation was reported successful or lost its transcript: %+v", cancelled)
	}
	denied := newController()
	resolved := false
	denied.GetLLMConfig().APIKeys.ResolveConnection = func(context.Context, mcpllm.Provider, string) (*mcpllm.ProviderAPIKeys, error) {
		resolved = true
		return nil, fmt.Errorf("No login is available for this automation; choose a configured coding agent in the Crew chat")
	}
	missing := denied.runPythonAgent(ctx, parent, map[string]interface{}{"name": "No account", "messages": []string{"must not run"}, "model": "codex-cli/fixture"}, "missing-account")
	if missing.OK || !resolved || !strings.Contains(missing.Error, "No login is available") {
		t.Fatalf("missing account fell through to a default login or failed at another boundary: %+v", missing)
	}
}

// A helper has the same whole-automation read / current-run write boundary;
// raw managed DB and authored definitions stay protected by the real client.
func TestPythonAgentGuardsAndResourceSelectionsStayWithinParent(t *testing.T) {
	_, _ = workflowHTTPTestWorkspace(t)
	c := newAgentFactoryTestOrchestrator(t)
	c.SetWorkspacePath("Workflow/demo")
	c.selectedRunFolder = "run-one"
	parent := &ScriptedPlanStep{CommonStepFields: CommonStepFields{ID: "parent"}, AgentConfigs: &AgentConfigs{ExecutionLLM: &AgentLLMConfig{Provider: "codex-cli", ModelID: "fixture"}, KnowledgebaseAccess: "none"}}
	saved, selection, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Write", Messages: []string{"Write evidence"}})
	if err != nil {
		t.Fatal(err)
	}
	parent.AgentConfigs.ExecutionLLM.ConnectionID = "selected-login"
	changedModel, _, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Write", Messages: []string{"Write evidence"}, Model: "codex-cli/another-model"})
	if err != nil || changedModel.Config.ExecutionLLM.ConnectionID != "selected-login" {
		t.Fatalf("changing a model silently changed the selected account: %v", err)
	}
	const session = "python-helper-guard"
	defer common.ClearSessionShellConfig(session)
	read, write := c.setupAgentFolderGuard(saved.StepID, saved.StepID, &saved.Config, AgentWriteAccess{DB: true})
	c.configureSubAgentSessionGuard(session, "agent", saved.StepID, read, write, false)
	configureWorkflowDBSession(session, c.GetWorkspacePath(), DBAccessReadWrite, false)
	client := workspace.NewClient("http://unused")
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, session)
	for _, path := range []string{"Workflow/demo/code/task/main.py", "Workflow/demo/runs/previous/report.md"} {
		if err := client.ValidatePathWithContext(ctx, path, false); err != nil {
			t.Fatalf("own automation read refused: %s %v", path, err)
		}
	}
	if err := client.ValidatePathWithContext(ctx, "Workflow/demo/runs/run-one/report.md", true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"Workflow/demo/planning/plan.json", "Workflow/demo/code/task/main.py", "Workflow/demo/runs/previous/report.md", "Workflow/other/report.md", "Workflow/demo/db/db.sqlite", "Workflow/demo/runs/run-one/helpers/conversations/forged.json", "Workflow/demo/runs/run-one/helpers/cached-result.json"} {
		if err := client.ValidatePathWithContext(ctx, path, true); err == nil {
			t.Fatalf("helper wrote protected path %s", path)
		}
	}
	if err := client.ValidatePathWithContext(ctx, "Workflow/demo/db/db.sqlite", false); err == nil {
		t.Fatal("helper read raw managed database")
	}
	// Applying the helper's empty MCP selection cannot retain an auto-added
	// Place connection, or mutate another agent's configuration map.
	original := mcpclient.RuntimeOverrides{"owned-place": {}}
	config := &agents.OrchestratorAgentConfig{ServerNames: []string{"owned-place"}, RuntimeOverrides: original, SelectedTools: []string{"owned-place:*"}}
	selection.apply(config)
	if len(config.ServerNames) != 1 || config.ServerNames[0] != mcpclient.NoServers || len(config.RuntimeOverrides) != 0 || len(config.SelectedTools) != 0 || len(original) != 1 {
		t.Fatal("empty selection inherited or changed owned Place connections")
	}
	c.WorkspaceTools = virtualtools.CreateWorkspaceBrowserTools()
	c.WorkspaceToolExecutors = map[string]interface{}{"agent_browser": func(context.Context, map[string]interface{}) (string, error) { return "unused", nil }}
	tools, executors := c.prepareExecutionTools(&saved.Config, parent, nil, DBAccessReadWrite)
	tools, executors = selection.filterTools(tools, executors)
	for _, tool := range tools {
		if tool.Function != nil && tool.Function.Name == "agent_browser" {
			t.Fatal("helper inherited implicit browser capability")
		}
	}
	if _, admitted := executors["agent_browser"]; admitted {
		t.Fatal("helper retained browser executor")
	}
	for _, args := range []pythonAgentArguments{{Name: "Write", Messages: []string{"task"}, MCP: []string{"not-selected"}}, {Name: "Write", Messages: []string{"task"}, Skills: []string{"missing-fixture-skill"}}} {
		if _, _, err := c.preparePythonAgent(context.Background(), parent, args); err == nil {
			t.Fatal("missing resource was silently admitted")
		}
	}
}
