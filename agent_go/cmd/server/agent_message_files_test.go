package server

import "testing"

// A file name from a sender never carries a folder, brackets or control characters
// into the message note, and only a real file_id can name a stored record.
func TestAgentMessageFileNameAndIDAreSafe(t *testing.T) {
	for in, want := range map[string]string{
		"../../etc/passwd":      "passwd",
		`C:\Users\a\report.pdf`: "report.pdf",
		"a[b]<c>.txt":           "abc.txt",
		"line\nbreak.txt":       "linebreak.txt",
		"..":                    "",
		"":                      "",
	} {
		got := agentMessageFileName(in)
		if got != want {
			t.Errorf("agentMessageFileName(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"../x", "file-../../a", "file-ABC", "", "file-" + "0123456789abcdef0123456789abcde"} {
		if agentMessageFileIDPattern.MatchString(bad) {
			t.Errorf("%q must not be accepted as a file id", bad)
		}
	}
	if !agentMessageFileIDPattern.MatchString("file-0123456789abcdef0123456789abcdef") {
		t.Error("a generated id must match")
	}
}

// Only a message from the account that stored a file can name it; quoting a known file id
// in another account's own conversation fetches nothing.
func TestAgentMessageFileIsBoundToItsSender(t *testing.T) {
	id := "file-0123456789abcdef0123456789abcdef"
	c := agentConversation{Endpoints: [2]agentMessageEndpoint{{UserID: "alice"}, {UserID: "bob"}}, Messages: []agentMessage{{From: 0, Text: "see " + id}}}
	if !agentMessageFileSentBy(c, id, "alice") {
		t.Error("the sender's own message must name the file")
	}
	if agentMessageFileSentBy(c, id, "bob") {
		t.Error("a message from someone else must not count")
	}
	c.Messages = []agentMessage{{From: 1, Text: "I quote " + id}}
	if agentMessageFileSentBy(c, id, "alice") {
		t.Error("a quote of the id by another account must not count")
	}
}
