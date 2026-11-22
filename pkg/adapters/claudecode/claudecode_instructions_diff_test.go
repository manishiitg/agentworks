package claudecode

import (
	"strings"
	"testing"
)

func TestContextBreakdownCountsChangedLines(t *testing.T) {
	var c breakdownChars
	c.addUser(resentInstructionsChangedStart + strings.Repeat("d", 96) + resentInstructionsEnd + "hi")
	if c.resent != len(resentInstructionsChangedStart)+96 || c.resentCount != 1 || c.conversation != len(resentInstructionsEnd)+2 {
		t.Fatalf("got %+v", c)
	}
}
