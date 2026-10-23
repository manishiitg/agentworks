package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// Exercise the actual local HTTP boundary: first native connection must select
// its own stable address/default login, never an earlier folder agent's inbox.
func TestAutomaticChatKeepsNativeIdentityAndExplicitDisconnect(t *testing.T) {
	root, err := os.MkdirTemp("", "aw-auto-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err = agentworksclient.PrepareLocalBridgeRoot(root); err != nil {
		t.Fatal(err)
	}
	login := filepath.Join(root, "login.json")
	if err = agentworksclient.SaveConfig(login, agentworksclient.Config{Server: "https://example.com", Token: "fixture"}); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(root, "bridge.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err = os.Chmod(filepath.Join(root, "bridge.sock"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var attached agentworksclient.AgentAttachment
	var first agentworksclient.LocalBridgeRequest
	attaches, stopped := 0, false
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var in agentworksclient.LocalBridgeRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			return
		}
		out := agentworksclient.LocalBridgeReply{Stopped: stopped, Attached: attached.ID != "", Attachment: attached}
		if in.Action == "attach" {
			attaches++
			first = in
			attached = agentworksclient.AgentAttachment{ID: "lease", AgentName: in.AgentName, Provider: in.Provider, SessionID: in.SessionID}
			out.Attached, out.Attachment = true, attached
		}
		_ = json.NewEncoder(w).Encode(out)
	})}
	go srv.Serve(l)
	defer srv.Close()
	o := &options{configPath: login, getenv: func(string) string { return "" }, stdout: io.Discard, stderr: io.Discard}
	folder := filepath.Join(root, "project")
	rememberAutoAttach(root, "codex", folder, agentworksclient.AgentAttachment{AgentName: "older-folder-agent"}, "another-login", "")
	req := agentworksclient.LocalBridgeRequest{Provider: "codex", SessionID: uuid.NewString(), HostKey: "verified-process"}
	for i := 0; i < 2; i++ {
		if err = autoConnectChat(context.Background(), root, o, req, folder); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	if attaches != 1 || !first.Automatic || first.Setup || first.Also || first.SwitchConnection || first.ConfigPath != login || first.AgentName == "older-folder-agent" || first.SessionID != req.SessionID {
		t.Fatalf("wrong automatic connection: count=%d %+v", attaches, first)
	}
	stopped, attached = true, agentworksclient.AgentAttachment{}
	mu.Unlock()
	if err = autoConnectChat(context.Background(), root, o, req, folder); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attaches != 1 {
		t.Fatal("explicit disconnect was overridden")
	}
}

// Account moves use the existing native/hook setup. Cursor has no shared plugin
// installer, so a real move used to fail before reaching the helper at all.
func TestMoveCursorChatKeepsItsExistingDeliverySetup(t *testing.T) {
	root, err := os.MkdirTemp("", "aw-move-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err = agentworksclient.PrepareLocalBridgeRoot(root); err != nil {
		t.Fatal(err)
	}
	login := filepath.Join(root, "login.json")
	if err = agentworksclient.SaveConfig(login, agentworksclient.Config{Server: "https://example.com", Token: "fixture"}); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(root, "bridge.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err = os.Chmod(filepath.Join(root, "bridge.sock"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	session := uuid.NewString()
	attachment := agentworksclient.AgentAttachment{ID: "lease", AgentName: "cursor-agent", Provider: "cursor", SessionID: session}
	var mu sync.Mutex
	var moves []agentworksclient.LocalBridgeRequest
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in agentworksclient.LocalBridgeRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		if in.Action == "attach" {
			moves = append(moves, in)
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(agentworksclient.LocalBridgeReply{Attached: true, Attachment: attachment})
	})}
	go srv.Serve(l)
	defer srv.Close()
	o := &options{configPath: login, switchPath: login, getenv: func(string) string { return "" }, stdout: io.Discard, stderr: io.Discard}
	chats := []liveAgentSession{{provider: "cursor", session: session, host: "verified-process", name: "cursor-agent"}}
	if err = moveAttachedChats(context.Background(), o, root, chats, "default", true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(moves) != 1 || moves[0].Setup || !moves[0].SwitchConnection || moves[0].AgentName != "cursor-agent" || moves[0].ConfigPath != login {
		t.Fatalf("move must keep identity without reinstalling delivery: %+v", moves)
	}
}
