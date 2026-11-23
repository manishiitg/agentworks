package agycli

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"google.golang.org/protobuf/encoding/protowire"
	_ "modernc.org/sqlite"
)

func agyToolStepPayload(callID, name, args string) []byte {
	var call []byte
	for i, v := range []string{callID, name, args} {
		call = protowire.AppendTag(call, protowire.Number(i+1), protowire.BytesType)
		call = protowire.AppendString(call, v)
	}
	var section []byte
	section = protowire.AppendTag(section, 4, protowire.BytesType)
	section = protowire.AppendBytes(section, call)
	var outer []byte
	outer = protowire.AppendTag(outer, 1, protowire.VarintType)
	outer = protowire.AppendVarint(outer, agyStepToolCall)
	outer = protowire.AppendTag(outer, 5, protowire.BytesType)
	return protowire.AppendBytes(outer, section)
}

// A running turn shows the calls that have finished (a later step follows them), never the
// newest step, which may still be executing.
func TestAgyCompletedToolCallsExcludeTheNewestStep(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "c1.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE steps (idx INTEGER PRIMARY KEY, step_type INTEGER NOT NULL DEFAULT 0, step_payload BLOB, error_details BLOB)`); err != nil {
		t.Fatal(err)
	}
	insert := func(idx, stepType int, payload []byte) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), `INSERT INTO steps (idx, step_type, step_payload) VALUES (?, ?, ?)`, idx, stepType, payload); err != nil {
			t.Fatal(err)
		}
	}
	insert(0, agyStepUser, agyTestPayload(agyStepUser, 19, 2, "q"))
	insert(1, agyStepToolCall, agyToolStepPayload("call_1", "view_file", "{}"))
	insert(2, agyStepToolCall, agyToolStepPayload("call_2", "run_command", "{}"))

	calls := agyCompletedToolCallsSince("c1", 0, home)
	if len(calls) != 1 || calls[0].CallID != "call_1" {
		t.Fatalf("only the finished call is published, got %+v", calls)
	}
	insert(3, agyStepAssistant, agyTestPayload(agyStepAssistant, 20, 1, "done"))
	if calls := agyCompletedToolCallsSince("c1", 0, home); len(calls) != 2 {
		t.Fatalf("both calls are finished once a step follows, got %+v", calls)
	}

	t.Run("headless resume excludes the earlier turn", func(t *testing.T) {
		insert(4, agyStepUser, agyTestPayload(agyStepUser, 19, 2, "next task"))
		insert(5, agyStepToolCall, agyToolStepPayload("call_3", "view_file", `{"path":"output/evidence.json"}`))
		insert(6, agyStepAssistant, agyTestPayload(agyStepAssistant, 20, 1, "verified"))
		chunks := agyExecRecordedToolChunks("c1", 4, home)
		if len(chunks) != 2 || chunks[0].Type != llmtypes.StreamChunkTypeToolCallStart || chunks[1].Type != llmtypes.StreamChunkTypeToolCallEnd || chunks[0].ToolCallID != "call_3" || chunks[1].ToolCallID != "call_3" || chunks[0].ToolArgs != `{"path":"output/evidence.json"}` {
			t.Fatalf("headless tool capture must retain this turn's call only: %+v", chunks)
		}
		if got := agyExecRecordedToolChunks("c1", -1, home); len(got) != 0 {
			t.Fatal("missing intake must not replay all calls")
		}
	})
}
