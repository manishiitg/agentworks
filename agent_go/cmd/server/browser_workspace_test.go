package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
	"github.com/manishiitg/coding-agent-loop/workspace/browserteach"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
)

func TestCodeBrowserLegacyAutomaticUsesWorkspaceBrowser(t *testing.T) {
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "true")
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/missing/") {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"content": `{"mode":"auto","port":9222}`}})
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	for _, tc := range []struct{ path, mode string }{
		{"Chats/Code/projects/legacy", "headless"},
		{"_users/alice/Chats/Code/projects/missing", "headless"},
		{"Chats/Work/projects/legacy", "auto"},
	} {
		settings, err := readWorkspaceBrowserSettings(context.Background(), tc.path)
		if err != nil || settings.Mode != tc.mode {
			t.Fatalf("%s: mode=%s err=%v, want %s", tc.path, settings.Mode, err, tc.mode)
		}
	}
}

func TestWorkspaceBrowserStartsWithoutChatAndEnforcesScope(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"bob","username":"bob","can_create":true,"products":[]},{"id":"stranger","username":"stranger","can_create":true,"products":[]}]}`)
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	t.Setenv("WORKSPACE_API_TOKEN", "browser-test-token")
	var commands []string
	var restores int
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Workspace-Token") != "browser-test-token" {
			t.Error("missing service authentication")
		}
		if strings.HasPrefix(r.URL.Path, "/api/documents/") {
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"content": `{"version":"1","id":"one","label":"One","access":{"owners":["alice"],"readers":["bob"]},"capabilities":{"browser_mode":"none"}}`}})
			return
		}
		if r.URL.Path == "/api/execute" {
			var req browser.ShellExecuteRequest
			json.NewDecoder(r.Body).Decode(&req)
			if r.Header.Get("X-User-ID") != "alice" || req.FolderGuard == nil || !req.FolderGuard.Enabled || req.FolderGuard.BrowserSession != browserSessionForWorkspace("alice", "Workflow/one") || req.WorkingDirectory != "Workflow/one" {
				t.Error("missing trusted account/scope guard")
			}
			commands = append(commands, req.Command)
			out := `{"success":true,"data":{}}`
			if strings.Contains(req.Command, "stream") {
				out = `{"success":true,"data":{"enabled":true}}`
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"stdout": out, "exit_code": 0}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/restore-tabs") {
			restores++
			if r.URL.Path != "/api/browser/live/"+browserSessionForWorkspace("alice", "Workflow/one")+"/restore-tabs" {
				t.Error("wrong restore scope")
			}
			json.NewEncoder(w).Encode(map[string]bool{"success": true})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/teaching") {
			var payload map[string]any
			json.NewDecoder(r.Body).Decode(&payload)
			if payload["workspace_path"] != "Workflow/one" {
				t.Error("caller chose upstream workspace")
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "idle"})
			return
		}
		http.NotFound(w, r)
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	api := &StreamingAPI{}
	call := func(user, route, body string, teaching bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", route, strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user}))
		response := httptest.NewRecorder()
		if teaching {
			api.handleBrowserTeaching(response, req)
		} else {
			api.handleWorkspaceBrowser(response, req)
		}
		return response
	}
	session := browserSessionForWorkspace("alice", "Workflow/one")
	defer browser.GetSessionTracker().Remove(session)
	for _, user := range []string{"", "bob", "stranger"} {
		r := call(user, "/?workspace_path=Workflow/one", `{"action":"start"}`, false)
		if r.Code != 403 {
			t.Fatalf("%q can start: %d %s", user, r.Code, r.Body.String())
		}
	}
	for _, bad := range []string{"Workflow/one/../other", "_users/alice/Chats/Code/projects/p/../../../../../bob", "/Workflow/one"} {
		if r := call("alice", "/?workspace_path="+bad, `{"action":"start"}`, false); r.Code != 403 {
			t.Fatal("Unsafe workspace accepted", bad, r.Code)
		}
	}
	for _, user := range []string{"", "bob", "stranger"} {
		if response := call(user, "/?workspace_path=Workflow/one", `{"action":"recover"}`, false); response.Code != 403 {
			t.Fatal("unwritable recovery was admitted")
		}
	}
	r := call("alice", "/?workspace_path=Workflow/one", `{"action":"start"}`, false)
	if r.Code != 200 || !strings.Contains(r.Body.String(), session) {
		t.Fatalf("cannot start without a chat: %d %s", r.Code, r.Body.String())
	}
	if restores != 1 {
		t.Fatal("startup skipped authorized tab restore")
	}
	if len(commands) != 2 || strings.Contains(commands[0], "--cdp") || strings.Contains(commands[1], "enable") {
		t.Fatalf("wrong startup/stream lifecycle: %v", commands)
	}
	browser.BindViewerCDPPort(session, 9222)
	if response := call("alice", "/?workspace_path=Workflow/one", `{"action":"recover"}`, false); response.Code != 409 {
		t.Fatal("Recovery restarted physical Chrome", response.Code)
	}
	browser.BindViewerCDPPort(session, 0)
	r = call("alice", "/?workspace_path=Workflow/one", `{"action":"save","mode":"cdp","port":9222}`, false)
	if r.Code != 400 {
		t.Fatal("server allowed local Chrome", r.Code)
	}
	req := httptest.NewRequest("POST", "/?workspace_path=Workflow/one", strings.NewReader(`{"action":"status","workspace_path":"Workflow/stolen"}`))
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
	req = mux.SetURLVars(req, map[string]string{"session": session})
	r = httptest.NewRecorder()
	api.handleBrowserTeaching(r, req)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	req = mux.SetURLVars(req, map[string]string{"session": "another-browser"})
	req.Body = http.NoBody
	r = httptest.NewRecorder()
	api.handleBrowserTeaching(r, req)
	if r.Code == 200 {
		t.Fatal("invalid teaching request accepted")
	}
	release, ok := browser.TryTakeBrowserControl(session)
	if !ok {
		t.Fatal("startup leaked control gate")
	}
	r = call("alice", "/?workspace_path=Workflow/one", `{"action":"start"}`, false)
	release()
	if r.Code != 409 {
		t.Fatal("startup interrupted manual control", r.Code)
	}
}

// Qualify the actual user-start bridge, not just a mocked execute response.
func TestWorkspaceBrowserStartsRealChrome(t *testing.T) {
	if os.Getenv("RUN_BROWSER_TEACH_E2E") != "1" {
		t.Skip("set RUN_BROWSER_TEACH_E2E=1 for owned Chrome")
	}
	if _, err := exec.LookPath("agent-browser"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("WORKSPACE_API_TOKEN", "start-real-token")
	t.Setenv("NATIVE_WORKSPACE", "true")
	if executable := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"; func() bool { _, err := os.Stat(executable); return err == nil }() {
		t.Setenv("AGENT_BROWSER_EXECUTABLE_PATH", executable)
	}
	root := t.TempDir()
	viper.Set("docs-dir", root)
	defer viper.Set("docs-dir", "")
	name := fmt.Sprintf("user-start-real-%d", time.Now().UnixNano())
	workspacePath := "Workflow/" + name
	os.MkdirAll(filepath.Join(root, workspacePath), 0700)
	profileRoot, err := os.MkdirTemp("/tmp", "aw-start-profile-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(profileRoot)
	t.Setenv("AGENT_BROWSER_PROFILE_ROOT", filepath.Join(profileRoot, "profiles"))
	// Ensure an installation-wide legacy override cannot reuse any user's profile.
	previous, had := os.LookupEnv("AGENT_BROWSER_SHARED_PROFILE")
	os.Unsetenv("AGENT_BROWSER_SHARED_PROFILE")
	defer func() {
		if had {
			os.Setenv("AGENT_BROWSER_SHARED_PROFILE", previous)
		} else {
			os.Unsetenv("AGENT_BROWSER_SHARED_PROFILE")
		}
	}()
	router := gin.New()
	router.POST("/api/execute", workspacehandlers.ExecuteShellCommand)
	router.POST("/api/browser/live/:session/teaching", workspacehandlers.BrowserTeaching)
	router.POST("/api/browser/live/:session/restore-tabs", workspacehandlers.BrowserRestoreTabs)
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Workspace-Token") != "start-real-token" {
			http.Error(w, "no service token", 403)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/documents/") {
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"content": `{"version":"1","id":"real","label":"Real"}`}})
			return
		}
		router.ServeHTTP(w, r)
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	api := &StreamingAPI{}
	session := browserSessionForWorkspace("alice", workspacePath)
	client := browser.NewClient(workspace.URL)
	defer func() {
		client.ExecuteCommand(context.Background(), append(browser.HeadlessLaunchArgsForSession(session), "--session", session, "close", "--json"), workspaceBrowserExecuteOptions("alice", workspacePath, session, 10*time.Second))
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cleanup := exec.CommandContext(cleanupCtx, "agent-browser", append(browser.HeadlessLaunchArgsForSession(session), "--session", session, "close", "--json")...)
		cleanup.Env = append(os.Environ(), "AGENT_BROWSER_SOCKET_DIR="+browserconfig.SocketDirForSession(session))
		cleanup.Run()
		browser.GetSessionTracker().Remove(session)
		os.RemoveAll(browserconfig.SocketDirForSession(session))
	}()
	start := func() {
		req := httptest.NewRequest("POST", "/?workspace_path="+workspacePath, strings.NewReader(`{"action":"start"}`))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
		response := httptest.NewRecorder()
		api.handleWorkspaceBrowser(response, req)
		if response.Code != 200 {
			t.Fatalf("Real startup failed: %d %s", response.Code, response.Body.String())
		}
	}
	start()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<h1>Sign-in fixture</h1><label>Customer <input id="customer"></label><button onclick="document.getElementById('result').textContent='Hello '+document.getElementById('customer').value">Submit</button><p id="result"></p>`))
	}))
	defer site.Close()
	run := func(args ...string) string {
		t.Helper()
		out, err := client.ExecuteCommand(context.Background(), append(browser.HeadlessLaunchArgsForSession(session), append([]string{"--session", session}, append(args, "--json")...)...), workspaceBrowserExecuteOptions("alice", workspacePath, session, 15*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	run("open", site.URL)
	run("eval", "localStorage.setItem('signed_in','yes')")
	start()
	if out := run("eval", "localStorage.getItem('signed_in')"); !strings.Contains(out, "yes") {
		t.Fatal("Repeated start lost sign-in", out)
	}
	if out := run("get", "url"); !strings.Contains(out, site.URL) {
		t.Fatal("Repeated start reset the page", out)
	}
	call := func(payload map[string]any) struct {
		ID      string                `json:"id"`
		Status  string                `json:"status"`
		Actions []browserteach.Action `json:"actions"`
		Errors  []string              `json:"errors"`
	} {
		t.Helper()
		payload["workspace_path"] = workspacePath
		body, err := forwardTeaching(context.Background(), session, payload)
		if err != nil {
			t.Fatal(err)
		}
		var state struct {
			ID      string                `json:"id"`
			Status  string                `json:"status"`
			Actions []browserteach.Action `json:"actions"`
			Errors  []string              `json:"errors"`
		}
		if err := json.Unmarshal(body, &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	call(map[string]any{"action": "start", "goal": "Greet a customer"})
	run("click", "#customer")
	run("keyboard", "type", "Alice")
	run("press", "Tab")
	run("click", "button")
	state := call(map[string]any{"action": "finish"})
	found := false
	for i, action := range state.Actions {
		if action.Kind == "fill" {
			state.Actions[i].Parameter = "customer"
			found = true
		}
	}
	if !found {
		t.Fatal("service recorder missed demonstrated field edit", state)
	}
	call(map[string]any{"action": "save", "id": state.ID, "actions": state.Actions, "check": map[string]string{"kind": "text", "value": "Hello Bob"}})
	state = call(map[string]any{"action": "test", "id": state.ID, "inputs": map[string]string{"customer": "Bob"}})
	if state.Status != "tested" {
		t.Fatal("service replay failed", state.Errors)
	}
	state = call(map[string]any{"action": "publish", "id": state.ID})
	if state.Status != "saved" {
		t.Fatal("service publication failed", state)
	}
	if out := run("eval", "localStorage.getItem('signed_in')"); !strings.Contains(out, "yes") {
		t.Fatal("teaching lost sign-in", out)
	}

}

func TestViewerCommandsPreserveSelectedRuntime(t *testing.T) {
	session := "workflow-1111111111111111--browser"
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "")
	if args := browserViewerLaunchArgs(session); !strings.Contains(strings.Join(args, " "), "--no-sandbox") {
		t.Fatal("managed viewer omitted native launch flags", args)
	}
	browser.BindViewerCDPPort(session, 9222)
	defer browser.BindViewerCDPPort(session, 0)
	args := strings.Join(browserViewerLaunchArgs(session), " ")
	if !strings.Contains(args, "--cdp") || strings.Contains(args, "--profile") || strings.Contains(args, "--args") {
		t.Fatal("local viewer switched to managed Chrome", args)
	}
}
