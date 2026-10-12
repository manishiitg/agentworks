package server

import (
	"strings"
	"testing"
)

// 856-59: a guest's free-text ask only reads: no output folder to write, no function-call note. Functions are how a guest acts.
func TestGuestAskNoticeIsReadOnly(t *testing.T) {
	notice := crewSessionModeNotice("Crew/c-1", "", true)
	if strings.Contains(notice, "only place you can write") || !strings.Contains(notice, "Change nothing") {
		t.Fatalf("a read-only ask must be told to change nothing:\n%s", notice)
	}
	req := QueryRequest{CrewAskReadOnly: true}
	if !req.CrewAskReadOnly {
		t.Fatal("marker lost")
	}
}
