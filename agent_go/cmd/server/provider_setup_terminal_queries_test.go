package server

import (
	"strings"
	"testing"
)

// PLAT-688: Muse asks its terminal for the cursor position and colours before
// it draws. In a read-only usage run nothing answered, so it printed "cursor
// position could not be read" and the text kept "10;?11;?4;0;?..." fragments.
func TestReadOnlyUsageAnswersAndStripsTerminalQueries(t *testing.T) {
	out := []byte("\x1b]10;?\x07\x1b]11;?\x07\x1b]4;0;?\x07\x1b]4;1;?\x1b\\\x1b[6n\x1b[cUsage: 12% of weekly limit\r\n")
	reply := terminalQueryReplies(out)
	for _, want := range []string{"\x1b]10;rgb:", "\x1b]11;rgb:", "\x1b]4;0;rgb:", "\x1b]4;1;rgb:", "\x1b[1;1R", "\x1b[?62;22c"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("no reply %q in %q", want, reply)
		}
	}
	if got := cleanProviderUsageText(string(out)); got != "Usage: 12% of weekly limit" {
		t.Fatalf("usage text = %q", got)
	}
}
