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
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/clisecurity"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-414: the earlier real-Python fixture entered the runner directly,
// missing the storage opens done by a trigger and a saved timer before launch.
// Both entrances here use the actual API/session/scheduler and Python runner.
func checkSavedScriptTriggerAndSchedule(t *testing.T, docs string, binding crewExecutableBinding) {
	t.Helper()
	t.Setenv("AGENTWORKS_SLOTS", "off")
	// The build host has other service users; use this fixture's own managed
	// browser socket parent, preserving the real scope/permission checks.
	t.Setenv("AGENTWORKS_BROWSER_STAGING_NAMESPACE", t.Name()+docs)
	managedBrowserRoot := browserconfig.ManagedSocketRoot()
	t.Cleanup(func() { os.RemoveAll(managedBrowserRoot) })
	registry := agentprofiles.NewRegistry()
	for _, profile := range workproduct.BuiltinAgentProfiles() {
		profile.Product = "work"
		if err := registry.RegisterProfile(profile); err != nil {
			t.Fatal(err)
		}
	}
	eventStore := events.NewEventStore(500)
	t.Cleanup(eventStore.Stop)
	api := &StreamingAPI{
		config:   ServerConfig{Provider: "openai", ModelID: "unused-script-fixture", MaxTurns: 1},
		provider: "openai", model: "unused-script-fixture", logger: loggerv2.NewNoop(),
		agentProfiles: registry, eventStore: eventStore, terminalStore: terminals.NewStore(), runtimeCoordinator: NewRuntimeCoordinator(),
		bgAgentRegistry:  NewBackgroundAgentRegistry(),
		agentCancelFuncs: map[string]context.CancelFunc{}, workflowOrchestratorContexts: map[string]context.CancelFunc{},
		activeWorkflowExecutions: map[string]*ActiveWorkflowExecution{}, trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{},
		sessionQueryIDs: map[string][]string{}, workflowObjectives: map[string]string{}, workflowStepIDs: map[string]string{},
		conversationHistory: map[string][]llmtypes.MessageContent{}, restoredConversationPersistTargets: map[string]restoredChatHistoryPersistTarget{},
		activeSessions: map[string]*ActiveSessionInfo{}, workflowOrchestrators: map[string]orchestrator.Orchestrator{},
		sessionBusy: map[string]bool{}, sessionBusySince: map[string]time.Time{},
	}
	// Real empty stores mirror server startup; no provider credentials or AI
	// executor are supplied. Both entrances must finish using Python alone.
	var err error
	api.chatStore, err = chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api.cliSecurityStore, err = clisecurity.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api.mcpConfigPath = filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(api.mcpConfigPath, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	api.lastQueryRequests = map[string]QueryRequest{}
	api.projectActions = NewProjectActionDispatcher(api, registry)
	api.scheduler = NewSchedulerService(api)
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "scheduler.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	api.scheduler.stateStore = store
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if _, _, _, _, err := api.projectActions.projectManifestAnyOwner(ctx, "owner", "work", binding.CrewID); err != nil {
		t.Fatalf("fixture parent Crew must resolve before checking trigger admission: %v", err)
	}
	manifest, found, err := ReadWorkflowManifest(ctx, binding.Root)
	if err != nil || !found {
		t.Fatal(err)
	}
	manifest.Schedules = []WorkflowSchedule{{ID: "fixture-timer", Name: "Script fixture", ScheduleType: "cron", CronExpression: "0 0 1 1 *", Timezone: "UTC", Enabled: true, ChatID: "main", Inputs: map[string]string{"count": "6"}}}
	if err := WriteWorkflowManifest(ctx, binding.Root, manifest); err != nil {
		t.Fatal(err)
	}
	reg := &recordingRegistrar{}
	if err := api.registerCrewWorkflowRunTools(reg, "owner", "fixture-parent", binding.CrewRoot); err != nil {
		t.Fatal(err)
	}
	var tools []mcpagent.ToolDefinition
	for name, tool := range reg.tools {
		tools = append(tools, mcpagent.ToolDefinition{Name: name, Description: tool.desc, InputSchema: tool.params, DisplayGroup: tool.group, Execute: tool.exec})
	}
	projected, err := platformtools.Project(tools)
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
		t.Fatal("current functions family was not advertised")
	}
	endpoint := httptest.NewServer(AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/timer" {
			result, err := api.scheduler.triggerSavedSchedule(binding.Root, "fixture-timer", "", nil)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": result})
			return
		}
		var args map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		result, err := functions.Execute(r.Context(), args)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Write([]byte(result))
	})))
	defer endpoint.Close()
	token, err := GenerateJWT("owner", "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	call := func(path, body string) []byte {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "POST", endpoint.URL+path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		if response.StatusCode != 200 {
			t.Fatalf("%s HTTP%d %s", path, response.StatusCode, raw)
		}
		return raw
	}
	for _, target := range []string{binding.Root, binding.ID, "workflow:" + binding.ID} {
		args, _ := json.Marshal(map[string]interface{}{"action": "triggers", "workspace_path": target})
		raw := call("/functions", string(args))
		var listed struct {
			Inputs []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"inputs"`
			Routes  []interface{} `json:"routes"`
			Presets []string      `json:"input_presets"`
		}
		if err := json.Unmarshal(raw, &listed); err != nil {
			t.Fatal(err)
		}
		types := map[string]string{}
		for _, input := range listed.Inputs {
			types[input.Name] = input.Type
		}
		if types["extended"] != "boolean" || types["rate"] != "number" || len(listed.Routes) != 0 || len(listed.Presets) != 1 {
			t.Fatalf("script discovery lost typed inputs/presets for %s: %s", target, raw)
		}
	}

	triggerArgs, _ := json.Marshal(map[string]interface{}{"action": "run_trigger", "workspace_path": binding.Root, "inputs": map[string]interface{}{"count": 5}, "delivery_id": "fixture-once"})
	var accepted struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(call("/functions", string(triggerArgs)), &accepted); err != nil || accepted.RunID == "" {
		t.Fatalf("trigger acceptance: %+v %v", accepted, err)
	}
	verify := func(id, schedule string, count string) {
		t.Helper()
		for {
			runs, err := ReadScheduleRuns(ctx, binding.Root)
			if err != nil {
				t.Fatal(err)
			}
			for _, run := range runs {
				if id != "" && run.ID != id || schedule != "" && run.ScheduleID != schedule || run.CompletedAt == nil {
					continue
				}
				if run.Status != "success" {
					t.Fatalf("saved-script entrance failed: %+v", run)
				}
				layout, found, err := crewroot.LookupExecutable(docs, binding.Root)
				if err != nil || !found {
					t.Fatal(err)
				}
				path := filepath.Join(layout.Runtime, "runs", run.RunFolder, "execution/saved/result.json")
				raw, err := os.ReadFile(path)
				if err != nil || !strings.Contains(string(raw), `"count": `+count) || !strings.Contains(string(raw), `"python_ran": true`) {
					t.Fatalf("real Python result missing: %s %v", raw, err)
				}
				var artifact struct {
					RunFolder string `json:"run_folder"`
				}
				if err := json.Unmarshal(raw, &artifact); err != nil || artifact.RunFolder != filepath.Join(layout.Runtime, "runs", run.RunFolder) {
					t.Fatalf("script got wrong permanent WORKFLOW_RUN_DIR: %s %v", raw, err)
				}
				// Runs UI and step inspection use these persisted events. A full
				// stdout copy here would recreate the native receipt token failure.
				payload, err := json.Marshal(api.eventStore.GetAllEventsRaw(run.SessionID))
				if err != nil || bytes.Contains(payload, bytes.Repeat([]byte("Q"), 32768)) {
					t.Fatal("run events retained unbounded script stdout")
				}
				// The Python helper uses this existing chat append service. Its
				// progress must appear without starting an AI turn or using an
				// external notification destination.
				resources := newCrewWorkflowResources(api.projectActions, "owner")
				destination, err := resources.NotifyScript(ctx, binding.Root, run.SessionID, "notify-"+run.ID, "Python helper fixture progress")
				if err != nil || destination == "" {
					t.Fatalf("script progress destination: %q %v", destination, err)
				}
				progress, _ := json.Marshal(api.eventStore.GetAllEventsRaw(destination))
				if !bytes.Contains(progress, []byte("Python helper fixture progress")) {
					t.Fatal("script notification missing from the real chat journal")
				}
				t.Logf("script entrance schedule=%s run=%s real_python=true private_run_folder=%s", run.ScheduleID, run.ID, run.RunFolder)
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	verify(accepted.RunID, "", "5")
	call("/timer", `{}`)
	verify("", "fixture-timer", "6")
	if _, err := os.Stat(filepath.Join(docs, binding.Root)); !os.IsNotExist(err) {
		t.Fatalf("script recreated retired runtime: %v", err)
	}
}
