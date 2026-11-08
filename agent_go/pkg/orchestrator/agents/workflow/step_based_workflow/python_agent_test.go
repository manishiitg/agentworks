package step_based_workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/clisecurity"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/executor"
	mcpllm "github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
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
	slotProof := os.Getenv("AGENTWORKS_PYTHON_AGENT_SLOT_E2E") == "1"
	if !slotProof {
		t.Setenv("AGENTWORKS_SLOTS", "off")
	}
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CODEX_CLI_TRANSPORT", "app-server")
	const root = "Workflow/python-agent"
	runRoot := filepath.Join(docs, root, "runs/run-one")
	fixtureRoot := runRoot
	sourceDir := filepath.Join(runRoot, "execution/frozen/code")
	authoredDir := filepath.Join(docs, "Crew/authoring/.agentworks/code/script")
	if err := os.MkdirAll(filepath.Join(authoredDir, "shared"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authoredDir, "shared/x.py"), []byte("print('wrong-authoring-source')"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"workflow.json": `{"code_layout_version":1}`, "soul/soul.md": "# Task\nDo the requested work.", "planning/plan.json": `{"steps":[]}`, "runs/run-one/marker": "run"} {
		file := filepath.Join(docs, root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if slotProof {
		// Match the prepared host's Crew ownership. Declaring run_as for an
		// owner-only service temp directory does not make it the slot's folder.
		group, err := user.LookupGroup("slot01")
		if err != nil {
			t.Fatal(err)
		}
		gid, err := strconv.Atoi(group.Gid)
		if err != nil {
			t.Fatal(err)
		}
		for directory := runRoot; strings.HasPrefix(directory, os.TempDir()+string(filepath.Separator)); directory = filepath.Dir(directory) {
			info, err := os.Stat(directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(directory, -1, gid); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(directory, info.Mode()|0050); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chmod(runRoot, os.ModeSetgid|0770); err != nil {
			t.Fatal(err)
		}
	}
	// A managed Crew already has its database before an agent starts. Keep
	// only that baseline here; the native child creates and verifies its table.
	if err := os.MkdirAll(filepath.Join(docs, root, "db"), 0750); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(docs, root, "db/db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, setupErr := db.Exec("CREATE TABLE fixture_setup (id INTEGER)")
	closeErr := db.Close()
	if setupErr != nil || closeErr != nil {
		t.Fatalf("managed database fixture: %v %v", setupErr, closeErr)
	}
	bin := t.TempDir()
	// Match an installed shared CLI; owner-only fixture programs cannot run as a slot.
	if err := os.Chmod(bin, 0755); err != nil {
		t.Fatal(err)
	}
	fixture := "#!" + python + "\n" + fmt.Sprintf(`import json,os,pwd,sys,uuid,urllib.request,subprocess
from pathlib import Path
root=Path(%q)
rollouts=Path.cwd()/'.codex/sessions/2026/01/01'
if '--version' in sys.argv:
    print('codex-cli 1.0.0');raise SystemExit(0)
def trace(stage, reason=''):
    try:
        header,credential=os.environ['MCP_AUTH'].split(':',1)
        request=urllib.request.Request(os.environ['MCP_CUSTOM']+'/_fixture_trace',data=json.dumps({'stage':stage,'reason':reason}).encode(),headers={'Content-Type':'application/json',header:credential.strip()})
        with urllib.request.urlopen(request,timeout=3) as response: response.read()
    except Exception:
        pass
sys.excepthook=lambda kind,error,tb: trace('python-error',type(error).__name__+': '+str(error))
trace('started')
thread=''
state=None
def send(value):
    print(json.dumps(value),flush=True)
for line in sys.stdin:
    request=json.loads(line)
    method=request.get('method','')
    trace(method)
    params=request.get('params',{})
    with (root/'fixture-requests.jsonl').open('a') as out:
        out.write(json.dumps({'method':method,'thread':params.get('threadId'),'input':params.get('input'),'effort':params.get('effort'),'cwd':str(Path.cwd())})+'\n')
    result={}
    if method=='thread/start':
        thread=str(uuid.uuid4())
        rollouts.mkdir(parents=True,exist_ok=True)
        state=rollouts/('rollout-fixture-'+thread+'.jsonl')
        state.write_text('[]')
        result={'thread':{'id':thread},'model':'fixture'}
    elif method=='thread/resume':
        thread=params['threadId']
        state=rollouts/('rollout-fixture-'+thread+'.jsonl')
        if not state.exists():
            send({'id':request['id'],'error':{'code':-32600,'message':'no rollout found for thread id '+thread}})
            continue
        result={'thread':{'id':thread},'model':'fixture'}
    elif method=='turn/start':
        turn='turn-'+str(uuid.uuid4())
        result={'turn':{'id':turn,'status':'inProgress'}}
    if 'id' in request:send({'id':request['id'],'result':result})
    if method=='turn/start':
        prompt=json.dumps(params.get('input'))
        if 'cancel-this-call' in prompt:continue
        try:
            if 'first task' in prompt or 'follow-up' in prompt:
                assert params.get('effort')=='high', 'per-call reasoning did not reach the native provider'
            if 'first task' in prompt:
                if %s: assert pwd.getpwuid(os.geteuid()).pw_name=='slot01', 'native provider did not run as slot01'
                assert Path.cwd()==root, 'native provider did not start in its run root'
                assert Path(os.environ['WORKFLOW_CODE_ROOT'])==root/'execution/frozen/code'
                assert os.environ['SCRIPT_SOURCE_DIR']==os.environ['WORKFLOW_CODE_ROOT']
                package=subprocess.check_output(['python3','-B','-c',"import os,tiny_fixture; from pathlib import Path; assert Path(tiny_fixture.__file__).is_relative_to(Path(os.environ['WORKFLOW_CODE_DEPS'])); print(tiny_fixture.marker)"],text=True)
                assert package.strip()=='native-package-ok', package
                assert json.loads((Path(os.environ['WORKFLOW_CODE_ROOT'])/'script.json').read_text())['id']=='fixture-script'
                shared=subprocess.check_output(['/bin/sh','-c','python3 "$WORKFLOW_CODE_ROOT/shared/x.py"'],text=True)
                assert shared.strip()=='frozen-shared-ok', shared
                (root/'new.json').write_text('{"created":true}')
                (root/'new-folder').mkdir()
                (root/'new-folder/result.txt').write_text('native-output')
                try: (root/'execution/frozen/code/main.py').write_text('forged')
                except PermissionError: pass
                else: raise AssertionError('native provider changed its frozen source')
                if %s:
                    try: (root/'helpers/fixture-record.json').write_text('forged')
                    except PermissionError: pass
                    else: raise AssertionError('slot changed a server-owned record')
                base=os.environ['MCP_CUSTOM']
                header,credential=os.environ['MCP_AUTH'].split(':',1)
                def database(action,sql):
                    req=urllib.request.Request(base+'/database',data=json.dumps({'action':action,'sql':sql}).encode(),headers={'Content-Type':'application/json',header:credential.strip()})
                    with urllib.request.urlopen(req) as response: envelope=json.load(response)
                    assert envelope['success'], envelope
                    return json.loads(envelope['result'])
                database('mutate','CREATE TABLE IF NOT EXISTS agent_evidence (id INTEGER PRIMARY KEY)')
                database('mutate','INSERT INTO agent_evidence VALUES (1)')
                assert database('query','SELECT id FROM agent_evidence')['rows'][0]['id']==1
                (root/'database-readback.json').write_text(json.dumps({'session':os.environ['MCP_SESSION_ID'],'row':1}))
        except Exception as error:
            message=type(error).__name__+': '+str(error)
            (root/'fixture-error.txt').write_text(message)
            send({'method':'turn/completed','params':{'threadId':thread,'turn':{'id':turn,'status':'failed','error':{'message':message}}}})
            continue
        history=json.loads(state.read_text())
        answer='continued' if 'follow-up' in prompt and history else 'answer-start:'+'evidence'*3000+':answer-end'
        history.append(prompt);state.write_text(json.dumps(history))
        send({'method':'item/agentMessage/delta','params':{'threadId':thread,'turnId':turn,'itemId':'answer','delta':answer}})
        send({'method':'item/completed','params':{'threadId':thread,'turnId':turn,'item':{'id':'answer','type':'agentMessage','text':answer,'phase':'final_answer'}}})
        send({'method':'turn/completed','params':{'threadId':thread,'turn':{'id':turn,'status':'completed'}}})
`, fixtureRoot, map[bool]string{true: "True", false: "False"}[slotProof], map[bool]string{true: "True", false: "False"}[slotProof])
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(fixture), 0755); err != nil {
		t.Fatal(err)
	}
	// This Codex protocol fixture does not launch MCP subprocesses. Keep the
	// required executable lookup local, and fail visibly if that changes.
	bridge := filepath.Join(bin, "mcpbridge")
	if err := os.WriteFile(bridge, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_BRIDGE_BINARY", bridge)
	common.SetBridgeTokenSecret("fixture-only-signing-key")
	t.Cleanup(func() { common.SetBridgeTokenSecret("") })
	var traceMu sync.Mutex
	var traceEntries []string
	handlers := executor.NewExecutorHandlers("", loggerv2.NewNoop())
	bridgeAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		session, valid := common.VerifyBridgeToken(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if !valid || len(parts) != 5 || parts[0] != "s" || parts[1] != session || !strings.HasPrefix(session, "python-runtime-python-agent-") {
			http.Error(w, "own agent session required", http.StatusForbidden)
			return
		}
		if parts[4] == "_fixture_trace" {
			var event struct {
				Stage  string `json:"stage"`
				Reason string `json:"reason"`
			}
			if json.NewDecoder(r.Body).Decode(&event) != nil {
				http.Error(w, "invalid fixture trace", 400)
				return
			}
			traceMu.Lock()
			traceEntries = append(traceEntries, event.Stage+": "+event.Reason)
			traceMu.Unlock()
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": "{}"})
			return
		}
		r.Header.Set("X-Session-ID", session)
		// Match the serving host's authenticated /s/{id}/tools/custom route:
		// both the registry and DB resolver receive the verified child identity.
		ctx := context.WithValue(r.Context(), common.ChatSessionIDKey, session)
		handlers.HandlePerToolCustomRequest(w, r.WithContext(ctx), parts[4])
	}))
	defer bridgeAPI.Close()
	t.Setenv("MCP_API_URL", bridgeAPI.URL)
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
		c.crewAutomationCodeRoot = authoredDir
		c.selectedRunFolder = "run-one"
		c.WorkspaceClient = workspace.NewClient(files.URL)
		database := virtualtools.CreateWorkflowDBToolRegistry(files.URL, "", "unusable-parent-session")
		c.WorkspaceTools, c.ToolCategories = database.Tools, database.Categories
		c.WorkspaceToolExecutors = map[string]interface{}{}
		for name, execute := range database.Executors {
			c.WorkspaceToolExecutors[name] = execute
		}
		return c
	}
	c := newController()
	if err := c.materializeScriptBundle(context.Background(), root+"/runs/run-one/execution/frozen/code", map[string][]byte{"main.py": []byte("frozen-source"), "shared/x.py": []byte("print('frozen-shared-ok')"), "requirements.txt": []byte("tiny-fixture==1.0\n")}); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteWorkspaceFile(context.Background(), root+"/runs/run-one/execution/frozen/code/script.json", `{"id":"fixture-script","name":"Fixture"}`); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runRoot, "helpers"), 0700); err != nil {
		t.Fatal(err)
	}
	helperRoot, err := os.OpenRoot(filepath.Join(runRoot, "helpers"))
	if err != nil {
		t.Fatal(err)
	}
	if err := workflowfiles.WriteServerFile(helperRoot, "fixture-record.json", []byte(`{"server":true}`)); err != nil {
		t.Fatal(err)
	}
	helperRoot.Close()
	t.Cleanup(func() { c.finishPythonAgentSessions("failed") })
	// The executable fixture has no real model or tools. Actual enforcing shell
	// guards are tested independently below and through the real Python runner.
	previousResolver := clisecurity.ResolveWorkflowPolicy
	clisecurity.ResolveWorkflowPolicy = func(context.Context, string, string, string) (*llmtypes.CLISecurityPolicy, error) {
		return &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeCompatibility}, nil
	}
	if slotProof {
		clisecurity.ResolveWorkflowPolicy = func(_ context.Context, session, provider, cwd string) (*llmtypes.CLISecurityPolicy, error) {
			host := clisecurity.Host{WorkflowExecution: true, DocsRoot: docs,
				ResolvePath:    func(path string) string { return workflowAbsPath(path) },
				LookupRoots:    func(string) (crewroot.Roots, bool, error) { return crewroot.Roots{}, false, nil },
				LandlockRunner: func() (string, bool) { runner := os.Getenv("AGENTWORKS_LANDLOCK_RUNNER"); return runner, runner != "" },
			}
			base := &llmtypes.CLISecurityPolicy{RunAs: llmtypes.RunAs{Declared: true, User: "u1", Slot: "slot01", Root: cwd}, HostReadPaths: []string{bin, python}}
			runtimeDir := filepath.Join(os.Getenv("AGENTWORKS_STATE_ROOT"), "cli-runtimes", "v1", session)
			policy, decision, why := host.Policy(session, provider, cwd, base, runtimeDir)
			if decision != clisecurity.Confined {
				return nil, fmt.Errorf("slot proof needs confinement: %s", why)
			}
			policy.DeclareRunAs()
			return policy, nil
		}
	}
	t.Cleanup(func() { clisecurity.ResolveWorkflowPolicy = previousResolver })
	supportedModel := ""
	for _, model := range codexcli.GetAllCodexCLIModels() {
		if model.SupportsReasoningEffort && slices.Contains(model.ReasoningEffortLevels, "high") {
			supportedModel = model.ModelID
			break
		}
	}
	if supportedModel == "" {
		t.Fatal("no Codex model offers the reasoning needed by this protocol fixture")
	}
	parent := &ScriptedPlanStep{CommonStepFields: CommonStepFields{ID: "parent"}, AgentConfigs: &AgentConfigs{ExecutionLLM: &AgentLLMConfig{Provider: "codex-cli", ModelID: supportedModel, Options: map[string]interface{}{"reasoning_effort": "low"}}, KnowledgebaseAccess: "none"}, crewScript: &CrewScriptConfiguration{Definition: CrewScriptDefinition{ID: "fixture-script"}, Files: map[string][]byte{"requirements.txt": []byte("tiny-fixture==1.0\n")}}}
	dependencies := c.scriptDependencies(parent)
	if err := os.MkdirAll(dependencies, 0750|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dependencies, "tiny_fixture.py"), []byte("marker='native-package-ok'\n"), 0440); err != nil {
		t.Fatal(err)
	}
	if slotProof {
		group, err := user.LookupGroup("slot01")
		if err != nil {
			t.Fatal(err)
		}
		gid, err := strconv.Atoi(group.Gid)
		if err != nil {
			t.Fatal(err)
		}
		for directory := dependencies; strings.HasPrefix(directory, filepath.Join(docs, root, ".sandbox-cache")); directory = filepath.Dir(directory) {
			if err := os.Chown(directory, -1, gid); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(directory, 0750|os.ModeSetgid); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chown(filepath.Join(dependencies, "tiny_fixture.py"), -1, gid); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	missingDB := newController()
	delete(missingDB.WorkspaceToolExecutors, "mutate_workflow_db")
	missingBinding := missingDB.runPythonAgent(ctx, parent, map[string]interface{}{"name": "Missing database", "messages": []string{"first task"}}, "missing-database")
	if missingBinding.OK || !strings.Contains(missingBinding.Error, "database tools are unavailable") {
		t.Fatalf("missing binding did not refuse before launch: %+v", missingBinding)
	}
	if _, err := os.Stat(filepath.Join(fixtureRoot, "fixture-requests.jsonl")); !os.IsNotExist(err) {
		t.Fatal("provider launched before database preflight")
	}
	handler, finishSteps := c.pythonAutomationHandler(ctx, parent, sourceDir, runRoot)
	defer finishSteps(false)
	rawFirst, firstErr := handler(ctx, map[string]interface{}{"action": "agent", "key": "first", "order": float64(1), "arguments": map[string]interface{}{"name": "Research", "messages": []string{"first task"}, "reasoning": "high"}})
	var first pythonHelperReply
	if firstErr != nil || json.Unmarshal([]byte(rawFirst), &first) != nil {
		t.Fatalf("Python helper dispatch failed: %v %s", firstErr, rawFirst)
	}
	large := "answer-start:" + strings.Repeat("evidence", 3000) + ":answer-end"
	if !first.OK || !first.LogComplete || first.LogKind != "provider_transcript" || len(first.Text) >= len(large) {
		errorText, _ := os.ReadFile(filepath.Join(fixtureRoot, "fixture-error.txt"))
		traceMu.Lock()
		entries := append([]string{}, traceEntries...)
		traceMu.Unlock()
		t.Fatalf("first result is not a bounded completed transcript receipt: %+v fixture=%s trace=%v", first, errorText, entries)
	}
	readback, readErr := os.ReadFile(filepath.Join(fixtureRoot, "database-readback.json"))
	if readErr != nil || !strings.Contains(string(readback), `"row": 1`) || !strings.Contains(string(readback), `python-runtime-`+first.ConversationID) {
		t.Fatalf("native agent did not write/read a row through its own bridge: %s %v", readback, readErr)
	}
	if output, err := os.ReadFile(filepath.Join(runRoot, "new-folder/result.txt")); err != nil || string(output) != "native-output" {
		t.Fatalf("native root output folder was unavailable: %q %v", output, err)
	}
	if source, err := os.ReadFile(filepath.Join(runRoot, "execution/frozen/code/main.py")); err != nil || string(source) != "frozen-source" {
		t.Fatalf("native launch changed frozen source: %q %v", source, err)
	}
	if slotProof {
		t.Log("native slot01 executed shared/x.py from the frozen code root, created run-root outputs, refused source/record writes and used its signed child DB bridge")
		return
	}
	ctx = context.WithValue(ctx, pythonScriptRuntimeKey{}, pythonScriptRuntime{SourceDir: sourceDir, DependenciesDir: dependencies})
	log, err := os.ReadFile(first.LogFile)
	if err != nil || !strings.Contains(string(log), large) {
		t.Fatalf("full answer missing from transcript: %v", err)
	}
	answer, err := os.ReadFile(first.AnswerFile)
	if err != nil || string(answer) != large {
		t.Fatalf("full final answer was missing or mixed with transcript events: %v", err)
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
	secondAnswer, answerErr := os.ReadFile(second.AnswerFile)
	if answerErr != nil || string(secondAnswer) != "continued" || second.AnswerFile == first.AnswerFile {
		t.Fatalf("continuation did not save its own final answer: %v", answerErr)
	}
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
	var nativeHomes []string
	for _, conversation := range []string{first.ConversationID, fresh.ConversationID} {
		file, _ := c.pythonAgentConversationPath(conversation)
		raw, err := c.ReadWorkspaceFile(ctx, file)
		var saved pythonAgentConversation
		if err != nil || json.Unmarshal([]byte(raw), &saved) != nil || saved.AgentSessionHandle == nil {
			t.Fatalf("missing native continuation state: %v", err)
		}
		if conversation == first.ConversationID && (saved.Settings.Reasoning != "high" || saved.Config.ExecutionLLM.Options["reasoning_effort"] != "high") {
			t.Fatal("saved continuation lost the requested reasoning level")
		}
		identities = append(identities, saved.AgentSessionHandle.SessionID)
		home := saved.AgentSessionHandle.Provider.WorkingDir
		rollout := filepath.Join(home, ".codex/sessions/2026/01/01", "rollout-fixture-"+saved.AgentSessionHandle.Provider.NativeSessionID+".jsonl")
		if _, err := os.Stat(rollout); err != nil {
			t.Fatalf("closing a call removed its native rollout: %v", err)
		}
		nativeHomes = append(nativeHomes, saved.AgentSessionHandle.Provider.RuntimeDir)
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
	if parent.AgentConfigs.ExecutionLLM.Options["reasoning_effort"] != "low" {
		t.Fatal("per-call reasoning changed the parent model's default")
	}
	changedReasoning := c.runPythonAgent(ctx, parent, map[string]interface{}{"name": "Research", "messages": []string{"change"}, "conversation_id": first.ConversationID, "reasoning": "low"}, "changed-reasoning")
	if changedReasoning.OK || !strings.Contains(changedReasoning.Error, "original model") {
		t.Fatalf("continued conversation silently changed reasoning: %+v", changedReasoning)
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
	c.finishPythonAgentSessions("waiting_for_capacity")
	c.finishPythonAgentSessions("interrupted")
	for _, home := range nativeHomes {
		if _, err := os.Stat(home); err != nil {
			t.Fatalf("an unfinished run lost its native home: %v", err)
		}
	}
	// No in-memory runtime list survives a process restart. The finalizer uses
	// only the protected conversation files written before each launch.
	c = newController()
	c.finishPythonAgentSessions("completed")
	for _, home := range nativeHomes {
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatalf("a finished run retained its native home: %s %v", home, err)
		}
	}
	for _, status := range []string{"failed", "stopped", "cancelled"} {
		if err := os.MkdirAll(nativeHomes[0], 0700); err != nil {
			t.Fatal(err)
		}
		c.finishPythonAgentSessions(status)
		if _, err := os.Stat(nativeHomes[0]); !os.IsNotExist(err) {
			t.Fatalf("terminal %s retained its native home: %v", status, err)
		}
	}
	denied.finishPythonAgentSessions("failed")
}

// A helper has the same whole-automation read / current-run write boundary;
// raw managed DB and authored definitions stay protected by the real client.
func TestPythonAgentGuardsAndResourceSelectionsStayWithinParent(t *testing.T) {
	docs, _ := workflowHTTPTestWorkspace(t)
	if err := os.MkdirAll(filepath.Join(docs, "Workflow/demo"), 0700); err != nil {
		t.Fatal(err)
	}
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
	// A deployment-pinned Pi model must not silently discard a requested level.
	template := t.TempDir()
	for name, data := range map[string]string{
		"models.json":   `{"providers":{"example":{"baseUrl":"https://example.com/v1","api":"openai-completions","apiKey":"$EXAMPLE_API_KEY","models":[{"id":"example-model","contextWindow":1000}]}}}`,
		"settings.json": fmt.Sprintf(`{"modelThinkingLevels":{"example/example-model":"off",%q:"low"}}`, picli.DefaultModelID),
	} {
		if err := os.WriteFile(filepath.Join(template, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PI_CLI_AGENT_TEMPLATE_DIR", template)
	for _, choice := range []struct{ model, effort, want string }{
		{"cursor-cli/auto", "high", "cursor-cli/auto does not offer reasoning=high"},
		{"cursor-cli/glm-5.3", "medium", "does not offer reasoning=medium"},
		{"codex-cli/fixture", "max", "Choose reasoning="},
		{"pi-cli/example/example-model", "high", "does not offer reasoning=high"},
		{"claude-code/claude-code", "high", ""},
		{"pi-cli/" + picli.DefaultModelID, "high", "does not offer reasoning=high"},
		{"pi-cli/" + picli.DefaultModelID, "low", ""},
	} {
		selected, _, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Reason", Messages: []string{"Task"}, Model: choice.model, Reasoning: choice.effort})
		if choice.want == "" {
			if err != nil || selected.Config.ExecutionLLM.Options["reasoning_effort"] != choice.effort {
				t.Fatalf("offered reasoning refused or dropped: %s %s: %v", choice.model, choice.effort, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), choice.want) {
			t.Fatalf("unsupported reasoning must refuse by name: %s %s: %v", choice.model, choice.effort, err)
		}
	}
	const session = "python-helper-guard"
	defer common.ClearSessionShellConfig(session)
	read, write := c.setupAgentFolderGuard(saved.StepID, saved.StepID, &saved.Config, AgentWriteAccess{DB: true})
	c.configureSubAgentSessionGuard(session, "agent", saved.StepID, read, write, false)
	configureWorkflowDBSession(session, c.GetWorkspacePath(), DBAccessReadWrite, false)
	client := workspace.NewClient("http://unused")
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, session)
	for _, path := range []string{"Workflow/demo/code/task/main.py", "Workflow/demo/runs/previous/report.md", "Workflow/demo/knowledgebase/context/rules.md"} {
		if err := client.ValidatePathWithContext(ctx, path, false); err != nil {
			t.Fatalf("own automation read refused: %s %v", path, err)
		}
	}
	if err := client.ValidatePathWithContext(ctx, "Workflow/demo/runs/run-one/report.md", true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"Workflow/demo/knowledgebase/notes/default-denied.md", "Workflow/demo/planning/plan.json", "Workflow/demo/code/task/main.py", "Workflow/demo/runs/previous/report.md", "Workflow/other/report.md", "Workflow/demo/db/db.sqlite", "Workflow/demo/runs/run-one/helpers/conversations/forged.json", "Workflow/demo/runs/run-one/helpers/cached-result.json"} {
		if err := client.ValidatePathWithContext(ctx, path, true); err == nil {
			t.Fatalf("helper wrote protected path %s", path)
		}
	}
	if err := client.ValidatePathWithContext(ctx, "Workflow/demo/db/db.sqlite", false); err == nil {
		t.Fatal("helper read raw managed database")
	}
	writable, _, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "KB update", Messages: []string{"Record a fact"}, Knowledge: "write"})
	if err != nil || writable.Config.KnowledgebaseAccess != KBAccessReadWrite {
		t.Fatalf("knowledge write selection: %v", err)
	}
	kbRead, kbWrite := c.setupAgentFolderGuard(writable.StepID, writable.StepID, &writable.Config, AgentWriteAccess{DB: true, Knowledgebase: true})
	c.configureSubAgentSessionGuard(session, "agent", writable.StepID, kbRead, kbWrite, false)
	if err := client.ValidatePathWithContext(ctx, "Workflow/demo/knowledgebase/notes/fact.md", true); err != nil {
		t.Fatalf("explicit knowledge write denied: %v", err)
	}
	if err := client.ValidatePathWithContext(ctx, "Workflow/demo/knowledgebase/context/rules.md", true); err == nil {
		t.Fatal("knowledge write widened beyond notes")
	}

	// Applying the helper's empty MCP selection cannot retain an auto-added
	// Place connection, or mutate another agent's configuration map.
	original := mcpclient.RuntimeOverrides{"owned-place": {}}
	config := &agents.OrchestratorAgentConfig{ServerNames: []string{"owned-place"}, RuntimeOverrides: original, SelectedTools: []string{"owned-place:*"}}
	selection.apply(config)
	if len(config.ServerNames) != 1 || config.ServerNames[0] != mcpclient.NoServers || len(config.RuntimeOverrides) != 0 || len(config.SelectedTools) != 0 || len(original) != 1 {
		t.Fatal("empty selection inherited or changed owned Place connections")
	}
	// PLAT-746-446: requesting a selected MCP must retain its existing
	// read-tool restriction, while an unrestricted selected server still works.
	for _, selected := range [][]string{{"notes:read"}, nil} {
		parent.AgentConfigs.SelectedTools = selected
		inherited, _, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Read notes", Messages: []string{"Read selected notes"}})
		if err != nil {
			t.Fatal(err)
		}
		cfg := &agents.OrchestratorAgentConfig{SelectedTools: inherited.Config.SelectedTools}
		(&pythonAgentSelection{Servers: []string{"notes"}}).apply(cfg)
		filter := mcpagent.NewToolFilter(cfg.SelectedTools, cfg.ServerNames, map[string]mcpclient.ClientInterface{"notes": nil}, nil, nil)
		if !filter.ShouldIncludeTool("notes", "read", false, false) || filter.ShouldIncludeTool("notes", "write", false, false) != (len(selected) == 0) {
			t.Fatal("Python helper widened or lost the workflow's MCP tool selection", cfg.SelectedTools)
		}
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
	c.WorkspaceTools = append(c.WorkspaceTools, virtualtools.CreateHumanTools()...)
	c.WorkspaceToolExecutors["human_feedback"] = func(context.Context, map[string]interface{}) (string, error) { return "unused", nil }
	// PLAT-746-451: timers retain selected apps, but never person questions.
	c.SetExecutionOptions(&ExecutionOptions{ExecutionStrategy: ExecutionStrategyStartFromBeginningNoHuman})
	c.WorkspaceToolExecutors["google_workspace_cli"] = func(context.Context, map[string]interface{}) (string, error) { return "unused", nil }
	withHelp, explicit, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Ask", Messages: []string{"task"}, Browser: true})
	if err != nil {
		t.Fatal(err)
	}
	tools, executors = c.prepareExecutionTools(&withHelp.Config, parent, nil, DBAccessReadWrite)
	tools, executors = explicit.filterTools(tools, executors)
	for _, name := range []string{"agent_browser"} {
		if executors[name] == nil {
			t.Fatalf("explicit existing capability %s was dropped", name)
		}
	}
	for _, tool := range tools {
		if tool.Function != nil && (tool.Function.Name == "human_feedback" || tool.Function.Name == "send_email" || tool.Function.Name == "send_slack_message" || tool.Function.Name == "create_human_input_request") {
			t.Fatalf("asking the person broadened human tools: %s", tool.Function.Name)
		}
	}
	withGoogle, _, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Google", Messages: []string{"Use the selected app"}, Tools: []string{"human_tools:integrations.google"}})
	if err != nil {
		t.Fatal(err)
	}
	_, googleExecutors := c.prepareExecutionTools(&withGoogle.Config, parent, nil, DBAccessReadWrite)
	if googleExecutors["google_workspace_cli"] == nil || googleExecutors["human_feedback"] != nil || googleExecutors["send_email"] != nil {
		t.Fatal("named Google selection was lost or broadened")
	}
	withGoogle, _, err = c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Google", Messages: []string{"Use the selected app"}, Apps: []string{"google"}})
	if err != nil {
		t.Fatal(err)
	}
	_, googleExecutors = c.prepareExecutionTools(&withGoogle.Config, parent, nil, DBAccessReadWrite)
	if googleExecutors["google_workspace_cli"] == nil || googleExecutors["send_email"] != nil || googleExecutors["human_feedback"] != nil {
		t.Fatal("apps shorthand broadened or lost the connected Google selection")
	}
	path, err := c.pythonAgentConversationPath(withGoogle.StepID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(withGoogle)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.writePythonHelperState(context.Background(), path, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Google", Messages: []string{"Continue"}, ConversationID: withGoogle.StepID}); err != nil {
		t.Fatalf("omitted apps lost the original conversation selection: %v", err)
	}
	if _, _, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Google", Messages: []string{"Continue"}, ConversationID: withGoogle.StepID, Apps: []string{"github"}}); err == nil {
		t.Fatal("continued agent changed its original app selection")
	}
	if _, _, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Unknown", Messages: []string{"task"}, Apps: []string{"not-connected"}}); err == nil {
		t.Fatal("unknown app selection was admitted")
	}
	delete(c.WorkspaceToolExecutors, "google_workspace_cli")
	if _, _, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Native files", Messages: []string{"task"}, Tools: []string{"workspace_advanced:*", "workspace_advanced:terminal.execute", "workspace_advanced:files.patch", "workspace_advanced:browser.execute"}}); err != nil {
		t.Fatalf("old native tool entries blocked conversion: %v", err)
	}
	if _, _, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Google", Messages: []string{"task"}, Tools: []string{"human_tools:integrations.google"}}); err == nil {
		t.Fatal("unavailable app selection was admitted")
	}
	if _, _, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Google", Messages: []string{"task"}, Apps: []string{"google"}}); err == nil {
		t.Fatal("apps shorthand bypassed an unavailable app")
	}
	c.executionOptions.AgentFunctionTools = func(context.Context, AgentFunctionScope) ([]llmtypes.Tool, map[string]interface{}, error) {
		return []llmtypes.Tool{{Type: "function", Function: &llmtypes.FunctionDefinition{Name: "project_functions_call", Parameters: llmtypes.NewParameters(map[string]interface{}{"type": "object"})}}}, map[string]interface{}{
			"project_functions_call": func(context.Context, map[string]interface{}) (string, error) { return `{}`, nil },
		}, nil
	}
	_, functions, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Call", Messages: []string{"Use the published function"}, Tools: []string{agentworksproduct.ExecutionFunctionTools().Category + ":project_functions_call"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &agents.OrchestratorAgentConfig{}
	functions.apply(cfg)
	if len(cfg.DirectTools) != 1 || cfg.DirectTools[0].Name != "project_functions_call" || cfg.DirectTools[0].Execute == nil || cfg.DirectTools[0].InputSchema["type"] != "object" {
		t.Fatal("explicit function selection lost its scoped executor/schema")
	}
	c.WorkspaceTools = nil
	if _, _, err := c.preparePythonAgent(context.Background(), parent, pythonAgentArguments{Name: "Browser", Messages: []string{"task"}, Browser: true}); err == nil {
		t.Fatal("missing browser was silently admitted")
	}
	for _, args := range []pythonAgentArguments{{Name: "Write", Messages: []string{"task"}, MCP: []string{"not-selected"}}, {Name: "Write", Messages: []string{"task"}, Skills: []string{"missing-fixture-skill"}}} {
		if _, _, err := c.preparePythonAgent(context.Background(), parent, args); err == nil {
			t.Fatal("missing resource was silently admitted")
		}
	}
}
