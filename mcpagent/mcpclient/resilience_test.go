package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/mark3labs/mcp-go/client"
)

func TestIsTransportDeadError(t *testing.T) {
	dead := []error{
		io.EOF,
		fmt.Errorf("request failed: %w", io.EOF),
		syscall.EPIPE,
		syscall.ECONNRESET,
		errors.New("write |1: broken pipe"),
		errors.New("read tcp 127.0.0.1:51000: connection reset by peer"),
		errors.New("use of closed network connection"),
		errors.New("read |0: file already closed"),
		errors.New("transport is closed"),
		errors.New("process already finished"),
	}
	for _, err := range dead {
		if !isTransportDeadError(err) {
			t.Errorf("expected dead-transport classification for %q", err)
		}
	}

	alive := []error{
		nil,
		errors.New("tool not found: fetch_page"),
		errors.New("invalid arguments: missing required field url"),
		errors.New("rate limit exceeded, retry after 30s"),
		context.DeadlineExceeded,
	}
	for _, err := range alive {
		if isTransportDeadError(err) {
			t.Errorf("should NOT classify as dead transport: %v", err)
		}
	}
}

func TestShouldReconnectAfterErrorRespectsContext(t *testing.T) {
	live := context.Background()
	if !shouldReconnectAfterError(live, io.EOF) {
		t.Error("dead transport with live context should reconnect")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if shouldReconnectAfterError(cancelled, io.EOF) {
		t.Error("must not reconnect on behalf of a cancelled caller")
	}
	if shouldReconnectAfterError(live, errors.New("tool execution failed")) {
		t.Error("tool-level errors must not trigger reconnect")
	}
}

func TestReconnectIfStaleSkipsWhenGenerationMoved(t *testing.T) {
	c := New(MCPServerConfig{Command: "true"}, loggerv2.NewNoop())
	// Simulate: caller observed generation 0, another goroutine reconnected
	// (generation now 1). reconnectIfStale must be a no-op (no Connect attempt
	// — Connect on this config would fail, so a nil error proves the skip).
	c.connGen.Add(1)
	if err := c.reconnectIfStale(context.Background(), 0); err != nil {
		t.Errorf("expected skip (nil error) when generation moved, got: %v", err)
	}
}

func TestLeakGuardCountsUnclosedClients(t *testing.T) {
	before := LeakedClientCount()

	// Client with an armed guard, dropped without Close.
	leaky := New(MCPServerConfig{Description: "leak-test"}, loggerv2.NewNoop())
	leaky.mcpClient = &client.Client{} // fake transport; cleanup's Close panic is recovered
	leaky.mu.Lock()
	leaky.armLeakGuardLocked()
	leaky.mu.Unlock()
	leaky = nil
	_ = leaky

	deadline := time.Now().Add(3 * time.Second)
	for LeakedClientCount() == before && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	if LeakedClientCount() != before+1 {
		t.Fatalf("leaked client not detected: count %d, want %d", LeakedClientCount(), before+1)
	}
}

func TestLeakGuardDisarmedByClose(t *testing.T) {
	before := LeakedClientCount()

	closed := New(MCPServerConfig{Description: "closed-test"}, loggerv2.NewNoop())
	closed.mcpClient = &client.Client{}
	closed.mu.Lock()
	closed.armLeakGuardLocked()
	closed.disarmLeakGuardLocked() // what Close() does
	closed.mu.Unlock()
	closed.mcpClient = nil
	closed = nil
	_ = closed

	for i := 0; i < 5; i++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	if got := LeakedClientCount(); got != before {
		t.Errorf("closed client wrongly counted as leak: count %d, want %d", got, before)
	}
}

func TestClientConcurrentCallAndCloseRaceFree(t *testing.T) {
	c := New(MCPServerConfig{Description: "race-test"}, loggerv2.NewNoop())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				_ = c.Ping(context.Background())
				_, _ = c.ListTools(context.Background())
				_, _ = c.CallTool(context.Background(), "noop", nil)
				_ = c.GetServerInfo()
				_ = c.Close()
			}
		}()
	}
	wg.Wait()
}

func TestSSEAndHTTPManagerAbortStartOnCanceledContext(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(block)
		srv.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := NewSSEManager(srv.URL, nil, loggerv2.NewNoop()).Connect(ctx)
	if err == nil {
		t.Fatal("expected SSEManager.Connect to fail when ctx times out")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("SSEManager.Connect took too long to abort on ctx cancel: %s", time.Since(start))
	}

	canceledCtx, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := NewHTTPManager(srv.URL, nil, loggerv2.NewNoop()).Connect(canceledCtx); err == nil {
		t.Fatal("expected HTTPManager.Connect to fail when ctx is already canceled")
	}
}

func TestDiscoverAllToolsParallelRespectsParentCancel(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(block)
		srv.Close()
	}()

	cfg := &MCPConfig{
		MCPServers: map[string]MCPServerConfig{
			"hanging-sse": {URL: srv.URL, Protocol: ProtocolSSE},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	results := DiscoverAllToolsParallel(ctx, cfg, loggerv2.NewNoop())
	if time.Since(start) > 2*time.Second {
		t.Fatalf("DiscoverAllToolsParallel did not abort promptly on parent cancel: %s", time.Since(start))
	}
	if len(results) != 1 || results[0].Error == nil {
		t.Fatalf("expected error result on canceled discovery, got %+v", results)
	}
}
