// Package codingready holds the shared MCP-readiness gate used by the tmux
// coding-agent adapters (claude/codex/cursor/pi). On a COLD session the input
// prompt becoming ready does NOT mean the MCP servers have finished connecting —
// they load asynchronously, so a first prompt fired too early runs before the
// CLI's tools/list handshake and the model has no tools. The mcpagent bridge
// writes a marker file the instant it answers tools/list; the adapter holds the
// first prompt until that file appears.
package codingready

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// MetadataKeyMCPReadyFile is the CallOptions.Metadata.Custom key carrying the
	// bridge's readiness-marker path. Set by WithMCPReadyFile; read by every tmux
	// adapter. A single shared string so one generic option feeds all providers.
	MetadataKeyMCPReadyFile = "mcp_ready_file"

	// EnvMCPReadyWaitSeconds caps how long a cold session holds its first prompt
	// waiting for the marker. On timeout the adapter proceeds anyway.
	EnvMCPReadyWaitSeconds = "CODING_AGENT_MCP_READY_WAIT_SECONDS"

	// DefaultMCPReadyWait is the fallback cap when the env var is unset.
	DefaultMCPReadyWait = 30 * time.Second
)

// MCPReadyFileFromMetadata extracts the readiness-marker path from CallOptions
// custom metadata, or "" if absent.
func MCPReadyFileFromMetadata(custom map[string]interface{}) string {
	if custom == nil {
		return ""
	}
	s, _ := custom[MetadataKeyMCPReadyFile].(string)
	return strings.TrimSpace(s)
}

// MCPReadyWait resolves the readiness wait cap from EnvMCPReadyWaitSeconds,
// falling back to DefaultMCPReadyWait.
func MCPReadyWait() time.Duration {
	if v := strings.TrimSpace(os.Getenv(EnvMCPReadyWaitSeconds)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return DefaultMCPReadyWait
}

// WaitForMCPReadyFile blocks until readyFile exists, maxWait elapses, or ctx is
// canceled. Strictly best-effort: a no-op that returns true immediately when
// readyFile is empty or already present, and it NEVER errors — callers proceed
// regardless. Returns true if the marker is (or became) present, false if it
// timed out or the context was canceled (the caller then proceeds without the
// tools-connected guarantee, i.e. today's behavior). ONLY call on a freshly
// created (cold) session: the marker path is per-launch, so waiting on a reused
// persistent session would block for a file that launch will never write.
func WaitForMCPReadyFile(ctx context.Context, readyFile string, maxWait time.Duration) bool {
	readyFile = strings.TrimSpace(readyFile)
	if readyFile == "" || fileExists(readyFile) {
		return true
	}
	if maxWait <= 0 {
		maxWait = DefaultMCPReadyWait
	}
	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if fileExists(readyFile) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
