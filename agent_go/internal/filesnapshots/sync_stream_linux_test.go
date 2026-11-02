package filesnapshots

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

type largeSyncContext struct{ context.Context }

func (largeSyncContext) CrewSyncTestLimits() crewsync.Limits {
	return crewsync.Limits{Bytes: 2 << 30, FileBytes: 256 << 20, Files: 12000}
}

// PLAT-856-509: exercise the actual private Git capture/import/apply path at
// >1 GiB / >64 MiB per file / >10,000 paths without raising shipping limits.
func TestSyncStreamsLargeProjectWithBoundedMemory(t *testing.T) {
	ctx := largeSyncContext{t.Context()}
	roots := func() crewroot.Roots {
		return crewroot.Roots{Files: t.TempDir(), Resources: t.TempDir(), Private: t.TempDir()}
	}
	a, b := roots(), roots()
	sa, sb := Store{Root: a.Files, Private: a.Private}, Store{Root: b.Files, Private: b.Private}
	stage := t.TempDir()
	if err := agentworksclient.EnsurePrivateDirectory(stage); err != nil {
		t.Fatal(err)
	}
	block := make([]byte, 64<<10)
	if _, err := rand.Read(block); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		f, err := os.Create(filepath.Join(a.Files, fmt.Sprintf("large-%d.bin", i)))
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 2048; j++ {
			if _, err = f.Write(block); err != nil {
				t.Fatal(err)
			}
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10001; i++ {
		if err := os.WriteFile(filepath.Join(a.Files, fmt.Sprintf("small-%05d", i)), []byte(strconv.Itoa(i)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Shipping admission still rejects the same project, and cleans partial staging.
	if snap, err := crewsync.Capture(t.Context(), a, nil, stage); err == nil {
		snap.Close()
		t.Fatal("shipping limits were raised")
	}
	entries, _ := os.ReadDir(stage)
	if len(entries) != 0 {
		t.Fatal("failed capture left private contents")
	}
	runtime.GC()
	var peakHeap, peakRSS atomic.Uint64
	done, stopped := make(chan struct{}), make(chan struct{})
	sample := func() {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		if m.HeapAlloc > peakHeap.Load() {
			peakHeap.Store(m.HeapAlloc)
		}
		rss := syncProcessRSS(os.Getpid(), map[int]bool{})
		if rss > peakRSS.Load() {
			peakRSS.Store(rss)
		}
	}
	go func() {
		defer close(stopped)
		timer := time.NewTicker(20 * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-done:
				sample()
				return
			case <-timer.C:
				sample()
			}
		}
	}()
	defer func() {
		close(done)
		<-stopped
		t.Logf("STREAM_PROOF bytes=%d paths=10009 largest_file=%d peak_go_heap=%d peak_process_tree_rss=%d", int64(1<<30)+40000, 128<<20, peakHeap.Load(), peakRSS.Load())
		if peakHeap.Load() > 192<<20 || peakRSS.Load() > 384<<20 {
			t.Errorf("memory grows with project contents: heap=%d process_tree_rss=%d", peakHeap.Load(), peakRSS.Load())
		}
	}()
	var previous crewsync.Manifest
	roundtrip := func(from, to crewroot.Roots, source, destination Store) {
		t.Helper()
		snap, err := crewsync.Capture(ctx, from, previous, stage)
		if err != nil {
			t.Fatal(err)
		}
		defer snap.Close()
		if len(snap.Files) != 10009 {
			t.Fatalf("paths=%d", len(snap.Files))
		}
		if err = source.EncodeSync(ctx, "large", from, &snap); err != nil {
			t.Fatal(err)
		}
		if err = destination.DecodeSync(ctx, "large", &snap); err != nil {
			t.Fatal(err)
		}
		if err = crewsync.Apply(ctx, to, snap); err != nil {
			t.Fatal(err)
		}
		// Read destination bytes through the same streaming checks, no ReadFile.
		for name, expected := range snap.Files {
			if err = crewsync.CopyContent(ctx, io.Discard, to, name, expected); err != nil {
				t.Fatal(err)
			}
		}
		for _, store := range []Store{source, destination} {
			if err = store.AckSync(ctx, "large", snap.Content); err != nil {
				t.Fatal(err)
			}
		}
		previous = snap.Files
		t.Logf("STREAM_LEG files=%d content_bundle_bytes=%d", len(snap.Files), fileSize(snap.Content.BundlePath))
		_ = os.Remove(snap.Content.BundlePath)
		_ = os.Remove(filepath.Dir(snap.Content.BundlePath))
	}
	roundtrip(a, b, sa, sb)
	for i := 0; i < 8; i++ {
		f, err := os.OpenFile(filepath.Join(b.Files, fmt.Sprintf("large-%d.bin", i)), os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteAt([]byte("return"), 0)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	roundtrip(b, a, sb, sa)
	entries, _ = os.ReadDir(stage)
	if len(entries) != 0 {
		t.Fatal("successful capture left staging files")
	}
}
func fileSize(p string) int64 {
	info, err := os.Stat(p)
	if err != nil {
		return -1
	}
	return info.Size()
}

// Include Git subprocesses: bounding only the Go heap could hide a large pack
// allocation in a child. Linux-only fixture, run on the remote verify host.
func syncProcessRSS(pid int, seen map[int]bool) uint64 {
	if seen[pid] {
		return 0
	}
	seen[pid] = true
	data, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	var rss uint64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) > 1 {
				n, _ := strconv.ParseUint(fields[1], 10, 64)
				rss = n * 1024
			}
		}
	}
	threads, _ := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", pid))
	for _, p := range threads {
		data, _ = os.ReadFile(p)
		for _, v := range strings.Fields(string(data)) {
			child, _ := strconv.Atoi(v)
			rss += syncProcessRSS(child, seen)
		}
	}
	return rss
}
