package claudecode

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func instructionLines(n int, change map[int]string) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "rule number " + strings.Repeat("x", i%7) + string(rune('a'+i%26))
		if v, ok := change[i]; ok {
			lines[i] = v
		}
	}
	return strings.Join(lines, "\n")
}

// A kept process gets nothing when it holds the current instructions, the changed lines when they changed, and
// after a compaction the changes since its start again.
func TestClaudeSDKSessionSendsOnlyWhatItLacks(t *testing.T) {
	root := t.TempDir()
	opts := &llmtypes.CallOptions{CLIRuntimeDir: root}
	launch := instructionLines(300, nil)
	changed := instructionLines(300, map[int]string{40: "New rule: deploy only through the deploy agent."})
	s := &claudeSDKSession{}
	s.launchedWith(launch)

	prompt, remember := s.currentInstructionsPrompt(opts, root, "native-id", launch, "hello", true)
	if prompt != "hello" {
		t.Fatalf("unchanged instructions were sent: %q", prompt)
	}
	remember()

	prompt, remember = s.currentInstructionsPrompt(opts, root, "native-id", changed, "next", true)
	if !strings.HasPrefix(prompt, resentInstructionsChangedStart) || !strings.Contains(prompt, "+New rule: deploy only") || strings.Contains(prompt, launch[:200]) || !strings.HasSuffix(prompt, resentInstructionsEnd+"\nnext") {
		t.Fatalf("want only the changed lines, got %q", prompt)
	}
	// A failed turn does not run the receipt, so the retry sends them again.
	if retry, _ := s.currentInstructionsPrompt(opts, root, "native-id", changed, "next", true); retry != prompt {
		t.Fatal("a failed turn consumed the change")
	}
	remember()
	if again, _ := s.currentInstructionsPrompt(opts, root, "native-id", changed, "later", true); again != "later" {
		t.Fatalf("delivered change sent again: %q", again)
	}
	if s.compacted(opts, "native-id", changed) {
		t.Fatal("process started with older instructions reported current after compaction")
	}
	if after, _ := s.currentInstructionsPrompt(opts, root, "native-id", changed, "after", true); !strings.Contains(after, "+New rule: deploy only") {
		t.Fatalf("change lost by compaction not resent: %q", after)
	}

	fresh := &claudeSDKSession{}
	fresh.launchedWith(changed)
	if !fresh.compacted(opts, "native-id", changed) {
		t.Fatal("a process started with the current instructions still holds them after compaction")
	}
	if p, _ := fresh.currentInstructionsPrompt(opts, root, "native-id", changed, "go", true); p != "go" {
		t.Fatalf("compaction resent instructions the system prompt holds: %q", p)
	}
}

func TestContextBreakdownCountsChangedLines(t *testing.T) {
	var c breakdownChars
	c.addUser(resentInstructionsChangedStart + strings.Repeat("d", 96) + resentInstructionsEnd + "hi")
	if c.resent != len(resentInstructionsChangedStart)+96 || c.resentCount != 1 || c.conversation != len(resentInstructionsEnd)+2 {
		t.Fatalf("got %+v", c)
	}
}
