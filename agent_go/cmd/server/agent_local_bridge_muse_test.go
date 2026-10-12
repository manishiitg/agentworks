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

// A Muse chat that was restarted (new lease) still receives the messages that
// were waiting for it: they are not dropped when the server has not yet offered
// them to the new lease, and the first receive returns the setup text together
// with the waiting messages instead of looking empty.
func TestAgentLocalBridgeMuseOffersSurviveARestart(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]}]}`)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "muse-offers-fixture-secret")
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
	sender, err := agentworksclient.New(srv.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	loginPath := filepath.Join(t.TempDir(), "alice.json")
	if err = agentworksclient.SaveConfig(loginPath, agentworksclient.Config{Server: srv.URL, Token: token}); err != nil {
		t.Fatal(err)
	}
	if _, err = sender.Call(context.Background(), "messages", map[string]any{"action": "register", "agent_name": "sender"}); err != nil {
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
	chat := agentworksclient.LocalBridgeRequest{Provider: "muse", SessionID: "01a12a4a-30ce-7581-9270-6b6cd51b970e", HostKey: "muse-process", AgentName: "muse-chat"}
	cancel, done := start(true)
	attach := chat
	attach.Action, attach.ConfigPath, attach.Setup = "attach", loginPath, true
	if out := call(attach); !out.Attached {
		t.Fatalf("muse chat did not attach: %+v", out)
	}
	for _, text := range []string{"first waiting message", "second waiting message"} {
		if _, err = sender.Call(context.Background(), "messages", map[string]any{"action": "send", "agent_name": "sender", "target": "agent:muse-chat", "message": text, "submission_id": text}); err != nil {
			t.Fatal(err)
		}
	}
	waiting := func() int {
		for _, state := range call(agentworksclient.LocalBridgeRequest{Action: "list"}).AgentStates {
			if state.AgentName == "muse-chat" {
				return state.Pending
			}
		}
		return 0
	}
	for deadline := time.Now().Add(5 * time.Second); waiting() < 2; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bridge never received the waiting messages")
		}
	}
	// Ctrl+C and resume: the bridge restarts and the chat comes back on a new lease.
	cancel()
	<-done
	cancel, done = start(false)
	defer func() { cancel(); <-done }()
	resume := chat
	resume.Action, resume.HostKey, resume.AgentName = "resume", "muse-process-new", ""
	if out := call(resume); !out.Attached {
		t.Fatalf("muse chat did not come back: %+v", out)
	}
	got := map[string]bool{}
	sawSetup := false
	for deadline := time.Now().Add(8 * time.Second); len(got) < 2 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		drain := chat
		drain.Action, drain.Transport, drain.HostKey = "drain", "muse_hook", "muse-process-new"
		out := call(drain)
		sawSetup = sawSetup || out.SetupContext != ""
		for _, m := range out.Messages {
			got[m.Message] = true
		}
	}
	if !got["first waiting message"] || !got["second waiting message"] {
		t.Fatalf("waiting messages were lost across the restart: %v", got)
	}
	_ = sawSetup

	// A message the chat never acknowledged (interrupted, lost tool result) is
	// handed out again after the quiet period; an admitted one is not.
	t.Setenv("AGENTWORKS_MUSE_REDELIVER_AFTER", "1s")
	time.Sleep(1200 * time.Millisecond)
	again := map[string]string{}
	drain := chat
	drain.Action, drain.Transport, drain.HostKey = "drain", "muse_hook", "muse-process-new"
	for _, m := range call(drain).Messages {
		again[m.Message] = m.MessageID
	}
	if len(again) != 2 {
		t.Fatalf("unacknowledged messages were not offered again: %v", again)
	}
	// The helper restarts while both messages are received but unacknowledged: the
	// received state survives, so ack and reply are still accepted afterwards.
	cancel()
	<-done
	cancel, done = start(false)
	again2 := chat
	again2.Action, again2.HostKey = "resume", "muse-process-new"
	if out := call(again2); !out.Attached {
		t.Fatalf("chat did not come back after the second restart: %+v", out)
	}
	reply := chat
	reply.Action, reply.HostKey, reply.MessageID, reply.Message, reply.SubmissionID = "reply", "muse-process-new", again["second waiting message"], "answering after a restart", "after-restart"
	if out := call(reply); out.Error != "" {
		t.Fatalf("reply after a helper restart was refused: %s", out.Error)
	}
	ack := chat
	ack.Action, ack.Delivery, ack.HostKey, ack.MessageID = "ack", "admitted", "muse-process-new", again["first waiting message"]
	if out := call(ack); out.Error != "" {
		t.Fatalf("ack failed: %s", out.Error)
	}
	time.Sleep(1200 * time.Millisecond)
	left := call(drain).Messages
	if len(left) != 1 || left[0].Message != "second waiting message" {
		t.Fatalf("only the unacknowledged message may come back: %+v", left)
	}
}
