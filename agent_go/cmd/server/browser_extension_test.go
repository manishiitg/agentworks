package server

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
)

func TestChromeExtensionPairingRequiresWorkspaceWriteAccess(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"bob","username":"bob","can_create":true,"products":[]}]}`)
	previous := browserrelay.Default
	browserrelay.Default = browserrelay.New()
	defer func() { browserrelay.Default.Close(); browserrelay.Default = previous }()
	api := &StreamingAPI{}
	call := func(user, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{"action":"pair"}`))
		if user != "" {
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user}))
		}
		w := httptest.NewRecorder()
		api.handleBrowserExtension(w, req)
		return w
	}
	for _, user := range []string{"", "bob", "stranger"} {
		if w := call(user, "/?workspace_path=_users/alice/Chats/Code/projects/one&profile_id=code"); w.Code != 403 {
			t.Fatalf("%q paired: %d %s", user, w.Code, w.Body.String())
		}
	}
	if w := call("alice", "/?workspace_path=../Chats/Code/projects/one&profile_id=code"); w.Code != 403 {
		t.Fatal("noncanonical scope admitted")
	}
	for _, path := range []string{"/?workspace_path=Workflow/one&profile_id=code", "/?workspace_path=Workflow/one&profile_id=work", "/?workspace_path=_users/alice/Chats/Code/projects/one&profile_id=work"} {
		if w := call("alice", path); w.Code != 403 {
			t.Fatalf("extension rollout leaked: %s %d", path, w.Code)
		}
	}
	ownerPath := "/?workspace_path=_users/alice/Chats/Code/projects/one&profile_id=code"
	first, second := call("alice", ownerPath), call("alice", ownerPath)
	if first.Code != 200 || first.Body.String() != second.Body.String() {
		t.Fatalf("copy rotated code: %d %s %s", first.Code, first.Body.String(), second.Body.String())
	}
	var result struct {
		Token string `json:"token"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	crewPath := "/?workspace_path=_users/alice/Chats/Work/projects/one&profile_id=work"
	crew := call("alice", crewPath)
	var crewCode struct{ Token, Scope string }
	if err := json.Unmarshal(crew.Body.Bytes(), &crewCode); err != nil || crew.Code != 200 || crewCode.Token != result.Token || crewCode.Scope == result.Scope {
		t.Fatalf("Code/Crew account code or scope: %d %s", crew.Code, crew.Body.String())
	}
	if w := call("bob", crewPath); w.Code != 403 {
		t.Fatal("Crew reader paired owner's browser")
	}
	socketServer := httptest.NewServer(http.HandlerFunc(api.handleBrowserExtensionConnect))
	defer socketServer.Close()
	connect := func() *websocket.Conn {
		t.Helper()
		c, _, err := websocket.DefaultDialer.Dial(strings.Replace(socketServer.URL, "http", "ws", 1), http.Header{"Origin": []string{"chrome-extension://test"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		return c
	}
	connection := connect()
	connection.WriteJSON(map[string]string{"type": "pair", "token": result.Token, "scope": result.Scope})
	var reply map[string]any
	if err := connection.ReadJSON(&reply); err != nil || reply["type"] != "paired" {
		t.Fatal("Code socket authorization", err, reply)
	}
	connection.WriteJSON(map[string]string{"type": "ping"})
	if err := connection.ReadJSON(&reply); err != nil || reply["type"] != "pong" {
		t.Fatal("authorized heartbeat", err, reply)
	}
	*directory = `{"users":[{"id":"alice","username":"alice","can_create":true,"disabled":true,"products":[]}]}`
	invalidateUserDirectoryCache()
	connection.WriteJSON(map[string]string{"type": "ping"})
	if err := connection.ReadJSON(&reply); err == nil {
		t.Fatal("disabled owner kept browser authority")
	}
	rejected := connect()
	rejected.WriteJSON(map[string]string{"type": "pair", "token": result.Token, "scope": result.Scope})
	if err := rejected.ReadJSON(&reply); err != nil || reply["type"] != "error" {
		t.Fatal("saved code bypassed disabled account", err, reply)
	}

	if !shouldSkipAuth(browserExtensionConnectPath) || shouldSkipAuth("/api/browser/extension") || shouldSkipAuth(browserExtensionConnectPath+"/other") {
		t.Fatal("extension auth exemption is not exact")
	}
}

func TestChromeExtensionSharedCrewRequiresRegisteredOwner(t *testing.T) {
	f := newMultiUserFixture(t, sharedIdentityLayout())
	previous := browserrelay.Default
	browserrelay.Default = browserrelay.New()
	defer func() { browserrelay.Default.Close(); browserrelay.Default = previous }()
	workspace := f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
	for _, user := range []string{fixtureUserA, fixtureUserB, fixtureUserC} {
		r := httptest.NewRequest(http.MethodPost, "/?workspace_path="+workspace+"&profile_id=work", strings.NewReader(`{"action":"pair"}`)).WithContext(f.Ctx(user))
		w := httptest.NewRecorder()
		f.API.handleBrowserExtension(w, r)
		if user == fixtureUserA && w.Code != 200 {
			t.Fatalf("shared Crew owner rejected: %d %s", w.Code, w.Body.String())
		}
		if user != fixtureUserA && w.Code != 403 {
			t.Fatalf("Crew reader paired: %s %d", user, w.Code)
		}
	}
}

// A workflow grant uses the same account credential, never another owner's
// personal binding. Revoking workflow write access closes the live socket.
func TestChromeExtensionWorkflowAccessAndRevocation(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","can_create":true,"products":[]},{"id":"bob","can_create":true,"products":[]},{"id":"charlie","can_create":true,"products":["agentworks"]},{"id":"code-only","can_create":true,"products":["code"]}]}`)
	manifest := `{"version":"1","id":"browser-workflow","label":"Browser workflow","access":{"owners":["alice","charlie","code-only"],"readers":["bob"]},"capabilities":{"browser_mode":"auto"}}`
	workspaceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"content": manifest}})
	}))
	defer workspaceServer.Close()
	t.Setenv("WORKSPACE_API_URL", workspaceServer.URL)
	previous := browserrelay.Default
	browserrelay.Default = browserrelay.New()
	defer func() { browserrelay.Default.Close(); browserrelay.Default = previous }()
	api := &StreamingAPI{}
	call := func(user, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"action":"pair"}`)).WithContext(context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user}))
		w := httptest.NewRecorder()
		api.handleBrowserExtension(w, r)
		return w
	}
	path := "/?workspace_path=Workflow/browser-workflow"
	for _, user := range []string{"bob", "code-only", "unknown"} {
		if w := call(user, path); w.Code != 403 {
			t.Fatalf("workflow access %s: %d", user, w.Code)
		}
	}
	for _, invalid := range []string{path + "/runs/run-one", path + "&profile_id=code", path + "&profile_id=work"} {
		if w := call("alice", invalid); w.Code != 403 {
			t.Fatalf("invalid workflow binding admitted: %s %d", invalid, w.Code)
		}
	}
	var account, workflow, other struct{ Token, Scope string }
	code := call("alice", "/?workspace_path=Chats/Code/projects/one&profile_id=code")
	json.Unmarshal(code.Body.Bytes(), &account)
	first := call("alice", path)
	json.Unmarshal(first.Body.Bytes(), &workflow)
	second := call("charlie", path+"&profile_id=workflow")
	json.Unmarshal(second.Body.Bytes(), &other)
	if first.Code != 200 || second.Code != 200 || workflow.Token != account.Token || workflow.Scope == account.Scope || other.Token == workflow.Token {
		t.Fatalf("workflow pairing: %d %d", first.Code, second.Code)
	}
	server := httptest.NewServer(http.HandlerFunc(api.handleBrowserExtensionConnect))
	defer server.Close()
	socket, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), http.Header{"Origin": []string{"chrome-extension://test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	socket.SetReadDeadline(time.Now().Add(3 * time.Second))
	socket.WriteJSON(map[string]string{"type": "pair", "token": workflow.Token, "scope": workflow.Scope})
	var reply map[string]any
	if err := socket.ReadJSON(&reply); err != nil || reply["type"] != "paired" || reply["profile_id"] != "workflow" {
		t.Fatal("workflow socket", reply, err)
	}
	socket.WriteJSON(map[string]string{"type": "ping"})
	if err := socket.ReadJSON(&reply); err != nil || reply["type"] != "pong" {
		t.Fatal("workflow heartbeat", reply, err)
	}
	if browserrelay.Default.Lookup("charlie", workflow.Scope) != nil {
		t.Fatal("one owner's socket appeared in another owner's binding")
	}
	manifest = `{"version":"1","id":"browser-workflow","label":"Browser workflow","access":{"owners":["charlie"],"readers":["alice"]},"capabilities":{"browser_mode":"auto"}}`
	socket.WriteJSON(map[string]string{"type": "ping"})
	if err := socket.ReadJSON(&reply); err == nil {
		t.Fatal("revoked workflow owner kept browser authority")
	}
	manifest = `{"version":"1","id":"browser-workflow","label":"Browser workflow","kind":"relay","access":{"owners":["charlie"]},"capabilities":{"browser_mode":"auto"}}`
	if w := call("charlie", path); w.Code != 403 {
		t.Fatal("Relay manifest admitted as a workflow")
	}
}
