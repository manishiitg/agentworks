package agycli

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"google.golang.org/protobuf/encoding/protowire"
)

func agyTestStepPayload(outer, inner protowire.Number, text string) []byte {
	body := protowire.AppendString(protowire.AppendTag(nil, inner, protowire.BytesType), text)
	return protowire.AppendBytes(protowire.AppendTag(nil, outer, protowire.BytesType), body)
}

// The history read parses only the newest rows after the last step the caller holds, says when a limit left rows out,
// and reports a replaced conversation by its newest step.
func TestReadNativeTranscriptSinceIsBoundedAndIncremental(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "conv-1.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE steps(idx INTEGER PRIMARY KEY, step_type INTEGER, step_payload BLOB, status INTEGER)`); err != nil {
		t.Fatal(err)
	}
	add := func(idx int, stepType int, payload []byte) {
		if _, err := db.Exec(`INSERT INTO steps VALUES(?,?,?,3)`, idx, stepType, payload); err != nil {
			t.Fatal(err)
		}
	}
	add(0, agyStepUser, agyTestStepPayload(19, 2, "first question"))
	add(1, 132, []byte{}) // a tool step is not conversation
	add(2, agyStepAssistant, agyTestStepPayload(20, 1, "first answer"))
	add(3, agyStepUser, agyTestStepPayload(19, 2, "second question"))
	add(4, agyStepAssistant, agyTestStepPayload(20, 1, "second answer"))
	texts := func(tail NativeTail) string {
		var out []string
		for _, row := range tail.Rows {
			out = append(out, row.Message.Parts[0].(llmtypes.TextContent).Text)
		}
		return strings.Join(out, "|")
	}

	tail, ok, err := ReadNativeTranscriptSince("conv-1", -1, 2, home)
	if err != nil || !ok || texts(tail) != "second question|second answer" || !tail.More || tail.MaxIdx != 4 {
		t.Fatalf("newest two rows of a longer conversation: ok=%t err=%v rows=%q more=%t max=%d", ok, err, texts(tail), tail.More, tail.MaxIdx)
	}
	add(5, agyStepUser, agyTestStepPayload(19, 2, "third question"))
	tail, ok, err = ReadNativeTranscriptSince("conv-1", 4, 10, home)
	if err != nil || !ok || texts(tail) != "third question" || tail.More || tail.MaxIdx != 5 {
		t.Fatalf("only the step after the last held one: ok=%t err=%v rows=%q more=%t max=%d", ok, err, texts(tail), tail.More, tail.MaxIdx)
	}
	if _, ok := StatNativeTranscript("conv-1", home); !ok {
		t.Fatal("stat of an existing conversation")
	}
	if _, ok, _ := ReadNativeTranscriptSince("missing", -1, 10, home); ok {
		t.Fatal("a conversation with no file read as present")
	}
}
