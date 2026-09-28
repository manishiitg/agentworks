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
// exactly as a chat turn builds it. The browser attaches over a WebSocket
// bridged to `tmux -S <socket> attach` in a PTY. Each person with editor
// access or more gets their own shell of the Code; viewers get none. A shell
// with no viewer for codeShellIdleTimeout is stopped.

const codeShellIdleTimeout = 30 * time.Minute

type codeShellState struct {
	socket   string
	viewers  int
	lastSeen time.Time
}

var codeShells = struct {
	sync.Mutex
	byID map[string]*codeShellState
}{byID: map[string]*codeShellState{}}

var codeShellReaperOnce sync.Once

// codeShellID names one person's shell of one Code (the workspace allows
// ^[a-z0-9][a-z0-9-]{0,47}$).
func codeShellID(ownerID, projectID, userID string) string {
	sum := sha256.Sum256([]byte(ownerID + "\x00" + projectID + "\x00" + userID))
	return "code-" + hex.EncodeToString(sum[:])[:32]
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
	claims := GetUserFromContext(r.Context())
	projectID := strings.TrimSpace(mux.Vars(r)["project_id"])
	if claims == nil || strings.TrimSpace(claims.UserID) == "" || projectID == "" || api == nil || api.agentProfiles == nil {
		return "", "", "", http.StatusNotFound, "Code workspace not found"
	}
	profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, claims.UserID)
	if err != nil || !userAllowedProduct(claims, profile.Product) {
		return "", "", "", http.StatusNotFound, "Code workspace not found"
	}
	project, err := resolveCrewProjectBinding(r.Context(), claims.UserID, profile, projectID, "")
	if err != nil {
		return "", "", "", http.StatusNotFound, "Code workspace not found"
	}
	if !codeRoleFor(r.Context(), claims.UserID, project.OwnerID, project.Binding.ResourceID).atLeast(codeRoleEditor) {
		return "", "", "", http.StatusForbidden, "the shell needs editor access to this Code workspace"
	}
	root = agentProfileRuntimeWorkspace(project.OwnerID, project.Binding.WorkspacePath)
	return claims.UserID, codeShellID(project.OwnerID, projectID, claims.UserID), root, 0, ""
}

// codeShellStart starts (or reuses) the sandboxed shell; the workspace call is
// idempotent. It returns the shell's tmux socket.
var codeShellStart = func(ctx context.Context, shellID, workDir string, guard *workspace.FolderGuardConfig, cols, rows int) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"shell_id": shellID, "working_directory": workDir, "folder_guard": guard, "cols": cols, "rows": rows,
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

func codeShellWorkspaceCall(ctx context.Context, route string, body []byte, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(getWorkspaceAPIURL(), "/")+route, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	wsauth.SetHeader(req)
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
	userID, shellID, root, status, message := api.codeShellTarget(r)
	if status != 0 {
		http.Error(w, message, status)
		return
	}
	cols, rows := liveAttachInitialSize(r)
	socket, err := codeShellStart(r.Context(), shellID, root, api.codeShellFolderGuard(r.Context(), userID, root), cols, rows)
	if err != nil {
		log.Printf("[CODE_SHELL] start %s: %v", shellID, err)
		http.Error(w, "could not start the shell", http.StatusServiceUnavailable)
		return
	}
	codeShellReaperOnce.Do(func() { go codeShellReaper() })

	// The attach client runs in the shell's own sandbox on the workspace
	// service: a tmux client executes what its server tells it to
	// (`detach-client -E`), so one attached from here would give the shell
	// this server's rights.
	shell, err := codeShellAttach(r.Context(), shellID, cols, rows)
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
	defer codeShellViewer(shellID, socket, -1)

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

func codeShellViewer(shellID, socket string, delta int) {
	codeShells.Lock()
	defer codeShells.Unlock()
	state := codeShells.byID[shellID]
	if state == nil {
		state = &codeShellState{socket: socket}
		codeShells.byID[shellID] = state
	}
	state.viewers += delta
	if state.viewers < 0 {
		state.viewers = 0
	}
	state.lastSeen = time.Now()
}

// codeShellReaper stops shells nobody has watched for codeShellIdleTimeout.
func codeShellReaper() {
	for range time.Tick(time.Minute) {
		stopIdleCodeShells(time.Now())
	}
}

func stopIdleCodeShells(now time.Time) []string {
	codeShells.Lock()
	var idle []string
	for id, state := range codeShells.byID {
		if state.viewers == 0 && now.Sub(state.lastSeen) >= codeShellIdleTimeout {
			idle = append(idle, id)
			delete(codeShells.byID, id)
		}
	}
	codeShells.Unlock()
	for _, id := range idle {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := codeShellStop(ctx, id); err != nil {
			log.Printf("[CODE_SHELL] stop idle %s: %v", id, err)
		}
		cancel()
	}
	return idle
}
