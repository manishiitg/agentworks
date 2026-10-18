package agentworksclient

import (
	"context"
	"encoding/json"
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
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer slow.Close()
	client, err := New(slow.URL, "token")
	if err != nil {
		t.Skip("test server not accepted by the client:", err)
	}
	session := &localSession{Attachment: AgentAttachment{ID: "lease", AgentName: "a", Provider: "claude", SessionID: "s"}, HostKey: "h", AccountSenders: true, client: client, Offers: map[string]localOffer{}}
	b := &localBridge{sessions: map[string]*localSession{localBridgeKey("claude", "s"): session}, changed: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		b.handle(context.Background(), context.Background(), LocalBridgeRequest{Action: "peers", Provider: "claude", SessionID: "s", HostKey: "h"})
		close(done)
	}()
	time.Sleep(60 * time.Millisecond) // the peers call is now waiting on the server
	start := time.Now()
	b.handle(context.Background(), context.Background(), LocalBridgeRequest{Action: "status", Provider: "claude", SessionID: "s", HostKey: "h"})
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("status waited %v behind a peers call that was only waiting on the server", elapsed)
	}
	<-done
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
		b.sessions[localBridgeKey("muse", "s1")] = &localSession{Attachment: AgentAttachment{AgentName: "a", ID: "att", Provider: "muse", SessionID: "s1"}, HostKey: "h", client: client, Offers: map[string]localOffer{}}
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
func TestAckOfAMessageOnlySeenAsANoticeSaysToReceiveFirst(t *testing.T) {
	client, err := New("http://127.0.0.1:1", "token")
	if err != nil {
		t.Fatal(err)
	}
	b := &localBridge{sessions: map[string]*localSession{}, changed: make(chan struct{})}
	b.sessions[localBridgeKey("muse", "s1")] = &localSession{Attachment: AgentAttachment{AgentName: "a", ID: "att", Provider: "muse", SessionID: "s1"}, HostKey: "h", client: client,
		Offers: map[string]localOffer{"m1": {MuseHinted: true, AgentLiveMessage: AgentLiveMessage{MessageID: "m1"}}}}
	reply := b.handle(context.Background(), context.Background(), LocalBridgeRequest{Action: "ack", Provider: "muse", SessionID: "s1", HostKey: "h", MessageID: "m1", Delivery: "admitted"})
	if !strings.Contains(reply.Error, "action=receive") {
		t.Fatalf("ack of a notice-only message = %q", reply.Error)
	}
	if b.sessions[localBridgeKey("muse", "s1")].Offers["m1"].Offered {
		t.Fatal("the refused ack marked the message offered")
	}
}

// Muse sends each plugin tool to its model provider as "mcp__plugin_<plugin>_<server>__<tool>" and the
// provider rejects names over 64 characters ("name must be at most 64 characters, got 66"), which
// broke a Muse chat right after attach. Every Muse tool must fit with room to spare, so a long tool name
// or server id fails here and not in the owner's chat.
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
	b.sessions[localBridgeKey("claude", "s1")] = &localSession{Attachment: AgentAttachment{AgentName: "a", ID: "att", Provider: "claude", SessionID: "s1"}, HostKey: "h", AccountSenders: true, client: client, Offers: map[string]localOffer{}}
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
func TestAutoUpdateWaitsWhileAMessageIsBeingDelivered(t *testing.T) {
	var calls atomic.Int32
	b := &localBridge{sessions: map[string]*localSession{}, changed: make(chan struct{}), options: LocalBridgeOptions{
		AutoUpdate:      func(context.Context) { calls.Add(1) },
		AutoUpdateFirst: 20 * time.Millisecond, AutoUpdateEvery: time.Hour, AutoUpdateRetry: 30 * time.Millisecond,
	}}
	s := &localSession{Attachment: AgentAttachment{AgentName: "a", ID: "att", Provider: "claude", SessionID: "s1"}, Offers: map[string]localOffer{
		"m1": {Offered: true, OfferedAt: time.Now(), AgentLiveMessage: AgentLiveMessage{MessageID: "m1", Delivery: "admitted"}},
	}}
	b.sessions[localBridgeKey("claude", "s1")] = s
	if b.idleForUpdate() {
		t.Fatal("a message just handed over and not processed is a delivery in progress")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.autoUpdateLoop(ctx)
	time.Sleep(80 * time.Millisecond) // inside the loop's six retries (6 x 30 ms)
	if calls.Load() != 0 {
		t.Fatalf("the update ran during a delivery (%d calls)", calls.Load())
	}
	b.mu.Lock()
	m := s.Offers["m1"]
	m.Delivery = "processed"
	s.Offers["m1"] = m
	b.mu.Unlock()
	for i := 0; i < 40 && calls.Load() == 0; i++ {
		time.Sleep(25 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("the update did not run once the delivery was processed (%d calls)", calls.Load())
	}
}

// takeOffers asks the server with the bridge lock released. Two calls for one chat at the same time (the
// adapter and a hook) must not both pick the same waiting message: each comes out exactly once.
func TestConcurrentTakeOffersHandOverEachMessageOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			IDs []string `json:"message_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		time.Sleep(100 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"message_ids": in.IDs})
	}))
	defer srv.Close()
	client, err := New(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	b := &localBridge{sessions: map[string]*localSession{}, changed: make(chan struct{})}
	s := &localSession{Attachment: AgentAttachment{AgentName: "a", ID: "att", Provider: "claude", SessionID: "s1"}, client: client, Offers: map[string]localOffer{}}
	for i, id := range []string{"m1", "m2", "m3"} {
		s.Offers[id] = localOffer{AgentLiveMessage: AgentLiveMessage{MessageID: id, Sequence: i}}
	}
	b.sessions[localBridgeKey("claude", "s1")] = s
	var mu sync.Mutex
	seen := map[string]int{}
	var errs []string
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.mu.Lock()
			defer b.mu.Unlock()
			got, _, _, err := b.takeOffers(context.Background(), s, "", 20)
			mu.Lock()
			if err != nil {
				errs = append(errs, err.Error())
			}
			for _, m := range got {
				seen[m.MessageID]++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	for _, id := range []string{"m1", "m2", "m3"} {
		if seen[id] != 1 {
			t.Fatalf("message %s handed out %d times: %v (errors: %v)", id, seen[id], seen, errs)
		}
	}
}

// A Codex chat whose own saved connection file was deleted could not attach again: the quick own-attach
// path stopped at "saved connection is incomplete" and the normal path reused the missing file. It must
// make a new connection from the saved login and keep the chat's name.
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
		HostKey:    "h", AccountSenders: true, OwnCLI: true, Offline: true, ParentConfigPath: parent, ConfigPath: lost, Offers: map[string]localOffer{},
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
