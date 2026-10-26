package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

type workspaceBrowserSettings struct {
	Mode string `json:"mode"`
	Port int    `json:"port"`
}

func normalBrowserMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "extension"
	}
	if browser.ValidateMode(mode) != nil {
		return ""
	}
	return mode
}
func effectiveWorkspaceBrowserMode(mode string) string { return normalBrowserMode(mode) }

func readWorkspaceBrowserSettings(ctx context.Context, workspace string) (workspaceBrowserSettings, error) {
	settings := workspaceBrowserSettings{Mode: "extension", Port: defaultCDPPort}
	manifest, found, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil {
		return settings, err
	}
	if found && manifest != nil {
		settings.Mode = normalBrowserMode(manifest.Capabilities.BrowserMode)
		if manifest.Capabilities.BrowserCDPPort > 0 {
			settings.Port = manifest.Capabilities.BrowserCDPPort
		}
	}
	if settings.Mode == "" {
		return settings, fmt.Errorf("Select an available browser in this project's Browser panel")
	}
	return settings, nil
}

func saveWorkspaceBrowserSettings(ctx context.Context, workspace string, settings workspaceBrowserSettings) error {
	if err := browser.ValidateMode(settings.Mode); err != nil {
		return err
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil {
		return err
	}
	if !found || manifest == nil {
		return fmt.Errorf("Project settings could not be found")
	}
	manifest.Capabilities.BrowserMode = settings.Mode
	manifest.Capabilities.BrowserCDPPort = settings.Port
	return WriteWorkflowManifest(ctx, workspace, manifest)
}

func validBrowserWorkspacePath(workspace string) bool {
	return workspace != "" && !path.IsAbs(workspace) && path.Clean(workspace) == workspace && !strings.Contains(workspace, "\\")
}

// browserWorkspaceAccess ties user-start/configuration to the same workspace and
// product authorization used by the live viewer; no caller-selected browser ID.
func (api *StreamingAPI) browserWorkspaceAccess(r *http.Request, workspace, profileID string, write bool) (string, error) {
	if !validBrowserWorkspacePath(workspace) {
		return "", fmt.Errorf("Use the canonical workspace path")
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		return "", fmt.Errorf("Sign in first")
	}
	// The workflow-write role governs workflows only. A Code or Crew project's
	// browser is its owner's (canControlLiveBrowser checks that): requiring the
	// workflow role refused every browser check for members with read-only
	// workflow access in their own Code, so its browser never left the loading
	// screen (server B 2026-10-07, PLAT-673).
	workflowRoleOK := isProjectWorkspacePath(workspace) || currentUserCanWriteWorkflows(r)
	if write && (!workflowRoleOK || !api.canControlLiveBrowser(r.Context(), claims, workspace)) {
		return "", fmt.Errorf("Workspace write access required")
	}
	if isProjectWorkspacePath(workspace) {
		product, project, _ := projectProductForPath(workspace)
		root := product.ProjectsRoot + "/" + project
		if workspaceref.MustParse(workspace).IsShared() {
			root = workspaceref.SharedProjectPath(project)
		}
		if normalizeConversationWorkspace(workspace) != root && normalizeConversationWorkspace(workspace) != normalizeConversationWorkspace(root) {
			return "", fmt.Errorf("Open the project root browser")
		}
		if api.crewBrowserAccess(claims, workspace) == WorkflowAccessNone {
			return "", fmt.Errorf("Project access required")
		}
		// The browser key may retain a migrated Crew's old path to keep its
		// cookies. Execution and folder grants must use the current directory.
		if ref, ok := resolveCrewPath(r.Context(), claims.UserID, workspace); ok {
			return ref.Path(), nil
		}
		return agentProfileRuntimeWorkspace(claims.UserID, workspace), nil
	}
	if strings.HasPrefix(normalizeConversationWorkspace(workspace), "Workflow/") {
		level, manifest := workflowAccessForWorkspacePath(r.Context(), claims, workspace)
		if manifest == nil || level == WorkflowAccessNone {
			return "", fmt.Errorf("Workflow access required")
		}
		return strings.Trim(workspace, "/"), nil
	}
	if profileID == "" || api.agentProfiles == nil {
		return "", fmt.Errorf("A product profile is required")
	}
	profile, err := api.agentProfiles.Resolve(profileID, 0, claims.UserID)
	if err != nil || !userAllowedProduct(claims, profile.Product) {
		return "", fmt.Errorf("Product access required")
	}
	if profile.Runtime.Capabilities.Browser == agentprofiles.CapabilityDisabled {
		return "", fmt.Errorf("Browser unavailable for this product profile")
	}
	if profile.ToolPolicy.IsAllowlist() {
		allowed := false
		for _, tool := range profile.ToolPolicy.Enabled {
			if tool == "agent_browser" {
				allowed = true
			}
		}
		if !allowed {
			return "", fmt.Errorf("Browser unavailable for this product")
		}
	}
	clean, err := cleanAgentProfileWorkspace(workspace, claims.UserID)
	if err != nil {
		return "", err
	}
	declared := profile.Runtime.Workspace.Root
	if declared == "" || !workspacePathsMatchForUser(claims.UserID, declared, clean) {
		return "", fmt.Errorf("Workspace does not belong to this product")
	}
	if !api.ownsProjectWorkspace(claims.UserID, workspace) {
		return "", fmt.Errorf("Workspace access required")
	}
	return agentProfileRuntimeWorkspace(claims.UserID, clean), nil
}
func (api *StreamingAPI) handleWorkspaceBrowser(w http.ResponseWriter, r *http.Request) {
	workspace := strings.TrimSpace(r.URL.Query().Get("workspace_path"))
	physical, err := api.browserWorkspaceAccess(r, workspace, r.URL.Query().Get("profile_id"), r.Method == http.MethodPost)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	settings, err := readWorkspaceBrowserSettings(r.Context(), physical)
	if err != nil {
		http.Error(w, "Cannot load browser settings", 502)
		return
	}
	if r.Method == http.MethodPost {
		var req struct {
			Action string `json:"action"`
			Mode   string `json:"mode"`
			Port   int    `json:"port"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req) != nil {
			http.Error(w, "Invalid browser request", 400)
			return
		}
		if req.Action == "save" {
			if err := browser.ValidateMode(req.Mode); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if req.Port < 1 || req.Port > 65535 {
				http.Error(w, "Invalid Chrome port", 400)
				return
			}
			settings = workspaceBrowserSettings{Mode: req.Mode, Port: req.Port}
			err = saveWorkspaceBrowserSettings(r.Context(), physical, settings)
			if err != nil {
				http.Error(w, "Cannot save browser settings", 502)
				return
			}
		} else if req.Action == "start" || req.Action == "recover" {
			if settings.Mode != "cdp" && settings.Mode != "headless" {
				http.Error(w, "Choose a saved browser or a direct browser connection first", 400)
				return
			}
			if settings.Mode == "headless" && !browser.ManagedEnabled() {
				http.Error(w, "This installation uses the extension or local Chrome", 400)
				return
			}
			// A viewer has its own scoped managed daemon. On local installations it may
			// attach to the explicitly configured Chrome; server CDP policy still wins.
			session := browserSessionForWorkspace(GetUserIDFromContext(r.Context()), workspace)
			release, ok := browser.TryTakeBrowserControl(session)
			if !ok {
				http.Error(w, "Browser busy; return control before starting", 409)
				return
			}
			defer release()
			client := browser.NewClient(workspaceBrowserEndpoint())
			args := browser.HeadlessLaunchArgsForSession(session)
			cdpPort := 0
			if req.Action == "recover" && (browser.ViewerCDPPort(session) > 0 || settings.Mode == "cdp") {
				http.Error(w, "Reconnect local Chrome manually", 409)
				return
			}
			if req.Action != "recover" && browser.CDPEnabled() && settings.Mode == "cdp" {
				connected, _, checkErr := client.CheckCDP(r.Context(), settings.Port)
				if connected && checkErr == nil {
					cdpPort = settings.Port
					args = []string{"--cdp", browser.ConfiguredCDPEndpoint(settings.Port), "--pin-tab"}
				} else if settings.Mode == "cdp" {
					http.Error(w, "Start the configured local Chrome first", 409)
					return
				}
			}
			if cdpPort > 0 {
				unlock, ok := browser.TryTakeCDPBrowserControl(cdpPort)
				if !ok {
					http.Error(w, "Local Chrome is busy; wait for the current action to finish", 409)
					return
				}
				defer unlock()
			}
			// Listing tabs lazily starts Chrome and preserves the selected signed-in page.
			// A daemon finishing shutdown may still have stale socket metadata. A
			// bounded retry is safe here: listing tabs performs no website action.
			for attempt := 0; attempt < 3; attempt++ {
				_, err = client.ExecuteCommand(r.Context(), append(args, "--session", session, "tab", "--json"), workspaceBrowserExecuteOptions(GetUserIDFromContext(r.Context()), physical, session, 30*time.Second))
				if err == nil || attempt == 2 {
					break
				}
				select {
				case <-r.Context().Done():
					return
				case <-time.After(250 * time.Millisecond):
				}
			}
			if err != nil {
				http.Error(w, "Cannot start browser: "+err.Error(), 502)
				return
			}
			stream, streamErr := client.ExecuteCommand(r.Context(), append(args, "--session", session, "stream", "status", "--json"), workspaceBrowserExecuteOptions(GetUserIDFromContext(r.Context()), physical, session, 10*time.Second))
			var streamState struct {
				Data struct {
					Enabled bool `json:"enabled"`
				} `json:"data"`
			}
			if streamErr == nil && json.Unmarshal([]byte(stream), &streamState) != nil {
				streamErr = fmt.Errorf("Cannot inspect browser streaming")
			}
			if streamErr == nil && !streamState.Data.Enabled {
				_, streamErr = client.ExecuteCommand(r.Context(), append(args, "--session", session, "stream", "enable", "--json"), workspaceBrowserExecuteOptions(GetUserIDFromContext(r.Context()), physical, session, 10*time.Second))
			}
			if streamErr != nil {
				http.Error(w, "Browser started, but live streaming could not connect: "+streamErr.Error(), 502)
				return
			}
			if cdpPort == 0 {
				if err := restoreWorkspaceBrowserTabs(r.Context(), session); err != nil {
					http.Error(w, "Browser started, but previous tabs could not be reopened. Try again.", 502)
					return
				}
			}
			browser.BindViewerCDPPort(session, cdpPort)
			browser.GetSessionTracker().Touch(session, "viewer:"+session, "viewer:"+session)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"browser_session": session, "settings": settings})
			return
		} else {
			http.Error(w, "Invalid browser action", 400)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}
func browserViewerLaunchArgs(session string) []string {
	if port := browser.ViewerCDPPort(session); port > 0 {
		return []string{"--cdp", browser.ConfiguredCDPEndpoint(port), "--pin-tab"}
	}
	if browser.IsUserBrowserSession(session) {
		return browser.HeadlessLaunchArgsForSession(session)
	}
	return nil
}

func workspaceBrowserExecuteOptions(userID, workspace, session string, timeout time.Duration) *browser.ExecuteOptions {
	root := strings.TrimSuffix(workspace, "/") + "/"
	return &browser.ExecuteOptions{Timeout: timeout, UserID: userID, WorkingDirectory: workspace, FolderGuard: &browser.FolderGuardConfig{Enabled: true, ReadPaths: []string{root}, WritePaths: []string{root}, BrowserSession: session}}
}

func workspaceBrowserEndpoint() string {
	endpoint := strings.TrimRight(os.Getenv("WORKSPACE_API_URL"), "/")
	if endpoint == "" {
		return "http://127.0.0.1:8081"
	}
	return endpoint
}

// Restoring is a trusted operation in the workspace namespace. The route has
// no caller-selected profile or addresses, and runs under startup's scope lease.
func restoreWorkspaceBrowserTabs(ctx context.Context, session string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, workspaceBrowserEndpoint()+"/api/browser/live/"+session+"/restore-tabs", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Workspace-Token", os.Getenv("WORKSPACE_API_TOKEN"))
	response, err := (&http.Client{Timeout: 65 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != 200 {
		return fmt.Errorf("cannot restore browser tabs")
	}
	return nil
}
