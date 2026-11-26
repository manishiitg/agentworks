package codingready

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitForMCPReadyFile_EmptyOrPresentIsImmediate(t *testing.T) {
	start := time.Now()
	if !WaitForMCPReadyFile(context.Background(), "", time.Second) {
		t.Fatal("empty path should return true (nothing to wait for)")
	}
	marker := filepath.Join(t.TempDir(), "ready")
	if err := os.WriteFile(marker, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !WaitForMCPReadyFile(context.Background(), marker, time.Second) {
		t.Fatal("present marker should return true")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("no-op paths should be immediate, took %s", elapsed)
	}
}

func TestWaitForMCPReadyFile_AppearsMidWait(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ready")
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = os.WriteFile(marker, []byte("ready"), 0o600)
	}()
	start := time.Now()
	if !WaitForMCPReadyFile(context.Background(), marker, 3*time.Second) {
		t.Fatal("should return true once the marker appears")
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("returned before the marker was written (%s) — did not actually wait", elapsed)
	}
}

func TestWaitForMCPReadyFile_TimesOutFalse(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "never")
	start := time.Now()
	if WaitForMCPReadyFile(context.Background(), marker, 300*time.Millisecond) {
		t.Fatal("should return false when the marker never appears")
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Fatalf("returned too early (%s); should have waited the full cap", elapsed)
	}
}

func TestWaitForMCPReadyFile_ContextCancel(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "never")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()
	start := time.Now()
	if WaitForMCPReadyFile(ctx, marker, 30*time.Second) {
		t.Fatal("canceled wait should return false")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("did not honor cancellation (%s)", elapsed)
	}
}

func TestMCPReadyFileFromMetadata(t *testing.T) {
	if MCPReadyFileFromMetadata(nil) != "" {
		t.Fatal("nil metadata should yield empty path")
	}
	got := MCPReadyFileFromMetadata(map[string]interface{}{MetadataKeyMCPReadyFile: "  /tmp/x  "})
	if got != "/tmp/x" {
		t.Fatalf("got %q, want trimmed /tmp/x", got)
	}
}
