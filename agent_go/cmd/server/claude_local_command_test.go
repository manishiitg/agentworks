package server

import (
	"testing"
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

const localCommandHistoryFixture = `{"session_id":"chat","conversation_history":[
 {"Role":"human","Parts":[{"Text":"Please fix the report"}]},
 {"Role":"ai","Parts":[{"Text":"Fixed the report"}]},
 {"Role":"human","Parts":[{"Text":"<command-name>/exit</command-name><command-message>exit</command-message><command-args></command-args>"}]},
 {"Role":"human","Parts":[{"Text":"<local-command-stdout>See ya!</local-command-stdout>"}]}
 ],"ui_events":[{"id":"preserve-me"}]}`
