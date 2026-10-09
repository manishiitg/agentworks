package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestLiveBrowserSessionIsolation(t *testing.T) {
	tracker := browser.GetSessionTracker()
	tracker.Touch("live-own", "run-own", "run-own")
	tracker.Touch("live-other", "run-other", "run-other")
	defer tracker.Remove("live-own")
	defer tracker.Remove("live-other")
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{
		"run-own":   {UserID: "alice", WorkspacePath: "Workflow/one"},
		"run-other": {UserID: "bob", WorkspacePath: "Workflow/one"},
	}}
	request := httptest.NewRequest("GET", "/?workspace_path=Workflow/one", nil)
	request = request.WithContext(context.WithValue(request.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
	items := api.liveBrowserSessions(request)
	if len(items) != 1 || items[0]["browser_session"] != "live-own" {
		t.Fatalf("wrong sessions: %v", items)
	}
	request.URL.RawQuery = "workspace_path=Workflow/two"
	if len(api.liveBrowserSessions(request)) != 0 {
		t.Fatal("cross-workflow leak")
	}
	request.URL.RawQuery = ""
	if len(api.liveBrowserSessions(request)) != 0 {
		t.Fatal("unscoped sessions exposed")
	}
}

func TestLiveBrowserStreamWatchControlAndDisconnect(t *testing.T) {
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	t.Setenv("MULTI_USER_MODE", "false")
	const session = "live-stream-test"
	browser.GetSessionTracker().Touch(session, "run", "run")
	defer browser.GetSessionTracker().Remove(session)
	received := make(chan map[string]interface{}, 8)
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/documents/") {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]string{"content": `{"version":"1","id":"test","label":"Test"}`}})
			return
		}
		if r.Header.Get("X-Workspace-Token") != "test-service-token" {
			t.Error("missing service auth")
			http.Error(w, "auth", 401)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/text") {
			if r.Header.Get("X-User-ID") != "alice" || r.URL.Path != "/api/browser/live/"+session+"/text" {
				t.Error("paste scope or caller changed")
			}
			var request map[string]interface{}
			json.NewDecoder(r.Body).Decode(&request)
			received <- request
			json.NewEncoder(w).Encode(map[string]bool{"success": true})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/teaching") {
			var request map[string]interface{}
			json.NewDecoder(r.Body).Decode(&request)
			received <- request
			json.NewEncoder(w).Encode(map[string]interface{}{"status": "recording", "id": "test"})
			return
		}
		if r.URL.Path == "/api/execute" {
			var request map[string]interface{}
			json.NewDecoder(r.Body).Decode(&request)
			received <- request
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"stdout": func() string {
				if strings.Contains(fmt.Sprint(request["command"]), "eval") {
					return `{"success":true,"data":{"result":"selected fixture text"}}`
				}
				return "ok"
			}(), "exit_code": 0}})
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.WriteJSON(map[string]interface{}{"type": "frame", "data": "test-frame"})
		for {
			var message map[string]interface{}
			if conn.ReadJSON(&message) != nil {
				return
			}
			if message["type"] != "config" {
				received <- message
			}
		}
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	t.Setenv("WORKSPACE_API_TOKEN", "test-service-token")
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{"run": {UserID: "alice", WorkspacePath: "Workflow/one"}}}
	router := mux.NewRouter()
	router.HandleFunc("/api/browser/live/{session}/stream", api.handleLiveBrowserStream)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		router.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "alice"})))
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1)+"/api/browser/live/"+session+"/stream?workspace_path=Workflow/one", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readType := func(kind string) map[string]interface{} {
		t.Helper()
		for {
			var message map[string]interface{}
			if err := conn.ReadJSON(&message); err != nil {
				t.Fatal(err)
			}
			if message["type"] == "viewer_error" {
				t.Fatalf("viewer error: %v", message)
			}
			if message["type"] == kind {
				return message
			}
		}
	}
	readType("viewer_control")
	conn.WriteJSON(map[string]interface{}{"type": "input_mouse", "eventType": "mousePressed", "x": 10, "y": 10})
	conn.WriteJSON(map[string]interface{}{"type": "resize_viewport", "width": 900, "height": 1200})
	conn.WriteJSON(map[string]string{"type": "new_tab", "url": "https://example.com"})
	conn.WriteJSON(map[string]string{"type": "switch_tab", "tab": "t2"})
	conn.WriteJSON(map[string]string{"type": "input_text", "text": "watch-only must not paste"})
	conn.WriteJSON(map[string]string{"type": "clipboard_copy", "requestId": "copy-1"})
	conn.WriteJSON(map[string]string{"type": "history", "action": "reload"})
	conn.WriteJSON(map[string]string{"type": "ping"})
	readType("pong")
	select {
	case value := <-received:
		t.Fatalf("watch mode forwarded input %v", value)
	default:
	}
	conn.WriteJSON(map[string]string{"type": "take_control"})
	if readType("viewer_control")["controlling"] != true {
		t.Fatal("control not acquired")
	}
	conn.WriteJSON(map[string]string{"type": "input_text", "text": "paste fixture\n日本語🙂"})
	select {
	case request := <-received:
		if request["text"] != "paste fixture\n日本語🙂" || len(request) != 1 {
			t.Fatal(request)
		}
	case <-time.After(time.Second):
		t.Fatal("paste did not reach trusted endpoint")
	}
	conn.WriteJSON(map[string]string{"type": "clipboard_copy", "requestId": "copy-2"})
	if reply := readType("clipboard"); reply["text"] != "selected fixture text" || reply["requestId"] != "copy-2" {
		t.Fatal(reply)
	}
	select {
	case request := <-received:
		if !strings.Contains(fmt.Sprint(request["command"]), "eval") {
			t.Fatal(request)
		}
		guard, _ := request["folder_guard"].(map[string]interface{})
		if guard["browser_session"] != session || request["working_directory"] != "Workflow/one" {
			t.Fatal(request)
		}
	case <-time.After(time.Second):
		t.Fatal("copy did not use scoped executor")
	}
	conn.WriteJSON(map[string]string{"type": "history", "action": "eval"})
	conn.WriteJSON(map[string]string{"type": "history", "action": "reload"})
	select {
	case request := <-received:
		if !strings.Contains(fmt.Sprint(request["command"]), "reload") || strings.Contains(fmt.Sprint(request["command"]), "eval") {
			t.Fatal(request)
		}
	case <-time.After(time.Second):
		t.Fatal("reload did not execute")
	}
	conn.WriteJSON(map[string]interface{}{"type": "resize_viewport", "width": 900, "height": 1200})
	select {
	case value := <-received:
		command, _ := value["command"].(string)
		if !strings.Contains(command, "viewport") || !strings.Contains(command, "1200") {
			t.Fatalf("unexpected resize command: %v", value)
		}
	case <-time.After(time.Second):
		t.Fatal("resize not executed")
	}
	conn.WriteJSON(map[string]string{"type": "teach_start", "goal": "Use tabs"})
	readType("teaching")
	select {
	case request := <-received:
		if request["action"] != "start" {
			t.Fatal(request)
		}
	case <-time.After(time.Second):
		t.Fatal("Teaching did not start")
	}
	for _, change := range []map[string]string{{"type": "new_tab", "url": "https://example.com"}, {"type": "switch_tab", "tab": "t2"}} {
		conn.WriteJSON(change)
		for _, expected := range []string{"flush", "command", "select_tab"} {
			select {
			case request := <-received:
				if expected == "command" {
					command, _ := request["command"].(string)
					if !strings.Contains(command, "tab") {
						t.Fatal(request)
					}
				} else if request["action"] != expected {
					t.Fatal(request)
				}
			case <-time.After(time.Second):
				t.Fatalf("Missing tab operation %s", expected)
			}
		}
	}
	conn.WriteJSON(map[string]string{"type": "navigate", "url": "https://example.com/second"})
	for _, expected := range []string{"flush", "prepare_navigation", "command"} {
		select {
		case request := <-received:
			if expected == "command" {
				command, _ := request["command"].(string)
				if !strings.Contains(command, "open") || !strings.Contains(command, "https://example.com/second") {
					t.Fatal(request)
				}
			} else if request["action"] != expected {
				t.Fatal(request)
			}
		case <-time.After(time.Second):
			t.Fatalf("Missing navigation operation %s", expected)
		}
	}
	conn.WriteJSON(map[string]interface{}{"type": "resize_viewport", "width": 900, "height": 99999})
	var rejected map[string]interface{}
	for {
		if err := conn.ReadJSON(&rejected); err != nil {
			t.Fatal(err)
		}
		if rejected["type"] == "viewer_error" {
			break
		}
	}
	select {
	case value := <-received:
		t.Fatalf("invalid resize executed: %v", value)
	default:
	}
	if release, ok := browser.TryTakeBrowserControl(session); ok {
		release()
		t.Fatal("control gate not held")
	}
	conn.WriteJSON(map[string]interface{}{"type": "input_keyboard", "eventType": "char", "text": "hello"})
	select {
	case value := <-received:
		if value["text"] != "hello" {
			t.Fatal(value)
		}
	case <-time.After(time.Second):
		t.Fatal("control input not forwarded")
	}
	conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := browser.AcquireBrowserAutomation(ctx, session)
	if err != nil {
		t.Fatal("disconnect left browser blocked", err)
	}
	release()
}

func TestLiveBrowserCannotBypassWorkflowProxy(t *testing.T) {
	for _, route := range []struct{ method, path string }{{"GET", "stream"}, {"POST", "text"}} {
		response := httptest.NewRecorder()
		workspaceProxyHandler().ServeHTTP(response, httptest.NewRequest(route.method, "/api/wp/api/browser/live/other/"+route.path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatal(route.path, response.Code)
		}
	}
}

// Opt-in integration check against the installed CLI and real headless Chrome.
func TestLiveBrowserRealHeadless(t *testing.T) {
	if os.Getenv("RUN_LIVE_BROWSER_E2E") != "1" {
		t.Skip("set RUN_LIVE_BROWSER_E2E=1")
	}
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	name := fmt.Sprintf("aw-live-e2e-%d", time.Now().UnixNano())
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, "agent-browser", append([]string{"--session", name}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("agent-browser failed: %v %s", err, output)
		}
		return string(output)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "agent-browser", "--session", name, "close").Run()
	}()
	run("open", "data:text/html,<html><body><h1>Live browser test</h1><input aria-label='Test input' style='position:absolute;left:20px;top:100px;width:200px;height:40px'></body></html>")
	router := gin.New()
	router.GET("/api/browser/live/:session/stream", workspacehandlers.BrowserLiveStream)
	router.POST("/api/browser/live/:session/text", workspacehandlers.BrowserViewerText)
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	streamURL := strings.Replace(workspace.URL, "http", "ws", 1) + "/api/browser/live/" + name + "/stream"

	conn, _, err := websocket.DefaultDialer.Dial(streamURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	gotFrame, gotTabs := false, false
	for !gotFrame || !gotTabs {
		var message map[string]interface{}
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message["type"] == "frame" {
			gotFrame = len(message["data"].(string)) > 100
		}
		if message["type"] == "tabs" {
			gotTabs = len(message["tabs"].([]interface{})) > 0
		}
	}
	for _, eventType := range []string{"mousePressed", "mouseReleased"} {
		if err := conn.WriteJSON(map[string]interface{}{"type": "input_mouse", "eventType": eventType, "x": 30, "y": 110, "button": "left", "clickCount": 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.WriteJSON(map[string]string{"type": "input_keyboard", "eventType": "keyDown", "key": "a", "code": "KeyA", "text": "a"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		result := run("eval", "document.querySelector('input').value", "--json")
		if strings.Contains(result, `"result":"a"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("input did not reach headless browser: %s", result)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Paste uses the stream, not a host-wide browser clipboard. Selection copying
	// reads only the current selection, including multiline text and shadow DOM.
	run("eval", `document.body.insertAdjacentHTML('beforeend', '<textarea aria-label="Notes"></textarea><div id="shadow"></div><iframe srcdoc="<input value=frame-text>"></iframe>'); document.querySelector('#shadow').attachShadow({mode:'open'}).innerHTML='<input value=shadow-text>'; true`, "--json")
	text := "line one\n日本語🙂 \"quoted\" $()"
	run("eval", `document.querySelector('textarea').focus(); true`, "--json")
	body, _ := json.Marshal(map[string]string{"text": text})
	response, err := http.Post(workspace.URL+"/api/browser/live/"+name+"/text", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("paste status %d", response.StatusCode)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		output := run("eval", `document.querySelector('textarea').select(); document.querySelector('textarea').value`, "--json")
		actual, err := liveBrowserSelection(output)
		if err == nil && actual == text {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("multiline paste did not reach browser: %s", output)
		}
		time.Sleep(50 * time.Millisecond)
	}
	selected, err := liveBrowserSelection(run("eval", liveBrowserSelectionScript, "--json"))
	if err != nil || selected != text {
		t.Fatalf("copy failed: %q %v", selected, err)
	}
	for _, fixture := range []struct{ script, expected string }{
		{`(() => { const e=document.querySelector('#shadow').shadowRoot.querySelector('input'); e.focus(); e.select(); return true; })()`, "shadow-text"},
		{`(() => { const e=document.querySelector('iframe').contentDocument.querySelector('input'); e.focus(); e.select(); return true; })()`, "frame-text"},
	} {
		run("eval", fixture.script, "--json")
		selected, err = liveBrowserSelection(run("eval", liveBrowserSelectionScript, "--json"))
		if err != nil || selected != fixture.expected {
			t.Fatalf("selection failed: %q %v", selected, err)
		}
	}

}

func TestUserBrowserDiscoveryHonorsOwnershipAndWorkflowAccess(t *testing.T) {
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "/data/browser-profile")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"bob","username":"bob","can_create":true,"products":[]},{"id":"outsider","username":"outsider","can_create":true,"products":[]}]}`)
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]string{"content": `{"version":"1","id":"shared-view-check","label":"Shared check","capabilities":{"browser_mode":"auto"},"access":{"owners":["alice"],"readers":["bob"]}}`}})
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	api := &StreamingAPI{}
	// A product browser belonging to a workflow owner must not be exposed as
	// that workflow's browser, even though the owner may access both.
	productBrowser := browserSessionForWorkspace("alice", "Chats/SparkQuill")
	browser.GetSessionTracker().Touch(productBrowser, "product-chat", "product-chat")
	defer browser.GetSessionTracker().Remove(productBrowser)
	name := common.PrefixBrowserSessionID(common.WorkflowBrowserSessionNamespace("Workflow/shared-view-check") + "--browser")
	browser.GetSessionTracker().Touch(name, "old-chat", "old-chat")
	defer browser.GetSessionTracker().Remove(name)
	otherWorkflow := common.PrefixBrowserSessionID(common.WorkflowBrowserSessionNamespace("Workflow/private-work") + "--browser")
	browser.GetSessionTracker().Touch(otherWorkflow, "other-chat", "other-chat")
	defer browser.GetSessionTracker().Remove(otherWorkflow)
	for _, user := range []string{"alice", "bob", "outsider"} {
		r := httptest.NewRequest("GET", "/?workspace_path=Workflow/shared-view-check", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user}))
		items := api.liveBrowserSessions(r)
		if user == "outsider" {
			if len(items) != 0 {
				t.Fatal("inaccessible workflow exposed browser")
			}
			continue
		}
		if len(items) != 1 || items[0]["browser_session"] != common.PrefixBrowserSessionID(common.WorkflowBrowserSessionNamespace("Workflow/shared-view-check")+"--browser") {
			t.Fatalf("%s: %v", user, items)
		}
	}
}

func TestProductBrowserDiscoveryUsesActualUserBinding(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	workspace := httptest.NewServer(http.NotFoundHandler())
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	api := &StreamingAPI{}
	common.BindSessionBrowserIsolationForProject("live-product-binding-test", "_users/default/Chats/SparkQuill")
	defer common.ClearSessionShellConfig("live-product-binding-test")
	name := common.ResolveBrowserSessionID("live-product-binding-test", "default")
	browser.GetSessionTracker().Touch(name, "live-product-binding-test", "live-product-binding-test")
	defer browser.GetSessionTracker().Remove(name)
	for _, user := range []string{"default", "other-user"} {
		r := httptest.NewRequest("GET", "/?workspace_path=Chats/SparkQuill", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user}))
		items := api.liveBrowserSessions(r)
		if user == "default" {
			if len(items) != 1 || items[0]["browser_session"] != name {
				t.Fatalf("product browser missing: %v", items)
			}
		} else if len(items) != 0 {
			t.Fatalf("another user's browser was exposed: %v", items)
		}
	}
}

// Fixed-workspace products (SparkQuill, ...) have no workflow.json
// at their workspace path, so workflowAccessForWorkspacePath always returns a
// nil manifest for them. Confirmed live: this made every SparkQuill managed-
// browser session invisible to /api/browser/live/sessions, since the discovery
// code previously required a non-nil manifest with capabilities.browser_mode
// set before showing a user-scoped shared-profile session at all.
//
// Outside multi-user mode (every fixed-workspace product deployment today:
// one account per deployment) the fix trusts the deterministic session
// identity alone, matching how the deployment already has exactly one user.
func TestUserBrowserDiscoveryShowsFixedWorkspaceProductSessionInSingleUserMode(t *testing.T) {
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "/data/browser-profile")
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	api := &StreamingAPI{}
	session := browserSessionForWorkspace("default", "Chats/SparkQuill")
	browser.GetSessionTracker().Touch(session, "default-chat", "default-chat")
	defer browser.GetSessionTracker().Remove(session)

	r := httptest.NewRequest("GET", "/?workspace_path=Chats/SparkQuill", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "default"}))
	items := api.liveBrowserSessions(r)
	if len(items) != 1 || items[0]["browser_session"] != session {
		t.Fatalf("expected the single-user session to be visible, got %v", items)
	}
}

func TestUserBrowserDiscoveryShowsOnlyTheSelectedCrewBrowser(t *testing.T) {
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "/data/browser-profile")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":["work"]}]}`)
	workspaceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Real Crew projects have a workflow.json. The discovery path must not
		// mistake that shared project manifest for a Workflow whose browser is
		// disabled by capabilities.browser_mode.
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]string{"content": `{"version":"1","id":"crew-a","capabilities":{"browser_mode":"none"}}`},
		})
	}))
	defer workspaceServer.Close()
	t.Setenv("WORKSPACE_API_URL", workspaceServer.URL)

	const workspace = "Chats/Work/projects/crew-a"
	api := &StreamingAPI{}
	crewA := browserSessionForWorkspace("alice", workspace)
	crewB := browserSessionForWorkspace("alice", "Chats/Work/projects/crew-b")
	browser.GetSessionTracker().Touch(crewA, "crew-a-chat", "crew-a-chat")
	browser.GetSessionTracker().Touch(crewB, "crew-b-chat", "crew-b-chat")
	defer browser.GetSessionTracker().Remove(crewA)
	defer browser.GetSessionTracker().Remove(crewB)

	r := httptest.NewRequest("GET", "/?workspace_path="+workspace, nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
	items := api.liveBrowserSessions(r)
	if len(items) != 1 || items[0]["browser_session"] != crewA || items[0]["label"] != "Crew browser" {
		t.Fatalf("expected only selected Crew browser %q, got %v", crewA, items)
	}
}

// One browser per Crew, not per user: the owner (logical path) and a member
// (physical path) name the same browser, a Crew member may watch it, an
// outsider without the Crew product may not, and only the owner may control it.
func TestCrewBrowserIsSharedWithCrewMembersOnly(t *testing.T) {
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "/data/browser-profile")
	fx := newCrewRunModeFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"aman","can_create":true,"products":["work"]},{"id":"reader","username":"vaibhav","can_create":true,"products":["work"]},{"id":"stranger","username":"stranger","can_create":true,"products":["video-studio"]}]}`)
	api := fx.api
	const physical = crewRunModeOwnerRoot

	ownerView := browserSessionForWorkspace("owner", "Chats/Work/projects/alpha")
	if got := browserSessionForWorkspace("reader", physical); got != ownerView {
		t.Fatalf("owner and member got different Crew browsers: %q vs %q", ownerView, got)
	}
	if got := browserSessionForWorkspace("owner", physical); got != ownerView {
		t.Fatalf("owner's logical and physical paths split the Crew browser: %q vs %q", ownerView, got)
	}
	if !browser.IsUserBrowserSession(ownerView) {
		t.Fatalf("Crew browser %q is not recognized as a persistent managed browser", ownerView)
	}
	// Binding a conversation matches discovery, whoever opens the Crew.
	for _, c := range []struct{ session, user, workspace string }{{"crew-owner-chat", "owner", "Chats/Work/projects/alpha"}, {"crew-reader-chat", "reader", physical}} {
		bindConversationBrowserIsolation(c.session, c.user, c.workspace, nil)
		defer common.ClearSessionShellConfig(c.session)
		if got := common.ResolveBrowserSessionID(c.session, "default"); got != ownerView {
			t.Fatalf("%s bound %q, want %q", c.user, got, ownerView)
		}
	}
	if browserSessionForWorkspace("owner", "Workflow/alpha") == ownerView {
		t.Fatal("Crew and Workflow browsers collided")
	}

	browser.GetSessionTracker().Touch(ownerView, "crew-owner-chat", "crew-owner-chat")
	defer browser.GetSessionTracker().Remove(ownerView)
	browser.GetSessionTracker().RecordAction(ownerView, "fill", []string{"#password", "hunter2"})
	for _, c := range []struct {
		user, workspace string
		visible         bool
	}{{"owner", "Chats/Work/projects/alpha", true}, {"reader", physical, true}, {"stranger", physical, false}} {
		r := httptest.NewRequest("GET", "/?workspace_path="+c.workspace, nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: c.user}))
		items := api.liveBrowserSessions(r)
		if !c.visible {
			if len(items) != 0 {
				t.Fatalf("%s: outsider saw the Crew browser: %v", c.user, items)
			}
			continue
		}
		if len(items) != 1 || items[0]["browser_session"] != ownerView {
			t.Fatalf("%s: expected the Crew browser, got %v", c.user, items)
		}
		if items[0]["last_action"] != `Typed into "#password"` || items[0]["last_action_at"] == "" {
			t.Fatalf("%s: last action = %q at %q", c.user, items[0]["last_action"], items[0]["last_action_at"])
		}
	}
	if !api.canControlLiveBrowser(context.Background(), &UserClaims{UserID: "owner"}, physical) {
		t.Fatal("the Crew owner must control the Crew browser")
	}
	for _, user := range []string{"reader", "stranger"} {
		if api.canControlLiveBrowser(context.Background(), &UserClaims{UserID: user}, physical) {
			t.Fatalf("%s must not control the Crew browser", user)
		}
	}
}

func TestCanControlLiveBrowserTreatsManifestBackedCrewAsProductWorkspace(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":["work"]}]}`)
	workspaceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]string{"content": `{"version":"1","id":"crew-a","capabilities":{"browser_mode":"none"}}`},
		})
	}))
	defer workspaceServer.Close()
	t.Setenv("WORKSPACE_API_URL", workspaceServer.URL)

	const publicWorkspace = "Chats/Work/projects/crew-a"
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{
		"crew-a-chat": {
			SessionID:     "crew-a-chat",
			UserID:        "alice",
			WorkspacePath: "_users/alice/Chats/Work/projects/crew-a",
		},
	}}
	if !api.canControlLiveBrowser(context.Background(), &UserClaims{UserID: "alice"}, publicWorkspace) {
		t.Fatal("expected the owner to control the active Crew browser even though the Crew has workflow.json")
	}
	if api.canControlLiveBrowser(context.Background(), &UserClaims{UserID: "bob"}, "_users/alice/Chats/Work/projects/crew-a") {
		t.Fatal("another user must not control the Crew browser")
	}
}

// A fixed-workspace product project (SparkQuill, ...) is owner-
// qualified: the logical path always names the caller's own tree, so two users
// at "Chats/SparkQuill" get distinct browsers and neither sees the other's.
func TestUserBrowserDiscoveryKeepsFixedWorkspaceProjectBrowsersPerOwnerInMultiUserMode(t *testing.T) {
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "/data/browser-profile")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"bob","username":"bob","can_create":true,"products":[]}]}`)
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	api := &StreamingAPI{activeSessions: make(map[string]*ActiveSessionInfo)}
	aliceSession := browserSessionForWorkspace("alice", "Chats/SparkQuill")
	if aliceSession == browserSessionForWorkspace("bob", "Chats/SparkQuill") {
		t.Fatal("two users' own projects shared a browser")
	}
	browser.GetSessionTracker().Touch(aliceSession, "alice-chat", "alice-chat")
	defer browser.GetSessionTracker().Remove(aliceSession)

	for _, c := range []struct {
		user, workspace string
		visible         bool
	}{{"alice", "Chats/SparkQuill", true}, {"bob", "Chats/SparkQuill", false}, {"bob", "_users/alice/Chats/SparkQuill", false}} {
		r := httptest.NewRequest("GET", "/?workspace_path="+c.workspace, nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: c.user}))
		items := api.liveBrowserSessions(r)
		if c.visible != (len(items) == 1 && items[0]["browser_session"] == aliceSession) || (!c.visible && len(items) != 0) {
			t.Fatalf("%s at %s: got %v", c.user, c.workspace, items)
		}
	}
}

// canControlLiveBrowser is the "take control" gate; it must apply the exact
// same fixed-workspace-product fallback liveBrowserSessions does, or nobody
// could ever take control of a SparkQuill live browser (manifest is
// always nil for them) even after they became visible in the session list.
func TestCanControlLiveBrowserAppliesSameFixedWorkspaceFallbackAsDiscovery(t *testing.T) {
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "/data/browser-profile")
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	t.Run("single user mode trusts the account", func(t *testing.T) {
		api := &StreamingAPI{}
		if !api.canControlLiveBrowser(context.Background(), &UserClaims{UserID: "default"}, "Chats/SparkQuill") {
			t.Fatal("expected control to be granted outside multi-user mode")
		}
	})

	t.Run("multi user mode grants the owner only", func(t *testing.T) {
		t.Setenv("MULTI_USER_MODE", "true")
		withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"bob","username":"bob","can_create":true,"products":[]}]}`)
		api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{}}
		if !api.canControlLiveBrowser(context.Background(), &UserClaims{UserID: "alice"}, "Chats/SparkQuill") {
			t.Fatal("expected alice to control her own project browser")
		}
		if api.canControlLiveBrowser(context.Background(), &UserClaims{UserID: "bob"}, "_users/alice/Chats/SparkQuill") {
			t.Fatal("bob must not control alice's project browser")
		}
	})
}

// PLAT-673: a member with the read-only workflow role still owns their Code
// project, so its browser is theirs to use. Requiring the workflow role left
// every Code browser on server B stuck loading (all status checks 403).
func TestCodeBrowserNeedsProjectOwnershipNotTheWorkflowRole(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","products":["code"]}]}`)
	workspaceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]string{"content": `{"version":"1","id":"code-a","capabilities":{"browser_mode":"auto"}}`},
		})
	}))
	defer workspaceServer.Close()
	t.Setenv("WORKSPACE_API_URL", workspaceServer.URL)

	claims := &UserClaims{UserID: "alice", Username: "alice"}
	if workflowPermissionInfoForClaims(claims).CanWriteWorkflows {
		t.Fatal("test setup: alice must have the read-only workflow role")
	}
	const workspace = "Chats/Code/projects/code-a"
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{
		"code-a-chat": {SessionID: "code-a-chat", UserID: "alice", WorkspacePath: "_users/alice/Chats/Code/projects/code-a"},
	}}
	r := httptest.NewRequest(http.MethodGet, "/api/browser/extension", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
	if _, err := api.browserWorkspaceAccess(r, workspace, "code", true); err != nil {
		t.Fatalf("owner with read-only workflow role was refused their Code browser: %v", err)
	}
	other := r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "bob", Username: "bob"}))
	if _, err := api.browserWorkspaceAccess(other, "_users/alice/Chats/Code/projects/code-a", "code", true); err == nil {
		t.Fatal("another user must not get alice's Code browser")
	}
}
