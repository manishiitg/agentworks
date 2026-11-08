package step_based_workflow

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
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

type helperCheckRecorder struct {
	mu        sync.Mutex
	failures  int
	passes    int
	maxDetail int
}

func (*helperCheckRecorder) OnExecutionStart(WorkshopExecutionStart) {}
func (*helperCheckRecorder) OnExecutionTerminated(string, string)    {}
func (r *helperCheckRecorder) OnExecutionComplete(_, _, detail string, _ map[string]string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(detail) > r.maxDetail {
		r.maxDetail = len(detail)
	}
	if err == nil {
		r.passes++
	} else {
		r.failures++
	}
}
func (r *helperCheckRecorder) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failures, r.passes
}

type helperNotificationRecorder struct {
	CrewScriptResolver
	mu    sync.Mutex
	texts []string
}

func (r *helperNotificationRecorder) NotifyScript(_ context.Context, _, _, _, text string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.texts = append(r.texts, text)
	return "fixture-authorized-chat", nil
}

// Real Python imports the staged package inside the existing sandbox, crosses
// HTTP and the real script-session registry, and reads back durable receipts.
// Repeating a key inside one execution must never skip a call in a loop.
func TestPythonHelpersRepeatCallsAndReuseOnlyResumedOccurrences(t *testing.T) {
	docs, files := workflowHTTPTestWorkspace(t)
	t.Setenv("AGENTWORKS_SLOTS", "off")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_BROWSER_STAGING_NAMESPACE", "python-helper-"+filepath.Base(docs))
	const root = "Workflow/python-helpers"
	code := filepath.Join(docs, root, "code/task")
	output := filepath.Join(docs, root, "runs/run-one/execution/task")
	for _, p := range []string{code, output} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	source := `import json, os
from pathlib import Path
from agentworks import check, notify, run_folder
if os.environ['VAR_PHASE'] == 'limits':
    big = 'large detail ' * 1000
    for i in range(3):
        c = check('bounded', True, big)
        n = notify(big)
        assert c.ok and n.ok
        if i == 1: assert 'helper logs only' in c.text and 'helper logs only' in n.text
        else: assert not c.text and not n.text
    for fn in (notify, lambda text: check('delivery', True, text)):
        assert not fn('fixture-refusal').ok
        assert not fn('fixture-offline').ok
        assert not fn('fixture-handler-error').ok
    raise SystemExit(0)
first = check("same", False, "needs attention", key="same", redo=os.environ['VAR_REDO'] == 'yes')
if os.environ['VAR_PHASE'] == 'interrupt':
    receipts = list((run_folder / 'helpers').rglob('*.json'))
    assert receipts, 'server receipt must already exist'
    original = receipts[0].read_text()
    try:
        receipts[0].write_text('{"complete":true,"reply":{"ok":true}}')
    except OSError:
        pass
    else:
        raise AssertionError('authored script forged a server-owned helper receipt')
    assert receipts[0].read_text() == original
    (run_folder / 'first.json').write_text(json.dumps({'log_file':str(first.log_file)}))
    raise SystemExit(17)
second = check("same", False, "needs attention", key="same")
passed = check("same", True, "fixed", key="same")
notice = notify("Progress saved", key="notice")
assert not first.ok and not second.ok and passed.ok
assert notice.ok and not notice.conversation_id and not notice.text
assert first.log_complete and second.log_complete and passed.log_complete
(run_folder / 'reply.json').write_text(json.dumps({'first':str(first.log_file),'second':str(second.log_file),'passed':str(passed.log_file)}))
`
	if err := os.WriteFile(filepath.Join(code, "main.py"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := NewStepBasedWorkflowOrchestrator(t.Context(), "", "", 0, "", nil, nil, false, "", &orchestrator.LLMConfig{}, 1, loggerv2.NewNoop(), nil, nil, nil, nil, nil, nil, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetWorkspacePath(root)
	c.selectedRunFolder = "run-one"
	c.WorkspaceClient = workspace.NewClient(files.URL)
	recorder := &helperCheckRecorder{}
	c.SetWorkshopExecutionNotifier(recorder)
	notifications := &helperNotificationRecorder{}
	executorHandlers := executor.NewExecutorHandlers("", loggerv2.NewNoop())
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 5 || parts[0] != "s" || r.Header.Get("Authorization") != "Bearer helper-test-token" {
			http.Error(w, "script session required", http.StatusForbidden)
			return
		}
		var args map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		arguments, _ := args["arguments"].(map[string]interface{})
		if arguments["text"] == "fixture-refusal" || arguments["detail"] == "fixture-refusal" {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "fixture delivery refused"})
			return
		}
		if arguments["text"] == "fixture-offline" || arguments["detail"] == "fixture-offline" {
			http.Error(w, "fixture delivery unavailable", http.StatusServiceUnavailable)
			return
		}
		// Exercise the production per-tool envelope, including canonical failure
		// for a check's ok:false payload. A homemade success envelope hid the
		// live loss of its log path and completion evidence (PLAT-746).
		body, _ := json.Marshal(args)
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.Header.Set("X-Session-ID", parts[1])
		tool := parts[4]
		if arguments["text"] == "fixture-handler-error" || arguments["detail"] == "fixture-handler-error" {
			// A missing session-scoped executor is an actual handler failure.
			tool = "unregistered_fixture_handler"
		}
		executorHandlers.HandlePerToolCustomRequest(w, r, tool)
	}))
	defer bridge.Close()
	t.Setenv("MCP_API_URL", bridge.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	step := &ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "task", Title: "Task"}}
	guard, err := c.resolveScriptedShellGuard(ctx, step, 0, "task", root+"/runs/run-one/execution/task", false)
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) map[string]string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(docs, root, "runs/run-one", name))
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]string
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	run := func(resume, redo bool, phase string) {
		t.Helper()
		c.SetExecutionOptions(&ExecutionOptions{AllowRunContinuation: resume, CrewResources: &CrewResources{Scripts: notifications}})
		if resume {
			c.executionOptions.ResumeFromStep = 1
		}
		env := map[string]string{"MCP_API_TOKEN": "helper-test-token", "VAR_PHASE": phase, "VAR_REDO": map[bool]string{true: "yes", false: "no"}[redo]}
		release := c.configureScriptBridgeSession(ctx, step, guard, DBAccessNone, env, code, root+"/runs/run-one/execution/task")
		defer release()
		if _, err := codeexec.CallCustomToolWithSession(ctx, "unrelated-script-step", "python_automation", map[string]interface{}{}); err == nil {
			t.Fatal("unregistered step borrowed the private Python helper")
		}
		iso := security.Isolator{BaseDir: docs, WorkDir: code, ReadPaths: guard.ReadPaths, WritePaths: guard.WritePaths, BlockedWritePaths: append(append([]string{}, guard.BlockedWritePaths...), code), ExtraEnv: env}
		command, cleanup, err := iso.ExecuteIsolated(ctx, "python3", []string{filepath.Join(code, "main.py")})
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		// The normal workspace shell adds approved per-call values after isolation.
		command.Env = security.MergeExtraEnv(command.Env, env)
		raw, err := command.CombinedOutput()
		if phase == "interrupt" {
			if err == nil || !strings.Contains(err.Error(), "17") {
				t.Fatalf("expected interruption after a completed call: %s %v", raw, err)
			}
		} else if err != nil {
			t.Fatalf("helper script: %s %v", raw, err)
		}
	}
	run(false, false, "interrupt")
	first := read("first.json")["log_file"]
	run(true, false, "complete")
	resumed := read("reply.json")
	failures, passes := recorder.counts()
	if resumed["first"] != first || resumed["second"] == first || failures != 2 || passes != 1 {
		t.Fatalf("resume skipped/repeated wrong calls: %v failures=%d passes=%d", resumed, failures, passes)
	}
	run(false, false, "complete")
	fresh := read("reply.json")
	failures, passes = recorder.counts()
	if fresh["first"] == first || fresh["first"] == fresh["second"] || failures != 4 || passes != 2 {
		t.Fatalf("ordinary calls were cached: %v", fresh)
	}
	run(true, true, "complete")
	redone := read("reply.json")
	failures, passes = recorder.counts()
	if redone["first"] == fresh["first"] || redone["second"] != fresh["second"] || failures != 5 || passes != 2 {
		t.Fatalf("redo lost its attempt or repeated other calls: %v", redone)
	}
	for _, path := range redone {
		if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "check finished") {
			t.Fatalf("helper full log missing: %s %v", path, err)
		}
	}
	// Seed an almost-full run rather than launching a thousand redundant calls.
	// A second adapter must share these counters and retain complete source text
	// in logs while keeping chat/inspection payloads small.
	if err := os.WriteFile(filepath.Join(docs, root, "runs/run-one/helpers/presentation.json"), []byte(`{"notifications":99,"checks":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	beforeNotifications := len(notifications.texts)
	beforeFailures, beforePasses := recorder.counts()
	run(false, false, "limits")
	failures, passes = recorder.counts()
	if failures != beforeFailures || passes != beforePasses+1 || recorder.maxDetail != 4096 || len(notifications.texts) != beforeNotifications+1 || len(notifications.texts[len(notifications.texts)-1]) != 4096 || !strings.Contains(notifications.texts[len(notifications.texts)-1], "truncated") {
		t.Fatalf("presentation limits failed: failures=%d passes=%d detail=%d notifies=%d", failures, passes, recorder.maxDetail, len(notifications.texts))
	}
	var full bool
	filepath.WalkDir(filepath.Join(docs, root, "runs/run-one/logs/helpers"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			data, _ := os.ReadFile(path)
			full = full || strings.Contains(string(data), strings.Repeat("large detail ", 1000))
		}
		return err
	})
	if !full {
		t.Fatal("bounded presentation lost its original full log")
	}
	t.Log(fmt.Sprintf("real_python=true resumed_first=true repeated_key_runs=true failed_checks_retained=%d", failures))
	common.ClearSessionShellConfig(c.GetMCPSessionID())
}
