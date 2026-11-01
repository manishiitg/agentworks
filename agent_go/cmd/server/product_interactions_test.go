package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type fakeInteractionStore struct {
	mu    sync.Mutex
	files map[string]string
	wrote int
}

func (f *fakeInteractionStore) read(_ context.Context, path string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.files[path]
	return c, ok, nil
}

func (f *fakeInteractionStore) write(_ context.Context, path, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = content
	f.wrote++
	return nil
}

func (f *fakeInteractionStore) waitWrites(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		w := f.wrote
		f.mu.Unlock()
		if w >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected %d writes", n)
}

func newTestTracker(store *fakeInteractionStore, now *time.Time) *productInteractionTracker {
	tr := newProductInteractionTracker()
	tr.now = func() time.Time { return *now }
	tr.readFile = store.read
	tr.writeFile = store.write
	return tr
}

func TestProductInteractionTrackerNeverUsedReadsAsIdle(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	tr := newTestTracker(&fakeInteractionStore{files: map[string]string{}}, &now)
	if got := tr.SinceInteractive(context.Background(), "default", "example-app"); got != productInteractionNever {
		t.Fatalf("SinceInteractive = %s, want %s", got, productInteractionNever)
	}
}

func TestProductInteractionTrackerNoteThenSinceAndPersistence(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	store := &fakeInteractionStore{files: map[string]string{}}
	tr := newTestTracker(store, &now)
	ctx := context.Background()

	tr.Note(ctx, "default", "example-app")
	store.waitWrites(t, 1)
	if got := tr.SinceInteractive(ctx, "default", "example-product"); got != productInteractionNever {
		t.Fatal("another product must not inherit this one's activity")
	}

	// A second stamp inside the write interval updates memory but not the file.
	now = now.Add(20 * time.Second)
	tr.Note(ctx, "default", "example-app")
	time.Sleep(20 * time.Millisecond)
	store.mu.Lock()
	wrote := store.wrote
	store.mu.Unlock()
	if wrote != 1 {
		t.Fatalf("expected the second stamp to be rate-limited, got %d writes", wrote)
	}
	if got := tr.SinceInteractive(ctx, "default", "example-app"); got != 0 {
		t.Fatalf("memory must be current even when the file is not, got %s", got)
	}
	now = now.Add(3 * time.Minute)
	if got := tr.SinceInteractive(ctx, "default", "example-app"); got != 3*time.Minute {
		t.Fatalf("SinceInteractive = %s, want 3m", got)
	}

	// A fresh process reads the file back: the family is not idle after a restart.
	var stored map[string]string
	if err := json.Unmarshal([]byte(store.files[productInteractionsPath("default")]), &stored); err != nil {
		t.Fatal(err)
	}
	if stored["example-app"] == "" {
		t.Fatalf("persisted record = %v", stored)
	}
	restarted := newTestTracker(store, &now)
	if got := restarted.SinceInteractive(ctx, "default", "example-app"); got >= productInteractionNever {
		t.Fatalf("after a restart the persisted stamp must count, got %s", got)
	}
}
