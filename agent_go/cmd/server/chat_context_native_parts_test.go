package server

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

// #4198: repeated GET /api/chat-context reads must reuse the cached transcript
// breakdown while the JSONL file's size and modTime are unchanged, re-parse when
// the file changes, and keep the cache bounded.
func TestClaudeTranscriptBreakdownCacheHitsInvalidatesAndBounds(t *testing.T) {
	home := t.TempDir()
	workDir := filepath.Join(home, "work")
	slug := claudeNativeTranscriptProjectSlug(workDir)
	projectDir := filepath.Join(home, ".claude", "projects", slug)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession := func(id, body string) string {
		t.Helper()
		p := filepath.Join(projectDir, id+".jsonl")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	p1 := writeSession("sess-1", "{\"turn\":1}\n")
	parses := 0
	cache := &claudeTranscriptBreakdownCache{
		maxEntries: 2,
		parse: func(nativeID, workingDir, accountHome string) (claudecode.ContextBreakdown, bool) {
			parses++
			return claudecode.ContextBreakdown{Total: parses * 100, Setup: 20}, true
		},
	}

	b1, ok := cache.getOrParse("sess-1", workDir, home)
	if !ok || b1.Total != 100 || parses != 1 {
		t.Fatalf("first read: b=%+v ok=%v parses=%d", b1, ok, parses)
	}
	// Unchanged file must hit cache without re-parsing.
	b2, ok := cache.getOrParse("sess-1", workDir, home)
	if !ok || b2.Total != 100 || parses != 1 {
		t.Fatalf("cached read: b=%+v ok=%v parses=%d", b2, ok, parses)
	}

	// Appending to the transcript changes size/mtime and must invalidate the cache.
	if err := os.WriteFile(p1, []byte("{\"turn\":1}\n{\"turn\":2}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(p1, future, future)
	b3, ok := cache.getOrParse("sess-1", workDir, home)
	if !ok || b3.Total != 200 || parses != 2 {
		t.Fatalf("invalidated read: b=%+v ok=%v parses=%d", b3, ok, parses)
	}

	// Adding two more sessions must evict the oldest entry and stay bounded at maxEntries=2.
	writeSession("sess-2", "{\"turn\":1}\n")
	writeSession("sess-3", "{\"turn\":1}\n")
	_, _ = cache.getOrParse("sess-2", workDir, home)
	_, _ = cache.getOrParse("sess-3", workDir, home)
	cache.mu.Lock()
	gotLen := len(cache.entries)
	cache.mu.Unlock()
	if gotLen != 2 {
		t.Fatalf("expected bounded cache size 2, got %d", gotLen)
	}
}

// #4423: when staleWhileRevalidate is enabled, a transcript change returns the
// previous cached breakdown immediately and refreshes in a single background
// goroutine so GET /api/chat-context never stalls on large transcripts.
func TestClaudeTranscriptBreakdownCacheServesStaleWhileRevalidating(t *testing.T) {
	home := t.TempDir()
	workDir := filepath.Join(home, "work")
	slug := claudeNativeTranscriptProjectSlug(workDir)
	projectDir := filepath.Join(home, ".claude", "projects", slug)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p1 := filepath.Join(projectDir, "sess-swr.jsonl")
	if err := os.WriteFile(p1, []byte("{\"turn\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var parses atomic.Int32
	blockRefresh := make(chan struct{})
	refreshStarted := make(chan struct{}, 2)

	cache := &claudeTranscriptBreakdownCache{
		maxEntries:           8,
		staleWhileRevalidate: true,
		parse: func(nativeID, workingDir, accountHome string) (claudecode.ContextBreakdown, bool) {
			n := int(parses.Add(1))
			if n >= 2 {
				select {
				case refreshStarted <- struct{}{}:
				default:
				}
				<-blockRefresh
			}
			return claudecode.ContextBreakdown{Total: n * 100, Setup: 20}, true
		},
	}

	// Cold read parses synchronously.
	b1, ok := cache.getOrParse("sess-swr", workDir, home)
	if !ok || b1.Total != 100 || parses.Load() != 1 {
		t.Fatalf("cold read: b=%+v ok=%v parses=%d", b1, ok, parses.Load())
	}

	// Appending to the transcript triggers a background refresh while returning
	// the stale breakdown immediately without blocking.
	if err := os.WriteFile(p1, []byte("{\"turn\":1}\n{\"turn\":2}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(p1, future, future)

	start := time.Now()
	b2, ok := cache.getOrParse("sess-swr", workDir, home)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("stale read blocked for %v", elapsed)
	}
	if !ok || b2.Total != 100 {
		t.Fatalf("stale read: b=%+v ok=%v, want Total=100", b2, ok)
	}
	// A second concurrent poll while refresh is in flight must deduplicate.
	b3, ok := cache.getOrParse("sess-swr", workDir, home)
	if !ok || b3.Total != 100 {
		t.Fatalf("second stale read: b=%+v ok=%v", b3, ok)
	}

	<-refreshStarted
	close(blockRefresh)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b4, ok := cache.getOrParse("sess-swr", workDir, home)
		if ok && b4.Total == 200 {
			if got := parses.Load(); got != 2 {
				t.Fatalf("parses = %d, want 2 (deduplicated)", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("background refresh did not update cache in time (parses=%d)", parses.Load())
}

func TestClaudeTranscriptBreakdownCacheLargeColdMissRunsAsync(t *testing.T) {
	home := t.TempDir()
	workDir := filepath.Join(home, "work")
	slug := claudeNativeTranscriptProjectSlug(workDir)
	projectDir := filepath.Join(home, ".claude", "projects", slug)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p1 := filepath.Join(projectDir, "sess-large.jsonl")
	if err := os.WriteFile(p1, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}

	var parses atomic.Int32
	blockParse := make(chan struct{})
	parseStarted := make(chan struct{}, 2)

	cache := &claudeTranscriptBreakdownCache{
		maxEntries:           8,
		staleWhileRevalidate: true,
		asyncColdBytes:       1024,
		parse: func(nativeID, workingDir, accountHome string) (claudecode.ContextBreakdown, bool) {
			parses.Add(1)
			select {
			case parseStarted <- struct{}{}:
			default:
			}
			<-blockParse
			return claudecode.ContextBreakdown{Total: 4200, Setup: 100}, true
		},
	}

	start := time.Now()
	_, ok := cache.getOrParse("sess-large", workDir, home)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("large cold miss blocked for %v", elapsed)
	}
	if ok {
		t.Fatal("expected cold async miss to return ok=false immediately")
	}
	// Concurrent second poll must deduplicate.
	_, _ = cache.getOrParse("sess-large", workDir, home)

	<-parseStarted
	close(blockParse)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b, ok := cache.getOrParse("sess-large", workDir, home)
		if ok && b.Total == 4200 {
			if got := parses.Load(); got != 1 {
				t.Fatalf("parses = %d, want 1", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("async cold parse did not populate cache in time (parses=%d)", parses.Load())
}

func TestMuseContextBreakdownCacheSWRServesStaleAndRefreshes(t *testing.T) {
	var reads atomic.Int32
	blockSecond := make(chan struct{})
	secondStarted := make(chan struct{}, 1)

	cache := &museBreakdownCache{
		entries:    map[string]museBreakdownCacheEntry{},
		refreshing: map[string]bool{},
		freshTTL:   20 * time.Millisecond,
		staleTTL:   5 * time.Minute,
		read: func(nativeID, dataHome string) (musecli.ContextBreakdown, bool) {
			n := int(reads.Add(1))
			if n >= 2 {
				select {
				case secondStarted <- struct{}{}:
				default:
				}
				<-blockSecond
			}
			return musecli.ContextBreakdown{Total: n * 1000}, true
		},
	}

	// Cold miss runs asynchronously without blocking.
	_, ok := cache.getOrRead("muse-sess-1", "/tmp/muse-home")
	if ok {
		t.Fatal("expected cold miss ok=false")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b, ok := cache.getOrRead("muse-sess-1", "/tmp/muse-home")
		if ok && b.Total == 1000 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Wait past freshTTL: stale hit returns immediately and refreshes in background.
	time.Sleep(30 * time.Millisecond)
	start := time.Now()
	b2, ok := cache.getOrRead("muse-sess-1", "/tmp/muse-home")
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("stale read blocked for %v", elapsed)
	}
	if !ok || b2.Total != 1000 {
		t.Fatalf("stale read = %+v ok=%v, want Total=1000", b2, ok)
	}
	<-secondStarted
	close(blockSecond)

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b3, ok := cache.getOrRead("muse-sess-1", "/tmp/muse-home")
		if ok && b3.Total == 2000 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("background refresh did not update muse cache (reads=%d)", reads.Load())
}
