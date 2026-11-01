package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
)

// Browser discovery is scoped to both the signed-in session owner and workflow.
// Never expose the host-wide dashboard, which includes unrelated workflows.
func (api *StreamingAPI) liveBrowserSessions(r *http.Request) []map[string]string {
	result := []map[string]string{}
	workspace := strings.TrimRight(strings.TrimSpace(r.URL.Query().Get("workspace_path")), "/")
	if workspace == "" {
		return result
	}
	userID := GetUserIDFromContext(r.Context())
	claims := GetUserFromContext(r.Context())
	result = api.playwrightSessionsFor(claims, workspace)
	shared := browserSessionForWorkspace(userID, workspace)
	for _, item := range browser.GetSessionTracker().ActiveSessions() {
		if browser.IsUserBrowserSession(item["browser_session"]) {
			if userID == "" || shared == "" || item["browser_session"] != shared {
				continue
			}
			// Crew projects have workflow.json because they reuse the shared
			// project manifest, but they are not workflows and are not gated
			// by a workflow browser_mode. Classify the path first.
			if isProjectWorkspacePath(workspace) {
				if api.crewBrowserAccess(claims, workspace) != WorkflowAccessNone {
					item["label"] = api.projectProductName(userID, workspace) + " browser"
					result = append(result, item)
				}
				continue
			}
			level, manifest := workflowAccessForWorkspacePath(r.Context(), claims, workspace)
			if manifest != nil {
				// Workflow access and a supported browser mode are required.
				item["label"] = "Workflow browser"
				if level != WorkflowAccessNone && normalBrowserMode(manifest.Capabilities.BrowserMode) != "" {
					result = append(result, item)
				}
			} else if api.ownsProjectWorkspace(userID, workspace) {
				// A fixed-workspace product project:
				// its browser belongs to the project owner.
				item["label"] = "Project browser"
				result = append(result, item)
			}
			continue
		}
		id := item["workflow_session"]
		api.activeSessionsMux.RLock()
		owner := api.activeSessions[id]
		matches := owner != nil && strings.TrimRight(owner.WorkspacePath, "/") == workspace && owner.UserID == userID
		api.activeSessionsMux.RUnlock()
		if matches {
			result = append(result, item)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i]["browser_session"] < result[j]["browser_session"] })
	return result
}

// crewBrowserAccess follows Crew Run mode: the owner has full access and any
// other signed-in user with the Crew product has read (view-only) access.
func (api *StreamingAPI) crewBrowserAccess(claims *UserClaims, workspace string) WorkflowAccessLevel {

	if claims == nil || strings.TrimSpace(claims.UserID) == "" || !isCrewProjectPath(workspace) {
		return WorkflowAccessNone
	}
	if crewProjectOwnedByCaller(claims.UserID, workspace) {
		return WorkflowAccessOwner
	}
	if api.agentProfiles == nil {
		return WorkflowAccessNone
	}
	profile, err := api.agentProfiles.Resolve("work", 0, claims.UserID)
	if err != nil || !userAllowedProduct(claims, profile.Product) {
		return WorkflowAccessNone
	}
	return WorkflowAccessRead
}

// ownsProjectWorkspace reports whether userID owns a non-crew product project
// workspace: its physical path names them, or (single-account deployments and
// logical paths) they hold a live session there.
func (api *StreamingAPI) ownsProjectWorkspace(userID, workspace string) bool {
	if userID == "" {
		return false
	}
	if ownerID, ok := crewProjectOwnerID(browserProjectKey(userID, workspace)); ok && ownerID == sanitizeUserIDForPath(userID) {
		return true
	}
	return !IsMultiUserMode() || api.userOwnsActiveSessionAtWorkspace(userID, workspace)
}

// userOwnsActiveSessionAtWorkspace reports whether userID owns a currently
// tracked chat session whose workspace matches workspace.
func (api *StreamingAPI) userOwnsActiveSessionAtWorkspace(userID, workspace string) bool {
	if userID == "" {
		return false
	}
	api.activeSessionsMux.RLock()
	defer api.activeSessionsMux.RUnlock()
	for _, owner := range api.activeSessions {
		if owner != nil && owner.UserID == userID && workspacePathsMatchForUser(userID, owner.WorkspacePath, workspace) {
			return true
		}
	}
	return false
}

// canControlLiveBrowser decides whether claims may take manual control of the
// live browser at workspace. Viewing follows liveBrowserSessions; control
// needs write access: the Crew owner, or a workflow owner/editor.
func (api *StreamingAPI) canControlLiveBrowser(ctx context.Context, claims *UserClaims, workspace string) bool {
	if !validBrowserWorkspacePath(workspace) {
		return false
	}
	userID := ""
	if claims != nil {
		userID = claims.UserID
	}
	if isProjectWorkspacePath(workspace) {
		return api.crewBrowserAccess(claims, workspace) == WorkflowAccessOwner
	}
	level, manifest := workflowAccessForWorkspacePath(ctx, claims, workspace)
	if manifest != nil {
		return level == WorkflowAccessOwner || level == WorkflowAccessWrite
	}
	return api.ownsProjectWorkspace(userID, workspace)
}

func (api *StreamingAPI) handleLiveBrowserSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"sessions": api.liveBrowserSessions(r)})
}

var liveBrowserTabRef = regexp.MustCompile(`^(t[0-9]+|[0-9]+)$`)

func (api *StreamingAPI) handleLiveBrowserStream(w http.ResponseWriter, r *http.Request) {
	session := mux.Vars(r)["session"]
	authorized := func() bool {
		for _, item := range api.liveBrowserSessions(r) {
			if item["browser_session"] == session {
				return true
			}
		}
		return false
	}
	if !authorized() {
		http.Error(w, "Browser session not found", http.StatusNotFound)
		return
	}
	if !api.checkLiveAttachOrigin(r) {
		http.Error(w, "Origin not allowed", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(session, "pw-") {
		api.handlePlaywrightViewer(w, r, session)
		return
	}
	workspaceURL := strings.TrimRight(os.Getenv("WORKSPACE_API_URL"), "/")
	if workspaceURL == "" {
		workspaceURL = "http://127.0.0.1:8081"
	}
	target, err := url.Parse(workspaceURL)
	if err != nil {
		http.Error(w, "Workspace unavailable", http.StatusBadGateway)
		return
	}
	target.Scheme = strings.Replace(target.Scheme, "http", "ws", 1)
	target.Path += "/api/browser/live/" + url.PathEscape(session) + "/stream"
	target.RawQuery = ""
	headers := http.Header{}
	headers.Set("X-Workspace-Token", os.Getenv("WORKSPACE_API_TOKEN"))
	if browser.ViewerCDPPort(session) == 0 {
		headers.Set("X-Browser-Managed", "true")
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	upstream, response, err := dialer.DialContext(r.Context(), target.String(), headers)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		http.Error(w, "Live browser unavailable; check the server agent-browser version and stream", http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	_ = upstream.WriteJSON(map[string]interface{}{"type": "config", "maxFps": 10})
	upgrader := api.liveAttachUpgrader()
	viewer, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer viewer.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	workspace := r.URL.Query().Get("workspace_path")
	physical := workspace
	if !strings.HasPrefix(normalizeConversationWorkspace(workspace), "Workflow/") {
		physical = browserProjectKey(GetUserIDFromContext(r.Context()), workspace)
	}
	var writeMu sync.Mutex
	send := func(value interface{}) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		viewer.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return viewer.WriteJSON(value)
	}
	sendError := func(message string) { _ = send(map[string]interface{}{"type": "viewer_error", "message": message}) }
	upstream.SetReadLimit(8 << 20)
	viewer.SetReadLimit(16 << 10)
	_ = viewer.SetReadDeadline(time.Now().Add(45 * time.Second))
	streamStarted := time.Now()
	firstFrameLogged := false
	go func() {
		defer cancel()
		defer viewer.Close()
		for {
			_, data, err := upstream.ReadMessage()
			if err != nil {
				return
			}
			// Only viewport/status/tab updates are needed. Don't leak console, storage,
			// network payloads or command outputs through this viewer.
			var message struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &message) != nil {
				continue
			}
			switch message.Type {
			case "frame", "status", "tabs", "url":
			default:
				continue
			}
			if message.Type == "frame" && !firstFrameLogged {
				firstFrameLogged = true
				// One line per viewer session (PLAT-776): how long the first
				// frame took and the reported page size, so slowness can be
				// read from server logs. Never the page address or content.
				var frame struct {
					Metadata struct {
						Width  int `json:"deviceWidth"`
						Height int `json:"deviceHeight"`
					} `json:"metadata"`
				}
				_ = json.Unmarshal(data, &frame)
				log.Printf("[BROWSER-LIVE] first frame session=%s after_ms=%d device=%dx%d", session, time.Since(streamStarted).Milliseconds(), frame.Metadata.Width, frame.Metadata.Height)
			}
			writeMu.Lock()
			viewer.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err = viewer.WriteMessage(websocket.TextMessage, data)
			writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	canControl := func() bool {
		workspace := strings.TrimRight(strings.TrimSpace(r.URL.Query().Get("workspace_path")), "/")
		// A project's browser is its owner's; the workflow-write role applies to workflows only (PLAT-673).
		if !isProjectWorkspacePath(workspace) && !currentUserCanWriteWorkflows(r) {
			return false
		}
		return api.canControlLiveBrowser(ctx, GetUserFromContext(r.Context()), workspace)
	}
	canInteract := func() bool {
		if !canControl() {
			log.Printf("[BROWSER-LIVE] input refused session=%q user=%q reason=project_access", session, GetUserIDFromContext(r.Context()))
			return false
		}
		browser.GetSessionTracker().TouchExisting(session)
		return true
	}
	// Browser commands (open, tab, resize, paste) can take seconds.
	// Running them in the viewer read loop stalled input forwarding and pings
	// behind them, so clicks landed late or not at all (PLAT-776). One ordered
	// worker per viewer runs them in arrival order from a bounded queue; the
	// read loop keeps forwarding input and answering pings. The worker never
	// writes the viewer socket directly: replies go through send (writeMu).
	commands := make(chan func(), 32)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case command := <-commands:
				command()
			}
		}
	}()
	defer func() { cancel(); <-workerDone }()
	enqueue := func(command func()) {
		select {
		case commands <- command:
		case <-ctx.Done():
		default:
			sendError("Still working on the last action. Try again in a moment.")
		}
	}
	log.Printf("[BROWSER-LIVE] viewer connected session=%q user=%q interactive=%t", session, GetUserIDFromContext(r.Context()), canControl())
	defer log.Printf("[BROWSER-LIVE] viewer disconnected session=%q user=%q", session, GetUserIDFromContext(r.Context()))
	_ = send(map[string]interface{}{"type": "viewer_ready", "interactive": canControl(), "platform": runtime.GOOS})
	for {
		_, data, err := viewer.ReadMessage()
		if err != nil {
			return
		}
		if !authorized() {
			return
		}
		var message struct {
			Type      string `json:"type"`
			Tab       string `json:"tab"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			URL       string `json:"url"`
			Action    string `json:"action"`
			RequestID string `json:"requestId"`
			Text      string `json:"text"`
		}
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		_ = viewer.SetReadDeadline(time.Now().Add(45 * time.Second))
		switch message.Type {
		case "ping":
			_ = send(map[string]string{"type": "pong"})
		case "navigate", "history":
			command := []string{"open", message.URL}
			if message.Type == "history" {
				switch message.Action {
				case "back", "forward", "reload":
					command = []string{message.Action}
				default:
					continue
				}
			} else {
				target, err := url.Parse(message.URL)
				if err != nil || target.User != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
					sendError("Enter an http or https address.")
					continue
				}
			}
			userID := GetUserIDFromContext(r.Context())
			enqueue(func() {
				if !canInteract() {
					return
				}
				_, err := browser.NewClient(workspaceURL).ExecuteCommand(ctx, append(browserViewerLaunchArgs(session), append([]string{"--session", session}, append(command, "--json")...)...), workspaceBrowserExecuteOptions(userID, physical, session, 30*time.Second))
				if err != nil {
					sendError("Unable to open this address.")
				}
			})
		case "resize_viewport":
			// Bound rendering cost and accept only real viewport dimensions.
			if message.Width < 320 || message.Width > 1920 || message.Height < 320 || message.Height > 1920 {
				sendError("Page dimensions must be between 320 and 1920 pixels.")
				continue
			}
			width, height := message.Width, message.Height
			userID := GetUserIDFromContext(r.Context())
			enqueue(func() {
				if !canInteract() {
					return
				}
				_, err := browser.NewClient(workspaceURL).ExecuteCommand(ctx, append(browserViewerLaunchArgs(session), "--session", session, "set", "viewport", fmt.Sprint(width), fmt.Sprint(height), "--json"), workspaceBrowserExecuteOptions(userID, physical, session, 10*time.Second))
				if err != nil {
					sendError("Unable to resize the browser page.")
				}
			})
		case "switch_tab", "new_tab", "close_tab":
			msgType, tabRef, address := message.Type, message.Tab, message.URL
			if msgType == "new_tab" {
				if address == "" {
					address = "about:blank"
				}
				parsed, e := url.Parse(address)
				if address != "about:blank" && (e != nil || parsed.User != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https")) {
					sendError("Enter an http or https address.")
					continue
				}
			} else if !liveBrowserTabRef.MatchString(tabRef) {
				continue
			}
			userID := GetUserIDFromContext(r.Context())
			enqueue(func() {
				if !canInteract() {
					return
				}
				command := []string{"tab", tabRef}
				client := browser.NewClient(workspaceURL)
				run := func(args ...string) (string, error) {
					return client.ExecuteCommand(ctx, append(browserViewerLaunchArgs(session), append([]string{"--session", session}, append(args, "--json")...)...), workspaceBrowserExecuteOptions(userID, physical, session, 30*time.Second))
				}
				recovery := ""
				if msgType == "new_tab" {
					command = []string{"tab", "new", address}
				} else if msgType == "close_tab" {
					out, e := run("tab")
					var result struct {
						Data struct {
							Tabs []struct {
								Ref    string `json:"tabId"`
								Active bool   `json:"active"`
							} `json:"tabs"`
						} `json:"data"`
					}
					if e != nil || json.Unmarshal([]byte(out), &result) != nil || len(result.Data.Tabs) < 2 {
						sendError("Keep at least one browser tab open.")
						return
					}
					active := ""
					for _, tab := range result.Data.Tabs {
						if tab.Active {
							active = tab.Ref
						} else if recovery == "" {
							recovery = tab.Ref
						}
					}
					if active != tabRef {
						sendError("Select the tab before closing it.")
						return
					}
					command = []string{"tab", "close", tabRef}
				}
				if _, e := run(command...); e != nil {
					sendError("Unable to change browser tabs.")
					return
				}
				if recovery != "" {
					if _, e := run("tab", recovery); e != nil {
						sendError("Select another browser tab to continue.")
						return
					}
				}
			})
		case "input_text":
			if len(message.Text) == 0 || len(message.Text) > 8<<10 {
				continue
			}
			text, userID := message.Text, GetUserIDFromContext(r.Context())
			enqueue(func() {
				if !canInteract() {
					return
				}
				started := time.Now()
				log.Printf("[BROWSER_INPUT] event=accepted session=%q user=%q kind=input_text transport=workspace", session, userID)
				err := forwardBrowserViewerText(ctx, workspaceURL, session, userID, text)
				result := "completed"
				if err != nil {
					result = "failed"
				}
				log.Printf("[BROWSER_INPUT] event=result session=%q user=%q kind=input_text transport=workspace result=%s elapsed_ms=%d", session, userID, result, time.Since(started).Milliseconds())
				if err != nil {
					sendError("Unable to paste into this browser. Select the field and try again.")
				}
			})
		case "clipboard_copy":
			if !liveBrowserClipboardRequest.MatchString(message.RequestID) {
				continue
			}
			requestID, userID := message.RequestID, GetUserIDFromContext(r.Context())
			enqueue(func() {
				if !canInteract() {
					return
				}
				output, err := browser.NewClient(workspaceURL).ExecuteCommand(ctx, append(browserViewerLaunchArgs(session), "--session", session, "eval", liveBrowserSelectionScript, "--json"), workspaceBrowserExecuteOptions(userID, physical, session, 8*time.Second))
				text := ""
				if err == nil {
					text, err = liveBrowserSelection(output)
				}
				reply := map[string]any{"type": "clipboard", "requestId": requestID}
				if err != nil {
					reply["error"] = "Unable to copy the selected text."
				} else if text == "" {
					reply["error"] = "Select some text before copying."
				} else {
					reply["text"] = text
				}
				_ = send(reply)
			})
		case "input_mouse", "input_keyboard", "input_touch":
			if !canInteract() {
				continue
			}
			started := time.Now()
			log.Printf("[BROWSER_INPUT] event=accepted session=%q user=%q kind=%s transport=workspace", session, GetUserIDFromContext(r.Context()), message.Type)
			upstream.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := upstream.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Printf("[BROWSER_INPUT] event=result session=%q kind=%s transport=workspace result=failed elapsed_ms=%d", session, message.Type, time.Since(started).Milliseconds())
				return
			}
			log.Printf("[BROWSER_INPUT] event=result session=%q kind=%s transport=workspace result=forwarded elapsed_ms=%d", session, message.Type, time.Since(started).Milliseconds())
		}
	}
}
