package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Exercise the real HTTP body: a moving transfer can outlast the idle timeout,
// unknown-length responses still show progress, and a stalled body is canceled.
func TestUpdateDownloadProgressAndIdleTimeout(t *testing.T) {
	payload := bytes.Repeat([]byte("agentworks"), 32<<10)
	for _, knownLength := range []bool{true, false} {
		t.Run(fmt.Sprintf("moving-known-length-%t", knownLength), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if knownLength {
					w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
				}
				for start := 0; start < len(payload); start += len(payload) / 5 {
					_, _ = w.Write(payload[start : start+len(payload)/5])
					w.(http.Flusher).Flush()
					time.Sleep(200 * time.Millisecond)
				}
			}))
			defer server.Close()
			var progress bytes.Buffer
			got, err := downloadUpdate(server.URL, &progress, 600*time.Millisecond)
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("moving download failed: bytes=%d err=%v", len(got), err)
			}
			if !strings.Contains(progress.String(), "Download:") || strings.Contains(progress.String(), "\r") {
				t.Fatalf("missing plain progress: %q", progress.String())
			}
			if knownLength && !strings.Contains(progress.String(), "100%") {
				t.Fatalf("known length did not complete: %q", progress.String())
			}
			if !knownLength && (!strings.Contains(progress.String(), "MB received") || strings.Contains(progress.String(), "%")) {
				t.Fatalf("unknown length reported a guessed percentage: %q", progress.String())
			}
		})
	}
	t.Run("stalled body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "1000000")
			_, _ = w.Write([]byte("partial"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		defer server.Close()
		var progress bytes.Buffer
		got, err := downloadUpdate(server.URL, &progress, 100*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "stopped receiving data") || got != nil {
			t.Fatalf("stalled transfer did not fail cleanly: bytes=%d err=%v", len(got), err)
		}
		if strings.Contains(progress.String(), "100%") {
			t.Fatalf("stalled transfer reported completion: %q", progress.String())
		}
	})
}
