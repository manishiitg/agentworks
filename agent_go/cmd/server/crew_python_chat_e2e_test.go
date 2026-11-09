package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/clisecurity"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746: exercise the Crew chat's actual lazy registration/bound executor,
// real Python and background registry, then its authenticated HTTP Stop. This
// does not claim a model interpreted a message or a deployed AI was exercised.
func TestCrewChatSavedPythonStartAndHTTPStop(t *testing.T) {
	checkCrewChatPythonStop(t, "bound")
}

// PLAT-746-506: a trigger in a Crew must be real background work
// of the calling chat. A shared Run person's published call has the same Stop.
func TestCrewChatRunTriggerAndFunctionStop(t *testing.T) {
	for _, entry := range []string{"trigger", "shared-function"} {
		t.Run(entry, func(t *testing.T) { checkCrewChatPythonStop(t, entry) })
	}
}

func checkCrewChatPythonStop(t *testing.T, entry string) {
	if runtime.GOOS == "linux" {
		root, err := os.MkdirTemp(".", ".crew-python-chat-")
		if err != nil {
			t.Fatal(err)
		}
		root, err = filepath.Abs(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
		t.Setenv("TMPDIR", root)
	}
	tokenTestSetup(t)
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_BROWSER_STAGING_NAMESPACE", t.Name()+f.docs)
	managedBrowserRoot := browserconfig.ManagedSocketRoot()
	t.Cleanup(func() { _ = os.RemoveAll(managedBrowserRoot) })
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"other","username":"other","can_create":false,"products":["work"]}]}`)
	t.Setenv("AGENTWORKS_SLOTS", "off")
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	common.SetBridgeTokenSecret("crew-python-chat-fixture")
	t.Cleanup(func() { common.SetBridgeTokenSecret("") })
	executors := executor.NewExecutorHandlers("", loggerv2.NewNoop())
	bridgeRoutes := mux.NewRouter()
	bridgeRoutes.Use(bridgeAuthMiddleware(""))
	bridgeRoutes.HandleFunc("/s/{session_id}/tools/custom/{tool}", func(w http.ResponseWriter, r *http.Request) {
		executors.HandlePerToolCustomRequest(w, r, mux.Vars(r)["tool"])
	})
	bridge := httptest.NewServer(bridgeRoutes)
	defer bridge.Close()
	t.Setenv("MCP_API_URL", bridge.URL)
	const sessionID = "crew-python-chat"
	actor := "owner"
	if entry == "shared-function" {
		actor = "other"
	}
	api := f.api
	api.projectActions = NewProjectActionDispatcher(api, api.agentProfiles)
	var err error
	api.chatStore, err = chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api.logger = loggerv2.NewNoop()
	api.config = ServerConfig{Provider: "openai", ModelID: "unused-script-fixture", MaxTurns: 1}
	api.provider, api.model = "openai", "unused-script-fixture"
	api.terminalStore = terminals.NewStore()
	api.runtimeCoordinator = NewRuntimeCoordinator()
	api.scheduler = NewSchedulerService(api)
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "scheduler.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	api.scheduler.stateStore = store
	t.Cleanup(func() { store.Close() })
	api.cliSecurityStore, err = clisecurity.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api.mcpConfigPath = filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(api.mcpConfigPath, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	api.agentCancelFuncs = map[string]context.CancelFunc{}
	api.workflowOrchestratorContexts = map[string]context.CancelFunc{}
	api.activeWorkflowExecutions = map[string]*ActiveWorkflowExecution{}
	api.sessionQueryIDs = map[string][]string{}
	api.workflowObjectives = map[string]string{}
	api.workflowStepIDs = map[string]string{}
	api.lastQueryRequests = map[string]QueryRequest{}
	api.conversationHistory = map[string][]llmtypes.MessageContent{}
	api.restoredConversationPersistTargets = map[string]restoredChatHistoryPersistTarget{}
	api.workflowOrchestrators = map[string]orchestrator.Orchestrator{}
	api.bgAgentRegistry = NewBackgroundAgentRegistry()
	api.eventStore = events.NewEventStore(100)
	t.Cleanup(api.eventStore.Stop)
	api.eventStore.SetSessionOwner(sessionID, actor)
	api.activeSessions = map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, UserID: actor, WorkspacePath: externalCommandCrewRoot}}
	// No model-turn dispatcher runs in this fixture. The production notifier
	// still registers actual work and publishes its state and events.
	api.completionLoopStarted = map[string]bool{sessionID: true}
	api.pendingCompletions = map[string][]string{}
	api.pendingStartNotifications = map[string][]string{}
	api.stoppedSessions = map[string]bool{}
	api.startNotificationRetryScheduled = map[string]bool{}
	api.completionRetryScheduled = map[string]bool{}
	api.sessionBusy = map[string]bool{}
	api.sessionBusySince = map[string]time.Time{}
	api.trackedWorkflowExecutions = map[string]*TrackedWorkflowExecution{}
	t.Cleanup(func() {
		api.markSessionStopped(sessionID)
		api.cancelBackgroundAgents(sessionID)
		api.workshopChatSessions.Range(func(_, value any) bool {
			if closer, ok := value.(interface{ Close() }); ok {
				closer.Close()
			}
			return true
		})
	})
	ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"}), 35*time.Second)
	defer cancel()
	stopRun := func(id string) {
		api.scheduler.cancelScheduleRunContext(id)
		if run, err := api.scheduler.existingWebhookRun(context.Background(), id); err == nil && run.ActiveSessionID != "" {
			api.scheduler.cancelScheduledSessionWork(run.ActiveSessionID, "fixture cleanup", runtimePhaseCanceled)
		}
	}
	binding := crewExecutableBinding{ID: "chat-saved", CrewID: "team", CrewRoot: externalCommandCrewRoot, Root: "Workflow/chat-saved", Kind: "script", RuntimeRoot: "workflows/chat-saved"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	if err := initializeCrewScriptSource(ctx, binding, "Chat saved Python"); err != nil {
		t.Fatal(err)
	}
	seedArchivedPlanWithMissingDependency(t, f.docs, binding.Root)
	if err := saveCrewScriptInputs(ctx, binding.Root, &workflow.VariablesManifest{
		Variables: []workflow.Variable{{Name: "count", Type: "integer", Value: "2"}, {Name: "enabled", Type: "boolean", Value: "false"}},
		Groups:    []workflow.VariableGroup{{Name: "Saved", Values: map[string]string{"count": "7"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeFileToWorkspace(ctx, binding.Root+"/code/main.py", `import json, os, subprocess, sys
from agentworks import inputs, run_folder
assert inputs['count'] == 7 and inputs['enabled'] is True, inputs
child_source = "import signal,sys,time\nfrom pathlib import Path\nsignal.signal(signal.SIGTERM, signal.SIG_IGN)\nfor i in range(2400):\n Path(sys.argv[1]).write_text(str(time.monotonic_ns()))\n time.sleep(0.05)\n"
child = subprocess.Popen([sys.executable, '-u', '-c', child_source, str(run_folder / 'heartbeat.txt')])
(run_folder / 'worker-pids.json').write_text(json.dumps({'parent': os.getpid(), 'child': child.pid, 'group': os.getpgid(child.pid)}))
child.wait()
`); err != nil {
		t.Fatal(err)
	}
	var runID string
	if entry == "bound" {
		resolved := &resolvedAgentProfile{}
		resolved.Definition.ID = crewProfileID
		resolved.Definition.Version = 1
		resolved.Definition.Runtime.Capabilities.WorkflowExecution = agentprofiles.CapabilityOptional
		req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: crewProfileID, AgentProfileConversationKey: "team", SelectedFolder: externalCommandCrewRoot}
		draft := &productSurfaceDraft{}
		if err := api.registerAgentProfileWorkflowTools(ctx, draft, resolved, "owner", sessionID, req.SelectedFolder, nil, nil, req, false); err != nil {
			t.Fatal(err)
		}
		start, ok := draft.tools["run_full_workflow"]
		if !ok {
			t.Fatal("current saved script was not offered to its Crew chat")
		}
		api.workshopChatSessions.Range(func(_, _ any) bool { t.Error("listing started a runtime"); return false })
		bound, err := api.bindToolExecutionContext(ctx, sessionID, req, false)(ctx, "run_full_workflow")
		if err != nil {
			t.Fatal(err)
		}
		if out, err := start.exec(bound, map[string]any{"extra": "fixture-value-not-for-errors"}); err == nil || !strings.Contains(err.Error(), "extra") || !strings.Contains(err.Error(), "inputs") || strings.Contains(err.Error(), "fixture-value-not-for-errors") || strings.Contains(out, "execution_id") {
			t.Fatalf("unknown start argument was accepted or not explained plainly: %s %v", out, err)
		}
		// PLAT-746-500: the Crew tool used a string-only parser despite the
		// saved script's typed contract. Reject a wrong type before work starts.
		if out, err := start.exec(bound, map[string]any{"input_preset": "Saved", "inputs": map[string]any{"count": false}}); err == nil || !strings.Contains(err.Error(), "count") || strings.Contains(out, "execution_id") {
			t.Fatalf("wrong typed input was admitted or the declared input was not named: %s %v", out, err)
		}
		if len(api.bgAgentRegistry.GetAll(sessionID)) != 0 {
			t.Fatal("wrong typed input started background work")
		}
		if out, err := start.exec(bound, map[string]any{"input_preset": "Saved", "inputs": map[string]any{"enabled": true}}); err != nil || !strings.Contains(out, "execution_id") {
			t.Fatalf("Crew chat start with saved preset and typed override: %s %v", out, err)
		}
	} else {
		reg := &recordingRegistrar{}
		args := map[string]any{"action": "run_trigger", "workspace_path": binding.Root, "input_preset": "Saved", "inputs": map[string]any{"enabled": true}}
		if entry == "shared-function" {
			manifest, found, err := ReadWorkflowManifest(ctx, binding.Root)
			if err != nil || !found {
				t.Fatal(err)
			}
			manifest.Schedules = []WorkflowSchedule{{ID: "fn-script", Name: "Run script", ScheduleType: "webhook", Enabled: true, Kind: triggerKindFunction, WorkshopMode: "run", Webhook: &WorkflowWebhookConfig{}, InputPreset: "Saved", Function: &WorkflowFunctionSpec{Name: "run_script", Inputs: []string{"enabled"}}}}
			if err := WriteWorkflowManifest(ctx, binding.Root, manifest); err != nil {
				t.Fatal(err)
			}
			if err := api.registerCrewFunctionTools(reg, actor, sessionID, QueryRequest{SelectedFolder: externalCommandCrewRoot}, crewTriggerLinkCaller(externalCommandCrewRoot), nil); err != nil {
				t.Fatal(err)
			}
			args = map[string]any{"action": "call", "project": binding.Root, "function": "run_script", "input": map[string]any{"enabled": true}}
		} else if err := api.registerCrewWorkflowRunTools(reg, actor, sessionID, externalCommandCrewRoot); err != nil {
			t.Fatal(err)
		}
		var definitions []mcpagent.ToolDefinition
		for name, tool := range reg.tools {
			definitions = append(definitions, mcpagent.ToolDefinition{Name: name, Description: tool.desc, InputSchema: tool.params, Execute: tool.exec})
		}
		projected, err := platformtools.Project(definitions)
		if err != nil {
			t.Fatal(err)
		}
		var functions mcpagent.ToolDefinition
		for _, tool := range projected {
			if tool.Name == "functions" {
				functions = tool
			}
		}
		if functions.Execute == nil {
			t.Fatal("functions family absent")
		}
		transport := httptest.NewServer(AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var in map[string]any
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			out, err := functions.Execute(r.Context(), in)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			w.Write([]byte(out))
		})))
		defer transport.Close()
		token, err := GenerateJWT(actor, actor, "")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(args)
		request, _ := http.NewRequestWithContext(ctx, "POST", transport.URL, bytes.NewReader(raw))
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("actual functions start HTTP%d %s", response.StatusCode, body)
		}
		var accepted struct {
			RunID  string `json:"run_id"`
			CallID string `json:"call_id"`
		}
		if err := json.Unmarshal(body, &accepted); err != nil {
			t.Fatal(err)
		}
		runID = accepted.RunID
		if runID == "" && accepted.CallID != "" {
			call := lookupCrewFunctionCall(accepted.CallID)
			if call != nil {
				call.mu.Lock()
				runID = call.RunID
				call.mu.Unlock()
			}
		}
		if runID == "" {
			t.Fatalf("no actual run accepted: %s", body)
		}
		t.Cleanup(func() { stopRun(runID) })
	}
	layout, found, err := crewroot.LookupExecutable(f.docs, binding.Root)
	if err != nil || !found {
		t.Fatalf("current script runtime: %v", err)
	}
	var heartbeat string
	for heartbeat == "" && ctx.Err() == nil {
		_ = filepath.WalkDir(filepath.Join(layout.Runtime, "runs"), func(path string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && entry.Name() == "heartbeat.txt" {
				heartbeat = path
			}
			return nil
		})
		if heartbeat == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if heartbeat == "" {
		t.Fatalf("registered Crew tool never started Python: %v", ctx.Err())
	}
	// PLAT-746-506: the real writer is a child of the waiting Python worker,
	// not the tracked launcher. A TERM-ignoring child exposes wrapper-only Stop.
	pidBytes, err := os.ReadFile(filepath.Join(filepath.Dir(heartbeat), "worker-pids.json"))
	var pids struct {
		Parent int `json:"parent"`
		Child  int `json:"child"`
		Group  int `json:"group"`
	}
	if err != nil || json.Unmarshal(pidBytes, &pids) != nil || pids.Parent <= 0 || pids.Child <= 0 || pids.Parent == pids.Child || pids.Group <= 0 {
		t.Fatalf("heartbeat was not from the actual child: %s %v", pidBytes, err)
	}
	routes := mux.NewRouter()
	routes.HandleFunc("/api/sessions/{session_id}/background-work", api.handleGetSessionBackgroundWork)
	routes.HandleFunc("/api/sessions/{session_id}/background-work/{execution_id}/stop", api.handleStopSessionBackgroundWork)
	endpoint := httptest.NewServer(AuthMiddleware(routes))
	defer endpoint.Close()
	ownerToken, err := GenerateJWT(actor, actor, "")
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string) (int, []byte) {
		t.Helper()
		request, _ := http.NewRequestWithContext(ctx, method, endpoint.URL+path, nil)
		request.Header.Set("Authorization", "Bearer "+ownerToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, body
	}
	status, body := call(http.MethodGet, "/api/sessions/"+sessionID+"/background-work")
	var work struct {
		Items []SessionBackgroundWorkItem `json:"items"`
	}
	if err := json.Unmarshal(body, &work); err != nil || status != 200 || len(work.Items) == 0 || !work.Items[0].CanStop {
		t.Fatalf("actual Python missing from chat's background work: HTTP%d %s (%v)", status, body, err)
	}
	id := work.Items[0].ID
	// A valid person cannot stop another person's chat or borrow the execution
	// ID through another chat they own.
	foreign := "other"
	if actor == "other" {
		foreign = "owner"
	}
	foreignToken, err := GenerateJWT(foreign, foreign, "")
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL+"/api/sessions/"+sessionID+"/background-work/"+id+"/stop", nil)
	request.Header.Set("Authorization", "Bearer "+foreignToken)
	refused, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	refused.Body.Close()
	if refused.StatusCode != 404 {
		t.Fatalf("foreign person Stop HTTP%d", refused.StatusCode)
	}
	api.activeSessionsMux.Lock()
	api.activeSessions["other-chat"] = &ActiveSessionInfo{SessionID: "other-chat", UserID: actor, WorkspacePath: externalCommandCrewRoot}
	api.activeSessionsMux.Unlock()
	if status, body := call(http.MethodPost, "/api/sessions/other-chat/background-work/"+id+"/stop"); status != 404 {
		t.Fatalf("borrowed execution Stop HTTP%d %s", status, body)
	}
	if entry != "bound" {
		// Losing the active origin binding does not cancel or hide the durable
		// workflow run; reopening the chat restores its background controls.
		api.activeSessionsMux.Lock()
		saved := api.activeSessions[sessionID]
		delete(api.activeSessions, sessionID)
		api.activeSessionsMux.Unlock()
		before, _ := os.ReadFile(heartbeat)
		time.Sleep(120 * time.Millisecond)
		after, _ := os.ReadFile(heartbeat)
		if string(before) == string(after) {
			t.Fatal("detaching origin canceled worker")
		}
		run, err := api.scheduler.existingWebhookRun(ctx, runID)
		if err != nil || run.State != schedulerstate.StateWorkflowRunning {
			t.Fatalf("detached worker missing from durable runs: %+v %v", run, err)
		}
		api.activeSessionsMux.Lock()
		api.activeSessions[sessionID] = saved
		api.activeSessionsMux.Unlock()
	}
	if status, body := call(http.MethodPost, "/api/sessions/"+sessionID+"/background-work/"+id+"/stop"); status != 200 {
		t.Fatalf("actual Python Stop: HTTP%d %s", status, body)
	}
	stoppedAt := time.Now()
	for api.bgAgentRegistry.Get(sessionID, id).GetStatus() == BGAgentRunning && ctx.Err() == nil {
		time.Sleep(20 * time.Millisecond)
	}
	if api.bgAgentRegistry.Get(sessionID, id).GetStatus() != BGAgentCanceled {
		t.Fatal("HTTP Stop did not cancel the running script")
	}
	if entry == "bound" {
		var settled bool
		for !settled && ctx.Err() == nil {
			api.workshopChatSessions.Range(func(_, value any) bool {
				if session, ok := value.(*workflow.WorkshopChatSession); ok {
					if snapshot, found := session.StepRegistry.GetSnapshot(id); found && snapshot.Status == workflow.WorkshopStepCancelled {
						settled = true
					}
				}
				return true
			})
			if !settled {
				time.Sleep(20 * time.Millisecond)
			}
		}
		if !settled {
			t.Fatal("Python worker did not return after its Stop")
		}
	}
	// Cancellation is asynchronous: wait for the actual process's writes to stop,
	// rather than treating a registry status alone as proof it ended.
	var last string
	stable := 0
	for stable < 10 && ctx.Err() == nil && time.Since(stoppedAt) < 5*time.Second {
		raw, err := os.ReadFile(heartbeat)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) == last {
			stable++
		} else {
			last, stable = string(raw), 0
		}
		time.Sleep(50 * time.Millisecond)
	}
	if stable < 10 {
		t.Fatal("Python kept writing after the authenticated HTTP Stop")
	}
	if remaining := time.Until(stoppedAt.Add(5 * time.Second)); remaining > 0 {
		time.Sleep(remaining)
	}
	before, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	after, err := os.Stat(heartbeat)
	if err != nil || !after.ModTime().Equal(before.ModTime()) || after.ModTime().After(stoppedAt.Add(5*time.Second)) {
		t.Fatalf("actual child heartbeat advanced after Stop +5s: before=%v after=%v err=%v", before, after, err)
	}
	if entry == "trigger" {
		// A timer has no originating chat: it remains a normal durable Crew run,
		// with no new background item or dependency on the parent chat staying open.
		manifest, found, err := ReadWorkflowManifest(ctx, binding.Root)
		if err != nil || !found {
			t.Fatal(err)
		}
		timer := WorkflowSchedule{ID: "unattended-timer", Name: "Timer", ScheduleType: "cron", CronExpression: "0 0 1 1 *", Timezone: "UTC", Enabled: true, WorkshopMode: "run", Mode: "workshop", InputPreset: "Saved", Inputs: map[string]string{"enabled": "true"}}
		manifest.Schedules = append(manifest.Schedules, timer)
		if err := WriteWorkflowManifest(ctx, binding.Root, manifest); err != nil {
			t.Fatal(err)
		}
		if _, err := api.scheduler.triggerSavedSchedule(binding.Root, timer.ID, "", nil); err != nil {
			t.Fatal(err)
		}
		key := scheduleRuntimeKey(buildScheduleContext(binding.Root, manifest, timer))
		api.scheduler.runtimeStatesMu.RLock()
		timerRun := api.scheduler.runtimeStates[key].ActiveRunID
		api.scheduler.runtimeStatesMu.RUnlock()
		t.Cleanup(func() { stopRun(timerRun) })
		if timerRun == "" {
			t.Fatal("timer did not start")
		}
		for ctx.Err() == nil {
			run, err := api.scheduler.existingWebhookRun(ctx, timerRun)
			if err == nil && run.State == schedulerstate.StateWorkflowRunning {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if ctx.Err() != nil {
			t.Fatal("timer never ran")
		}
		if items := api.sessionBackgroundWork(sessionID); len(items) != 0 {
			t.Fatalf("unattended timer registered in originating chat: %+v", items)
		}
		stopRun(timerRun)
	}
}
