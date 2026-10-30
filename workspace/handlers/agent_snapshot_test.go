package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestAgentSnapshotTagDetectsSameSizeAndTimestampEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "_system/agent_messages/conversations.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte(`{"mailbox_storage":"history-v1","agents":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentMailboxHistoryPath(path), []byte(`[]`), 0600); err != nil {
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
	if err := writeFileAtomic(path, []byte(`{"mailbox_storage":"history-v1","groups":[]}`), 0600); err != nil {
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
	if err := os.WriteFile(path, []byte(`{"mailbox_storage":"history-v1","agents":[]}`), 0600); err != nil {
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

// PLAT-856-418: frequent presence updates must preserve cold history in place;
// reads still return the complete logical store and detect external history edits.
func TestAgentMailboxSplitWritesOnlyChangedPart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversations.json")
	first := []byte(`{"agents":[{"name":"alpha"}],"conversations":[{"inbox_id":"inbox-a","messages":[{"message":"hello","delivery":"stored"}]}]}`)
	if err := saveAgentSnapshotFile(path, first); err != nil {
		t.Fatal(err)
	}
	historyPath := agentMailboxHistoryPath(path)
	historyBefore, err := os.Stat(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	second := []byte(`{"agents":[{"name":"sigma"}],"conversations":[{"inbox_id":"inbox-a","messages":[{"message":"hello","delivery":"stored"}]}]}`)
	if err := saveAgentSnapshotFile(path, second); err != nil {
		t.Fatal(err)
	}
	historyAfter, _ := os.Stat(historyPath)
	if !os.SameFile(historyBefore, historyAfter) || historyBefore.ModTime() != historyAfter.ModTime() {
		t.Fatal("presence update rewrote history")
	}
	metadataBefore, _ := os.Stat(path)
	third := []byte(`{"agents":[{"name":"sigma"}],"conversations":[{"inbox_id":"inbox-a","messages":[{"message":"hello","delivery":"delivered"}]}]}`)
	if err := saveAgentSnapshotFile(path, third); err != nil {
		t.Fatal(err)
	}
	metadataAfter, _ := os.Stat(path)
	if !os.SameFile(metadataBefore, metadataAfter) {
		t.Fatal("receipt update rewrote unchanged metadata")
	}
	raw, tag, err := readAgentSnapshotFile(path, metadataAfter)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(third, &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("split API lost logical records")
	}
	// Editing only the history must invalidate a previously assembled cache.
	if err := writeFileAtomic(historyPath, []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	_, changed, err := readAgentSnapshotFile(path, metadataAfter)
	if err != nil || changed == tag {
		t.Fatalf("external history edit remained cached: %v", err)
	}
}
