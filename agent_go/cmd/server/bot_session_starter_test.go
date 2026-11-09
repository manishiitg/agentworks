package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"

	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	pkgevents "github.com/manishiitg/mcpagent/events"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

func TestFinalResponseForExecutionMatchesMainAgentScope(t *testing.T) {
	store := internalevents.NewEventStore(10)
	api := &StreamingAPI{eventStore: store}
	// Main-agent turns are scoped main:<session>, not the query ID.
	generated := pkgevents.NewLLMGenerationEndEvent(1, "scoped summary", 0, time.Second, pkgevents.UsageMetrics{})
	store.AddEvent("s", internalevents.Event{
		ID: "e1", Type: "llm_generation_end", SessionID: "s", ExecutionID: "main:s",
		Data: &pkgevents.AgentEvent{Type: pkgevents.EventType("llm_generation_end"), Data: generated},
	})
	if got := api.finalResponseForExecution("s", "query-1"); got != "scoped summary" {
		t.Fatalf("final response = %q, want main-scope generation content", got)
	}
}

func TestInternalBotRequestContextUsesThePairedAccountsLiveRole(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
	  {"id":"owner-1","username":"owner","admin":true,"can_create":true,"products":[]},
	  {"id":"reader-1","username":"reader","can_create":false,"products":["agentworks"]}
	]}`)

	tests := []struct {
		userID   string
		username string
		access   WorkflowAccessLevel
	}{
		{userID: "owner-1", username: "owner", access: WorkflowAccessOwner},
		{userID: "reader-1", username: "reader", access: WorkflowAccessRead},
	}
	for _, test := range tests {
		ctx := internalBotRequestContext(context.Background(), test.userID)
		claims := GetUserFromContext(ctx)
		if claims == nil || claims.UserID != test.userID || claims.Username != test.username {
			t.Fatalf("bot claims for %s = %+v", test.userID, claims)
		}
		if got := workflowAccessForClaims(claims); got != test.access {
			t.Fatalf("bot access for %s = %s, want %s", test.userID, got, test.access)
		}
	}
}

func TestInternalBotRequestContextPreservesExistingAuthenticatedClaims(t *testing.T) {
	existing := &UserClaims{UserID: "existing", Username: "signed-in-user"}
	ctx := context.WithValue(context.Background(), UserContextKey, existing)
	got := GetUserFromContext(internalBotRequestContext(ctx, "different"))
	if got != existing {
		t.Fatalf("existing authenticated claims were replaced: %+v", got)
	}
}

func TestValidateSlackRouteMutationRequiresOwnerForGrantDowngrade(t *testing.T) {
	current := map[string]ChannelRoute{
		"C1234567890": {
			ProfileID:       "work",
			ConversationKey: "acme",
			WorkspacePath:   "_users/owner-1/Chats/Work/projects/acme",
			WorkspaceUserID: "owner-1",
			BotGrant:        "owner",
		},
	}
	next := map[string]ChannelRoute{
		"C1234567890": {
			ProfileID:       "work",
			ConversationKey: "acme",
			WorkspacePath:   "_users/owner-1/Chats/Work/projects/acme",
			BotGrant:        "run",
		},
	}

	if err := validateSlackRouteMutationPermissions(context.Background(), nil, next, current); err == nil {
		t.Fatal("owner -> run grant change did not require destination owner validation")
	}
}

func TestValidateSlackRouteMutationPreservesProfileWorkspaceOwner(t *testing.T) {
	current := map[string]ChannelRoute{
		"C1234567890": {
			ProfileID:       "work",
			ConversationKey: "acme",
			WorkspacePath:   "_users/owner-1/Chats/Work/projects/acme",
			WorkspaceUserID: "owner-1",
			BotGrant:        "owner",
		},
	}
	next := map[string]ChannelRoute{
		"C1234567890": {
			ProfileID:       "work",
			ConversationKey: "acme",
			WorkspacePath:   "_users/owner-1/Chats/Work/projects/acme",
			BotGrant:        "owner",
		},
	}

	if err := validateSlackRouteMutationPermissions(context.Background(), nil, next, current); err != nil {
		t.Fatalf("unchanged route required owner validation: %v", err)
	}
	if got := next["C1234567890"].WorkspaceUserID; got != "owner-1" {
		t.Fatalf("workspace owner = %q, want owner-1", got)
	}
}

// P0 integration: the bot's follow-up crosses handleQuery and reaches retained
// Muse delivery while the conversation is running. The terminal delivery
// boundary is stubbed; no real user chat is messaged.
func TestBotFollowUpReachesRetainedMuseThroughQueryP0(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("TRACING_PROVIDER", "noop")
	const sessionID = "bot-steer-query-p0"
	const userID = "owner"
	store := internalevents.NewEventStore(20)
	defer store.Stop()
	terminalStore := terminals.NewStore()
	terminalStore.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(time.Now(), sessionID, "main:"+sessionID, "mlp-muse-cli-int-bot-steer"))
	deliveries := 0
	api := &StreamingAPI{
		agentProfiles: f.api.agentProfiles, provider: "muse-cli",
		eventStore: store, terminalStore: terminalStore,
		activeSessions: map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, UserID: userID, Status: "running", WorkspacePath: externalCommandCrewRoot}},
		internalRetainedTerminalInputHandler: func(_ context.Context, provider llmproviders.Provider, _, owner, message string) error {
			if provider != llmproviders.ProviderMuseCLI || owner != sessionID || message != "use Notion instead" {
				t.Fatalf("wrong steer: provider=%s owner=%s message=%q", provider, owner, message)
			}
			deliveries++
			return nil
		},
	}
	t.Cleanup(func() {
		api.retainedMainTurnsMu.Lock()
		cancelWatch := api.retainedMainTurnWatchCancels[sessionID]
		api.retainedMainTurnsMu.Unlock()
		if cancelWatch != nil {
			cancelWatch()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Model the same admitted Crew definition that launched the retained turn.
	// A profileless general chat is retired; bots keep the Crew's real access checks.
	query := QueryRequest{AgentMode: "multi-agent", AgentProfileID: "work", AgentProfileConversationKey: "team", AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Test Team"}, SelectedFolder: externalCommandCrewRoot, Provider: "muse-cli", ModelID: "muse-spark-1.3"}
	profile, _, admitErr := api.admitQueryTarget(internalBotRequestContext(ctx, userID), &query, userID, sessionID)
	if admitErr != nil {
		t.Fatal(admitErr)
	}
	api.launchedAgentProfileKeyBySession = map[string]string{sessionID: agentProfileSessionKey(profile)}
	api.lastQueryRequests = map[string]QueryRequest{sessionID: query}
	request := map[string]interface{}{
		"query": "use Notion instead", "agent_mode": "multi-agent", "agent_profile_id": "work",
		"agent_profile_conversation_key": "team", "selected_folder": externalCommandCrewRoot, "provider": "muse-cli", "model_id": "muse-spark-1.3",
		"agent_profile_context": map[string]interface{}{"project_title": "Test Team"},
	}
	if err := api.sendFollowUpInternal(ctx, request, sessionID, userID); err != nil {
		if deliveries != 0 {
			t.Fatal(err)
		}
		// The channel error deliberately hides bodies. Diagnose only this synthetic
		// refused request, before any stub delivery; never widen production errors.
		body, _ := json.Marshal(request)
		r := httptest.NewRequest("POST", "/api/query", strings.NewReader(string(body)))
		r = r.WithContext(internalBotRequestContext(ctx, userID, request))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Session-ID", sessionID)
		r.Header.Set("X-User-ID", userID)
		response := httptest.NewRecorder()
		api.handleQuery(response, r)
		t.Fatalf("%v; synthetic refusal HTTP=%d body=%s", err, response.Code, response.Body.String())
	}
	if deliveries != 1 {
		t.Fatalf("retained tmux deliveries=%d, want 1", deliveries)
	}
}

func TestBotWorkflowAccessClaimsKeepWhatsAppUserAndSlackRouteSemantics(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
	  {"id":"paired-owner","username":"owner","can_edit":true,"products":["agentworks"]},
	  {"id":"paired-reader","username":"reader","products":["agentworks"]},
	  {"id":"unshared-user","username":"other","products":["agentworks"]}
	]}`)
	route := services.ChannelRoute{WorkflowID: "wf", WorkspacePath: "Workflow/wf", BotGrant: "run"}
	manifest := &WorkflowManifest{ID: "wf", Access: &WorkflowAccess{Owners: []string{"paired-owner"}, Readers: []string{"paired-reader"}}}

	reader := botWorkflowAccessClaims("paired-reader", "reader@example.com", route)
	if reader.Provider == "bot_route" || workflowAccessForManifest(reader, manifest) != WorkflowAccessRead {
		t.Fatalf("WhatsApp reader claims bypassed manifest: %+v", reader)
	}
	outsider := botWorkflowAccessClaims("unshared-user", "other@example.com", route)
	if workflowAccessForManifest(outsider, manifest) != WorkflowAccessNone {
		t.Fatal("unshared WhatsApp user received workflow access")
	}
	slack := botWorkflowAccessClaims(services.BotPrincipalIDForRoute("slack", route), "", route)
	if slack.Provider != "bot_route" || workflowAccessForManifest(slack, manifest) != WorkflowAccessRead {
		t.Fatalf("Slack route grant changed: %+v", slack)
	}
}
