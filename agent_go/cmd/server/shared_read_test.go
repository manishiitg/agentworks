package server

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Overlapping identical list reads share one read and one reply; a later call reads again; another key never shares.
func TestWriteSharedReadSharesOnlyOverlappingCallsWithTheSameKey(t *testing.T) {
	var runs atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	read := func(w http.ResponseWriter, r *http.Request) {
		if runs.Add(1) == 1 {
			close(started)
			<-release
		}
		w.Header().Set("X-Read", "yes")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("list"))
	}
	call := func(key string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		writeSharedRead(w, httptest.NewRequest(http.MethodGet, "/x", nil), key, read)
		return w
	}
	var wg sync.WaitGroup
	replies := make([]*httptest.ResponseRecorder, 4)
	for i := range replies {
		wg.Add(1)
		go func() { defer wg.Done(); replies[i] = call("person-a") }()
	}
	<-started
	time.Sleep(100 * time.Millisecond) // let the other three join the running read
	if w := call("person-b"); w.Code != http.StatusAccepted {
		t.Fatalf("another key waited for or shared the running read: %d", w.Code)
	}
	close(release)
	wg.Wait()
	for _, w := range replies {
		if w.Code != http.StatusAccepted || w.Body.String() != "list" || w.Header().Get("X-Read") != "yes" {
			t.Fatalf("shared reply: %d %q %v", w.Code, w.Body, w.Header())
		}
	}
	if n := runs.Load(); n != 2 {
		t.Fatalf("reads = %d, want 1 shared by person-a's four calls + 1 for person-b", n)
	}
	call("person-a")
	if runs.Load() != 3 {
		t.Fatal("a call after the read finished was served from memory")
	}
}
