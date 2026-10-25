package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeHelperNotificationIsNeverTerminalHumanInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native.jsonl")
	rows := `{"type":"user","timestamp":"2026-10-25T10:00:01Z","origin":{"kind":"human"},"message":{"content":"Real person input"}}
{"type":"user","timestamp":"2026-10-25T10:00:02Z","origin":{"kind":"task-notification"},"turnOrigin":"task_notification","message":{"content":"<task-notification><task-id>helper</task-id><status>completed</status></task-notification>"}}
{"type":"user","timestamp":"2026-10-25T10:00:03Z","origin":{"kind":"task-notification"},"queueTranscriptOnly":true,"message":{"content":"<task-notification><task-id>helper</task-id><status>stopped</status></task-notification>"}}
`
	if err := os.WriteFile(path, []byte(rows), 0600); err != nil {
		t.Fatal(err)
	}
	messages, _, err := readNewClaudeTranscriptMessages(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	users := nativeTerminalUsers(messages)
	if len(users) != 1 || users[0] != "Real person input" {
		t.Fatalf("helper storage row became human input: %v", users)
	}
}
