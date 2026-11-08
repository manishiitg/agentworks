package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func waitForAuthRefresh(t *testing.T, cache *cliAuthProbeCache) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cache.Lock()
		done := !cache.refreshing && time.Since(cache.checkedAt) < cliAuthProbeTTL
		cache.Unlock()
		if done {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background auth refresh did not finish")
}

func withCursorStatusJSON(t *testing.T, output string, err error) {
	t.Helper()
	previous := cursorCLIStatusJSON
	resetCursorCLIAuthProbeCache()
	cursorCLIStatusJSON = func(context.Context) ([]byte, error) {
		return []byte(output), err
	}
	t.Cleanup(func() {
		cursorCLIStatusJSON = previous
		resetCursorCLIAuthProbeCache()
	})
}

func withFakeExecutable(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type recordingAppender struct {
	texts []string
	fail  error
}

func (r *recordingAppender) AddInstructions(sections ...string) error {
	if r.fail != nil {
		return r.fail
	}
	r.texts = append(r.texts, sections...)
	return nil
}

func sectionByName(t *testing.T, name string) promptSection {
	t.Helper()
	for _, section := range promptSections {
		if section.Name == name {
			return section
		}
	}
	t.Fatalf("no prompt section named %q", name)
	return promptSection{}
}

type scheduleRunWorkspaceStub struct {
	mu    sync.Mutex
	files map[string]string
}

func newScheduleRunWorkspaceStub(t *testing.T) (*scheduleRunWorkspaceStub, *httptest.Server) {
	t.Helper()
	stub := &scheduleRunWorkspaceStub{files: make(map[string]string)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/documents/")
		stub.mu.Lock()
		defer stub.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			content, ok := stub.files[path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"content": content},
			})
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var payload struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			stub.files[path] = payload.Content
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	return stub, server
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

func resetCLIAuthProbeCache(cache *cliAuthProbeCache) {
	cache.Lock()
	defer cache.Unlock()
	cache.checkedAt = time.Time{}
	cache.authenticated = false
	cache.conclusive = false
}

func resetCursorCLIAuthProbeCache() {
	resetCLIAuthProbeCache(&cursorCLIAuthProbeCache)
}
