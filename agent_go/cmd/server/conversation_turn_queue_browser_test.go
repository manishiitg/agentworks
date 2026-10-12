package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// Exercise profile enrichment, persisted queue replacement and re-admission:
// a headless project must not replay the server's default local Chrome port.
func TestProjectBrowserQueueReplayWithoutCDP(t *testing.T) {
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	t.Setenv("MULTI_USER_MODE", "false")
	const folder = "Chats/Work/projects/demo"
	const settingsPath = "_users/owner/" + folder + "/.browser-settings.json"
	workspace := &mockWorkspaceAPI{files: map[string]string{settingsPath: `{"mode":"cdp","port":9222}`}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	registry := agentprofiles.NewRegistry()
	if err := registry.RegisterProfile(agentprofiles.Profile{
		ID: "browser-queue", Name: "Browser queue", Version: 1, BuiltIn: true,
		SystemPromptTemplate: "{{.ProjectTitle}}",
		Runtime:              agentprofiles.RuntimePolicy{Transport: "auto", Capabilities: agentprofiles.RuntimeCapabilities{Browser: agentprofiles.CapabilityOptional}},
	}); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	api := newConversationTurnQueueTestAPI(files)
	api.agentProfiles = registry
	incoming := QueryRequest{Query: "Explain gravity", AgentMode: "multi-agent", AgentProfileID: "browser-queue", SelectedFolder: folder, AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Demo"}, DeliveryMode: "queue"}
	if err := validateRequestedCDPPorts(incoming); err != nil {
		t.Fatal(err)
	}
	ctx := withQueuedBrowserInput(context.Background(), incoming)
	input := ctx.Value(queuedBrowserInputKey{}).(queuedBrowserInput)
	incoming.callerBrowserInput = &input
	if _, err := api.resolveAgentProfileForQuery(ctx, &incoming, "owner", "chat"); err != nil {
		t.Fatal(err)
	}
	if incoming.BrowserMode != "headless" || incoming.CdpPort != nil || len(incoming.CdpPorts) != 0 {
		t.Fatalf("enriched browser settings: mode=%s port=%v ports=%v", incoming.BrowserMode, incoming.CdpPort, incoming.CdpPorts)
	}
	// Even a later enrichment must never become persisted caller input.
	stalePort := 9222
	incoming.CdpPort = &stalePort
	incoming.CdpPorts = []int{stalePort}
	if _, _, err := api.enqueueConversationTurn(ctx, "owner", "chat", incoming); err != nil {
		t.Fatal(err)
	}
	// The lightweight live-input path has a fresh context and copies the
	// stored continuation request. It must still use the original browser input.
	if _, _, err := api.enqueueConversationTurn(context.Background(), "owner", "chat", incoming); err != nil {
		t.Fatal(err)
	}
	restarted := newConversationTurnQueueTestAPI(files)
	for _, queued := range restarted.mustReadTurnQueueForTest(t, "owner") {
		if queued.Request.CdpPort != nil || len(queued.Request.CdpPorts) > 0 || queued.Request.BrowserMode != "" {
			t.Fatal("live-input continuation persisted runtime browser fields")
		}
	}
	restarted.agentProfiles = registry
	turn, ok := restarted.claimNextConversationTurn(context.Background(), "owner", "chat")
	if !ok {
		t.Fatal("persisted queue was not claimable")
	}
	wire, err := queryRequestToMap(turn.Request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var replay QueryRequest
	if err := json.Unmarshal(raw, &replay); err != nil {
		t.Fatal(err)
	}
	if replay.BrowserMode != "" || replay.CdpPort != nil || len(replay.CdpPorts) > 0 {
		t.Fatal("queue persisted runtime browser enrichment")
	}
	if err := validateRequestedCDPPorts(replay); err != nil {
		t.Fatal(err)
	}
	workspace.files[settingsPath] = `{"mode":"headless","port":9444}`
	if _, err := restarted.resolveAgentProfileForQuery(context.Background(), &replay, "owner", "chat"); err != nil {
		t.Fatal(err)
	}
	if err := validateRequestedCDPPorts(replay); err != nil {
		t.Fatal(err)
	}
	if replay.BrowserMode != "headless" || replay.CdpPort != nil {
		t.Fatal("replay did not rederive managed browser settings")
	}
	// Refusal of actual caller CDP remains before any profile normalization.
	for _, request := range []QueryRequest{{BrowserMode: "cdp"}, {BrowserMode: "headless", CdpPort: &stalePort}} {
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		api.handleQuery(response, httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(string(body))))
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "CDP is disabled") {
			t.Fatalf("caller CDP refusal: %d %s", response.Code, response.Body.String())
		}
	}
}

// A real early handleQuery rejection used to consume the keyed queue before
// any user row existed. Its text/failed receipt must survive a fresh read, and
// only an explicit owner action may make the dispatch claimable again.
func TestFailedQueuedMessageSurvivesAndCanRetry(t *testing.T) {
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	t.Setenv("MULTI_USER_MODE", "false")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true}]}`)
	const session = "failed-queue"
	files := map[string]string{}
	api := newConversationTurnQueueTestAPI(files)
	api.eventStore = openClientIdentityTestStore(t, session)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	ctx = context.WithValue(ctx, chatSubmissionContextKey{}, chatSubmissionContext{ID: "queued-client"})
	port := 9222
	turn, _, err := api.enqueueConversationTurn(ctx, "owner", session, QueryRequest{Query: "Explain gravity", AgentMode: "multi-agent", DeliveryMode: "queue", CdpPort: &port})
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok := api.claimNextConversationTurn(ctx, "owner", session)
	if !ok {
		t.Fatal("not claimed")
	}
	api.eventStore.SetEventAddedCallback(func(_ string, event internalevents.Event) {
		if event.Type == "live_input_confirmed" {
			stored := api.mustReadTurnQueueForTest(t, "owner")
			if len(stored) != 1 || stored[0].DeliveryError == "" {
				t.Error("failure was emitted before its no-replay state persisted")
			}
		}
	})
	api.executeQueuedConversationTurn(claimed)
	api.eventStore.SetEventAddedCallback(nil)
	restarted := newConversationTurnQueueTestAPI(files)
	pending := restarted.pendingQueuedMessages(ctx, "owner", session)
	if len(pending) != 1 || pending[0].Content != "Explain gravity" || pending[0].DeliveryError == "" {
		t.Fatalf("failed queue lost after reload: %+v", pending)
	}
	if _, ok := restarted.claimNextConversationTurn(ctx, "owner", session); ok {
		t.Fatal("failure was automatically replayed")
	}
	page, err := api.eventStore.ReadDurableChatPage(session, internalevents.DurableEventPageOptions{Limit: 100, FromStart: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || page.Events[0].ID != "user:queued-client" || page.Events[1].Type != "live_input_confirmed" {
		t.Fatalf("failed content and ordered receipt not durable: %+v", page.Events)
	}
	var message unifiedevents.UserMessageEvent
	encoded, err := json.Marshal(page.Events[0].Data.Data)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &message); err != nil {
		t.Fatal(err)
	}
	if message.Content != "Explain gravity" || message.Metadata["confirmation"] != "failed" {
		t.Fatalf("failed user message: %+v", message)
	}
	// Cross-account and wrong-session retries cannot claim this owner's message.
	if _, status, _ := restarted.retryQueuedConversationMessage(ctx, "other", session, turn.ID); status != http.StatusNotFound {
		t.Fatalf("cross-account retry=%d", status)
	}
	if _, status, _ := restarted.retryQueuedConversationMessage(ctx, "owner", "other-chat", turn.ID); status != http.StatusNotFound {
		t.Fatalf("cross-chat retry=%d", status)
	}
	retried, status, err := restarted.retryQueuedConversationMessage(ctx, "owner", session, turn.ID)
	if err != nil || status != http.StatusOK || retried.ID == turn.ID || retried.SubmissionID != turn.SubmissionID {
		t.Fatalf("owner retry=%+v %d %v", retried, status, err)
	}
	if next, ok := restarted.claimNextConversationTurn(ctx, "owner", session); !ok || next.ID != retried.ID {
		t.Fatal("explicit retry not claimable")
	}
	// The HTTP retry route refuses access-token callers like the existing actions.
	req := httptest.NewRequest(http.MethodPost, "/retry", nil).WithContext(context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{ID: "test-token"}}))
	req = mux.SetURLVars(req, map[string]string{"session_id": session, "message_id": turn.ID})
	response := httptest.NewRecorder()
	restarted.handleRetryQueuedConversationMessage(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("access-token retry=%d", response.Code)
	}
	// Removing the failed queue never erases the persisted chat content.
	if err := restarted.removeConversationTurn(ctx, retried); err != nil {
		t.Fatal(err)
	}
	if ids := durableChatIDs(t, api.eventStore, session); len(ids) != 2 {
		t.Fatalf("durable failure vanished after removal: %v", ids)
	}
}
