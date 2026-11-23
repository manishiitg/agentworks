package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// optsWithReadyFile builds CallOptions carrying the MCP readiness-file path the
// same way WithMCPReadyFile does, so the unexported gate can be exercised directly.
func optsWithReadyFile(path string) *llmtypes.CallOptions {
	o := &llmtypes.CallOptions{}
	ensureMetadata(o)
	if path != "" {
		o.Metadata.Custom[MetadataKeyMCPReadyFile] = path
	}
	return o
}

// TestWaitForMCPReadyFile_NoFileConfigured proves the gate is a no-op (returns
// immediately) when no readiness file is wired — the non-bridge path must pay
// nothing.
func TestWaitForMCPReadyFile_NoFileConfigured(t *testing.T) {
	start := time.Now()
	waitForMCPReadyFile(context.Background(), optsWithReadyFile(""), "sess", nil)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("no-file gate should return immediately, took %s", elapsed)
	}
}

// TestWaitForMCPReadyFile_AlreadyPresent proves that when the handshake already
// completed (marker exists) the gate falls straight through — the cost paid on
// turns 2+ of a persistent session.
func TestWaitForMCPReadyFile_AlreadyPresent(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ready.marker")
	if err := os.WriteFile(marker, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	waitForMCPReadyFile(context.Background(), optsWithReadyFile(marker), "sess", nil)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("present-marker gate should return immediately, took %s", elapsed)
	}
}

// TestWaitForMCPReadyFile_AppearsMidWait proves the gate blocks until the marker
// shows up, then releases — the cold-turn fix: the first prompt is held until the
// bridge signals tools connected.
func TestWaitForMCPReadyFile_AppearsMidWait(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ready.marker")
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = os.WriteFile(marker, []byte("ready\n"), 0o600)
	}()
	start := time.Now()
	waitForMCPReadyFile(context.Background(), optsWithReadyFile(marker), "sess", nil)
	elapsed := time.Since(start)
	if elapsed < 200*time.Millisecond {
		t.Fatalf("gate released before the marker was written (%s) — it did not actually wait", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("gate took too long (%s) after the marker appeared", elapsed)
	}
}

// TestWaitForMCPReadyFile_DegradesOnTimeout proves the gate is best-effort: if the
// marker never appears it returns after the bounded wait rather than hanging the
// turn (today's behavior, minus the guarantee).
func TestWaitForMCPReadyFile_DegradesOnTimeout(t *testing.T) {
	t.Setenv(EnvClaudeMCPReadyWaitSeconds, "1")
	marker := filepath.Join(t.TempDir(), "never.marker")
	start := time.Now()
	waitForMCPReadyFile(context.Background(), optsWithReadyFile(marker), "sess", nil)
	elapsed := time.Since(start)
	if elapsed < 900*time.Millisecond {
		t.Fatalf("gate returned too early (%s); should have waited ~1s before degrading", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("gate exceeded its bound (%s) — timeout not honored", elapsed)
	}
}

// TestWaitForMCPReadyFile_ContextCancel proves a canceled turn unblocks the gate
// promptly instead of waiting out the full timeout.
func TestWaitForMCPReadyFile_ContextCancel(t *testing.T) {
	t.Setenv(EnvClaudeMCPReadyWaitSeconds, "30")
	marker := filepath.Join(t.TempDir(), "never.marker")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	waitForMCPReadyFile(ctx, optsWithReadyFile(marker), "sess", nil)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("gate did not honor context cancellation (%s)", elapsed)
	}
}
