package llmtypes

import (
	"fmt"
	"strings"
	"testing"
)

func TestCompactTerminalPaneForErrorUsesSmallTail(t *testing.T) {
	var pane strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&pane, "line-%02d\n", i)
	}

	got := CompactTerminalPaneForError("tmux-test", pane.String())
	if !strings.Contains(got, "tmux_session=tmux-test") {
		t.Fatalf("missing tmux session in compact error: %q", got)
	}
	if strings.Contains(got, "line-00") {
		t.Fatalf("compact error included old pane head: %q", got)
	}
	if !strings.Contains(got, "line-59") {
		t.Fatalf("compact error missing latest pane tail: %q", got)
	}
	if lines := strings.Count(got, "\n") + 1; lines > terminalErrorTailLines+3 {
		t.Fatalf("compact error has %d lines, want small tail: %q", lines, got)
	}
}
