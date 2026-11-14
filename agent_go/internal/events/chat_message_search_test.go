package events

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-856: the actual durable SQLite index must page past tool traces and
// streaming deltas without publishing hidden prompt, tool or wrapper text.
func TestChatMessageSearchIndexesOnlyReadableMessages(t *testing.T) {
	j, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	fixture := []string{
		`{"type":"user_message","data":{"data":{"content":"visible question"}}}`,
		`{"type":"user_message","data":{"data":{"content":"private transport envelope","metadata":{"source":"agent_message","display_content":"visible agent question"}}}}`,
		`{"type":"system_prompt","data":{"data":{"content":"hidden system prompt"}}}`,
		`{"type":"tool_call_end","data":{"data":{"content":"hidden tool result"}}}`,
		`{"type":"streaming_chunk","data":{"data":{"content":"unfinished block","source":"transcript","is_delta":false,"metadata":{"assistant_block_id":"block","assistant_block_phase":"delta"}}}}`,
		`{"type":"streaming_chunk","data":{"data":{"content":"visible native answer","source":"transcript","is_delta":false,"metadata":{"assistant_block_id":"block","assistant_block_phase":"end"}}}}`,
		`{"type":"unified_completion","reply_blocks_complete":true,"data":{"data":{"final_result":"duplicate native answer"}}}`,
	}
	for i, raw := range fixture {
		var event Event
		if err = json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		event.ID = fmt.Sprint("event-", i)
		event.Timestamp = time.Now()
		if _, _, err = j.Append("chat", event); err != nil {
			t.Fatal(err)
		}
	}
	page, err := j.ScanChatMessages(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var text []string
	for _, m := range page.Messages {
		text = append(text, m.Text)
	}
	got := strings.Join(text, "\n")
	if len(page.Messages) != 3 || strings.Contains(got, "hidden") || strings.Contains(got, "envelope") || strings.Contains(got, "unfinished") || strings.Contains(got, "duplicate") {
		t.Fatalf("wrong message projection: %s", got)
	}
	for i := 0; i < 130; i++ {
		if _, _, err = j.Append("chat", journalTestEvent(fmt.Sprint("page-", i), "more")); err != nil {
			t.Fatal(err)
		}
	}
	page, err = j.ScanChatMessages(context.Background(), nil)
	if err != nil || len(page.Messages) != 128 || page.Next == nil {
		t.Fatalf("bounded page: %+v %v", page, err)
	}
	next, err := j.ScanChatMessages(context.Background(), page.Next)
	if err != nil || len(next.Messages) != 5 || next.Next != nil {
		t.Fatalf("continuation: count=%d next=%v err=%v", len(next.Messages), next.Next, err)
	}
	selected := page.Messages[0]
	window, err := j.ChatMessageWindow(context.Background(), selected.SessionID, selected.Sequence)
	if err != nil || len(window) == 0 || window[len(window)-1].ID != selected.ID {
		t.Fatalf("message boundary lost: %+v %v", window, err)
	}
	if err = j.DeleteSession("chat"); err != nil {
		t.Fatal(err)
	}
	page, err = j.ScanChatMessages(context.Background(), nil)
	if err != nil || len(page.Messages) != 0 {
		t.Fatal("deleted chat survived search", err)
	}
}

// Run on the build host only. Measure both the one-time index install on a
// synthetic 500 MB store and a bounded lookup; no person's database is opened.
func TestChatMessageSearchSkipsToolStore500MB(t *testing.T) {
	j, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	for _, name := range []string{"structured_chat_message_scan", "structured_chat_message_window"} {
		if _, err = j.db.Exec("DROP INDEX " + name); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := j.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	tool := `{"type":"tool_call_end","data":{"data":{"result":"` + strings.Repeat("x", 500000) + `"}}}`
	for i := 0; i < 1000; i++ {
		if _, err = tx.Exec(`INSERT INTO structured_chat_events(session_id,sequence,event_id,payload,created_at) VALUES('chat',?,?,?,?)`, i+1, fmt.Sprint("tool-", i), tool, fmt.Sprintf("%08d", i)); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(Event{ID: "needle", SessionID: "chat", Sequence: 1001, Type: "user_message", Timestamp: time.Now(), Data: journalTestEvent("needle", "find this message").Data})
	if _, err = tx.Exec(`INSERT INTO structured_chat_events(session_id,sequence,event_id,payload,created_at) VALUES('chat',1001,'needle',?,'99999999')`, raw); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err = installChatMessageIndexes(j); err != nil {
		t.Fatal(err)
	}
	installMS := time.Since(started).Milliseconds()
	started = time.Now()
	page, err := j.ScanChatMessages(context.Background(), nil)
	elapsed := time.Since(started)
	if err != nil || len(page.Messages) != 1 || page.Scanned != 1 || page.Bytes > 65536 {
		t.Fatalf("scan read tool store: %+v %v", page, err)
	}
	rows, err := j.db.Query(`EXPLAIN QUERY PLAN SELECT payload FROM structured_chat_events WHERE ` + chatMessagePredicate + ` ORDER BY created_at DESC,session_id DESC,sequence DESC LIMIT 129`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var a, b, c int
		var detail string
		if err = rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	if !strings.Contains(plan, "structured_chat_message_scan") {
		t.Fatalf("message scan missed index: %s", plan)
	}
	t.Logf("[CHAT_SEARCH_MEASURE] fixture_bytes=%d index_ms=%d scan_us=%d scanned=%d payload_bytes=%d plan=%s", len(tool)*1000, installMS, elapsed.Microseconds(), page.Scanned, page.Bytes, plan)
}
