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
