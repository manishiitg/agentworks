package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	agentevents "github.com/manishiitg/mcpagent/events"
	pkgevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Shared fixtures that used to live in deleted test files and are still used by
// tests of this package.

func conditionCodeFixture(t *testing.T) (triggerLinkEnv, context.Context, string) {
	t.Helper()
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	root := registerTestCrew(t, "checks", "owner", false, "checks")
	env.mock.mu.Lock()
	env.mock.files[root+"/product.json"] = `{"schema_version":1,"product":"work","id":"checks","title":"Checks","session_id":"sess-condition-main"}`
	env.mock.files[root+"/code/triggers/check.py"] = "import json\nprint(json.dumps({'events': [], 'state': {}}))\n"
	env.mock.mu.Unlock()
	return env, internalBotRequestContext(context.Background(), "owner"), root
}

func newAgentWorksFixture(t *testing.T) *multiUserFixture {
	t.Helper()
	f := newMultiUserFixture(t, legacyIdentityLayout())
	registry := agentprofiles.NewRegistry()
	crew := f.Crew
	crew.UIPanels.Schedules = true
	for _, profile := range []agentprofiles.Profile{f.Code, crew} {
		if err := registry.RegisterProfile(profile); err != nil {
			t.Fatal(err)
		}
	}
	f.API.agentProfiles = registry
	f.API.projectActions = NewProjectActionDispatcher(f.API, registry)
	return f
}

func lifecycleTestAPI() *StreamingAPI {
	return &StreamingAPI{
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{},
		bgAgentRegistry:           NewBackgroundAgentRegistry(),
	}
}

func stubTerminalTmuxCommand(t *testing.T) *[]string {
	t.Helper()
	t.Setenv(envCodingAgentTmuxOrphanIdleSeconds, "")
	gotArgs := []string{}
	oldRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		gotArgs = append([]string(nil), args...)
		return nil
	}
	oldOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		return "", nil
	}
	t.Cleanup(func() {
		runTerminalTmuxCommand = oldRun
		runTerminalTmuxOutputCommand = oldOutput
	})
	return &gotArgs
}

func codingAgentTmuxReaperChunkEvent(timestamp time.Time, sessionID, executionID, tmuxSession string) storeevents.Event {
	executionKind := "workflow_step"
	scope := "workflow_step"
	if strings.HasPrefix(executionID, "main:") {
		executionKind = "main_agent"
		scope = "main_agent"
	}
	return storeevents.Event{
		Type:          "streaming_chunk",
		Timestamp:     timestamp,
		SessionID:     sessionID,
		ExecutionID:   executionID,
		ExecutionKind: executionKind,
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingChunk,
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{
					Metadata: map[string]interface{}{
						"kind":           "terminal",
						"tmux_session":   tmuxSession,
						"execution_kind": executionKind,
						"scope":          scope,
					},
				},
				Content:    "pane",
				ChunkIndex: 1,
			},
		},
	}
}

func openClientIdentityTestStore(t *testing.T, sessionID string) *internalevents.EventStore {
	t.Helper()
	journal, err := internalevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := internalevents.NewEventStore(100)
	t.Cleanup(store.Stop)
	store.SetDurableJournal(journal)
	if err := store.SetSessionPersistenceClass(sessionID, internalevents.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	return store
}

func testUserMessage(id, content string) internalevents.Event {
	return internalevents.Event{
		ID: id, Type: string(pkgevents.UserMessage), Timestamp: time.Now(),
		Data: pkgevents.NewAgentEvent(pkgevents.NewUserMessageEvent(0, content, "user")),
	}
}

func newTestChatSubmissionStore() *chatSubmissionStore {
	var mu sync.Mutex
	files := map[string]string{}
	return &chatSubmissionStore{
		resolveProject: func(owner, session string) (string, error) { return "", nil },
		read: func(_ context.Context, p string) (string, bool, error) {
			mu.Lock()
			defer mu.Unlock()
			v, ok := files[p]
			return v, ok, nil
		},
		write: func(_ context.Context, p, v string) error { mu.Lock(); defer mu.Unlock(); files[p] = v; return nil },
	}
}

func newCrewCreationTestEnv(t *testing.T) (*ProjectActionDispatcher, *mockWorkspaceAPI, context.Context) {
	t.Helper()
	return newCrewCreationTestEnvWithOptions(t, nil)
}

func registerWorkCrewProfile(t *testing.T, registry *agentprofiles.Registry) {
	t.Helper()
	if err := registry.RegisterProfile(workCrewTestProfile(nil)); err != nil {
		t.Fatal(err)
	}
}

func workCrewTestProfile(providerOptions []agentprofiles.ProviderOption) agentprofiles.Profile {
	return agentprofiles.Profile{
		ID: "work", Name: "Work", Version: 2, SystemPromptTemplate: "hi", BuiltIn: true, Product: "work",
		Runtime: agentprofiles.RuntimePolicy{
			Transport:       "auto",
			Conversation:    agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace:       agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: "Chats/Work/projects"},
			ProviderOptions: providerOptions,
		},
		Features: []agentprofiles.FeatureBinding{{ID: "triggers"}},
		UIPanels: agentprofiles.UIPanels{Schedules: true},
	}
}

func newCrewCreationTestEnvWithOptions(t *testing.T, providerOptions []agentprofiles.ProviderOption) (*ProjectActionDispatcher, *mockWorkspaceAPI, context.Context) {
	t.Helper()
	ensureProjectStateRoot(t)
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true}]}`)
	registry := agentprofiles.NewRegistry()
	profile := workCrewTestProfile(providerOptions)
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	svc := NewProjectActionDispatcher(nil, registry)
	// Hermetic availability fixtures: github is connected, gitlab is
	// configured but disconnected, and SHARED_GH is a Vault secret.
	svc.crewAvailability = &crewCreationAvailability{
		MCPServer: func(name string) (string, bool, error) {
			switch name {
			case "github":
				return "github", true, nil
			case "gitlab":
				return "gitlab", false, nil
			default:
				return "", false, fmt.Errorf("MCP server %q is not configured; use list_mcp_servers or search_mcp_catalog first", name)
			}
		},
		GlobalSecrets: func() map[string]bool {
			return map[string]bool{"SHARED_GH": true}
		},
	}
	manifest := NewWorkflowManifest("Builder pipeline")
	manifest.CreatedBy = "owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{manifestPath("Workflow/build"): string(manifestRaw)}}
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	return svc, mock, ctx
}

func startRegisteredTestTmuxSession(t *testing.T, sessionID, tmuxSession string) *mcpagent.Session {
	t.Helper()
	agent := testAgentWithHandle(sessionID, llmtypes.CodingProviderSessionHandle{
		Provider:    string(llm.ProviderClaudeCode),
		Transport:   llmtypes.CodingProviderTransportTmux,
		TmuxSession: tmuxSession,
		Status:      llmtypes.CodingProviderSessionStatusIdle,
	})
	session, err := agent.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		_ = agent.Close()
	})
	return session
}

func testCodingAgent(provider llm.Provider, model string) *mcpagent.Agent {
	agent := &mcpagent.Agent{}
	mcpagent.ApplyAgentResumeHandle(agent, &mcpagent.AgentSessionHandle{
		Provider: llmtypes.CodingProviderSessionHandle{Provider: string(provider), Model: model},
	})
	return agent
}
