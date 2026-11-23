package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
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
