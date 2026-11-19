package agycli

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestAgyNativeSubagentCompletionRequiresChildAndParentReceipt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	wrap := func(field protowire.Number, inner []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, field, protowire.BytesType), inner)
	}
	refs := append(wrap(10, wrap(1, []byte("child"))), wrap(10, wrap(1, []byte("other-child")))...)
	payload := refs
	for _, field := range []protowire.Number{143, 2, 6, 2, 140} {
		payload = wrap(field, payload)
	}
	ids := agyNativeSubagentIDs(payload)
	if len(ids) != 2 || ids[0] != "child" || ids[1] != "other-child" {
		t.Fatalf("child references = %v", ids)
	}
	if got := agyNativeSubagentIDs(payload[:len(payload)-1]); len(got) != 0 {
		t.Fatalf("malformed references = %v", got)
	}
	// AGY 1.5.12 (#3191 report): the result is an InvokeSubagent Any whose
	// repeated field 2 names each created child; 1.2.14's path found none.
	agy15 := func(ids ...string) []byte {
		value := wrap(1, wrap(2, []byte("Directory Lister")))
		for _, id := range ids {
			value = append(value, wrap(2, append(wrap(1, []byte(id)), wrap(2, []byte("file:///tmp/transcript.jsonl"))...))...)
		}
		result := append(wrap(1, []byte("type.googleapis.com/exa.cortex.core.tools.InvokeSubagent")), wrap(2, value)...)
		return wrap(140, wrap(2, wrap(6, result)))
	}
	if ids := agyNativeSubagentIDs(agy15("child", "other-child")); len(ids) != 2 || ids[0] != "child" || ids[1] != "other-child" {
		t.Fatalf("AGY 1.5 child references = %v", ids)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "parent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(context.Background(), `CREATE TABLE steps (idx INTEGER PRIMARY KEY, step_type INTEGER, status INTEGER, step_payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	// A single launched child, followed by a completed interim assistant.
	payload = agy15("child")
	insert := func(idx, typ int, raw []byte) {
		if _, err := db.ExecContext(context.Background(), `INSERT INTO steps VALUES (?, ?, 3, ?)`, idx, typ, raw); err != nil {
			t.Fatal(err)
		}
	}
	insert(0, agyStepToolCall, payload)
	insert(1, agyStepAssistant, agyTestPayload(15, 20, 1, "waiting"))
	read := func() agyTurnRecord {
		r, err := agyReadTurnRecord("parent", -1, "")
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if !agyPendingNativeSubagents(read(), 0) {
		t.Fatal("missing child accepted as complete")
	}
	child, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "child.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if _, err := child.ExecContext(context.Background(), `CREATE TABLE steps (idx INTEGER PRIMARY KEY, step_type INTEGER, status INTEGER, step_payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := child.ExecContext(context.Background(), `INSERT INTO steps VALUES (0, 15, 3, ?)`, agyTestPayload(15, 20, 1, "done")); err != nil {
		t.Fatal(err)
	}
	if !agyPendingNativeSubagents(read(), 0) {
		t.Fatal("completed child without parent notification accepted")
	}
	insert(2, 101, wrap(114, wrap(4, wrap(3, []byte("child")))))
	insert(3, agyStepAssistant, agyTestPayload(15, 20, 1, "final"))
	if agyPendingNativeSubagents(read(), 0) {
		t.Fatal("completed, notified child still pending")
	}
	if _, err := child.ExecContext(context.Background(), `UPDATE steps SET status = 6`); err != nil {
		t.Fatal(err)
	}
	if !agyPendingNativeSubagents(read(), 0) {
		t.Fatal("interrupted child accepted as complete")
	}
}
