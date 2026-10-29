package agentworksclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The bridge answers every hook, so one slow step must not stall the rest (an owner's Mac
// timed out /agentworks attach twice: twenty chats, a process listing for each under the
// lock). Listing probes chat processes before it takes the lock, and a request that cannot
// get the lock in time answers "still connecting" instead of waiting past its caller.
func TestBridgeListProbesOutsideTheLockAndBusyAnswersQuickly(t *testing.T) {
	var probes atomic.Int32
	b := &localBridge{sessions: map[string]*localSession{}, changed: make(chan struct{}), options: LocalBridgeOptions{HostAlive: func(provider, host string) bool {
		probes.Add(1)
		time.Sleep(50 * time.Millisecond)
		return true
	}}}
	for _, id := range []string{"a", "b", "c"} {
		b.sessions[localBridgeKey("claude", id)] = &localSession{Attachment: AgentAttachment{AgentName: id, Provider: "claude", SessionID: id}, HostKey: "h" + id}
	}
	// While a slow process check runs, another request (status of an unknown chat) is not held up.
	done := make(chan LocalBridgeReply, 1)
	go func() {
		done <- b.handle(context.Background(), context.Background(), LocalBridgeRequest{Action: "list"})
	}()
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	b.handle(context.Background(), context.Background(), LocalBridgeRequest{Action: "status", Provider: "claude", SessionID: "unknown"})
	if elapsed := time.Since(start); elapsed > 40*time.Millisecond {
		t.Fatalf("a request waited %v for a list that was only probing processes", elapsed)
	}
	if reply := <-done; len(reply.AgentStates) != 3 || probes.Load() != 3 {
		t.Fatalf("list = %d chats after %d probes", len(reply.AgentStates), probes.Load())
	}

	b.mu.Lock() // a step that is holding the bridge for a long server call
	defer b.mu.Unlock()
	if b.lockWithin(30 * time.Millisecond) {
		t.Fatal("the lock was taken while held")
	}
	b.options.HostAlive = nil
	if got := b.probeHosts(); got != nil {
		t.Fatalf("probeHosts without a probe = %v", got)
	}
}

// Looking for a chat's native route lists session files and checks processes, and every hook of
// every attached chat reaches it with the bridge lock held: a chat with no route is looked up at
// most once every 30 s, not on every hook.
func TestNativeRouteDiscoveryIsThrottledPerChat(t *testing.T) {
	var calls atomic.Int32
	b := &localBridge{options: LocalBridgeOptions{
		HostAlive:   func(string, string) bool { return true },
		NativeOffer: func(context.Context, string, LocalBridgeReply) error { return nil },
		NativeRoute: func(string, string, string, string) string { calls.Add(1); return "" },
	}}
	s := &localSession{Attachment: AgentAttachment{Provider: "claude", SessionID: "s"}, HostKey: "h", AccountSenders: true, client: &Client{}}
	for i := 0; i < 5; i++ {
		b.recoverNativeRoute(s)
	}
	if calls.Load() != 1 {
		t.Fatalf("route discovery ran %d times for one chat in a burst of hooks", calls.Load())
	}
}

// An attach runs its server calls without the bridge lock, so a retry of the same attach
// must wait for it (and then find the chat attached) rather than start a second one.
func TestAttachGateRunsOneAttachPerChat(t *testing.T) {
	b := &localBridge{}
	release, ok := b.attachGate("claude|s", time.Second)
	if !ok {
		t.Fatal("the first attach must start")
	}
	if _, ok = b.attachGate("claude|s", 30*time.Millisecond); ok {
		t.Fatal("a second attach for the same chat started while the first ran")
	}
	if release2, ok2 := b.attachGate("claude|other", 30*time.Millisecond); !ok2 {
		t.Fatal("another chat must not wait")
	} else {
		release2()
	}
	go func() { time.Sleep(20 * time.Millisecond); release() }()
	if release3, ok3 := b.attachGate("claude|s", time.Second); !ok3 {
		t.Fatal("the retry must run once the first attach is done")
	} else {
		release3()
	}
}

// A busy server can take many seconds per call, so peers, send, read, ack and reply talk to it
// without the bridge lock: while one of them waits, other commands still answer at once.
func TestSlowServerCallDoesNotHoldTheBridgeLock(t *testing.T) {
	for _, action := range []string{"peers", "directories", "agent_online"} {
		t.Run(action, func(t *testing.T) {
			started := make(chan struct{}, 1)
			slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case started <- struct{}{}:
				default:
				}
				time.Sleep(300 * time.Millisecond)
				_, _ = w.Write([]byte(`{"agents":[]}`))
			}))
			defer slow.Close()
			client, err := New(slow.URL, "token")
			if err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(t.TempDir(), "connection.json")
			if err := SaveConfig(config, Config{Server: slow.URL, Token: "token"}); err != nil {
				t.Fatal(err)
			}
			session := &localSession{Attachment: AgentAttachment{ID: "lease", AgentName: "a", Provider: "claude", SessionID: "s"}, HostKey: "h", AccountSenders: true, client: client, ParentConfigPath: config}
			b := &localBridge{sessions: map[string]*localSession{localBridgeKey("claude", "s"): session}, changed: make(chan struct{})}
			done := make(chan LocalBridgeReply, 1)
			go func() {
				done <- b.handle(context.Background(), context.Background(), LocalBridgeRequest{Action: action, Provider: "claude", SessionID: "s", HostKey: "h", ConfigPath: config})
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("server call did not start")
			}
			start := time.Now()
			b.handle(context.Background(), context.Background(), LocalBridgeRequest{Action: "status", Provider: "claude", SessionID: "s", HostKey: "h"})
			if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
				t.Errorf("status waited %v behind %s waiting on the server", elapsed, action)
			}
			if reply := <-done; reply.Error != "" {
				t.Fatal(reply.Error)
			}
		})
	}
}

// The whole attach, against a fake server whose attach step is slow: other commands keep
// answering while it runs, and the chat is attached when it ends.
func TestAttachDoesNotBlockOtherCommandsWhileTheServerIsSlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Action string `json:"action"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/agents/events"):
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case body.Action == "connection":
			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":3600}`))
		case body.Action == "attach":
			time.Sleep(300 * time.Millisecond)
			_, _ = w.Write([]byte(`{"attachment_id":"L1","agent_id":"ag","agent_name":"test-agent","provider":"claude","native_session_id":"sess"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	root, err := os.MkdirTemp("", "awb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	parent := filepath.Join(root, "parent.json")
	if err = SaveConfig(parent, Config{Server: srv.URL, Token: "t"}); err != nil {
		t.Skip("loopback server not accepted:", err)
	}
	b := &localBridge{root: root, sessions: map[string]*localSession{}, changed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); b.workers.Wait() }()
	attached := make(chan LocalBridgeReply, 1)
	go func() {
		attached <- b.handle(ctx, ctx, LocalBridgeRequest{Action: "attach", Provider: "claude", SessionID: "sess", HostKey: "h", AgentName: "test-agent", ConfigPath: parent, Setup: true})
	}()
	time.Sleep(100 * time.Millisecond) // the attach is now waiting on the server
	start := time.Now()
	b.handle(ctx, ctx, LocalBridgeRequest{Action: "status", Provider: "claude", SessionID: "other", HostKey: "x"})
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("another command waited %v behind an attach that was only waiting on the server", elapsed)
	}
	reply := <-attached
	if !reply.Attached || reply.Error != "" || reply.Attachment.ID != "L1" {
		t.Fatalf("attach = %+v", reply)
	}
	if b.sessions[localBridgeKey("claude", "sess")] == nil {
		t.Fatal("the attached chat was not recorded")
	}
}

// peers and read only read, so the lease check runs beside the read (one server round trip
// less); a failed check still drops the answer, and the helper log rolls over instead of growing.
func TestMailboxReadChecksLeaseBesideTheReadAndLogRolls(t *testing.T) {
	var denied atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		if strings.HasSuffix(r.URL.Path, "/agents/attachment") && denied.Load() {
			http.Error(w, `{"error":"gone"}`, http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"agents":[]}`))
	}))
	defer server.Close()
	client, err := New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	b := &localBridge{}
	s := &localSession{Attachment: AgentAttachment{AgentName: "a", ID: "att"}, client: client}
	start := time.Now()
	if reply := b.mailboxCall(context.Background(), s, LocalBridgeRequest{Action: "peers"}, LocalBridgeReply{}); reply.Error != "" || time.Since(start) > 280*time.Millisecond {
		t.Fatalf("peers = %q after %v, want one round trip", reply.Error, time.Since(start))
	}
	denied.Store(true)
	if reply := b.mailboxCall(context.Background(), s, LocalBridgeRequest{Action: "peers"}, LocalBridgeReply{}); reply.Error == "" || reply.Result != nil {
		t.Fatalf("peers after the lease was revoked = %+v", reply)
	}

	path := filepath.Join(t.TempDir(), "bridge.log")
	l, err := openRotatingLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	chunk := []byte(strings.Repeat("x", 100<<10))
	for i := 0; i < 8; i++ {
		if _, err = l.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if info, e := os.Stat(path); e != nil || info.Size() > bridgeLogMaxBytes {
		t.Fatalf("log = %v, %v", info, e)
	}
	if _, e := os.Stat(path + ".1"); e != nil {
		t.Fatalf("no rolled log: %v", e)
	}
}

// A restart must not look like revoked access, let a send through a failed check,
// or require replacing the same attachment once the server recovers.
func TestMailboxValidationOutageDoesNotRevokeAttachment(t *testing.T) {
	var status, sends atomic.Int32
	status.Store(http.StatusBadGateway)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/agents/attachment") {
			var request struct {
				Action       string `json:"action"`
				AgentName    string `json:"agent_name"`
				AttachmentID string `json:"attachment_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Action != "validate" || request.AgentName != "test-agent" || request.AttachmentID != "test-attachment" {
				t.Error("validation changed the chat's attachment identity")
			}
			if code := int(status.Load()); code != http.StatusOK {
				http.Error(w, `{"error":"unavailable"}`, code)
				return
			}
			_, _ = w.Write([]byte(`{"message_ids":[]}`))
			return
		}
		var request struct {
			Arguments struct {
				Action string `json:"action"`
			} `json:"arguments"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Arguments.Action == "send" {
			sends.Add(1)
		}
		_, _ = w.Write([]byte(`{"agents":[]}`))
	}))
	defer server.Close()
	client, err := New(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	bridge := &localBridge{}
	session := &localSession{Attachment: AgentAttachment{AgentName: "test-agent", ID: "test-attachment"}, client: client}
	check := func(want string) {
		t.Helper()
		for _, action := range []string{"peers", "send"} {
			reply := bridge.mailboxCall(context.Background(), session, LocalBridgeRequest{Action: action, Target: "agent:recipient", Message: "test", SubmissionID: "test-submission"}, LocalBridgeReply{})
			if want == "" {
				if reply.Error != "" || reply.Result == nil {
					t.Fatalf("%s after recovery: error=%q result=%s", action, reply.Error, reply.Result)
				}
			} else if !strings.Contains(reply.Error, want) || reply.Result != nil {
				t.Fatalf("%s: error=%q result=%s, want %q and no result", action, reply.Error, reply.Result, want)
			}
		}
	}
	check("temporarily unavailable")
	for _, denied := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		status.Store(int32(denied))
		check("no longer authorized")
	}
	if sends.Load() != 0 {
		t.Fatal("a failed validation allowed a send")
	}
	status.Store(http.StatusOK)
	check("")
	if sends.Load() != 1 {
		t.Fatal("the same attachment could not send after recovery")
	}
	server.Close()
	check("temporarily unavailable")
	if sends.Load() != 1 || session.Attachment.ID != "test-attachment" {
		t.Fatal("transport failure sent a message or changed attachment identity")
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
	hdr http.Header
}

func (l *lockedBuffer) Header() http.Header { return l.hdr }
func (l *lockedBuffer) WriteHeader(int)     {}
func (l *lockedBuffer) Flush()              {}
func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }

// A Muse chat's Monitor watch ended whenever the server was slow for its lease check, and the chat
// then heard nothing until a prompt re-armed it. A busy server only delays the watch; a refusal ends it.
func TestMuseWatchWaitsOutABusyServerButEndsWhenTheLeaseIsRefused(t *testing.T) {
	museWatchRetryDelay = 10 * time.Millisecond
	defer func() { museWatchRetryDelay = 5 * time.Second }()
	var calls atomic.Int32
	var refuse atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case refuse.Load():
			http.Error(w, `{"error":"gone"}`, http.StatusForbidden)
		case calls.Add(1) < 3:
			http.Error(w, `{"error":"busy"}`, http.StatusServiceUnavailable)
		default:
			_, _ = w.Write([]byte(`{"message_ids":[]}`))
		}
	}))
	defer server.Close()
	client, err := New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	run := func() (*lockedBuffer, chan struct{}, context.CancelFunc) {
		b := &localBridge{sessions: map[string]*localSession{}, changed: make(chan struct{}), museCurrent: map[string]string{}}
		b.sessions[localBridgeKey("muse", "s1")] = &localSession{Attachment: AgentAttachment{AgentName: "a", ID: "att", Provider: "muse", SessionID: "s1"}, HostKey: "h", client: client}
		ctx, cancel := context.WithCancel(context.Background())
		out := &lockedBuffer{hdr: http.Header{}}
		done := make(chan struct{})
		go func() {
			defer close(done)
			req := httptest.NewRequest(http.MethodPost, "/bridge", nil).WithContext(ctx)
			b.serveMuseWatch(ctx, out, req, LocalBridgeRequest{Provider: "muse", SessionID: "s1", AttachmentID: "att", HostKey: "h"})
		}()
		return out, done, cancel
	}
	out, done, cancel := run()
	for i := 0; i < 200 && !strings.Contains(out.String(), `"ready"`); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(out.String(), `"ready"`) || calls.Load() < 3 {
		t.Fatalf("watch after a busy server = %q (%d checks)", out.String(), calls.Load())
	}
	cancel()
	<-done

	refuse.Store(true)
	out, done, cancel = run()
	defer cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watch kept running after the lease was refused")
	}
	if !strings.Contains(out.String(), "no longer authorized") {
		t.Fatalf("refused watch = %q", out.String())
	}
}

// A long poll or a slow server call runs with the bridge lock released; the "held the bridge lock"
// log must not count that time (it logged 25 s "holds" for every idle listener, thousands of times).
func TestHoldClockLeavesOutTimeSpentWithTheLockReleased(t *testing.T) {
	b := &localBridge{}
	b.mu.Lock()
	clock := &holdClock{since: time.Now()}
	b.hold = clock
	b.unlocked(func() {
		if !b.mu.TryLock() { // another request can take the lock meanwhile
			t.Error("the lock was not released")
			return
		}
		b.hold = &holdClock{since: time.Now()}
		b.hold = nil // another request ends its turn
		b.mu.Unlock()
		time.Sleep(60 * time.Millisecond)
	})
	defer b.mu.Unlock()
	if clock.away < 60*time.Millisecond || b.hold != clock {
		t.Fatalf("away = %v, hold restored = %v", clock.away, b.hold == clock)
	}
}

// A notice (or a read) never hands a message to a chat; acking it must stay refused, and the refusal
// must tell the chat the one thing that works: take it with receive first.
func TestMusePluginToolNamesFitTheProviderNameLimit(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "plugins", "muse", ".muse-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Name         string `json:"name"`
		Capabilities struct {
			MCPServers []struct {
				ID string `json:"id"`
			} `json:"mcpServers"`
		} `json:"capabilities"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil || manifest.Name == "" || len(manifest.Capabilities.MCPServers) != 1 {
		t.Fatalf("Muse manifest: %+v %v", manifest, err)
	}
	server, err := NewMusePluginMCPServer(context.Background(), t.TempDir(), "host", func() (string, error) { return "s1", nil })
	if err != nil {
		t.Fatal(err)
	}
	const limit, margin = 64, 4 // Muse's exact separator is not documented: leave a few characters
	prefix := "mcp__plugin_" + manifest.Name + "_" + manifest.Capabilities.MCPServers[0].ID + "__"
	for name := range server.ListTools() {
		if n := len(prefix + name); n > limit-margin {
			t.Errorf("%s is %d characters; keep it at %d or fewer", prefix+name, n, limit-margin)
		}
	}
}

// agentworks update starts the new helper right after the old one stopped answering, while the old one is
// still exiting and holds the single-listener lock. The new helper must wait for that lock, not quit
// (which left delivery down: "CLI ready, but delivery did not reconnect").
func TestNewHelperWaitsForTheStoppingHelpersLock(t *testing.T) {
	dir, err := os.MkdirTemp("", "aw")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	root := filepath.Join(dir, "bridge")
	if err = privateLiveDir(root); err != nil {
		t.Fatal(err)
	}
	oldHelper, err := liveFileLock(filepath.Join(root, "bridge.lock")) // the old helper, still exiting
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeLocalBridge(ctx, root, LocalBridgeOptions{Version: "new"}) }()
	time.Sleep(700 * time.Millisecond)
	select {
	case err = <-done:
		t.Fatalf("the new helper quit while the old one held the lock: %v", err)
	default:
	}
	oldHelper()
	var reply LocalBridgeReply
	for i := 0; i < 100; i++ {
		callCtx, stop := context.WithTimeout(ctx, time.Second)
		reply, err = CallLocalBridge(callCtx, root, LocalBridgeRequest{Action: "ping"})
		stop()
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	<-done
	if err != nil || reply.Version != "new" {
		t.Fatalf("new helper did not take over: %+v %v", reply, err)
	}
}

// A chat whose native-route lookup failed is looked at again only after a wait (lookups scan processes and
// ran on every hook), but a chat starting can bring its route, so that looks at once: otherwise a chat
// started just after a failed lookup seems attached but receives nothing for the whole wait.
func TestRouteLookupWaitsBetweenFailuresButNotAfterAChatStarts(t *testing.T) {
	var lookups atomic.Int32
	client, err := New("http://127.0.0.1:1", "token")
	if err != nil {
		t.Fatal(err)
	}
	b := &localBridge{sessions: map[string]*localSession{}, changed: make(chan struct{}), options: LocalBridgeOptions{
		HostAlive:   func(string, string) bool { return true },
		NativeOffer: func(context.Context, string, LocalBridgeReply) error { return nil },
		NativeRoute: func(_, _, _, _ string) string { lookups.Add(1); return "" },
	}}
	b.sessions[localBridgeKey("claude", "s1")] = &localSession{Attachment: AgentAttachment{AgentName: "a", ID: "att", Provider: "claude", SessionID: "s1"}, HostKey: "h", AccountSenders: true, client: client}
	status := LocalBridgeRequest{Action: "status", Provider: "claude", SessionID: "s1", HostKey: "h"}
	call := func(in LocalBridgeRequest) { b.handle(context.Background(), context.Background(), in) }
	call(status)
	call(status)
	if lookups.Load() != 1 {
		t.Fatalf("a second status right after a failed lookup looked again: %d lookups", lookups.Load())
	}
	status.RefreshRoute = true // the chat's start hook
	call(status)
	if lookups.Load() != 2 {
		t.Fatalf("a chat starting did not trigger a lookup: %d lookups", lookups.Load())
	}
}

// A hook gives up on /agentworks attach after a few seconds while the helper finishes it. The next prompt must
// then say the chat is attached (once), so the person is not left guessing after a "still connecting" message.
func TestAttachFinishedAfterTheCommandGaveUpIsConfirmedOnTheNextPrompt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Action string `json:"action"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/agents/events"):
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case body.Action == "connection":
			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":3600}`))
		case body.Action == "attach":
			time.Sleep(700 * time.Millisecond)
			_, _ = w.Write([]byte(`{"attachment_id":"L1","agent_id":"ag","agent_name":"test-agent","provider":"claude","native_session_id":"sess"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	root, err := os.MkdirTemp("", "awb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	bridgeRoot := filepath.Join(root, "b")
	parent := filepath.Join(root, "parent.json")
	if err = SaveConfig(parent, Config{Server: srv.URL, Token: "t"}); err != nil {
		t.Skip("loopback server not accepted:", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeLocalBridge(ctx, bridgeRoot) }()
	defer func() { cancel(); <-done }()
	for i := 0; i < 100; i++ {
		if _, e := os.Lstat(filepath.Join(bridgeRoot, "bridge.sock")); e == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	short, stop := context.WithTimeout(ctx, 150*time.Millisecond) // the hook's patience
	_, err = CallLocalBridge(short, bridgeRoot, LocalBridgeRequest{Action: "attach", Provider: "claude", SessionID: "sess", HostKey: "h", AgentName: "test-agent", ConfigPath: parent})
	stop()
	if err == nil {
		t.Fatal("the attach finished inside the hook's patience; the test needs a slower server")
	}
	prompt := LocalBridgeRequest{Action: "drain", Transport: "claude_hook", Provider: "claude", SessionID: "sess", HostKey: "h", WantNotice: true}
	var notice string
	for i := 0; i < 60 && notice == ""; i++ {
		time.Sleep(100 * time.Millisecond)
		reply, e := CallLocalBridge(ctx, bridgeRoot, prompt)
		if e == nil {
			notice = reply.Notice
		}
	}
	if !strings.Contains(notice, "agent:test-agent") {
		t.Fatalf("no confirmation after the late attach: %q", notice)
	}
	if again, e := CallLocalBridge(ctx, bridgeRoot, prompt); e != nil || again.Notice != "" {
		t.Fatalf("the confirmation was repeated: %+v %v", again, e)
	}
}

// The helper checks for an update on a schedule but never while a chat is being delivered to: it waits and tries again.
func TestCodexOwnAttachRebuildsAMissingChatConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Action string `json:"action"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/agents/events"):
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case body.Action == "connection":
			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":3600}`))
		case body.Action == "attach":
			_, _ = w.Write([]byte(`{"attachment_id":"L2","agent_id":"ag","agent_name":"codex-chat","provider":"codex","native_session_id":"sess"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	root, err := os.MkdirTemp("", "awb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	parent := filepath.Join(root, "parent.json")
	if err = SaveConfig(parent, Config{Server: srv.URL, Token: "t"}); err != nil {
		t.Skip("loopback server not accepted:", err)
	}
	key := localBridgeKey("codex", "sess")
	lost := filepath.Join(root, "connections", "gone.json")
	b := &localBridge{root: root, changed: make(chan struct{}), sessions: map[string]*localSession{key: {
		Attachment: AgentAttachment{ID: "L1", AgentName: "codex-chat", Provider: "codex", SessionID: "sess"},
		HostKey:    "h", AccountSenders: true, OwnCLI: true, Offline: true, ParentConfigPath: parent, ConfigPath: lost,
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); b.workers.Wait() }()
	// The status the attach command asks first must answer from local state, and nothing else may revive the chat.
	if st := b.handle(ctx, ctx, LocalBridgeRequest{Action: "status", Provider: "codex", SessionID: "sess", HostKey: "h"}); st.Error != "" || !st.Offline || st.Attachment.AgentName != "codex-chat" {
		t.Fatalf("status of a chat with a missing connection file = %+v", st)
	}
	if peers := b.handle(ctx, ctx, LocalBridgeRequest{Action: "peers", Provider: "codex", SessionID: "sess", HostKey: "h"}); peers.Error == "" || b.sessions[key].client != nil {
		t.Fatalf("a message call must not revive a chat without its connection: %+v", peers)
	}
	reply := b.handle(ctx, ctx, LocalBridgeRequest{Action: "attach", Provider: "codex", SessionID: "sess", HostKey: "h", ConfigPath: parent, OwnAttach: true})
	if !reply.Attached || reply.Error != "" || reply.Attachment.AgentName != "codex-chat" {
		t.Fatalf("attach after the chat's connection file was lost = %+v", reply)
	}
	if got := b.sessions[key]; got == nil || got.ConfigPath == lost {
		t.Fatal("the chat still points at the lost connection file")
	} else if _, err = os.Stat(got.ConfigPath); err != nil {
		t.Fatalf("the new connection file was not written: %v", err)
	}
}

// Regression: a scheduled Codex turn used the messaging CLI after its provider
// restarted and stopped at "not attached", even though this chat's lease remained.
func TestCodexMessagingResumesItsExistingAttachment(t *testing.T) {
	if err := localIPCSupported(); err != nil {
		t.Skip(err)
	}
	var attachments atomic.Int32
	var backgroundAttachments, detaches atomic.Int32
	var providerRestarted, nameReplaced atomic.Bool
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/agents/events") {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		var body struct {
			Action   string `json:"action"`
			Name     string `json:"agent_name"`
			Session  string `json:"native_session_id"`
			Expected string `json:"attachment_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Action == "detach" {
			detaches.Add(1)
		}
		if body.Action == "attach" {
			if body.Session == "background" {
				attempt := backgroundAttachments.Add(1)
				if attempt == 1 && body.Expected != "lease-background" || attempt > 1 && body.Expected != "background-renewed" {
					t.Error("recovery did not retain the saved attachment")
				}
				if nameReplaced.Load() {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"error":{"code":"agent_attachment_closed","message":"name moved to another chat"}}`))
					return
				}
				_ = json.NewEncoder(w).Encode(AgentAttachment{ID: "background-renewed", AgentID: "agent-background", AgentName: body.Name, Provider: "codex", SessionID: body.Session})
				return
			}
			id := attachments.Add(1)
			_ = json.NewEncoder(w).Encode(AgentAttachment{ID: fmt.Sprintf("renewed-%d", id), AgentID: "agent-" + body.Session, AgentName: body.Name, Provider: "codex", SessionID: body.Session})
			return
		}
		_, _ = w.Write([]byte(`{"agents":[],"message_ids":[]}`))
	}))
	defer remote.Close()
	root, err := os.MkdirTemp("", "aw-resume")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	config := filepath.Join(root, "parent.json")
	if err = SaveConfig(config, Config{Server: remote.URL, Token: "fixture-token"}); err != nil {
		t.Fatal(err)
	}
	seed := func(session, id string, closed bool) *localSession {
		return &localSession{Attachment: AgentAttachment{ID: id, AgentID: "agent-" + session, AgentName: "saved-" + session, Provider: "codex", SessionID: session}, HostKey: "prior-process", AccountSenders: true, OwnCLI: true, Offline: true, ServerClosed: closed, ConfigPath: config, ParentConfigPath: config}
	}
	background := seed("background", "lease-background", false)
	background.NativeRemote = "desktop+unix:///fixture.sock"
	background.ReadPosition = 7
	closed := seed("closed", "lease-closed", true)
	closed.NativeRemote = background.NativeRemote
	if err = atomicLiveJSON(filepath.Join(root, "sessions.json"), map[string]*localSession{
		localBridgeKey("codex", "saved"):      seed("saved", "lease-saved", false),
		localBridgeKey("codex", "closed"):     closed,
		localBridgeKey("codex", "background"): background,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	recovered := make(chan error, 1)
	go func() {
		done <- ServeLocalBridge(ctx, root, LocalBridgeOptions{
			HostAlive: func(_ string, host string) bool {
				return host == "restarted-process" || host == "current-process" && !providerRestarted.Load()
			},
			HousekeepingEvery: 20 * time.Millisecond,
			NativeOffer:       func(context.Context, string, LocalBridgeReply) error { return nil },
			NativeRoute: func(provider, session, host, remote string) string {
				if provider == "codex" && session == "background" && (host == "current-process" || host == "restarted-process") {
					return remote
				}
				return ""
			},
			Reconnect: func(ctx context.Context, saved []AgentAttachment) {
				if len(saved) != 1 || saved[0].SessionID != "background" || saved[0].AgentName != "saved-background" {
					select {
					case recovered <- fmt.Errorf("recovery selected a closed or unrelated chat: %+v", saved):
					default:
					}
					return
				}
				host := "current-process"
				if providerRestarted.Load() {
					host = "restarted-process"
				}
				_, err := CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "resume", Provider: "codex", SessionID: "background", HostKey: host, NativeRemote: background.NativeRemote, AttachmentID: saved[0].ID})
				select {
				case recovered <- err:
				default:
				}
			},
		})
	}()
	defer func() { cancel(); <-done }()
	for i := 0; i < 100; i++ {
		if _, err = CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "ping"}); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	// No prompt or messaging call is made for this native chat: maintenance
	// must restore it by its exact saved identity, outside the bridge lock.
	select {
	case err := <-recovered:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("saved native chat did not reconnect without a prompt")
	}
	status, err := CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "status", Provider: "codex", SessionID: "background", HostKey: "current-process"})
	if err != nil || !status.Attached || status.Attachment.AgentName != "saved-background" || backgroundAttachments.Load() != 1 {
		t.Fatalf("background recovery changed or failed the saved identity: %+v %v", status, err)
	}
	var restored map[string]*localSession
	if err := readLiveJSON(filepath.Join(root, "sessions.json"), &restored); err != nil {
		t.Fatal(err)
	}
	if s := restored[localBridgeKey("codex", "background")]; s == nil || s.ReadPosition != 7 || s.ParentConfigPath != config {
		t.Fatalf("background recovery lost the saved login or read position: %+v", s)
	}
	// A real SessionEnd-style IPC request must leave the server lease intact
	// and let background recovery return this exact native chat without a prompt.
	if _, err = CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "offline", Provider: "codex", SessionID: "background", HostKey: "current-process"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-recovered:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SessionEnd permanently disconnected the saved native chat")
	}
	if detaches.Load() != 0 || backgroundAttachments.Load() != 2 {
		t.Fatal("SessionEnd detached the server lease or failed to recover it")
	}
	// Another chat claimed this address while the old provider was gone.
	// The server's existing expected-attachment refusal must end recovery.
	nameReplaced.Store(true)
	providerRestarted.Store(true)
	select {
	case err := <-recovered:
		if err == nil {
			t.Fatal("recovery ignored the replaced-attachment refusal")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("recovery did not observe the replaced attachment")
	}
	stopped, err := CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "connection", Provider: "codex", SessionID: "background"})
	if err != nil || !stopped.Stopped || !stopped.Offline || backgroundAttachments.Load() != 3 {
		t.Fatalf("replacement did not stop recovery: %+v %v", stopped, err)
	}
	if err := readLiveJSON(filepath.Join(root, "sessions.json"), &restored); err != nil || !restored[localBridgeKey("codex", "background")].ServerClosed {
		t.Fatal("replacement refusal was not saved")
	}
	if _, err := CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "resume", Provider: "codex", SessionID: "background", HostKey: "restarted-process", AttachmentID: "background-renewed"}); err == nil || backgroundAttachments.Load() != 3 {
		t.Fatal("server-closed recovery made another attachment request")
	}
	providerRestarted.Store(false)
	request := LocalBridgeRequest{Action: "peers", Provider: "codex", SessionID: "saved", HostKey: "current-process"}
	before, err := CallLocalBridge(ctx, root, request)
	if err != nil || !before.Offline || before.Attached {
		t.Fatalf("fixture did not reproduce offline messaging: %+v %v", before, err)
	}
	got, err := CallMessagingBridge(ctx, root, request, nil)
	if err != nil || !got.Attached || got.Attachment.AgentID != "agent-saved" || got.Attachment.AgentName != "saved-saved" {
		t.Fatalf("messaging did not resume the same saved agent: %+v %v", got, err)
	}
	if attachments.Load() != 1 {
		t.Fatal("messaging must renew the lease only once")
	}
	wrong := request
	wrong.Action, wrong.HostKey, wrong.InboxID = "reply_inbox", "another-process", "saved-inbox"
	if refused, e := CallMessagingBridge(ctx, root, wrong, nil); e != nil || refused.Attached || attachments.Load() != 1 {
		t.Fatalf("another process could act as the reconnected chat: %+v %v", refused, e)
	}
	// SessionEnd stops local delivery but keeps this chat's saved server lease.
	if _, err = CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "offline", Provider: "codex", SessionID: "saved", HostKey: "current-process"}); err != nil {
		t.Fatal(err)
	}
	got, err = CallMessagingBridge(ctx, root, request, nil)
	if err != nil || !got.Attached || got.Attachment.AgentID != "agent-saved" || got.Attachment.AgentName != "saved-saved" || attachments.Load() != 2 {
		t.Fatalf("a resumed chat did not reconnect after SessionEnd: %+v %v", got, err)
	}
	for _, session := range []string{"never-attached", "closed"} {
		request.SessionID = session
		got, err = CallMessagingBridge(ctx, root, request, nil)
		if got.Attached || session == "closed" && err == nil {
			t.Fatalf("messaging revived %s: %+v %v", session, got, err)
		}
	}
	if _, err = CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "detach", Provider: "codex", SessionID: "saved", HostKey: "current-process"}); err != nil {
		t.Fatal(err)
	}
	if detaches.Load() != 1 {
		t.Fatal("the person's detach did not close the server lease")
	}
	request.SessionID = "saved"
	if got, err = CallMessagingBridge(ctx, root, request, nil); err != nil || got.Attached {
		t.Fatalf("messaging revived an explicit detach: %+v %v", got, err)
	}
	if attachments.Load() != 2 {
		t.Fatal("an unavailable chat was attached")
	}
}

// A wake hint for message21 must not skip it after a bounded20-message page.
func TestPendingReadPositionKeepsTheNextBatch(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			After int `json:"after"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		messages := []AgentLiveMessage{}
		end := min(request.After+20, 21)
		for sequence := request.After + 1; sequence <= end; sequence++ {
			messages = append(messages, AgentLiveMessage{AttachmentID: "lease", MessageID: fmt.Sprint(sequence), InboxID: "inbox", Sequence: sequence})
		}
		json.NewEncoder(w).Encode(map[string]any{"messages": messages, "next_cursor": end, "pending": 21 - request.After})
	}))
	defer remote.Close()
	client, err := New(remote.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	s := &localSession{Attachment: AgentAttachment{ID: "lease"}, client: client, latestSequence: 21}
	b := &localBridge{root: t.TempDir(), sessions: map[string]*localSession{"chat": s}}
	b.mu.Lock()
	defer b.mu.Unlock()
	messages, _, more, err := b.readPending(context.Background(), s, "codex_desktop", 20, false)
	if err != nil || len(messages) != 20 || more != 1 || s.ReadPosition != 20 {
		t.Fatalf("first page skipped pending: count=%d more=%d position=%d error=%v", len(messages), more, s.ReadPosition, err)
	}
	messages, _, more, err = b.readPending(context.Background(), s, "codex_desktop", 20, false)
	if err != nil || len(messages) != 1 || messages[0].Sequence != 21 || more != 0 || s.ReadPosition != 21 {
		t.Fatalf("next page lost: %+v more=%d position=%d error=%v", messages, more, s.ReadPosition, err)
	}
}

// A native message starts a prompt before its remote delivered write finishes.
// The whole native batch stays out of that hook, without a second mailbox.
func TestNativeHandoffDoesNotRepeatDuringSlowReceipt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ackStarted, releaseAck := make(chan struct{}), make(chan struct{})
	var once sync.Once
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/external/v1/call" {
			once.Do(func() { close(ackStarted) })
			select {
			case <-releaseAck:
			case <-r.Context().Done():
			}
			json.NewEncoder(w).Encode(map[string]any{"delivery": "delivered"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"messages": []AgentLiveMessage{
			{AttachmentID: "lease", MessageID: "first", InboxID: "inbox", Message: "private native body"},
			{AttachmentID: "lease", MessageID: "second", InboxID: "inbox"},
		}, "next_cursor": 2, "pending": 2})
	}))
	defer func() { close(releaseAck); remote.Close() }()
	client, err := New(remote.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	s := &localSession{Attachment: AgentAttachment{ID: "lease", Provider: "claude", SessionID: "chat", AgentName: "reviewer"}, HostKey: "host", client: client, latestSequence: 2, NativeRemote: "claude+session://fixture"}
	b := &localBridge{root: t.TempDir(), sessions: map[string]*localSession{localBridgeKey("claude", "chat"): s}, changed: make(chan struct{})}
	b.options.NativeOffer = func(_ context.Context, _ string, reply LocalBridgeReply) error {
		if len(reply.Messages) != 2 {
			t.Errorf("native batch: %+v", reply)
		}
		// The app can run this hook before its socket write returns.
		hook := b.handle(ctx, ctx, LocalBridgeRequest{Action: "drain", Provider: "claude", SessionID: "chat", HostKey: "host", Transport: "claude_hook"})
		if hook.Error != "" || len(hook.Messages) != 0 {
			t.Errorf("prompt repeated the in-flight native write: %+v", hook)
		}
		return nil
	}
	done := make(chan struct{})
	go func() { defer close(done); b.nativeLoop(ctx, s, "host") }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("native worker did not stop")
		}
	}()
	select {
	case <-ackStarted:
	case <-time.After(time.Second):
		t.Fatal("native receipt did not start")
	}
	hook := b.handle(ctx, ctx, LocalBridgeRequest{Action: "drain", Provider: "claude", SessionID: "chat", HostKey: "host", Transport: "claude_hook"})
	if hook.Error != "" || len(hook.Messages) != 0 || hook.Pending != 0 {
		t.Fatalf("prompt repeated native handoff: %+v", hook)
	}
	raw, err := os.ReadFile(filepath.Join(b.root, "sessions.json"))
	if err != nil || strings.Contains(string(raw), "private native body") || strings.Contains(string(raw), "handedOff") || strings.Contains(string(raw), "\"first\"") {
		t.Fatalf("helper persisted native delivery data: %s %v", raw, err)
	}
	// A failed receipt must not hide mail forever; only just-handed-off ids skip.
	b.mu.Lock()
	s.handedOff["first"] = time.Now().Add(-time.Second)
	b.mu.Unlock()
	hook = b.handle(ctx, ctx, LocalBridgeRequest{Action: "drain", Provider: "claude", SessionID: "chat", HostKey: "host", Transport: "claude_hook"})
	if hook.Error != "" || len(hook.Messages) != 1 || hook.Messages[0].MessageID != "first" {
		t.Fatalf("expired handoff did not retry: %+v", hook)
	}
}

// The saved parent path can stay the same while the person signs into a different
// account. A deliberate move must derive a new child, not reuse the old account.
func TestAccountMoveDoesNotReuseThePreviousAccountChild(t *testing.T) {
	var children atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Action string `json:"action"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/agents/events"):
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case body.Action == "connection":
			if r.Header.Get("Authorization") != "Bearer target-parent" {
				t.Error("child was not derived from selected parent")
			}
			children.Add(1)
			_, _ = w.Write([]byte(`{"access_token":"target-child","refresh_token":"new-refresh","expires_in":3600}`))
		case body.Action == "attach":
			if r.Header.Get("Authorization") == "Bearer target-child" {
				_, _ = w.Write([]byte(`{"attachment_id":"new-lease","agent_id":"target-agent","agent_name":"coding-chat","provider":"claude","native_session_id":"session"}`))
			} else {
				_, _ = w.Write([]byte(`{"attachment_id":"old-account-lease","agent_id":"old-agent","agent_name":"coding-chat","provider":"claude","native_session_id":"session"}`))
			}
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	root, err := os.MkdirTemp("", "awb-move-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	parent, child := filepath.Join(root, "parent.json"), filepath.Join(root, "old-child.json")
	if err = SaveConfig(parent, Config{Server: srv.URL, Token: "target-parent"}); err != nil {
		t.Fatal(err)
	}
	if err = SaveConfig(child, Config{Server: srv.URL, Token: "old-child"}); err != nil {
		t.Fatal(err)
	}
	client, err := localConfiguredClient(child)
	if err != nil {
		t.Fatal(err)
	}
	key := localBridgeKey("claude", "session")
	b := &localBridge{root: root, changed: make(chan struct{}), sessions: map[string]*localSession{key: {
		Attachment: AgentAttachment{ID: "old-lease", AgentID: "old-agent", AgentName: "coding-chat", Provider: "claude", SessionID: "session"},
		HostKey:    "host", AccountSenders: true, ConfigPath: child, ParentConfigPath: parent, client: client,
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); b.workers.Wait() }()
	reply := b.handle(ctx, ctx, LocalBridgeRequest{Action: "attach", Provider: "claude", SessionID: "session", HostKey: "host", AgentName: "coding-chat", ConfigPath: parent, SwitchConnection: true})
	if !reply.Attached || reply.Error != "" || reply.Attachment.AgentID != "target-agent" || children.Load() != 1 {
		t.Fatalf("move used the wrong account: children=%d reply=%+v", children.Load(), reply)
	}
	if b.sessions[key].ConfigPath == child {
		t.Fatal("move retained the old account child")
	}
}

// A current inbox member can write a new reply even when the first history page
// contains only old submissions whose sending connections have been revoked.
func TestInboxReplyUsesCurrentServerMembership(t *testing.T) {
	var reads, sends, leases atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Action     string         `json:"action"`
			MessageIDs []string       `json:"message_ids"`
			Arguments  map[string]any `json:"arguments"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Action == "validate" {
			leases.Add(1)
			_, _ = w.Write([]byte(`{"message_ids":[],"inbox_ids":{"old-submission":"own-inbox"}}`))
			return
		}
		switch body.Arguments["action"] {
		case "read":
			reads.Add(1)
			_, _ = w.Write([]byte(`{"messages":[{"message_id":"old-submission"}],"next_cursor":20}`))
		case "send":
			sends.Add(1)
			if body.Arguments["inbox_id"] != "own-inbox" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"conversation unavailable or access denied"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"accepted":true}`))
		default:
			t.Errorf("unexpected request: %+v", body)
		}
	}))
	defer srv.Close()
	client, err := New(srv.URL, "current-child")
	if err != nil {
		t.Fatal(err)
	}
	s := &localSession{client: client, Attachment: AgentAttachment{ID: "current-lease", AgentName: "coding-agent"}}
	b := &localBridge{}
	for _, inbox := range []string{"own-inbox", "another-inbox"} {
		reply := b.mailboxCall(context.Background(), s, LocalBridgeRequest{Action: "reply_inbox", InboxID: inbox, Message: "reply", SubmissionID: "reply-" + inbox}, LocalBridgeReply{})
		if inbox == "own-inbox" && reply.Error != "" {
			t.Fatalf("current member could not reply: %s", reply.Error)
		}
		if inbox != "own-inbox" && reply.Error == "" {
			t.Fatal("server refusal was ignored")
		}
	}
	inbox, err := authorizedReplyInbox(context.Background(), client, s.Attachment, "old-submission")
	if err != nil || inbox != "own-inbox" {
		t.Fatalf("reply context was gated by old body admission: %q %v", inbox, err)
	}
	if reads.Load() != 0 || sends.Load() != 2 || leases.Load() != 3 {
		t.Fatalf("reply repeated old admission or skipped current authority: reads=%d sends=%d leases=%d", reads.Load(), sends.Load(), leases.Load())
	}
}

func TestHeldNoticeChangesWithoutRepeatingAtEveryBoundary(t *testing.T) {
	revision := "set-one"
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			After int `json:"after"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		held, current := 1, revision
		if request.After > 0 {
			held, current = 0, ""
		}
		json.NewEncoder(w).Encode(map[string]any{"messages": []any{}, "pending": held, "held": held, "held_revision": current, "held_reasons": map[string]int{"sender_authorization_failed": held}, "held_details": []HeldMessageDetail{{Sender: "sender", Reason: "Sharing has ended.", Next: "Ask the sender to send again after access is restored."}}})
	}))
	defer remote.Close()
	client, err := New(remote.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	s := &localSession{Attachment: AgentAttachment{ID: "lease"}, client: client, latestSequence: 1}
	b := &localBridge{root: t.TempDir(), sessions: map[string]*localSession{"chat": s}}
	b.mu.Lock()
	defer b.mu.Unlock()
	read := func(boundary bool) heldInboxNotice {
		t.Helper()
		_, held, _, err := b.readPending(context.Background(), s, "claude_hook", 20, boundary)
		if err != nil {
			t.Fatal(err)
		}
		return held
	}
	first := read(true)
	if !first.Notice || first.Count != 1 || first.Details[0].Sender != "sender" {
		t.Fatal("first held set lacked its notice", first)
	}
	if again := read(true); again.Notice || again.Count != 1 {
		t.Fatal("unchanged held set repeated or lost diagnostic count", again)
	}
	read(false)
	if read(true).Notice {
		t.Fatal("background cursor read reset the held notice")
	}
	revision = "different-message-same-count"
	if !read(true).Notice {
		t.Fatal("changed held set with the same count was hidden")
	}
	saved, err := os.ReadFile(filepath.Join(b.root, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), revision) || strings.Contains(string(saved), "Sharing has ended") {
		t.Fatal("helper persisted notice metadata")
	}
}

// The real laptop held hundreds of ended chats; a bounded first page must not
// starve a returning native chat later in that saved set.
func TestNativeRecoveryBatchesRotatePastOldChats(t *testing.T) {
	saved := make([]AgentAttachment, 65)
	for i := range saved {
		saved[i] = AgentAttachment{ID: fmt.Sprintf("lease-%02d", i), ActivityAt: time.Unix(int64(i), 0)}
	}
	first, cursor := nativeRecoveryBatch(saved, "")
	if len(first) != 32 || first[0].ID != "lease-64" || first[31].ID != "lease-33" {
		t.Fatal("recent saved chats were not checked first")
	}
	second, cursor := nativeRecoveryBatch(saved, cursor)
	if len(second) != 32 || second[0].ID != "lease-32" || second[31].ID != "lease-01" {
		t.Fatal("later saved chats were starved by the first page")
	}
	last, _ := nativeRecoveryBatch(saved, cursor)
	if last[0].ID != "lease-00" || last[1].ID != "lease-64" {
		t.Fatal("recovery did not wrap after checking the oldest chat")
	}
}

// PLAT-856: a printed hook message's receipt must outlive the hook process.
// The same helper must also retry a temporary outage before sending.
func TestMessagingSurvivesRestartAndHookCallerExit(t *testing.T) {
	var validates, sends, receipts atomic.Int32
	// PLAT-856-375: a real deployment outlasted the old 30s send window.
	// Exercise the complete helper budget, not only the inner HTTP retry.
	restartingUntil := time.Now().Add(35 * time.Second)
	accepted := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/attachment") {
			validates.Add(1)
			if time.Now().Before(restartingUntil) {
				w.WriteHeader(503)
				return
			}
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var body struct{ Arguments map[string]any }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Arguments["action"] == "ack" {
			receipts.Add(1)
			close(accepted)
			<-release
			_, _ = w.Write([]byte(`{"status":"acked"}`))
			return
		}
		if body.Arguments["submission_id"] != "stable" {
			t.Error("lost send identity")
		}
		if sends.Add(1) == 1 {
			w.WriteHeader(502)
			return
		}
		_, _ = w.Write([]byte(`{"accepted":true,"message_id":"original"}`))
	}))
	defer server.Close()
	client, _ := New(server.URL, "token")
	session := &localSession{Attachment: AgentAttachment{ID: "lease", AgentName: "a", Provider: "claude", SessionID: "s"}, HostKey: "h", client: client}
	b := &localBridge{sessions: map[string]*localSession{localBridgeKey("claude", "s"): session}, changed: make(chan struct{})}
	reply := b.handle(context.Background(), context.Background(), LocalBridgeRequest{Action: "send", Provider: "claude", SessionID: "s", HostKey: "h", AttachmentID: "lease", Target: "agent:peer", Message: "body", SubmissionID: "stable"})
	if reply.Error != "" || validates.Load() < 2 || sends.Load() != 2 {
		t.Fatalf("send after restart: %+v validates=%d sends=%d", reply, validates.Load(), sends.Load())
	}
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan LocalBridgeReply, 1)
	go func() {
		done <- b.handle(context.Background(), caller, LocalBridgeRequest{Action: "ack", Provider: "claude", SessionID: "s", HostKey: "h", AttachmentID: "lease", MessageID: "original", Delivery: "delivered", Transport: "claude_hook"})
	}()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("receipt did not reach server")
	}
	cancel()
	close(release)
	got := <-done
	if got.Error != "" || !strings.Contains(string(got.Result), "acked") || receipts.Load() != 1 {
		t.Fatalf("hook exit lost receipt: %+v", got)
	}
}
