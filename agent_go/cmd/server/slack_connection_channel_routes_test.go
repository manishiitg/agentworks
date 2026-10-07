package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// ownerBotWorld: alice owns Alpha (with its own enabled bot) and Gamma,
// can edit Delta, and only reads Epsilon; bob owns Beta.
func ownerBotWorld(t *testing.T) (*StreamingAPI, string) {
	t.Helper()
	api, workspace := setupSlackConnectionTest(t)
	workspace.files["Workflow/gamma/workflow.json"] = `{"schema_version":1,"id":"wf_gamma","label":"Gamma","access":{"owners":["alice"],"readers":[]}}`
	workspace.files["Workflow/delta/workflow.json"] = `{"schema_version":1,"id":"wf_delta","label":"Delta","access":{"owners":["bob"],"editors":["alice"],"readers":[]}}`
	workspace.files["Workflow/epsilon/workflow.json"] = `{"schema_version":1,"id":"wf_epsilon","label":"Epsilon","access":{"owners":["bob"],"readers":["alice"]}}`
	createSlackConnectionForTest(t, api, "alice", `{"display_name":"Alpha App","bot_token":"xoxb-alpha","app_token":"xapp-alpha","enabled":true,"workspace_path":"Workflow/alpha"}`)
	createSlackConnectionForTest(t, api, "bob", `{"display_name":"Beta App","bot_token":"xoxb-beta","app_token":"xapp-beta","enabled":true,"workspace_path":"Workflow/beta"}`)
	services.SetDedicatedSlackRouteFunc(api.dedicatedSlackRoute)
	t.Cleanup(func() { services.SetDedicatedSlackRouteFunc(nil) })
	return api, slackConnectionIDForScope(t, "Workflow/alpha")
}

func slackConnectionIDForScope(t *testing.T, workspacePath string) string {
	t.Helper()
	for _, conn := range services.GetSlackService().ListConnections() {
		if conn.WorkspacePath == workspacePath {
			return conn.ID
		}
	}
	t.Fatalf("no connection scoped to %s", workspacePath)
	return ""
}

func putChannelRoute(api *StreamingAPI, userID, connID, channel, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("PUT", "/api/human-feedback/slack/connections/"+connID+"/channel-routes/"+channel, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, slackConnectionClaims(userID)))
	req = mux.SetURLVars(req, map[string]string{"id": connID, "channel": channel})
	w := httptest.NewRecorder()
	putSlackConnectionChannelRouteHandler(api)(w, req)
	return w
}

func deleteChannelRoute(api *StreamingAPI, userID, connID, channel string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("DELETE", "/api/human-feedback/slack/connections/"+connID+"/channel-routes/"+channel, nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, slackConnectionClaims(userID)))
	req = mux.SetURLVars(req, map[string]string{"id": connID, "channel": channel})
	w := httptest.NewRecorder()
	deleteSlackConnectionChannelRouteHandler(api)(w, req)
	return w
}

// An owner routes C1 on their Alpha bot to their Gamma workflow: C1 on that
// app answers for Gamma, every other channel still for Alpha.
func TestOwnerBotChannelRouteResolvesToRoutedWorkflow(t *testing.T) {
	api, alphaApp := ownerBotWorld(t)
	if w := putChannelRoute(api, "alice", alphaApp, "C1111111111", `{"workspace_path":"Workflow/gamma"}`); w.Code != http.StatusOK {
		t.Fatalf("add route: %d %s", w.Code, w.Body.String())
	}
	fallback := func() *ChannelRoute { return &ChannelRoute{WorkflowID: "wf_shared"} }
	routed := services.ResolveSlackRoute(context.Background(), alphaApp, "C1111111111", fallback)
	if routed == nil || routed.WorkflowID != "wf_gamma" || routed.WorkspacePath != "Workflow/gamma" || routed.BotGrant != "run" {
		t.Fatalf("C1 resolved to %+v, want Gamma", routed)
	}
	own := services.ResolveSlackRoute(context.Background(), alphaApp, "C2222222222", fallback)
	if own == nil || own.WorkflowID != "wf_alpha" {
		t.Fatalf("C2 resolved to %+v, want Alpha", own)
	}
	// Another app in C1 is unaffected by Alpha's routes.
	beta := services.ResolveSlackRoute(context.Background(), slackConnectionIDForScope(t, "Workflow/beta"), "C1111111111", fallback)
	if beta == nil || beta.WorkflowID != "wf_beta" {
		t.Fatalf("Beta app in C1 resolved to %+v, want Beta", beta)
	}
	// A writer (not owner) of Delta may route to it too.
	if w := putChannelRoute(api, "alice", alphaApp, "C3333333333", `{"workspace_path":"Workflow/delta"}`); w.Code != http.StatusOK {
		t.Fatalf("writer route: %d %s", w.Code, w.Body.String())
	}

	// "Bots I can use" lists alice's bot with its routes and no tokens.
	req := httptest.NewRequest("GET", "/api/human-feedback/slack/connections/mine", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, slackConnectionClaims("alice")))
	w := httptest.NewRecorder()
	listUsableSlackBotsHandler(api)(w, req)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "xoxb") || strings.Contains(w.Body.String(), "xapp") {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var listed SlackUsableBotsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Bots) != 1 || listed.Bots[0].ID != alphaApp || listed.Bots[0].OwnerLabel != "Alpha" || len(listed.Bots[0].ChannelRoutes) != 2 || listed.Bots[0].ChannelRoutes[0].Label != "Gamma" {
		t.Fatalf("alice's bots = %+v", listed.Bots)
	}

	// Removing the route hands C1 back to Alpha.
	if w := deleteChannelRoute(api, "alice", alphaApp, "C1111111111"); w.Code != http.StatusOK {
		t.Fatalf("remove route: %d %s", w.Code, w.Body.String())
	}
	if back := services.ResolveSlackRoute(context.Background(), alphaApp, "C1111111111", fallback); back == nil || back.WorkflowID != "wf_alpha" {
		t.Fatalf("after removal C1 resolved to %+v, want Alpha", back)
	}
}

// Routing needs both: manage access to the bot and write access to the
// destination.
func TestOwnerBotChannelRoutePermissions(t *testing.T) {
	api, alphaApp := ownerBotWorld(t)
	betaApp := slackConnectionIDForScope(t, "Workflow/beta")

	// Alice cannot route on Bob's bot, even to her own workflow.
	if w := putChannelRoute(api, "alice", betaApp, "C1111111111", `{"workspace_path":"Workflow/gamma"}`); w.Code != http.StatusForbidden {
		t.Fatalf("route on someone else's bot: %d %s", w.Code, w.Body.String())
	}
	// Alice cannot route her bot to a workflow she only reads, or one she
	// has no access to.
	for _, path := range []string{"Workflow/epsilon", "Workflow/beta"} {
		if w := putChannelRoute(api, "alice", alphaApp, "C1111111111", `{"workspace_path":"`+path+`"}`); w.Code != http.StatusForbidden {
			t.Fatalf("route to %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	// Path tricks resolve to the real folder before the check.
	if w := putChannelRoute(api, "alice", alphaApp, "C1111111111", `{"workspace_path":"Workflow/gamma/../beta"}`); w.Code != http.StatusForbidden {
		t.Fatalf("dot-dot route: %d %s", w.Code, w.Body.String())
	}
	// Bob cannot remove a route on Alice's bot.
	if w := putChannelRoute(api, "alice", alphaApp, "C1111111111", `{"workspace_path":"Workflow/gamma"}`); w.Code != http.StatusOK {
		t.Fatalf("add: %d %s", w.Code, w.Body.String())
	}
	if w := deleteChannelRoute(api, "bob", alphaApp, "C1111111111"); w.Code != http.StatusForbidden {
		t.Fatalf("bob removed alice's route: %d %s", w.Code, w.Body.String())
	}
	// Bob's "bots I can use" does not include Alice's bot.
	for _, bot := range usableSlackBots(httptest.NewRequest("GET", "/", nil).WithContext(context.WithValue(context.Background(), UserContextKey, slackConnectionClaims("bob"))), api, services.GetSlackService()) {
		if bot.ID == alphaApp {
			t.Fatal("bob can use alice's bot")
		}
	}
	// A bot-route principal can never manage routes.
	req := httptest.NewRequest("PUT", "/", strings.NewReader(`{"workspace_path":"Workflow/gamma"}`))
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "bot", Provider: "bot_route"}))
	req = mux.SetURLVars(req, map[string]string{"id": alphaApp, "channel": "C4444444444"})
	w := httptest.NewRecorder()
	putSlackConnectionChannelRouteHandler(api)(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("bot principal route: %d", w.Code)
	}
}

// A channel lists several destinations per bot, picked with "@bot <slug>"
// (PLAT-668); the platform bot cannot be routed here.
func TestOwnerBotChannelListsDestinations(t *testing.T) {
	api, alphaApp := ownerBotWorld(t)
	if w := putChannelRoute(api, "alice", alphaApp, "C1111111111", `{"workspace_path":"Workflow/gamma"}`); w.Code != http.StatusOK {
		t.Fatalf("add: %d %s", w.Code, w.Body.String())
	}
	if w := putChannelRoute(api, "alice", alphaApp, "C1111111111", `{"workspace_path":"Workflow/delta"}`); w.Code != http.StatusOK {
		t.Fatalf("second destination for C1: %d %s", w.Code, w.Body.String())
	}
	if w := putChannelRoute(api, "alice", alphaApp, "C1111111111", `{"workspace_path":"Workflow/gamma"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate route: %d %s", w.Code, w.Body.String())
	}
	// Channels are explicit (PLAT-668): the bot's own workflow is listed
	// per channel like any other target.
	if w := putChannelRoute(api, "alice", alphaApp, "C2222222222", `{"workspace_path":"Workflow/alpha"}`); w.Code != http.StatusOK {
		t.Fatalf("route to the bot's own workflow: %d %s", w.Code, w.Body.String())
	}
	if w := putChannelRoute(api, "alice", alphaApp, "not-a-channel", `{"workspace_path":"Workflow/gamma"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad channel id: %d %s", w.Code, w.Body.String())
	}
	createSlackConnectionForTest(t, api, "admin-1", `{"display_name":"Platform","bot_token":"xoxb-plat","app_token":"xapp-plat","enabled":false}`)
	if w := putChannelRoute(api, "admin-1", slackConnectionIDForScope(t, ""), "C1111111111", `{"workspace_path":"Workflow/gamma"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("route on the platform bot: %d %s", w.Code, w.Body.String())
	}
	conn, _ := services.GetSlackService().GetConnection(alphaApp)
	route := conn.ChannelRoutes["C1111111111"]
	if len(conn.ChannelRoutes) != 2 || route.WorkspacePath != "Workflow/gamma" || route.AddedBy != "alice" || len(route.Targets) != 1 || route.Targets[0].WorkspacePath != "Workflow/delta" {
		t.Fatalf("routes = %+v", conn.ChannelRoutes)
	}
}

// The queued-turn gate accepts the routed destination for that app and
// channel, and refuses a turn whose target does not match it.
func TestOwnerBotRevalidatePrincipalAcceptsRoutedDestination(t *testing.T) {
	api, alphaApp := ownerBotWorld(t)
	if w := putChannelRoute(api, "alice", alphaApp, "C1111111111", `{"workspace_path":"Workflow/gamma"}`); w.Code != http.StatusOK {
		t.Fatalf("add: %d %s", w.Code, w.Body.String())
	}
	gamma := ChannelRoute{WorkflowID: "wf_gamma", WorkspacePath: "Workflow/gamma", BotGrant: "run"}
	claims := botRouteUserClaims("synthetic", gamma)
	claims.SlackTrustedApp = true
	req := QueryRequest{PresetQueryID: "wf_gamma", SelectedFolder: "Workflow/gamma", BotPlatform: "slack", BotChannelID: "C1111111111", BotConnectionID: alphaApp, BotUserID: "U1"}
	ctx := context.WithValue(context.Background(), UserContextKey, claims)
	resolved, err := api.revalidateExecutionPrincipal(ctx, req)
	if err != nil {
		t.Fatalf("routed destination rejected: %v", err)
	}
	if principal := GetUserFromContext(resolved).ExecutionPrincipal; principal == nil || principal.Target.WorkflowID != "wf_gamma" {
		t.Fatalf("principal = %+v", principal)
	}

	// Same Gamma turn in another channel: that channel answers for Alpha.
	other := req
	other.BotChannelID = "C2222222222"
	if _, err := api.revalidateExecutionPrincipal(ctx, other); err == nil || !strings.Contains(err.Error(), "target changed") {
		t.Fatalf("Gamma turn outside its routed channel admitted, err = %v", err)
	}
	// An Alpha turn in the routed channel is a mismatch too.
	alpha := ChannelRoute{WorkflowID: "wf_alpha", WorkspacePath: "Workflow/alpha", BotGrant: "run"}
	alphaClaims := botRouteUserClaims("synthetic", alpha)
	alphaClaims.SlackTrustedApp = true
	alphaReq := QueryRequest{PresetQueryID: "wf_alpha", SelectedFolder: "Workflow/alpha", BotPlatform: "slack", BotChannelID: "C1111111111", BotConnectionID: alphaApp, BotUserID: "U1"}
	if _, err := api.revalidateExecutionPrincipal(context.WithValue(context.Background(), UserContextKey, alphaClaims), alphaReq); err == nil {
		t.Fatal("Alpha turn admitted in a channel routed to Gamma")
	}

	// Once the route is removed, the queued Gamma turn is refused.
	if w := deleteChannelRoute(api, "alice", alphaApp, "C1111111111"); w.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	if _, err := api.revalidateExecutionPrincipal(ctx, req); err == nil {
		t.Fatal("Gamma turn admitted after its route was removed")
	}
}

// Rescoping a bot drops its channel routes: they were granted by the old
// scope's owner.
func TestOwnerBotRescopeClearsChannelRoutes(t *testing.T) {
	api, alphaApp := ownerBotWorld(t)
	if w := putChannelRoute(api, "alice", alphaApp, "C1111111111", `{"workspace_path":"Workflow/gamma"}`); w.Code != http.StatusOK {
		t.Fatalf("add: %d %s", w.Code, w.Body.String())
	}
	update := mux.SetURLVars(slackConnectionRequest(t, "PATCH", "admin-1", `{"workspace_path":"Workflow/beta"}`), map[string]string{"id": alphaApp})
	w := httptest.NewRecorder()
	updateSlackConnectionHandler(api)(w, update)
	if w.Code != http.StatusOK {
		t.Fatalf("rescope: %d %s", w.Code, w.Body.String())
	}
	if conn, _ := services.GetSlackService().GetConnection(alphaApp); len(conn.ChannelRoutes) != 0 {
		t.Fatalf("routes survived rescope: %+v", conn.ChannelRoutes)
	}
	// A plain rename keeps them.
	if w := putChannelRoute(api, "admin-1", alphaApp, "C1111111111", `{"workspace_path":"Workflow/gamma"}`); w.Code != http.StatusOK {
		t.Fatalf("admin add: %d %s", w.Code, w.Body.String())
	}
	rename := mux.SetURLVars(slackConnectionRequest(t, "PATCH", "admin-1", `{"display_name":"Renamed"}`), map[string]string{"id": alphaApp})
	w = httptest.NewRecorder()
	updateSlackConnectionHandler(api)(w, rename)
	if conn, _ := services.GetSlackService().GetConnection(alphaApp); w.Code != http.StatusOK || len(conn.ChannelRoutes) != 1 {
		t.Fatalf("rename dropped routes: %d %+v", w.Code, conn.ChannelRoutes)
	}
}
