package agentworksclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadPageLimitCapsAtTwenty(t *testing.T) {
	if got := readPageLimit(0); got != 20 {
		t.Fatalf("readPageLimit(0) = %d, want 20", got)
	}
	if got := readPageLimit(15); got != 15 {
		t.Fatalf("readPageLimit(15) = %d, want 15", got)
	}
	if got := readPageLimit(50); got != 20 {
		t.Fatalf("readPageLimit(50) = %d, want 20", got)
	}
}

func TestShareAndSetupContextReleaseBridgeLockDuringHTTP(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		_, _ = w.Write([]byte(`{"ok":true,"agents":[]}`))
	}))
	defer srv.Close()

	client, err := New(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "parent.json")
	if err := SaveConfig(cfgPath, Config{Server: srv.URL, Token: "token"}); err != nil {
		t.Fatal(err)
	}

	s := &localSession{
		Attachment:       AgentAttachment{ID: "lease-1", AgentName: "agent-a", Provider: "claude", SessionID: "sess-1"},
		HostKey:          "host-1",
		ParentConfigPath: cfgPath,
		SetupPending:     true,
		client:           client,
	}
	b := &localBridge{
		root:     t.TempDir(),
		sessions: map[string]*localSession{localBridgeKey("claude", "sess-1"): s},
		changed:  make(chan struct{}),
	}

	t.Run("share_own", func(t *testing.T) {
		done := make(chan LocalBridgeReply, 1)
		go func() {
			done <- b.handle(context.Background(), context.Background(), LocalBridgeRequest{
				Action:    "share_own",
				Provider:  "claude",
				SessionID: "sess-1",
				HostKey:   "host-1",
			})
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("share_own did not start HTTP request")
		}
		start := time.Now()
		b.handle(context.Background(), context.Background(), LocalBridgeRequest{Action: "status", Provider: "claude", SessionID: "sess-1", HostKey: "host-1"})
		if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
			t.Errorf("status waited %v while share_own was doing HTTP", elapsed)
		}
		close(release)
		if reply := <-done; reply.Error != "" {
			t.Fatalf("share_own error: %s", reply.Error)
		}
	})

	t.Run("setupContext", func(t *testing.T) {
		release = make(chan struct{})
		s.SetupPending = true
		done := make(chan LocalBridgeReply, 1)
		go func() {
			done <- b.handle(context.Background(), context.Background(), LocalBridgeRequest{
				Action:    "drain",
				Provider:  "claude",
				SessionID: "sess-1",
				HostKey:   "host-1",
				Transport: "claude_hook",
			})
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("setupContext did not start HTTP request")
		}
		start := time.Now()
		b.handle(context.Background(), context.Background(), LocalBridgeRequest{Action: "status", Provider: "claude", SessionID: "sess-1", HostKey: "host-1"})
		if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
			t.Errorf("status waited %v while setupContext was doing HTTP", elapsed)
		}
		close(release)
		if reply := <-done; reply.Error != "" {
			t.Fatalf("drain setupContext error: %s", reply.Error)
		}
	})
}

func TestNativeLoopRewindsReadPositionOnRejectedOfferWithoutWakeAndNoRace(t *testing.T) {
	var inboxCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inboxCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]any{{
				"attachment_id": "lease-1",
				"message_id":    "msg-1",
				"inbox_id":      "inbox-1",
				"sequence":      1,
				"message":       "hello",
			}},
			"next_cursor": 1,
			"pending":     1,
		})
	}))
	defer srv.Close()

	client, err := New(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	offered := make(chan struct{}, 4)
	var b *localBridge
	s := &localSession{
		Attachment:     AgentAttachment{ID: "lease-1", AgentName: "agent-a", Provider: "codex", SessionID: "sess-1"},
		HostKey:        "host-1",
		NativeRemote:   "codex+queue://sess-1",
		latestSequence: 1,
		client:         client,
	}
	b = &localBridge{
		root:     t.TempDir(),
		sessions: map[string]*localSession{localBridgeKey("codex", "sess-1"): s},
		changed:  make(chan struct{}),
		options: LocalBridgeOptions{
			// NativeWake is nil, so wake cannot be queued; ReadPosition must still rewind to 0.
			NativeOffer: func(ctx context.Context, remote string, reply LocalBridgeReply) error {
				select {
				case offered <- struct{}{}:
				default:
				}
				return ErrNativeOfferRejected
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		b.nativeLoop(ctx, s, "host-1")
	}()
	// Concurrently mutate s.Attachment and s.NativeRemote under b.mu to verify -race safety.
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			b.mu.Lock()
			s.Attachment.Label = "updated"
			s.NativeRemote = "codex+queue://sess-1"
			b.mu.Unlock()
			time.Sleep(2 * time.Millisecond)
		}
	}()

	select {
	case <-offered:
	case <-time.After(2 * time.Second):
		cancel()
		wg.Wait()
		t.Fatal("nativeLoop did not offer message")
	}

	// Wait briefly for nativeLoop to record the rejection and rewind ReadPosition.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		pos := s.ReadPosition
		lastErr := s.LastError
		b.mu.Unlock()
		if pos == 0 && lastErr != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	wg.Wait()

	b.mu.Lock()
	defer b.mu.Unlock()
	if s.ReadPosition != 0 {
		t.Fatalf("ReadPosition = %d after ErrNativeOfferRejected without wake, want 0", s.ReadPosition)
	}
	if s.LastError == "" {
		t.Fatal("expected LastError to be set after ErrNativeOfferRejected")
	}
}
