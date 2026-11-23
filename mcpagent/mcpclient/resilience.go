package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/mark3labs/mcp-go/client"
)

// Mid-session connection resilience.
//
// Retry previously existed only at initial Connect(): a transport that died
// mid-session (server subprocess crashed, SSE stream dropped, socket reset)
// failed every subsequent tool call until the whole agent was torn down.
// CallTool now detects dead-transport errors and performs one serialized
// reconnect + retry. EnsureConnected exposes the same check proactively
// (ping, reconnect if dead) for callers that want to heal before a batch of
// calls.

// reconnectTimeout bounds a mid-session reconnect attempt. Deliberately much
// shorter than RetryConfig.ConnectTimeout (15m, sized for first-time npx
// installs): a mid-session reconnect re-spawns an already-installed server.
const reconnectTimeout = 60 * time.Second

// healthPingTimeout bounds the liveness ping in EnsureConnected.
const healthPingTimeout = 5 * time.Second

// isTransportDeadError reports whether err means the connection to the MCP
// server is gone (as opposed to a tool/protocol-level failure, which must not
// trigger a reconnect).
func isTransportDeadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	msg := err.Error()
	for _, pattern := range []string{
		"broken pipe",
		"connection reset",
		"use of closed network connection",
		"file already closed",
		"transport is closed",
		"connection closed",
		"process already finished",
		"EOF",
	} {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

// shouldReconnectAfterError decides whether a failed call warrants a
// reconnect+retry: the transport must look dead AND the caller's context must
// still be live (a context-cancelled call often surfaces as a transport error
// string — reconnecting on behalf of a cancelled caller would waste a spawn).
func shouldReconnectAfterError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	return isTransportDeadError(err)
}

// connGeneration returns the current connection generation. It increments on
// every successful connect, letting concurrent callers detect that another
// goroutine already reconnected so they skip a redundant reconnect.
func (c *Client) connGeneration() int64 {
	return c.connGen.Load()
}

// reconnectIfStale reconnects unless another goroutine already did (the
// generation moved past observedGen). Serialized so N concurrent failed calls
// produce one reconnect, not N.
func (c *Client) reconnectIfStale(ctx context.Context, observedGen int64) error {
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()

	if c.connGeneration() != observedGen {
		c.logger.Debug("Skipping reconnect — connection already refreshed by another caller",
			loggerv2.String("server", c.getServerName()))
		return nil
	}

	c.logger.Warn("MCP transport dead mid-session, reconnecting",
		loggerv2.String("server", c.getServerName()))

	reconnectCtx, cancel := context.WithTimeout(ctx, reconnectTimeout)
	defer cancel()
	if err := c.Connect(reconnectCtx); err != nil {
		return err
	}
	c.logger.Info("MCP mid-session reconnect succeeded",
		loggerv2.String("server", c.getServerName()))
	return nil
}

// EnsureConnected verifies the connection is alive (connecting first if it
// never was), reconnecting once when the ping fails. Useful before a batch of
// tool calls on a long-lived client.
func (c *Client) EnsureConnected(ctx context.Context) error {
	if c.mcpClient == nil {
		return c.Connect(ctx)
	}
	gen := c.connGeneration()
	pingCtx, cancel := context.WithTimeout(ctx, healthPingTimeout)
	err := c.Ping(pingCtx)
	cancel()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	c.logger.Warn("MCP health ping failed, attempting reconnect",
		loggerv2.String("server", c.getServerName()),
		loggerv2.Error(err))
	return c.reconnectIfStale(ctx, gen)
}

// === Leak detection ===
//
// Every stdio connection owns a subprocess; cleanup depends entirely on the
// consumer calling Close(). A Client dropped without Close leaked its
// subprocess silently. The guard below attaches a GC cleanup to each live
// connection: when an unclosed Client becomes unreachable, it logs loudly,
// closes the underlying transport (killing the subprocess), and bumps a
// counter exposed via LeakedClientCount.

var leakedClients atomic.Int64

// LeakedClientCount returns how many Clients were garbage collected without
// Close() since process start. Nonzero means a consumer is dropping agents or
// clients without closing them.
func LeakedClientCount() int64 {
	return leakedClients.Load()
}

// leakGuardArg carries what the cleanup needs. It must not reference the
// *Client itself (that would keep it reachable and the cleanup would never run).
type leakGuardArg struct {
	serverName string
	mcpClient  *client.Client
	logger     loggerv2.Logger
}

// armLeakGuard attaches a GC cleanup for the just-established connection,
// replacing any guard from a previous connection. Caller must hold c.mu.
func (c *Client) armLeakGuardLocked() {
	if c.leakGuard != nil {
		c.leakGuard.Stop()
		c.leakGuard = nil
	}
	if c.mcpClient == nil {
		return
	}
	arg := leakGuardArg{
		serverName: c.getServerName(),
		mcpClient:  c.mcpClient,
		logger:     c.logger,
	}
	cleanup := runtime.AddCleanup(c, func(a leakGuardArg) {
		// Runs on the GC cleanup goroutine — never let a panic escape.
		defer func() { _ = recover() }()
		leakedClients.Add(1)
		if a.logger != nil {
			a.logger.Error("MCP client leaked — garbage collected without Close(); closing transport to reap subprocess",
				fmt.Errorf("unclosed MCP client for server %q", a.serverName),
				loggerv2.String("server", a.serverName),
				loggerv2.Int("total_leaked", int(leakedClients.Load())))
		}
		_ = a.mcpClient.Close()
	}, arg)
	c.leakGuard = &cleanup
}

// disarmLeakGuard stops the GC cleanup after an explicit Close(). Caller must
// hold c.mu.
func (c *Client) disarmLeakGuardLocked() {
	if c.leakGuard != nil {
		c.leakGuard.Stop()
		c.leakGuard = nil
	}
}
