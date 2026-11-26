package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
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
