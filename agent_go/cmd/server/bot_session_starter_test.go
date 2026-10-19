package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	pkgevents "github.com/manishiitg/mcpagent/events"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

func TestApplyBotRouteClaimsStampsTheTurnPrincipal(t *testing.T) {
	// WhatsApp turns run as the paired owner: no channel grant exists, so
	// they carry their own principal rather than bot_route (which would
	// demand Slack channel bindings at revalidation time).
	owner := &UserClaims{UserID: "owner-1", Username: "owner"}
	applyBotRouteClaims(owner, map[string]interface{}{"bot_platform": "whatsapp"})
	if owner.Provider != "bot_owner" || owner.UserID != "owner-1" {
		t.Fatalf("whatsapp claims = %+v, want the paired owner stamped bot_owner", owner)
	}

	// Slack keeps the grant principal, and only when a grant is present.
	slack := &UserClaims{UserID: "bot"}
	applyBotRouteClaims(slack, map[string]interface{}{"bot_platform": "slack", "bot_route_grant": "run", "preset_query_id": "wf"})
	if slack.Provider != "bot_route" || slack.BotRouteGrant != "run" || slack.BotRouteWorkflowID != "wf" {
		t.Fatalf("slack claims = %+v, want the bot_route grant principal", slack)
	}
	ungranted := &UserClaims{UserID: "bot"}
	applyBotRouteClaims(ungranted, map[string]interface{}{"bot_platform": "slack"})
	if ungranted.Provider != "" {
		t.Fatalf("ungranted slack claims = %+v, want untouched", ungranted)
	}

	// Anything else (and nil inputs) passes through untouched.
	plain := &UserClaims{UserID: "owner-1"}
	applyBotRouteClaims(plain, map[string]interface{}{})
	if plain.Provider != "" {
		t.Fatalf("non-bot claims = %+v, want untouched", plain)
	}
	applyBotRouteClaims(nil, map[string]interface{}{"bot_platform": "whatsapp"})
	applyBotRouteClaims(&UserClaims{}, nil)
}

func TestFinalResponseForExecutionUsesTheExactTurn(t *testing.T) {
	store := internalevents.NewEventStore(10)
	api := &StreamingAPI{eventStore: store}
	for _, item := range []struct {
		executionID string
		response    string
	}{{"turn-1", "first answer"}, {"turn-2", "second answer"}} {
		completion := pkgevents.NewUnifiedCompletionEvent("coding_agent", "retained", "", item.response, "completed", time.Second, 1)
		store.AddEvent("shared-session", internalevents.Event{
			ID: item.executionID, Type: "unified_completion", SessionID: "shared-session", ExecutionID: item.executionID,
			Data: &pkgevents.AgentEvent{Type: pkgevents.EventType("unified_completion"), Data: completion},
		})
	}
	if got := api.finalResponseForExecution("shared-session", "turn-1"); got != "first answer" {
		t.Fatalf("final response = %q, want exact first turn", got)
	}
}

func TestFinalResponseForExecutionFallsBackToGenerationEnd(t *testing.T) {
	store := internalevents.NewEventStore(10)
	api := &StreamingAPI{eventStore: store}
	generated := pkgevents.NewLLMGenerationEndEvent(1, "the summary", 0, time.Second, pkgevents.UsageMetrics{})
	store.AddEvent("crew-session", internalevents.Event{
		ID: "turn-9", Type: "llm_generation_end", SessionID: "crew-session", ExecutionID: "turn-9",
		Data: &pkgevents.AgentEvent{Type: pkgevents.EventType("llm_generation_end"), Data: generated},
	})
	if got := api.finalResponseForExecution("crew-session", "turn-9"); got != "the summary" {
		t.Fatalf("final response = %q, want generation content", got)
	}
	if got := api.finalResponseForExecution("crew-session", "other-turn"); got != "" {
		t.Fatalf("final response = %q, want empty for another execution", got)
	}
}

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

func TestFinalResponseForExecutionPrefersUnifiedCompletion(t *testing.T) {
	store := internalevents.NewEventStore(10)
	api := &StreamingAPI{eventStore: store}
	generated := pkgevents.NewLLMGenerationEndEvent(1, "older summary", 0, time.Second, pkgevents.UsageMetrics{})
	store.AddEvent("s", internalevents.Event{
		ID: "turn-1", Type: "llm_generation_end", SessionID: "s", ExecutionID: "turn-1",
		Data: &pkgevents.AgentEvent{Type: pkgevents.EventType("llm_generation_end"), Data: generated},
	})
	completion := pkgevents.NewUnifiedCompletionEvent("coding_agent", "retained", "", "exact answer", "completed", time.Second, 1)
	store.AddEvent("s", internalevents.Event{
		ID: "turn-1", Type: "unified_completion", SessionID: "s", ExecutionID: "turn-1",
		Data: &pkgevents.AgentEvent{Type: pkgevents.EventType("unified_completion"), Data: completion},
	})
	if got := api.finalResponseForExecution("s", "turn-1"); got != "exact answer" {
		t.Fatalf("final response = %q, want unified completion", got)
	}
}

func TestEndEventTextReadsGenericEndPayload(t *testing.T) {
	// Persisted/reloaded payloads lose their Go type and arrive as a map.
	if got := endEventText(map[string]interface{}{"content": "reloaded summary"}); got != "reloaded summary" {
		t.Fatalf("generic content = %q", got)
	}
	if got := endEventText(map[string]interface{}{"final_result": "reloaded answer"}); got != "reloaded answer" {
		t.Fatalf("generic final_result = %q", got)
	}
	if got := endEventText(map[string]interface{}{"final_result": "exact", "content": "other"}); got != "exact" {
		t.Fatalf("generic precedence = %q, want final_result", got)
	}
	if got := endEventText(map[string]interface{}{"turns": 1}); got != "" {
		t.Fatalf("textless generic = %q, want empty", got)
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

func TestNormalizeSlackChannelRoutingDefaultsNewRoutesToRun(t *testing.T) {
	routes, err := normalizeSlackChannelRouting(map[string]ChannelRoute{
		" c1234567890 ": {
			WorkflowID:    " workflow-1 ",
			WorkspacePath: " Workflow/demo ",
		},
	})
	if err != nil {
		t.Fatalf("normalizeSlackChannelRouting: %v", err)
	}
	route, ok := routes["C1234567890"]
	if !ok {
		t.Fatalf("normalized route missing: %+v", routes)
	}
	if route.BotGrant != "run" || route.WorkshopMode != "run" {
		t.Fatalf("new Slack route mode = grant %q mode %q, want run/run", route.BotGrant, route.WorkshopMode)
	}
	if !route.SendFullDetails {
		t.Fatal("Slack route should keep full details enabled")
	}
}

// P0 integration: the bot's follow-up crosses handleQuery and reaches retained
// Muse delivery while the conversation is running. The terminal delivery
// boundary is stubbed; no real user chat is messaged.
func TestBotFollowUpReachesRetainedMuseThroughQueryP0(t *testing.T) {
	workspace, _ := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	t.Setenv("TRACING_PROVIDER", "noop")
	const sessionID = "bot-steer-query-p0"
	const userID = "bot-steer-owner"
	store := internalevents.NewEventStore(20)
	defer store.Stop()
	terminalStore := terminals.NewStore()
	terminalStore.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(time.Now(), sessionID, "main:"+sessionID, "mlp-muse-cli-int-bot-steer"))
	deliveries := 0
	api := &StreamingAPI{
		eventStore: store, terminalStore: terminalStore,
		activeSessions: map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, UserID: userID, Status: "running"}},
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
	if err := api.sendFollowUpInternal(ctx, map[string]interface{}{"query": "use Notion instead", "agent_mode": "workflow_phase", "phase_id": "workflow-builder"}, sessionID, userID); err != nil {
		t.Fatal(err)
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

// A shared-bot channel route to a crew may hold the crew's logical path while
// the crew's turns run in the owner's physical folder; both checks accept it.
func TestSlackCrewRouteAcceptsLogicalAndPhysicalFolder(t *testing.T) {
	route := services.ChannelRoute{ProfileID: "work", ConversationKey: "sde", WorkspacePath: "Chats/Work/projects/sde", WorkspaceUserID: "owner-1"}
	if !slackRouteFolderMatches(route, "_users/owner-1/Chats/Work/projects/sde") {
		t.Fatal("logical crew route rejected the crew's own turn")
	}
	if slackRouteFolderMatches(route, "_users/owner-2/Chats/Work/projects/sde") {
		t.Fatal("crew route matched another owner's folder")
	}
	claims := &UserClaims{UserID: "owner-1", Provider: "bot_route", BotRouteProfileID: "work", BotRouteConversationKey: "sde", BotRouteWorkspacePath: "Chats/Work/projects/sde", BotRouteGrant: "run"}
	req := QueryRequest{AgentProfileID: "work", AgentProfileConversationKey: "sde", SelectedFolder: "_users/owner-1/Chats/Work/projects/sde"}
	if level, scoped := botRouteProfileAccessForRequest(claims, req); !scoped || level != WorkflowAccessRead {
		t.Fatalf("crew route access = %q scoped=%v, want read", level, scoped)
	}
	req.SelectedFolder = "_users/owner-2/Chats/Work/projects/sde"
	if level, _ := botRouteProfileAccessForRequest(claims, req); level != WorkflowAccessNone {
		t.Fatalf("crew route reached another owner's folder: %q", level)
	}
}

// A workflow Crew step saved only the journal's 16 KB preview of a long answer
// as its output file (a cut-off JSON object); the run result must be whole.
func TestFinalResponseForExecutionReadsAnOversizedAnswerWhole(t *testing.T) {
	journal, err := internalevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	store := internalevents.NewEventStore(10)
	store.SetDurableJournal(journal)
	api := &StreamingAPI{eventStore: store}
	answer := `{"items":"` + strings.Repeat("x", 80*1024) + `"}`
	completion := pkgevents.NewUnifiedCompletionEvent("coding_agent", "retained", "", answer, "completed", time.Second, 1)
	store.AddEvent("crew-session", internalevents.Event{
		ID: "big-turn", Type: "unified_completion", SessionID: "crew-session", ExecutionID: "big-turn",
		Data: &pkgevents.AgentEvent{Type: pkgevents.EventType("unified_completion"), Data: completion},
	})
	if got := api.finalResponseForExecution("crew-session", "big-turn"); got != answer {
		t.Fatalf("final response is %d bytes, want the whole %d-byte answer", len(got), len(answer))
	}
}
