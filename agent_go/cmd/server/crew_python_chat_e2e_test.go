package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
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

// PLAT-871-33: authenticated Crew timer create/list/run/Stop uses the actual
// saved Python runner, with typed inputs and the single Crew run ledger.
func TestCrewWorkflowTimerRunsPythonAndSupportsHTTPStop(t *testing.T) {
	checkCrewChatPythonStop(t, "crew-timer")
}

// PLAT-871-33: an on-time dependency wait survives the ordinary clock window,
// uses its original occurrence, and launches real Python only after release.
func TestCrewWorkflowTimerWaitsForPrerequisiteWithoutCatchup(t *testing.T) {
	checkCrewChatPythonStop(t, "crew-timer-dependency")
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
	timeout := 35 * time.Second
	if entry == "crew-timer-dependency" {
		timeout = 60 * time.Second // the actual clock adds up to five seconds of launch jitter
	}
	ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"}), timeout)
	defer cancel()
	stopRun := func(id string) {
		api.scheduler.cancelScheduleRunContext(id)
		if run, err := api.scheduler.existingWebhookRun(context.Background(), id); err == nil && run.ActiveSessionID != "" {
			api.scheduler.cancelScheduledSessionWork(run.ActiveSessionID, "fixture cleanup", runtimePhaseCanceled)
		}
	}
	binding := crewExecutableBinding{ID: "chat-saved", CrewID: "team", CrewRoot: externalCommandCrewRoot, Root: "Workflow/chat-saved", Kind: "script", RuntimeRoot: "workflows/chat-saved"}
	if strings.HasPrefix(entry, "crew-timer") {
		// The Python adoption converter preserves Workflow/<same-id> and stores
		// kind=script, runtime_root=workflows/<same-id> (not kind=workflow).
		// Exercise that adopted binding, rather than the new automations layout.
		binding.ID, binding.Root, binding.RuntimeRoot = "adopted-same-id", "Workflow/adopted-same-id", "workflows/adopted-same-id"
	}
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
	var runID, timerJobID string
	var dependencyOccurrence time.Time
	var timerHTTP func(string, string, any) (int, []byte)
	if strings.HasPrefix(entry, "crew-timer") {
		api.costLedger = installWorkflowCostTestLedger(t)
		routes := mux.NewRouter()
		SchedulerRoutes(routes, api.scheduler)
		server := httptest.NewServer(AuthMiddleware(routes))
		defer server.Close()
		token, err := GenerateJWT("owner", "owner", "")
		if err != nil {
			t.Fatal(err)
		}
		timerHTTP = func(method, path string, data any) (int, []byte) {
			t.Helper()
			raw, _ := json.Marshal(data)
			req, _ := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)
			return res.StatusCode, body
		}
		create := map[string]any{"entity_type": "product", "product_profile_id": "work", "product_project_id": "team", "name": "Saved Python timer", "schedule_type": "cron", "cron_expression": "0 0 1 1 *", "timezone": "UTC", "enabled": false, "target_workflow_id": binding.ID, "input_preset": "Saved", "inputs": map[string]string{"enabled": "true"}}
		registerTestCrew(t, "foreign", "other", true, "foreign")
		if err := registerCrewExecutable(crewExecutableBinding{ID: "foreign-script", CrewID: "foreign", CrewRoot: "Crew/foreign", Root: "Workflow/foreign-script", Kind: "script", RuntimeRoot: "workflows/foreign-script"}); err != nil {
			t.Fatal(err)
		}
		create["target_workflow_id"] = "foreign-script"
		if status, body := timerHTTP("POST", "/api/scheduler/jobs", create); status != 400 || !strings.Contains(string(body), "this Crew") {
			t.Fatalf("foreign target was not refused by ownership: HTTP%d %s", status, body)
		}
		create["target_workflow_id"] = binding.ID
		var prerequisite string
		if entry == "crew-timer-dependency" {
			create["after_schedule_ids"] = []string{projectScheduleJobID("work", "foreign", "timer")}
			if status, body := timerHTTP("POST", "/api/scheduler/jobs", create); status != 400 || !strings.Contains(string(body), "this Crew") {
				t.Fatalf("foreign prerequisite accepted: HTTP%d %s", status, body)
			}
			delete(create, "after_schedule_ids")
			status, body := timerHTTP("POST", "/api/scheduler/jobs", map[string]any{"entity_type": "product", "product_profile_id": "work", "product_project_id": "team", "name": "Prerequisite", "schedule_type": "cron", "cron_expression": "0 0 1 1 *", "timezone": "UTC", "enabled": false, "messages": []string{"Collect the data"}})
			var saved ScheduledJobResponse
			if status != 201 || json.Unmarshal(body, &saved) != nil {
				t.Fatalf("prerequisite create: HTTP%d %s", status, body)
			}
			prerequisite = strings.TrimPrefix(saved.ID, "product-project:work:team:")
			create["enabled"], create["cron_expression"] = true, "* * * * *"
			create["after_schedule_ids"], create["after_delay_minutes"] = []string{saved.ID}, 0
		}
		status, body := timerHTTP("POST", "/api/scheduler/jobs", create)
		var saved ScheduledJobResponse
		if status != 201 || json.Unmarshal(body, &saved) != nil || saved.TargetWorkflowID != binding.ID || saved.Inputs["enabled"] != "true" {
			t.Fatalf("create lost target/inputs: HTTP%d %s", status, body)
		}
		timerJobID = saved.ID
		if status, body := timerHTTP("GET", "/api/scheduler/jobs/"+timerJobID, nil); status != 200 || !strings.Contains(string(body), binding.ID) {
			t.Fatalf("timer list lost target: HTTP%d %s", status, body)
		}
		if entry == "crew-timer-dependency" {
			if len(saved.AfterScheduleIDs) != 1 || saved.AfterScheduleIDs[0] != projectScheduleJobID("work", "team", prerequisite) || saved.AfterDelayMinutes != 0 {
				t.Fatalf("create lost dependency projection: %+v", saved)
			}
			dependencyOccurrence = time.Now().UTC().Truncate(time.Minute)
			rawID := strings.TrimPrefix(timerJobID, "product-project:work:team:")
			// The real admission guard rechecks wall time after clock dispatch.
			// Backdate only the fixture activation cursor, not the occurrence or
			// completion, so that guard can observe the same ready prerequisite.
			if err := api.projectActions.updateStateByKey(ctx, "owner", projectScheduleStateKey("work", "team", rawID), func(state *productScheduleUserState) {
				state.ActivatedAt = dependencyOccurrence.Add(-time.Minute).Format(time.RFC3339)
			}); err != nil {
				t.Fatal(err)
			}
			checkWaiting := func(at time.Time) {
				t.Helper()
				api.scheduler.dispatchProjectTimers(ctx, at)
				pending, err := store.GetPendingOccurrence(ctx, "crew", "team", rawID)
				if err != nil || !pending.ScheduledFor.Equal(dependencyOccurrence) || pending.Policy != "retry" {
					t.Fatalf("dependency occurrence lost: %+v %v", pending, err)
				}
				decisions, _ := store.ListFireDecisions(ctx, "crew", "team", rawID, 10)
				for _, decision := range decisions {
					if decision.RunID != "" {
						t.Fatalf("Python admitted before its prerequisite: %+v", decision)
					}
				}
			}
			checkWaiting(dependencyOccurrence)
			checkWaiting(dependencyOccurrence.Add(5 * time.Minute))
			completePrerequisite := func(id string, occurrence, completed time.Time) {
				t.Helper()
				if err := store.RecordAdmittedRun(ctx, schedulerstate.Run{RunID: id, ScopeType: "crew", ScopeID: "team", ScheduleID: prerequisite, TriggerSource: "cron", ScheduledFor: occurrence, StartedAt: occurrence}); err != nil {
					t.Fatal(err)
				}
				if err := store.ForceTerminal(ctx, schedulerstate.Transition{RunID: id, To: schedulerstate.StateCompleted, At: completed}); err != nil {
					t.Fatal(err)
				}
			}
			// A later prerequisite occurrence cannot release this older wait.
			completePrerequisite("later-prerequisite", dependencyOccurrence.Add(time.Minute), dependencyOccurrence.Add(2*time.Minute))
			checkWaiting(dependencyOccurrence.Add(5 * time.Minute))
			completePrerequisite("matching-prerequisite", dependencyOccurrence, time.Now().UTC())
			api.scheduler.dispatchProjectTimers(ctx, dependencyOccurrence.Add(5*time.Minute))
		} else if status, body := timerHTTP("POST", "/api/scheduler/jobs/"+timerJobID+"/trigger", nil); status != 200 {
			t.Fatalf("manual Crew workflow timer: HTTP%d %s", status, body)
		}
		var admissions []schedulerstate.FireDecision
		for ctx.Err() == nil {
			decisions, err := store.ListFireDecisions(ctx, "crew", "team", strings.TrimPrefix(saved.ID, "product-project:work:team:"), 10)
			if err != nil {
				t.Fatal(err)
			}
			admissions = nil
			for _, decision := range decisions {
				if decision.RunID != "" {
					admissions = append(admissions, decision)
				}
			}
			if len(admissions) != 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if len(admissions) != 1 {
			t.Fatalf("one timer admission: %+v %v", admissions, ctx.Err())
		}
		runID = admissions[0].RunID
		if entry == "crew-timer-dependency" {
			if !admissions[0].ScheduledFor.Equal(dependencyOccurrence) || admissions[0].TriggerSource != "queued" {
				t.Fatalf("dependency retry replaced original occurrence: %+v", admissions[0])
			}
			if _, err := store.GetPendingOccurrence(ctx, "crew", "team", strings.TrimPrefix(timerJobID, "product-project:work:team:")); !errors.Is(err, schedulerstate.ErrRunNotFound) {
				t.Fatalf("admission did not consume pending occurrence: %v", err)
			}
		}
		t.Cleanup(func() { api.projectActions.Stop("owner", timerJobID) })
	} else if entry == "bound" {
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
	if strings.HasPrefix(entry, "crew-timer") {
		// PR1798: direct Python does not pass through the message router, so
		// cron and manual starts must hold the same conversation claim.
		job, err := api.projectActions.Job(ctx, "owner", timerJobID)
		if err != nil {
			t.Fatal(err)
		}
		claim, err := api.projectActions.claimAutomationRun(ctx, job, "cron", time.Now(), productScheduleRunOptions{}, nil)
		if !errors.Is(err, productschedule.ErrAlreadyRunning) {
			if claim != nil {
				claim.cancel()
				api.projectActions.finishAutomationRun(claim.jobKey, claim.convKey)
			}
			t.Fatalf("cron overlapped the running manual workflow: %v", err)
		}
		// Real SQLite entries exercise the visitor's date bounds and run/user
		// attribution. These are fixture costs, not a model billing assertion.
		for i, owner := range []string{"owner", "other"} {
			if err := api.costLedger.Append(costledger.Entry{EventID: fmt.Sprintf("timer-cost-%d", i), UserID: owner, SourceRunID: runID, ModelID: "fixture", Provider: "fixture", PromptTokens: 100, TotalCostUSD: 0.25, LLMCallCount: 1}); err != nil {
				t.Fatal(err)
			}
		}
		if status, body := timerHTTP("POST", "/api/scheduler/jobs/"+timerJobID+"/stop", nil); status != 200 {
			t.Fatalf("Crew timer Stop: HTTP%d %s", status, body)
		}
		stoppedAt := time.Now()
		for ctx.Err() == nil {
			saved, err := store.GetRun(ctx, runID)
			_, busy := api.projectActions.Running("owner", timerJobID)
			if err == nil && saved.State == schedulerstate.StateStopped && !busy {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if ctx.Err() != nil {
			t.Fatal("Crew workflow Stop did not settle terminal evidence/claim")
		}
		assertCrewPythonHeartbeatStops(t, ctx, heartbeat, stoppedAt)
		runsWorkspace, err := api.projectActions.RunsWorkspace(ctx, job)
		if err != nil {
			t.Fatal(err)
		}
		runs, _, err := ListScheduleRuns(ctx, runsWorkspace, timerJobID, 10, 0)
		if err != nil || len(runs) != 1 || runs[0].Usage == nil || runs[0].Usage.PromptTokens != 100 || runs[0].Usage.CostUSD != 0.25 {
			t.Fatalf("attributed SQLite usage missing or includes another person: %+v %v", runs, err)
		}
		if err := writeFileToWorkspace(ctx, binding.Root+"/code/main.py", "from agentworks import inputs, run_folder\nassert inputs['count']==7 and inputs['enabled'] is True\n(run_folder/'result.txt').write_text('timer finished')\n"); err != nil {
			t.Fatal(err)
		}
		if entry == "crew-timer-dependency" {
			// Run now is the explicit override, even while the clock would wait.
			status, body := timerHTTP("PUT", "/api/scheduler/jobs/"+timerJobID, map[string]any{"after_delay_minutes": 1440})
			var updated ScheduledJobResponse
			if status != 200 || json.Unmarshal(body, &updated) != nil || updated.AfterDelayMinutes != 1440 {
				t.Fatalf("dependency update lost existing setting: HTTP%d %s", status, body)
			}
		}
		if status, body := timerHTTP("POST", "/api/scheduler/jobs/"+timerJobID+"/trigger", nil); status != 200 {
			t.Fatalf("second manual timer: HTTP%d %s", status, body)
		}
		var completed schedulerstate.Run
		for ctx.Err() == nil {
			decisions, _ := store.ListFireDecisions(ctx, "crew", "team", strings.TrimPrefix(timerJobID, "product-project:work:team:"), 10)
			for _, d := range decisions {
				if d.RunID != "" && d.RunID != runID {
					completed, _ = store.GetRun(ctx, d.RunID)
				}
			}
			if schedulerstate.IsTerminal(completed.State) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if completed.State != schedulerstate.StateCompleted || completed.RunFolder == "" {
			t.Fatalf("actual saved Python completion: %+v", completed)
		}
		result, err := os.ReadFile(filepath.Join(layout.Runtime, "runs", completed.RunFolder, "result.txt"))
		if err != nil || string(result) != "timer finished" {
			t.Fatalf("actual Python result: %q %v", result, err)
		}
		if status, body := timerHTTP("GET", "/api/scheduler/jobs/"+timerJobID+"/runs", nil); status != 200 || !strings.Contains(string(body), completed.RunID) || !strings.Contains(string(body), binding.Root) {
			t.Fatalf("Crew history lost run/definition path: HTTP%d %s", status, body)
		}
		file, found, err := api.liveConversationPath("owner", completed.ActiveSessionID, externalCommandCrewRoot)
		if err != nil || !found {
			t.Fatalf("timer conversation missing: %s %v", file, err)
		}
		history, found, err := readConversationTailFromWorkspace(ctx, file, 100)
		if err != nil || !found || !strings.Contains(history, runID+":stopped") || !strings.Contains(history, completed.RunID+":completed") {
			t.Fatalf("durable Stop/completion chat receipts missing: %s %v", history, err)
		}
		// Two enabled future timers may become stale together. Pausing/deleting
		// one must not revalidate the unchanged sibling's launch inputs.
		if status, body := timerHTTP("POST", "/api/scheduler/jobs/"+timerJobID+"/enable", nil); status != 200 {
			t.Fatalf("enable valid timer: HTTP%d %s", status, body)
		}
		secondTimer := map[string]any{"entity_type": "product", "product_profile_id": "work", "product_project_id": "team", "name": "Second future timer", "schedule_type": "cron", "cron_expression": "0 0 1 1 *", "timezone": "UTC", "enabled": true, "target_workflow_id": binding.ID, "input_preset": "Saved", "inputs": map[string]string{"enabled": "true"}}
		status, body := timerHTTP("POST", "/api/scheduler/jobs", secondTimer)
		var sibling ScheduledJobResponse
		if status != 201 || json.Unmarshal(body, &sibling) != nil {
			t.Fatalf("create sibling timer: HTTP%d %s", status, body)
		}
		// A later definition edit can invalidate a saved preset. Its attempted
		// run must remain visible in the same user-facing history and status.
		if err := saveCrewScriptInputs(ctx, binding.Root, &workflow.VariablesManifest{Variables: []workflow.Variable{{Name: "count", Type: "integer", Value: "2"}, {Name: "enabled", Type: "boolean", Value: "false"}}}); err != nil {
			t.Fatal(err)
		}
		if status, body := timerHTTP("POST", "/api/scheduler/jobs/"+timerJobID+"/trigger", nil); status != 200 {
			t.Fatalf("preflight attempt not recorded: HTTP%d %s", status, body)
		}
		var latest ScheduledJobResponse
		for ctx.Err() == nil {
			status, body := timerHTTP("GET", "/api/scheduler/jobs/"+timerJobID, nil)
			if status != 200 || json.Unmarshal(body, &latest) != nil {
				t.Fatalf("read preflight result: HTTP%d %s", status, body)
			}
			if latest.LastStatus == "error" {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		runs, _, err = ListScheduleRuns(ctx, runsWorkspace, timerJobID, 10, 0)
		if ctx.Err() != nil || err != nil || latest.ConsecutiveFailures != 1 || latest.LastError == "" || len(runs) != 3 || runs[0].Status != "error" || runs[0].Error == "" {
			t.Fatalf("preflight failure absent from history/state: job=%+v runs=%+v err=%v", latest, runs, err)
		}
		if status, body := timerHTTP("POST", "/api/scheduler/jobs/"+timerJobID+"/disable", nil); status != 200 {
			t.Fatalf("stale sibling blocked pausing timer: HTTP%d %s", status, body)
		}
		if status, body := timerHTTP("DELETE", "/api/scheduler/jobs/"+timerJobID, nil); status != 204 {
			t.Fatalf("stale sibling blocked deleting timer: HTTP%d %s", status, body)
		}
		if status, body := timerHTTP("POST", "/api/scheduler/jobs/"+sibling.ID+"/disable", nil); status != 200 {
			t.Fatalf("remaining stale timer cannot be paused: HTTP%d %s", status, body)
		}
		if status, body := timerHTTP("POST", "/api/scheduler/jobs/"+sibling.ID+"/enable", nil); status != 400 || !strings.Contains(string(body), `saved inputs "Saved" not found`) {
			t.Fatalf("re-enable accepted stale inputs: HTTP%d %s", status, body)
		}
		return
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
	assertCrewPythonHeartbeatStops(t, ctx, heartbeat, stoppedAt)

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

func assertCrewPythonHeartbeatStops(t *testing.T, ctx context.Context, heartbeat string, stoppedAt time.Time) {
	t.Helper()
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
}
