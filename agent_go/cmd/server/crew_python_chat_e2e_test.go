package server

import (
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
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-746: exercise the Crew chat's actual lazy registration/bound executor,
// real Python and background registry, then its authenticated HTTP Stop. This
// does not claim a model interpreted a message or a deployed AI was exercised.
func TestCrewChatSavedPythonStartAndHTTPStop(t *testing.T) {
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
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"other","username":"other","can_create":true,"products":["work"]}]}`)
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
	api := f.api
	api.projectActions = NewProjectActionDispatcher(api, api.agentProfiles)
	var err error
	api.chatStore, err = chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api.logger = loggerv2.NewNoop()
	api.bgAgentRegistry = NewBackgroundAgentRegistry()
	api.eventStore = events.NewEventStore(100)
	t.Cleanup(api.eventStore.Stop)
	api.eventStore.SetSessionOwner(sessionID, "owner")
	api.activeSessions = map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, UserID: "owner", WorkspacePath: externalCommandCrewRoot}}
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
	binding := crewExecutableBinding{ID: "chat-saved", CrewID: "team", CrewRoot: externalCommandCrewRoot, Root: "Workflow/chat-saved", Kind: "script", RuntimeRoot: "workflows/chat-saved"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	if err := initializeCrewScriptSource(ctx, binding, "Chat saved Python"); err != nil {
		t.Fatal(err)
	}
	if err := saveCrewScriptInputs(ctx, binding.Root, &workflow.VariablesManifest{
		Variables: []workflow.Variable{{Name: "count", Type: "integer", Value: "2"}, {Name: "enabled", Type: "boolean", Value: "false"}},
		Groups:    []workflow.VariableGroup{{Name: "Saved", Values: map[string]string{"count": "7"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeFileToWorkspace(ctx, binding.Root+"/code/main.py", `import time
from agentworks import inputs, run_folder
assert inputs['count'] == 7 and inputs['enabled'] is True, inputs
while True:
    (run_folder / 'heartbeat.txt').write_text(str(time.monotonic_ns()))
    time.sleep(0.05)
`); err != nil {
		t.Fatal(err)
	}
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
	routes := mux.NewRouter()
	routes.HandleFunc("/api/sessions/{session_id}/background-work", api.handleGetSessionBackgroundWork)
	routes.HandleFunc("/api/sessions/{session_id}/background-work/{execution_id}/stop", api.handleStopSessionBackgroundWork)
	endpoint := httptest.NewServer(AuthMiddleware(routes))
	defer endpoint.Close()
	ownerToken, err := GenerateJWT("owner", "owner", "")
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
	if status, body := call(http.MethodPost, "/api/sessions/"+sessionID+"/background-work/"+id+"/stop"); status != 200 {
		t.Fatalf("actual Python Stop: HTTP%d %s", status, body)
	}
	for api.bgAgentRegistry.Get(sessionID, id).GetStatus() == BGAgentRunning && ctx.Err() == nil {
		time.Sleep(20 * time.Millisecond)
	}
	if api.bgAgentRegistry.Get(sessionID, id).GetStatus() != BGAgentCanceled {
		t.Fatal("HTTP Stop did not cancel the running script")
	}
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
	// Cancellation is asynchronous: wait for the actual process's writes to stop,
	// rather than treating a registry status alone as proof it ended.
	var last string
	stable := 0
	for stable < 10 && ctx.Err() == nil {
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
}
