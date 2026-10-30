package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentSnapshotTagDetectsSameSizeAndTimestampEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "_system/agent_messages/conversations.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte(`{"agents":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	router := setupRouter(dir)
	get := func(tag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/documents/_system/agent_messages/conversations.json", nil)
		if tag != "" {
			req.Header.Set("If-None-Match", tag)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	stamp := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	first := get("")
	tag := first.Header().Get("ETag")
	if first.Code != 200 || tag == "" {
		t.Fatalf("initial snapshot %d %s", first.Code, first.Body.String())
	}
	cached := get(tag)
	if cached.Code != 304 || cached.Body.Len() != 0 {
		t.Fatalf("validation %d", cached.Code)
	}
	// Different bytes with the same size and timestamp must not validate old data.
	if err := writeFileAtomic(path, []byte(`{"groups":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	edited := get(tag)
	if edited.Code != 200 || edited.Header().Get("ETag") == tag {
		t.Fatal("external edit reused stale tag")
	}
	// PLAT-856-418: in-place edits also invalidate the byte cache, even when
	// an external writer restores the timestamp and preserves the file size.
	tag = edited.Header().Get("ETag")
	if err := os.WriteFile(path, []byte(`{"agents":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	inPlace := get(tag)
	if inPlace.Code != 200 || inPlace.Header().Get("ETag") == tag {
		t.Fatal("in-place edit reused stale cache")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if missing := get(inPlace.Header().Get("ETag")); missing.Code == 304 {
		t.Fatal("deleted mailbox reused cached authority")
	}
}
