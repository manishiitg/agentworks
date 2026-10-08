package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

func TestExecutorEnablesShellForEverySharedFolder(t *testing.T) {
	for _, downloads := range []bool{false, true} {
		t.Run(fmt.Sprintf("downloads=%v", downloads), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if err := os.Mkdir(filepath.Join(home, "Downloads"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AGENTWORKS_LANDLOCK_RUNNER", os.Getenv("AGENTWORKS_LANDLOCK_RUNNER"))
			received := make(chan localfiles.Hello, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/external/v1/devices/connect" || r.Header.Get("Authorization") != "Bearer test-token" {
					http.Error(w, "unexpected connection", 403)
					return
				}
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				var hello localfiles.Hello
				if err := conn.ReadJSON(&hello); err != nil {
					return
				}
				if err := conn.WriteJSON(map[string]bool{"connected": true}); err != nil {
					return
				}
				received <- hello
				var request any
				_ = conn.ReadJSON(&request)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var stdout, stderr bytes.Buffer
			finished := make(chan int, 1)
			args := []string{"--server", server.URL, "--config", filepath.Join(t.TempDir(), "executor.json"), "executor", "connect", "--device", "laptop", "--folder", "reference=" + t.TempDir(), "--write-folder", "project=" + t.TempDir()}
			if downloads {
				args = append(args, "--downloads")
			}
			go func() {
				finished <- run(ctx, args, strings.NewReader(""), &stdout, &stderr, func(key string) string {
					if key == "AGENTWORKS_TOKEN" {
						return "test-token"
					}
					return ""
				})
			}()
			select {
			case hello := <-received:
				count := 2
				if downloads {
					count++
				}
				if len(hello.Resources) != count {
					t.Fatalf("resources %+v", hello.Resources)
				}
				for _, resource := range hello.Resources {
					if !resource.Shell || resource.Writable != (resource.ID == "project" || resource.ID == "downloads") || resource.Downloads != (downloads && resource.ID != "downloads") {
						t.Fatalf("incorrect default permissions %+v", resource)
					}
				}
			case code := <-finished:
				t.Fatalf("executor exited %d: %s", code, stderr.String())
			case <-time.After(10 * time.Second):
				t.Fatal("executor did not connect")
			}
			cancel()
			select {
			case code := <-finished:
				if code != 0 {
					t.Fatalf("disconnect %d %s", code, stderr.String())
				}
				for _, message := range []string{"Sharing reference: read-only files and shell commands enabled", "Sharing project: file edits and shell commands enabled", "localhost services and your local network"} {
					if !strings.Contains(stderr.String(), message) {
						t.Fatalf("missing connection permissions %q: %s", message, stderr.String())
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("executor did not stop")
			}
		})
	}
}

func TestExecutorRetriesAStaleDeviceConnection(t *testing.T) {
	var attempts atomic.Int32
	connected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var hello localfiles.Hello
		if conn.ReadJSON(&hello) != nil {
			return
		}
		if attempts.Add(1) == 1 {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "old socket still closing"), time.Now().Add(time.Second))
			return
		}
		_ = conn.WriteJSON(map[string]bool{"connected": true})
		close(connected)
		var request any
		_ = conn.ReadJSON(&request)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stdout, stderr bytes.Buffer
	finished := make(chan int, 1)
	args := []string{"--server", server.URL, "--config", filepath.Join(t.TempDir(), "executor.json"), "executor", "connect", "--device", "laptop", "--folder", "project=" + t.TempDir()}
	go func() {
		finished <- run(ctx, args, strings.NewReader(""), &stdout, &stderr, func(key string) string {
			if key == "AGENTWORKS_TOKEN" {
				return "test-token"
			}
			return ""
		})
	}()
	select {
	case <-connected:
	case code := <-finished:
		t.Fatalf("quit during transient collision: %d", code)
	case <-time.After(5 * time.Second):
		t.Fatal("did not reconnect")
	}
	cancel()
	select {
	case code := <-finished:
		if code != 0 || attempts.Load() != 2 {
			t.Fatalf("reconnect failed: %d (%d attempts) %s", code, attempts.Load(), stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop")
	}
}

// `agentworks start` shares the folder you are standing in, read and write with shell, named after the folder and the
// computer, with no flags: the one-command flow the setup page describes.
func TestStartSharesTheCurrentFolderReadAndWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(t.TempDir(), "My App")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	received := make(chan localfiles.Hello, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var hello localfiles.Hello
		if conn.ReadJSON(&hello) != nil || conn.WriteJSON(map[string]bool{"connected": true}) != nil {
			return
		}
		received <- hello
		var request any
		_ = conn.ReadJSON(&request)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stdout, stderr bytes.Buffer
	finished := make(chan int, 1)
	go func() {
		finished <- run(ctx, []string{"--server", server.URL, "start", "--foreground", "--no-open", "--workspace", "My App"}, strings.NewReader(""), &stdout, &stderr, func(key string) string {
			if key == "AGENTWORKS_TOKEN" {
				return "test-token"
			}
			return ""
		})
	}()
	select {
	case hello := <-received:
		if hello.DeviceID != defaultDeviceName() || len(hello.Resources) != 1 {
			t.Fatalf("hello %+v", hello)
		}
		if r := hello.Resources[0]; r.ID != "my-app" || !r.Writable || !r.Shell {
			t.Fatalf("the folder must be shared read-and-write with shell, named after the folder: %+v", r)
		}
	case code := <-finished:
		t.Fatalf("start exited %d: %s", code, stderr.String())
	case <-time.After(10 * time.Second):
		t.Fatal("start did not connect")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("start did not stop")
	}
}

// The answers to start's first-run questions are remembered, so a later run does not ask again.
func TestStartRemembersItsFirstRunAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "start-preferences.json")
	if got := loadStartPrefs(path); got.Background != nil || got.Open != nil {
		t.Fatalf("no answers yet, got %+v", got)
	}
	background, open := true, false
	saveStartPrefs(path, startPrefs{Background: &background, Open: &open})
	got := loadStartPrefs(path)
	if got.Background == nil || !*got.Background || got.Open == nil || *got.Open {
		t.Fatalf("saved answers not restored: %+v", got)
	}
}

// Without a workspace the CLI refuses to start: which Code workspace uses a folder is never guessed.
func TestStartRequiresAWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := filepath.Join(t.TempDir(), "app")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"--server", "https://agents.example.test", "start", "--foreground", "--no-open"}, strings.NewReader(""), &stdout, &stderr, func(key string) string {
		if key == "AGENTWORKS_TOKEN" {
			return "test-token"
		}
		return ""
	})
	if code == 0 || !strings.Contains(stderr.String(), "--workspace") {
		t.Fatalf("expected a refusal naming --workspace, got %d: %s", code, stderr.String())
	}
}
