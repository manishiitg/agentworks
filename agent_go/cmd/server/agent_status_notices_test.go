package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// A message to an agent nobody is reading is reported to its sender once, as a plain status line that the sender's chat
// receives like any message but that is not a message from the target and asks for nothing.
func TestAgentStatusNoticeTellsTheSenderOnceThatAMessageIsWaiting(t *testing.T) {
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
	defer func() { cancel(); <-done }()
	attach := chat
	attach.Action, attach.ConfigPath, attach.Setup = "attach", loginPath, true
	if out := call(attach); !out.Attached {
		t.Fatalf("the chat did not attach: %+v", out)
	}
	if _, err = sender.Call(context.Background(), "messages", map[string]any{"action": "register", "agent_name": "detached"}); err != nil {
		t.Fatal(err)
	}
	send := chat
	send.Action, send.Target, send.Message, send.SubmissionID = "send", "agent:detached", "are you there", "status-notice-1"
	if out := call(send); out.Error != "" {
		t.Fatalf("send failed: %s", out.Error)
	}
	old := agentStatusNoticeAfter
	agentStatusNoticeAfter = time.Millisecond
	t.Cleanup(func() { agentStatusNoticeAfter = old })
	runAgentStatusSweep(context.Background(), time.Now().UTC().Add(time.Minute))
	waiting := func() int {
		for _, state := range call(agentworksclient.LocalBridgeRequest{Action: "list"}).AgentStates {
			if state.AgentName == "muse-chat" {
				return state.Pending
			}
		}
		return 0
	}
	for deadline := time.Now().Add(5 * time.Second); waiting() < 1; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the sender was never told its message is waiting")
		}
	}
	drain := chat
	drain.Action, drain.Transport = "drain", "muse_hook"
	got := call(drain).Messages
	if len(got) != 1 || got[0].Kind != "status" || got[0].Sender != "AgentWorks" || !strings.Contains(got[0].Message, "detached has not read your message") || !strings.Contains(got[0].Message, "no chat is attached to this name") {
		t.Fatalf("the status line is wrong: %+v", got)
	}
	// Reported once: a second sweep adds nothing, and the line is not offered again.
	runAgentStatusSweep(context.Background(), time.Now().UTC().Add(2*time.Minute))
	time.Sleep(300 * time.Millisecond)
	if again := call(drain).Messages; len(again) != 0 {
		t.Fatalf("the notice was repeated or a notice was written about the notice: %+v", again)
	}
}

// A recipient that wrote back has read the earlier message, so its sender is not told it is unread; a name nobody is
// attached to is called that, not "connected".
func TestAgentStatusSweepSkipsRepliedMessagesAndNamesDeadNames(t *testing.T) {
	now := time.Now().UTC()
	ep := func(id string) agentMessageEndpoint {
		return agentMessageEndpoint{UserID: "alice", NamedAgentID: id, Label: id}
	}
	msg := func(id string, from int, at time.Time) agentMessage {
		return agentMessage{ID: id, From: from, SentAt: at, Delivery: "stored"}
	}
	s := agentMessageStore{
		Agents: []namedMessageAgent{{ID: "a", UserID: "alice", Name: "a"}, {ID: "b", UserID: "alice", Name: "b"}, {ID: "dead", UserID: "alice", Name: "dead"}},
		Conversations: []agentConversation{
			{ID: "c1", Endpoints: [2]agentMessageEndpoint{ep("a"), ep("b")}, Messages: []agentMessage{msg("msg-1", 0, now.Add(-10*time.Minute))}},
			{ID: "c2", Endpoints: [2]agentMessageEndpoint{ep("b"), ep("a")}, Messages: []agentMessage{msg("msg-2", 0, now.Add(-5*time.Minute))}},
			{ID: "c3", Endpoints: [2]agentMessageEndpoint{ep("a"), ep("dead")}, Messages: []agentMessage{msg("msg-3", 0, now.Add(-10*time.Minute))}},
		},
	}
	if !sweepAgentStatusNotices(&s, now) {
		t.Fatal("expected the store to change")
	}
	if got := s.Conversations[0].Messages[0]; got.Delivery != "stored" || !got.AdmittedAt.IsZero() || !got.NoticeSent {
		t.Fatalf("a replied-to message gets no notice but must stay undelivered-state stored, got %+v", got)
	}
	var text string
	for _, c := range s.Conversations {
		if c.ID == agentStatusConversationPrefix+"a" {
			text = c.Messages[0].Text
		}
	}
	if !strings.Contains(text, "no chat is attached to this name") || strings.Contains(text, "your message 1") || strings.Contains(text, "b has not read") {
		t.Fatalf("unexpected notice: %q", text)
	}
	if sweepAgentStatusNotices(&s, now.Add(time.Minute)) {
		t.Fatal("a message must be reported once only")
	}
	// A mailbox-only name (no attached chat, but it sends and reads) is not called retired.
	s2 := agentMessageStore{
		Agents: []namedMessageAgent{{ID: "a", UserID: "alice", Name: "a"}, {ID: "box", UserID: "alice", Name: "box"}},
		Conversations: []agentConversation{
			{ID: "c1", Endpoints: [2]agentMessageEndpoint{ep("a"), ep("box")}, Messages: []agentMessage{msg("msg-1", 0, now.Add(-10*time.Minute))}},
			{ID: "c2", Endpoints: [2]agentMessageEndpoint{ep("box"), ep("a")}, Messages: []agentMessage{{ID: "msg-0", From: 0, SentAt: now.Add(-2 * time.Hour), Delivery: "processed"}}},
		},
	}
	sweepAgentStatusNotices(&s2, now)
	for _, c := range s2.Conversations {
		if c.ID == agentStatusConversationPrefix+"a" {
			text = c.Messages[0].Text
		}
	}
	if !strings.Contains(text, "reads its mailbox") || strings.Contains(text, "retired") {
		t.Fatalf("a mailbox-only name was called retired: %q", text)
	}
}
