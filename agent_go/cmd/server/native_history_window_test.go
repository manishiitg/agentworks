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

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
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

// PLAT-746-531 / PR1730: a sliding display window must not lose repeated
// terminal inputs or reuse the durable event ID of a different assistant reply.
func TestNativeHistorySlidingWindowKeepsOccurrenceIdentities(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex-cli"} {
		t.Run(provider, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal.jsonl")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			appendRow := func(role, text string) {
				t.Helper()
				var row string
				if provider == "codex-cli" {
					block := "input_text"
					if role == "assistant" {
						block = "output_text"
					}
					row = fmt.Sprintf(`{"timestamp":"2026-11-09T10:00:00Z","type":"response_item","payload":{"type":"message","role":%q,"content":[{"type":%q,"text":%q}]}}`, role, block, text)
				} else {
					row = fmt.Sprintf(`{"timestamp":"2026-11-09T10:00:00Z","type":%q,"message":{"role":%q,"content":%q}}`, role, role, text)
				}
				if _, err := fmt.Fprintln(file, row); err != nil {
					t.Fatal(err)
				}
			}
			defer file.Close()
			for i := 0; i < nativeHistoryMessages/2; i++ {
				appendRow("user", "continue")
				appendRow("assistant", "working")
			}
			before, _, err := readRecentNativeTranscript(path, provider, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			appendRow("user", "continue")
			appendRow("assistant", "working")
			after, _, err := readRecentNativeTranscript(path, provider, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			inputs := nativeTerminalNewUserOccurrences(before, after)
			if len(inputs) != 1 || builderConversationMessageText(inputs[0]) != "continue" {
				t.Fatalf("new input occurrences = %v", inputs)
			}
			if before[2].NativeID == "" || before[2].NativeID != after[0].NativeID {
				t.Fatal("retained occurrence changed identity")
			}
			oldReply := nativeRecoveredMessageID("chat", before[len(before)-1], len(before)-1, false)
			newReply := nativeRecoveredMessageID("chat", after[len(after)-1], len(after)-1, false)
			if oldReply == newReply {
				t.Fatal("two recovered replies share a durable event ID")
			}
			// Eviction/restart must derive the same identities without scanning a prefix.
			cache := nativeHistoryWindow(path, provider)
			cache.mu.Lock()
			cache.info = nil
			cache.mu.Unlock()
			cold, _, err := readRecentNativeTranscript(path, provider, time.Time{})
			if err != nil || cold[len(cold)-1].NativeID != after[len(after)-1].NativeID {
				t.Fatal("cold read changed occurrence identity")
			}
			o := nativeInputObserverFixture(t)
			defer o.api.eventStore.Stop()
			journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			o.api.eventStore.SetDurableJournal(journal)
			if err := o.api.eventStore.SetSessionPersistenceClass(o.snapshot.SessionID, storeevents.SessionPersistenceInteractiveChat); err != nil {
				t.Fatal(err)
			}
			o.provider = provider
			o.consume(before, path, true)
			o.mu.Lock()
			o.nativeInputs = true
			o.mu.Unlock()
			o.consume(after, path, false)
			if counts := nativeTerminalEventUserCounts(o.api.eventStore, o.snapshot.SessionID); counts["continue"] != 1 {
				t.Fatalf("input was not adopted: %v", counts)
			}
			replies := liveAssistantMessageCounts(o.api.eventStore.GetAllEventsRaw(o.snapshot.SessionID))
			if replies["working"] != 1 {
				t.Fatalf("reply was not recovered: %v", replies)
			}
			o.consume(cold, path, false)
			if counts := nativeTerminalEventUserCounts(o.api.eventStore, o.snapshot.SessionID); counts["continue"] != 1 {
				t.Fatal("cold reread duplicated input")
			}
			appendRow("user", "continue")
			appendRow("assistant", "next reply")
			next, _, err := readRecentNativeTranscript(path, provider, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			o.consume(next, path, false)
			replies = liveAssistantMessageCounts(o.api.eventStore.GetAllEventsRaw(o.snapshot.SessionID))
			if replies["working"] != 1 || replies["next reply"] != 1 {
				t.Fatalf("sliding window lost a durable reply: %v", replies)
			}
			if counts := nativeTerminalEventUserCounts(o.api.eventStore, o.snapshot.SessionID); counts["continue"] != 2 {
				t.Fatalf("second repeated input was lost: %v", counts)
			}
			// Reattaching an observer resets its bounded counters, not source identities.
			fresh := &nativeTerminalObserver{api: o.api, snapshot: o.snapshot, provider: provider}
			fresh.consume(next, path, true)
			fresh.mu.Lock()
			fresh.nativeInputs = true
			fresh.mu.Unlock()
			appendRow("user", "continue")
			appendRow("assistant", "reply after reattach")
			reattached, _, err := readRecentNativeTranscript(path, provider, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			fresh.consume(reattached, path, false)
			if counts := nativeTerminalEventUserCounts(o.api.eventStore, o.snapshot.SessionID); counts["continue"] != 3 {
				t.Fatalf("reattached observer reused a user event ID: %v", counts)
			}

		})
	}
}
