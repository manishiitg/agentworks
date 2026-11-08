package step_based_workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

type pythonSavedScripts map[string]CrewScriptConfiguration

func (scripts pythonSavedScripts) ResolveCrewScript(_ context.Context, _, id string) (CrewScriptConfiguration, error) {
	return scripts[id], nil
}

// PLAT-746: nested Python uses the existing runner, inputs and saved-source
// evidence, while server-owned call stacks reject recursive side effects.
func TestPythonSavedScriptsKeepRunOutputsAndRejectRecursiveCalls(t *testing.T) {
	docs, files := workflowHTTPTestWorkspace(t)
	t.Setenv("AGENTWORKS_SLOTS", "off")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_BROWSER_STAGING_NAMESPACE", "saved-helper-"+filepath.Base(docs))
	const root = "Workflow/python-saved"
	write := func(name, text string) {
		t.Helper()
		name = filepath.Join(docs, root, name)
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("workflow.json", `{"code_layout_version":1}`)
	write("code/A/main.py", "pass\n")
	c, err := NewStepBasedWorkflowOrchestrator(t.Context(), "", "", 0, "", nil, nil, false, "", &orchestrator.LLMConfig{}, 1, loggerv2.NewNoop(), nil, nil, nil, nil, nil, nil, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetWorkspacePath(root)
	c.selectedRunFolder = "run-saved"
	c.WorkspaceClient = workspace.NewClient(files.URL)
	c.SetWorkspaceEnvRef(map[string]string{"MCP_API_TOKEN": "saved-script-fixture-token"})
	c.variableValues = map[string]string{"parent_only": "must not become a child's input"}
	scripts := pythonSavedScripts{}
	for _, id := range []string{"A", "B", "C", "D", "E", "F", "required", "failing", "timed-out"} {
		definition := CrewScriptDefinition{ID: id, Name: id, Inputs: []Variable{{Name: "count", Type: "integer", Value: "3"}, {Name: "next", Type: "string"}}, Outputs: []string{"result.json"}}
		if id == "required" {
			definition.Inputs = []Variable{{Name: "count", Type: "integer", Required: true}}
		}
		source := `import json, os
from pathlib import Path
from agentworks import inputs, run_script, ScriptResult
import subprocess, sys
# Converted step programs run as subprocesses and must keep this exact bridge.
program = """import os
from agentworks import check
assert os.environ['MCP_API_URL'].endswith('/s/' + os.environ['MCP_SESSION_ID'])
assert os.environ['MCP_CUSTOM'] == os.environ['MCP_API_URL'] + '/tools/custom'
assert os.environ['MCP_AUTH'] == 'Authorization: Bearer ' + os.environ['MCP_API_TOKEN']
reply = check('subprocess bridge', True)
assert reply.ok and reply.log_complete, reply.error
print('SUBPROCESS_BRIDGE_OK')
"""
child = subprocess.run([sys.executable, '-B', '-c', program], capture_output=True, text=True, timeout=15)
assert child.returncode == 0 and 'SUBPROCESS_BRIDGE_OK' in child.stdout, child.stderr
value = {'count': inputs.get('count'), 'input_names': sorted(inputs)}
if inputs.get('next'):
    next_id = inputs['next']
    follow = {'C':'D','D':'E','E':'F'}.get(next_id, '')
    reply = run_script(next_id, {'next':follow} if follow else {})
    assert isinstance(reply, ScriptResult) and isinstance(reply.output_dir, (Path, type(None)))
    assert all(isinstance(path, Path) for path in reply.outputs)
    value = json.loads(reply.outputs[0].read_text()) if reply.ok else {'error':reply.error}
Path(os.environ['STEP_OUTPUT_DIR'], 'result.json').write_text(json.dumps(value))
print('saved-script-finished')
`
		if id == "failing" {
			source = "raise RuntimeError('fixture child failure')\n"
		}
		if id == "timed-out" {
			seconds := 1
			definition.TimeoutSeconds = &seconds
			source = "import time\nprint('timeout-script-started', flush=True)\ntime.sleep(30)\nraise RuntimeError('timeout did not stop the script')\n"
		}
		write("code/"+id+"/main.py", source)
		raw, _ := json.Marshal(definition)
		write("code/"+id+"/script.json", string(raw))
		scripts[id] = CrewScriptConfiguration{Definition: definition, Root: root + "/code/" + id, Files: map[string][]byte{"main.py": []byte(source), "script.json": raw}}
	}
	// The caller is A. Its extra launch value must not leak into B's defaults.
	c.directCrewScript = &CrewScriptDefinition{ID: "A", Name: "A", Inputs: []Variable{{Name: "parent_only", Type: "string"}}}
	if err := c.upsertRunMetadata(t.Context(), c.selectedRunFolder, func(metadata map[string]interface{}) {
		metadata["source_snapshot_hash"] = "main-script-snapshot"
	}); err != nil {
		t.Fatal(err)
	}
	parent := &ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "A", Title: "A"}, Script: "A", crewScript: &CrewScriptConfiguration{Definition: *c.directCrewScript}}
	handlers := executor.NewExecutorHandlers("", loggerv2.NewNoop())
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 5 || parts[0] != "s" || r.Header.Get("Authorization") != "Bearer saved-script-fixture-token" {
			http.Error(w, "session required", 403)
			return
		}
		r.Header.Set("X-Session-ID", parts[1])
		handlers.HandlePerToolCustomRequest(w, r, parts[4])
	}))
	defer bridge.Close()
	t.Setenv("MCP_API_URL", bridge.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	defer common.ClearSessionShellConfig(c.GetMCPSessionID())
	dispatch := func(key, id string, inputs map[string]interface{}, resume, explicit, redo bool) pythonHelperReply {
		t.Helper()
		c.SetExecutionOptions(&ExecutionOptions{PythonResume: func() *PythonRunResume {
			if resume {
				return &PythonRunResume{}
			}
			return nil
		}(), ResumeFromStep: 1, CrewResources: &CrewResources{Scripts: scripts}})
		handler, finishSteps := c.pythonAutomationHandler(ctx, parent, filepath.Join(docs, root, "code/A"), root+"/runs/run-saved/execution/A")
		defer finishSteps(false)
		out, err := handler(ctx, map[string]interface{}{"action": "run_script", "key": key, "explicit_key": explicit, "order": float64(1), "redo": redo, "arguments": map[string]interface{}{"id": id, "inputs": inputs}})
		if err != nil {
			t.Fatal(err)
		}
		var reply pythonHelperReply
		if err := json.Unmarshal([]byte(out), &reply); err != nil {
			t.Fatal(err)
		}
		return reply
	}
	call := func(key, id string, inputs map[string]interface{}, resume bool, redo ...bool) pythonHelperReply {
		t.Helper()
		return dispatch(key, id, inputs, resume, true, len(redo) > 0 && redo[0])
	}
	read := func(reply pythonHelperReply) string {
		t.Helper()
		if !reply.OK || !reply.LogComplete {
			t.Fatalf("saved script receipt: %+v", reply)
		}
		value := reply.Value.(map[string]interface{})
		paths := value["outputs"].([]interface{})
		data, err := os.ReadFile(paths[0].(string))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(value["output_dir"].(string), filepath.Join(docs, root, "runs/run-saved/execution/helpers")) {
			t.Fatal("child left its caller's run")
		}
		if log, err := os.ReadFile(reply.LogFile); err != nil || !strings.Contains(string(log), "saved-script-finished") {
			t.Fatalf("child full log missing: %v", err)
		}
		return string(data)
	}
	first := call("saved-b", "B", map[string]interface{}{"count": 3}, false)
	if !strings.Contains(read(first), `"count": 3`) {
		t.Fatal("typed input not passed")
	}
	sharedInputs := map[string]interface{}{"count": 7, "parent_only": "unused by B"}
	filtered := read(call("shared-parent-inputs", "B", sharedInputs, false))
	if !strings.Contains(filtered, `"count": 7`) || strings.Contains(filtered, "parent_only") || sharedInputs["parent_only"] != "unused by B" {
		t.Fatalf("parent inputs leaked into the child or the caller map was changed: %s", filtered)
	}
	if reply := call("unknown-input", "B", map[string]interface{}{"count": 7, "parnet_only": "typo"}, false); reply.OK || !strings.Contains(reply.Error, "not declared") || reply.LogFile == "" {
		t.Fatalf("a name declared nowhere silently fell through to defaults: %+v", reply)
	}
	if !strings.Contains(read(call("default-b", "B", map[string]interface{}{}, false)), `"count": 3`) {
		t.Fatal("child defaults lost")
	}
	if metadata, err := c.ReadWorkspaceFile(t.Context(), "runs/"+c.selectedRunFolder+"/run_metadata.json"); err != nil || !strings.Contains(metadata, `"source_snapshot_hash": "main-script-snapshot"`) {
		t.Fatalf("child call replaced its main script's source hash: %s %v", metadata, err)
	}
	for _, invalid := range []struct {
		id     string
		inputs map[string]interface{}
	}{
		{"B", map[string]interface{}{"count": "bad", "parent_only": "ignored"}}, {"required", map[string]interface{}{}},
	} {
		reply := call("invalid-"+invalid.id, invalid.id, invalid.inputs, false)
		if reply.OK || reply.Error == "" || reply.LogFile == "" || reply.Value != nil || !strings.Contains(reply.Text, "did not start") {
			t.Fatalf("invalid inputs admitted: %+v", reply)
		}
		if invalid.id == "B" && !strings.Contains(reply.Error, "integer") {
			t.Fatalf("wrong type was refused at the wrong boundary: %+v", reply)
		}
	}
	if reply := call("started-failure", "failing", map[string]interface{}{}, false); reply.OK || !strings.Contains(reply.Error, "fixture child failure") || reply.Value == nil || !strings.Contains(reply.Text, "did not complete") {
		t.Fatalf("launched failure lost honest receipt: %+v", reply)
	}
	// PLAT-746-482: enforce the declared limit through the actual Python/shell
	// path, not only a captured timeout parameter or a simulated timeout result.
	timeoutStart := time.Now()
	if reply := call("whole-script-timeout", "timed-out", map[string]interface{}{}, false); reply.OK || !strings.Contains(reply.Error, "1-second limit") {
		t.Fatalf("saved script ignored its whole-process limit: %+v", reply)
	} else if log, err := os.ReadFile(reply.LogFile); err != nil || !strings.Contains(string(log), "timeout-script-started") || strings.Contains(string(log), "timeout did not stop") {
		t.Fatalf("timeout lost actual execution evidence: %s %v", log, err)
	}
	if time.Since(timeoutStart) > 15*time.Second {
		t.Fatal("declared one-second timeout did not bound the running Python process")
	}
	if reply := call("self", "A", map[string]interface{}{}, false); reply.OK || !strings.Contains(reply.Error, "A → A") {
		t.Fatalf("self recursion: %+v", reply)
	}
	for _, tc := range []struct{ key, next, expected string }{
		{"cycle", "A", "A → B → A"},
		{"depth", "C", "depth limit of 5"},
	} {
		data := read(call(tc.key, "B", map[string]interface{}{"next": tc.next}, false))
		var outcome struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &outcome); err != nil || !strings.Contains(outcome.Error, tc.expected) {
			t.Fatalf("recursive result %s: %v", data, err)
		}
	}

	// Continuation retains completed child output even when the parent's own
	// disposable output directory is cleared before its Python starts again.
	if err := os.RemoveAll(filepath.Join(docs, root, "runs/run-saved/execution/A")); err != nil {
		t.Fatal(err)
	}
	if cached := call("saved-b", "B", map[string]interface{}{"count": 3}, true); !cached.Reused || cached.LogFile != first.LogFile || read(cached) != read(first) {
		t.Fatal("resume lost completed child files")
	}
	changed := call("saved-b", "B", map[string]interface{}{"count": 4}, true)
	if changed.Reused || changed.LogFile == first.LogFile || !strings.Contains(read(changed), `"count": 4`) {
		t.Fatal("changed explicit-key arguments reused the old result")
	}
	implicit := dispatch("implicit-b", "B", map[string]interface{}{"count": 3}, false, false, false)
	read(implicit)
	if fresh := dispatch("implicit-b", "B", map[string]interface{}{"count": 3}, true, false, false); fresh.Reused || fresh.LogFile == implicit.LogFile || read(fresh) == "" {
		t.Fatal("unkeyed script work was reused during resume")
	}
	failed := call("failed-again", "failing", map[string]interface{}{}, false)
	if retried := call("failed-again", "failing", map[string]interface{}{}, true); failed.OK || retried.OK || retried.Reused || retried.LogFile == failed.LogFile || !strings.Contains(retried.Error, "fixture child failure") {
		t.Fatalf("failed script result was reused: %+v", retried)
	}
	unfinished := call("unfinished", "B", map[string]interface{}{}, false)
	read(unfinished)
	var stateFile string
	var state pythonHelperCall
	if err := filepath.WalkDir(filepath.Join(docs, root, "runs/run-saved/helpers"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var candidate pythonHelperCall
		if json.Unmarshal(data, &candidate) == nil && candidate.Reply.LogFile == unfinished.LogFile {
			stateFile, state = path, candidate
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if stateFile == "" || len(state.ScriptStack) != 2 || state.ScriptStack[0] != "A" || state.ScriptStack[1] != "B" {
		t.Fatal("server-owned call stack was not saved")
	}
	// An interrupted last receipt is not permission to silently repeat work.
	state.Complete = false
	pending, _ := json.Marshal(state)
	if err := os.WriteFile(stateFile, pending, 0600); err != nil {
		t.Fatal(err)
	}
	if reply := call("unfinished", "B", map[string]interface{}{}, true); reply.OK || !strings.Contains(reply.Error, "no completed receipt") {
		t.Fatalf("uncertain script replayed: %+v", reply)
	}
	if redone := call("unfinished", "B", map[string]interface{}{}, true, true); redone.LogFile == unfinished.LogFile || read(redone) == "" {
		t.Fatal("explicit redo did not retain a fresh attempt")
	}
	changed := scripts["B"]
	changed.Files["main.py"] = append(append([]byte{}, changed.Files["main.py"]...), []byte("\n# changed source\n")...)
	scripts["B"] = changed
	if reply := call("saved-b", "B", map[string]interface{}{"count": 3}, true); reply.OK || !strings.Contains(reply.Error, "Start a new run") {
		t.Fatalf("changed child reused old result: %+v", reply)
	}
}
