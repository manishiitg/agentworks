package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// Drive the real preview executor and workspace HTTP serialization. Slot
// servers require both the authenticated caller and an enabled folder guard,
// including for browser helpers, screenshots and the deferred close.
func TestReportPreviewCarriesCallerAndIsolatedGuard(t *testing.T) {
	t.Setenv("AUTH_SECRET", "report-preview-test-secret-0123456789abcdef")
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "/tmp/preview-test-shared-profile")
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "true") // preview must override this
	const workspace = "Workflow/demo"
	const parent = "preview-parent"
	common.SetSessionFolderGuard(parent, []string{workspace + "/"}, []string{workspace + "/"})
	common.SetSessionFolderGuardBlockedPaths(parent, []string{workspace + "/private/"})
	common.SetSessionFolderGuardBlockedWritePaths(parent, []string{workspace + "/planning/"})
	common.BindSessionBrowserIsolationForWorkflow(parent, workspace)
	defer common.ClearSessionShellConfig(parent)
	parentConfig := common.GetSessionShellConfig(parent)
	previousBrowser := ""

	for _, withClaims := range []bool{true, false} {
		name := "workshop"
		if withClaims {
			name = "api-mcp"
		}
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []browser.ShellExecuteRequest
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req browser.ShellExecuteRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode workspace command: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.URL.Path != "/api/execute" || r.Header.Get("X-User-ID") != "preview-owner" || req.FolderGuard == nil || !req.FolderGuard.Enabled {
					t.Errorf("slot command missing caller or guard: path=%s user=%q guard=%+v", r.URL.Path, r.Header.Get("X-User-ID"), req.FolderGuard)
					w.WriteHeader(http.StatusForbidden)
					return
				}
				mu.Lock()
				requests = append(requests, req)
				mu.Unlock()
				stdout := `{"success":true,"data":{"tabs":[{"tabId":"t0","active":true}]}}`
				if strings.Contains(req.Command, " eval ") {
					result := ""
					switch {
					case strings.Contains(req.Command, "data-preview-state"):
						result = "ready"
					case strings.Contains(req.Command, "getUIChecks()"):
						result = `{"viewport_width":768,"frame_width":768,"content_width":768}`
					case strings.Contains(req.Command, "getState()"):
						result = `{"previewState":"ready","report":{"title":"Demo","height":200}}`
					case strings.Contains(req.Command, "scrollHeight"):
						result = "200"
					}
					encoded, _ := json.Marshal(map[string]interface{}{"success": true, "data": map[string]interface{}{"result": result}})
					stdout = string(encoded)
				}
				if req.ArtifactTransfer != nil {
					encoded, _ := json.Marshal(map[string]interface{}{"success": true, "path": req.ArtifactTransfer.SourcePath})
					stdout = string(encoded)
				}
				_ = json.NewEncoder(w).Encode(browser.APIResponse{Success: true, Data: browser.ShellExecuteResponse{Stdout: stdout}})
			}))
			defer service.Close()
			t.Setenv("WORKSPACE_API_URL", service.URL)
			t.Setenv("MCP_API_URL", "https://example.com")

			ctx := context.WithValue(context.Background(), common.UserIDKey, "unrelated-context-user")
			ctx = context.WithValue(ctx, common.BrowserDownloadsPathKey, "unrelated/downloads")
			callerID, callerSession := "preview-owner", parent
			if withClaims {
				ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "preview-owner", Username: "preview-owner"})
				callerID, callerSession = "unrelated-argument-user", "dashboard-preview-owner"
			}
			out, err := (&StreamingAPI{}).runReportPreview(ctx, callerSession, callerID, workspace, map[string]interface{}{"theme": "dark", "width": "tablet"})
			if err != nil {
				t.Fatalf("preview failed: %v", err)
			}
			var result struct {
				State       string                    `json:"state"`
				Screenshots []reportPreviewScreenshot `json:"screenshots"`
				UIChecks    []reportPreviewUICheck    `json:"ui_checks"`
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil || result.State != "ready" || len(result.Screenshots) != 1 || result.Screenshots[0].Error != "" || len(result.UIChecks) != 1 || result.UIChecks[0].Error != "" || result.UIChecks[0].ViewportWidth != 768 {
				t.Fatalf("preview did not settle and publish a screenshot: %v %s", err, out)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requests) == 0 {
				t.Fatal("no workspace browser commands")
			}
			resized := false
			for _, req := range requests {
				if strings.Contains(req.Command, " set ") && strings.Contains(req.Command, "viewport") {
					resized = true
				}
			}
			if !resized {
				t.Fatal("preview did not resize the actual browser viewport")
			}
			scope := requests[0].FolderGuard.BrowserSession
			if !browser.IsUserBrowserSession(scope) || scope == common.SandboxBrowserSession(parent) || scope == previousBrowser {
				t.Fatalf("preview reused another browser: %q", scope)
			}
			previousBrowser = scope
			shot := workspace + "/" + result.Screenshots[0].Path
			for _, req := range requests {
				guard := req.FolderGuard
				if guard.BrowserSession != scope || !strings.Contains(req.Command, scope) || strings.Contains(req.Command, "--cdp") || req.WorkingDirectory != workspace {
					t.Errorf("command escaped preview session: %+v", req)
				}
				if len(guard.WritePaths) != 1 || !reflect.DeepEqual(guard.ReadPaths, []string{workspace + "/", guard.WritePaths[0]}) || !strings.HasPrefix(shot, guard.WritePaths[0]) || guard.WritePaths[0] == workspace+"/" {
					t.Errorf("preview guard not confined to workspace and this run's screenshots: %+v", guard)
				}
				if !withClaims && (!reflect.DeepEqual(guard.BlockedPaths, parentConfig.BlockedPaths) || !reflect.DeepEqual(guard.BlockedWritePaths, parentConfig.BlockedWritePaths)) {
					t.Errorf("preview dropped parent denies: %+v", guard)
				}
				if req.ArtifactTransfer != nil && req.ArtifactTransfer.DestinationPath != shot {
					t.Errorf("screenshot published outside preview output: %+v", req.ArtifactTransfer)
				}
			}
			if !strings.Contains(requests[len(requests)-1].Command, " close ") {
				t.Error("preview did not close its browser with the same caller and guard")
			}
		})
	}
	if !reflect.DeepEqual(common.GetSessionShellConfig(parent), parentConfig) {
		t.Error("preview changed the caller's shell configuration")
	}
}
