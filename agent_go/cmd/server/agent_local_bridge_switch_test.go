package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// The person's explicit move of one chat to another login: the new login gets
// the attachment, the old one is released, a failed move leaves the chat where it
// was, and a bare reattach keeps the connection the chat is on.
func TestAgentLocalBridgeSwitchesConnectionAndKeepsItOtherwise(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"bob","username":"bob","can_create":true,"products":[]}]}`)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "switch-fixture-secret")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	mux.HandleFunc("/api/external/v1/tools", env.api.handleExternalTools)
	mux.HandleFunc("/api/external/v1/agents/events", env.api.handleAgentEvents)
	mux.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment)
	srv := httptest.NewServer(AuthMiddleware(mux))
	defer srv.Close()
	t.Setenv("PUBLIC_URL", srv.URL)
	login := func(user string) string {
		t.Helper()
		device, verification, e := store.CreateCLIDevice(context.Background(), "crews:run", "workflows:read", "runs:execute")
		if e != nil {
			t.Fatal(e)
		}
		if e = store.DecideCLIDevice(context.Background(), verification, &UserClaims{UserID: user, Username: user}, true); e != nil {
			t.Fatal(e)
		}
		_, token, _, e := store.PollCLIDevice(context.Background(), device, srv.URL+"/api/external/v1")
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(t.TempDir(), user+".json")
		if e = agentworksclient.SaveConfig(path, agentworksclient.Config{Server: srv.URL, Token: token}); e != nil {
			t.Fatal(e)
		}
		return path
	}
	alicePath, bobPath := login("alice"), login("bob")
	deadPath := filepath.Join(t.TempDir(), "dead.json")
	if err = agentworksclient.SaveConfig(deadPath, agentworksclient.Config{Server: "http://127.0.0.1:9", Token: "none"}); err != nil {
		t.Fatal(err)
	}
	cache, err := os.MkdirTemp("", "aw-cache-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cache)
	t.Setenv("HOME", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	root, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agentworksclient.ServeLocalBridge(ctx, root) }()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, e := agentworksclient.CallLocalBridge(context.Background(), root, agentworksclient.LocalBridgeRequest{Action: "ping"}); e == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("local bridge did not start: %v", e)
		}
	}
	call := func(in agentworksclient.LocalBridgeRequest) agentworksclient.LocalBridgeReply {
		t.Helper()
		callCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		out, e := agentworksclient.CallLocalBridge(callCtx, root, in)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	attachedTo := func(user string) bool {
		t.Helper()
		agentMessageMu.Lock()
		s, e := readAgentMessageStore(context.Background())
		agentMessageMu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		for _, a := range s.Agents {
			if a.UserID == user && a.Name == "mover" {
				return a.Attachment != nil
			}
		}
		return false
	}
	base := agentworksclient.LocalBridgeRequest{Provider: "claude", SessionID: "exact-switch-session", HostKey: "exact-switch-process"}
	first := base
	first.Action, first.ConfigPath, first.AgentName = "attach", alicePath, "mover"
	if out := call(first); out.Error != "" || !out.Attached || !attachedTo("alice") {
		t.Fatalf("first attach: %+v", out)
	}
	// A failed move leaves the chat attached where it was.
	failing := base
	failing.Action, failing.ConfigPath, failing.ReuseIdentity, failing.SwitchConnection = "attach", deadPath, true, true
	if out, e := agentworksclient.CallLocalBridge(context.Background(), root, failing); e == nil && out.Error == "" {
		t.Fatalf("a move to an unreachable login reported success: %+v", out)
	}
	if st := call(agentworksclient.LocalBridgeRequest{Action: "status", Provider: base.Provider, SessionID: base.SessionID, HostKey: base.HostKey}); !st.Attached || !attachedTo("alice") {
		t.Fatalf("failed move disturbed the source: %+v", st)
	}
	move := base
	move.Action, move.ConfigPath, move.ReuseIdentity, move.SwitchConnection = "attach", bobPath, true, true
	if out := call(move); out.Error != "" || !out.Attached || out.Attachment.AgentName != "mover" {
		t.Fatalf("move: %+v", out)
	}
	if !attachedTo("bob") || attachedTo("alice") {
		t.Fatalf("after the move the chat must be on bob only: bob=%v alice=%v", attachedTo("bob"), attachedTo("alice"))
	}
	// A bare reattach names the old login but keeps the chat on its current one.
	bare := base
	bare.Action, bare.ConfigPath, bare.ReuseIdentity = "attach", alicePath, true
	if out := call(bare); out.Error != "" || !out.Attached {
		t.Fatalf("bare reattach: %+v", out)
	}
	if !attachedTo("bob") || attachedTo("alice") {
		t.Fatal("a bare reattach moved the chat back to the default login")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bridge did not stop")
	}
}
