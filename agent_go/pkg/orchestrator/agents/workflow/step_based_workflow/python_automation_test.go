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
	"sync/atomic"
	"testing"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
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

type blockedStepRecorder struct {
	helperCheckRecorder
	started chan struct{}
	proceed chan struct{}
}

func (r *blockedStepRecorder) OnExecutionStart(WorkshopExecutionStart) { close(r.started); <-r.proceed }

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
// Checks/notices remain ordinary calls, including during a resumed script.
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
from agentworks import check, notify, run_folder, step, call_function, function_status, FunctionResult
if os.environ['VAR_PHASE'] == 'apps':
    from agentworks import google, slack, github, AutomationError, send_email
    assert google.sheets.read('sheet', 'A1:B2') == ['fixture', {'value': 3}]
    assert google.sheets.write('sheet', 'A1:B2', [['company', 3]])['args'][1] == 'update'
    assert google.sheets.append('sheet', 'A1:B2', [['company', 3]])['args'][1] == 'append'
    assert google.gmail.search('newer_than:1d', max=4)['args'][3:5] == ['--max', '4']
    assert google.calendar.list('2026-01-01', '2026-01-02')['args'][1] == 'events'
    created = google.calendar.create('Review', '2026-01-01', '2026-01-02', attendees=('member@example.com',))
    assert created['args'][-4:] == ['--attendees', 'member@example.com', '--json', '--results-only']
    document = google.drive.read('document')
    assert document.parent == run_folder and document.read_text() == 'Document text'
    assert google.sheets.read('empty', 'A1') is None
    assert google.sheets.read('large', 'A1') == 'λ' * 6000
    for sheet, reason in [('huge', 'smaller'), ('invalid', 'unreadable'), ('denied', 'fixture app refused')]:
        try: google.sheets.read(sheet, 'A1')
        except AutomationError as error: assert reason in str(error), str(error)
        else: raise AssertionError('unusable app result was accepted')
    assert github.files('example/project')['action'] == 'files'
    assert github.read('example/project', 'README.md') == 'Repository text'
    assert github.issues('example/project')['state'] == 'open'
    assert github.create_issue('example/project', 'Review')['action'] == 'issue_create'
    assert github.comment('example/project', 2, 'Done')['number'] == 2
    assert github.prs('example/project', state='closed')['state'] == 'closed'
    assert github.create_pr('example/project', 'topic', 'main', 'Change')['action'] == 'pr_create'
    assert github.diff('example/project', 2) == 'diff text'
    assert slack.read('route', since_ts='1.000001', limit=3)['parameters'] == {'oldest':'1.000001','limit':3}
    assert slack.send('route', 'Progress')['message'] == 'Progress'
    assert slack.reply('route', '1.000001', 'Reply')['thread_ts'] == '1.000001'
    plain = send_email('member@example.com', 'Plain report', 'Report')
    assert plain.ok and json.loads(plain.text) == {'to':['member@example.com'], 'subject':'Plain report', 'body':'Report', 'attachments':[]}
    rich = send_email('member@example.com', 'Rich report', '', cc=('copy@example.com',), html='<p>Report</p>')
    assert rich.ok and json.loads(rich.text) == {'to':['member@example.com'], 'subject':'Rich report', 'body':'', 'attachments':[], 'cc':['copy@example.com'], 'html':'<p>Report</p>'}

    raise SystemExit(0)
if os.environ['VAR_PHASE'] == 'unavailable-app':
    from agentworks import google, AutomationError
    try: google.sheets.read('sheet', 'A1')
    except AutomationError as error: assert 'unavailable' in str(error), str(error)
    else: raise AssertionError('unavailable app borrowed an executor outside its tool pool')
    raise SystemExit(0)
if os.environ['VAR_PHASE'] == 'missing-finish':
    @step('Missing finish receipt')
    def done(): return 42
    assert done() == 42
    raise SystemExit(0)
if os.environ['VAR_PHASE'] == 'slow-step':
    @step('Delayed receipt')
    def normal():
        return 42
    assert normal() == 42
    raise SystemExit(0)
if os.environ['VAR_PHASE'] == 'evidence':
    @step('Evidence')
    def evidence():
        import time
        time.sleep(0.03)
        assert check('Evidence inside', True, 'MCP_API_TOKEN=fixture-credential ' + 'λ' * 700).ok
        assert not check('Evidence expected failure', False, 'expected').ok
        notify('MCP_API_TOKEN=fixture-credential progress')
        @step('Nested evidence')
        def nested(): check('Nested check', True)
        nested()
        check('Restored outer check', True)
    evidence()
    def shared_check(label): check('Shared ' + label, True)
    for label in ['first', 'second']:
        step('Shared ' + label)(shared_check)(label)
    check('Evidence outside', True)
    from agentworks import _call
    assert _call('check', {'name':'Unknown step check', 'ok':True, 'step_execution_id':'python-unknown', 'step_name':'Forged name'}, None, False).ok
    raise SystemExit(0)
if os.environ['VAR_PHASE'] == 'functions':
    first = call_function('Beta', 'check_build', {'build': '7'})
    second = call_function('Beta', 'check_build', {'build': '7'})
    assert isinstance(first, FunctionResult) and first.ok and first.status == 'running'
    assert second.ok and second.call_id != first.call_id
    status = function_status(first.call_id)
    assert status.ok and status.status == 'completed' and status.value == {'build': '7'}
    assert status.log_file and status.log_file.is_file()
    raise SystemExit(0)
if os.environ['VAR_PHASE'] == 'labels':
    @step('Ordinary code')
    def increment(value):
        if value < 0: raise ValueError('original failure')
        return value + 1
    for i in range(3): assert increment(i) == i + 1
    try: increment(-1)
    except ValueError as error: assert str(error) == 'original failure'
    else: raise AssertionError('decorator swallowed the function error')
    raise SystemExit(0)
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
	c.WorkspaceTools = virtualtools.CreateHumanTools()
	appCalls := 0
	c.WorkspaceToolExecutors = map[string]interface{}{}
	for _, name := range []string{"google_workspace_cli", "github", "slack", "send_slack_message", "send_email"} {
		c.WorkspaceToolExecutors[name] = func(ctx context.Context, args map[string]interface{}) (string, error) {
			appCalls++
			// PLAT-746-466: the HTTP caller cannot choose a different app scope.
			if name != "send_slack_message" && name != "send_email" { // existing standalone sender paths are unchanged
				session, _ := ctx.Value(common.ChatSessionIDKey).(string)
				owningRoot, err := common.GoogleWorkspaceFromContext(ctx)
				if err != nil || owningRoot != root || !strings.HasPrefix(session, "script-step-task-") {
					return "", fmt.Errorf("app lost server-bound owning scope: %q %v", owningRoot, err)
				}
			}
			if name == "google_workspace_cli" {
				var command []string
				raw, _ := json.Marshal(args["args"])
				if err := json.Unmarshal(raw, &command); err != nil || len(command) < 3 {
					return "", fmt.Errorf("invalid Google command")
				}
				switch command[2] {
				case "denied":
					return "", fmt.Errorf("fixture app refused")
				case "huge":
					return "{}\n...[truncated]", nil
				case "invalid":
					return "not JSON", nil
				case "empty":
					return "(no output)", nil
				case "large":
					raw, _ := json.Marshal(strings.Repeat("λ", 6000))
					return string(raw), nil
				case "document":
					return "Document text", nil
				}
				if command[1] == "get" {
					return `["fixture",{"value":3}]`, nil
				}
			}
			if name == "github" && args["action"] == "read" {
				return "Repository text", nil
			}
			if name == "github" && args["action"] == "pr_diff" {
				return "diff text", nil
			}
			if name == "send_slack_message" && args["idempotency_key"] == nil {
				return "", fmt.Errorf("missing existing tracked-send key")
			}
			raw, err := json.Marshal(args)
			return string(raw), err
		}
	}
	recorder := &helperCheckRecorder{}
	c.SetWorkshopExecutionNotifier(recorder)
	notifications := &helperNotificationRecorder{}
	executorHandlers := executor.NewExecutorHandlers("", loggerv2.NewNoop())
	var dropFinish atomic.Bool
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
		if args["action"] == "step" && arguments["phase"] == "start" && arguments["title"] == "Missing finish receipt" {
			dropFinish.Store(true)
		}
		if args["action"] == "step" && arguments["phase"] == "finish" && dropFinish.Swap(false) {
			http.Error(w, "finish delivery unavailable", http.StatusServiceUnavailable)
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
		if args["action"] == "step" && arguments["phase"] == "start" && arguments["title"] == "Delayed receipt" {
			// PLAT-746: the step has been durably started, but a slow response
			// exceeds the old SDK-only 3s timer. The Python work still succeeds.
			reply := httptest.NewRecorder()
			executorHandlers.HandlePerToolCustomRequest(reply, r, tool)
			time.Sleep(3500 * time.Millisecond)
			for key, values := range reply.Header() {
				w.Header()[key] = values
			}
			w.WriteHeader(reply.Code)
			_, _ = w.Write(reply.Body.Bytes())
			return
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
	functionCalls, statusReads := 0, 0
	factory := func(_ context.Context, scope AgentFunctionScope) ([]llmtypes.Tool, map[string]interface{}, error) {
		if scope.ExecutionID != "run-one" || scope.StepID != step.GetID() {
			t.Fatal("untrusted function scope", scope)
		}
		return nil, map[string]interface{}{
			"project_functions_call": func(_ context.Context, args map[string]interface{}) (string, error) {
				functionCalls++
				return fmt.Sprintf(`{"call_id":%q,"status":"running"}`, args["submission_id"]), nil
			},
			"project_functions_get_call": func(_ context.Context, args map[string]interface{}) (string, error) {
				statusReads++
				return fmt.Sprintf(`{"call_id":%q,"status":"completed","result":{"build":"7"}}`, args["call_id"]), nil
			},
		}, nil
	}
	run := func(resume, redo bool, phase string) {
		t.Helper()
		ctx, endInvocation := context.WithCancel(ctx)
		defer endInvocation()
		c.SetExecutionOptions(&ExecutionOptions{AgentFunctionTools: factory, CrewResources: &CrewResources{Scripts: notifications}})
		if resume {
			c.executionOptions.PythonResume = &PythonRunResume{}
		}
		env := map[string]string{"MCP_API_TOKEN": "helper-test-token", "VAR_PHASE": phase, "VAR_REDO": map[bool]string{true: "yes", false: "no"}[redo]}
		processSucceeded := false
		release := c.configureScriptBridgeSession(ctx, step, guard, DBAccessNone, env, code, root+"/runs/run-one/execution/task")
		defer func() { release(processSucceeded) }()
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
		processSucceeded = err == nil
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
	if resumed["first"] == first || resumed["second"] == first || failures != 3 || passes != 1 {
		t.Fatalf("resume skipped/repeated wrong calls: %v failures=%d passes=%d", resumed, failures, passes)
	}
	run(false, false, "complete")
	fresh := read("reply.json")
	failures, passes = recorder.counts()
	if fresh["first"] == first || fresh["first"] == fresh["second"] || failures != 5 || passes != 2 {
		t.Fatalf("ordinary calls were cached: %v", fresh)
	}
	run(true, true, "complete")
	redone := read("reply.json")
	failures, passes = recorder.counts()
	if redone["first"] == fresh["first"] || redone["second"] == fresh["second"] || failures != 7 || passes != 3 {
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
	beforeFailures, beforePasses = recorder.counts()
	run(false, false, "labels")
	failures, passes = recorder.counts()
	if failures != beforeFailures+1 || passes != beforePasses+3 {
		t.Fatalf("ordinary decorated calls were skipped or changed outcome: failures=%d passes=%d", failures, passes)
	}
	run(false, false, "functions")
	run(true, false, "functions")
	if functionCalls != 4 || statusReads != 2 {
		t.Fatalf("ordinary function work or status read incorrectly reused: calls=%d reads=%d", functionCalls, statusReads)
	}
	run(false, false, "apps")
	beforeAppCalls := appCalls
	// An executor present in a different/global registry is not admission.
	// Keep its function in the map but remove the owning tool definitions.
	c.WorkspaceTools = nil
	run(false, false, "unavailable-app")
	if appCalls != beforeAppCalls {
		t.Fatal("unavailable app executed despite the owning pool's refusal")
	}
	if err := os.Remove(filepath.Join(docs, root, "runs/run-one/helpers/presentation.json")); err != nil {
		t.Fatal(err)
	}
	beforeEvidenceNotices := len(notifications.texts)
	// Crew-chat script starts may have no live execution notifier.
	c.SetWorkshopExecutionNotifier(nil)
	run(false, false, "evidence")
	if len(notifications.texts) != beforeEvidenceNotices+1 {
		t.Fatal("evidence notification did not reach the real chat target adapter")
	}

	// A valid ID from another invocation still proves no relationship to this
	// handler. Keep the check, without accepting its supplied step name.
	ownerHandler, finishOwner := c.pythonAutomationHandler(ctx, step, code, root+"/runs/run-one/execution/task")
	otherHandler, finishOther := c.pythonAutomationHandler(ctx, step, code, root+"/runs/run-one/execution/task")
	startRaw, err := ownerHandler(ctx, map[string]interface{}{"action": "step", "key": "foreign-start", "order": float64(1), "arguments": map[string]interface{}{"phase": "start", "title": "Other invocation", "source_call": "main.py:shared@main.py:1"}})
	var foreignStart pythonHelperReply
	if err != nil || json.Unmarshal([]byte(startRaw), &foreignStart) != nil || !foreignStart.OK {
		t.Fatalf("other invocation setup failed: %s %v", startRaw, err)
	}
	checkRaw, err := otherHandler(ctx, map[string]interface{}{"action": "check", "key": "foreign-check", "order": float64(1), "arguments": map[string]interface{}{"name": "Other invocation check", "ok": true, "step_execution_id": foreignStart.ConversationID, "step_name": "Forged name"}})
	var foreignCheck pythonHelperReply
	if err != nil || json.Unmarshal([]byte(checkRaw), &foreignCheck) != nil || !foreignCheck.OK {
		t.Fatalf("unassigned check did not run: %s %v", checkRaw, err)
	}
	finishOwner(true)
	finishOther(true)
	run(false, false, "slow-step")
	run(false, false, "missing-finish")
	// A successful script must not acquire a failed/running step simply
	// because the already-accepted start response took more than three seconds.
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(docs, root, "runs/run-one/run_metadata.json"))
		var record struct {
			Steps map[string]PythonStepEvidence `json:"python_steps"`
		}
		_ = json.Unmarshal(data, &record)
		settled := false
		for _, item := range record.Steps {
			if item.Name == "Delayed receipt" {
				if item.Status == "completed" {
					settled = true
				} else if item.Status == "failed" || time.Now().After(deadline) {
					t.Fatalf("successful Python acquired a false failed/incomplete step after delayed start receipt: %+v", item)
				}
			}
		}
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("successful delayed step has no completed record")
		}
		time.Sleep(10 * time.Millisecond)
	}

	metadata, err := os.ReadFile(filepath.Join(docs, root, "runs/run-one/run_metadata.json"))
	var recorded struct {
		Steps    map[string]PythonStepEvidence `json:"python_steps"`
		Evidence []PythonRunEvidence           `json:"python_evidence"`
	}
	if err != nil || json.Unmarshal(metadata, &recorded) != nil || len(recorded.Evidence) == 0 || len(recorded.Evidence) > 200 {
		t.Fatalf("bounded helper results absent from run record: %s %v", metadata, err)
	}
	foundCompleted, foundFailure, foundWithoutNotifier, foundMissingFinish := false, false, false, false
	for _, item := range recorded.Steps {
		if item.StartedAt == "" || item.CompletedAt == "" || item.DurationMs < 0 || item.SourceCall == "" || item.LogNote == "" {
			t.Fatalf("step record lacks timing/source/log explanation: %+v", item)
		}
		if item.Name == "Missing finish receipt" {
			if item.Status != "completed" || item.Detail != "finish receipt not received" {
				t.Fatalf("exit-0 missing finish receipt recorded dishonestly: %+v", item)
			}
			foundMissingFinish = true
		}
		foundCompleted = foundCompleted || item.Status == "completed"
		foundFailure = foundFailure || item.Status == "failed"
		if item.Name == "Evidence" && item.Status == "completed" {
			// A check is instantaneous; the real decorator must time the function.
			if item.DurationMs < 30 {
				t.Fatalf("decorated work lost elapsed time: %+v", item)
			}
			foundWithoutNotifier = true
		}
	}
	if !foundMissingFinish {
		t.Fatal("successful missing-finish step was not recorded")
	}
	if !foundCompleted || !foundFailure || !foundWithoutNotifier {
		t.Fatalf("notifier-independent step evidence missing: completed=%t failed=%t no_notifier=%t", foundCompleted, foundFailure, foundWithoutNotifier)
	}
	items, err := ReadPythonRunEvidence(docs, root, "run-one")
	if err != nil {
		t.Fatal(err)
	}
	foundInside, foundFailed, foundOutside, foundNotify := false, false, false, false
	for _, item := range items {
		encoded, _ := json.Marshal(item)
		if strings.Contains(string(encoded), "fixture-credential") || len([]rune(item.Detail)) > 500 {
			t.Fatalf("run evidence missed existing credential redaction or bounded detail: %s", encoded)
		}
		if item.StepExecutionID != "" {
			step, found := recorded.Steps[item.StepExecutionID]
			if !found || step.Name != item.StepName {
				t.Fatalf("check did not identify the actual recorded step: %+v", item)
			}
		} else if item.StepName != "" {
			t.Fatalf("unassigned check accepted an authored step name: %+v", item)
		}
		switch item.Name {
		case "Evidence inside":
			foundInside = item.OK != nil && *item.OK && strings.HasPrefix(item.SourceCall, "main.py:evidence@") && item.StepName == "Evidence"
		case "Evidence expected failure":
			foundFailed = item.OK != nil && !*item.OK && item.SourceCall != ""
		case "Evidence outside":
			foundOutside = item.SourceCall == "" && item.StepExecutionID == "" && item.StepName == ""
		}
		if item.Kind == "notify" && strings.Contains(item.Detail, "progress") {
			foundNotify = item.OK == nil && item.SourceCall != "" && item.StepName == "Evidence"
		}
	}
	if !foundInside || !foundFailed || !foundOutside || !foundNotify {
		t.Fatalf("real Python run evidence missing: inside=%t failed=%t outside=%t notify=%t", foundInside, foundFailed, foundOutside, foundNotify)
	}
	byName := map[string]PythonRunEvidence{}
	for _, item := range items {
		byName[item.Name] = item
	}
	firstEvidence, secondEvidence := byName["Shared first"], byName["Shared second"]
	if firstEvidence.StepExecutionID == "" || secondEvidence.StepExecutionID == "" || firstEvidence.StepExecutionID == secondEvidence.StepExecutionID || firstEvidence.SourceCall != secondEvidence.SourceCall || firstEvidence.StepName != "Shared first" || secondEvidence.StepName != "Shared second" {
		t.Fatalf("shared Python helper attribution is ambiguous: first=%+v second=%+v", firstEvidence, secondEvidence)
	}
	if byName["Nested check"].StepName != "Nested evidence" || byName["Restored outer check"].StepName != "Evidence" || byName["Restored outer check"].StepExecutionID != byName["Evidence inside"].StepExecutionID {
		t.Fatal("nested step context did not restore its outer execution")
	}
	for _, name := range []string{"Unknown step check", "Other invocation check"} {
		if unassigned, found := byName[name]; !found || unassigned.StepExecutionID != "" || unassigned.StepName != "" {
			t.Fatalf("unproven execution ID gained step attribution: %+v", unassigned)
		}
	}
	t.Logf("shared helper source=%s attributed distinct executions=%s,%s; nested/outer/outside/unknown cases passed", firstEvidence.SourceCall, firstEvidence.StepExecutionID, secondEvidence.StepExecutionID)
	// PR1642: Stop may finalize the invocation while OnExecutionStart is
	// blocked after the initial durable record but before activity registration.
	for _, succeeded := range []bool{false, true} {
		blocked := &blockedStepRecorder{started: make(chan struct{}), proceed: make(chan struct{})}
		c.SetWorkshopExecutionNotifier(blocked)
		env := map[string]string{"MCP_API_TOKEN": "helper-test-token"}
		closeBridge := c.configureScriptBridgeSession(ctx, step, guard, DBAccessNone, env, code, root+"/runs/run-one/execution/task")
		title := fmt.Sprintf("Registered after invocation end %t", succeeded)
		body, _ := json.Marshal(map[string]interface{}{"action": "step", "key": title, "order": 1, "arguments": map[string]interface{}{"phase": "start", "title": title, "source_call": "main.py:late@main.py:1"}})
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, env["MCP_API_URL"]+"/tools/custom/python_automation", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer helper-test-token")
		done := make(chan error, 1)
		go func() {
			response, err := http.DefaultClient.Do(request)
			if err == nil {
				_, _ = io.Copy(io.Discard, response.Body)
				response.Body.Close()
			}
			done <- err
		}()
		select {
		case <-blocked.started:
		case <-ctx.Done():
			close(blocked.proceed)
			closeBridge(false)
			t.Fatal(ctx.Err())
		}
		closeBridge(succeeded)
		close(blocked.proceed)
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		data, err := os.ReadFile(filepath.Join(docs, root, "runs/run-one/run_metadata.json"))
		var record struct {
			Steps map[string]PythonStepEvidence `json:"python_steps"`
		}
		if err != nil || json.Unmarshal(data, &record) != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range record.Steps {
			if item.Name == title {
				found = true
				want := "failed"
				if succeeded {
					want = "completed"
				}
				if item.Status != want || item.CompletedAt == "" {
					t.Fatalf("late step remained running after invocation ended: %+v", item)
				}
			}
		}
		if !found {
			t.Fatal("late step record missing")
		}
	}
	t.Log(fmt.Sprintf("real_python=true checks_reexecuted_on_resume=true repeated_labels_run=true failed_checks_retained=%d", failures))
	common.ClearSessionShellConfig(c.GetMCPSessionID())
}
