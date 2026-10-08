package agentworksclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func TestExecutorShellKeepsHeartbeatAndHandlesCancellation(t *testing.T) {
	capability := security.CurrentSandboxCapability()
	if !capability.Available || runtime.GOOS == "linux" && capability.Backend != "landlock" {
		t.Skip("OS sandbox unavailable")
	}
	base := t.TempDir()
	root := filepath.Join(base, "project")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "readme"), []byte("local file"), 0600)
	executor, err := localfiles.Open("laptop", []localfiles.Grant{{Resource: localfiles.Resource{ID: "project", Writable: true, Shell: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}}}, Root: root, State: filepath.Join(base, "state")}})
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Close()
	finished := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finished <- func() error {
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return err
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(8 * time.Second))
			var hello localfiles.Hello
			if err = conn.ReadJSON(&hello); err != nil {
				return err
			}
			if err = conn.WriteJSON(map[string]bool{"connected": true}); err != nil {
				return err
			}
			if err = conn.WriteJSON(localfiles.Request{ID: "command", ResourceID: "project", Operation: "shell", Path: ".", Command: "echo ready > started; sleep 30", RequestID: "long-command"}); err != nil {
				return err
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err = os.Stat(filepath.Join(root, "started")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("command did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			pong := false
			conn.SetPongHandler(func(string) error { pong = true; return nil })
			if err = conn.WriteControl(websocket.PingMessage, []byte("heartbeat"), time.Now().Add(time.Second)); err != nil {
				return err
			}
			if err = conn.WriteJSON(localfiles.Request{ID: "read", ResourceID: "project", Operation: "read", Path: "readme"}); err != nil {
				return err
			}
			var response localfiles.Response
			if err = conn.ReadJSON(&response); err != nil {
				return err
			}
			if response.ID != "read" || response.File == nil || response.File.Content != "local file" {
				return fmt.Errorf("long command blocked file request: %+v", response)
			}
			if !pong {
				return fmt.Errorf("long command blocked heartbeat")
			}
			if err = conn.WriteJSON(localfiles.Request{ID: "command", Operation: "cancel"}); err != nil {
				return err
			}
			if err = conn.ReadJSON(&response); err != nil {
				return err
			}
			if response.ID != "command" || response.Shell == nil || response.Shell.ExitCode == 0 {
				return fmt.Errorf("command did not cancel: %+v", response)
			}
			return nil
		}()
	}))
	defer server.Close()
	client, err := New(server.URL, "executor-test-token")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	returned := make(chan error, 1)
	go func() { returned <- client.ServeExecutor(ctx, executor, nil) }()
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("executor hung after disconnect")
	}
}
