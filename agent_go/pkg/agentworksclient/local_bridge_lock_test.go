package agentworksclient

import (
	"context"
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
