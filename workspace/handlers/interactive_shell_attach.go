package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

// An attached tmux client executes commands its server sends it (for example
// `detach-client -E <command>` typed in the shell). A client outside the
// sandbox would therefore hand the shell the service's own rights. The attach
// client runs here, in the sandbox the shell was started in, and its terminal
// is streamed over a WebSocket the agent server proxies to the browser.
// Binary frames are terminal bytes; text frames are JSON control
// ({"type":"resize","cols":..,"rows":..}).

var interactiveShellSandboxes = struct {
	sync.Mutex
	byID map[string]security.Isolator
}{byID: map[string]security.Isolator{}}

func rememberInteractiveShell(id string, iso security.Isolator) {
	interactiveShellSandboxes.Lock()
	defer interactiveShellSandboxes.Unlock()
	interactiveShellSandboxes.byID[id] = iso
}

func forgetInteractiveShell(id string) {
	interactiveShellSandboxes.Lock()
	defer interactiveShellSandboxes.Unlock()
	delete(interactiveShellSandboxes.byID, id)
}

func interactiveShellSandbox(id string) (security.Isolator, bool) {
	interactiveShellSandboxes.Lock()
	defer interactiveShellSandboxes.Unlock()
	iso, ok := interactiveShellSandboxes.byID[id]
	return iso, ok
}

// Only the agent server connects (workspace token, no Origin): browsers
// always send an Origin on WebSocket handshakes.
var interactiveShellUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return r.Header.Get("Origin") == "" },
}

// AttachInteractiveShell (GET /api/shell/interactive/attach?shell_id=&cols=&rows=)
// attaches to a running shell from inside its sandbox.
func AttachInteractiveShell(c *gin.Context) {
	id := c.Query("shell_id")
	if !interactiveShellID.MatchString(id) {
		shellError(c, http.StatusBadRequest, "valid shell_id is required")
		return
	}
	slot, status, message := interactiveShellSlot(c)
	if status != 0 {
		shellError(c, status, message)
		return
	}
	iso, known := interactiveShellSandbox(id)
	_, socket, pathErr := interactiveShellPaths(id, slot)
	if pathErr != nil || !known || iso.Slot != slot || !interactiveShellRunning(socket) {
		shellError(c, http.StatusNotFound, "The shell is not running")
		return
	}
	cols, _ := strconv.Atoi(c.Query("cols"))
	rows, _ := strconv.Atoi(c.Query("rows"))
	cols, rows = clampShellSize(cols, rows)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := fmt.Sprintf("TERM=xterm-256color exec tmux -S %s attach -t %s", shellQuote(socket), interactiveShellSession)
	cmd, cleanup, err := iso.ExecuteIsolated(ctx, command, nil)
	if err != nil {
		shellError(c, http.StatusInternalServerError, "Failed to set up the sandbox: "+err.Error())
		return
	}
	if cleanup != nil {
		defer cleanup()
	}
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		shellError(c, http.StatusInternalServerError, "Could not attach to the shell")
		return
	}
	defer func() {
		_ = terminal.Close()
		cancel()
		_ = cmd.Wait()
	}()
	conn, err := interactiveShellUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, err := terminal.Read(buf)
			if n > 0 && conn.WriteMessage(websocket.BinaryMessage, buf[:n]) != nil {
				return
			}
			if err != nil {
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
			if _, err := terminal.Write(data); err != nil {
				return
			}
		case websocket.TextMessage:
			var control struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &control) == nil && control.Type == "resize" {
				cols, rows := clampShellSize(control.Cols, control.Rows)
				_ = pty.Setsize(terminal, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
			}
		}
	}
}
