package utils

import (
	"errors"
	"testing"
	"time"
)

// fakeSlowLoader simulates the real bug: tiktoken-go's default loader is a
// bare http.Get with no timeout, so a stalled network response blocks it
// forever. This never resolves within the test's lifetime.
type fakeSlowLoader struct{}

func (fakeSlowLoader) LoadTiktokenBpe(string) (map[string]int, error) {
	select {} // block forever, exactly like a stalled http.Get
}

type fakeInstantLoader struct {
	bpe map[string]int
	err error
}

func (l fakeInstantLoader) LoadTiktokenBpe(string) (map[string]int, error) {
	return l.bpe, l.err
}

// The regression this exists for, measured live on 2026-08-19: a bare-agent-
// loop test hung for the entire 5-minute test timeout inside tiktoken-go's
// unbounded vocab download, reached from this package's own
// countTokensWithEncoding on the very first turn. Proves the wrapper actually
// bounds a loader that would otherwise hang forever.
func TestTimeoutBpeLoaderBoundsAStalledLoad(t *testing.T) {
	loader := timeoutBpeLoader{inner: fakeSlowLoader{}}

	done := make(chan error, 1)
	go func() {
		_, err := loader.LoadTiktokenBpe("https://example.invalid/never-responds")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a timeout error, got nil")
		}
		if !errors.Is(err, errBpeLoadTimedOut) {
			t.Fatalf("expected errBpeLoadTimedOut, got: %v", err)
		}
	case <-time.After(bpeLoadTimeout + 5*time.Second):
		t.Fatal("timeoutBpeLoader did not bound the stalled load — this is the exact bug it exists to fix")
	}
}

func TestTimeoutBpeLoaderPassesThroughAFastLoad(t *testing.T) {
	want := map[string]int{"a": 1}
	loader := timeoutBpeLoader{inner: fakeInstantLoader{bpe: want}}

	bpe, err := loader.LoadTiktokenBpe("irrelevant")
	if err != nil {
		t.Fatalf("unexpected error from a fast load: %v", err)
	}
	if len(bpe) != 1 || bpe["a"] != 1 {
		t.Fatalf("got %v, want %v", bpe, want)
	}
}

func TestTimeoutBpeLoaderPassesThroughAFastError(t *testing.T) {
	wantErr := errors.New("boom")
	loader := timeoutBpeLoader{inner: fakeInstantLoader{err: wantErr}}

	_, err := loader.LoadTiktokenBpe("irrelevant")
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want %v", err, wantErr)
	}
}

// errors.Is needs the sentinel to compare equal to itself through the
// bpeLoadTimeoutError type — pinned directly since the type has no custom Is.
func TestBpeLoadTimeoutErrorIsStable(t *testing.T) {
	if !errors.Is(errBpeLoadTimedOut, errBpeLoadTimedOut) {
		t.Fatal("errBpeLoadTimedOut is not stable under errors.Is")
	}
}
