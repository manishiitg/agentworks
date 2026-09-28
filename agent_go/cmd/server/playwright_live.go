package server

// Playwright publishes viewport frames over its existing authenticated tool
// connection. There are no browser ports to discover or arbitrary URLs to dial.
import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/mcpagent/mcpclient"
)

type playwrightLiveSession struct {
	id, owner, workspace, run, label string
	mu                               sync.Mutex
	latest                           map[string][]byte
	watchers                         map[chan []byte]struct{}
	done                             chan struct{}
	recording                        *playwrightRecording
}

type playwrightLiveRegistry struct {
	sync.Mutex
	sessions   map[string]*playwrightLiveSession
	recordings map[string]*playwrightRecording
}

// playwrightScope is the key a Playwright live session is filed under: the
// workflow path, or for a Crew the owner-qualified project path so the owner
// (logical path) and Crew members (physical path) name the same crew.
func playwrightScope(userID, workspace string) string {
	workspace = strings.TrimRight(workspace, "/")
	if isProjectWorkspacePath(workspace) {
		return browserProjectKey(userID, workspace)
	}
	return workspace
}

// playwrightVisible reports whether a viewer who asked for workspace may see a
// Playwright session filed under (owner, scope). Workflow tests stay private to
// the user who ran them; Crew tests follow Crew browser access.
func (api *StreamingAPI) playwrightVisible(claims *UserClaims, workspace, owner, scope string) bool {
	userID := ""
	if claims != nil {
		userID = claims.UserID
	}
	workspace = strings.TrimRight(workspace, "/")
	if isProjectWorkspacePath(workspace) {
		return api.crewBrowserAccess(claims, workspace) != WorkflowAccessNone && playwrightScope(userID, workspace) == scope
	}
	return userID != "" && owner == userID && scope == workspace
}

func (api *StreamingAPI) playwrightSessions(user, workspace string) []map[string]string {
	return api.playwrightSessionsFor(&UserClaims{UserID: user}, workspace)
}

func (api *StreamingAPI) playwrightSessionsFor(claims *UserClaims, workspace string) []map[string]string {
	api.playwrightLive.Lock()
	defer api.playwrightLive.Unlock()
	items := []map[string]string{}
	for _, s := range api.playwrightLive.sessions {
		if api.playwrightVisible(claims, workspace, s.owner, s.workspace) {
			item := map[string]string{"browser_session": s.id, "workflow_session": s.run, "label": s.label, "kind": "playwright", "read_only": "true"}
			if s.recording != nil {
				item["recording_state"], item["recording_error"] = s.recording.status()
			}
			items = append(items, item)
		}
	}
	for _, rec := range api.playwrightLive.recordings {
		if !api.playwrightVisible(claims, workspace, rec.owner, rec.workspace) {
			continue
		}
		state, problem := rec.status()
		found := false
		for _, item := range items {
			if item["browser_session"] == rec.id {
				item["recording_state"] = state
				item["recording_error"] = problem
				found = true
				break
			}
		}
		if !found {
			items = append(items, map[string]string{"browser_session": rec.id, "workflow_session": rec.run, "label": rec.label, "kind": "playwright", "read_only": "true", "state": "completed", "recording_state": state, "recording_error": problem})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := api.playwrightLive.recordings[items[i]["browser_session"]], api.playwrightLive.recordings[items[j]["browser_session"]]
		if a == nil && b == nil {
			return items[i]["browser_session"] < items[j]["browser_session"]
		}
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		return a.created.After(b.created)
	})
	return items
}

func (s *playwrightLiveSession) publish(kind string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest[kind] = data
	for ch := range s.watchers {
		select {
		case ch <- data:
		default:
		} // A slow viewer never stalls the test.
	}
}

// Saved steps use an MCP group session rather than the visible chat ID. Resolve
// only the server-owned parent mapping, then verify its active owner and workflow.
// Never infer ownership from a session-name prefix or producer-supplied fields.
func (api *StreamingAPI) playwrightWorkflowOwner(run string) (string, string) {
	parent := virtualtools.GetParentChat(run)
	registeredRun := mcpclient.GetSessionRegistry().HTTPSessionForMCPSession(run)
	api.activeSessionsMux.RLock()
	defer api.activeSessionsMux.RUnlock()
	owner := api.activeSessions[run]
	// Scheduled/webhook group sessions have no parent chat. Their execution
	// tracker still binds them to an HTTP run; require that run to be active.
	if owner == nil && parent == nil && registeredRun != "" {
		owner = api.activeSessions[registeredRun]
	}
	if owner == nil && parent != nil {
		owner = api.activeSessions[parent.SessionID]
		if owner == nil || strings.TrimRight(parent.WorkflowPath, "/") != strings.TrimRight(owner.WorkspacePath, "/") || (parent.UserID != "" && parent.UserID != owner.UserID) {
			return "", ""
		}
	}
	if owner == nil {
		return "", ""
	}
	return owner.UserID, strings.TrimRight(owner.WorkspacePath, "/")
}

// Mounted under sessionToolsRouter, behind the same bearer authentication as
// execute_shell_command. Identity is taken from the server's run, never a body.
func (api *StreamingAPI) handlePlaywrightPublisher(w http.ResponseWriter, r *http.Request) {
	run := mux.Vars(r)["session_id"]
	user, workspace := api.playwrightWorkflowOwner(run)
	if user == "" || (!strings.HasPrefix(workspace, "Workflow/") && !isProjectWorkspacePath(workspace)) {
		http.Error(w, "An active workflow or Crew session is required", http.StatusNotFound)
		return
	}
	workspace = playwrightScope(user, workspace)
	label := strings.TrimSpace(r.URL.Query().Get("label"))
	if len(label) > 240 {
		label = label[:240]
	}
	if label == "" {
		label = "Playwright test"
	}
	s := &playwrightLiveSession{id: "pw-" + uuid.NewString(), owner: user, workspace: workspace, run: run, label: label, latest: map[string][]byte{}, watchers: map[chan []byte]struct{}{}, done: make(chan struct{})}
	api.playwrightLive.Lock()
	if len(api.playwrightLive.sessions) >= 64 {
		api.playwrightLive.Unlock()
		http.Error(w, "Too many live test browsers", 429)
		return
	}
	if api.playwrightLive.sessions == nil {
		api.playwrightLive.sessions = map[string]*playwrightLiveSession{}
	}
	api.playwrightLive.sessions[s.id] = s
	api.playwrightLive.Unlock()
	defer func() {
		api.playwrightLive.Lock()
		delete(api.playwrightLive.sessions, s.id)
		api.playwrightLive.Unlock()
		close(s.done)
		if s.recording != nil {
			s.recording.finish()
		}
	}()
	upgrader := api.liveAttachUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	rec := api.newPlaywrightRecording(s)
	api.playwrightLive.Lock()
	s.recording = rec
	api.playwrightLive.Unlock()
	conn.SetReadLimit(2 << 20)
	_ = conn.WriteJSON(map[string]string{"type": "registered", "browser_session": s.id})
	var lastFrame time.Time
	for {
		_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		// Reconstruct accepted messages instead of forwarding extra producer fields.
		var m struct {
			Type     string `json:"type"`
			Data     string `json:"data"`
			Metadata struct {
				Width  int `json:"deviceWidth"`
				Height int `json:"deviceHeight"`
			} `json:"metadata"`
			Tabs []struct {
				ID     string `json:"tabId"`
				Title  string `json:"title"`
				URL    string `json:"url"`
				Active bool   `json:"active"`
			} `json:"tabs"`
		}
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		switch m.Type {
		case "ping":
			continue
		case "frame":
			if time.Since(lastFrame) < 200*time.Millisecond {
				continue
			}
			if len(m.Data) > 1400000 || m.Metadata.Width < 1 || m.Metadata.Height < 1 || m.Metadata.Width > 16384 || m.Metadata.Height > 16384 {
				return
			}
			bytes, err := base64.StdEncoding.DecodeString(m.Data)
			if err != nil || len(bytes) < 3 || bytes[0] != 0xff || bytes[1] != 0xd8 || bytes[2] != 0xff {
				return
			}
			lastFrame = time.Now()
			clean, _ := json.Marshal(map[string]interface{}{"type": "frame", "data": m.Data, "metadata": m.Metadata})
			s.publish("frame", clean)
			s.recording.add(bytes)
		case "tabs":
			if len(m.Tabs) > 32 {
				return
			}
			for _, tab := range m.Tabs {
				if len(tab.Title) > 500 || len(tab.URL) > 2048 || len(tab.ID) > 40 {
					return
				}
			}
			clean, _ := json.Marshal(map[string]interface{}{"type": "tabs", "tabs": m.Tabs})
			s.publish("tabs", clean)
		default: // No console, network data, commands or client-selected ownership.
		}
	}
}

func (api *StreamingAPI) handlePlaywrightViewer(w http.ResponseWriter, r *http.Request, id string) {
	api.playwrightLive.Lock()
	s := api.playwrightLive.sessions[id]
	api.playwrightLive.Unlock()
	if s == nil || !api.playwrightVisible(GetUserFromContext(r.Context()), r.URL.Query().Get("workspace_path"), s.owner, s.workspace) {
		http.NotFound(w, r)
		return
	}
	upgrader := api.liveAttachUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	ch := make(chan []byte, 8)
	s.mu.Lock()
	for _, kind := range []string{"tabs", "frame"} {
		if data := s.latest[kind]; data != nil {
			ch <- data
		}
	}
	s.watchers[ch] = struct{}{}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.watchers, ch); s.mu.Unlock() }()
	stopped := make(chan struct{})
	errors := make(chan struct{}, 1)
	conn.SetReadLimit(16384)
	go func() {
		defer close(stopped)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
			var m struct {
				Type string `json:"type"`
			}
			if conn.ReadJSON(&m) != nil {
				return
			}
			if m.Type != "ping" {
				select {
				case errors <- struct{}{}:
				default:
				}
			}
		}
	}()
	write := func(v interface{}) error {
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return conn.WriteJSON(v)
	}
	if write(map[string]interface{}{"type": "viewer_control", "controlling": false, "read_only": true}) != nil {
		return
	}
	for {
		select {
		case <-s.done:
			return
		case <-stopped:
			return
		case <-r.Context().Done():
			return
		case <-errors:
			if write(map[string]string{"type": "viewer_error", "message": "Playwright tests are watch-only."}) != nil {
				return
			}
		case data := <-ch:
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if conn.WriteMessage(websocket.TextMessage, data) != nil {
				return
			}
		}
	}
}

// Distribute the exact fixture sources shipped with this server release through
// the authenticated session bridge, so sandboxed tests need no host-path grants.
func (api *StreamingAPI) handlePlaywrightPackage(w http.ResponseWriter, r *http.Request) {
	user, workspace := api.playwrightWorkflowOwner(mux.Vars(r)["session_id"])
	allowed := user != "" && strings.HasPrefix(workspace, "Workflow/")
	if !allowed {
		http.NotFound(w, r)
		return
	}
	name := map[string]string{"node": "agentworks-playwright.tgz", "python": "agentworks-playwright-python.zip"}[mux.Vars(r)["package"]]
	if name == "" {
		http.NotFound(w, r)
		return
	}
	directory := os.Getenv("AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR")
	if directory == "" {
		directory = "packages"
	}
	file := filepath.Join(directory, name)
	if _, err := os.Stat(file); err != nil {
		http.Error(w, "Playwright fixture package is not installed in this release", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, file)
}
