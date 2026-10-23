package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

// Run the real CLI in a separate process, without building on a developer's
// computer or substituting a fake device process for the live socket path.
func init() {
	if os.Getenv("AGENTWORKS_LAPTOP_UPDATE_TEST_CHILD") == "1" {
		cliBuild = os.Getenv("AGENTWORKS_LAPTOP_UPDATE_TEST_BUILD")
		os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
	}
}

func TestLaptopUpdateRestoresSameConnection(t *testing.T) {
	home := t.TempDir()
	for _, env := range []string{"HOME", "APPDATA", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(env, home)
	}
	t.Setenv("AGENTWORKS_TOKEN", "")
	t.Setenv("AGENTWORKS_SERVER", "")
	t.Setenv("AGENTWORKS_LAPTOP_UPDATE_TEST_CHILD", "1")
	t.Setenv("AGENTWORKS_LAPTOP_UPDATE_TEST_BUILD", "old-build")
	var owner atomic.Value
	owner.Store("original-owner")
	hellos := make(chan localfiles.Hello, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("inspect") == "1" {
			_ = json.NewEncoder(w).Encode(map[string]any{"owner_id": owner.Load()})
			return
		}
		if r.URL.Query().Get("owner_id") != owner.Load().(string) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var hello localfiles.Hello
		if conn.ReadJSON(&hello) != nil || conn.WriteJSON(map[string]bool{"connected": true}) != nil {
			return
		}
		hellos <- hello
		var request any
		_ = conn.ReadJSON(&request)
	}))
	defer server.Close()
	config := filepath.Join(home, "connection.json")
	if err := agentworksclient.SaveConfig(config, agentworksclient.Config{Server: server.URL, Token: "test-token"}); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	o := &options{configPath: config, serverURL: server.URL, getenv: os.Getenv, stdout: io.Discard, stderr: io.Discard}
	dir, err := o.shareDir()
	if err != nil {
		t.Fatal(err)
	}
	key := shareKey(config)
	state := filepath.Join(dir, key+".json")
	defer func() {
		if st, ok := readShareState(state); ok {
			terminateProcess(st.PID, true)
		}
	}()
	if err := runShareBackground(t.Context(), o, startFlags{executable: exe}, "", "my-laptop", "", "", server.URL, dir, key, state, false, func() error { return fmt.Errorf("unexpected login") }); err != nil {
		t.Fatal(err)
	}
	before, _ := readShareState(state)
	first := <-hellos
	if first.DeviceID != "my-laptop" || !first.SettingsManaged || len(first.Resources) != 0 || before.OwnerID != "original-owner" || before.CLIBuild != "old-build" {
		t.Fatalf("unexpected initial connection: %+v %+v", before, first)
	}
	t.Setenv("AGENTWORKS_LAPTOP_UPDATE_TEST_BUILD", "new-build")
	if err := refreshLaptopConnections(t.Context(), o, exe, "new-build"); err != nil {
		t.Fatal(err)
	}
	after, _ := readShareState(state)
	second := <-hellos
	if after.PID == before.PID || after.Device != before.Device || after.ConfigPath != before.ConfigPath || after.Server != before.Server || after.OwnerID != before.OwnerID || after.CLIBuild != "new-build" || second.DeviceID != first.DeviceID || !second.SettingsManaged || len(second.Resources) != 0 {
		t.Fatalf("update changed identity or grants: before=%+v after=%+v hello=%+v", before, after, second)
	}
	// A current CLI still refreshes an old endpoint, but an already refreshed
	// endpoint is not interrupted again. A switched account never replaces it.
	if err := refreshLaptopConnections(t.Context(), o, exe, "new-build"); err != nil {
		t.Fatal(err)
	}
	changed := after
	changed.ProcessStart = "different-process"
	writeShareState(state, changed)
	if err := refreshLaptopConnections(t.Context(), o, exe, "next-build"); err == nil || !strings.Contains(err.Error(), "process") {
		t.Fatalf("reused PID accepted: %v", err)
	}
	if !processAlive(after.PID) {
		t.Fatal("a different process start was terminated")
	}
	writeShareState(state, after)
	owner.Store("another-owner")
	if err := refreshLaptopConnections(t.Context(), o, exe, "next-build"); err == nil || !strings.Contains(err.Error(), "different account") {
		t.Fatalf("account switch accepted: %v", err)
	}
	unchanged, _ := readShareState(state)
	if unchanged.PID != after.PID || !processAlive(after.PID) {
		t.Fatal("refused account change stopped the original connection")
	}
	select {
	case <-hellos:
		t.Fatal("unexpected third device connection")
	case <-time.After(50 * time.Millisecond):
	}
}
