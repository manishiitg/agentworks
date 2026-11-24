package server

import (
	"strings"
	"testing"
)

// Owner, 24 Nov: without a parent purpose the fork note had no closing bracket, so the chat showed it as the
// person's own message.
func TestForkFirstTurnNoteIsAlwaysBracketed(t *testing.T) {
	for _, purpose := range []string{"", "Reviews tools"} {
		note := forkFirstTurnNote("Parent", "Fork", purpose)
		if !strings.HasPrefix(note, "[This chat is a fork.") || !strings.HasSuffix(note, "]") || strings.Count(note, "]") != 1 {
			t.Fatalf("purpose %q: note is not one bracketed block: %q", purpose, note)
		}
		if purpose != "" && !strings.Contains(note, "The parent's job was: "+purpose) {
			t.Fatalf("purpose missing: %q", note)
		}
	}
}
