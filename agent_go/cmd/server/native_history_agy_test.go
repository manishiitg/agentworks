package server

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	_ "modernc.org/sqlite"
)

func agyHistoryTestPayload(outer, inner protowire.Number, text string) []byte {
	body := protowire.AppendString(protowire.AppendTag(nil, inner, protowire.BytesType), text)
	return protowire.AppendBytes(protowire.AppendTag(nil, outer, protowire.BytesType), body)
}

// An agy conversation is read as a bounded recent window that grows by only the steps added; a conversation replaced
// by a shorter one starts over; and a window that dropped rows can no longer prove a prompt was never delivered.
func TestAgyHistoryWindowIsIncrementalAndBounded(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "conv-window.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE steps(idx INTEGER PRIMARY KEY, step_type INTEGER, step_payload BLOB, status INTEGER)`); err != nil {
		t.Fatal(err)
	}
	next := 0
	say := func(user bool, text string) {
		stepType, payload := 15, agyHistoryTestPayload(20, 1, text)
		if user {
			stepType, payload = 14, agyHistoryTestPayload(19, 2, text)
		}
		if _, err := db.Exec(`INSERT INTO steps VALUES(?,?,?,3)`, next, stepType, payload); err != nil {
			t.Fatal(err)
		}
		next++
	}
	texts := func() string {
		messages, _, _, ok, err := readRecentAgyTranscript("conv-window", home)
		if err != nil || !ok {
			t.Fatalf("read: ok=%t err=%v", ok, err)
		}
		var out []string
		for _, message := range messages {
			out = append(out, builderConversationMessageText(message))
		}
		return strings.Join(out, "|")
	}
	say(true, "question one")
	say(false, "answer one")
	if got := texts(); got != "question one|answer one" {
		t.Fatalf("first read: %q", got)
	}
	if !agyHistoryComplete(path) {
		t.Fatal("a short conversation read from its start should be complete")
	}
	say(true, "question two")
	if got := texts(); got != "question one|answer one|question two" {
		t.Fatalf("after one more step: %q", got)
	}
	// A longer conversation than the window keeps only the newest rows and no longer proves absence.
	for i := 0; i < nativeHistoryMessages+50; i++ {
		say(i%2 == 0, fmt.Sprintf("m%03d", i))
	}
	got := strings.Split(texts(), "|")
	if len(got) != nativeHistoryMessages || got[len(got)-1] != fmt.Sprintf("m%03d", nativeHistoryMessages+49) || agyHistoryComplete(path) {
		t.Fatalf("window=%d last=%q complete=%t", len(got), got[len(got)-1], agyHistoryComplete(path))
	}
}
