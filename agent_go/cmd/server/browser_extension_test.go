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
