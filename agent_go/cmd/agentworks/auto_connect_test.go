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
	var resumeFence string
	var networkRejoins int
	attaches, resumes, stopped, offline, replaced := 0, 0, false, false, false
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var in agentworksclient.LocalBridgeRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			return
		}
		out := agentworksclient.LocalBridgeReply{Stopped: stopped, Offline: offline, Attached: attached.ID != "" && !offline, Attachment: attached}
		if in.Action == "status" && in.AttachmentID != "" && in.AttachmentID != attached.ID {
			_ = json.NewEncoder(w).Encode(agentworksclient.LocalBridgeReply{})
			return
		}
		if in.Action == "status" {
			out.Networks = []agentworksclient.LocalNetworkState{{Network: "partner", AgentName: "saved-alias", ConfigPath: filepath.Join(root, "partner.json")}}
		}
		if in.Action == "attach" && in.Also {
			networkRejoins++
			if in.AttachmentID != "" || !in.Automatic || in.AgentName != "saved-alias" {
				t.Error("joined recovery leaked the primary fence or changed the saved alias")
			}
		} else if in.Action == "attach" {
			attaches++
			first = in
			attached = agentworksclient.AgentAttachment{ID: "lease", AgentName: in.AgentName, Provider: in.Provider, SessionID: in.SessionID}
			out.Attached, out.Attachment = true, attached
		}
		if in.Action == "resume" && !stopped {
			resumes++
			resumeFence = in.AttachmentID
			if replaced {
				stopped = true
				out.Stopped, out.Attached, out.Offline = true, false, true
				out.Error = "This chat was replaced by another attachment"
			} else {
				attached.ID = "renewed-lease"
				out.Attachment = attached
				offline = false
				out.Offline, out.Attached = false, true
			}
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
	saved := attached
	offline = true
	mu.Unlock()
	available := []liveAgentSession{{provider: "codex", session: uuid.NewString(), host: "another-chat"}}
	reconnect := savedChatReconnector(root, o, func(_ context.Context, _ string, probe *options, selected ...agentworksclient.AgentAttachment) ([]liveAgentSession, error) {
		if !probe.attachedOnly || len(selected) != 1 || selected[0].SessionID != saved.SessionID {
			t.Error("recovery requested discovery beyond the saved recovery candidates")
		}
		return available, nil
	})
	reconnect(context.Background(), []agentworksclient.AgentAttachment{saved})
	mu.Lock()
	if resumes != 0 {
		t.Fatal("recovery adopted another native thread")
	}
	mu.Unlock()
	available = append(available, liveAgentSession{provider: "codex", session: req.SessionID, host: "restarted-process", cwd: folder})
	reconnect(context.Background(), []agentworksclient.AgentAttachment{saved})
	mu.Lock()
	if resumes != 1 || offline || attached.AgentName != saved.AgentName || attaches != 1 || resumeFence != saved.ID || networkRejoins != 1 {
		t.Fatalf("recovery did not retain the exact saved chat: resumes=%d offline=%v attached=%+v", resumes, offline, attached)
	}
	saved = attached
	offline, replaced = true, true
	mu.Unlock()
	reconnect(context.Background(), []agentworksclient.AgentAttachment{saved})
	reconnect(context.Background(), []agentworksclient.AgentAttachment{saved})
	mu.Lock()
	if resumes != 2 || !stopped {
		t.Fatalf("replaced attachment was retried: resumes=%d stopped=%v", resumes, stopped)
	}
	stopped, attached = true, agentworksclient.AgentAttachment{}
	mu.Unlock()
	reconnect(context.Background(), []agentworksclient.AgentAttachment{saved})
	if err = autoConnectChat(context.Background(), root, o, req, folder); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attaches != 1 || resumes != 2 {
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
	directoryCalls := 0
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in agentworksclient.LocalBridgeRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		if in.Action == "peers" || in.Action == "list" {
			directoryCalls++
		}
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
	if directoryCalls != 0 {
		t.Fatalf("move repeated the whole directory %d times", directoryCalls)
	}
	if len(moves) != 1 || moves[0].Setup || !moves[0].SwitchConnection || moves[0].AgentName != "cursor-agent" || moves[0].ConfigPath != login {
		t.Fatalf("move must keep identity without reinstalling delivery: %+v", moves)
	}
}
