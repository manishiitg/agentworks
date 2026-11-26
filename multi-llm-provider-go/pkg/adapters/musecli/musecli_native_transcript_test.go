package musecli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestReadNativeTranscript pins the exported reader mcp-agent-builder-go's
// chat-history resync needs: muse had no equivalent of
// cursorcli.ReadNativeTranscript / picli.ReadNativeTranscript at all, so any
// assistant message not captured by the live streaming path was never
// backfilled from muse's own session.jsonl the way it would be for the
// other four tmux-backed providers (found live 2026-09-11 auditing that
// parity).
func TestReadNativeTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", home)
	sessionID := "01a08ec6-9a6e-77c3-bcbb-ae96565a0f6b"
	dir := filepath.Join(home, "muse", "sessions", "2026", "09", "11", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"payload_type":"runtime.command_intake.received","payload":{"record":{"command_id":"c1","command":{"prompt":"hi"}}}}` + "\n" +
		`{"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"assistant_message_committed","text":"Hi there!"}}}` + "\n"
	logPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if err := os.Chtimes(logPath, past, past); err != nil {
		t.Fatal(err)
	}

	transcript, ok, err := ReadNativeTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadNativeTranscript: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for an existing session log")
	}
	if transcript.Path != logPath {
		t.Fatalf("path = %q, want %q", transcript.Path, logPath)
	}
	if transcript.UpdatedAt.IsZero() {
		t.Fatal("expected a non-zero UpdatedAt")
	}
	if len(transcript.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (human + ai): %+v", len(transcript.Messages), transcript.Messages)
	}
	if transcript.Messages[0].Role != llmtypes.ChatMessageTypeHuman {
		t.Fatalf("messages[0].Role = %v, want Human", transcript.Messages[0].Role)
	}
	if transcript.Messages[1].Role != llmtypes.ChatMessageTypeAI {
		t.Fatalf("messages[1].Role = %v, want AI", transcript.Messages[1].Role)
	}
}

// TestReadNativeTranscriptMissingSession pins the not-found contract: a
// resync caller must be able to tell "no transcript here" from an error, so
// it can leave the persisted chat-history record as-is.
func TestReadNativeTranscriptMissingSession(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	transcript, ok, err := ReadNativeTranscript("no-such-session")
	if err != nil {
		t.Fatalf("expected no error for a missing session, got: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a missing session")
	}
	if transcript.Path != "" || len(transcript.Messages) != 0 {
		t.Fatalf("expected a zero-value transcript, got %+v", transcript)
	}
}
