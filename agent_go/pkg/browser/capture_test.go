package browser

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCaptureUsesManagedServiceAndSessionPermissions(t *testing.T) {
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "/data/browser-profile")
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	t.Setenv("WORKSPACE_API_TOKEN", "test-workspace-token")
	sid := "capture-tool-test"
	common.SetSessionWorkingDir(sid, "Workflow/demo")
	common.SetSessionFolderGuard(sid, []string{"Workflow/demo"}, []string{"Workflow/demo"})
	defer common.ClearSessionShellConfig(sid)
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sid)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/browser/live/shared-browser/recording" || r.Method != http.MethodPost || r.Header.Get("X-Workspace-Token") != "test-workspace-token" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var req struct {
			Action    string             `json:"action"`
			Workspace string             `json:"workspace_path"`
			Guard     *FolderGuardConfig `json:"folder_guard"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Workspace != "Workflow/demo" || req.Guard == nil || !req.Guard.Enabled || len(req.Guard.WritePaths) != 1 || req.Guard.WritePaths[0] != "Workflow/demo" {
			t.Errorf("wrong trusted context: %+v", req)
		}
		if req.Action == "stop" {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":"Recording belongs to a different workflow"}`))
			return
		}
		_, _ = w.Write([]byte(`{"recording":true,"directory":"Workflow/demo/browser-recordings/test"}`))
	}))
	defer server.Close()
	e := NewExecutor(NewClient(server.URL))
	for _, action := range []string{"status", "start"} {
		out, err := e.HandleAgentBrowser(ctx, map[string]interface{}{"command": "capture", "args": []string{action}, "session": "anything", "workspace_path": "Workflow/other"})
		if err != nil || !strings.Contains(out, `"recording":true`) {
			t.Fatalf("%s: %s %v", action, out, err)
		}
	}
	_, err := e.HandleAgentBrowser(ctx, map[string]interface{}{"command": "capture", "args": []string{"stop"}, "session": "main"})
	if err == nil || !strings.Contains(err.Error(), "different workflow") {
		t.Fatalf("lost service error: %v", err)
	}
	for _, bad := range [][]string{nil, {"start", "/tmp/output"}, {"restart"}} {
		if _, err := e.HandleAgentBrowser(ctx, map[string]interface{}{"command": "capture", "args": bad, "session": "main"}); err == nil {
			t.Fatal("invalid capture accepted")
		}
	}
	if _, err := e.HandleAgentBrowser(context.Background(), map[string]interface{}{"command": "capture", "args": []string{"start"}, "session": "main"}); err == nil {
		t.Fatal("unguarded capture accepted")
	}
	if calls != 3 {
		t.Fatalf("invalid commands reached backend: %d", calls)
	}
}

// Issue #210: Crew sessions capture into their own project root, like
// workflows capture into Workflow/<name>. The fixture is a real Crew shape
// (physical per-user path, browser_mode none): modeling a fixed product
// hid the last Crew regression (PLAT-322).
func TestCaptureAcceptsCrewProjectRoot(t *testing.T) {
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "/data/browser-profile")
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	t.Setenv("WORKSPACE_API_TOKEN", "test-workspace-token")
	sid := "capture-crew-test"
	physical := "_users/u1/Chats/Work/projects/demo"
	common.SetSessionWorkingDir(sid, physical)
	common.SetSessionFolderGuard(sid, []string{physical}, []string{physical})
	defer common.ClearSessionShellConfig(sid)
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sid)
	ctx = context.WithValue(ctx, common.UserIDKey, "u1")
	var workspace string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Workspace string `json:"workspace_path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		workspace = req.Workspace
		_, _ = w.Write([]byte(`{"recording":false}`))
	}))
	defer server.Close()
	e := NewExecutor(NewClient(server.URL))
	out, err := e.HandleAgentBrowser(ctx, map[string]interface{}{"command": "capture", "args": []string{"status"}, "session": "main"})
	if err != nil || !strings.Contains(out, `"recording":false`) {
		t.Fatalf("crew capture refused: %s %v", out, err)
	}
	if workspace != "Chats/Work/projects/demo" {
		t.Fatalf("crew capture bound to %q", workspace)
	}

	// The same physical path without the owning user still denies: another
	// user's project must never become capturable.
	for _, caller := range []string{"", "u2"} {
		other := context.WithValue(context.Background(), common.ChatSessionIDKey, sid)
		other = context.WithValue(other, common.UserIDKey, caller)
		if _, err := e.HandleAgentBrowser(other, map[string]interface{}{"command": "capture", "args": []string{"status"}, "session": "main"}); err == nil || !strings.Contains(err.Error(), "CAPTURE_ACCESS_DENIED") {
			t.Fatalf("crew capture accepted for non-owner %q: %v", caller, err)
		}
	}
}
func TestCaptureHonorsDisabledAndCDPModes(t *testing.T) {
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "true")
	for _, tt := range []struct {
		mode  string
		ports []int
		args  []string
		want  string
	}{
		{"none", nil, []string{"start"}, "BROWSER_DISABLED"},
		{"cdp", []int{9222}, []string{"--cdp", "http://localhost:9222", "start"}, "CAPTURE_UNSUPPORTED"},
	} {
		e := NewExecutor(NewClient("http://invalid"), WithBrowserRuntimeConfig(NewBrowserRuntimeConfig(tt.mode, tt.ports)))
		_, err := e.HandleAgentBrowser(context.Background(), map[string]interface{}{"command": "capture", "args": tt.args, "session": "main"})
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v", tt.mode, err)
		}
	}
}
