package utils

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkoukk/tiktoken-go"
)

// countingFailingLoader fails every call and counts how many times it was
// actually invoked, so the test can assert on ATTEMPTS, not just wall time.
type countingFailingLoader struct {
	calls atomic.Int32
	delay time.Duration
}

func (l *countingFailingLoader) LoadTiktokenBpe(string) (map[string]int, error) {
	l.calls.Add(1)
	if l.delay > 0 {
		time.Sleep(l.delay)
	}
	return nil, errors.New("simulated network failure")
}

// The regression this exists for, measured live on 2026-08-19: a real agent
// turn against a real (working) model still failed the test's overall
// timeout, even after the per-call bpe-load timeout fix, because
// getCachedEncoding cached nothing on failure. CountTokensForModel is called
// several times within one turn (once per content string sized: the user
// message, each tool description, each tool result, ...), and each of those
// calls re-attempted the full network load, serialized one after another
// behind encodingCacheMu -- so N calls cost N times the per-call timeout, not
// one.
//
// This resets tiktoken-go's own package-level loader for the duration, since
// getCachedEncoding calls the real tiktoken.GetEncoding -- exercising the
// actual call path this bug lived in, not a reimplementation of it.
func TestGetCachedEncodingBacksOffAfterAFailureInsteadOfRetryingEveryCall(t *testing.T) {
	const encodingName = "cl100k_base" // distinct from other tests' encodings, so caches never collide
	prior := tiktoken.NewDefaultBpeLoader()
	loader := &countingFailingLoader{}
	tiktoken.SetBpeLoader(loader)
	defer tiktoken.SetBpeLoader(prior)
	encodingFailureCache.Delete(encodingName)
	defer encodingFailureCache.Delete(encodingName)

	// Five calls in a row, as a real turn's several CountTokensForModel calls
	// would produce against a persistently-failing network.
	for i := 0; i < 5; i++ {
		if _, err := getCachedEncoding(encodingName); err == nil {
			t.Fatalf("call %d: expected an error from a failing loader, got nil", i)
		}
	}

	if got := loader.calls.Load(); got != 1 {
		t.Fatalf("underlying loader was invoked %d times for 5 calls in the backoff window; want exactly 1 — repeat calls must be served from the failure cache, not retried", got)
	}
}

// The failure must not be permanent: once the backoff window has passed, the
// network is tried again, in case it has recovered.
func TestGetCachedEncodingRetriesAfterTheBackoffWindow(t *testing.T) {
	const encodingName = "p50k_base" // distinct encoding, own cache slot
	prior := tiktoken.NewDefaultBpeLoader()
	loader := &countingFailingLoader{}
	tiktoken.SetBpeLoader(loader)
	defer tiktoken.SetBpeLoader(prior)
	encodingFailureCache.Delete(encodingName)
	defer encodingFailureCache.Delete(encodingName)

	if _, err := getCachedEncoding(encodingName); err == nil {
		t.Fatal("expected an error from a failing loader")
	}
	if got := loader.calls.Load(); got != 1 {
		t.Fatalf("first call: loader invoked %d times, want 1", got)
	}

	// Simulate the window having elapsed by backdating the recorded failure,
	// rather than sleeping encodingFailureBackoff in a test.
	encodingFailureCache.Store(encodingName, time.Now().Add(-encodingFailureBackoff-time.Second))

	if _, err := getCachedEncoding(encodingName); err == nil {
		t.Fatal("expected an error from a still-failing loader")
	}
	if got := loader.calls.Load(); got != 2 {
		t.Fatalf("after the backoff window elapsed: loader invoked %d times, want 2 — a network recovery must be retried, not permanently skipped", got)
	}
}
