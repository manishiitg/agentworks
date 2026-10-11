//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// Pin the reported process/session ambiguity at the real Unix activation
// boundary, including PID birth, private records and nofollow reads.
func TestPiSDKRecordAndLeaseActivation(t *testing.T) {
	root, err := os.MkdirTemp("", "pi-sdk-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("AGENTWORKS_BRIDGE_ROOT", "")
	defaultRoot, err := piBridgeRoot()
	cache, cacheErr := os.UserCacheDir()
	if err != nil || cacheErr != nil || defaultRoot != filepath.Join(cache, "agentworks", "bridge") {
		t.Fatal("Pi changed default bridge routing")
	}
	t.Setenv("AGENTWORKS_BRIDGE_ROOT", root)
	if selected, err := piBridgeRoot(); err != nil || selected != root {
		t.Fatal("private explicit routing refused", err)
	}
	link := root + "-link"
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	t.Setenv("AGENTWORKS_BRIDGE_ROOT", link)
	if _, err := piBridgeRoot(); err == nil {
		t.Fatal("symlink routing accepted")
	}
	t.Setenv("AGENTWORKS_BRIDGE_ROOT", root)
	executable, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	executable = filepath.Join(root, "pi")
	if err := os.WriteFile(executable, binary, 0700); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	key, err := piPIDHostKey(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "pi-sessions")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "pi-"+strconv.Itoa(child.Process.Pid)+"-test.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	session := "11111111-2222-4333-8444-555555555555"
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Host    string `json:"host_key"`
			Session string `json:"session_id"`
			Lease   string `json:"attachment_id"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Host != key || input.Session != session || input.Lease != "person-lease" {
			w.WriteHeader(http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(`{"active":true}`))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	record := piSessionRecord{PID: child.Process.Pid, Host: key, Session: session, Socket: socket, Mode: "tui", Cwd: root}
	file := filepath.Join(dir, strconv.Itoa(child.Process.Pid)+".json")
	write := func() {
		t.Helper()
		body, _ := json.Marshal(record)
		if err := os.WriteFile(file, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if err := activatePiSession(context.Background(), root, key, session, "wrong-lease"); err == nil {
		t.Fatal("wrong lease activated")
	}
	if err := activatePiSession(context.Background(), root, key, session, "person-lease"); err != nil {
		t.Fatal(err)
	}
	record.Host = "stale-birth"
	write()
	if _, err := readPiSession(root, file); err == nil {
		t.Fatal("stale process record accepted")
	}
	record.Host = key
	write()
	if err := os.Rename(file, file+".source"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file+".source", file); err != nil {
		t.Fatal(err)
	}
	if _, err := readPiSession(root, file); err == nil {
		t.Fatal("symlink registration accepted")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	write()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := readPiSession(root, file); err == nil {
		t.Fatal("public registration directory accepted")
	}
}
