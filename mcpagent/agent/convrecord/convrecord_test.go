package convrecord

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestWriteFileAtomicNoLeftoverTempFile proves writeFileAtomic leaves exactly
// the target file behind on success — no dangling ".tmp-*" sibling — and that
// the content is exactly what was written.
func TestWriteFileAtomicNoLeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conversation.json")

	if err := writeFileAtomic(path, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	// #nosec G304 - path is a t.TempDir()-scoped test file, not external input.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != `{"a":1}` {
		t.Fatalf("content = %q, want %q", got, `{"a":1}`)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "conversation.json" {
		t.Fatalf("directory has %d entries after write, want exactly [conversation.json]: %v", len(entries), entries)
	}
}

// TestWriteFileAtomicOverwrite proves a second write replaces the first
// atomically — a reader never sees a truncated/empty file, only the old or
// the new content in full (verified indirectly here via the final content
// being exactly the second write; direct torn-write detection needs a
// concurrent reader, which the docstring on writeFileAtomic explains this
// guards against by construction — rename is atomic on this repo's target
// platforms).
func TestWriteFileAtomicOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conversation.json")

	if err := writeFileAtomic(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeFileAtomic(path, []byte("second, and longer than first"), 0o600); err != nil {
		t.Fatalf("second write: %v", err)
	}

	// #nosec G304 - path is a t.TempDir()-scoped test file, not external input.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "second, and longer than first" {
		t.Fatalf("content = %q, want the second write's content", got)
	}
}

// TestFileJSONSinkConcurrentWritesFromOneInstanceDontLoseTurns proves the
// documented, in-scope guarantee: multiple goroutines calling WriteTurn on
// the SAME *FileJSONSink instance never lose a turn (s.mu serializes them).
// This is deliberately NOT a test of the out-of-scope case (two SEPARATE
// sink instances on the same path) — see WriteTurn's doc comment for why
// that's a documented boundary, not a bug this fixes.
func TestFileJSONSinkConcurrentWritesFromOneInstanceDontLoseTurns(t *testing.T) {
	dir := t.TempDir()
	sink := NewFileJSONSink(filepath.Join(dir, "conversation.json"))

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sink.WriteTurn(TurnRecord{Turn: i}); err != nil {
				t.Errorf("WriteTurn(%d): %v", i, err)
			}
		}()
	}
	wg.Wait()

	// #nosec G304 - path is a t.TempDir()-scoped test file, not external input.
	b, err := os.ReadFile(filepath.Join(dir, "conversation.json"))
	if err != nil {
		t.Fatalf("read final file: %v", err)
	}
	var doc fileJSONDocument
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse final file: %v", err)
	}
	if len(doc.Turns) != n {
		t.Fatalf("turns recorded = %d, want %d — a concurrent WriteTurn was lost", len(doc.Turns), n)
	}
}
