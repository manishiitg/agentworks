package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsauth"
)

// A Code workspace's plain shell (docs/design/code_product.md, "Terminal").
//
// The workspace service starts the shell in the same sandbox as the shell
// tool (POST /api/shell/interactive/start: Landlock, private /tmp, its own
// tmux server) under the Code's Folder Guard, built here from the project
// exactly as a chat turn builds it. The browser's WebSocket is bridged to the
// workspace's in-sandbox attach (codeShellAttach). Each person with editor
// access or more gets their own shell of the Code; viewers get none. A shell
// with no viewer for codeShellIdleTimeout is stopped.

const (
	codeShellIdleTimeout  = 30 * time.Minute
	codeShellRoleInterval = 30 * time.Second
	codeShellSocketsDir   = "/tmp/.agentworks-shells"
)

type codeShellState struct {
	socket    string
	viewers   int
	lastSeen  time.Time
	ownerID   string
	projectID string
	userID    string
	conns     map[*websocket.Conn]bool
}

var codeShells = struct {
	sync.Mutex
	byID map[string]*codeShellState
}{byID: map[string]*codeShellState{}}

var codeShellReaperOnce sync.Once

// codeShellSweepOnStart lets tests keep the reaper from stopping a developer's
// real shells.
var codeShellSweepOnStart = true

// codeShellMaxTabs is how many terminals one person can have open in one Code (user decision 2026-10-03).
const codeShellMaxTabs = 3

// codeShellID names one person's shell of one Code in terminal tab `tab` (1..codeShellMaxTabs; the workspace
// allows ^[a-z0-9][a-z0-9-]{0,47}$). Tab 1 keeps the id a single terminal always had, so a shell running
// before tabs existed is tab 1.
func codeShellID(ownerID, projectID, userID string, tab int) string {
	key := ownerID + "\x00" + projectID + "\x00" + userID
	if tab > 1 {
		key += "\x00tab" + strconv.Itoa(tab)
	}
	sum := sha256.Sum256([]byte(key))
	return "code-" + hex.EncodeToString(sum[:])[:32]
}

// codeShellTab reads the terminal tab a request is about: none or "1" is tab 1, else 2..codeShellMaxTabs.
// Anything else is refused, which is what caps a person at codeShellMaxTabs shells per Code.
func codeShellTab(r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("tab"))
	if raw == "" {
		return 1, true
	}
	tab, err := strconv.Atoi(raw)
	if err != nil || tab < 1 || tab > codeShellMaxTabs {
		return 0, false
	}
	return tab, true
}

// codeShellFolderGuard is the Folder Guard a chat turn gives the shell tool in
// this Code: the project writable, the profile's read-only folders, the
// user's attached folders, the coding agent's own files write-protected, and
// the Code's own browser.
func (api *StreamingAPI) codeShellFolderGuard(ctx context.Context, userID, root string) *workspace.FolderGuardConfig {
	sandbox := codeproduct.BuiltinAgentProfile().Runtime.Sandbox
	if api != nil && api.agentProfiles != nil {
		if profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, userID); err == nil {
			sandbox = profile.Runtime.Sandbox
		}
	}
	write := strings.TrimSuffix(root, "/") + "/"
	reads := append([]string{write}, agentProfileReadOnlyFolders(sandbox, nil)...)
	writes := []string{write}
	grantRead, grantWrite, _, _ := workFolderGuardInputs(ctx)
	reads = appendUniqueStrings(reads, grantRead...)
	writes = appendUniqueStrings(writes, grantWrite...)
	return &workspace.FolderGuardConfig{
		Enabled:           true,
		ReadPaths:         reads,
		WritePaths:        writes,
		BlockedWritePaths: common.CodingAgentProjectionBlockedWrites(root),
		StrictAllowlist:   sandbox.IsStrict(),
		BrowserSession:    browserSessionForWorkspace(userID, root),
	}
}

// codeShellTarget authorizes the caller (owner, co-owner or editor) and
// returns their shell id and the Code's root.
func (api *StreamingAPI) codeShellTarget(r *http.Request) (userID, shellID, root string, status int, message string) {
	userID, shellID, root, _, _, status, message = api.codeShellTargetFull(r)
	return
}

func (api *StreamingAPI) codeShellTargetFull(r *http.Request) (userID, shellID, root, ownerID, projectID string, status int, message string) {
	claims := GetUserFromContext(r.Context())
	tab, validTab := codeShellTab(r)
	if !validTab {
		return "", "", "", "", "", http.StatusBadRequest, fmt.Sprintf("a Code has at most %d terminals (tab 1 to %d)", codeShellMaxTabs, codeShellMaxTabs)
	}
	projectID = strings.TrimSpace(mux.Vars(r)["project_id"])
	if claims == nil || strings.TrimSpace(claims.UserID) == "" || projectID == "" || api == nil || api.agentProfiles == nil {
		return "", "", "", "", "", http.StatusNotFound, "Code workspace not found"
	}
	profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, claims.UserID)
	if err != nil || !userAllowedProduct(claims, profile.Product) {
		return "", "", "", "", "", http.StatusNotFound, "Code workspace not found"
	}
	project, err := resolveCrewProjectBinding(r.Context(), claims.UserID, profile, projectID, "")
	if err != nil {
		return "", "", "", "", "", http.StatusNotFound, "Code workspace not found"
	}
	if !codeRoleFor(r.Context(), claims.UserID, project.OwnerID, project.Binding.ResourceID).atLeast(codeRoleEditor) {
		return "", "", "", "", "", http.StatusForbidden, "the shell needs editor access to this Code workspace"
	}
	root = agentProfileRuntimeWorkspace(project.OwnerID, project.Binding.WorkspacePath)
	return claims.UserID, codeShellID(project.OwnerID, projectID, claims.UserID, tab), root, project.OwnerID, projectID, 0, ""
}

// codeShellStart starts (or reuses) the sandboxed shell; the workspace call is
// idempotent. It returns the shell's tmux socket.
var codeShellStart = func(ctx context.Context, shellID, workDir string, guard *workspace.FolderGuardConfig, cols, rows int) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"shell_id": shellID, "working_directory": workDir, "folder_guard": guard, "cols": cols, "rows": rows,
		// The same rule the coding agents follow: unconfined only on a person's own Mac.
		"unconfined": cliUnconfinedAllowed(),
	})
	var out struct {
		Data struct {
			Socket  string `json:"socket"`
			Session string `json:"session"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := codeShellWorkspaceCall(ctx, "/api/shell/interactive/start", body, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.Data.Socket) == "" {
		return "", fmt.Errorf("the workspace returned no shell socket")
	}
	return out.Data.Socket, nil
}

var codeShellStop = func(ctx context.Context, shellID string) error {
	body, _ := json.Marshal(map[string]string{"shell_id": shellID})
	return codeShellWorkspaceCall(ctx, "/api/shell/interactive/stop", body, nil)
}

// codeShellUserKey carries whose shell a workspace call is about: the workspace service runs the shell as that
// person's own account, so every call (start, attach, stop, sweep) says who it is for.
type codeShellUserKey struct{}

func withCodeShellUser(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, codeShellUserKey{}, userID)
}

func codeShellUser(ctx context.Context) string {
	if userID, _ := ctx.Value(codeShellUserKey{}).(string); strings.TrimSpace(userID) != "" {
		return userID
	}
	return GetUserIDFromContext(ctx)
}

func codeShellWorkspaceCall(ctx context.Context, route string, body []byte, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(getWorkspaceAPIURL(), "/")+route, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	wsauth.SetHeader(req)
	if userID := codeShellUser(ctx); userID != "" {
		req.Header.Set("X-User-ID", userID)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("workspace %s: status %d: %s", route, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// GET /api/agent-profiles/code/projects/{project_id}/shell/stream — the
// caller's own sandboxed shell of this Code, over a WebSocket. Binary frames
// are keystrokes; text frames are JSON control ({"type":"resize",...}).
func (api *StreamingAPI) handleCodeShellStream(w http.ResponseWriter, r *http.Request) {
	if !api.checkLiveAttachOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	userID, shellID, root, ownerID, projectID, status, message := api.codeShellTargetFull(r)
	if status != 0 {
		http.Error(w, message, status)
		return
	}
	// Only a real WebSocket may start a shell: a plain GET would otherwise
	// start one nobody attaches to.
	if !websocket.IsWebSocketUpgrade(r) {
		http.Error(w, "a WebSocket upgrade is required", http.StatusBadRequest)
		return
	}
	codeShellReaperOnce.Do(func() { go codeShellReaper() })
	cols, rows := liveAttachInitialSize(r)
	// Tracked before it starts, so a failed start or upgrade never leaves an
	// untracked shell: the idle reaper stops it.
	codeShellTrack(shellID, ownerID, projectID, userID)
	socket, err := codeShellStart(withCodeShellUser(r.Context(), userID), shellID, root, api.codeShellFolderGuard(r.Context(), userID, root), cols, rows)
	if err != nil {
		log.Printf("[CODE_SHELL] start %s: %v", shellID, err)
		http.Error(w, "could not start the shell", http.StatusServiceUnavailable)
		return
	}

	// The attach client runs in the shell's own sandbox on the workspace
	// service: a tmux client executes what its server tells it to
	// (`detach-client -E`), so one attached from here would give the shell
	// this server's rights.
	shell, err := codeShellAttach(withCodeShellUser(r.Context(), userID), shellID, cols, rows)
	if err != nil {
		log.Printf("[CODE_SHELL] attach %s: %v", shellID, err)
		http.Error(w, "could not attach to the shell", http.StatusServiceUnavailable)
		return
	}
	defer shell.Close()
	upgrader := api.liveAttachUpgrader()
	conn, err := upgrader.Upgrade(unwrapResponseWriter(w), r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	codeShellViewer(shellID, socket, +1)
	codeShellConn(shellID, conn, true)
	// An audit trail of who had a terminal open on which Code (the keystrokes themselves are not recorded).
	log.Printf("[CODE_SHELL] %s opened terminal %s of %s/%s", userID, shellID, ownerID, projectID)
	opened := time.Now()
	defer func() {
		log.Printf("[CODE_SHELL] %s closed terminal %s of %s/%s after %s", userID, shellID, ownerID, projectID, time.Since(opened).Round(time.Second))
	}()
	defer codeShellViewer(shellID, socket, -1)
	defer codeShellConn(shellID, conn, false)

	// Access is re-checked while the shell is open: a viewer demoted or
	// removed loses it within codeShellRoleInterval (sharing changes and Code
	// deletion also close it at once, see stopCodeShellsFor).
	roleCtx, stopRoleCheck := context.WithCancel(context.Background())
	defer stopRoleCheck()
	go func() {
		ticker := time.NewTicker(codeShellRoleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-roleCtx.Done():
				return
			case <-ticker.C:
				if !codeRoleFor(roleCtx, userID, ownerID, projectID).atLeast(codeRoleEditor) {
					log.Printf("[CODE_SHELL] %s lost access to %s/%s; closing shell %s", userID, ownerID, projectID, shellID)
					// Both ends: the in-sandbox attach client exits too.
					_ = shell.Close()
					_ = conn.Close()
					return
				}
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			kind, data, err := shell.ReadMessage()
			if err != nil || kind != websocket.BinaryMessage {
				if err != nil {
					return
				}
				continue
			}
			if conn.WriteMessage(websocket.BinaryMessage, data) != nil {
				return
			}
		}
	}()
	go func() {
		<-done
		_ = conn.Close()
	}()
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch kind {
		case websocket.BinaryMessage:
			if shell.WriteMessage(websocket.BinaryMessage, data) != nil {
				return
			}
		case websocket.TextMessage:
			var control struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &control) == nil && control.Type == "resize" && control.Cols > 0 && control.Rows > 0 {
				resize, _ := json.Marshal(map[string]interface{}{"type": "resize", "cols": min(control.Cols, liveAttachMaxCols), "rows": min(control.Rows, liveAttachMaxRows)})
				if shell.WriteMessage(websocket.TextMessage, resize) != nil {
					return
				}
			}
		}
	}
}

// codeShellAttach opens the workspace service's in-sandbox attach to a
// running shell (binary frames = terminal bytes, text frames = JSON resize).
var codeShellAttachBase = getWorkspaceAPIURL

func codeShellAttach(ctx context.Context, shellID string, cols, rows int) (*websocket.Conn, error) {
	base := strings.TrimSuffix(codeShellAttachBase(), "/")
	switch {
	case strings.HasPrefix(base, "https://"):
		base = "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		base = "ws://" + strings.TrimPrefix(base, "http://")
	}
	target := base + "/api/shell/interactive/attach?" + url.Values{
		"shell_id": {shellID}, "cols": {strconv.Itoa(cols)}, "rows": {strconv.Itoa(rows)},
	}.Encode()
	header := http.Header{}
	if token := wsauth.Token(); token != "" {
		header.Set(wsauth.HeaderName, token)
	}
	if userID := codeShellUser(ctx); userID != "" {
		header.Set("X-User-ID", userID)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	conn, resp, err := dialer.DialContext(ctx, target, header)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("workspace attach: status %d: %w", resp.StatusCode, err)
		}
		return nil, err
	}
	return conn, nil
}

// POST /api/agent-profiles/code/projects/{project_id}/shell/stop — ends the
// caller's shell of this Code (its processes and files in the shell's own
// folder).
func (api *StreamingAPI) handleCodeShellStop(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_, shellID, _, status, message := api.codeShellTarget(r)
	if status != 0 {
		writeAgentProfileError(w, status, message)
		return
	}
	if err := codeShellStop(r.Context(), shellID); err != nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "could not stop the shell")
		return
	}
	codeShells.Lock()
	delete(codeShells.byID, shellID)
	codeShells.Unlock()
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"stopped": true})
}

func codeShellTrack(shellID, ownerID, projectID, userID string) {
	codeShells.Lock()
	defer codeShells.Unlock()
	state := codeShells.byID[shellID]
	if state == nil {
		state = &codeShellState{}
		codeShells.byID[shellID] = state
	}
	state.ownerID, state.projectID, state.userID = ownerID, projectID, userID
	state.lastSeen = time.Now()
}

func codeShellConn(shellID string, conn *websocket.Conn, open bool) {
	codeShells.Lock()
	defer codeShells.Unlock()
	state := codeShells.byID[shellID]
	if state == nil {
		return
	}
	if state.conns == nil {
		state.conns = map[*websocket.Conn]bool{}
	}
	if open {
		state.conns[conn] = true
	} else {
		delete(state.conns, conn)
	}
}

// codeGranteeID is the directory id of a user named by id, username or email, else the name as given.
func codeGranteeID(callerID string) string {
	callerID = strings.TrimSpace(callerID)
	if record := directoryUserFor(callerID, callerID, callerID); record != nil {
		return record.ID
	}
	return callerID
}

// stopCodeShellsFor ends the shells of one Code at once: every person's when
// users is empty (the Code was deleted), else only theirs (they lost access).
func stopCodeShellsFor(ownerID, projectID string, users []string) []string {
	match := map[string]bool{}
	for _, user := range users {
		match[codeGranteeID(user)] = true
		match[user] = true
	}
	codeShells.Lock()
	var ids []string
	idUser := map[string]string{}
	var conns []*websocket.Conn
	for id, state := range codeShells.byID {
		if state.ownerID != ownerID || state.projectID != projectID {
			continue
		}
		if len(users) > 0 && !match[state.userID] && !match[codeGranteeID(state.userID)] {
			continue
		}
		ids = append(ids, id)
		idUser[id] = state.userID
		for conn := range state.conns {
			conns = append(conns, conn)
		}
		delete(codeShells.byID, id)
	}
	codeShells.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
	for _, id := range ids {
		ctx, cancel := context.WithTimeout(withCodeShellUser(context.Background(), idUser[id]), 30*time.Second)
		if err := codeShellStop(ctx, id); err != nil {
			log.Printf("[CODE_SHELL] stop %s: %v", id, err)
		}
		cancel()
	}
	return ids
}

// sweepOrphanCodeShells stops Code shells a previous server process left
// running: at startup nothing tracks them, so nothing would ever stop them. The
// workspace service does it: it can see every slot's shell folder.
func sweepOrphanCodeShells() {
	codeShells.Lock()
	keep := make([]string, 0, len(codeShells.byID))
	for id := range codeShells.byID {
		keep = append(keep, id)
	}
	codeShells.Unlock()
	body, _ := json.Marshal(map[string]interface{}{"prefix": "code-", "keep": keep})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := codeShellWorkspaceCall(ctx, "/api/shell/interactive/sweep", body, nil); err != nil {
		log.Printf("[CODE_SHELL] sweep: %v", err)
	}
}

func codeShellViewer(shellID, socket string, delta int) {
	codeShells.Lock()
	defer codeShells.Unlock()
	state := codeShells.byID[shellID]
	if state == nil {
		state = &codeShellState{}
		codeShells.byID[shellID] = state
	}
	state.socket = socket
	state.viewers += delta
	if state.viewers < 0 {
		state.viewers = 0
	}
	state.lastSeen = time.Now()
}

// codeShellReaper stops shells nobody has watched for codeShellIdleTimeout.
func codeShellReaper() {
	swept := false
	for range time.Tick(time.Minute) {
		// The first tick, not startup itself: the workspace server may not
		// be up yet when this process starts.
		if !swept && codeShellSweepOnStart {
			sweepOrphanCodeShells()
			swept = true
		}
		stopIdleCodeShells(time.Now())
	}
}

func stopIdleCodeShells(now time.Time) []string {
	codeShells.Lock()
	var idle []string
	idleUser := map[string]string{}
	for id, state := range codeShells.byID {
		if state.viewers == 0 && now.Sub(state.lastSeen) >= codeShellIdleTimeout {
			idle = append(idle, id)
			idleUser[id] = state.userID
			delete(codeShells.byID, id)
		}
	}
	codeShells.Unlock()
	for _, id := range idle {
		ctx, cancel := context.WithTimeout(withCodeShellUser(context.Background(), idleUser[id]), 30*time.Second)
		if err := codeShellStop(ctx, id); err != nil {
			log.Printf("[CODE_SHELL] stop idle %s: %v", id, err)
		}
		cancel()
	}
	return idle
}
