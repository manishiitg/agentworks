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

func TestPrepareColdLaunchRemovesStaleMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.marker")
	if err := os.WriteFile(path, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := PrepareColdLaunch(path)
	if marker.Path != path || marker.HadPrior {
		t.Fatalf("PrepareColdLaunch = %+v, want Path=%q HadPrior=false", marker, path)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected stale marker removed, stat err=%v", err)
	}
	if WaitForMCPReadyFileAfter(context.Background(), marker, 250*time.Millisecond) {
		t.Fatal("WaitForMCPReadyFileAfter must not return true before new bridge creates marker")
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = os.WriteFile(path, []byte("ready\n"), 0o600)
	}()
	if !WaitForMCPReadyFileAfter(context.Background(), marker, 2*time.Second) {
		t.Fatal("WaitForMCPReadyFileAfter should return true once new bridge writes marker")
	}
}

func TestWaitForMCPReadyFileAfter_UnremovableStaleMarkerRequiresNewerModTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.marker")
	if err := os.WriteFile(path, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	marker := ColdLaunchMarker{
		Path:         path,
		HadPrior:     true,
		PriorModTime: oldTime,
	}
	if WaitForMCPReadyFileAfter(context.Background(), marker, 250*time.Millisecond) {
		t.Fatal("stale marker with unchanged mtime must not satisfy WaitForMCPReadyFileAfter")
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = os.WriteFile(path, []byte("ready\n"), 0o600)
	}()
	if !WaitForMCPReadyFileAfter(context.Background(), marker, 2*time.Second) {
		t.Fatal("rewritten marker with newer mtime should satisfy WaitForMCPReadyFileAfter")
	}
}
