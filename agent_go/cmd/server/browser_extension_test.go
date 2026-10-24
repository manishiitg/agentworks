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

func TestChromeExtensionProjectSelectsSavedAccountBrowser(t *testing.T) {
	f := newMultiUserFixture(t, sharedIdentityLayout())
	previous := browserrelay.Default
	browserrelay.Default = browserrelay.New()
	defer func() { browserrelay.Default.Close(); browserrelay.Default = previous }()
	workspace := f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
	projectURL := "/?workspace_path=" + workspace + "&profile_id=work"
	call := func(user, method, endpoint, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, endpoint, strings.NewReader(body)).WithContext(f.Ctx(user))
		w := httptest.NewRecorder()
		f.API.handleBrowserExtension(w, r)
		return w
	}
	account := call(fixtureUserA, http.MethodPost, "/", `{"action":"copy"}`)
	var pair struct{ Token string }
	if account.Code != 200 || json.Unmarshal(account.Body.Bytes(), &pair) != nil || pair.Token == "" {
		t.Fatal("account pairing failed")
	}
	server := httptest.NewServer(http.HandlerFunc(f.API.handleBrowserExtensionConnect))
	defer server.Close()
	socket, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), http.Header{"Origin": []string{"chrome-extension://example"}})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	socket.SetReadDeadline(time.Now().Add(3 * time.Second))
	const browserID = "11111111-1111-4111-8111-111111111111"
	socket.WriteJSON(map[string]string{"type": "pair", "token": pair.Token, "browser_id": browserID, "browser": "Chrome", "version": "0.5.0"})
	var reply map[string]any
	if err := socket.ReadJSON(&reply); err != nil || reply["type"] != "account-paired" {
		t.Fatal("account connection", err)
	}
	status := call(fixtureUserA, http.MethodGet, projectURL, "")
	var before browserrelay.Status
	if status.Code != 200 || json.Unmarshal(status.Body.Bytes(), &before) != nil || !before.AccountConnected || before.Selected {
		t.Fatal("account connection should not select a project")
	}
	for _, action := range []string{"pair", "copy", "reset"} {
		body, _ := json.Marshal(map[string]string{"action": action})
		w := call(fixtureUserA, http.MethodPost, projectURL, string(body))
		if w.Code != 400 || strings.Contains(w.Body.String(), pair.Token) {
			t.Fatalf("retired project action %s: %d", action, w.Code)
		}
	}
	if w := call(fixtureUserA, http.MethodPost, projectURL, `{"action":"connect"}`); w.Code != 400 {
		t.Fatal("browser choice must be explicit")
	}
	selection, _ := json.Marshal(map[string]string{"action": "connect", "browser_id": browserID})
	if w := call(fixtureUserB, http.MethodPost, projectURL, string(selection)); w.Code != 403 {
		t.Fatal("Run user selected another person's browser")
	}
	selected := call(fixtureUserA, http.MethodPost, projectURL, string(selection))
	var after browserrelay.Status
	if selected.Code != 200 || json.Unmarshal(selected.Body.Bytes(), &after) != nil || !after.Selected || after.BrowserID != browserID || strings.Contains(selected.Body.String(), pair.Token) {
		t.Fatalf("project selection failed or returned a credential: %d", selected.Code)
	}
	if err := socket.ReadJSON(&reply); err != nil || reply["type"] != "connect-project" || reply["scope"] != browserSessionForWorkspace(fixtureUserA, workspace) {
		t.Fatal("project did not reuse saved browser", err)
	}
	if !strings.Contains(after.Message, "Connecting it to this project") {
		t.Fatal("selected online browser was shown as offline")
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
		r := httptest.NewRequest(http.MethodGet, "/?workspace_path="+workspace+"&profile_id=work", nil).WithContext(f.Ctx(user))
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
	manifest := `{"schema_version":1,"id":"browser-workflow","label":"Browser workflow","access":{"owners":["alice","charlie","code-only"],"readers":["bob"]},"capabilities":{"browser_mode":"auto"}}`
	// Honor writes from read-time manifest migrations. Returning the original
	// manifest for every GET/PUT recursively reruns migration during changelog
	// authorization, instead of exercising the workspace's actual behavior.
	workspaceServer, documents := newFakeWorkspaceServer(t)
	setManifest := func(content string) {
		documents.mu.Lock()
		documents.files["Workflow/browser-workflow/workflow.json"] = content
		documents.mu.Unlock()
	}
	setManifest(manifest)
	t.Setenv("WORKSPACE_API_URL", workspaceServer.URL)
	previous := browserrelay.Default
	browserrelay.Default = browserrelay.New()
	defer func() { browserrelay.Default.Close(); browserrelay.Default = previous }()
	api := &StreamingAPI{}
	call := func(user, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user}))
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
	if call("alice", path).Code != 200 || call("charlie", path+"&profile_id=workflow").Code != 200 {
		t.Fatal("workflow owners cannot choose their browsers")
	}
	scope := browserSessionForWorkspace("alice", "Workflow/browser-workflow")
	token, err := browserrelay.Default.PairForProfile("alice", scope, "Workflow/browser-workflow", "workflow")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(api.handleBrowserExtensionConnect))
	defer server.Close()
	socket, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), http.Header{"Origin": []string{"chrome-extension://test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	socket.SetReadDeadline(time.Now().Add(3 * time.Second))
	socket.WriteJSON(map[string]string{"type": "pair", "token": token, "scope": scope})
	var reply map[string]any
	if err := socket.ReadJSON(&reply); err != nil || reply["type"] != "paired" || reply["profile_id"] != "workflow" {
		t.Fatal("workflow socket", reply, err)
	}
	socket.WriteJSON(map[string]string{"type": "ping"})
	if err := socket.ReadJSON(&reply); err != nil || reply["type"] != "pong" {
		t.Fatal("workflow heartbeat", reply, err)
	}
	if browserrelay.Default.Lookup("charlie", scope) != nil {
		t.Fatal("one owner's socket appeared in another owner's binding")
	}
	setManifest(`{"schema_version":1,"id":"browser-workflow","label":"Browser workflow","access":{"owners":["charlie"],"readers":["alice"]},"capabilities":{"browser_mode":"auto"}}`)
	socket.WriteJSON(map[string]string{"type": "ping"})
	if err := socket.ReadJSON(&reply); err == nil {
		t.Fatal("revoked workflow owner kept browser authority")
	}
	setManifest(`{"schema_version":1,"id":"browser-workflow","label":"Browser workflow","kind":"relay","access":{"owners":["charlie"]},"capabilities":{"browser_mode":"auto"}}`)
	if w := call("charlie", path); w.Code != 403 {
		t.Fatal("Relay manifest admitted as a workflow")
	}
}
