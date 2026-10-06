package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
)

func TestAccessTokenRunToolDenylistAppliesAtExecution(t *testing.T) {
	claims := &UserClaims{UserID: "alice", AccessToken: &accesstokens.Token{ID: "token-1", Scopes: []string{"runs:execute"}, AllWorkflows: true}}
	resolve := (&StreamingAPI{}).bindToolExecutionContext(
		context.WithValue(context.Background(), UserContextKey, claims),
		"pat-token-1-session", QueryRequest{}, true,
	)
	if _, err := resolve(context.Background(), "google_workspace_cli"); err == nil {
		t.Fatal("token-backed assistant can invoke a tool hidden from the external catalog")
	}
	child := (&StreamingAPI{}).bindToolExecutionContextForSession(
		context.WithValue(context.Background(), UserContextKey, claims),
		"pat-token-1-session", "delegated-child", QueryRequest{}, true,
	)
	if _, err := child(context.Background(), "google_workspace_cli"); err == nil {
		t.Fatal("token-backed delegated agent can invoke a denylisted tool")
	}
	if accessTokenRunToolDenied(&UserClaims{UserID: "alice"}, "google_workspace_cli") {
		t.Fatal("ordinary Run-mode sessions lost the Google tool")
	}
}

func TestToolExecutionContextUsesAuthenticatedQueryForAllTransports(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner("builder", "alice")
	claims := &UserClaims{UserID: "alice", Email: "alice@example.com", Provider: "local"}
	requestCtx := context.WithValue(context.Background(), UserContextKey, claims)
	resolve := api.bindToolExecutionContext(requestCtx, "builder", QueryRequest{}, false)
	// Mutating the original request must not alter the definition's binding.
	claims.UserID = "bob"
	for _, incoming := range []context.Context{context.Background(), executor.WithSessionID(context.Background(), "builder")} {
		ctx, err := resolve(incoming, "any_future_tool")
		if err != nil {
			t.Fatal(err)
		}
		got := GetUserFromContext(ctx)
		if got.UserID != "alice" || got.Email != "alice@example.com" || got.Provider != "local" || executor.SessionIDFromContext(ctx) != "builder" || ctx.Value(common.UserIDKey) != "alice" {
			t.Fatalf("lost bound identity: %+v", got)
		}
		got.UserID = "changed-by-handler"
	}
	for _, incoming := range []context.Context{
		executor.WithSessionID(context.Background(), "other-session"),
		context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "bob"}),
		context.WithValue(context.Background(), common.UserIDKey, "bob"),
		context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice", Provider: "bot_route"}),
	} {
		if _, err := resolve(incoming, "tool"); err == nil {
			t.Fatal("conflicting caller identity was accepted")
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	ctx, err := resolve(canceled, "tool")
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != context.Canceled {
		t.Fatal("context binding discarded cancellation")
	}
	api.eventStore.SetSessionOwner("builder", "bob")
	if _, err := resolve(context.Background(), "tool"); err == nil {
		t.Fatal("session ownership change was ignored")
	}
}

func TestToolExecutionContextAdmitsRegisteredWorkflowChildSession(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	// Unique per run: CloseHTTPSession marks the child stopped for good
	// (zombie prevention), so the topology matrix re-running this test with
	// fixed IDs found the child already stopped and rejected it.
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	parentSession := "schedule-cron--daily-tool-context-" + suffix
	childSession := "session-group-default-tool-context-" + suffix
	registry := mcpclient.GetSessionRegistry()
	registry.RegisterHTTPSession(parentSession, childSession)
	t.Cleanup(func() { registry.CloseHTTPSession(parentSession) })

	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner(parentSession, "alice")
	requestCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice", Provider: "local"})
	resolve := api.bindToolExecutionContext(requestCtx, parentSession, QueryRequest{}, false)

	incoming := context.WithValue(context.Background(), common.WorkflowSessionIDKey, "forged-controller")
	ctx, err := resolve(executor.WithSessionID(incoming, childSession), "agent_browser")
	if err != nil {
		t.Fatalf("registered workflow child was rejected: %v", err)
	}
	if got := executor.SessionIDFromContext(ctx); got != parentSession {
		t.Fatalf("bound execution session = %q, want authenticated parent %q", got, parentSession)
	}
	if got := ctx.Value(common.WorkflowSessionIDKey); got != parentSession {
		t.Fatalf("child browser controller = %v, want authenticated parent %q", got, parentSession)
	}
	if got := GetUserFromContext(ctx); got == nil || got.UserID != "alice" {
		t.Fatalf("registered child lost owner identity: %+v", got)
	}

	if _, err := resolve(executor.WithSessionID(context.Background(), childSession+"-forged"), "agent_browser"); err == nil {
		t.Fatal("unregistered lookalike child session was accepted")
	}
}

func TestToolExecutionContextNeverInventsLocalOwner(t *testing.T) {
	api := &StreamingAPI{}
	for _, claims := range []*UserClaims{nil, {}} {
		ctx := context.Background()
		if claims != nil {
			ctx = context.WithValue(ctx, UserContextKey, claims)
		}
		resolve := api.bindToolExecutionContext(ctx, "builder", QueryRequest{}, false)
		if _, err := resolve(context.Background(), "tool"); err == nil {
			t.Fatal("missing authentication fell back to local owner")
		}
	}
}

func TestToolExecutionContextRetainsBotIdentityAndRevalidatesRoute(t *testing.T) {
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{chatStore: store}
	route := ChannelRoute{ProfileID: "work", ConversationKey: "acme", WorkspacePath: "Chats/Work/projects/acme", WorkspaceUserID: "alice", BotGrant: "run"}
	encoded, _ := json.Marshal(map[string]ChannelRoute{"C123": route})
	save := func(routes string) {
		if _, err := store.UpsertBotConnectorConfig(context.Background(), &chathistory.CreateBotConnectorConfigRequest{ID: "slack", Enabled: true, BotMode: true, AllowedChannels: routes}); err != nil {
			t.Fatal(err)
		}
	}
	save(string(encoded))
	req := QueryRequest{AgentProfileID: "work", AgentProfileConversationKey: "acme", SelectedFolder: route.WorkspacePath, BotPlatform: "slack", BotChannelID: "C123"}
	claims := botRouteUserClaims("bot", route)
	requestCtx, err := api.revalidateExecutionPrincipal(context.WithValue(context.Background(), UserContextKey, claims), req)
	if err != nil {
		t.Fatal(err)
	}
	resolve := api.bindToolExecutionContext(requestCtx, "bot-session", req, true)
	incoming := executor.WithSessionID(context.Background(), "bot-session")
	ctx, err := resolve(incoming, "read_tool")
	if err != nil {
		t.Fatal(err)
	}
	got := GetUserFromContext(ctx)
	if got.Provider != "bot_route" || got.BotRouteGrant != "run" || got.ExecutionPrincipal == nil || got.ExecutionPrincipal.Access != WorkflowAccessRead || userAccessForClaims(got).Admin {
		t.Fatalf("bot identity widened: %+v", got)
	}
	save("{}")
	if _, err := resolve(incoming, "read_tool"); err == nil {
		t.Fatal("revoked route was ignored")
	}
}

func TestToolExecutionContextAdmitsTheWhatsAppOwnerPrincipal(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{
		"wa-product": {SessionID: "wa-product", BotPlatform: "whatsapp", TriggeredBy: "bot:whatsapp"},
	}}
	req := QueryRequest{BotPlatform: "whatsapp", TriggeredBy: "bot:whatsapp"}
	incoming := executor.WithSessionID(context.Background(), "wa-product")

	// The paired owner's own turn executes tools in its bot-marked session.
	owner := api.bindToolExecutionContext(
		context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Provider: "bot_owner"}),
		"wa-product", req, false)
	if _, err := owner(incoming, "read_image"); err != nil {
		t.Fatalf("bot_owner tools rejected: %v", err)
	}

	// Anything else bound on a bot-marked session is still an origin change.
	plain := api.bindToolExecutionContext(
		context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Provider: "local"}),
		"wa-product", req, false)
	if _, err := plain(incoming, "read_image"); err == nil {
		t.Fatal("non-bot principal executed tools in a bot-marked session")
	}
}

// TestToolExecutionContextTopologyMatrix is the CI contract for every place a
// tool call can originate. Keep the topology names explicit: adding a new
// execution origin, transport, or privileged tool surface must add a row here
// rather than relying on an interactive-chat test to stand in for it.
func TestToolExecutionContextTopologyMatrix(t *testing.T) {
	t.Run("interactive owner and conflicting reader/direct tool invocation", TestToolExecutionContextUsesAuthenticatedQueryForAllTransports)
	t.Run("Brain executor reused across authenticated sessions", TestKnowledgebaseToolExecutionRejectsForeignIdentity)
	t.Run("cron manual API and internal triggers/workflow owner/action surfaces", func(t *testing.T) {
		t.Setenv("MULTI_USER_MODE", "false")
		t.Setenv("DEFAULT_USER_ID", "legacy-owner")
		origins := []struct {
			name    string
			session string
			tool    string
			owner   string
			caller  func(string) context.Context
		}{
			{"cron schedule", "schedule-cron--daily", "agent_browser", "owner", func(session string) context.Context { return executor.WithSessionID(context.Background(), session) }},
			{"manual schedule", "schedule-manual--daily", "execute_shell_command", "owner", func(session string) context.Context { return executor.WithSessionID(context.Background(), session) }},
			{"API trigger", "schedule-api--incoming", "diff_patch_workspace_file", "owner", func(session string) context.Context {
				return context.WithValue(context.Background(), common.ChatSessionIDKey, session)
			}},
			{"internal trigger", "schedule-internal--incoming", "get_workflow_config", "owner", func(session string) context.Context {
				return context.WithValue(context.Background(), common.ChatSessionIDKey, session)
			}},
			{"legacy single-user schedule", "schedule-cron--legacy", "get_workflow_config", workflowExecutionOwnerUserID(&WorkflowManifest{ID: "legacy"}), func(session string) context.Context { return executor.WithSessionID(context.Background(), session) }},
		}
		for _, origin := range origins {
			t.Run(origin.name, func(t *testing.T) {
				api := &StreamingAPI{eventStore: events.NewEventStore(10)}
				api.eventStore.SetSessionOwner(origin.session, origin.owner)
				requestCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: origin.owner, Provider: "local"})
				ctx, err := api.bindToolExecutionContext(requestCtx, origin.session, QueryRequest{}, false)(origin.caller(origin.session), origin.tool)
				if err != nil {
					t.Fatalf("%s rejected: %v", origin.tool, err)
				}
				if got := GetUserFromContext(ctx); got == nil || got.UserID != origin.owner {
					t.Fatalf("bound claims = %+v, want owner %q", got, origin.owner)
				}
			})
		}
	})
	t.Run("registered and forged workflow children/MCP custom HTTP invocation", TestToolExecutionContextAdmitsRegisteredWorkflowChildSession)
	t.Run("real MCP stdio bridge and HTTP custom-tool transport", TestWorkflowStepDatabaseToolsThroughMCPBridge)
	t.Run("interactive reader remains read-only", func(t *testing.T) {
		t.Setenv("MULTI_USER_MODE", "true")
		withMemoryUserDirectory(t, `{"users":[{"id":"reader","username":"reader","can_create":false}]}`)
		requestCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "reader", Username: "reader", Provider: "local"})
		api := &StreamingAPI{}
		read := api.bindToolExecutionContext(requestCtx, "reader-session", QueryRequest{}, true)
		if _, err := read(executor.WithSessionID(context.Background(), "reader-session"), "get_workflow_config"); err != nil {
			t.Fatalf("reader read tool rejected: %v", err)
		}
		write := api.bindToolExecutionContext(requestCtx, "reader-session", QueryRequest{}, false)
		if _, err := write(executor.WithSessionID(context.Background(), "reader-session"), "execute_shell_command"); err == nil {
			t.Fatal("reader was widened to builder authority")
		}
	})
	t.Run("Slack Run principal/read-only custom tool", TestToolExecutionContextRetainsBotIdentityAndRevalidatesRoute)
	t.Run("WhatsApp paired owner", TestToolExecutionContextAdmitsTheWhatsAppOwnerPrincipal)
	t.Run("missing authenticated principal fails closed", TestToolExecutionContextNeverInventsLocalOwner)
	t.Run("delegated and background agent isolated session", func(t *testing.T) {
		t.Setenv("MULTI_USER_MODE", "false")
		api := &StreamingAPI{eventStore: events.NewEventStore(10)}
		api.eventStore.SetSessionOwner("parent", "owner")
		requestCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Provider: "local"})
		resolve := api.bindToolExecutionContextForSession(requestCtx, "parent", "delegated-isolated", QueryRequest{}, false)
		ctx, err := resolve(executor.WithSessionID(context.Background(), "delegated-isolated"), "execute_shell_command")
		if err != nil {
			t.Fatalf("delegated action tool rejected: %v", err)
		}
		if got := executor.SessionIDFromContext(ctx); got != "delegated-isolated" {
			t.Fatalf("delegated execution session = %q", got)
		}
		if got := GetUserFromContext(ctx); got == nil || got.UserID != "owner" {
			t.Fatalf("delegated claims = %+v", got)
		}
		api.eventStore.SetSessionOwner("parent", "different-owner")
		if _, err := resolve(executor.WithSessionID(context.Background(), "delegated-isolated"), "agent_browser"); err == nil {
			t.Fatal("background child ignored parent ownership revocation")
		}
	})
}

// One person, one chat: the owner's Slack DM or WhatsApp message continues
// the chat a web turn started, in the same warm CLI whose tools were bound to
// that web turn. Those tools keep working for the owner's own bot turn; a
// shared channel route's turn (or an unknown principal) is still refused.
func TestToolExecutionContextOwnersDMContinuesWebBoundTools(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	incoming := executor.WithSessionID(context.Background(), "crew-main")
	webBound := func(api *StreamingAPI) func(context.Context, string) (context.Context, error) {
		return api.bindToolExecutionContext(
			context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Provider: "local"}),
			"crew-main", QueryRequest{}, false)
	}
	for _, tc := range []struct {
		name, platform, turnProvider string
		allowed                      bool
	}{
		{"owner's Slack DM", "slack", slackDMProvider, true},
		{"owner's WhatsApp", "whatsapp", "bot_owner", true},
		{"shared channel route", "slack", "bot_route", false},
		{"unknown principal", "slack", "", false},
	} {
		api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{
			"crew-main": {SessionID: "crew-main", UserID: "owner", BotPlatform: tc.platform, TriggeredBy: "bot:" + tc.platform, TurnProvider: tc.turnProvider},
		}}
		_, err := webBound(api)(incoming, "read_image")
		if tc.allowed && err != nil {
			t.Errorf("%s: tools refused: %v", tc.name, err)
		}
		if !tc.allowed && err == nil {
			t.Errorf("%s: tools ran on a person's web-bound session", tc.name)
		}
	}
}

func TestExternalBuilderToolBindingRejectsUnboundOperation(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AGENTWORKS_STATE_DIR", t.TempDir())
	claims := &UserClaims{UserID: "alice", ExternalBuilderOperationID: "missing-operation"}
	ctx := context.WithValue(context.Background(), UserContextKey, claims)
	api := &StreamingAPI{}
	for _, child := range []string{"main-chat", "delegated-child"} {
		bind := api.bindToolExecutionContextForSession(ctx, "main-chat", child, QueryRequest{SelectedFolder: "Workflow/test", ExternalBuilderOperationID: "missing-operation"}, false)
		if _, err := bind(context.Background(), "read_file"); err == nil {
			t.Fatal("missing operation executed tool", child)
		}
	}
}

// Exercise the real KB executor without the shared context wrapper as well as
// through it: a mixed-up tool table must never invent its registration identity.
func TestKnowledgebaseToolExecutionRejectsForeignIdentity(t *testing.T) {
	service, owner, workspace, folderID := knowledgeIntegrationFixture(t)
	project, err := knowledgeProjectLoad(t.Context(), "admin", workspace, true)
	if err != nil {
		t.Fatal(err)
	}
	owner.AccessOnly = true
	knowledgeDispatchTest(t, service, owner, "brain_access", map[string]any{"action": "bind_project", "workspace_path": workspace, "folder_id": folderID, "alias": "kbtest", "access": "read", "expected_manifest_version": project.Version, "request_id": "identity-bind"})
	owner.AccessOnly = false
	_, err = service.CallTool(t.Context(), owner, "brain_update", map[string]any{"action": "create", "folder_id": folderID, "filename": "identity.md", "type": "note", "title": "Identity", "content": "identity-bound-marker", "request_id": "identity-create"})
	if err != nil {
		t.Fatal(err)
	}
	const session = "kb-identity-owner"
	common.SetSessionWorkflowPath(session, workspace)
	defer common.ClearSessionShellConfig(session)
	_, tools, _ := createKnowledgebaseTools("admin", session, workspace)
	read := tools["brain_read"].(func(context.Context, map[string]interface{}) (string, error))
	args := map[string]any{"action": "read", "binding_alias": "kbtest", "path": "Imported/identity.md"}
	caller := executor.WithSessionID(t.Context(), session)
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"missing claims", caller},
		{"user key only", context.WithValue(caller, common.UserIDKey, "admin")},
		{"foreign authenticated caller", knowledgeTestCaller(caller, "priya")},
		{"conflicting user key", context.WithValue(knowledgeTestCaller(caller, "admin"), common.UserIDKey, "outsider")},
		{"missing caller session", knowledgeTestCaller(t.Context(), "admin")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := read(tc.ctx, args); err == nil || out != "" {
				t.Fatalf("reused executor admitted caller: %s %v", out, err)
			}
		})
	}
	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner(session, "admin")
	bound := api.bindToolExecutionContext(knowledgeTestCaller(t.Context(), "admin"), session, QueryRequest{}, false)
	ctx, err := bound(caller, "brain_read")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := read(ctx, args); err != nil || !strings.Contains(out, "identity-bound-marker") {
		t.Fatalf("owner read failed: %s %v", out, err)
	}
	if _, err := bound(executor.WithSessionID(t.Context(), "foreign-chat"), "brain_read"); err == nil {
		t.Fatal("foreign session admitted")
	}
}
