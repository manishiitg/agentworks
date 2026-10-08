package main

import (
	"bytes"
	"context"
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
		if len(hello.Resources) != 2 {
			t.Fatalf("resources %+v", hello.Resources)
		}
		for _, resource := range hello.Resources {
			if !resource.Shell || resource.Writable != (resource.ID == "project") {
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
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not stop")
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
