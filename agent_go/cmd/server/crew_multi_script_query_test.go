package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/clisecurity"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-500 / PR1658: multiple valid saved scripts must not make the
// owning Crew's real query/agent assembly fail. Stop before contacting a
// model, after all production registration and definition finalization.
func TestCrewQueryStartsWithZeroOneAndMultipleSavedScripts(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]}]}`)
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_SLOTS", "off")
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "claude-code")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	t.Setenv("NATIVE_WORKSPACE", "true")
	logger := loggerv2.NewNoop()
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(cfg, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	mcpConfig, err := mcpclient.LoadConfig(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{
		agentProfiles: f.api.agentProfiles, chatStore: store, logger: logger,
		config:   ServerConfig{Host: "127.0.0.1", Provider: "claude-code", ModelID: "claude-sonnet-4-6", MaxTurns: 1, MCPConfigPath: cfg},
		provider: "claude-code", model: "claude-sonnet-4-6", mcpConfigPath: cfg, mcpConfig: mcpConfig,
		eventStore: events.NewEventStore(100), runtimeCoordinator: NewRuntimeCoordinator(),
		bgAgentRegistry:                     NewBackgroundAgentRegistry(),
		agentCancelFuncs:                    make(map[string]context.CancelFunc),
		workflowOrchestratorContexts:        make(map[string]context.CancelFunc),
		activeWorkflowExecutions:            make(map[string]*ActiveWorkflowExecution),
		trackedWorkflowExecutions:           make(map[string]*TrackedWorkflowExecution),
		sessionQueryIDs:                     make(map[string][]string),
		workflowObjectives:                  make(map[string]string),
		conversationHistory:                 make(map[string][]llmtypes.MessageContent),
		restoredConversationPersistTargets:  make(map[string]restoredChatHistoryPersistTarget),
		toolStatus:                          make(map[string]ToolStatus),
		enabledTools:                        make(map[string][]string),
		serverLogs:                          make(map[string][]ServerLogEntry),
		discoveryFailedServers:              make(map[string]string),
		activeSessions:                      make(map[string]*ActiveSessionInfo),
		workflowOrchestrators:               make(map[string]orchestrator.Orchestrator),
		workflowStepIDs:                     make(map[string]string),
		sessionBusy:                         make(map[string]bool),
		sessionBusySince:                    make(map[string]time.Time),
		retainedMainTurns:                   make(map[string]time.Time),
		retainedMainTurnExecutionIDs:        make(map[string]string),
		retainedMainTurnPendingExecutionIDs: make(map[string][]string),
		retainedMainTurnWatchCancels:        make(map[string]context.CancelFunc),
		pendingCompletions:                  make(map[string][]string),
		completionRetryScheduled:            make(map[string]bool),
		pendingStartNotifications:           make(map[string][]string),
		startNotificationRetryScheduled:     make(map[string]bool),
		lastQueryRequests:                   make(map[string]QueryRequest),
		sessionWorkspaceFolders:             make(map[string]string),
		sessionAgents:                       make(map[string]*agent.LLMAgentWrapper),
		runningAgents:                       make(map[string]*mcpagent.Agent),
		sessionInputLanes:                   make(map[string]*sessionInputLane),
		conversationTurnQueueOwners:         make(map[string]map[string]bool),
		conversationTurnQueueDraining:       make(map[string]bool),
		conversationTurnQueueWaiters:        make(map[string]chan queuedConversationTurnResult),
		conversationTurnQueueCallbacks:      make(map[string]func(event *unifiedevents.AgentEvent)),
		completionLoopStarted:               make(map[string]bool),
		lastWorkshopModeBySession:           make(map[string]string),
		lastChatPolicyBySession:             make(map[string]string),
		lastChatPolicyRoleBySession:         make(map[string]string),
		lastAgentProfileKeyBySession:        make(map[string]string),
		stoppedSessions:                     make(map[string]bool),
		interruptedTurns:                    make(map[string]bool),
	}
	api.cliSecurityStore, err = clisecurity.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api.projectActions = NewProjectActionDispatcher(api, api.agentProfiles)
	api.scheduler = NewSchedulerService(api)
	t.Cleanup(api.eventStore.Stop)
	type surface struct{ bound, named bool }
	captured := make(chan surface, 3)
	api.internalPreparedAgent = func(_ context.Context, prepared *mcpagent.Agent) bool {
		offered := surface{}
		for _, tool := range prepared.Definition().Tools {
			if tool.Name == "run_full_workflow" {
				offered.bound = true
			}
			if tool.Name == "crew_execution" {
				offered.named = true
			}
		}
		captured <- offered
		return true
	}
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	for count := 0; count <= 2; count++ {
		if count > 0 {
			id := fmt.Sprintf("script-%d", count)
			binding := crewExecutableBinding{ID: id, CrewID: "team", CrewRoot: externalCommandCrewRoot, Root: "Workflow/" + id, Kind: "script", RuntimeRoot: "workflows/" + id}
			if err := registerCrewExecutable(binding); err != nil {
				t.Fatal(err)
			}
			if err := initializeCrewScriptSource(ctx, binding, "Saved script"); err != nil {
				t.Fatal(err)
			}
		}
		sid := fmt.Sprintf("crew-script-count-%d", count)
		req := QueryRequest{Query: "Hello", AgentMode: "multi-agent", AgentProfileID: crewProfileID, AgentProfileConversationKey: "team", SelectedFolder: externalCommandCrewRoot, AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Fixture Crew"}, Provider: "claude-code", ModelID: "claude-sonnet-4-6"}
		request := sharedSecretsRequest(http.MethodPost, "/api/query", "owner", req)
		request.Header.Set("X-Session-ID", sid)
		reply := httptest.NewRecorder()
		api.handleQuery(reply, request)
		if reply.Code >= 400 {
			t.Fatalf("%d scripts: query HTTP%d %s", count, reply.Code, reply.Body.String())
		}
		select {
		case offered := <-captured:
			if offered.bound != (count == 1) || !offered.named {
				t.Fatalf("%d scripts: start surface=%+v", count, offered)
			}
		case <-time.After(12 * time.Second):
			history := api.eventStore.GetEvents(sid, events.GetEventsOptions{})
			t.Fatalf("%d scripts: Crew query never completed agent assembly; HTTP%d %s events=%v", count, reply.Code, reply.Body.String(), history)
		}
	}
}
