package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeLocalCommandEnvelopeBoundary(t *testing.T) {
	for _, text := range []string{
		"<local-command-stdout>See ya!</local-command-stdout>",
		"<local-command-stderr>Unavailable</local-command-stderr>",
		"<local-command-caveat>Local command output</local-command-caveat>",
		"<command-name>/exit</command-name>\n <command-message>exit</command-message><command-args></command-args>",
	} {
		if !isClaudeLocalCommandMessage("human", text) {
			t.Errorf("command accepted: %s", text)
		}
		if isClaudeLocalCommandMessage("ai", text) {
			t.Error("assistant quotation removed")
		}
	}
	for _, text := range []string{"See ya!", "/exit", "Why is <local-command-stdout>See ya!</local-command-stdout> showing?", "```xml\n<local-command-stdout>See ya!</local-command-stdout>\n```", "<local-command-stdout>example</local-command-stdout> please explain", "<command-name>incomplete"} {
		if isClaudeLocalCommandRecord(text) {
			t.Errorf("real user text removed: %s", text)
		}
	}
}

func TestClaudeTranscriptSkipsLocalCommandsInStringAndBlockContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTranscriptFixture(t, path, []string{
		`{"type":"user","timestamp":"2026-09-06T08:00:00Z","message":{"content":"Please fix the report"}}`,
		`{"type":"assistant","timestamp":"2026-09-06T08:01:00Z","message":{"content":[{"type":"text","text":"Fixed the report"}]}}`,
		`{"type":"user","timestamp":"2026-09-06T08:02:00Z","message":{"content":"<command-name>/exit</command-name><command-message>exit</command-message><command-args></command-args>"}}`,
		`{"type":"user","timestamp":"2026-09-06T08:02:01Z","message":{"content":[{"type":"text","text":"<local-command-stdout>See ya!</local-command-stdout>"}]}}`,
	})
	messages, latest, err := readNewClaudeTranscriptMessages(path, time.Time{})
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if latest.Format(time.RFC3339) != "2026-09-06T08:01:00Z" {
		t.Fatalf("CLI exit advanced conversation time: %v", latest)
	}
}

const localCommandHistoryFixture = `{"session_id":"chat","conversation_history":[
 {"Role":"human","Parts":[{"Text":"Please fix the report"}]},
 {"Role":"ai","Parts":[{"Text":"Fixed the report"}]},
 {"Role":"human","Parts":[{"Text":"<command-name>/exit</command-name><command-message>exit</command-message><command-args></command-args>"}]},
 {"Role":"human","Parts":[{"Text":"<local-command-stdout>See ya!</local-command-stdout>"}]}
 ],"ui_events":[{"id":"preserve-me"}]}`

func TestExistingClaudeCommandHistoryUsesRealQuestionAndTurns(t *testing.T) {
	session, ok := parseLocalChatHistorySession("default", "_users/default/chat_history", "", "chat", localCommandHistoryFixture, time.Now())
	if !ok || session.Query != "Please fix the report" || len(session.PreviewMessages) != 2 {
		t.Fatalf("bad history preview: %+v", session)
	}
	for name, data := range map[string][]byte{
		"full":    filterClaudeLocalCommandHistory([]byte(localCommandHistoryFixture)),
		"preview": trimChatHistoryConversationForPreview([]byte(localCommandHistoryFixture), 2),
		"resume":  projectChatHistoryConversationForResumePage([]byte(localCommandHistoryFixture), 1, 0),
	} {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(doc["conversation_history"]), "See ya!") || !strings.Contains(string(doc["conversation_history"]), "Please fix the report") {
			t.Fatalf("%s did not select real turn: %s", name, data)
		}
	}
}

func TestLocalHistoryIndexRebuildsCommandTitlesWithoutFileChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conversation.json")
	if err := os.WriteFile(path, []byte(localCommandHistoryFixture), 0600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	file := localChatHistoryFile{sessionID: "chat", convPath: path, workspacePath: "chat/conversation.json", size: info.Size(), modTime: info.ModTime()}
	index := newChatHistoryIndex()
	putLocalChatHistoryIndexEntry(&index, file, ChatHistorySession{SessionID: "chat", Query: "<local-command-stdout>See ya!</local-command-stdout>"})
	if _, ok := indexedLocalChatHistorySession(index, file); ok {
		t.Fatal("stale command title served from cache")
	}
	session, ok := readLocalChatHistorySession("default", "chat", "", file)
	if !ok {
		t.Fatal("failed to rebuild preview")
	}
	putLocalChatHistoryIndexEntry(&index, file, session)
	got, ok := indexedLocalChatHistorySession(index, file)
	if !ok || got.Query != "Please fix the report" {
		t.Fatalf("rebuilt title not cached: %+v", got)
	}
}

func TestFullHistoryCommandFilterPreservesMetadataAndSource(t *testing.T) {
	source := []byte(localCommandHistoryFixture)
	clean := filterClaudeLocalCommandHistory(source)
	if !strings.Contains(string(clean), "preserve-me") || strings.Contains(string(clean), "See ya!") {
		t.Fatalf("bad full projection: %s", clean)
	}
	if !strings.Contains(string(source), "See ya!") {
		t.Fatal("source was mutated")
	}
}

// A background task's completion is written by Claude as a user turn; it must not show as a
// message the person sent. Text that merely mentions the tag is still a real message.
func TestClaudeTaskNotificationIsNotAUserMessage(t *testing.T) {
	notice := "<task-notification>\n<task-id>kqblj1dnx</task-id>\n<status>completed</status>\n<result>{}</result>\n</task-notification>"
	if !isClaudeLocalCommandMessage("user", notice) {
		t.Fatal("task notification shown as a user message")
	}
	if isClaudeLocalCommandMessage("assistant", notice) {
		t.Fatal("assistant text is never filtered")
	}
	if isClaudeLocalCommandMessage("user", "why do I see <task-notification> in the chat?") {
		t.Fatal("a real message mentioning the tag was hidden")
	}
}
