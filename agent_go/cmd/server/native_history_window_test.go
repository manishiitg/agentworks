package server

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// PLAT-746: importing a months-long journal previously ReadFile'd gigabytes
// before trimming. Exercise HTTP preview on a sparse 128MiB source and append.
func TestNativeHistoryPreviewLargeJournalUsesBoundedIncrementalWindow(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex-cli"} {
		t.Run(provider, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal.jsonl")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.Seek(128<<20, 0); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(f)
			row := func(text, stamp string) string {
				if provider == "codex-cli" {
					return fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}}`+"\n", stamp, text)
				}
				return fmt.Sprintf(`{"timestamp":%q,"type":"user","message":{"content":%q}}`+"\n", stamp, text)
			}
			f.WriteString(row("old", "2026-11-01T10:00:00Z"))
			for i := 0; i < 240; i++ {
				f.WriteString(row(fmt.Sprintf("recent-%d", i), "2026-11-09T10:00:00Z"))
			}
			f.Close()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/?native_id=selected", nil)
			writeLocalProjectPreview(w, r, []localProjectNativeSession{{ID: "selected", Provider: provider, Path: path}})
			runtime.ReadMemStats(&after)
			if w.Code != 200 || len(w.Body.Bytes()) > nativeHistoryTextBytes || strings.Contains(w.Body.String(), `"text":"old"`) || !strings.Contains(w.Body.String(), "recent-239") {
				t.Fatalf("preview not bounded: status=%d bytes=%d", w.Code, w.Body.Len())
			}
			if after.TotalAlloc-before.TotalAlloc > 64<<20 || time.Since(start) > 3*time.Second {
				t.Fatalf("preview loaded full journal: allocation=%d elapsed=%s", after.TotalAlloc-before.TotalAlloc, time.Since(start))
			}
			if nativeHistoryCanProveAbsence(provider, path) {
				t.Fatal("bounded tail was treated as complete delivery evidence")
			}
			cache := nativeHistoryWindow(path, provider)
			beforeOffset := cache.offset
			f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			addition := row("appended", "2026-11-09T10:00:01Z")
			f.WriteString(addition[:len(addition)/2])
			f.Close()
			if _, _, err = readRecentNativeTranscript(path, provider, time.Time{}); err != nil {
				t.Fatal(err)
			}
			if cache.offset != beforeOffset {
				t.Fatal("partial row advanced incremental cursor")
			}
			f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			f.WriteString(addition[len(addition)/2:])
			f.Close()
			messages, _, err := readRecentNativeTranscript(path, provider, time.Time{})
			if err != nil || len(messages) > nativeHistoryMessages || builderConversationMessageText(messages[len(messages)-1]) != "appended" || cache.offset != beforeOffset+int64(len(addition)) {
				t.Fatal("append/partial row recovery lost recent history")
			}
		})
	}
}

// PLAT-746-531: Cursor's full SQLite reader must not create an unbounded UI response.
func TestLocalCursorHistoryResponseIsBounded(t *testing.T) {
	messages := make([]builderConversationMessage, 240)
	for i := range messages {
		messages[i] = builderConversationMessage{Role: "human", Parts: []builderConversationPart{{Text: strings.Repeat("x", 20000)}}}
	}
	messages[len(messages)-1].Parts = []builderConversationPart{{Text: "latest"}}
	got := boundedLocalCursorMessages(messages)
	total := 0
	for _, message := range got {
		text := builderConversationMessageText(message)
		total += len(text)
		if len(text) > nativeHistoryMessageBytes+3 {
			t.Fatal("unbounded Cursor message")
		}
	}
	if len(got) > 60 || total > nativeHistoryTextBytes || builderConversationMessageText(got[len(got)-1]) != "latest" {
		t.Fatal("Cursor history response lost bounds or newest message")
	}
}
