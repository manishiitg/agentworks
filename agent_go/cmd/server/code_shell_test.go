package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsauth"
)

func TestCodeShellIsOwnerOnly(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	target := func(userID string) (string, string, int) {
		req := mux.SetURLVars(profileRouteRequest(http.MethodGet, "/x", nil, userID), map[string]string{"project_id": "c0de0001-0000"})
		_, shellID, root, status, _ := api.codeShellTarget(req)
		return shellID, root, status
	}
	ownerShell, root, status := target("owner")
	if status != 0 || root != codePrivacyOwnerRoot || !strings.HasPrefix(ownerShell, "code-") || len(ownerShell) > 48 {
		t.Fatalf("owner shell = %q %q %d", ownerShell, root, status)
	}
	// Code is owner-only: nobody else reaches the owner's terminal, whatever the share list says.
	if _, _, status := target("other"); status != http.StatusNotFound {
		t.Fatalf("a stranger reached the shell: %d", status)
	}
}

func TestCodeShellFolderGuardIsTheProjectOnly(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	guard := api.codeShellFolderGuard(ctx, "owner", codePrivacyOwnerRoot)
	if !guard.Enabled || len(guard.WritePaths) != 1 || guard.WritePaths[0] != codePrivacyOwnerRoot+"/" {
		t.Fatalf("writes = %v", guard.WritePaths)
	}
	for _, read := range guard.ReadPaths {
		if !strings.HasPrefix(read, codePrivacyOwnerRoot) && !strings.HasSuffix(read, "/") {
			t.Fatalf("unexpected read %q", read)
		}
		if strings.Contains(read, "_users/") && !strings.HasPrefix(read, codePrivacyOwnerRoot) {
			t.Fatalf("the shell reads another user's tree: %q", read)
		}
	}
	protected := strings.Join(guard.BlockedWritePaths, " ")
	for _, file := range []string{"CLAUDE.md", "AGENTS.md", ".claude"} {
		if !strings.Contains(protected, codePrivacyOwnerRoot+"/"+file) {
			t.Fatalf("%s is not write-protected: %v", file, guard.BlockedWritePaths)
		}
	}
}

func TestIdleCodeShellsAreStopped(t *testing.T) {
	var stopped []string
	previous := codeShellStop
	codeShellStop = func(_ context.Context, id string) error { stopped = append(stopped, id); return nil }
	t.Cleanup(func() { codeShellStop = previous })

	codeShellViewer("code-watched", "/s1", +1)
	codeShellViewer("code-idle", "/s2", +1)
	codeShellViewer("code-idle", "/s2", -1)
	t.Cleanup(func() {
		codeShells.Lock()
		delete(codeShells.byID, "code-watched")
		delete(codeShells.byID, "code-idle")
		codeShells.Unlock()
	})
	if got := stopIdleCodeShells(time.Now()); len(got) != 0 {
		t.Fatalf("stopped a fresh shell: %v", got)
	}
	if got := stopIdleCodeShells(time.Now().Add(codeShellIdleTimeout + time.Minute)); len(got) != 1 || got[0] != "code-idle" || len(stopped) != 1 {
		t.Fatalf("idle stop = %v (stopped %v); a watched shell must stay", got, stopped)
	}
}

// The WebSocket bridge end to end against a real tmux server standing in for
// the workspace's sandboxed shell: keystrokes reach the shell through the
// workspace's attach and its output comes back. Nothing here attaches to the
// shell's tmux server itself.
func TestCodeShellStreamBridgesARealTmuxShell(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	api, _ := newCodePrivacyFixture(t)
	// Unix socket paths are short (104 bytes on macOS); TempDir is too deep.
	dir, err := os.MkdirTemp("/tmp", "cs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "t.sock")
	if out, err := exec.Command("tmux", "-S", socket, "new-session", "-d", "-s", "shell", "-x", "80", "-y", "24", "/bin/sh").CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	previous := codeShellStart
	var gotGuard *workspace.FolderGuardConfig
	codeShellStart = func(_ context.Context, _ string, _ string, guard *workspace.FolderGuardConfig, _, _ int) (string, error) {
		gotGuard = guard
		return socket, nil
	}
	t.Cleanup(func() { codeShellStart = previous })

	// The workspace service's in-sandbox attach, stood in by a plain attach
	// in a PTY: this test covers the agent server's side of the bridge.
	var attachQuery, attachToken string
	workspaceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/shell/interactive/attach" {
			http.NotFound(w, r)
			return
		}
		attachQuery, attachToken = r.URL.RawQuery, r.Header.Get(wsauth.HeaderName)
		attach := exec.Command("tmux", "-S", socket, "attach", "-t", "shell")
		attach.Env = append(os.Environ(), "TERM=xterm-256color")
		terminal, err := pty.StartWithSize(attach, &pty.Winsize{Cols: 80, Rows: 24})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = terminal.Close(); _ = attach.Process.Kill(); _ = attach.Wait() }()
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		go func() {
			buf := make([]byte, 4096)
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
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if kind == websocket.BinaryMessage {
				_, _ = terminal.Write(data)
			}
		}
	}))
	defer workspaceServer.Close()
	previousBase := codeShellAttachBase
	codeShellAttachBase = func() string { return workspaceServer.URL }
	t.Cleanup(func() { codeShellAttachBase = previousBase })
	t.Setenv("WORKSPACE_API_TOKEN", "ws-token")

	router := mux.NewRouter()
	router.HandleFunc("/shell/{project_id}", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"}))
		api.handleCodeShellStream(w, r)
	})
	server := httptest.NewServer(router)
	defer server.Close()
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/shell/c0de0001-0000?cols=80&rows=24", nil)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("dial: %v: %d %s", err, resp.StatusCode, body)
		}
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if gotGuard == nil || gotGuard.WritePaths[0] != codePrivacyOwnerRoot+"/" {
		t.Fatalf("shell started without the Code's guard: %+v", gotGuard)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":100,"rows":30}`)); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("echo code-shell-$((6*7))\r")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var seen strings.Builder
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(deadline)
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		seen.Write(data)
		if strings.Contains(seen.String(), "code-shell-42") {
			if attachToken != "ws-token" || !strings.Contains(attachQuery, "shell_id=code-") {
				t.Fatalf("attach went to the workspace without its token or shell: %q %q", attachToken, attachQuery)
			}
			return
		}
	}
	t.Fatalf("shell output never arrived: %q", seen.String())
}

// Losing access closes an open shell at once: unsharing or demoting a person
// stops only their shell, deleting the Code stops everyone's.
func TestRevokedCodeShellsAreStoppedAndClosed(t *testing.T) {
	previous := codeShellStop
	var stopped []string
	codeShellStop = func(_ context.Context, id string) error {
		stopped = append(stopped, id)
		return nil
	}
	t.Cleanup(func() { codeShellStop = previous })
	codeShells.Lock()
	saved := codeShells.byID
	codeShells.byID = map[string]*codeShellState{}
	codeShells.Unlock()
	t.Cleanup(func() {
		codeShells.Lock()
		codeShells.byID = saved
		codeShells.Unlock()
	})

	ownerShell := codeShellID("owner", "p1", "owner", 1)
	editorShell := codeShellID("owner", "p1", "editor", 1)
	otherCode := codeShellID("owner", "p2", "editor", 1)
	codeShellTrack(ownerShell, "owner", "p1", "owner")
	codeShellTrack(editorShell, "owner", "p1", "editor")
	codeShellTrack(otherCode, "owner", "p2", "editor")

	if got := stopCodeShellsFor("owner", "p1", []string{"editor"}); len(got) != 1 || got[0] != editorShell {
		t.Fatalf("unshare stopped %v, want only the editor's shell", got)
	}
	if got := stopCodeShellsFor("owner", "p1", nil); len(got) != 1 || got[0] != ownerShell {
		t.Fatalf("delete stopped %v, want the owner's remaining shell", got)
	}
	codeShells.Lock()
	_, kept := codeShells.byID[otherCode]
	codeShells.Unlock()
	if !kept || len(stopped) != 2 {
		t.Fatalf("another Code's shell must survive; stopped=%v", stopped)
	}
}

// The test binary must never stop a developer's real Code shells.
func init() { codeShellSweepOnStart = false }

// A person has at most three terminals per Code. Tab 1 is the shell a single terminal always had, so one running
// before tabs existed carries over; tabs 2 and 3 are separate shells; anything else is refused.
func TestCodeShellTabs(t *testing.T) {
	legacy := func(ownerID, projectID, userID string) string {
		sum := sha256.Sum256([]byte(ownerID + "\x00" + projectID + "\x00" + userID))
		return "code-" + hex.EncodeToString(sum[:])[:32]
	}
	if got := codeShellID("o", "p", "u", 1); got != legacy("o", "p", "u") {
		t.Fatalf("tab 1 must keep the pre-tab shell id: %s", got)
	}
	seen := map[string]bool{}
	for tab := 1; tab <= codeShellMaxTabs; tab++ {
		id := codeShellID("o", "p", "u", tab)
		if seen[id] || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`).MatchString(id) {
			t.Fatalf("tab %d id %q is reused or not a valid workspace shell id", tab, id)
		}
		seen[id] = true
	}
	for raw, want := range map[string]int{"": 1, "1": 1, "2": 2, "3": 3} {
		r := httptest.NewRequest(http.MethodGet, "/x?tab="+raw, nil)
		if got, ok := codeShellTab(r); !ok || got != want {
			t.Errorf("tab=%q -> %d,%v want %d", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"0", "4", "-1", "x", "2.5", "99"} {
		if _, ok := codeShellTab(httptest.NewRequest(http.MethodGet, "/x?tab="+raw, nil)); ok {
			t.Errorf("tab=%q must be refused", raw)
		}
	}
}
