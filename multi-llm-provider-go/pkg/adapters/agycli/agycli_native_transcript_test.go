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

// agyTestPayload builds a minimal step payload: top-level field 1 = step
// type plus one length-delimited section carrying one string field. Mirrors
// the observed agy 1.2.7 layout (user: 19.2, assistant: 20.1).
func agyTestPayload(stepType int, section, field protowire.Number, text string) []byte {
	var inner []byte
	inner = protowire.AppendTag(inner, field, protowire.BytesType)
	inner = protowire.AppendString(inner, text)
	var outer []byte
	outer = protowire.AppendTag(outer, 1, protowire.VarintType)
	outer = protowire.AppendVarint(outer, uint64(stepType))
	outer = protowire.AppendTag(outer, section, protowire.BytesType)
	outer = protowire.AppendBytes(outer, inner)
	return outer
}

func agyWriteTestDB(t *testing.T, path string, steps []struct {
	stepType int
	payload  []byte
}) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE steps (idx INTEGER PRIMARY KEY, step_type INTEGER NOT NULL DEFAULT 0, step_payload BLOB)`); err != nil {
		t.Fatalf("create steps: %v", err)
	}
	for i, s := range steps {
		if _, err := db.ExecContext(t.Context(), `INSERT INTO steps (idx, step_type, step_payload) VALUES (?, ?, ?)`, i, s.stepType, s.payload); err != nil {
			t.Fatalf("insert step %d: %v", i, err)
		}
	}
}

func agyTestHome(t *testing.T, id string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}
	agyWriteTestDB(t, filepath.Join(dir, id+".db"), []struct {
		stepType int
		payload  []byte
	}{
		{14, agyTestPayload(14, 19, 2, "first question")},
		{15, agyTestPayload(15, 20, 1, "first answer")},
		{132, agyTestPayload(132, 5, 2, "view_file")},
		{17, agyTestPayload(17, 24, 3, "API error")},
		{101, agyTestPayload(101, 114, 1, "system notice")},
		{14, agyTestPayload(14, 19, 2, "second question")},
		{15, agyTestPayload(15, 20, 1, "second answer")},
		{15, []byte{0xff, 0xff, 0xff}},
	})
	return home
}

func TestAgyReadNativeTranscriptRoundTrip(t *testing.T) {
	home := agyTestHome(t, "conv-1")
	got, ok, err := ReadNativeTranscript("conv-1", home)
	if err != nil {
		t.Fatalf("ReadNativeTranscript() error = %v", err)
	}
	if !ok {
		t.Fatal("ReadNativeTranscript() ok = false, want transcript")
	}
	want := []struct {
		role llmtypes.ChatMessageType
		text string
	}{
		{llmtypes.ChatMessageTypeHuman, "first question"},
		{llmtypes.ChatMessageTypeAI, "first answer"},
		{llmtypes.ChatMessageTypeHuman, "second question"},
		{llmtypes.ChatMessageTypeAI, "second answer"},
	}
	if len(got.Messages) != len(want) {
		t.Fatalf("messages = %d, want %d (tool/error/system/corrupt steps must be skipped): %+v", len(got.Messages), len(want), got.Messages)
	}
	for i, w := range want {
		text := ""
		if len(got.Messages[i].Parts) == 1 {
			if part, ok := got.Messages[i].Parts[0].(llmtypes.TextContent); ok {
				text = part.Text
			}
		}
		if got.Messages[i].Role != w.role || text != w.text {
			t.Errorf("message %d = (%q, %q), want (%q, %q)", i, got.Messages[i].Role, text, w.role, w.text)
		}
	}
	if got.Path == "" || got.UpdatedAt.IsZero() {
		t.Errorf("Path/UpdatedAt unset: %+v", got)
	}
}

func TestAgyReadNativeTranscriptMissing(t *testing.T) {
	home := t.TempDir()
	if _, ok, err := ReadNativeTranscript("nope", home); err != nil || ok {
		t.Fatalf("missing id: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if _, ok, err := ReadNativeTranscript("../escape", home); err != nil || ok {
		t.Fatalf("traversal id: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if _, ok, err := ReadNativeTranscript("", home); err != nil || ok {
		t.Fatalf("empty id: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

func TestAgyReadNativeTranscriptIgnoresThinking(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Assistant payload whose ONLY string sits at 20.3 (thinking): the
	// reader must surface nothing, since field 1 (reply) is absent.
	agyWriteTestDB(t, filepath.Join(dir, "think.db"), []struct {
		stepType int
		payload  []byte
	}{{15, agyTestPayload(15, 20, 3, "internal reasoning")}})
	if _, ok, err := ReadNativeTranscript("think", home); err != nil || ok {
		t.Fatalf("thinking-only: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}
