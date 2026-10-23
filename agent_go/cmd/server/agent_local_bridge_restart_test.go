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

// After a CLI update or a bridge restart every attached chat is offline until its
// provider's next hook event; that event resumes it as itself (same name and
// login), for every provider, with no attach step from the person.
func TestAgentLocalBridgeAttachedChatsComeBackAfterRestart(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"root","username":"root","admin":true,"products":[]}]}`)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "restart-fixture-secret")
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
	device, verification, err := store.CreateCLIDevice(context.Background(), "crews:run", "workflows:read", "runs:execute")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.DecideCLIDevice(context.Background(), verification, &UserClaims{UserID: "alice", Username: "alice"}, true); err != nil {
		t.Fatal(err)
	}
	_, token, _, err := store.PollCLIDevice(context.Background(), device, srv.URL+"/api/external/v1")
	if err != nil {
		t.Fatal(err)
	}
	loginPath := filepath.Join(t.TempDir(), "alice.json")
	if err = agentworksclient.SaveConfig(loginPath, agentworksclient.Config{Server: srv.URL, Token: token}); err != nil {
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
	start := func(alive bool) (context.CancelFunc, <-chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- agentworksclient.ServeLocalBridge(ctx, root, agentworksclient.LocalBridgeOptions{HostAlive: func(string, string) bool { return alive }})
		}()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if _, e := agentworksclient.CallLocalBridge(context.Background(), root, agentworksclient.LocalBridgeRequest{Action: "ping"}); e == nil {
				return cancel, done
			}
			if time.Now().After(deadline) {
				t.Fatal("local bridge did not start")
			}
		}
	}
	call := func(in agentworksclient.LocalBridgeRequest) agentworksclient.LocalBridgeReply {
		t.Helper()
		ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		out, e := agentworksclient.CallLocalBridge(ctx, root, in)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	chats := []agentworksclient.LocalBridgeRequest{
		{Provider: "claude", SessionID: "claude-session", HostKey: "claude-process", AgentName: "claude-chat"},
		{Provider: "codex", SessionID: "codex-session", HostKey: "codex-process", AgentName: "codex-chat"},
		{Provider: "muse", SessionID: "muse-session", HostKey: "muse-process", AgentName: "muse-chat"},
	}
	cancel, done := start(true)
	for _, chat := range chats {
		attach := chat
		attach.Action, attach.ConfigPath, attach.Automatic = "attach", loginPath, true
		if out := call(attach); !out.Attached {
			t.Fatalf("%s did not attach: %+v", chat.Provider, out)
		}
	}
	// Automatic first connection is private, retains identity on repetition,
	// and an explicit detach remains stopped across a helper restart.
	snapshot, err := readAgentMessageStore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Shares) != 0 {
		t.Fatal("automatic connection published an agent")
	}
	detached := chats[2]
	detached.Action = "detach"
	call(detached)
	detached.Action = "status"
	if !call(detached).Stopped {
		t.Fatal("detach was forgotten")
	}
	detached.Action, detached.ConfigPath, detached.Automatic = "attach", loginPath, true
	if _, err := agentworksclient.CallLocalBridge(context.Background(), root, detached); err == nil {
		t.Fatal("automatic event undid explicit detach")
	}
	// An administrator disconnects the Claude chat. It must not come back by itself.
	listing, _, _, _ := env.api.bullpenAdminAct(context.Background(), "root", "list", map[string]any{})
	closedAgent, handle := "", ""
	for _, person := range listing["people"].([]map[string]interface{}) {
		for _, agent := range person["agents"].([]map[string]interface{}) {
			if agent["agent_name"] == "claude-chat" {
				closedAgent, _ = agent["agent_id"].(string)
				handle, _ = agent["attachment_handle"].(string)
			}
		}
	}
	if handle == "" {
		t.Fatalf("no handle for the Claude chat: %v", listing)
	}
	if out, _, code, msg := env.api.bullpenAdminAct(context.Background(), "root", "close_attachment", map[string]any{"handle": handle, "reason": "test disconnect"}); code != "" || out["state"] != "closed" {
		t.Fatalf("close: %v %s %s", out, code, msg)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		agentLiveEvents.Lock()
		_, streaming := agentLiveEvents.online[closedAgent]
		agentLiveEvents.Unlock()
		if !streaming || time.Now().After(deadline) {
			break
		}
	}
	time.Sleep(500 * time.Millisecond) // the bridge reads the closed frame and records it
	cancel()
	<-done
	// The update restarted the bridge and the old provider processes are gone.
	cancel, done = start(false)
	defer func() { cancel(); <-done }()
	detached.Action = "status"
	if !call(detached).Stopped {
		t.Fatal("helper restart lost explicit detach")
	}
	for _, chat := range chats[:2] {
		status := chat
		status.Action = "status"
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if out := call(status); out.Offline {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s should be offline after the restart", chat.Provider)
			}
		}
	}

	// Each chat's next hook event proves its (new) process and resumes it.
	for _, chat := range chats[:2] {
		resume := chat
		resume.Action, resume.HostKey, resume.AgentName = "resume", chat.HostKey+"-new", ""
		if chat.Provider == "claude" {
			if _, e := agentworksclient.CallLocalBridge(context.Background(), root, resume); e == nil {
				t.Fatal("a chat whose lease an administrator closed came back on its own")
			}
			continue
		}
		out := call(resume)
		if !out.Attached || out.Attachment.AgentName != chat.AgentName {
			t.Fatalf("%s did not come back as itself: %+v", chat.Provider, out)
		}
		status := resume
		status.Action = "status"
		if st := call(status); st.Offline || !st.Attached {
			t.Fatalf("%s is still offline after resuming: %+v", chat.Provider, st)
		}
	}
}
