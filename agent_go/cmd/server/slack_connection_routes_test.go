package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

const slackConnectionTestDirectory = `{"users":[
	{"id":"admin-1","username":"root","admin":true,"can_create":true},
	{"id":"alice","username":"alice","can_create":true},
	{"id":"bob","username":"bob","can_create":true}
]}`

// setupSlackConnectionTest builds a multi-user world: an admin, two members,
// and one workflow owned by each member. Connections stay disabled unless a
// test opts in, so no Socket Mode listener starts.
func setupSlackConnectionTest(t *testing.T) (*StreamingAPI, *fakeWorkspaceDocumentStore) {
	t.Helper()
	api, workspace := setupSlackConnectionWorld(t)
	if _, err := api.chatStore.UpsertBotConnectorConfig(context.Background(), &chathistory.CreateBotConnectorConfigRequest{ID: "slack", Enabled: true, BotMode: true, AllowedChannels: "{}"}); err != nil {
		t.Fatal(err)
	}
	return api, workspace
}

// setupSlackConnectionWorld builds the same world without seeding the
// "slack" bot connector config: a server where channel routing was never
// saved. Use it to cover first-time saves.
func setupSlackConnectionWorld(t *testing.T) (*StreamingAPI, *fakeWorkspaceDocumentStore) {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, slackConnectionTestDirectory)
	server, workspace := newSlackListableWorkspaceServer(t)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	workspace.files["Workflow/alpha/workflow.json"] = `{"schema_version":1,"id":"wf_alpha","label":"Alpha","access":{"owners":["alice"],"readers":[]}}`
	workspace.files["Workflow/beta/workflow.json"] = `{"schema_version":1,"id":"wf_beta","label":"Beta","access":{"owners":["bob"],"readers":[]}}`
	resetSlackServiceForTest(t)
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &StreamingAPI{chatStore: store}, workspace
}

// newSlackListableWorkspaceServer mirrors newFakeWorkspaceServer's document
// GET/PUT behavior and adds the exact-/api/documents folder listing that
// workflow discovery needs for the delete-reference check.
func newSlackListableWorkspaceServer(t *testing.T) (*httptest.Server, *fakeWorkspaceDocumentStore) {
	t.Helper()
	store := &fakeWorkspaceDocumentStore{files: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/documents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		store.mu.Lock()
		seen := map[string]bool{}
		var children []map[string]string
		for path := range store.files {
			parts := strings.SplitN(path, "/", 3)
			if len(parts) < 3 || parts[0] != "Workflow" {
				continue
			}
			folder := parts[0] + "/" + parts[1]
			if seen[folder] {
				continue
			}
			seen[folder] = true
			children = append(children, map[string]string{"filepath": folder, "type": "folder"})
		}
		store.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data":    []map[string]any{{"filepath": "Workflow", "type": "folder", "children": children}},
		})
	})
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/documents/")
		store.mu.Lock()
		defer store.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			content, ok := store.files[path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"filepath": path, "content": content},
			})
		case http.MethodPut:
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			store.files[path] = body.Content
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	return httptest.NewServer(mux), store
}

func resetSlackServiceForTest(t *testing.T) {
	t.Helper()
	if prev := services.GetSlackService(); prev != nil {
		prev.StopListening()
	}
	services.SetSlackService(nil)
	t.Cleanup(func() {
		if svc := services.GetSlackService(); svc != nil {
			svc.StopListening()
		}
		services.SetSlackService(nil)
	})
}

func slackConnectionClaims(userID string) *UserClaims {
	username := userID
	if userID == "admin-1" {
		username = "root"
	}
	return &UserClaims{UserID: userID, Username: username}
}

func slackConnectionRequest(t *testing.T, method, userID, body string) *http.Request {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/api/human-feedback/slack/connections", reader)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, slackConnectionClaims(userID)))
	return req
}

func decodeSlackConnectionResponse(t *testing.T, body []byte) SlackConnectionResponse {
	t.Helper()
	var out SlackConnectionResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("invalid connection response: %v", err)
	}
	return out
}

func TestSlackConnectionOwnerCRUD(t *testing.T) {
	api, workspace := setupSlackConnectionTest(t)

	// Alice creates a connection scoped to her workflow.
	create := slackConnectionRequest(t, "POST", "alice", `{"display_name":"Alpha App","bot_token":"xoxb-alpha-secret","app_token":"xapp-alpha-secret","enabled":false,"workspace_path":"Workflow/alpha"}`)
	w := httptest.NewRecorder()
	createSlackConnectionHandler(api)(w, create)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", w.Code, w.Body.String())
	}
	created := decodeSlackConnectionResponse(t, w.Body.Bytes())
	if created.ID == "" || created.DisplayName != "Alpha App" || created.Enabled || created.WorkspacePath != "Workflow/alpha" {
		t.Fatalf("created = %+v", created)
	}
	if created.BotToken == "" || strings.Contains(created.BotToken, "secret") || !strings.Contains(created.BotToken, "...") {
		t.Fatalf("bot token not masked: %q", created.BotToken)
	}
	if !created.Configured {
		t.Fatal("complete connection not reported configured")
	}
	stored := workspace.files["config/slack-config.json"]
	if stored == "" || strings.Contains(stored, "alpha-secret") {
		t.Fatal("registry was not encrypted at rest")
	}

	// Bob cannot touch Alice's connection.
	bad := slackConnectionRequest(t, "POST", "bob", `{"display_name":"Hijacked"}`)
	bad = mux.SetURLVars(bad, map[string]string{"id": created.ID})
	w = httptest.NewRecorder()
	updateSlackConnectionHandler(api)(w, bad)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-workflow update status %d, want 403", w.Code)
	}

	// Alice renames; omitted tokens are preserved.
	update := slackConnectionRequest(t, "PATCH", "alice", `{"display_name":"Alpha Renamed"}`)
	update = mux.SetURLVars(update, map[string]string{"id": created.ID})
	w = httptest.NewRecorder()
	updateSlackConnectionHandler(api)(w, update)
	if w.Code != http.StatusOK {
		t.Fatalf("update status %d: %s", w.Code, w.Body.String())
	}
	updated := decodeSlackConnectionResponse(t, w.Body.Bytes())
	if updated.DisplayName != "Alpha Renamed" || updated.BotToken != created.BotToken {
		t.Fatalf("updated = %+v", updated)
	}

	// Alice deletes; a second delete is 404.
	del := mux.SetURLVars(slackConnectionRequest(t, "DELETE", "alice", ""), map[string]string{"id": created.ID})
	w = httptest.NewRecorder()
	deleteSlackConnectionHandler(api)(w, del)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status %d: %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	deleteSlackConnectionHandler(api)(w, del)
	if w.Code != http.StatusNotFound {
		t.Fatalf("second delete status %d, want 404", w.Code)
	}
}

func TestSlackConnectionAdminScope(t *testing.T) {
	api, _ := setupSlackConnectionTest(t)

	// Members cannot create platform-managed connections.
	w := httptest.NewRecorder()
	createSlackConnectionHandler(api)(w, slackConnectionRequest(t, "POST", "alice", `{"display_name":"Global"}`))
	if w.Code != http.StatusForbidden {
		t.Fatalf("unscoped member create status %d, want 403", w.Code)
	}

	adminCreate := slackConnectionRequest(t, "POST", "admin-1", `{"display_name":"Platform","bot_token":"xoxb-plat-secret","app_token":"xapp-plat-secret","enabled":false}`)
	w = httptest.NewRecorder()
	createSlackConnectionHandler(api)(w, adminCreate)
	if w.Code != http.StatusCreated {
		t.Fatalf("admin create status %d: %s", w.Code, w.Body.String())
	}
	platform := decodeSlackConnectionResponse(t, w.Body.Bytes())
	if !platform.IsDefault {
		t.Fatalf("first connection is not default: %+v", platform)
	}

	// Owners cannot touch the platform connection.
	w = httptest.NewRecorder()
	updateSlackConnectionHandler(api)(w, mux.SetURLVars(slackConnectionRequest(t, "POST", "alice", `{"display_name":"Hijacked"}`), map[string]string{"id": platform.ID}))
	if w.Code != http.StatusForbidden {
		t.Fatalf("platform update by owner status %d, want 403", w.Code)
	}

	// Bot-route principals cannot manage connections at all.
	botReq := httptest.NewRequest("POST", "/api/human-feedback/slack/connections", strings.NewReader(`{"display_name":"Bot"}`))
	botReq = botReq.WithContext(context.WithValue(botReq.Context(), UserContextKey, &UserClaims{UserID: "bot", Provider: "bot_route"}))
	w = httptest.NewRecorder()
	createSlackConnectionHandler(api)(w, botReq)
	if w.Code != http.StatusForbidden {
		t.Fatalf("bot_route create status %d, want 403", w.Code)
	}

	// Only admins change the default.
	second := slackConnectionRequest(t, "POST", "admin-1", `{"display_name":"Second","enabled":false,"workspace_path":"Workflow/alpha"}`)
	w = httptest.NewRecorder()
	createSlackConnectionHandler(api)(w, second)
	if w.Code != http.StatusCreated {
		t.Fatalf("second create status %d: %s", w.Code, w.Body.String())
	}
	secondID := decodeSlackConnectionResponse(t, w.Body.Bytes()).ID

	w = httptest.NewRecorder()
	setDefaultSlackConnectionHandler(api)(w, mux.SetURLVars(slackConnectionRequest(t, "POST", "alice", ""), map[string]string{"id": secondID}))
	if w.Code != http.StatusForbidden {
		t.Fatalf("owner set-default status %d, want 403", w.Code)
	}
	w = httptest.NewRecorder()
	setDefaultSlackConnectionHandler(api)(w, mux.SetURLVars(slackConnectionRequest(t, "POST", "admin-1", ""), map[string]string{"id": secondID}))
	if w.Code != http.StatusOK {
		t.Fatalf("admin set-default status %d: %s", w.Code, w.Body.String())
	}

	// The default cannot be deleted.
	w = httptest.NewRecorder()
	deleteSlackConnectionHandler(api)(w, mux.SetURLVars(slackConnectionRequest(t, "DELETE", "admin-1", ""), map[string]string{"id": secondID}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("delete-default status %d, want 400", w.Code)
	}
}

func TestSlackConnectionDeleteBlockedByReference(t *testing.T) {
	api, workspace := setupSlackConnectionTest(t)

	create := slackConnectionRequest(t, "POST", "alice", `{"display_name":"Alpha App","enabled":false,"workspace_path":"Workflow/alpha"}`)
	w := httptest.NewRecorder()
	createSlackConnectionHandler(api)(w, create)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", w.Code, w.Body.String())
	}
	connID := decodeSlackConnectionResponse(t, w.Body.Bytes()).ID

	// Point the workflow at the connection, then delete must refuse.
	manifest, found, err := ReadWorkflowManifest(context.Background(), "Workflow/alpha")
	if err != nil || !found {
		t.Fatal("fixture manifest missing")
	}
	manifest.Capabilities.SlackConnectionID = connID
	if err := WriteWorkflowManifest(context.Background(), "Workflow/alpha", manifest); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	deleteSlackConnectionHandler(api)(w, mux.SetURLVars(slackConnectionRequest(t, "DELETE", "alice", ""), map[string]string{"id": connID}))
	if w.Code != http.StatusConflict {
		t.Fatalf("referenced delete status %d, want 409: %s", w.Code, w.Body.String())
	}

	manifest.Capabilities.SlackConnectionID = ""
	if err := WriteWorkflowManifest(context.Background(), "Workflow/alpha", manifest); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	deleteSlackConnectionHandler(api)(w, mux.SetURLVars(slackConnectionRequest(t, "DELETE", "alice", ""), map[string]string{"id": connID}))
	if w.Code != http.StatusNoContent {
		t.Fatalf("unreferenced delete status %d: %s", w.Code, w.Body.String())
	}
	_ = workspace
}

func TestSlackManifestRejectsUnknownConnection(t *testing.T) {
	api, _ := setupSlackConnectionTest(t)

	update := httptest.NewRequest("POST", "/api/workflow/manifest", strings.NewReader(`{"workspace_path":"Workflow/alpha","capabilities":{"slack_connection_id":"slack_ghost"}}`))
	update = update.WithContext(context.WithValue(update.Context(), UserContextKey, slackConnectionClaims("alice")))
	w := httptest.NewRecorder()
	api.handleUpdateWorkflowManifest(w, update)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown connection status %d, want 400: %s", w.Code, w.Body.String())
	}

	create := slackConnectionRequest(t, "POST", "alice", `{"display_name":"Alpha App","enabled":false,"workspace_path":"Workflow/alpha"}`)
	w = httptest.NewRecorder()
	createSlackConnectionHandler(api)(w, create)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", w.Code, w.Body.String())
	}
	connID := decodeSlackConnectionResponse(t, w.Body.Bytes()).ID

	update = httptest.NewRequest("POST", "/api/workflow/manifest", strings.NewReader(`{"workspace_path":"Workflow/alpha","capabilities":{"slack_connection_id":"`+connID+`"}}`))
	update = update.WithContext(context.WithValue(update.Context(), UserContextKey, slackConnectionClaims("alice")))
	w = httptest.NewRecorder()
	api.handleUpdateWorkflowManifest(w, update)
	if w.Code != http.StatusOK {
		t.Fatalf("known connection status %d: %s", w.Code, w.Body.String())
	}
}

func TestSlackLegacyConfigMapsToDefault(t *testing.T) {
	api, _ := setupSlackConnectionTest(t)

	// Admin posts legacy credentials: they land on the default connection.
	legacy := httptest.NewRequest("POST", "/api/human-feedback/slack/config", strings.NewReader(`{"enabled":false,"bot_mode":false,"bot_token":"xoxb-legacy-secret","app_token":"xapp-legacy-secret"}`))
	legacy = legacy.WithContext(context.WithValue(legacy.Context(), UserContextKey, slackConnectionClaims("admin-1")))
	w := httptest.NewRecorder()
	updateSlackConfigHandler(api)(w, legacy)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy save status %d: %s", w.Code, w.Body.String())
	}
	var saved SlackConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Connections) != 1 || saved.DefaultConnectionID == "" || !saved.ManageDefaultAllowed {
		t.Fatalf("legacy save response = %+v", saved)
	}
	if strings.Contains(w.Body.String(), "legacy-secret") {
		t.Fatal("raw token echoed in legacy save response")
	}

	// A non-admin cannot change the default credentials.
	deny := httptest.NewRequest("POST", "/api/human-feedback/slack/config", strings.NewReader(`{"enabled":false,"bot_mode":false,"bot_token":"xoxb-evil-secret","app_token":"xapp-evil-secret"}`))
	deny = deny.WithContext(context.WithValue(deny.Context(), UserContextKey, slackConnectionClaims("alice")))
	w = httptest.NewRecorder()
	updateSlackConfigHandler(api)(w, deny)
	if w.Code != http.StatusForbidden {
		t.Fatalf("owner credential save status %d, want 403", w.Code)
	}

	// GET exposes the masked registry and the manage flag per caller.
	get := httptest.NewRequest("GET", "/api/human-feedback/slack/config", nil)
	get = get.WithContext(context.WithValue(get.Context(), UserContextKey, slackConnectionClaims("alice")))
	w = httptest.NewRecorder()
	getSlackConfigHandler(api)(w, get)
	if w.Code != http.StatusOK {
		t.Fatalf("get status %d", w.Code)
	}
	var got SlackConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Connections) != 1 || got.ManageDefaultAllowed || got.BotToken == "" {
		t.Fatalf("owner GET = %+v", got)
	}
	getAdmin := httptest.NewRequest("GET", "/api/human-feedback/slack/config", nil)
	getAdmin = getAdmin.WithContext(context.WithValue(getAdmin.Context(), UserContextKey, slackConnectionClaims("admin-1")))
	w = httptest.NewRecorder()
	getSlackConfigHandler(api)(w, getAdmin)
	var gotAdmin SlackConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &gotAdmin); err != nil {
		t.Fatal(err)
	}
	if !gotAdmin.ManageDefaultAllowed {
		t.Fatalf("admin GET missing manage flag: %+v", gotAdmin)
	}
}

func TestSlackChannelRouteFirstSaveWithoutConnectorConfig(t *testing.T) {
	api, _ := setupSlackConnectionWorld(t)

	// No "slack" connector config exists yet: saving the first channel
	// route must create it instead of failing with
	// "bot connector config not found: slack".
	save := httptest.NewRequest("POST", "/api/human-feedback/slack/config", strings.NewReader(`{"enabled":false,"bot_mode":false,"bot_token":"","app_token":"","channel_routing":{"C1234567890":{"workflow_id":"wf_alpha","workspace_path":"Workflow/alpha","workshop_mode":"run","bot_grant":"run","send_full_details":true}}}`))
	save = save.WithContext(context.WithValue(save.Context(), UserContextKey, slackConnectionClaims("alice")))
	w := httptest.NewRecorder()
	updateSlackConfigHandler(api)(w, save)
	if w.Code != http.StatusOK {
		t.Fatalf("first route save status %d: %s", w.Code, w.Body.String())
	}
	var saved SlackConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	route, ok := saved.ChannelRouting["C1234567890"]
	if !ok || route.WorkflowID != "wf_alpha" || route.WorkspacePath != "Workflow/alpha" {
		t.Fatalf("first route save response = %+v", saved.ChannelRouting)
	}
	stored, err := api.chatStore.GetBotConnectorConfig(context.Background(), "slack")
	if err != nil || stored == nil {
		t.Fatalf("connector config after first save: %+v, err=%v", stored, err)
	}
}

func TestSlackRouteSaveRegistersConnectorWhenSwitchOff(t *testing.T) {
	api, _ := setupSlackConnectionWorld(t)
	api.botManager = services.NewBotConversationManager(api.chatStore, "", "")

	save := httptest.NewRequest("POST", "/api/human-feedback/slack/config", strings.NewReader(`{"enabled":false,"bot_mode":false,"bot_token":"","app_token":"","channel_routing":{"C1234567890":{"workflow_id":"wf_alpha","workspace_path":"Workflow/alpha","workshop_mode":"run","bot_grant":"run"}}}`))
	save = save.WithContext(context.WithValue(save.Context(), UserContextKey, slackConnectionClaims("alice")))
	w := httptest.NewRecorder()
	updateSlackConfigHandler(api)(w, save)
	if w.Code != http.StatusOK {
		t.Fatalf("route save status %d: %s", w.Code, w.Body.String())
	}
	if api.botManager.GetConnector("slack") == nil {
		t.Fatal("connector not registered after route save while switch off")
	}
}

func TestSlackConfigureToolOwnerBranch(t *testing.T) {
	api, _ := setupSlackConnectionTest(t)
	reg := &recordingRegistrar{}
	if err := api.registerSlackBotTools(reg, "session", "Workflow/alpha", "", true, true); err != nil {
		t.Fatal(err)
	}
	configure, found := reg.tools["configure_slack_bot"]
	if !found {
		t.Fatal("configure_slack_bot not registered")
	}
	alice := context.WithValue(context.Background(), UserContextKey, slackConnectionClaims("alice"))

	out, err := configure.exec(alice, map[string]interface{}{"enabled": false, "bot_token": "xoxb-alpha-tool", "app_token": "xapp-alpha-tool", "app_name": "Alpha Bot"})
	if err != nil {
		t.Fatalf("owner configure failed: %v", err)
	}
	var ack map[string]interface{}
	if err := json.Unmarshal([]byte(out), &ack); err != nil || ack["saved"] != true || ack["connection_id"] == "" {
		t.Fatalf("owner configure ack = %q, %v", out, err)
	}
	manifest, found, err := ReadWorkflowManifest(context.Background(), "Workflow/alpha")
	if err != nil || !found || manifest.Capabilities.SlackConnectionID == "" {
		t.Fatalf("workflow did not adopt its connection: %+v", manifest)
	}
	svc := services.GetSlackService()
	conn, ok := svc.GetConnection(ack["connection_id"].(string))
	if !ok || conn.DisplayName != "Alpha Bot" {
		t.Fatalf("app_name not saved on create: %+v", conn)
	}

	// A second call updates the same connection instead of minting another.
	out2, err := configure.exec(alice, map[string]interface{}{"enabled": false})
	if err != nil {
		t.Fatalf("owner reconfigure failed: %v", err)
	}
	var ack2 map[string]interface{}
	if err := json.Unmarshal([]byte(out2), &ack2); err != nil || ack2["connection_id"] != ack["connection_id"] {
		t.Fatalf("reconfigure minted a second connection: %q", out2)
	}
	conn, ok = svc.GetConnection(ack["connection_id"].(string))
	if !ok || conn.DisplayName != "Alpha Bot" {
		t.Fatalf("omitted app_name did not preserve the name: %+v", conn)
	}

	// An explicit app_name renames the existing connection.
	if _, err := configure.exec(alice, map[string]interface{}{"enabled": false, "app_name": "Alpha Renamed"}); err != nil {
		t.Fatalf("owner rename failed: %v", err)
	}
	conn, ok = svc.GetConnection(ack["connection_id"].(string))
	if !ok || conn.DisplayName != "Alpha Renamed" {
		t.Fatalf("app_name did not rename on update: %+v", conn)
	}

	// A non-owner is refused.
	bob := context.WithValue(context.Background(), UserContextKey, slackConnectionClaims("bob"))
	if _, err := configure.exec(bob, map[string]interface{}{"enabled": false}); err == nil {
		t.Fatal("non-owner configured another workflow's connection")
	}

	// Owners can inspect their own masked connection.
	inspect, found := reg.tools["get_slack_bot_credentials"]
	if !found {
		t.Fatal("get_slack_bot_credentials not registered")
	}
	raw, err := inspect.exec(alice, map[string]interface{}{})
	if err != nil {
		t.Fatalf("owner inspect failed: %v", err)
	}
	if !strings.Contains(raw, ack["connection_id"].(string)) || strings.Contains(raw, "xoxb-alpha-tool") {
		t.Fatalf("owner inspect = %q", raw)
	}
	if _, err := inspect.exec(bob, map[string]interface{}{}); err == nil {
		t.Fatal("non-owner inspected another workflow's connection")
	}
}

func TestSlackEnabledConnectionStartsChildRuntime(t *testing.T) {
	api, workspace := setupSlackConnectionTest(t)

	platform := slackConnectionRequest(t, "POST", "admin-1", `{"display_name":"Platform","bot_token":"xoxb-plat","app_token":"xapp-plat","enabled":false}`)
	w := httptest.NewRecorder()
	createSlackConnectionHandler(api)(w, platform)
	if w.Code != http.StatusCreated {
		t.Fatalf("platform create status %d: %s", w.Code, w.Body.String())
	}
	second := slackConnectionRequest(t, "POST", "alice", `{"display_name":"Alpha App","bot_token":"xoxb-alpha","app_token":"xapp-alpha","enabled":true,"workspace_path":"Workflow/alpha"}`)
	w = httptest.NewRecorder()
	createSlackConnectionHandler(api)(w, second)
	if w.Code != http.StatusCreated {
		t.Fatalf("workflow create status %d: %s", w.Code, w.Body.String())
	}
	connID := decodeSlackConnectionResponse(t, w.Body.Bytes()).ID

	svc := services.GetSlackService()
	if svc == nil {
		t.Fatal("slack service not initialized")
	}
	target, err := svc.ServiceForConnection(connID)
	if err != nil {
		t.Fatalf("child runtime not serving %q: %v", connID, err)
	}
	if target == svc || target.ConnectionID() != connID {
		t.Fatal("selection did not resolve to the child runtime")
	}
	if !svc.IsEnabled() {
		t.Fatal("service with an enabled child reports disabled")
	}
	_ = api
	_ = workspace
}

func TestSlackConfigureToolScopeFollowsContext(t *testing.T) {
	api, _ := setupSlackConnectionTest(t)

	// A pre-existing platform default the workflow call must leave alone.
	platform := slackConnectionRequest(t, "POST", "admin-1", `{"display_name":"Platform","bot_token":"xoxb-plat","app_token":"xapp-plat","enabled":false}`)
	w := httptest.NewRecorder()
	createSlackConnectionHandler(api)(w, platform)
	if w.Code != http.StatusCreated {
		t.Fatalf("platform create status %d: %s", w.Code, w.Body.String())
	}
	defaultID := decodeSlackConnectionResponse(t, w.Body.Bytes()).ID
	svc := services.GetSlackService()
	before, ok := svc.GetConnection(defaultID)
	if !ok {
		t.Fatal("default connection missing after create")
	}

	// An admin in a workflow builder chat configures the workflow's own
	// connection, not the platform default.
	reg := &recordingRegistrar{}
	if err := api.registerSlackBotTools(reg, "session", "Workflow/alpha", "", true, false); err != nil {
		t.Fatal(err)
	}
	configure, found := reg.tools["configure_slack_bot"]
	if !found {
		t.Fatal("configure_slack_bot not registered")
	}
	admin := context.WithValue(context.Background(), UserContextKey, slackConnectionClaims("admin-1"))
	out, err := configure.exec(admin, map[string]interface{}{"enabled": false, "bot_token": "xoxb-" + "alpha-admin", "app_token": "xapp-alpha-admin", "app_name": "Alpha Admin App"})
	if err != nil {
		t.Fatalf("admin workflow configure failed: %v", err)
	}
	var ack map[string]interface{}
	if err := json.Unmarshal([]byte(out), &ack); err != nil || ack["saved"] != true || ack["connection_id"] == "" {
		t.Fatalf("admin workflow configure ack = %q, %v", out, err)
	}
	if ack["connection_id"] == defaultID {
		t.Fatal("admin workflow call overwrote the platform default")
	}
	manifest, found, err := ReadWorkflowManifest(context.Background(), "Workflow/alpha")
	if err != nil || !found || manifest.Capabilities.SlackConnectionID != ack["connection_id"] {
		t.Fatalf("workflow did not adopt its connection: %+v", manifest)
	}
	conn, ok := svc.GetConnection(ack["connection_id"].(string))
	if !ok || normalizeConversationWorkspace(conn.WorkspacePath) != normalizeConversationWorkspace("Workflow/alpha") {
		t.Fatalf("connection not scoped to the workflow: %+v", conn)
	}
	if conn.DisplayName != "Alpha Admin App" {
		t.Fatalf("app_name not applied for admin workflow call: %+v", conn)
	}
	after, ok := svc.GetConnection(defaultID)
	if !ok || after.BotToken != before.BotToken || after.AppToken != before.AppToken || after.DisplayName != before.DisplayName {
		t.Fatalf("platform default changed: before=%+v after=%+v", before, after)
	}

	// Without a workflow or project context the admin still manages the default.
	regProfile := &recordingRegistrar{}
	if err := api.registerSlackBotTools(regProfile, "session", "", "work", true, false); err != nil {
		t.Fatal(err)
	}
	configureDefault, found := regProfile.tools["configure_slack_bot"]
	if !found {
		t.Fatal("configure_slack_bot not registered for unbound context")
	}
	out, err = configureDefault.exec(admin, map[string]interface{}{"enabled": false, "bot_token": "xoxb-" + "plat-new", "app_token": "xapp-plat-new"})
	if err != nil {
		t.Fatalf("admin default configure failed: %v", err)
	}
	var defAck map[string]interface{}
	if err := json.Unmarshal([]byte(out), &defAck); err != nil || defAck["saved"] != true {
		t.Fatalf("admin default configure ack = %q, %v", out, err)
	}
	if svc.DefaultConnectionID() != defaultID {
		t.Fatalf("unbound save moved the default to %q", svc.DefaultConnectionID())
	}

	// A non-admin without a workflow or project context is refused.
	alice := context.WithValue(context.Background(), UserContextKey, slackConnectionClaims("alice"))
	if _, err := configureDefault.exec(alice, map[string]interface{}{"enabled": false}); err == nil || !strings.Contains(err.Error(), "only an admin may configure the shared default") {
		t.Fatalf("non-admin unbound configure err = %v", err)
	}
}

func TestSlackConfigureToolProductScope(t *testing.T) {
	api, workspace := setupSlackConnectionTest(t)
	profiles := agentprofiles.NewRegistry()
	if err := profiles.RegisterProfile(agentprofiles.Profile{
		ID: "work", Name: "Work", Version: 1, SystemPromptTemplate: "test", BuiltIn: true,
		Runtime: agentprofiles.RuntimePolicy{
			Workspace: agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Chats/Work/projects"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	api.agentProfiles = profiles

	projectPath := "_users/alice/Chats/Work/projects/alpha"
	workspace.files[projectPath+"/workflow.json"] = `{"schema_version":1,"id":"proj_alpha","label":"Alpha Project","capabilities":{}}`

	reg := &recordingRegistrar{}
	if err := api.registerSlackBotTools(reg, "session", projectPath, "work", true, false); err != nil {
		t.Fatal(err)
	}
	configure, found := reg.tools["configure_slack_bot"]
	if !found {
		t.Fatal("configure_slack_bot not registered")
	}
	alice := context.WithValue(context.Background(), UserContextKey, slackConnectionClaims("alice"))
	bob := context.WithValue(context.Background(), UserContextKey, slackConnectionClaims("bob"))
	admin := context.WithValue(context.Background(), UserContextKey, slackConnectionClaims("admin-1"))

	// A product owner creates the project's own connection; the platform
	// default is untouched (none exists).
	out, err := configure.exec(alice, map[string]interface{}{"enabled": false, "bot_token": "xoxb-" + "proj-alice", "app_token": "xapp-proj-alice", "app_name": "Alpha Crew App"})
	if err != nil {
		t.Fatalf("owner product configure failed: %v", err)
	}
	var ack map[string]interface{}
	if err := json.Unmarshal([]byte(out), &ack); err != nil || ack["saved"] != true || ack["connection_id"] == "" {
		t.Fatalf("owner product configure ack = %q, %v", out, err)
	}
	connID := ack["connection_id"].(string)
	svc := services.GetSlackService()
	conn, ok := svc.GetConnection(connID)
	if !ok || conn.ProfileID != "work" || normalizeConversationWorkspace(conn.WorkspacePath) != normalizeConversationWorkspace(projectPath) {
		t.Fatalf("connection not scoped to the project: %+v", conn)
	}
	if conn.DisplayName != "Alpha Crew App" {
		t.Fatalf("app_name not saved on create: %+v", conn)
	}
	if svc.DefaultConnectionID() != "" {
		t.Fatalf("project save created a default: %q", svc.DefaultConnectionID())
	}
	selected, err := productSlackConnectionID(context.Background(), "work", projectPath)
	if err != nil || selected != connID {
		t.Fatalf("project did not adopt its connection: %q, %v", selected, err)
	}

	// A second call updates the same connection instead of minting another.
	out2, err := configure.exec(alice, map[string]interface{}{"enabled": false, "app_name": "Alpha Crew Renamed"})
	if err != nil {
		t.Fatalf("owner product reconfigure failed: %v", err)
	}
	var ack2 map[string]interface{}
	if err := json.Unmarshal([]byte(out2), &ack2); err != nil || ack2["connection_id"] != connID {
		t.Fatalf("reconfigure minted a second connection: %q", out2)
	}

	// Another user's project path is outside the caller's product root.
	bobProject := "_users/bob/Chats/Work/projects/beta"
	workspace.files[bobProject+"/workflow.json"] = `{"schema_version":1,"id":"proj_beta","label":"Beta Project","capabilities":{}}`
	regBob := &recordingRegistrar{}
	if err := api.registerSlackBotTools(regBob, "session", bobProject, "work", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := regBob.tools["configure_slack_bot"].exec(alice, map[string]interface{}{"enabled": false}); err == nil {
		t.Fatal("configured another user's project connection")
	}
	// An admin may configure any project's connection.
	if _, err := regBob.tools["configure_slack_bot"].exec(admin, map[string]interface{}{"enabled": false, "app_name": "Beta Admin App"}); err != nil {
		t.Fatalf("admin product configure failed: %v", err)
	}

	// Owners inspect their own masked connection; outsiders cannot.
	inspect, found := reg.tools["get_slack_bot_credentials"]
	if !found {
		t.Fatal("get_slack_bot_credentials not registered")
	}
	raw, err := inspect.exec(alice, map[string]interface{}{})
	if err != nil {
		t.Fatalf("owner product inspect failed: %v", err)
	}
	if !strings.Contains(raw, connID) || strings.Contains(raw, "proj-alice") {
		t.Fatalf("owner product inspect = %q", raw)
	}
	if _, err := inspect.exec(bob, map[string]interface{}{}); err == nil {
		t.Fatal("outsider inspected another project's connection")
	}

	// Route sends resolve the project selection.
	routeID := slackConnectionIDForRoute(context.Background(), ChannelRoute{ProfileID: "work", WorkspacePath: projectPath})
	if routeID != connID {
		t.Fatalf("route resolved %q, want %q", routeID, connID)
	}

	// Delete is blocked while the project selects the connection.
	del := mux.SetURLVars(slackConnectionRequest(t, "DELETE", "alice", ""), map[string]string{"id": connID})
	w := httptest.NewRecorder()
	deleteSlackConnectionHandler(api)(w, del)
	if w.Code != http.StatusConflict {
		t.Fatalf("referenced delete status %d, want 409: %s", w.Code, w.Body.String())
	}

	// The selection endpoint mirrors the workflow manifest write: unknown
	// selections are rejected, outsiders are refused, and clearing the
	// selection unblocks the delete.
	putUnknown := httptest.NewRequest("PUT", "/api/human-feedback/slack/connections/project/selection?profile_id=work&workspace_path="+projectPath, strings.NewReader(`{"slack_connection_id":"nope"}`))
	putUnknown = putUnknown.WithContext(alice)
	w = httptest.NewRecorder()
	projectSlackConnectionHandler(api)(w, putUnknown)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown selection status %d, want 400: %s", w.Code, w.Body.String())
	}
	putBob := httptest.NewRequest("PUT", "/api/human-feedback/slack/connections/project/selection?profile_id=work&workspace_path="+projectPath, strings.NewReader(`{"slack_connection_id":""}`))
	putBob = putBob.WithContext(bob)
	w = httptest.NewRecorder()
	projectSlackConnectionHandler(api)(w, putBob)
	if w.Code != http.StatusForbidden {
		t.Fatalf("outsider selection write status %d, want 403: %s", w.Code, w.Body.String())
	}
	getSel := httptest.NewRequest("GET", "/api/human-feedback/slack/connections/project/selection?profile_id=work&workspace_path="+projectPath, nil)
	getSel = getSel.WithContext(alice)
	w = httptest.NewRecorder()
	projectSlackConnectionHandler(api)(w, getSel)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), connID) {
		t.Fatalf("selection read = %d %q", w.Code, w.Body.String())
	}
	putClear := httptest.NewRequest("PUT", "/api/human-feedback/slack/connections/project/selection?profile_id=work&workspace_path="+projectPath, strings.NewReader(`{"slack_connection_id":""}`))
	putClear = putClear.WithContext(alice)
	w = httptest.NewRecorder()
	projectSlackConnectionHandler(api)(w, putClear)
	if w.Code != http.StatusOK {
		t.Fatalf("selection clear status %d: %s", w.Code, w.Body.String())
	}
	del = mux.SetURLVars(slackConnectionRequest(t, "DELETE", "alice", ""), map[string]string{"id": connID})
	w = httptest.NewRecorder()
	deleteSlackConnectionHandler(api)(w, del)
	if w.Code != http.StatusNoContent {
		t.Fatalf("unreferenced delete status %d: %s", w.Code, w.Body.String())
	}
}

// The crew Slack tab sends the crew's logical path ("Chats/Work/projects/<id>").
// Stored or read as-is it names no folder: selecting the new bot failed with
// "product manifest not found" and its route had no owner (server A 2026-09-25).
func TestCrewSlackBotAcceptsTheLogicalProjectPath(t *testing.T) {
	api, workspace := setupSlackConnectionTest(t)
	profiles := agentprofiles.NewRegistry()
	if err := profiles.RegisterProfile(agentprofiles.Profile{
		ID: "work", Name: "Work", Version: 1, SystemPromptTemplate: "test", BuiltIn: true,
		Runtime: agentprofiles.RuntimePolicy{
			Workspace: agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Chats/Work/projects"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	api.agentProfiles = profiles
	physical := "_users/alice/Chats/Work/projects/alpha"
	workspace.files[physical+"/workflow.json"] = `{"schema_version":1,"id":"proj_alpha","label":"Alpha","capabilities":{}}`

	create := slackConnectionRequest(t, "POST", "alice", `{"display_name":"SDE","bot_token":"xoxb-crew-alpha","app_token":"xapp-crew-alpha","enabled":false,"workspace_path":"Chats/Work/projects/alpha","profile_id":"work"}`)
	w := httptest.NewRecorder()
	createSlackConnectionHandler(api)(w, create)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	conn := decodeSlackConnectionResponse(t, w.Body.Bytes())
	stored, ok := services.GetSlackService().GetConnection(conn.ID)
	if !ok || stored.WorkspacePath != physical {
		t.Fatalf("stored scope = %q, want the physical project path %q", stored.WorkspacePath, physical)
	}

	selectReq := httptest.NewRequest("PUT", "/api/human-feedback/slack/connections/project/selection?profile_id=work&workspace_path=Chats/Work/projects/alpha", strings.NewReader(`{"slack_connection_id":"`+conn.ID+`"}`))
	selectReq = selectReq.WithContext(context.WithValue(selectReq.Context(), UserContextKey, slackConnectionClaims("alice")))
	w = httptest.NewRecorder()
	projectSlackConnectionHandler(api)(w, selectReq)
	if w.Code != http.StatusOK {
		t.Fatalf("select = %d %s", w.Code, w.Body.String())
	}
	if selected, err := productSlackConnectionID(context.Background(), "work", physical); err != nil || selected != conn.ID {
		t.Fatalf("crew did not select its bot: %q, %v", selected, err)
	}
}

// A crew bot saved before scopes were made physical keeps the logical path.
// Its owner must still manage it (and can re-save it to repair the scope);
// another user must not.
func TestLegacyLogicalCrewSlackConnectionStaysManageableByItsOwner(t *testing.T) {
	api, workspace := setupSlackConnectionTest(t)
	profiles := agentprofiles.NewRegistry()
	if err := profiles.RegisterProfile(agentprofiles.Profile{
		ID: "work", Name: "Work", Version: 1, SystemPromptTemplate: "test", BuiltIn: true,
		Runtime: agentprofiles.RuntimePolicy{
			Workspace: agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Chats/Work/projects"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	api.agentProfiles = profiles
	workspace.files["_users/alice/Chats/Work/projects/alpha/workflow.json"] = `{"schema_version":1,"id":"proj_alpha","capabilities":{}}`
	legacy := services.SlackConnection{ID: "slack_legacy", WorkspacePath: "Chats/Work/projects/alpha", ProfileID: "work"}

	if err := requireSlackConnectionAccess(slackConnectionRequest(t, "PUT", "alice", ""), api, legacy); err != nil {
		t.Fatalf("owner locked out of a legacy crew bot: %v", err)
	}
	if err := requireSlackConnectionAccess(slackConnectionRequest(t, "PUT", "bob", ""), api, services.SlackConnection{ID: "slack_alice", WorkspacePath: "_users/alice/Chats/Work/projects/alpha", ProfileID: "work"}); err == nil {
		t.Fatal("another user managed alice's crew bot")
	}
}

// server A 2026-09-26: crew destinations saved in the logical form had no owner,
// so the crew's own bot answered "This Slack route is no longer configured".
// The startup migration rewrites every store (connection scope, its channel
// routes, the platform channel routes) to the owner's physical folder and
// selects a repaired app for its crew.
func TestMigrateCrewBotScopes(t *testing.T) {
	api, workspace := setupSlackConnectionTest(t)
	physical := "_users/alice/Chats/Work/projects/alpha"
	logical := "Chats/Work/projects/alpha"
	workspace.files[physical+"/workflow.json"] = `{"schema_version":1,"id":"proj_alpha","capabilities":{}}`
	svc, err := ensureSlackService()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := svc.CreateSlackConnection(context.Background(), services.SlackConnectionInput{
		DisplayName: "SDE", BotToken: "xoxb-" + "repair-alpha", AppToken: "xapp-repair-alpha",
		Enabled: false, WorkspacePath: physical, ProfileID: "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateSlackConnection(context.Background(), services.SlackConnectionInput{
		DisplayName: "Helper", BotToken: "xoxb-" + "repair-helper", AppToken: "xapp-repair-helper",
		Enabled: false, WorkspacePath: "Workflow/helper",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSlackConnectionChannelRoute(context.Background(), other.ID, "C0SHARED1", &services.SlackConnectionRoute{WorkspacePath: physical, ProfileID: "work", AddedBy: "alice"}); err != nil {
		t.Fatal(err)
	}
	// Seed the legacy state as it existed on disk: logical crew paths.
	workspace.files["config/slack-config.json"] = strings.ReplaceAll(workspace.files["config/slack-config.json"], physical, logical)
	if err := svc.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := api.chatStore.UpsertBotConnectorConfig(context.Background(), &chathistory.CreateBotConnectorConfigRequest{
		ID: "slack", Enabled: true, BotMode: true,
		AllowedChannels: `{"C0PLATFORM":{"profile_id":"work","conversation_key":"proj_alpha","workspace_path":"` + logical + `","workspace_user_id":"alice","bot_grant":"run"}}`,
	}); err != nil {
		t.Fatal(err)
	}

	api.migrateCrewBotScopes(context.Background(), svc)

	repaired, _ := svc.GetConnection(conn.ID)
	if repaired.WorkspacePath != physical {
		t.Fatalf("connection scope = %q, want %q", repaired.WorkspacePath, physical)
	}
	if !strings.HasSuffix(repaired.BotToken, "lpha") || repaired.Enabled {
		t.Fatalf("migration changed tokens or enabled state: %+v", repaired)
	}
	if selected, err := productSlackConnectionID(context.Background(), "work", physical); err != nil || selected != conn.ID {
		t.Fatalf("crew did not select its repaired app: %q, %v", selected, err)
	}
	helper, _ := svc.GetConnection(other.ID)
	if route := helper.ChannelRoutes["C0SHARED1"]; route.WorkspacePath != physical {
		t.Fatalf("connection channel route = %q, want %q", route.WorkspacePath, physical)
	}
	_, routes, err := api.slackRoutes(context.Background())
	if err != nil || routes["C0PLATFORM"].WorkspacePath != physical {
		t.Fatalf("platform channel route = %+v, %v", routes["C0PLATFORM"], err)
	}
}

// Every Slack store refuses a logical crew path, so a caller that skips the
// edge conversion fails here rather than in Slack.
func TestSlackStoresRefuseLogicalCrewScope(t *testing.T) {
	setupSlackConnectionTest(t)
	svc, err := ensureSlackService()
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateSlackConnection(context.Background(), services.SlackConnectionInput{
		DisplayName: "SDE", BotToken: "xoxb-" + "logical-alpha", AppToken: "xapp-logical-alpha",
		WorkspacePath: "Chats/Work/projects/alpha", ProfileID: "work",
	})
	if !errors.Is(err, services.ErrLogicalCrewScope) {
		t.Fatalf("create with a logical crew path = %v, want ErrLogicalCrewScope", err)
	}
	if err := validateSlackRoutingScopes(map[string]ChannelRoute{"C1": {ProfileID: "work", WorkspacePath: "Chats/Work/projects/alpha"}}); !errors.Is(err, services.ErrLogicalCrewScope) {
		t.Fatalf("platform route with a logical crew path = %v", err)
	}
	if err := validateSlackRoutingScopes(map[string]ChannelRoute{"C1": {WorkflowID: "wf", WorkspacePath: "Workflow/reports"}}); err != nil {
		t.Fatalf("workflow route refused: %v", err)
	}
}
