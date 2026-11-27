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

// ColdLaunchMarker captures the pre-launch state of an MCP readiness marker
// for a fresh CLI process. Call PrepareColdLaunch right before spawning a cold
// CLI process (never on reused warm sessions), then pass the returned value to
// WaitForMCPReadyFileAfter once the CLI prompt is ready.
type ColdLaunchMarker struct {
	Path         string
	HadPrior     bool
	PriorModTime time.Time
}

// PrepareColdLaunch removes any leftover readiness marker from an earlier
// dead/evicted CLI process before a fresh launch starts. If the marker could
// not be unlinked (for example, directory permissions), it records the prior
// mtime so WaitForMCPReadyFileAfter still waits for the new mcpbridge process
// to rewrite it.
func PrepareColdLaunch(readyFile string) ColdLaunchMarker {
	readyFile = strings.TrimSpace(readyFile)
	if readyFile == "" {
		return ColdLaunchMarker{}
	}
	_ = os.Remove(readyFile)
	if info, err := os.Stat(readyFile); err == nil {
		return ColdLaunchMarker{
			Path:         readyFile,
			HadPrior:     true,
			PriorModTime: info.ModTime(),
		}
	}
	return ColdLaunchMarker{Path: readyFile}
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
	return WaitForMCPReadyFileAfter(ctx, ColdLaunchMarker{Path: readyFile}, maxWait)
}

// WaitForMCPReadyFileAfter waits until marker's readiness file is present — and
// if a stale marker could not be unlinked before launch, until its modification
// time advances past PriorModTime. Strictly best-effort, matching
// WaitForMCPReadyFile.
func WaitForMCPReadyFileAfter(ctx context.Context, marker ColdLaunchMarker, maxWait time.Duration) bool {
	readyFile := strings.TrimSpace(marker.Path)
	if readyFile == "" || markerReady(readyFile, marker.HadPrior, marker.PriorModTime) {
		return true
	}
	if maxWait <= 0 {
		maxWait = DefaultMCPReadyWait
	}
	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if markerReady(readyFile, marker.HadPrior, marker.PriorModTime) {
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

func markerReady(path string, hadPrior bool, priorModTime time.Time) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !hadPrior {
		return true
	}
	return info.ModTime().After(priorModTime)
}
