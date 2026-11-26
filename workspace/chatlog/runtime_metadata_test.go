package chatlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-746-876: context settings must not read the message log just for runtime.
func TestRuntimeMetadataDoesNotOpenHistoryLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "builder", "conversation", "chat-conversation.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, []byte(`{"session_id":"chat","runtime":{"provider":"cursor-cli","external_session_id":"native"},"workshop_mode":"coding","conversation_history":[{"Role":"human","Parts":[{"Text":"message content"}]}]}`)); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []bool{false, true} {
		if missing {
			if err := os.Remove(HistoryLogPath(path)); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(HistoryLogPath(path), []byte("not a message log"), 0600); err != nil {
			t.Fatal(err)
		}
		raw, err := RuntimeMetadata(path)
		if err != nil || !strings.Contains(string(raw), `"provider":"cursor-cli"`) || strings.Contains(string(raw), "message content") || strings.Contains(string(raw), "conversation_history") {
			t.Fatalf("metadata read history: %s %v", raw, err)
		}
	}
}
