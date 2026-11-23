package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/pkg/nativereplies"
)

func TestClaudeForegroundCommitsOnlyIncludedNativeRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const native = "9b6a291d-04e1-41d3-b64a-fb70293db03a"
	dir := filepath.Join(home, ".claude", "projects", "-tmp-project")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, native+".jsonl")
	rows := []string{
		`{"type":"user","uuid":"u","sessionId":"` + native + `","origin":{"kind":"human"},"timestamp":"2026-10-25T10:00:01Z"}`,
		`{"type":"assistant","uuid":"a","parentUuid":"u","timestamp":"2026-10-25T10:00:02Z","message":{"id":"a","stop_reason":"end_turn","content":[{"type":"text","text":"Foreground answer"}]}}`,
		`{"type":"user","uuid":"notice","parentUuid":"a","sessionId":"` + native + `","turnOrigin":"task_notification","origin":{"kind":"task-notification"},"timestamp":"2026-10-25T10:00:03Z","message":{"content":"<task-notification><task-id>task1</task-id></task-notification>"}}`,
		`{"type":"assistant","uuid":"r","parentUuid":"notice","timestamp":"2026-10-25T10:00:04Z","message":{"id":"late","content":[{"type":"text","text":"Helper still working"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := nativereplies.Configure(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer nativereplies.Configure("")
	nativereplies.SetForegroundLookup(nil)
	defer nativereplies.SetForegroundLookup(nil)
	start := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	_ = completedAssistantResponseFromTranscript(native, "", start)
	if nativereplies.State("claude-code", native, "notice").Owner != "" {
		t.Fatal("diagnostic read took foreground ownership")
	}
	nativereplies.SetForegroundLookup(func(string, string) string { return "turn-a" })
	response := completedAssistantResponseFromTranscript(native, "", start)
	if response.Text != "Foreground answer" || len(response.IncludedRoots) != 0 {
		t.Fatalf("incomplete root was included: %+v", response)
	}
	if err := nativereplies.SettleForeground("claude-code", native, "turn-a", response.IncludedRoots); err != nil {
		t.Fatal(err)
	}
	if nativereplies.State("claude-code", native, "notice").Owner != nativereplies.Observer {
		t.Fatal("late root was stranded by foreground completion")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"assistant","uuid":"r-final","parentUuid":"r","timestamp":"2026-10-25T10:00:05Z","message":{"id":"late","stop_reason":"end_turn","content":[{"type":"text","text":"Late helper answer"}]}}` + "\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	response = completedAssistantResponseFromTranscript(native, "", start)
	if strings.Contains(response.TurnText, "Late helper answer") || strings.Contains(response.Text, "Late helper answer") {
		t.Fatal("foreground republished an observer-owned root")
	}
}
