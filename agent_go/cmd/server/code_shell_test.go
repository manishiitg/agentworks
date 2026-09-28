package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

func TestCodeShellAccessFollowsRole(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	target := func(userID string) (string, string, int) {
		req := mux.SetURLVars(profileRouteRequest(http.MethodGet, "/x", nil, userID), map[string]string{"project_id": "c0de0001-0000"})
		_, shellID, root, status, _ := api.codeShellTarget(req)
		return shellID, root, status
	}
	ownerShell, root, status := target("owner")
	if status != 0 || root != codePrivacyOwnerRoot || !strings.HasPrefix(ownerShell, "code-") {
		t.Fatalf("owner shell = %q %q %d", ownerShell, root, status)
	}
	if _, _, status := target("other"); status != http.StatusNotFound {
		t.Fatalf("a stranger reached the shell: %d", status)
	}
	putCodeShares(t, api, "owner", `{"grants":[{"user":"other","role":"viewer"}]}`)
	if _, _, status := target("other"); status != http.StatusForbidden {
		t.Fatalf("a viewer got a shell: %d", status)
	}
	putCodeShares(t, api, "owner", `{"grants":[{"user":"other","role":"editor"}]}`)
	editorShell, editorRoot, status := target("other")
	if status != 0 || editorRoot != codePrivacyOwnerRoot {
		t.Fatalf("editor shell = %q %d", editorRoot, status)
	}
	// Each person gets their own shell of the Code.
	if editorShell == ownerShell || len(ownerShell) > 48 {
		t.Fatalf("shell ids owner=%q editor=%q", ownerShell, editorShell)
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
// the workspace's sandboxed shell: keystrokes reach the shell and its output
// comes back.
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

	router := mux.NewRouter()
	router.HandleFunc("/shell/{project_id}", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"}))
		api.handleCodeShellStream(w, r)
	})
	server := httptest.NewServer(router)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/shell/c0de0001-0000?cols=80&rows=24", nil)
	if err != nil {
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
			return
		}
	}
	t.Fatalf("shell output never arrived: %q", seen.String())
}
