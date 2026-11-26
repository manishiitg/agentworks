package picli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestReadNativeTranscriptKeepsTypedTurnsOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", dir)
	sessionDir := filepath.Join(dir, "--tmp-ws--")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"session","version":3,"id":"mlp-pi-abc","timestamp":"2026-09-02T05:40:37.091Z","cwd":"/tmp/ws"}`,
		`{"type":"message","timestamp":"2026-09-02T05:40:37.250Z","message":{"role":"user","content":[{"type":"text","text":"System: # Builder\n\nyou are the builder"}]}}`,
		`{"type":"message","timestamp":"2026-09-02T05:40:38.000Z","message":{"role":"user","content":[{"type":"text","text":"what is our top strategy"}]}}`,
		`{"type":"message","timestamp":"2026-09-02T05:40:39.666Z","message":{"role":"assistant","content":[{"type":"thinking","text":"hmm"},{"type":"toolCall","id":"c1","name":"read_file"}]}}`,
		`{"type":"message","timestamp":"2026-09-02T05:40:40.000Z","message":{"role":"toolResult","content":[{"type":"text","text":"file body"}]}}`,
		`{"type":"message","timestamp":"2026-09-02T05:40:41.000Z","message":{"role":"assistant","content":[{"type":"text","text":"The top strategy is the builder flywheel."}]}}`,
		`{"type":"model_change","timestamp":"2026-09-02T05:41:00.000Z"}`,
		`{"type":"message","timestamp":"2026-09-02T06:10:00.000Z","message":{"role":"user","content":[{"type":"text","text":"and how many posts"}]}}`,
		`{"type":"message","timestamp":"2026-09-02T06:10:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Twelve so far."}]}}`,
	}
	path := filepath.Join(sessionDir, "2026-09-02T05-40-37-091Z_mlp-pi-abc.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	transcript, ok, err := ReadNativeTranscript("mlp-pi-abc")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if transcript.Path != path {
		t.Fatalf("path = %q, want %q", transcript.Path, path)
	}
	want := []struct {
		role llmtypes.ChatMessageType
		text string
	}{
		{llmtypes.ChatMessageTypeHuman, "what is our top strategy"},
		{llmtypes.ChatMessageTypeAI, "The top strategy is the builder flywheel."},
		{llmtypes.ChatMessageTypeHuman, "and how many posts"},
		{llmtypes.ChatMessageTypeAI, "Twelve so far."},
	}
	if len(transcript.Messages) != len(want) {
		t.Fatalf("got %d messages, want %d: %+v", len(transcript.Messages), len(want), transcript.Messages)
	}
	for i, w := range want {
		got := transcript.Messages[i]
		text := got.Parts[0].(llmtypes.TextContent).Text
		if got.Role != w.role || text != w.text {
			t.Fatalf("message %d = %s %q, want %s %q", i, got.Role, text, w.role, w.text)
		}
	}
	wantTS, _ := time.Parse(time.RFC3339Nano, "2026-09-02T06:10:05.000Z")
	if !transcript.UpdatedAt.Equal(wantTS) {
		t.Fatalf("UpdatedAt = %v, want %v", transcript.UpdatedAt, wantTS)
	}

	if _, ok, err := ReadNativeTranscript("mlp-pi-missing"); ok || err != nil {
		t.Fatalf("missing session: ok=%v err=%v, want false/nil", ok, err)
	}
}

func TestReadNativeTranscriptFromWorkingDirUsesSessionScopedRuntime(t *testing.T) {
	workDir := t.TempDir()
	sessionID := "mlp-pi-scoped-transcript"
	_, sessionDir := piSessionRuntimeDirs(workDir, sessionID)
	leaf := filepath.Join(sessionDir, "--private-workspace--")
	if err := os.MkdirAll(leaf, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(leaf, "2026-09-11T12-00-00-000Z_"+sessionID+".jsonl")
	line := `{"type":"message","timestamp":"2026-09-11T12:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"private reply"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	transcript, ok, err := ReadNativeTranscriptFromWorkingDir(workDir, sessionID)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if transcript.Path != path {
		t.Fatalf("path = %q, want %q", transcript.Path, path)
	}
}
